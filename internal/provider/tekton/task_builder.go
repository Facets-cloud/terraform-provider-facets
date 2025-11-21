package tekton

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TaskSpec contains the specification for building a Tekton Task
type TaskSpec struct {
	TaskName    string
	Namespace   string
	Description string
	Labels      map[string]interface{}
}

// BuildStepWithResources builds a Tekton step with environment variables and compute resources
func BuildStepWithResources(ctx context.Context, step StepModel) map[string]interface{} {
	tektonStep := map[string]interface{}{
		"name":   step.Name.ValueString(),
		"image":  step.Image.ValueString(),
		"script": step.Script.ValueString(),
	}

	// Add env vars if present
	if !step.Env.IsNull() {
		var envVars []EnvVarModel
		step.Env.ElementsAs(ctx, &envVars, false)

		envList := []interface{}{}
		for _, env := range envVars {
			envList = append(envList, map[string]interface{}{
				"name":  env.Name.ValueString(),
				"value": env.Value.ValueString(),
			})
		}
		tektonStep["env"] = envList
	}

	// Add compute resources if present
	if !step.Resources.IsNull() {
		var computeRes ComputeResourcesModel
		diags := step.Resources.As(ctx, &computeRes, basetypes.ObjectAsOptions{})
		if !diags.HasError() {
			computeResources := make(map[string]interface{})

			if !computeRes.Requests.IsNull() {
				requestsMap := make(map[string]string)
				computeRes.Requests.ElementsAs(ctx, &requestsMap, false)
				if len(requestsMap) > 0 {
					computeResources["requests"] = requestsMap
				}
			}

			if !computeRes.Limits.IsNull() {
				limitsMap := make(map[string]string)
				computeRes.Limits.ElementsAs(ctx, &limitsMap, false)
				if len(limitsMap) > 0 {
					computeResources["limits"] = limitsMap
				}
			}

			if len(computeResources) > 0 {
				tektonStep["computeResources"] = computeResources
			}
		}
	}

	return tektonStep
}

// AddEnvVar adds or appends an environment variable to a step
func AddEnvVar(step map[string]interface{}, name, value string) {
	var envList []interface{}
	if existingEnv, ok := step["env"].([]interface{}); ok {
		envList = existingEnv
	}

	envList = append(envList, map[string]interface{}{
		"name":  name,
		"value": value,
	})

	step["env"] = envList
}

// BuildTask creates a Tekton Task from the spec
func BuildTask(spec TaskSpec, steps []interface{}, params []interface{}) *unstructured.Unstructured {
	description := spec.TaskName
	if spec.Description != "" {
		description = spec.Description
	}

	// Define a workspace for sharing files between steps
	workspaces := []interface{}{
		map[string]interface{}{
			"name":        "shared-data",
			"description": "Workspace for sharing helper scripts and data between steps",
		},
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tekton.dev/v1beta1",
			"kind":       "Task",
			"metadata": map[string]interface{}{
				"name":      spec.TaskName,
				"namespace": spec.Namespace,
				"labels":    spec.Labels,
			},
			"spec": map[string]interface{}{
				"description": description,
				"steps":       steps,
				"params":      params,
				"workspaces":  workspaces,
			},
		},
	}
}
