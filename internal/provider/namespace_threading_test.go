package provider

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// `namespace` was accepted by the schema but only half-applied: threaded through
// for the Task and Secret, hardcoded to tekton-pipelines for the StepAction, Read,
// Delete, Import and the ID. A StepAction is only resolvable from a TaskRun in the
// SAME namespace (Tekton taskref.go), so any non-default namespace broke every
// action and left a permanent drift warning.
//
// This is a source-level guard: exercising Create/Read/Delete needs the full
// plugin-framework harness, but the failure mode is simply "a k8s call that should
// use the resolved namespace uses the constant instead", which is greppable.
func TestNamespaceIsNotHardcodedInK8sCalls(t *testing.T) {
	src, err := os.ReadFile("resource_tekton_action_azure.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)

	// Every .Namespace(...) call must take a variable, never the constant.
	for _, m := range regexp.MustCompile(`\.Namespace\((\w+)\)`).FindAllStringSubmatch(text, -1) {
		if m[1] == "tektonPipelinesNamespace" {
			t.Errorf("k8s call uses the hardcoded namespace: %s", m[0])
		}
	}

	// The constant may only appear as a default/fallback assignment.
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "tektonPipelinesNamespace") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		isDefault := strings.Contains(trimmed, "= tektonPipelinesNamespace") ||
			strings.Contains(trimmed, "types.StringValue(tektonPipelinesNamespace)") ||
			strings.HasPrefix(trimmed, "//")
		if !isDefault {
			t.Errorf("unexpected use of the hardcoded namespace: %s", trimmed)
		}
	}

	// Import must honour a namespace given in the ID rather than discarding it.
	if strings.Contains(text, "namespace is ignored") {
		t.Error("ImportState still documents discarding the namespace")
	}
}
