package tekton

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The contract values are matched byte-for-byte by the control plane, so they
// are pinned literally here rather than compared against the constants.
func TestActionContract_ConstantsMatchControlPlane(t *testing.T) {
	pinned := map[string]string{
		ActionKeyLabel:               "facets.cloud/action-key",
		ResultTransportAnnotation:    "facets.cloud/result-transport",
		ResultTransportTektonResults: "tekton-results",
		ResultName:                   "facets_result",
		ResultDescription:            "Facets action result (JSON object)",
		ResultFileEnvVar:             "FACETS_RESULT_FILE",
		ResultFileEnvValue:           "$(results.facets_result.path)",
		ActionKeyPattern:             `^[a-z][a-z0-9_]{0,62}$`,
	}
	for got, want := range pinned {
		if got != want {
			t.Errorf("contract drifted: got %q, want %q", got, want)
		}
	}
}

// The zero contract must render nothing, or every existing action would show a
// diff on its next plan.
func TestActionContract_ZeroValueRendersNothing(t *testing.T) {
	var c ActionContract

	labels := map[string]interface{}{"display_name": "x"}
	c.ApplyLabels(labels)
	if _, ok := labels[ActionKeyLabel]; ok {
		t.Errorf("label added with no action key: %v", labels)
	}
	if c.Annotations() != nil {
		t.Errorf("annotations without a result: %v", c.Annotations())
	}
	if c.Results() != nil {
		t.Errorf("results without a result: %v", c.Results())
	}

	step := map[string]interface{}{"name": "run"}
	c.ApplyStepEnv(step)
	if _, ok := step["env"]; ok {
		t.Errorf("env added without a result: %v", step["env"])
	}
}

func TestActionContract_KeyOnlyAddsLabelButNoResult(t *testing.T) {
	c := ActionContract{ActionKey: "restart_db"}

	labels := map[string]interface{}{}
	c.ApplyLabels(labels)
	if labels[ActionKeyLabel] != "restart_db" {
		t.Errorf("label = %v, want restart_db", labels[ActionKeyLabel])
	}
	if c.Annotations() != nil || c.Results() != nil {
		t.Error("a key alone must not enable the result")
	}
}

func TestActionContract_ResultRendersContract(t *testing.T) {
	c := ActionContract{ActionKey: "get_status", Result: true}

	wantAnn := map[string]interface{}{"facets.cloud/result-transport": "tekton-results"}
	if !reflect.DeepEqual(c.Annotations(), wantAnn) {
		t.Errorf("annotations = %v, want %v", c.Annotations(), wantAnn)
	}

	wantResults := []interface{}{map[string]interface{}{
		"name":        "facets_result",
		"type":        "string",
		"description": "Facets action result (JSON object)",
	}}
	if !reflect.DeepEqual(c.Results(), wantResults) {
		t.Errorf("results = %v, want %v", c.Results(), wantResults)
	}
}

// The result variable is appended after the step's own env, keeping the
// provider-sorted order stable from plan to plan.
func TestActionContract_ApplyStepEnvAppendsLast(t *testing.T) {
	step := map[string]interface{}{
		"env": []interface{}{
			map[string]interface{}{"name": "A", "value": "1"},
		},
	}
	ActionContract{ActionKey: "k", Result: true}.ApplyStepEnv(step)

	env := step["env"].([]interface{})
	if len(env) != 2 {
		t.Fatalf("expected 2 env vars, got %v", env)
	}
	last := env[1].(map[string]interface{})
	if last["name"] != "FACETS_RESULT_FILE" || last["value"] != "$(results.facets_result.path)" {
		t.Errorf("result env = %v", last)
	}
}

func TestValidateActionContract(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		result  bool
		wantErr string
	}{
		{name: "neither", key: "", result: false},
		{name: "key only", key: "restart", result: false},
		{name: "key and result", key: "get_status_2", result: true},
		{name: "single letter", key: "a", result: true},
		{name: "63 chars", key: "a" + strings.Repeat("b", 62), result: false},
		{name: "result without key", key: "", result: true, wantErr: "requires action_key"},
		{name: "uppercase", key: "Restart", wantErr: "must match"},
		{name: "leading digit", key: "1restart", wantErr: "must match"},
		{name: "leading underscore", key: "_restart", wantErr: "must match"},
		{name: "hyphen", key: "re-start", wantErr: "must match"},
		{name: "64 chars", key: "a" + strings.Repeat("b", 63), wantErr: "must match"},
		{name: "trailing underscore", key: "restart_", wantErr: "must end with a letter or digit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateActionContract(tt.key, tt.result)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckReservedEnv(t *testing.T) {
	if err := CheckReservedEnv("run", []string{"A", "B"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := CheckReservedEnv("run", nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	err := CheckReservedEnv("run", []string{"A", "FACETS_RESULT_FILE"})
	if err == nil || !strings.Contains(err.Error(), "reserved") || !strings.Contains(err.Error(), `"run"`) {
		t.Errorf("error = %v, want reserved-name error naming the step", err)
	}
}

// A custom label must not be able to claim an action: the control plane routes
// results by this label.
func TestLabels_CustomActionKeyIsDropped(t *testing.T) {
	m := &ResourceMetadata{
		DisplayName:  "d",
		CustomLabels: map[string]string{ActionKeyLabel: "spoofed", "team": "platform"},
	}
	labels := m.Labels()
	if _, ok := labels[ActionKeyLabel]; ok {
		t.Errorf("custom action-key label survived: %v", labels)
	}
	if labels["team"] != "platform" {
		t.Errorf("unrelated custom label lost: %v", labels)
	}

	// And the generated value wins once a key is configured.
	li := m.LabelsAsInterface()
	ActionContract{ActionKey: "real"}.ApplyLabels(li)
	if li[ActionKeyLabel] != "real" {
		t.Errorf("label = %v, want real", li[ActionKeyLabel])
	}
}

func TestBuildTask_LegacyOutputHasNoAnnotationsOrResults(t *testing.T) {
	task := BuildTask(TaskSpec{
		TaskName:  "abc",
		Namespace: "tekton-pipelines",
		Labels:    map[string]interface{}{"display_name": "x"},
	}, []interface{}{}, []interface{}{})

	meta := task.Object["metadata"].(map[string]interface{})
	if _, ok := meta["annotations"]; ok {
		t.Errorf("legacy Task gained metadata.annotations: %v", meta)
	}
	spec := task.Object["spec"].(map[string]interface{})
	if _, ok := spec["results"]; ok {
		t.Errorf("legacy Task gained spec.results: %v", spec)
	}

	wantMeta := []string{"labels", "name", "namespace"}
	if got := sortedKeys(meta); !reflect.DeepEqual(got, wantMeta) {
		t.Errorf("metadata keys = %v, want %v", got, wantMeta)
	}
	wantSpec := []string{"description", "params", "steps"}
	if got := sortedKeys(spec); !reflect.DeepEqual(got, wantSpec) {
		t.Errorf("spec keys = %v, want %v", got, wantSpec)
	}
}

func TestBuildTask_EmitsAnnotationsAndResults(t *testing.T) {
	c := ActionContract{ActionKey: "get_status", Result: true}
	task := BuildTask(TaskSpec{
		TaskName:    "abc",
		Namespace:   "tekton-pipelines",
		Labels:      map[string]interface{}{},
		Annotations: c.Annotations(),
		Results:     c.Results(),
	}, []interface{}{}, []interface{}{})

	ann, found, _ := unstructured.NestedStringMap(task.Object, "metadata", "annotations")
	if !found || ann[ResultTransportAnnotation] != ResultTransportTektonResults {
		t.Errorf("annotations = %v", ann)
	}
	results, found, _ := unstructured.NestedSlice(task.Object, "spec", "results")
	if !found || len(results) != 1 {
		t.Fatalf("results = %v", results)
	}
	if results[0].(map[string]interface{})["name"] != ResultName {
		t.Errorf("result = %v", results[0])
	}
}

func TestActionContractFromTask(t *testing.T) {
	c := ActionContract{ActionKey: "get_status", Result: true}
	labels := map[string]interface{}{}
	c.ApplyLabels(labels)
	task := BuildTask(TaskSpec{
		TaskName: "abc", Namespace: "ns", Labels: labels,
		Annotations: c.Annotations(), Results: c.Results(),
	}, []interface{}{}, []interface{}{})

	got, hasKey := ActionContractFromTask(task)
	if !hasKey || got != c {
		t.Errorf("round trip = %+v (hasKey=%v), want %+v", got, hasKey, c)
	}

	legacy := BuildTask(TaskSpec{TaskName: "abc", Namespace: "ns", Labels: map[string]interface{}{}},
		[]interface{}{}, []interface{}{})
	got, hasKey = ActionContractFromTask(legacy)
	if hasKey || got != (ActionContract{}) {
		t.Errorf("legacy Task read as %+v (hasKey=%v)", got, hasKey)
	}
}

func sortedKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
