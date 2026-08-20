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

	spec := map[string]interface{}{
		"image":  "facetscloud/actions-base-image:v1.0.0",
		"script": GenerateAzureLoginScript(azureConfig),
	}

	// Client-secret mode passes the secret through an env var sourced from a
	// Kubernetes Secret rather than baking it into the script, so the rendered
	// Task manifest never contains the secret value.
	if !azureConfig.UseOIDCFederation {
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

	if config.UseOIDCFederation {
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
