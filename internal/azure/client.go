package azure

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// ProviderModel represents the Facets provider configuration.
// Note: this duplicates the structure from internal/provider to avoid import cycles
// (same pattern as internal/aws).
type ProviderModel struct {
	Azure types.Object `tfsdk:"azure"`
}

// ProviderAzureConfig represents Azure configuration from the provider block.
type ProviderAzureConfig struct {
	SubscriptionID     types.String `tfsdk:"subscription_id"`
	TenantID           types.String `tfsdk:"tenant_id"`
	ClientID           types.String `tfsdk:"client_id"`
	ClientSecret       types.String `tfsdk:"client_secret"`
	UseOIDCFederation  types.Bool   `tfsdk:"use_oidc_federation"`
	FederatedTokenFile types.String `tfsdk:"federated_token_file"`
}

// AzureAuthConfig represents processed Azure authentication configuration.
//
// Two authentication modes are supported, mirroring how the AWS variant offers
// IRSA-based role assumption:
//
//  1. OIDC federation (preferred): the pod presents a projected service-account
//     token to Microsoft Entra ID, which exchanges it for an Azure token. No
//     client secret exists anywhere -- this is the Azure equivalent of IRSA and
//     works cross-cloud (an EKS-hosted pod can authenticate to Azure).
//  2. Client secret: a service-principal password supplied via provider config.
//     Simpler to set up but the secret is persisted in Terraform state, so mode 1
//     should be preferred wherever the federated credential can be registered.
type AzureAuthConfig struct {
	SubscriptionID string
	TenantID       string
	ClientID       string

	// ClientSecret is empty when UseOIDCFederation is true.
	ClientSecret string

	// UseOIDCFederation selects federated-token auth over client-secret auth.
	UseOIDCFederation bool

	// FederatedTokenFile is the in-pod path of the projected service-account
	// token. Only meaningful when UseOIDCFederation is true.
	FederatedTokenFile string
}

// DefaultFederatedTokenFile is where the projected service-account token is
// expected when none is configured. The token must be projected with audience
// "api://AzureADTokenExchange" -- reusing the default service-account token
// fails with AADSTS700212 (audience mismatch).
const DefaultFederatedTokenFile = "/var/run/secrets/azure/tokens/azure-identity-token"

// GetAzureConfig extracts and validates Azure configuration from provider data.
//
// Validation rules:
//  1. subscription_id, tenant_id and client_id are always required.
//  2. Exactly one of client_secret / use_oidc_federation must be supplied --
//     specifying both is ambiguous and specifying neither leaves no way to
//     authenticate.
func GetAzureConfig(ctx context.Context, providerModel *ProviderModel) (*AzureAuthConfig, error) {
	if providerModel == nil {
		return nil, fmt.Errorf("provider model is nil")
	}

	if providerModel.Azure.IsNull() {
		return nil, fmt.Errorf("Azure configuration is required for facets_tekton_action_azure resource. " +
			"Please add an 'azure' block to your provider configuration with subscription_id, tenant_id, " +
			"client_id and either client_secret or use_oidc_federation")
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

	useOIDC := !azureConfig.UseOIDCFederation.IsNull() && azureConfig.UseOIDCFederation.ValueBool()

	clientSecret := ""
	if !azureConfig.ClientSecret.IsNull() {
		clientSecret = azureConfig.ClientSecret.ValueString()
	}

	if useOIDC && clientSecret != "" {
		return nil, fmt.Errorf("client_secret must not be set when use_oidc_federation is true; " +
			"OIDC federation exchanges a projected service-account token and needs no secret")
	}
	if !useOIDC && clientSecret == "" {
		return nil, fmt.Errorf("either client_secret or use_oidc_federation must be set in the azure block")
	}

	tokenFile := DefaultFederatedTokenFile
	if !azureConfig.FederatedTokenFile.IsNull() && azureConfig.FederatedTokenFile.ValueString() != "" {
		tokenFile = azureConfig.FederatedTokenFile.ValueString()
	}

	return &AzureAuthConfig{
		SubscriptionID:     subscriptionID,
		TenantID:           tenantID,
		ClientID:           clientID,
		ClientSecret:       clientSecret,
		UseOIDCFederation:  useOIDC,
		FederatedTokenFile: tokenFile,
	}, nil
}

func requiredString(v types.String, name string) (string, error) {
	if v.IsNull() || v.ValueString() == "" {
		return "", fmt.Errorf("%s is required in the azure block of the provider configuration", name)
	}
	return v.ValueString(), nil
}
