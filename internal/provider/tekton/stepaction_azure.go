package tekton

import (
	"fmt"

	"github.com/facets-cloud/terraform-provider-facets/internal/azure"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// AzureConfigDir is the shared workspace path holding the az CLI token
	// cache. Written by the setup-credentials step, read by every user step.
	AzureConfigDir = "/workspace/.azure"

	// azureCLIImage is the image used for the credential-setup step. Unlike the
	// AWS variant — which only writes a config file and can use the generic
	// base image — the Azure setup step must actually run `az login`, so it
	// needs the CLI on board.
	azureCLIImage = "mcr.microsoft.com/azure-cli:2.89.1"
)

// BuildAzureStepAction creates a StepAction that logs the workspace in to Azure.
//
// Two auth paths, mirroring the AWS resource's IRSA-only stance as closely as
// Azure allows:
//
//   - Workload identity (preferred): the pod's projected service account token
//     is exchanged for an Azure AD token. No secret exists anywhere — this is
//     the direct analogue of IRSA.
//   - Client secret ref: the SP password is pulled at pod start from an
//     existing Kubernetes Secret via secretKeyRef. The provider never reads the
//     value, so it stays out of Terraform state and out of the StepAction spec.
//
// Note there is deliberately no inline-client-secret path. Putting the password
// in the resource config would land it in plaintext in both the state file and
// the StepAction object in-cluster.
func BuildAzureStepAction(stepActionName, namespace string, labels map[string]interface{}, azureConfig *azure.AzureAuthConfig) (*unstructured.Unstructured, error) {
	if azureConfig == nil {
		return nil, fmt.Errorf("azure auth config is nil")
	}

	env := []interface{}{
		envVar("AZURE_CONFIG_DIR", AzureConfigDir),
		envVar("AZURE_SUBSCRIPTION_ID", azureConfig.SubscriptionID),
		envVar("AZURE_TENANT_ID", azureConfig.TenantID),
		envVar("AZURE_CLIENT_ID", azureConfig.ClientID),
		envVar("AZURE_ENVIRONMENT", azureConfig.Environment),
	}

	if azureConfig.ClientSecretRef != nil {
		env = append(env, envVarFromSecret(
			"AZURE_CLIENT_SECRET",
			azureConfig.ClientSecretRef.SecretName,
			azureConfig.ClientSecretRef.SecretKey,
		))
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
			"spec": map[string]interface{}{
				"image":  azureCLIImage,
				"script": GenerateAzureLoginScript(azureConfig),
				"env":    env,
			},
		},
	}, nil
}

// GenerateAzureLoginScript emits the az login for the configured auth method.
// The resulting token cache lands in AZURE_CONFIG_DIR on the shared workspace,
// which later steps pick up.
func GenerateAzureLoginScript(config *azure.AzureAuthConfig) string {
	if config == nil {
		return ""
	}

	script := `#!/bin/bash
set -e

mkdir -p "${AZURE_CONFIG_DIR}"
chmod 700 "${AZURE_CONFIG_DIR}"

az cloud set --name "${AZURE_ENVIRONMENT}"
`

	if config.UseWorkloadIdentity {
		// AZURE_FEDERATED_TOKEN_FILE is projected by the Azure Workload Identity
		// webhook onto every container in a labelled pod.
		script += `
if [ -z "${AZURE_FEDERATED_TOKEN_FILE}" ] || [ ! -f "${AZURE_FEDERATED_TOKEN_FILE}" ]; then
    echo "ERROR: AZURE_FEDERATED_TOKEN_FILE is not set or not readable." >&2
    echo "The Azure Workload Identity webhook must be installed, and the TaskRun's" >&2
    echo "service account must carry the azure.workload.identity/use=true label" >&2
    echo "and an azure.workload.identity/client-id annotation." >&2
    exit 1
fi

az login \
    --service-principal \
    --username "${AZURE_CLIENT_ID}" \
    --tenant "${AZURE_TENANT_ID}" \
    --federated-token "$(cat "${AZURE_FEDERATED_TOKEN_FILE}")" \
    --output none
`
	} else {
		script += `
if [ -z "${AZURE_CLIENT_SECRET}" ]; then
    echo "ERROR: AZURE_CLIENT_SECRET is empty. Check that the referenced Secret" >&2
    echo "exists in the tekton-pipelines namespace and contains the expected key." >&2
    exit 1
fi

az login \
    --service-principal \
    --username "${AZURE_CLIENT_ID}" \
    --password "${AZURE_CLIENT_SECRET}" \
    --tenant "${AZURE_TENANT_ID}" \
    --output none
`
	}

	script += `
az account set --subscription "${AZURE_SUBSCRIPTION_ID}"
echo "Authenticated to Azure subscription ${AZURE_SUBSCRIPTION_ID}"
`

	return script
}

func envVar(name, value string) map[string]interface{} {
	return map[string]interface{}{
		"name":  name,
		"value": value,
	}
}

func envVarFromSecret(name, secretName, secretKey string) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
		"valueFrom": map[string]interface{}{
			"secretKeyRef": map[string]interface{}{
				"name": secretName,
				"key":  secretKey,
			},
		},
	}
}
