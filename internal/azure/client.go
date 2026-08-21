package azure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

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
	CloudAccountID     types.String `tfsdk:"cloud_account_id"`
	SecretManagerPath  types.String `tfsdk:"secret_manager_path"`
	SecretName         types.String `tfsdk:"secret_name"`
	SecretKey          types.String `tfsdk:"secret_key"`
}

// AuthMode identifies how the action pod obtains Azure credentials.
type AuthMode int

const (
	// AuthModeSecretManager resolves the credentials at runtime from the
	// control plane's secret store using the pod's own cloud identity. Nothing
	// sensitive is stored in Terraform state, in the Task manifest, or in a
	// Kubernetes Secret.
	AuthModeSecretManager AuthMode = iota

	// AuthModeOIDCFederation exchanges a projected service-account token for an
	// Azure token. No secret exists at all, but the pod must be given a token
	// whose audience is api://AzureADTokenExchange.
	AuthModeOIDCFederation

	// AuthModeClientSecret reads the secret from a pre-existing Kubernetes
	// Secret via secretKeyRef.
	AuthModeClientSecret
)

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
//     Provider configuration is not persisted in Terraform state, and the password
//     reaches the pod through a Secret via secretKeyRef rather than being inlined
//     in the StepAction. Mode 1 is still preferable where the federated credential
//     can be registered, since it removes the standing secret altogether.
type AzureAuthConfig struct {
	Mode AuthMode

	// Identity fields. In secret-manager mode these are resolved at runtime and
	// are therefore empty here.
	SubscriptionID string
	TenantID       string
	ClientID       string

	// ClientSecret is only populated in client-secret mode.
	ClientSecret string

	// UseOIDCFederation selects federated-token auth over client-secret auth.
	UseOIDCFederation bool

	// FederatedTokenFile is the in-pod path of the projected service-account
	// token. Only meaningful in OIDC federation mode.
	FederatedTokenFile string

	// CloudAccountID identifies the Facets-linked cloud account whose
	// credentials the action should resolve at runtime. Secret-manager mode only.
	CloudAccountID string

	// SecretManagerPath is the secret id to read, e.g.
	// "<cluster>/backend/accounts/<cloud_account_id>".
	SecretManagerPath string

	// SecretName is the Kubernetes Secret the client-secret mode reads from, and
	// SecretKey the key within it. The Secret is created out of band (e.g. by a
	// k8s_resource module) -- this provider only references it.
	//
	// Because Tekton actions share one namespace across every project on a
	// control plane, leaving this at the default risks two projects with
	// different Azure tenants colliding on the same Secret. Set it per project.
	SecretName string
	SecretKey  string
}

// DefaultFederatedTokenFile is where the projected service-account token is
// expected when none is configured. The token must be projected with audience
// "api://AzureADTokenExchange" -- reusing the default service-account token
// fails with AADSTS700212 (audience mismatch).
const DefaultFederatedTokenFile = "/var/run/secrets/azure/tokens/azure-identity-token"

// Naming for the Kubernetes Secret that client-secret mode reads from.
const (
	// CredentialsSecretPrefix prefixes the derived Secret name.
	CredentialsSecretPrefix = "facets-azure-creds"

	// DefaultCredentialsSecretKey is the key within that Secret.
	DefaultCredentialsSecretKey = "client_secret"
)

// DeriveSecretName names the Secret holding a service principal's password from
// the IDENTITY of that principal.
//
// The point is that nobody has to be told the name. The Secret is created by
// whoever operates the control plane, while the module referencing it is
// configured by the customer -- and the customer cannot see the control plane's
// namespace to look the name up. Deriving it from tenant + client +
// subscription, which BOTH sides already hold, removes that coordination
// entirely: each computes the same name independently.
//
// It also fixes multi-account control planes. Two projects using the same service
// principal derive one name and share one Secret (correct: one credential, one
// rotation); two projects using different principals derive different names, so
// neither can overwrite the other's credentials. A single fixed name would
// silently hand the second project's credentials to the first.
func DeriveSecretName(tenantID, clientID, subscriptionID string) string {
	sum := sha256.Sum256([]byte(tenantID + "|" + clientID + "|" + subscriptionID))
	return CredentialsSecretPrefix + "-" + hex.EncodeToString(sum[:])[:16]
}

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

	useOIDC := !azureConfig.UseOIDCFederation.IsNull() && azureConfig.UseOIDCFederation.ValueBool()

	clientSecret := ""
	if !azureConfig.ClientSecret.IsNull() {
		clientSecret = azureConfig.ClientSecret.ValueString()
	}

	cloudAccountID := ""
	if !azureConfig.CloudAccountID.IsNull() {
		cloudAccountID = azureConfig.CloudAccountID.ValueString()
	}

	// Secret-manager mode: only a cloud-account id is supplied. The credentials
	// -- including subscription and tenant -- are resolved inside the pod, so
	// none of the identity fields are required here.
	if cloudAccountID != "" {
		if clientSecret != "" || useOIDC {
			return nil, fmt.Errorf("cloud_account_id cannot be combined with client_secret or " +
				"use_oidc_federation; pick exactly one authentication mode")
		}
		// secret_manager_path is optional and normally omitted: the action derives
		// it in-pod from the control plane's own environment (TF_VAR_CP_NAME /
		// TF_VAR_CP_CLOUD), exactly as cloudaccount-fetch-secret/secret-fetcher.py
		// does. End users only ever supply a cloud account id, which they pick from
		// a list -- they are not expected to know the control plane's internal
		// secret layout. Set it explicitly only to override that convention.
		path := ""
		if !azureConfig.SecretManagerPath.IsNull() {
			path = azureConfig.SecretManagerPath.ValueString()
		}
		if path == "" {
			// Derive it here, at apply time, where the control plane's own
			// environment is available. The ACTION pod does not carry
			// TF_VAR_CP_NAME (only the release pod does), so this cannot be
			// deferred to run time.
			path = DeriveSecretManagerPath(cloudAccountID)
			if path == "" {
				return nil, fmt.Errorf("could not derive the credentials secret id: neither " +
					"TF_VAR_CP_NAME nor CP_NAME is set in the release environment. " +
					"Set secret_manager_path explicitly in the azure block to override")
			}
		}
		return &AzureAuthConfig{
			Mode:              AuthModeSecretManager,
			CloudAccountID:    cloudAccountID,
			SecretManagerPath: path,
		}, nil
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

	if useOIDC && clientSecret != "" {
		return nil, fmt.Errorf("client_secret must not be set when use_oidc_federation is true; " +
			"OIDC federation exchanges a projected service-account token and needs no secret")
	}
	if !useOIDC && clientSecret == "" {
		return nil, fmt.Errorf("no authentication mode configured. Set client_secret in the " +
			"azure block (the supported mode: the provider creates and manages the backing " +
			"Kubernetes Secret for you). Alternatives: use_oidc_federation, which needs a " +
			"federated credential registered in Entra, or cloud_account_id, which resolves " +
			"credentials from the control plane's secret manager")
	}

	tokenFile := DefaultFederatedTokenFile
	if !azureConfig.FederatedTokenFile.IsNull() && azureConfig.FederatedTokenFile.ValueString() != "" {
		tokenFile = azureConfig.FederatedTokenFile.ValueString()
	}

	mode := AuthModeClientSecret
	if useOIDC {
		mode = AuthModeOIDCFederation
	}

	// Derived by default so neither side has to communicate the name.
	secretName := DeriveSecretName(tenantID, clientID, subscriptionID)
	if !azureConfig.SecretName.IsNull() && azureConfig.SecretName.ValueString() != "" {
		secretName = azureConfig.SecretName.ValueString()
	}
	secretKey := DefaultCredentialsSecretKey
	if !azureConfig.SecretKey.IsNull() && azureConfig.SecretKey.ValueString() != "" {
		secretKey = azureConfig.SecretKey.ValueString()
	}

	return &AzureAuthConfig{
		Mode:               mode,
		SubscriptionID:     subscriptionID,
		TenantID:           tenantID,
		ClientID:           clientID,
		ClientSecret:       clientSecret,
		UseOIDCFederation:  useOIDC,
		FederatedTokenFile: tokenFile,
		SecretName:         secretName,
		SecretKey:          secretKey,
	}, nil
}

func requiredString(v types.String, name string) (string, error) {
	if v.IsNull() || v.ValueString() == "" {
		return "", fmt.Errorf("%s is required in the azure block of the provider configuration", name)
	}
	return v.ValueString(), nil
}

// DeriveSecretManagerPath builds the secret id holding a cloud account's
// credentials, using the same convention as
// cloudaccount-fetch-secret/secret-fetcher.py. It runs at apply time, in the
// release environment, because the action pod itself has no TF_VAR_CP_NAME.
//
// Returns "" when the cluster name cannot be determined.
func DeriveSecretManagerPath(cloudAccountID string) string {
	cluster := firstNonEmptyEnv("TF_VAR_CP_NAME", "CP_NAME")
	if cluster == "" || cloudAccountID == "" {
		return ""
	}
	if cpCloud := firstNonEmptyEnv("TF_VAR_CP_CLOUD", "CP_CLOUD"); cpCloud == "gcp" {
		return fmt.Sprintf("%s_backend_accounts_%s", cluster, cloudAccountID)
	}
	return fmt.Sprintf("%s/backend/accounts/%s", cluster, cloudAccountID)
}

func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
