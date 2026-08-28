package azure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	SubscriptionID types.String `tfsdk:"subscription_id"`
	TenantID       types.String `tfsdk:"tenant_id"`
	ClientID       types.String `tfsdk:"client_id"`
	ClientSecret   types.String `tfsdk:"client_secret"`
	SecretName     types.String `tfsdk:"secret_name"`
	SecretKey      types.String `tfsdk:"secret_key"`
}

// AuthMode identifies how the action pod obtains Azure credentials.
//
// Only client-secret auth is supported. OIDC federation and control-plane
// secret-manager resolution were both implemented and removed deliberately: a
// mode selected by the mere PRESENCE of a field can be switched by accident --
// notably by an output-type mapping on a cloud_account module, which would then
// change auth for every project using that account. A lone use_oidc_federation
// applied cleanly and failed only when a user clicked the action. Keeping one
// mode removes that entire class of failure.
//
// The type is retained so re-introducing a mode stays a small change. See git
// history at 344e041 for the removed implementations.
type AuthMode int

const (
	// AuthModeClientSecret reads the service principal password from a
	// Kubernetes Secret, which this provider creates and maintains, via
	// secretKeyRef.
	AuthModeClientSecret AuthMode = iota
)

// AzureAuthConfig represents processed Azure authentication configuration.
//
// The action pod authenticates as a service principal, using a password the
// provider stores in a Kubernetes Secret and the pod reads via secretKeyRef. The
// user triggering the action supplies nothing, mirroring how the AWS variant gets
// credentials silently from IRSA.
//
// Provider configuration is not persisted in Terraform state, and the password is
// never inlined into the StepAction, so it appears in neither.
type AzureAuthConfig struct {
	Mode AuthMode

	// Identity of the service principal the action authenticates as.
	SubscriptionID string
	TenantID       string
	ClientID       string
	ClientSecret   string

	// SecretName is the Kubernetes Secret the provider creates and maintains, and
	// SecretKey the key within it holding the password.
	//
	// SecretName defaults to a value derived from the identity above (see
	// DeriveSecretName) rather than a fixed string. Tekton actions share one
	// namespace across every project on a control plane, so a fixed name would let
	// one project's credentials overwrite another's.
	SecretName string
	SecretKey  string
}

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
// All four of subscription_id, tenant_id, client_id and client_secret are
// required. There is no alternative mode to select, so a missing field is always
// a missing field rather than a signal to authenticate some other way.
func GetAzureConfig(ctx context.Context, providerModel *ProviderModel) (*AzureAuthConfig, error) {
	if providerModel == nil {
		return nil, fmt.Errorf("provider model is nil")
	}

	if providerModel.Azure.IsNull() {
		return nil, fmt.Errorf("Azure configuration is required for facets_tekton_action_azure " +
			"resource. Add an 'azure' block to your provider configuration with subscription_id, " +
			"tenant_id, client_id and client_secret")
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
	clientSecret, err := requiredString(azureConfig.ClientSecret, "client_secret")
	if err != nil {
		return nil, err
	}

	// Derived by default so neither side has to communicate the name: the Secret
	// lives in the control plane namespace, while the module referencing it is
	// configured by someone who cannot see that namespace.
	secretName := DeriveSecretName(tenantID, clientID, subscriptionID)
	if !azureConfig.SecretName.IsNull() && azureConfig.SecretName.ValueString() != "" {
		secretName = azureConfig.SecretName.ValueString()
	}
	secretKey := DefaultCredentialsSecretKey
	if !azureConfig.SecretKey.IsNull() && azureConfig.SecretKey.ValueString() != "" {
		secretKey = azureConfig.SecretKey.ValueString()
	}

	return &AzureAuthConfig{
		Mode:           AuthModeClientSecret,
		SubscriptionID: subscriptionID,
		TenantID:       tenantID,
		ClientID:       clientID,
		ClientSecret:   clientSecret,
		SecretName:     secretName,
		SecretKey:      secretKey,
	}, nil
}

func requiredString(v types.String, name string) (string, error) {
	if v.IsNull() || v.ValueString() == "" {
		return "", fmt.Errorf("%s is required in the azure block of the provider configuration", name)
	}
	return v.ValueString(), nil
}
