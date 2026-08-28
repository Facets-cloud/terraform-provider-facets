package azure

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var azureAttrTypes = map[string]attr.Type{
	"subscription_id": types.StringType,
	"tenant_id":       types.StringType,
	"client_id":       types.StringType,
	"client_secret":   types.StringType,
	"secret_name":     types.StringType,
	"secret_key":      types.StringType,
}

// newModel builds a ProviderModel with the given azure block values. Nil values
// become null, mirroring an unset attribute in HCL.
func newModel(t *testing.T, vals map[string]attr.Value) *ProviderModel {
	t.Helper()
	full := map[string]attr.Value{
		"subscription_id": types.StringNull(),
		"tenant_id":       types.StringNull(),
		"client_id":       types.StringNull(),
		"client_secret":   types.StringNull(),
		"secret_name":     types.StringNull(),
		"secret_key":      types.StringNull(),
	}
	for k, v := range vals {
		full[k] = v
	}
	obj, diags := types.ObjectValue(azureAttrTypes, full)
	if diags.HasError() {
		t.Fatalf("building object: %v", diags.Errors())
	}
	return &ProviderModel{Azure: obj}
}

func validSecretModel(t *testing.T) *ProviderModel {
	return newModel(t, map[string]attr.Value{
		"subscription_id": types.StringValue("sub-1"),
		"tenant_id":       types.StringValue("tenant-1"),
		"client_id":       types.StringValue("client-1"),
		"client_secret":   types.StringValue("shhh"),
	})
}

func TestGetAzureConfig_NilProviderModel(t *testing.T) {
	if _, err := GetAzureConfig(context.Background(), nil); err == nil {
		t.Fatal("expected an error for a nil provider model")
	}
}

func TestGetAzureConfig_MissingAzureBlock(t *testing.T) {
	m := &ProviderModel{Azure: types.ObjectNull(azureAttrTypes)}
	_, err := GetAzureConfig(context.Background(), m)
	if err == nil {
		t.Fatal("expected an error when the azure block is absent")
	}
	if !strings.Contains(err.Error(), "azure") {
		t.Errorf("error should mention the azure block, got: %v", err)
	}
}

func TestGetAzureConfig_ClientSecretMode(t *testing.T) {
	cfg, err := GetAzureConfig(context.Background(), validSecretModel(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SubscriptionID != "sub-1" || cfg.TenantID != "tenant-1" || cfg.ClientID != "client-1" {
		t.Errorf("identity fields not carried through: %+v", cfg)
	}
	if cfg.ClientSecret != "shhh" {
		t.Errorf("client secret not carried through, got %q", cfg.ClientSecret)
	}
}

func TestGetAzureConfig_RequiresClientSecret(t *testing.T) {
	m := newModel(t, map[string]attr.Value{
		"subscription_id": types.StringValue("sub-1"),
		"tenant_id":       types.StringValue("tenant-1"),
		"client_id":       types.StringValue("client-1"),
	})
	_, err := GetAzureConfig(context.Background(), m)
	if err == nil {
		t.Fatal("expected an error when neither auth mode is configured")
	}
}

func TestGetAzureConfig_RequiredFields(t *testing.T) {
	for _, missing := range []string{"subscription_id", "tenant_id", "client_id"} {
		t.Run(missing, func(t *testing.T) {
			vals := map[string]attr.Value{
				"subscription_id": types.StringValue("sub-1"),
				"tenant_id":       types.StringValue("tenant-1"),
				"client_id":       types.StringValue("client-1"),
				"client_secret":   types.StringValue("shhh"),
			}
			vals[missing] = types.StringNull()
			_, err := GetAzureConfig(context.Background(), newModel(t, vals))
			if err == nil {
				t.Fatalf("expected an error when %s is missing", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error should name %s, got: %v", missing, err)
			}
		})
	}
}

// An empty string is as unusable as a null, so it must be rejected too.
func TestGetAzureConfig_RejectsEmptyRequiredField(t *testing.T) {
	m := newModel(t, map[string]attr.Value{
		"subscription_id": types.StringValue(""),
		"tenant_id":       types.StringValue("tenant-1"),
		"client_id":       types.StringValue("client-1"),
		"client_secret":   types.StringValue("shhh"),
	})
	if _, err := GetAzureConfig(context.Background(), m); err == nil {
		t.Fatal("expected an error for an empty subscription_id")
	}
}

// The Secret name must be overridable: Tekton actions share one namespace across
// every project on a control plane, so a fixed name would make two tenants
// collide on one Secret.
func TestGetAzureConfig_SecretNameDefaultsAndOverrides(t *testing.T) {
	cfg, err := GetAzureConfig(context.Background(), validSecretModel(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := DeriveSecretName("tenant-1", "client-1", "sub-1")
	if cfg.SecretName != want {
		t.Errorf("derived name = %q, want %q", cfg.SecretName, want)
	}
	if cfg.SecretKey != DefaultCredentialsSecretKey {
		t.Errorf("default key = %q, want %q", cfg.SecretKey, DefaultCredentialsSecretKey)
	}

	m := newModel(t, map[string]attr.Value{
		"subscription_id": types.StringValue("sub-1"),
		"tenant_id":       types.StringValue("tenant-1"),
		"client_id":       types.StringValue("client-1"),
		"client_secret":   types.StringValue("shhh"),
		"secret_name":     types.StringValue("fourkites-azure-creds"),
		"secret_key":      types.StringValue("azure_client_secret"),
	})
	cfg, err = GetAzureConfig(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SecretName != "fourkites-azure-creds" {
		t.Errorf("override name = %q", cfg.SecretName)
	}
	if cfg.SecretKey != "azure_client_secret" {
		t.Errorf("override key = %q", cfg.SecretKey)
	}
}

// The name must be derivable by both sides independently: the control-plane
// operator creating the Secret and the customer configuring the module never
// exchange it, and the customer cannot see the namespace to look it up.
func TestDeriveSecretName_StableAndIdentityScoped(t *testing.T) {
	a := DeriveSecretName("tenant-1", "client-1", "sub-1")
	if a != DeriveSecretName("tenant-1", "client-1", "sub-1") {
		t.Error("must be deterministic: both sides compute it independently")
	}
	if len(a) > 253 {
		t.Errorf("name too long for a Kubernetes object: %d chars", len(a))
	}

	// Different principals must not collide, or one project would overwrite
	// another's credentials on a shared control plane.
	for _, other := range [][3]string{
		{"tenant-2", "client-1", "sub-1"},
		{"tenant-1", "client-2", "sub-1"},
		{"tenant-1", "client-1", "sub-2"},
	} {
		if DeriveSecretName(other[0], other[1], other[2]) == a {
			t.Errorf("collision with %v", other)
		}
	}
}
