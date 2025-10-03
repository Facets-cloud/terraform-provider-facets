package provider

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"

	"github.com/facets-cloud/terraform-provider-facets/internal/k8s"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	_ resource.Resource              = &TektonActionKubernetesResource{}
	_ resource.ResourceWithConfigure = &TektonActionKubernetesResource{}
)

func NewTektonActionKubernetesResource() resource.Resource {
	return &TektonActionKubernetesResource{}
}

type TektonActionKubernetesResource struct {
	client dynamic.Interface
}

type TektonActionKubernetesResourceModel struct {
	ID             types.String  `tfsdk:"id"`
	Name           types.String  `tfsdk:"name"`
	Description    types.String  `tfsdk:"description"`
	InstanceName   types.String  `tfsdk:"instance_name"`
	Environment    types.Dynamic `tfsdk:"environment"`
	Instance       types.Dynamic `tfsdk:"instance"`
	Namespace      types.String  `tfsdk:"namespace"`
	Steps          types.List    `tfsdk:"steps"`
	Params         types.Dynamic `tfsdk:"params"`
	TaskName       types.String  `tfsdk:"task_name"`
	StepActionName types.String  `tfsdk:"step_action_name"`
}

type StepModel struct {
	Name      types.String `tfsdk:"name"`
	Image     types.String `tfsdk:"image"`
	Script    types.String `tfsdk:"script"`
	Resources types.Object `tfsdk:"resources"`
	Env       types.List   `tfsdk:"env"`
}

type EnvVarModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}


func (r *TektonActionKubernetesResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tekton_action_kubernetes"
}

func (r *TektonActionKubernetesResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Tekton Task and StepAction for Kubernetes-based workflows",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource identifier",
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Display name of the Tekton Task",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Description of the Tekton Task",
				Optional:    true,
			},
			"instance_name": schema.StringAttribute{
				Description: "Resource instance name",
				Required:    true,
			},
			"environment": schema.DynamicAttribute{
				Description: "Environment object (any type)",
				Required:    true,
			},
			"instance": schema.DynamicAttribute{
				Description: "Instance object (any type)",
				Required:    true,
			},
			"namespace": schema.StringAttribute{
				Description: "Kubernetes namespace for Tekton resources",
				Optional:    true,
				Computed:    true,
			},
			"steps": schema.ListNestedAttribute{
				Description: "List of steps for the Tekton Task",
				Required:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "Step name",
							Required:    true,
						},
						"image": schema.StringAttribute{
							Description: "Container image for the step",
							Required:    true,
						},
						"script": schema.StringAttribute{
							Description: "Script to execute in the step",
							Required:    true,
						},
						"resources": schema.ObjectAttribute{
							Description:    "Resource requests and limits",
							Required:       true,
							AttributeTypes: map[string]attr.Type{},
						},
						"env": schema.ListNestedAttribute{
							Description: "Environment variables for the step",
							Optional:    true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										Description: "Environment variable name",
										Required:    true,
									},
									"value": schema.StringAttribute{
										Description: "Environment variable value",
										Required:    true,
									},
								},
							},
						},
					},
				},
			},
			"params": schema.DynamicAttribute{
				Description: "List of params for the Tekton Task (any type)",
				Optional:    true,
			},
			"task_name": schema.StringAttribute{
				Description: "Generated Tekton Task name",
				Computed:    true,
			},
			"step_action_name": schema.StringAttribute{
				Description: "Generated StepAction name",
				Computed:    true,
			},
		},
	}
}

func (r *TektonActionKubernetesResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Always create Kubernetes client
	client, err := k8s.GetKubernetesClient()
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Kubernetes Client",
			fmt.Sprintf("Failed to create Kubernetes client: %s", err.Error()),
		)
		return
	}

	r.client = client
}

func (r *TektonActionKubernetesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Set defaults
	if plan.Namespace.IsNull() || plan.Namespace.ValueString() == "" {
		plan.Namespace = types.StringValue("tekton-pipelines")
	}

	// Extract environment unique_name from environment object
	var envMap map[string]interface{}
	if err := json.Unmarshal([]byte(plan.Environment.String()), &envMap); err != nil {
		resp.Diagnostics.AddError(
			"Error parsing environment",
			fmt.Sprintf("Could not parse environment object: %s", err.Error()),
		)
		return
	}
	envName := ""
	if uniqueName, ok := envMap["unique_name"].(string); ok {
		envName = uniqueName
	}

	// Extract cluster_id and resource_kind from instance object
	var instanceMap map[string]interface{}
	if err := json.Unmarshal([]byte(plan.Instance.String()), &instanceMap); err != nil {
		resp.Diagnostics.AddError(
			"Error parsing instance",
			fmt.Sprintf("Could not parse instance object: %s", err.Error()),
		)
		return
	}
	resourceKind := ""
	if kind, ok := instanceMap["kind"].(string); ok {
		resourceKind = kind
	}

	// Generate names
	nameHash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s-%s-%s", plan.InstanceName.ValueString(), envName, plan.Name.ValueString()))))

	stepActionName := fmt.Sprintf("setup-credentials-%s", nameHash)
	if len(stepActionName) > 63 {
		stepActionName = stepActionName[:63]
	}
	plan.StepActionName = types.StringValue(stepActionName)

	taskName := nameHash
	if len(taskName) > 63 {
		taskName = taskName[:63]
	}
	plan.TaskName = types.StringValue(taskName)
	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", plan.Namespace.ValueString(), taskName))

	// Read deployment context for cluster_id
	clusterID := ""
	deploymentContextData, err := os.ReadFile("/sources/primary/capillary-cloud-tf/deploymentcontext.json")
	if err == nil {
		var deploymentContext map[string]interface{}
		if err := json.Unmarshal(deploymentContextData, &deploymentContext); err == nil {
			if cluster, ok := deploymentContext["cluster"].(map[string]interface{}); ok {
				if id, ok := cluster["id"].(string); ok {
					clusterID = id
				}
			}
		}
	}

	// Create labels
	labels := map[string]interface{}{
		"display_name":            plan.Name.ValueString(),
		"resource_name":           plan.InstanceName.ValueString(),
		"resource_kind":           resourceKind,
		"environment_unique_name": envName,
		"cluster_id":              clusterID,
	}

	// Create StepAction
	stepAction := r.buildStepAction(plan, labels)
	if err := r.createResource(ctx, stepAction, "tekton.dev", "v1beta1", "stepactions"); err != nil {
		resp.Diagnostics.AddError(
			"Error creating StepAction",
			fmt.Sprintf("Could not create StepAction: %s", err.Error()),
		)
		return
	}

	// Create Task
	task := r.buildTask(ctx, plan, labels)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.createResource(ctx, task, "tekton.dev", "v1beta1", "tasks"); err != nil {
		resp.Diagnostics.AddError(
			"Error creating Task",
			fmt.Sprintf("Could not create Task: %s", err.Error()),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *TektonActionKubernetesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Verify Task exists
	gvr := k8sschema.GroupVersionResource{
		Group:    "tekton.dev",
		Version:  "v1beta1",
		Resource: "tasks",
	}

	_, err := r.client.Resource(gvr).Namespace(state.Namespace.ValueString()).Get(ctx, state.TaskName.ValueString(), metav1.GetOptions{})
	if err != nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TektonActionKubernetesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Extract environment unique_name from environment object
	var envMap map[string]interface{}
	if err := json.Unmarshal([]byte(plan.Environment.String()), &envMap); err != nil {
		resp.Diagnostics.AddError(
			"Error parsing environment",
			fmt.Sprintf("Could not parse environment object: %s", err.Error()),
		)
		return
	}
	envName := ""
	if uniqueName, ok := envMap["unique_name"].(string); ok {
		envName = uniqueName
	}

	// Extract resource_kind from instance object
	var instanceMap map[string]interface{}
	if err := json.Unmarshal([]byte(plan.Instance.String()), &instanceMap); err != nil {
		resp.Diagnostics.AddError(
			"Error parsing instance",
			fmt.Sprintf("Could not parse instance object: %s", err.Error()),
		)
		return
	}
	resourceKind := ""
	if kind, ok := instanceMap["kind"].(string); ok {
		resourceKind = kind
	}

	// Read deployment context for cluster_id
	clusterID := ""
	deploymentContextData, err := os.ReadFile("/sources/primary/capillary-cloud-tf/deploymentcontext.json")
	if err == nil {
		var deploymentContext map[string]interface{}
		if err := json.Unmarshal(deploymentContextData, &deploymentContext); err == nil {
			if cluster, ok := deploymentContext["cluster"].(map[string]interface{}); ok {
				if id, ok := cluster["id"].(string); ok {
					clusterID = id
				}
			}
		}
	}

	// Create labels
	labels := map[string]interface{}{
		"display_name":            plan.Name.ValueString(),
		"resource_name":           plan.InstanceName.ValueString(),
		"resource_kind":           resourceKind,
		"environment_unique_name": envName,
		"cluster_id":              clusterID,
	}

	// Update StepAction
	stepAction := r.buildStepAction(plan, labels)
	if err := r.updateResource(ctx, stepAction, "tekton.dev", "v1beta1", "stepactions"); err != nil {
		resp.Diagnostics.AddError(
			"Error updating StepAction",
			fmt.Sprintf("Could not update StepAction: %s", err.Error()),
		)
		return
	}

	// Update Task
	task := r.buildTask(ctx, plan, labels)
	if err := r.updateResource(ctx, task, "tekton.dev", "v1beta1", "tasks"); err != nil {
		resp.Diagnostics.AddError(
			"Error updating Task",
			fmt.Sprintf("Could not update Task: %s", err.Error()),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *TektonActionKubernetesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Delete Task
	if err := r.deleteResource(ctx, state.Namespace.ValueString(), state.TaskName.ValueString(), "tekton.dev", "v1beta1", "tasks"); err != nil {
		resp.Diagnostics.AddError(
			"Error deleting Task",
			fmt.Sprintf("Could not delete Task: %s", err.Error()),
		)
		return
	}

	// Delete StepAction
	if err := r.deleteResource(ctx, state.Namespace.ValueString(), state.StepActionName.ValueString(), "tekton.dev", "v1beta1", "stepactions"); err != nil {
		resp.Diagnostics.AddError(
			"Error deleting StepAction",
			fmt.Sprintf("Could not delete StepAction: %s", err.Error()),
		)
		return
	}
}

func (r *TektonActionKubernetesResource) buildStepAction(plan TektonActionKubernetesResourceModel, labels map[string]interface{}) *unstructured.Unstructured {
	stepAction := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tekton.dev/v1beta1",
			"kind":       "StepAction",
			"metadata": map[string]interface{}{
				"name":      plan.StepActionName.ValueString(),
				"namespace": plan.Namespace.ValueString(),
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

	return stepAction
}

func (r *TektonActionKubernetesResource) buildTask(ctx context.Context, plan TektonActionKubernetesResourceModel, labels map[string]interface{}) *unstructured.Unstructured {
	// Build steps
	var steps []StepModel
	plan.Steps.ElementsAs(ctx, &steps, false)

	tektonSteps := []interface{}{
		map[string]interface{}{
			"name": "setup-credentials",
			"ref": map[string]interface{}{
				"name": plan.StepActionName.ValueString(),
			},
			"params": []interface{}{
				map[string]interface{}{
					"name":  "FACETS_USER_KUBECONFIG",
					"value": "$(params.FACETS_USER_KUBECONFIG)",
				},
			},
		},
	}

	for _, step := range steps {
		tektonStep := map[string]interface{}{
			"name":   step.Name.ValueString(),
			"image":  step.Image.ValueString(),
			"script": step.Script.ValueString(),
		}

		// Add env vars with KUBECONFIG
		var envVars []EnvVarModel
		if !step.Env.IsNull() {
			step.Env.ElementsAs(ctx, &envVars, false)
		}

		envList := []interface{}{}
		for _, env := range envVars {
			envList = append(envList, map[string]interface{}{
				"name":  env.Name.ValueString(),
				"value": env.Value.ValueString(),
			})
		}
		envList = append(envList, map[string]interface{}{
			"name":  "KUBECONFIG",
			"value": "/workspace/.kube/config",
		})
		tektonStep["env"] = envList

		// Add resources if provided
		if !step.Resources.IsNull() {
			// Resources is an object, convert to map
			resourcesMap := step.Resources.Attributes()
			if len(resourcesMap) > 0 {
				resources := make(map[string]interface{})
				for k, v := range resourcesMap {
					resources[k] = v
				}
				tektonStep["resources"] = resources
			}
		}

		tektonSteps = append(tektonSteps, tektonStep)
	}

	// Build params
	taskParams := []interface{}{
		map[string]interface{}{
			"name": "FACETS_USER_EMAIL",
			"type": "string",
		},
		map[string]interface{}{
			"name": "FACETS_USER_KUBECONFIG",
			"type": "string",
		},
	}

	// Params can be any type, parse from dynamic
	if !plan.Params.IsNull() {
		var params interface{}
		if err := json.Unmarshal([]byte(plan.Params.String()), &params); err == nil {
			// If params is a list, append each element
			if paramsList, ok := params.([]interface{}); ok {
				taskParams = append(taskParams, paramsList...)
			}
		}
	}

	description := plan.TaskName.ValueString()
	if !plan.Description.IsNull() && plan.Description.ValueString() != "" {
		description = plan.Description.ValueString()
	}

	task := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tekton.dev/v1beta1",
			"kind":       "Task",
			"metadata": map[string]interface{}{
				"name":      plan.TaskName.ValueString(),
				"namespace": plan.Namespace.ValueString(),
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"description": description,
				"steps":       tektonSteps,
				"params":      taskParams,
			},
		},
	}

	return task
}

func (r *TektonActionKubernetesResource) createResource(ctx context.Context, obj *unstructured.Unstructured, group, version, resource string) error {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	namespace := obj.GetNamespace()
	_, err := r.client.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	return err
}

func (r *TektonActionKubernetesResource) updateResource(ctx context.Context, obj *unstructured.Unstructured, group, version, resource string) error {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	namespace := obj.GetNamespace()
	_, err := r.client.Resource(gvr).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

func (r *TektonActionKubernetesResource) deleteResource(ctx context.Context, namespace, name, group, version, resource string) error {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	return r.client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}
