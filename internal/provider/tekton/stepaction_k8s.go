package tekton

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BuildKubernetesStepAction creates a StepAction for Kubernetes credential setup
// This StepAction decodes the base64-encoded FACETS_USER_KUBECONFIG and writes it to /workspace/.kube/config
func BuildKubernetesStepAction(stepActionName, namespace string, labels map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tekton.dev/v1beta1",
			"kind":       "StepAction",
			"metadata": map[string]interface{}{
				"name":      stepActionName,
				"namespace": namespace,
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"image": "facetscloud/actions-base-image:v1.0.0",
				"script": `#!/bin/bash
set -e
mkdir -p /workspace/.kube
echo -n "$FACETS_USER_KUBECONFIG" | base64 -d > /workspace/.kube/config
export KUBECONFIG=/workspace/.kube/config
`,
				"params": []interface{}{
					map[string]interface{}{
						"name": "FACETS_USER_KUBECONFIG",
						"type": "string",
					},
				},
				"env": []interface{}{
					map[string]interface{}{
						"name":  "FACETS_USER_KUBECONFIG",
						"value": "$(params.FACETS_USER_KUBECONFIG)",
					},
				},
			},
		},
	}
}
