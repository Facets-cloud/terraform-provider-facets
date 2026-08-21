package azure

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func azBlock(t *testing.T, fields map[string]attr.Value) *ProviderModel {
	t.Helper()
	all := map[string]attr.Type{
		"subscription_id": types.StringType, "tenant_id": types.StringType,
		"client_id": types.StringType, "client_secret": types.StringType,
		"cloud_account_id": types.StringType, "secret_manager_path": types.StringType,
		"use_oidc_federation": types.BoolType, "federated_token_file": types.StringType,
		"secret_name": types.StringType, "secret_key": types.StringType,
	}
	vals := map[string]attr.Value{}
	for k, ty := range all {
		if v, ok := fields[k]; ok {
			vals[k] = v
		} else if ty == types.BoolType {
			vals[k] = types.BoolNull()
		} else {
			vals[k] = types.StringNull()
		}
	}
	o, d := basetypes.NewObjectValue(all, vals)
	if d.HasError() {
		t.Fatalf("fixture: %v", d.Errors())
	}
	return &ProviderModel{Azure: o}
}

// The mode is selected by WHICH FIELDS ARE SET -- there is no mode argument. This
// pins the documented precedence so the table in docs/ cannot silently drift.
func TestModeSelection(t *testing.T) {
	ids := map[string]attr.Value{
		"subscription_id": types.StringValue("sub"),
		"tenant_id":       types.StringValue("ten"),
		"client_id":       types.StringValue("cli"),
	}
	with := func(extra map[string]attr.Value) map[string]attr.Value {
		m := map[string]attr.Value{}
		for k, v := range ids {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	t.Run("client_secret -> ClientSecret", func(t *testing.T) {
		c, err := GetAzureConfig(context.Background(), azBlock(t, with(map[string]attr.Value{
			"client_secret": types.StringValue("pw")})))
		if err != nil || c.Mode != AuthModeClientSecret {
			t.Fatalf("got mode=%v err=%v", c, err)
		}
	})

	t.Run("use_oidc_federation -> OIDCFederation", func(t *testing.T) {
		c, err := GetAzureConfig(context.Background(), azBlock(t, with(map[string]attr.Value{
			"use_oidc_federation": types.BoolValue(true)})))
		if err != nil || c.Mode != AuthModeOIDCFederation {
			t.Fatalf("got %v err=%v", c, err)
		}
	})

	t.Run("cloud_account_id alone -> SecretManager or a clear derivation error", func(t *testing.T) {
		c, err := GetAzureConfig(context.Background(), azBlock(t, map[string]attr.Value{
			"cloud_account_id":    types.StringValue("acct"),
			"secret_manager_path": types.StringValue("explicit/path")}))
		if err != nil || c.Mode != AuthModeSecretManager {
			t.Fatalf("got %v err=%v", c, err)
		}
	})

	// No default: setting none of the three must FAIL rather than silently pick one.
	t.Run("nothing set -> error, no default mode", func(t *testing.T) {
		_, err := GetAzureConfig(context.Background(), azBlock(t, ids))
		if err == nil {
			t.Fatal("expected an error when no auth mode is configured")
		}
		if !strings.Contains(err.Error(), "client_secret") {
			t.Errorf("error must point at the supported mode; got: %v", err)
		}
		// The old message recommended cloud_account_id, a mode we do not deploy.
		if strings.Contains(err.Error(), "cloud_account_id (recommended)") {
			t.Errorf("stale guidance in error: %v", err)
		}
	})

	t.Run("conflicting modes are rejected", func(t *testing.T) {
		for name, f := range map[string]map[string]attr.Value{
			"cloud_account_id+client_secret": {
				"cloud_account_id": types.StringValue("a"), "client_secret": types.StringValue("pw")},
			"cloud_account_id+oidc": {
				"cloud_account_id": types.StringValue("a"), "use_oidc_federation": types.BoolValue(true)},
			"client_secret+oidc": with(map[string]attr.Value{
				"client_secret": types.StringValue("pw"), "use_oidc_federation": types.BoolValue(true)}),
		} {
			if _, err := GetAzureConfig(context.Background(), azBlock(t, f)); err == nil {
				t.Errorf("%s must be rejected", name)
			}
		}
	})
}
