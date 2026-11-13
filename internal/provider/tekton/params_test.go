package tekton

import (
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBuildParamSpecs_NameAndTypeOnlyRendersAsBefore(t *testing.T) {
	got := BuildParamSpecs([]ParamModel{{
		Name:        types.StringValue("REPLICAS"),
		Type:        types.StringValue("string"),
		Description: types.StringNull(),
		Default:     types.StringNull(),
	}})

	want := []interface{}{map[string]interface{}{"name": "REPLICAS", "type": "string"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestBuildParamSpecs_EmitsDescriptionAndDefault(t *testing.T) {
	got := BuildParamSpecs([]ParamModel{{
		Name:        types.StringValue("REPLICAS"),
		Type:        types.StringValue("string"),
		Description: types.StringValue("Replica count to scale to"),
		Default:     types.StringValue("1"),
	}})

	want := []interface{}{map[string]interface{}{
		"name":        "REPLICAS",
		"type":        "string",
		"description": "Replica count to scale to",
		"default":     "1",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// An empty default is a real value: it makes the param optional with "" as
// its value, which is different from leaving default unset.
func TestBuildParamSpecs_KeepsEmptyDefault(t *testing.T) {
	got := BuildParamSpecs([]ParamModel{{
		Name:        types.StringValue("TAG"),
		Type:        types.StringValue("string"),
		Description: types.StringValue(""),
		Default:     types.StringValue(""),
	}})

	spec := got[0].(map[string]interface{})
	if v, ok := spec["default"]; !ok || v != "" {
		t.Errorf("empty default must be emitted, got %#v", spec)
	}
	if _, ok := spec["description"]; ok {
		t.Errorf("empty description must be omitted, got %#v", spec)
	}
}
