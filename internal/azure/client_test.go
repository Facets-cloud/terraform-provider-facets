package azure

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var azureAttrTypes = map[string]attr.Type{
	"subscription_id":      types.StringType,
	"tenant_id":            types.StringType,
	"client_id":            types.StringType,
	"client_secret":        types.StringType,
	"use_oidc_federation":  types.BoolType,
	"federated_token_file": types.StringType,
}

// newModel builds a ProviderModel with the given azure block values. Nil values
// become null, mirroring an unset attribute in HCL.
func newModel(t *testing.T, vals map[string]attr.Value) *ProviderModel {
	t.Helper()
	full := map[string]attr.Value{
		"subscription_id":      types.StringNull(),
		"tenant_id":            types.StringNull(),
		"client_id":            types.StringNull(),
		"client_secret":        types.StringNull(),
		"use_oidc_federation":  types.BoolNull(),
		"federated_token_file": types.StringNull(),
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
	if cfg.UseOIDCFederation {
		t.Error("UseOIDCFederation should be false in client-secret mode")
	}
}

func TestGetAzureConfig_OIDCMode_DefaultsTokenFile(t *testing.T) {
	m := newModel(t, map[string]attr.Value{
		"subscription_id":     types.StringValue("sub-1"),
		"tenant_id":           types.StringValue("tenant-1"),
		"client_id":           types.StringValue("client-1"),
		"use_oidc_federation": types.BoolValue(true),
	})
	cfg, err := GetAzureConfig(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.UseOIDCFederation {
		t.Error("UseOIDCFederation should be true")
	}
	if cfg.ClientSecret != "" {
		t.Errorf("no secret should be set in OIDC mode, got %q", cfg.ClientSecret)
	}
	if cfg.FederatedTokenFile != DefaultFederatedTokenFile {
		t.Errorf("token file should default to %q, got %q", DefaultFederatedTokenFile, cfg.FederatedTokenFile)
	}
}

func TestGetAzureConfig_OIDCMode_HonoursCustomTokenFile(t *testing.T) {
	m := newModel(t, map[string]attr.Value{
		"subscription_id":      types.StringValue("sub-1"),
		"tenant_id":            types.StringValue("tenant-1"),
		"client_id":            types.StringValue("client-1"),
		"use_oidc_federation":  types.BoolValue(true),
		"federated_token_file": types.StringValue("/custom/path/token"),
	})
	cfg, err := GetAzureConfig(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FederatedTokenFile != "/custom/path/token" {
		t.Errorf("custom token file not honoured, got %q", cfg.FederatedTokenFile)
	}
}

// Both auth modes at once is ambiguous and must be rejected, otherwise it is
// unclear whether the secret or the federated token is authoritative.
func TestGetAzureConfig_RejectsBothAuthModes(t *testing.T) {
	m := newModel(t, map[string]attr.Value{
		"subscription_id":     types.StringValue("sub-1"),
		"tenant_id":           types.StringValue("tenant-1"),
		"client_id":           types.StringValue("client-1"),
		"client_secret":       types.StringValue("shhh"),
		"use_oidc_federation": types.BoolValue(true),
	})
	_, err := GetAzureConfig(context.Background(), m)
	if err == nil {
		t.Fatal("expected an error when both client_secret and use_oidc_federation are set")
	}
	if !strings.Contains(err.Error(), "client_secret") {
		t.Errorf("error should name client_secret, got: %v", err)
	}
}

func TestGetAzureConfig_RejectsNeitherAuthMode(t *testing.T) {
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
