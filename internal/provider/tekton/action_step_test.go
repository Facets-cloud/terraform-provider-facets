package tekton

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func actionStep(t *testing.T, env map[string]string) ActionStepModel {
	t.Helper()
	m := types.MapNull(types.StringType)
	if env != nil {
		vals := map[string]attr.Value{}
		for k, v := range env {
			vals[k] = types.StringValue(v)
		}
		built, d := types.MapValue(types.StringType, vals)
		if d.HasError() {
			t.Fatalf("env map: %v", d.Errors())
		}
		m = built
	}
	return ActionStepModel{
		Name:      types.StringValue("run"),
		Image:     types.StringValue("img:1"),
		Script:    types.StringValue("#!/bin/sh\ntrue\n"),
		Env:       m,
		Resources: types.ObjectNull(map[string]attr.Type{}),
	}
}

// envFrom is what makes one resource work for every cloud: the provider attaches
// the Secret without knowing any of the key names inside it.
func TestBuildActionStep_AttachesCredentialsViaEnvFrom(t *testing.T) {
	step, err := BuildActionStep(context.Background(), actionStep(t, nil), "facets-action-creds-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	from, ok := step["envFrom"].([]interface{})
	if !ok || len(from) != 1 {
		t.Fatalf("expected one envFrom source, got %v", step["envFrom"])
	}
	ref := from[0].(map[string]interface{})["secretRef"].(map[string]interface{})
	if ref["name"] != "facets-action-creds-abc" {
		t.Errorf("wrong secretRef: %v", ref)
	}

	// No credential may appear as a literal anywhere in the step.
	if _, hasEnv := step["env"]; hasEnv {
		t.Errorf("no plain env was configured, yet env was emitted: %v", step["env"])
	}
}

// Referencing a Secret that was never created leaves every pod stuck in
// CreateContainerConfigError, so no secret means no envFrom at all.
func TestBuildActionStep_OmitsEnvFromWithoutCredentials(t *testing.T) {
	step, err := BuildActionStep(context.Background(), actionStep(t, nil), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := step["envFrom"]; present {
		t.Errorf("envFrom must be omitted when there is no Secret, got %v", step["envFrom"])
	}
}

// Go map iteration is randomised; without sorting the rendered Task differs
// between runs and every plan shows a diff.
func TestBuildActionStep_EnvIsSortedAndStable(t *testing.T) {
	env := map[string]string{"ZONE": "z", "ALPHA": "a", "MIDDLE": "m"}
	want := []string{"ALPHA", "MIDDLE", "ZONE"}

	for i := 0; i < 50; i++ {
		step, err := BuildActionStep(context.Background(), actionStep(t, env), "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := []string{}
		for _, e := range step["env"].([]interface{}) {
			got = append(got, e.(map[string]interface{})["name"].(string))
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("iteration %d: unstable order %v", i, got)
		}
	}
}

func TestBuildActionStep_CarriesCoreFields(t *testing.T) {
	step, _ := BuildActionStep(context.Background(), actionStep(t, map[string]string{"REGION": "us-east-1"}), "sec")
	if step["name"] != "run" || step["image"] != "img:1" {
		t.Errorf("core fields wrong: %v", step)
	}
	entry := step["env"].([]interface{})[0].(map[string]interface{})
	if entry["name"] != "REGION" || entry["value"] != "us-east-1" {
		t.Errorf("plain env not carried: %v", entry)
	}
}
