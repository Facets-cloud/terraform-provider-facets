package tekton

import (
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The action-result contract shared with the Facets control plane.
//
// A module declares an action, optionally with a JSON-object result schema, in
// its facets.yaml. The action's script writes one JSON object to the file named
// by FACETS_RESULT_FILE; Tekton captures it as the facets_result Task result,
// and the control plane -- notified through Tekton CloudEvents -- re-reads the
// TaskRun and validates status.results[facets_result]. It finds the action by
// the action-key label, which Tekton copies from the Task onto every TaskRun.
//
// Every value here is matched byte-for-byte by the control plane. Do not change
// any of them without changing it there too.
const (
	// ActionKeyLabel identifies the action a Task implements.
	ActionKeyLabel = "facets.cloud/action-key"

	// ResultTransportAnnotation tells the control plane how the result travels.
	ResultTransportAnnotation = "facets.cloud/result-transport"
	// ResultTransportTektonResults is the only transport: a Tekton Task result.
	ResultTransportTektonResults = "tekton-results"

	// ResultName is the name of the Tekton Task result carrying the JSON object.
	ResultName = "facets_result"
	// ResultDescription is the description of that Task result.
	ResultDescription = "Facets action result (JSON object)"

	// ResultFileEnvVar is the variable each user step writes its result through.
	// It is reserved: a step may not set it itself.
	ResultFileEnvVar = "FACETS_RESULT_FILE"
	// ResultFileEnvValue is Tekton's substitution for the result file's path.
	ResultFileEnvValue = "$(results." + ResultName + ".path)"

	// ActionKeyPattern is the control plane's validation for an action key.
	ActionKeyPattern = `^[a-z][a-z0-9_]{0,62}$`
)

var (
	// ActionKeyRegexp is ActionKeyPattern, compiled.
	ActionKeyRegexp = regexp.MustCompile(ActionKeyPattern)

	// ActionKeyEndRegexp additionally requires the key to end alphanumeric.
	// ActionKeyPattern admits a trailing underscore, but the key is written
	// verbatim as a label value, which Kubernetes requires to end alphanumeric;
	// rejecting it here beats a `metadata.labels: Invalid value` at apply time.
	// The key is never sanitized: the control plane matches it byte-for-byte.
	ActionKeyEndRegexp = regexp.MustCompile(`[a-z0-9]$`)
)

// ActionContract is an action's identity and result settings as configured on a
// resource. The zero value -- no key, no result -- renders nothing at all, which
// keeps a Task built without these attributes identical to one built before
// they existed.
type ActionContract struct {
	ActionKey string
	Result    bool
}

// ApplyLabels sets the action-key label on labels when a key is configured.
// It is applied after custom labels are merged, so the generated value wins.
func (c ActionContract) ApplyLabels(labels map[string]interface{}) {
	if c.ActionKey != "" {
		labels[ActionKeyLabel] = c.ActionKey
	}
}

// Annotations returns the Task annotations the contract requires, or nil when
// a result is not enabled.
func (c ActionContract) Annotations() map[string]interface{} {
	if !c.Result {
		return nil
	}
	return map[string]interface{}{
		ResultTransportAnnotation: ResultTransportTektonResults,
	}
}

// Results returns the Task's spec.results, or nil when a result is not enabled.
func (c ActionContract) Results() []interface{} {
	if !c.Result {
		return nil
	}
	return []interface{}{
		map[string]interface{}{
			"name":        ResultName,
			"type":        "string",
			"description": ResultDescription,
		},
	}
}

// ApplyStepEnv appends FACETS_RESULT_FILE to a user step when a result is
// enabled. It must not be called on a StepAction `ref` step: Tekton rejects env
// on those ("env cannot be used with Ref").
func (c ActionContract) ApplyStepEnv(step map[string]interface{}) {
	if c.Result {
		AddEnvVar(step, ResultFileEnvVar, ResultFileEnvValue)
	}
}

// ValidateActionContract checks the combination of attributes: a result is
// reported against an action key, so enabling one without the other leaves the
// control plane nothing to validate it against.
func ValidateActionContract(actionKey string, result bool) error {
	if result && actionKey == "" {
		return fmt.Errorf("result = true requires action_key: the control plane identifies the action, " +
			"and the result schema to validate against, by its key")
	}
	if actionKey == "" {
		return nil
	}
	if !ActionKeyRegexp.MatchString(actionKey) {
		return fmt.Errorf("action_key %q must match %s", actionKey, ActionKeyPattern)
	}
	if !ActionKeyEndRegexp.MatchString(actionKey) {
		return fmt.Errorf("action_key %q must end with a letter or digit: it is used as a Kubernetes label value", actionKey)
	}
	return nil
}

// CheckReservedEnv rejects a step env that sets FACETS_RESULT_FILE. The
// provider injects it, and a second definition would make the result path
// depend on which of the two Kubernetes happens to apply.
func CheckReservedEnv(stepName string, envNames []string) error {
	for _, n := range envNames {
		if n == ResultFileEnvVar {
			return fmt.Errorf("step %q sets %s, which is reserved: the provider sets it when result = true",
				stepName, ResultFileEnvVar)
		}
	}
	return nil
}

// ActionContractFromTask reads the contract back from a rendered Task, so that
// Read and import can detect drift. hasKey is false when the label is absent.
func ActionContractFromTask(task *unstructured.Unstructured) (contract ActionContract, hasKey bool) {
	labels, _, _ := unstructured.NestedStringMap(task.Object, "metadata", "labels")
	contract.ActionKey, hasKey = labels[ActionKeyLabel]

	results, _, _ := unstructured.NestedSlice(task.Object, "spec", "results")
	for _, r := range results {
		if m, ok := r.(map[string]interface{}); ok && m["name"] == ResultName {
			contract.Result = true
			break
		}
	}
	return contract, hasKey
}
