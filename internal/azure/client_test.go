package azure

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var secretRefAttrTypes = map[string]attr.Type{
	"secret_name": types.StringType,
	"secret_key":  types.StringType,
}

var azureAttrTypes = map[string]attr.Type{
	"subscription_id":       types.StringType,
	"tenant_id":             types.StringType,
	"client_id":             types.StringType,
	"environment":           types.StringType,
	"use_workload_identity": types.BoolType,
	"client_secret_ref":     types.ObjectType{AttrTypes: secretRefAttrTypes},
}

// azureObject builds the provider's azure block. workloadIdentity and secretRef
// are the two auth knobs under test; pass nil to leave one unset.
func azureObject(t *testing.T, subscriptionID, tenantID, clientID, environment string, workloadIdentity *bool, secretRef *ClientSecretRef) types.Object {
	t.Helper()

	wi := types.BoolNull()
	if workloadIdentity != nil {
		wi = types.BoolValue(*workloadIdentity)
	}

	ref := types.ObjectNull(secretRefAttrTypes)
	if secretRef != nil {
		key := types.StringNull()
		if secretRef.SecretKey != "" {
			key = types.StringValue(secretRef.SecretKey)
		}
		obj, diags := types.ObjectValue(secretRefAttrTypes, map[string]attr.Value{
			"secret_name": types.StringValue(secretRef.SecretName),
			"secret_key":  key,
		})
		if diags.HasError() {
			t.Fatalf("building secret_ref object: %v", diags.Errors())
		}
		ref = obj
	}

	env := types.StringNull()
	if environment != "" {
		env = types.StringValue(environment)
	}

	obj, diags := types.ObjectValue(azureAttrTypes, map[string]attr.Value{
		"subscription_id":       types.StringValue(subscriptionID),
		"tenant_id":             types.StringValue(tenantID),
		"client_id":             types.StringValue(clientID),
		"environment":           env,
		"use_workload_identity": wi,
		"client_secret_ref":     ref,
	})
	if diags.HasError() {
		t.Fatalf("building azure object: %v", diags.Errors())
	}
	return obj
}

func boolPtr(b bool) *bool { return &b }

func TestGetAzureConfig_WorkloadIdentity(t *testing.T) {
	model := &ProviderModel{
		Azure: azureObject(t, "sub-1", "tenant-1", "client-1", "", boolPtr(true), nil),
	}

	cfg, err := GetAzureConfig(context.Background(), model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.UseWorkloadIdentity {
		t.Error("expected UseWorkloadIdentity to be true")
	}
	if cfg.ClientSecretRef != nil {
		t.Errorf("expected no ClientSecretRef, got %+v", cfg.ClientSecretRef)
	}
	if cfg.Environment != "AzureCloud" {
		t.Errorf("expected default environment AzureCloud, got %q", cfg.Environment)
	}
	if cfg.SubscriptionID != "sub-1" || cfg.TenantID != "tenant-1" || cfg.ClientID != "client-1" {
		t.Errorf("identity fields not carried through: %+v", cfg)
	}
}

func TestGetAzureConfig_ClientSecretRefDefaultsKey(t *testing.T) {
	model := &ProviderModel{
		Azure: azureObject(t, "sub-1", "tenant-1", "client-1", "AzureUSGovernment", nil,
			&ClientSecretRef{SecretName: "facets-azure-sp"}),
	}

	cfg, err := GetAzureConfig(context.Background(), model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.UseWorkloadIdentity {
		t.Error("expected UseWorkloadIdentity to be false")
	}
	if cfg.ClientSecretRef == nil {
		t.Fatal("expected ClientSecretRef to be set")
	}
	if cfg.ClientSecretRef.SecretKey != DefaultClientSecretKey {
		t.Errorf("expected default secret key %q, got %q", DefaultClientSecretKey, cfg.ClientSecretRef.SecretKey)
	}
	if cfg.Environment != "AzureUSGovernment" {
		t.Errorf("expected environment to be honoured, got %q", cfg.Environment)
	}
}

func TestGetAzureConfig_Errors(t *testing.T) {
	tests := []struct {
		name      string
		model     *ProviderModel
		wantInErr string
	}{
		{
			name:      "nil model",
			model:     nil,
			wantInErr: "provider model is nil",
		},
		{
			name:      "azure block absent",
			model:     &ProviderModel{Azure: types.ObjectNull(azureAttrTypes)},
			wantInErr: "Azure configuration is required",
		},
		{
			name: "missing subscription_id",
			model: &ProviderModel{
				Azure: azureObject(t, "", "tenant-1", "client-1", "", boolPtr(true), nil),
			},
			wantInErr: "subscription_id is required",
		},
		{
			name: "missing tenant_id",
			model: &ProviderModel{
				Azure: azureObject(t, "sub-1", "", "client-1", "", boolPtr(true), nil),
			},
			wantInErr: "tenant_id is required",
		},
		{
			name: "missing client_id",
			model: &ProviderModel{
				Azure: azureObject(t, "sub-1", "tenant-1", "", "", boolPtr(true), nil),
			},
			wantInErr: "client_id is required",
		},
		{
			name: "no auth method",
			model: &ProviderModel{
				Azure: azureObject(t, "sub-1", "tenant-1", "client-1", "", nil, nil),
			},
			wantInErr: "one of use_workload_identity or client_secret_ref is required",
		},
		{
			name: "both auth methods",
			model: &ProviderModel{
				Azure: azureObject(t, "sub-1", "tenant-1", "client-1", "", boolPtr(true),
					&ClientSecretRef{SecretName: "facets-azure-sp"}),
			},
			wantInErr: "mutually exclusive",
		},
		{
			name: "secret ref without name",
			model: &ProviderModel{
				Azure: azureObject(t, "sub-1", "tenant-1", "client-1", "", nil,
					&ClientSecretRef{SecretName: ""}),
			},
			wantInErr: "client_secret_ref.secret_name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GetAzureConfig(context.Background(), tt.model)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantInErr)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantInErr, err.Error())
			}
		})
	}
}

// use_workload_identity = false with no secret ref is the same "nothing to
// present to az login" state as leaving it unset, and must be rejected rather
// than silently producing a StepAction that fails at runtime.
func TestGetAzureConfig_WorkloadIdentityExplicitlyFalse(t *testing.T) {
	model := &ProviderModel{
		Azure: azureObject(t, "sub-1", "tenant-1", "client-1", "", boolPtr(false), nil),
	}

	if _, err := GetAzureConfig(context.Background(), model); err == nil {
		t.Fatal("expected an error when neither auth method is usable")
	}
}
