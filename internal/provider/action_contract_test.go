package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton/testfake"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Tests for the action-result contract on facets_actions and
// facets_tekton_action_kubernetes. Configurations are built as raw tftypes
// values against each resource's real schema and decoded through tfsdk.Config,
// so the models under test are exactly what Terraform would hand the provider.

// rawFromSchema builds a raw object for the schema, null everywhere except the
// given attributes.
func rawFromSchema(ctx context.Context, s schema.Schema, vals map[string]tftypes.Value) tftypes.Value {
	objType := s.Type().TerraformType(ctx).(tftypes.Object)
	fields := map[string]tftypes.Value{}
	for name, ft := range objType.AttributeTypes {
		if v, ok := vals[name]; ok {
			fields[name] = v
		} else {
			fields[name] = tftypes.NewValue(ft, nil)
		}
	}
	return tftypes.NewValue(objType, fields)
}

// stepRaw builds one element of the steps list, with env set from envVal and
// every other optional attribute null.
func stepRaw(ctx context.Context, s schema.Schema, name string, envVal func(tftypes.Type) tftypes.Value) tftypes.Value {
	listType := s.Attributes["steps"].GetType().TerraformType(ctx).(tftypes.List)
	elem := listType.ElementType.(tftypes.Object)
	fields := map[string]tftypes.Value{}
	for attr, ft := range elem.AttributeTypes {
		fields[attr] = tftypes.NewValue(ft, nil)
	}
	fields["name"] = tftypes.NewValue(tftypes.String, name)
	fields["image"] = tftypes.NewValue(tftypes.String, "busybox:1")
	fields["script"] = tftypes.NewValue(tftypes.String, "#!/bin/sh\ntrue\n")
	if envVal != nil {
		fields["env"] = envVal(elem.AttributeTypes["env"])
	}
	return tftypes.NewValue(elem, fields)
}

func stepsRaw(ctx context.Context, s schema.Schema, steps ...tftypes.Value) tftypes.Value {
	listType := s.Attributes["steps"].GetType().TerraformType(ctx).(tftypes.List)
	return tftypes.NewValue(listType, steps)
}

// mapEnv returns an env builder for facets_actions, whose env is a map.
func mapEnv(kv map[string]string) func(tftypes.Type) tftypes.Value {
	return func(t tftypes.Type) tftypes.Value {
		vals := map[string]tftypes.Value{}
		for k, v := range kv {
			vals[k] = tftypes.NewValue(tftypes.String, v)
		}
		return tftypes.NewValue(t, vals)
	}
}

// listEnv returns an env builder for the kubernetes resource, whose env is a
// list of {name, value} objects.
func listEnv(names ...string) func(tftypes.Type) tftypes.Value {
	return func(t tftypes.Type) tftypes.Value {
		elem := t.(tftypes.List).ElementType.(tftypes.Object)
		items := make([]tftypes.Value, 0, len(names))
		for _, n := range names {
			items = append(items, tftypes.NewValue(elem, map[string]tftypes.Value{
				"name":  tftypes.NewValue(tftypes.String, n),
				"value": tftypes.NewValue(tftypes.String, "v"),
			}))
		}
		return tftypes.NewValue(t, items)
	}
}

func commonVals(ctx context.Context, s schema.Schema, actionKey interface{}, result interface{}, steps tftypes.Value) map[string]tftypes.Value {
	envType := s.Attributes["facets_environment"].GetType().TerraformType(ctx)
	resType := s.Attributes["facets_resource"].GetType().TerraformType(ctx)
	return map[string]tftypes.Value{
		"name":                 tftypes.NewValue(tftypes.String, "Get Status"),
		"facets_resource_name": tftypes.NewValue(tftypes.String, "db"),
		"facets_environment": tftypes.NewValue(envType, map[string]tftypes.Value{
			"unique_name": tftypes.NewValue(tftypes.String, "dev"),
		}),
		"facets_resource": tftypes.NewValue(resType, map[string]tftypes.Value{
			"kind": tftypes.NewValue(tftypes.String, "postgres"),
		}),
		"namespace":  tftypes.NewValue(tftypes.String, "tekton-pipelines"),
		"action_key": tftypes.NewValue(tftypes.String, actionKey),
		"result":     tftypes.NewValue(tftypes.Bool, result),
		"steps":      steps,
	}
}

func resourceSchema(ctx context.Context, t *testing.T, r resource.Resource) schema.Schema {
	t.Helper()
	resp := &resource.SchemaResponse{}
	r.Schema(ctx, resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Schema returned errors: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// --- facets_actions ------------------------------------------------------

func actionsConfig(ctx context.Context, t *testing.T, actionKey, result interface{}, env map[string]string) (*FacetsActionsResource, tfsdk.Config) {
	t.Helper()
	r := &FacetsActionsResource{
		credsFromEnv: func() (map[string]string, error) { return nil, nil },
	}
	s := resourceSchema(ctx, t, r)
	var envVal func(tftypes.Type) tftypes.Value
	if env != nil {
		envVal = mapEnv(env)
	}
	steps := stepsRaw(ctx, s, stepRaw(ctx, s, "run", envVal), stepRaw(ctx, s, "report", nil))
	return r, tfsdk.Config{Schema: s, Raw: rawFromSchema(ctx, s, commonVals(ctx, s, actionKey, result, steps))}
}

func buildActionsTask(ctx context.Context, t *testing.T, actionKey, result interface{}, env map[string]string) *unstructured.Unstructured {
	t.Helper()
	r, cfg := actionsConfig(ctx, t, actionKey, result, env)
	var m FacetsActionsResourceModel
	if d := cfg.Get(ctx, &m); d.HasError() {
		t.Fatalf("decoding config: %v", d)
	}
	task, diags := r.buildTask(ctx, &m)
	if diags.HasError() {
		t.Fatalf("buildTask: %v", diags)
	}
	return task
}

func TestFacetsActionsBuildTask_LegacyUnchanged(t *testing.T) {
	ctx := context.Background()
	task := buildActionsTask(ctx, t, nil, nil, map[string]string{"B": "2", "A": "1"})
	assertNoContract(t, task)
}

func TestFacetsActionsBuildTask_KeyAndResult(t *testing.T) {
	ctx := context.Background()
	task := buildActionsTask(ctx, t, "get_status", true, map[string]string{"B": "2", "A": "1"})
	assertContract(t, task, "get_status")

	steps, _, _ := unstructured.NestedSlice(task.Object, "spec", "steps")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	// User env stays sorted, with the result file appended after it.
	if got := envNames(steps[0]); strings.Join(got, ",") != "A,B,FACETS_RESULT_FILE" {
		t.Errorf("step 0 env = %v", got)
	}
	if got := envNames(steps[1]); strings.Join(got, ",") != "FACETS_RESULT_FILE" {
		t.Errorf("step 1 env = %v", got)
	}
}

func TestFacetsActionsBuildTask_KeyOnly(t *testing.T) {
	ctx := context.Background()
	task := buildActionsTask(ctx, t, "restart", false, nil)

	labels := task.GetLabels()
	if labels[tekton.ActionKeyLabel] != "restart" {
		t.Errorf("label = %q, want restart", labels[tekton.ActionKeyLabel])
	}
	if _, found, _ := unstructured.NestedSlice(task.Object, "spec", "results"); found {
		t.Error("key without result must not declare results")
	}
	if task.GetAnnotations() != nil {
		t.Errorf("key without result must not annotate: %v", task.GetAnnotations())
	}
}

func TestFacetsActionsBuildTask_ReservedEnvRejected(t *testing.T) {
	ctx := context.Background()
	r, cfg := actionsConfig(ctx, t, "get_status", true, map[string]string{"FACETS_RESULT_FILE": "/tmp/x"})
	var m FacetsActionsResourceModel
	if d := cfg.Get(ctx, &m); d.HasError() {
		t.Fatalf("decoding config: %v", d)
	}
	if _, diags := r.buildTask(ctx, &m); !diags.HasError() {
		t.Error("expected buildTask to reject a reserved env var")
	}
}

func TestFacetsActionsValidateConfig(t *testing.T) {
	tests := []struct {
		name      string
		actionKey interface{}
		result    interface{}
		env       map[string]string
		wantErr   string
	}{
		{name: "legacy", actionKey: nil, result: nil},
		{name: "legacy sets reserved env", actionKey: nil, result: nil, env: map[string]string{"FACETS_RESULT_FILE": "x"}},
		{name: "key only", actionKey: "restart", result: nil},
		{name: "key and result", actionKey: "get_status", result: true},
		{name: "result false without key", actionKey: nil, result: false},
		{name: "result without key", actionKey: nil, result: true, wantErr: "requires action_key"},
		{name: "reserved env", actionKey: "get_status", result: true, env: map[string]string{"FACETS_RESULT_FILE": "x"}, wantErr: "reserved"},
		{name: "unknown key", actionKey: tftypes.UnknownValue, result: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			r, cfg := actionsConfig(ctx, t, tt.actionKey, tt.result, tt.env)
			resp := &resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfg}, resp)
			assertDiag(t, resp, tt.wantErr)
		})
	}
}

// --- facets_tekton_action_kubernetes ------------------------------------

func k8sConfig(ctx context.Context, t *testing.T, actionKey, result interface{}, envNames ...string) (*TektonActionKubernetesResource, tfsdk.Config) {
	t.Helper()
	r := &TektonActionKubernetesResource{}
	s := resourceSchema(ctx, t, r)
	var envVal func(tftypes.Type) tftypes.Value
	if len(envNames) > 0 {
		envVal = listEnv(envNames...)
	}
	steps := stepsRaw(ctx, s, stepRaw(ctx, s, "run", envVal))
	return r, tfsdk.Config{Schema: s, Raw: rawFromSchema(ctx, s, commonVals(ctx, s, actionKey, result, steps))}
}

func buildK8sTask(ctx context.Context, t *testing.T, actionKey, result interface{}) *unstructured.Unstructured {
	t.Helper()
	r, cfg := k8sConfig(ctx, t, actionKey, result, "LOG_LEVEL")
	var m TektonActionKubernetesResourceModel
	if d := cfg.Get(ctx, &m); d.HasError() {
		t.Fatalf("decoding config: %v", d)
	}
	names := tekton.GenerateNames("db", "dev", "Get Status")
	m.TaskName = types.StringValue(names.TaskName)
	m.StepActionName = types.StringValue(names.StepActionName)
	return r.buildTask(ctx, m, map[string]interface{}{"display_name": "Get-Status"})
}

func TestK8sBuildTask_LegacyUnchanged(t *testing.T) {
	ctx := context.Background()
	task := buildK8sTask(ctx, t, nil, nil)
	assertNoContract(t, task)

	steps, _, _ := unstructured.NestedSlice(task.Object, "spec", "steps")
	if got := envNames(steps[1]); strings.Join(got, ",") != "LOG_LEVEL,KUBECONFIG" {
		t.Errorf("user step env = %v", got)
	}
}

func TestK8sBuildTask_KeyAndResult(t *testing.T) {
	ctx := context.Background()
	task := buildK8sTask(ctx, t, "get_status", true)
	assertContract(t, task, "get_status")

	steps, _, _ := unstructured.NestedSlice(task.Object, "spec", "steps")
	if len(steps) != 2 {
		t.Fatalf("expected setup-credentials + 1 user step, got %d", len(steps))
	}
	// Tekton rejects env on a ref step.
	ref := steps[0].(map[string]interface{})
	if _, ok := ref["ref"]; !ok {
		t.Fatalf("first step should be the StepAction ref: %v", ref)
	}
	if _, ok := ref["env"]; ok {
		t.Errorf("env added to the StepAction ref step: %v", ref["env"])
	}
	if got := envNames(steps[1]); strings.Join(got, ",") != "LOG_LEVEL,KUBECONFIG,FACETS_RESULT_FILE" {
		t.Errorf("user step env = %v", got)
	}
}

// The label identifies the Task; the StepAction built from the same labels map
// must not gain it.
func TestK8sBuildTask_DoesNotMutateSharedLabels(t *testing.T) {
	ctx := context.Background()
	r, cfg := k8sConfig(ctx, t, "get_status", true)
	var m TektonActionKubernetesResourceModel
	if d := cfg.Get(ctx, &m); d.HasError() {
		t.Fatalf("decoding config: %v", d)
	}
	shared := map[string]interface{}{"display_name": "x"}
	r.buildTask(ctx, m, shared)
	if _, ok := shared[tekton.ActionKeyLabel]; ok {
		t.Error("buildTask mutated the caller's labels map")
	}
}

func TestK8sValidateConfig(t *testing.T) {
	tests := []struct {
		name      string
		actionKey interface{}
		result    interface{}
		env       []string
		wantErr   string
	}{
		{name: "legacy", actionKey: nil, result: nil},
		{name: "legacy sets reserved env", actionKey: nil, result: nil, env: []string{"FACETS_RESULT_FILE"}},
		{name: "key only", actionKey: "restart", result: nil},
		{name: "key and result", actionKey: "get_status", result: true, env: []string{"A"}},
		{name: "result without key", actionKey: nil, result: true, wantErr: "requires action_key"},
		{name: "reserved env", actionKey: "get_status", result: true, env: []string{"A", "FACETS_RESULT_FILE"}, wantErr: "reserved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			r, cfg := k8sConfig(ctx, t, tt.actionKey, tt.result, tt.env...)
			resp := &resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfg}, resp)
			assertDiag(t, resp, tt.wantErr)
		})
	}
}

// Read refreshes action_key and result from the Task, so out-of-band edits
// show as drift, while a legacy Task leaves null attributes null.
func TestK8sReadResourceState_SyncsActionContract(t *testing.T) {
	withContract := testfake.Task(k8sReadTestNamespace, k8sReadTestTaskName,
		map[string]string{tekton.ActionKeyLabel: "get_status"})
	_ = unstructured.SetNestedSlice(withContract.Object, tekton.ActionContract{Result: true}.Results(), "spec", "results")
	sa := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, nil)
	r, c := resourceWithFake(withContract, sa)

	state := stateForRead(k8sReadTestNamespace, k8sReadTestTaskName)
	state.StepActionName = types.StringValue(k8sStepActionName)
	if _, diags := r.readResourceState(context.Background(), c, state); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if state.ActionKey.ValueString() != "get_status" || !state.Result.ValueBool() {
		t.Errorf("state not refreshed: action_key=%v result=%v", state.ActionKey, state.Result)
	}

	legacy := testfake.Task(k8sReadTestNamespace, k8sReadTestTaskName, nil)
	r, c = resourceWithFake(legacy, sa)
	state = stateForRead(k8sReadTestNamespace, k8sReadTestTaskName)
	state.StepActionName = types.StringValue(k8sStepActionName)
	state.ActionKey = types.StringNull()
	state.Result = types.BoolNull()
	if _, diags := r.readResourceState(context.Background(), c, state); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !state.ActionKey.IsNull() || !state.Result.IsNull() {
		t.Errorf("legacy Task must leave nulls: action_key=%v result=%v", state.ActionKey, state.Result)
	}
}

func TestSyncActionContract(t *testing.T) {
	legacy := testfake.Task("ns", "t", nil)

	// Null stays null, so configuration that never sets `result` shows no diff.
	key, result := types.StringNull(), types.BoolNull()
	syncActionContract(legacy, &key, &result)
	if !key.IsNull() || !result.IsNull() {
		t.Errorf("legacy: key=%v result=%v, want both null", key, result)
	}

	// Explicit false stays false.
	result = types.BoolValue(false)
	syncActionContract(legacy, &key, &result)
	if result.IsNull() || result.ValueBool() {
		t.Errorf("explicit false became %v", result)
	}

	// A result removed out of band shows as drift from true to false, and a
	// removed label from the key to null.
	key, result = types.StringValue("get_status"), types.BoolValue(true)
	syncActionContract(legacy, &key, &result)
	if !key.IsNull() || result.ValueBool() {
		t.Errorf("drift not detected: key=%v result=%v", key, result)
	}
}

// result must stay plain Optional: a Default (which requires Computed) would put
// `result = false` in the plan of every existing action.
func TestActionContractSchemaShape(t *testing.T) {
	ctx := context.Background()
	for _, r := range []resource.Resource{NewFacetsActionsResource(), NewTektonActionKubernetesResource()} {
		s := resourceSchema(ctx, t, r)
		res, ok := s.Attributes["result"].(schema.BoolAttribute)
		if !ok {
			t.Fatalf("result attribute missing or wrong type: %T", s.Attributes["result"])
		}
		if !res.IsOptional() || res.IsComputed() || res.Default != nil {
			t.Errorf("result should be Optional only, got optional=%v computed=%v default=%v",
				res.IsOptional(), res.IsComputed(), res.Default)
		}
		key, ok := s.Attributes["action_key"].(schema.StringAttribute)
		if !ok || !key.IsOptional() || key.IsComputed() || len(key.PlanModifiers) != 0 {
			t.Errorf("action_key should be Optional with no plan modifiers (in-place update)")
		}
	}
}

// --- helpers --------------------------------------------------------------

func assertNoContract(t *testing.T, task *unstructured.Unstructured) {
	t.Helper()
	if _, found := task.Object["metadata"].(map[string]interface{})["annotations"]; found {
		t.Error("legacy Task gained metadata.annotations")
	}
	if _, found := task.Object["spec"].(map[string]interface{})["results"]; found {
		t.Error("legacy Task gained spec.results")
	}
	if _, found := task.GetLabels()[tekton.ActionKeyLabel]; found {
		t.Error("legacy Task gained the action-key label")
	}
	steps, _, _ := unstructured.NestedSlice(task.Object, "spec", "steps")
	for i, s := range steps {
		for _, n := range envNames(s) {
			if n == tekton.ResultFileEnvVar {
				t.Errorf("legacy step %d gained %s", i, n)
			}
		}
	}
}

func assertContract(t *testing.T, task *unstructured.Unstructured, wantKey string) {
	t.Helper()
	if got := task.GetLabels()[tekton.ActionKeyLabel]; got != wantKey {
		t.Errorf("action-key label = %q, want %q", got, wantKey)
	}
	if got := task.GetAnnotations()[tekton.ResultTransportAnnotation]; got != tekton.ResultTransportTektonResults {
		t.Errorf("result-transport annotation = %q", got)
	}
	got, hasKey := tekton.ActionContractFromTask(task)
	if !hasKey || !got.Result {
		t.Errorf("Task does not declare the facets_result result: %+v", got)
	}
}

func envNames(step interface{}) []string {
	env, _ := step.(map[string]interface{})["env"].([]interface{})
	out := make([]string, 0, len(env))
	for _, e := range env {
		out = append(out, e.(map[string]interface{})["name"].(string))
	}
	return out
}

func assertDiag(t *testing.T, resp *resource.ValidateConfigResponse, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		return
	}
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Detail(), wantErr) {
			return
		}
	}
	t.Errorf("expected an error containing %q, got %v", wantErr, resp.Diagnostics)
}
