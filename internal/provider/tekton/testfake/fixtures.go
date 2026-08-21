package testfake

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Task returns an *unstructured.Unstructured representing a minimal Tekton
// Task in the given namespace. Labels are merged into metadata.labels.
// The object's apiVersion/kind are set to match the production GVR.
//
// The body intentionally lacks a `spec` because the bugs under test live in
// the resource lifecycle plumbing, not in spec correctness.
func Task(namespace, name string, labels map[string]string) *unstructured.Unstructured {
	return buildTektonObject("Task", namespace, name, labels)
}

// StepAction returns a fixture matching the production StepAction shape.
// Use the same conventions as Task — name should match the resource's
// generated `step_action_name` (`setup-credentials-<hash>`) for realism.
func StepAction(namespace, name string, labels map[string]string) *unstructured.Unstructured {
	return buildTektonObject("StepAction", namespace, name, labels)
}

// buildTektonObject constructs a minimal unstructured object with the given
// kind, namespace, name, and labels. Internal helper for Task and StepAction.
func buildTektonObject(kind, namespace, name string, labels map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "tekton.dev/v1beta1",
			"kind":       kind,
			"metadata": map[string]any{
				"namespace": namespace,
				"name":      name,
			},
		},
	}

	if labels != nil {
		labelsMap := make(map[string]any, len(labels))
		for k, v := range labels {
			labelsMap[k] = v
		}
		_ = unstructured.SetNestedMap(obj.Object, labelsMap, "metadata", "labels")
	}

	return obj
}

// Secret returns a core/v1 Secret fixture. `data` values are given in PLAIN
// text and stored under stringData, matching how the provider writes them --
// the fake client does no base64 encoding, so tests compare plain values.
func Secret(namespace, name string, data map[string]string) *unstructured.Unstructured {
	sd := make(map[string]any, len(data))
	for k, v := range data {
		sd[k] = v
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"namespace": namespace,
				"name":      name,
			},
			"type":       "Opaque",
			"stringData": sd,
		},
	}
}

// ConfigMap returns a core/v1 ConfigMap fixture. Used for Tekton's
// config-defaults, which decides whether OIDC federation can work at all.
func ConfigMap(namespace, name string, data map[string]string) *unstructured.Unstructured {
	d := make(map[string]any, len(data))
	for k, v := range data {
		d[k] = v
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"namespace": namespace,
				"name":      name,
			},
			"data": d,
		},
	}
}
