package tekton

import (
	"fmt"
	"strings"

	"github.com/facets-cloud/terraform-provider-facets/internal/azure"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// AzureConfigDir is where the Azure CLI profile is written inside the pod. It is
// exported to user steps as AZURE_CONFIG_DIR, mirroring how the AWS variant
// exports AWS_CONFIG_FILE.
const AzureConfigDir = "/workspace/.azure"

// AzureSetupImage is the image used by the credential-setup StepAction.
//
// It deliberately differs from the AWS/Kubernetes variants, which use
// facetscloud/actions-base-image. That image bundles awscli and kubectl but has
// NO az CLI (verified against the published image: Alpine 3.19 with bash, curl,
// jq, python3, awscli, kubectl, yq, git), so `az login` would fail there.
//
// If az is added to facetscloud/actions-base-image in future, this can be
// switched back so all three action types share one image.
const AzureSetupImage = "mcr.microsoft.com/azure-cli:2.61.0"

// AzureFetchImage runs the secret-manager fetch step. It must contain the aws CLI
// and jq; the azure-cli image has jq but no aws (and no boto3), so the shared
// Facets base image is used instead.
const AzureFetchImage = "facetscloud/actions-base-image:v1.0.0"

// AzureLoginStepName is the name of the step that performs `az login` in
// secret-manager mode, injected after the credential fetch.
const AzureLoginStepName = "azure-login"

// GenerateAzureSecretManagerLoginScript renders the az login step that consumes
// the credentials written by the fetch step, then shreds them.
func GenerateAzureSecretManagerLoginScript() string {
	return fmt.Sprintf(`#!/bin/bash
set -e
export AZURE_CONFIG_DIR=%s
CREDS_FILE=%s/creds.json

az login --service-principal \
  --username "$(jq -r .clientId "$CREDS_FILE")" \
  --password "$(jq -r .clientSecret "$CREDS_FILE")" \
  --tenant "$(jq -r .tenantId "$CREDS_FILE")" \
  --output none

az account set --subscription "$(jq -r .subscriptionId "$CREDS_FILE")"

# The credentials are no longer needed; remove them before user steps run.
shred -u "$CREDS_FILE" 2>/dev/null || rm -f "$CREDS_FILE"
`, AzureConfigDir, AzureConfigDir)
}

// BuildAzureStepAction creates a StepAction that authenticates the Azure CLI so
// that subsequent user steps can call `az ...` without handling credentials
// themselves.
//
// The user triggering the action never supplies credentials -- exactly like the
// AWS variant, where IRSA supplies them silently. That is the whole point: a
// one-click action in the Facets UI.
func BuildAzureStepAction(stepActionName, namespace string, labels map[string]interface{}, azureConfig *azure.AzureAuthConfig) (*unstructured.Unstructured, error) {
	if azureConfig == nil {
		return nil, fmt.Errorf("azure config is nil")
	}

	// Secret-manager mode fetches from AWS Secrets Manager and so needs the aws
	// CLI, which lives in the base image; the other modes call az directly.
	image := AzureSetupImage
	if azureConfig.Mode == azure.AuthModeSecretManager {
		image = AzureFetchImage
	}

	spec := map[string]interface{}{
		"image":  image,
		"script": GenerateAzureLoginScript(azureConfig),
	}

	// Client-secret mode passes the secret through an env var sourced from a
	// Kubernetes Secret rather than baking it into the script, so the rendered
	// Task manifest never contains the secret value.
	if azureConfig.Mode == azure.AuthModeClientSecret {
		spec["env"] = []interface{}{
			map[string]interface{}{
				"name": "FACETS_AZURE_CLIENT_SECRET",
				"valueFrom": map[string]interface{}{
					"secretKeyRef": map[string]interface{}{
						"name": AzureCredentialsSecretName,
						"key":  "client_secret",
					},
				},
			},
		}
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tekton.dev/v1beta1",
			"kind":       "StepAction",
			"metadata": map[string]interface{}{
				"name":      stepActionName,
				"namespace": namespace,
				"labels":    labels,
			},
			"spec": spec,
		},
	}, nil
}

// AzureCredentialsSecretName is the Kubernetes Secret the client-secret auth mode
// reads from. It is created out-of-band (by the cloud_account module or a
// bootstrap step) so the secret value never enters Terraform state for the action
// itself, nor the Tekton Task manifest.
const AzureCredentialsSecretName = "facets-azure-credentials"

// GenerateAzureLoginScript renders the credential-setup script.
//
// OIDC federation is preferred: Microsoft Entra ID exchanges the pod's projected
// service-account token for an Azure token, so no secret exists anywhere. This is
// the Azure analogue of the AWS IRSA flow and works cross-cloud, which matters
// when the Tekton pod runs in EKS while the target resources live in Azure.
func GenerateAzureLoginScript(config *azure.AzureAuthConfig) string {
	var b strings.Builder

	b.WriteString("#!/bin/bash\nset -e\n\n")
	b.WriteString(fmt.Sprintf("export AZURE_CONFIG_DIR=%s\n", AzureConfigDir))
	b.WriteString(fmt.Sprintf("mkdir -p %s\n\n", AzureConfigDir))

	if config.Mode == azure.AuthModeSecretManager {
		// Resolve the Azure credentials at run time using the pod's own cloud
		// identity (IRSA on EKS), which the control plane already authorises for
		// secretsmanager:GetSecretValue. Nothing sensitive is stored in Terraform
		// state, in the Task manifest, or in a Kubernetes Secret.
		//
		// This step runs on the base image because it needs the aws CLI; the
		// azure-cli image has az and jq but no aws. The credentials are written to
		// the shared /workspace volume and consumed by the az login step that
		// follows, then shredded.
		// SECRET_ID was resolved at apply time (see azure.DeriveSecretManagerPath),
		// so end users only ever supply a cloud account id -- they never need to
		// know the control plane's internal secret layout. It is baked in here
		// because the ACTION pod, unlike the release pod, has no TF_VAR_CP_NAME.
		b.WriteString(fmt.Sprintf(`SECRET_ID=%q
CREDS_FILE=%s/creds.json

echo "Resolving credentials for cloud account %s"

aws secretsmanager get-secret-value --secret-id "$SECRET_ID" \
  --query SecretString --output text > "$CREDS_FILE"
chmod 600 "$CREDS_FILE"

if [ ! -s "$CREDS_FILE" ]; then
    echo "ERROR: could not read $SECRET_ID from AWS Secrets Manager." >&2
    echo "The pod's service account needs secretsmanager:GetSecretValue." >&2
    exit 1
fi

for k in clientId clientSecret tenantId subscriptionId; do
    if [ -z "$(jq -r --arg k "$k" '.[$k] // empty' "$CREDS_FILE")" ]; then
        echo "ERROR: $k missing from $SECRET_ID." >&2
        exit 1
    fi
done
`, config.SecretManagerPath, AzureConfigDir, config.CloudAccountID))
		return b.String()
	}

	if config.Mode == azure.AuthModeOIDCFederation {
		b.WriteString(fmt.Sprintf("TOKEN_FILE=%q\n", config.FederatedTokenFile))
		b.WriteString(`if [ ! -s "$TOKEN_FILE" ]; then
    echo "ERROR: federated token not found at $TOKEN_FILE." >&2
    echo "The pod needs a projected service-account token with audience api://AzureADTokenExchange." >&2
    exit 1
fi

`)
		b.WriteString(fmt.Sprintf(`az login --service-principal \
  --username %q \
  --tenant %q \
  --federated-token "$(cat "$TOKEN_FILE")" \
  --output none
`, config.ClientID, config.TenantID))
	} else {
		b.WriteString(`if [ -z "${FACETS_AZURE_CLIENT_SECRET:-}" ]; then
    echo "ERROR: FACETS_AZURE_CLIENT_SECRET is not set." >&2
    echo "Expected it from the '` + AzureCredentialsSecretName + `' Kubernetes Secret." >&2
    exit 1
fi

`)
		b.WriteString(fmt.Sprintf(`az login --service-principal \
  --username %q \
  --password "$FACETS_AZURE_CLIENT_SECRET" \
  --tenant %q \
  --output none
`, config.ClientID, config.TenantID))
	}

	b.WriteString(fmt.Sprintf("\naz account set --subscription %q\n", config.SubscriptionID))

	return b.String()
}
