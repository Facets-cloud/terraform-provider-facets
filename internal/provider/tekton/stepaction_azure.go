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
		"image":  AzureSetupImage,
		"script": GenerateAzureLoginScript(azureConfig),
	}

	// The password is passed through an env var sourced from a Kubernetes Secret
	// rather than baked into the script, so the rendered Task manifest never
	// contains the secret value.
	{
		spec["env"] = []interface{}{
			map[string]interface{}{
				"name": "FACETS_AZURE_CLIENT_SECRET",
				"valueFrom": map[string]interface{}{
					"secretKeyRef": map[string]interface{}{
						"name": azureConfig.SecretName,
						"key":  azureConfig.SecretKey,
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

// The Kubernetes Secret that client-secret mode reads from is created OUT OF
// BAND (e.g. by a k8s_resource module) -- this provider only references it, so
// the value never appears in the rendered Task manifest. Its name and key are
// configurable via the provider block; see azure.DefaultCredentialsSecretName.

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

	b.WriteString(`if [ -z "${FACETS_AZURE_CLIENT_SECRET:-}" ]; then
    echo "ERROR: FACETS_AZURE_CLIENT_SECRET is not set." >&2
    echo "Expected it from the '` + config.SecretName + `' Kubernetes Secret (key: ` + config.SecretKey + `)." >&2
    exit 1
fi

`)
	// Progress output matters here. Every command on the success path is otherwise
	// silent (mkdir, the guard, `az login --output none`, `az account set`), so a
	// working step produced a COMPLETELY EMPTY log -- indistinguishable from a step
	// that never ran. Whoever clicks the action has no other signal, so say what is
	// happening and confirm the identity afterwards.
	b.WriteString(fmt.Sprintf(`echo "Authenticating to Azure as service principal %s"
echo "  tenant:       %s"
echo "  subscription: %s"

az login --service-principal \
  --username %q \
  --password "$FACETS_AZURE_CLIENT_SECRET" \
  --tenant %q \
  --output none

echo "Login succeeded."
`, config.ClientID, config.TenantID, config.SubscriptionID, config.ClientID, config.TenantID))

	b.WriteString(fmt.Sprintf(`
az account set --subscription %q
echo "Active subscription set to %s"

# Echo back what Azure thinks we are, so an authorization failure in a LATER step
# can be told apart from a wrong-identity problem here.
az account show --query "{subscriptionId:id, tenantId:tenantId, identity:user.name, type:user.type}" -o json

echo "Azure CLI profile written to $AZURE_CONFIG_DIR -- later steps inherit it."
`, config.SubscriptionID, config.SubscriptionID))

	return b.String()
}
