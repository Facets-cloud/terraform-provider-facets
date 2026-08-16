package azure

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// DefaultClientSecretKey is the Secret data key used when secret_key is omitted.
const DefaultClientSecretKey = "client-secret"

// ProviderModel represents the Facets provider configuration
// Note: This duplicates the structure from internal/provider to avoid import cycles
type ProviderModel struct {
	Azure types.Object `tfsdk:"azure"`
}

// ProviderAzureConfig represents Azure configuration from the provider
type ProviderAzureConfig struct {
	SubscriptionID      types.String `tfsdk:"subscription_id"`
	TenantID            types.String `tfsdk:"tenant_id"`
	ClientID            types.String `tfsdk:"client_id"`
	UseWorkloadIdentity types.Bool   `tfsdk:"use_workload_identity"`
	ClientSecretRef     types.Object `tfsdk:"client_secret_ref"`
	Environment         types.String `tfsdk:"environment"`
}

// ProviderAzureClientSecretRefConfig points at an existing Kubernetes Secret
// holding the service principal password. The provider never reads the secret
// value — it only emits a secretKeyRef into the StepAction.
type ProviderAzureClientSecretRefConfig struct {
	SecretName types.String `tfsdk:"secret_name"`
	SecretKey  types.String `tfsdk:"secret_key"`
}

// AzureAuthConfig represents processed Azure authentication configuration.
// Exactly one of UseWorkloadIdentity / ClientSecretRef is set.
type AzureAuthConfig struct {
	SubscriptionID      string
	TenantID            string
	ClientID            string
	Environment         string
	UseWorkloadIdentity bool
	ClientSecretRef     *ClientSecretRef
}

// ClientSecretRef is a reference to a Kubernetes Secret key in the
// tekton-pipelines namespace.
type ClientSecretRef struct {
	SecretName string
	SecretKey  string
}

// GetAzureConfig extracts and validates Azure configuration from provider data.
// Returns the processed Azure auth config or an error if missing/invalid.
//
// Validation rules:
//  1. subscription_id, tenant_id and client_id are required
//  2. exactly one of use_workload_identity / client_secret_ref must be set
//  3. secret_key defaults to "client-secret" when omitted
//
// Authentication flow:
//   - Workload identity: the pod's service account is federated to the Azure AD
//     application. The projected token at AZURE_FEDERATED_TOKEN_FILE is exchanged
//     for an access token. No static credentials exist anywhere.
//   - Client secret ref: the password is read at pod start from an existing
//     Kubernetes Secret via secretKeyRef. The value never enters Terraform state
//     or the StepAction spec.
func GetAzureConfig(ctx context.Context, providerModel *ProviderModel) (*AzureAuthConfig, error) {
	if providerModel == nil {
		return nil, fmt.Errorf("provider model is nil")
	}

	if providerModel.Azure.IsNull() {
		return nil, fmt.Errorf("Azure configuration is required for facets_tekton_action_azure resource. " +
			"Please add an 'azure' block to your provider configuration with subscription_id, tenant_id, client_id " +
			"and either use_workload_identity or client_secret_ref")
	}

	var azureConfig ProviderAzureConfig
	diags := providerModel.Azure.As(ctx, &azureConfig, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, fmt.Errorf("failed to extract Azure configuration: %v", diags.Errors())
	}

	subscriptionID, err := requiredString(azureConfig.SubscriptionID, "subscription_id")
	if err != nil {
		return nil, err
	}
	tenantID, err := requiredString(azureConfig.TenantID, "tenant_id")
	if err != nil {
		return nil, err
	}
	clientID, err := requiredString(azureConfig.ClientID, "client_id")
	if err != nil {
		return nil, err
	}

	// Azure cloud environment: AzureCloud (default), AzureUSGovernment, AzureChinaCloud.
	environment := "AzureCloud"
	if !azureConfig.Environment.IsNull() && azureConfig.Environment.ValueString() != "" {
		environment = azureConfig.Environment.ValueString()
	}

	useWorkloadIdentity := !azureConfig.UseWorkloadIdentity.IsNull() && azureConfig.UseWorkloadIdentity.ValueBool()
	hasSecretRef := !azureConfig.ClientSecretRef.IsNull()

	// Exactly one auth method. Both is ambiguous, neither leaves az login with
	// nothing to present.
	if useWorkloadIdentity && hasSecretRef {
		return nil, fmt.Errorf("use_workload_identity and client_secret_ref are mutually exclusive in the azure block. " +
			"Set use_workload_identity = true for federated credentials, or client_secret_ref for a service principal password")
	}
	if !useWorkloadIdentity && !hasSecretRef {
		return nil, fmt.Errorf("one of use_workload_identity or client_secret_ref is required in the azure block. " +
			"Prefer use_workload_identity = true — it requires no stored secret")
	}

	authConfig := &AzureAuthConfig{
		SubscriptionID:      subscriptionID,
		TenantID:            tenantID,
		ClientID:            clientID,
		Environment:         environment,
		UseWorkloadIdentity: useWorkloadIdentity,
	}

	if hasSecretRef {
		var secretRef ProviderAzureClientSecretRefConfig
		diags = azureConfig.ClientSecretRef.As(ctx, &secretRef, basetypes.ObjectAsOptions{})
		if diags.HasError() {
			return nil, fmt.Errorf("failed to extract client_secret_ref configuration: %v", diags.Errors())
		}

		secretName, err := requiredString(secretRef.SecretName, "client_secret_ref.secret_name")
		if err != nil {
			return nil, err
		}

		secretKey := DefaultClientSecretKey
		if !secretRef.SecretKey.IsNull() && secretRef.SecretKey.ValueString() != "" {
			secretKey = secretRef.SecretKey.ValueString()
		}

		authConfig.ClientSecretRef = &ClientSecretRef{
			SecretName: secretName,
			SecretKey:  secretKey,
		}
	}

	return authConfig, nil
}

func requiredString(v types.String, name string) (string, error) {
	if v.IsNull() || v.ValueString() == "" {
		return "", fmt.Errorf("%s is required in the azure block of the provider configuration", name)
	}
	return v.ValueString(), nil
}
