package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func paramObject(t *testing.T, typ string, def attr.Value) types.Object {
	t.Helper()
	obj, diags := types.ObjectValue(
		map[string]attr.Type{
			"name":        types.StringType,
			"type":        types.StringType,
			"description": types.StringType,
			"default":     types.StringType,
		},
		map[string]attr.Value{
			"name":        types.StringValue("P"),
			"type":        types.StringValue(typ),
			"description": types.StringNull(),
			"default":     def,
		},
	)
	if diags.HasError() {
		t.Fatalf("building object: %v", diags)
	}
	return obj
}

func validateParam(t *testing.T, obj types.Object) bool {
	t.Helper()
	resp := &validator.ObjectResponse{}
	paramDefaultValidator{}.ValidateObject(context.Background(), validator.ObjectRequest{
		Path:        path.Root("params").AtListIndex(0),
		ConfigValue: obj,
	}, resp)
	return resp.Diagnostics.HasError()
}

func TestParamDefaultValidator(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		def     attr.Value
		wantErr bool
	}{
		{"string with default", "string", types.StringValue("1"), false},
		{"string without default", "string", types.StringNull(), false},
		{"array without default", "array", types.StringNull(), false},
		{"array with default", "array", types.StringValue("a"), true},
		{"object with default", "object", types.StringValue("{}"), true},
		{"unknown default on array is still rejected", "array", types.StringUnknown(), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validateParam(t, paramObject(t, c.typ, c.def)); got != c.wantErr {
				t.Errorf("error = %v, want %v", got, c.wantErr)
			}
		})
	}
}
