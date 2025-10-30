package provider

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"regexp"

	"github.com/facets-cloud/terraform-provider-facets/internal/k8s"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	_ resource.Resource                = &TektonActionKubernetesResource{}
	_ resource.ResourceWithConfigure   = &TektonActionKubernetesResource{}
	_ resource.ResourceWithImportState = &TektonActionKubernetesResource{}
)

func NewTektonActionKubernetesResource() resource.Resource {
	return &TektonActionKubernetesResource{}
}

type TektonActionKubernetesResource struct {
	client dynamic.Interface
}

type TektonActionKubernetesResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	FacetsResourceName types.String `tfsdk:"facets_resource_name"`
	FacetsEnvironment  types.Object `tfsdk:"facets_environment"`
	FacetsResource     types.Object `tfsdk:"facets_resource"`
	Namespace          types.String `tfsdk:"namespace"`
	Steps              types.List   `tfsdk:"steps"`
	Params             types.List   `tfsdk:"params"`
	TaskName           types.String `tfsdk:"task_name"`
	StepActionName     types.String `tfsdk:"step_action_name"`
}

type FacetsEnvironmentModel struct {
	UniqueName types.String `tfsdk:"unique_name"`
}

type FacetsResourceModel struct {
	Kind types.String `tfsdk:"kind"`
}

type ParamModel struct {
	Name types.String `tfsdk:"name"`
	Type types.String `tfsdk:"type"`
}

type StepModel struct {
	Name      types.String `tfsdk:"name"`
	Image     types.String `tfsdk:"image"`
	Script    types.String `tfsdk:"script"`
	Resources types.Object `tfsdk:"resources"`
	Env       types.List   `tfsdk:"env"`
}

type ComputeResourcesModel struct {
	Requests types.Map `tfsdk:"requests"`
	Limits   types.Map `tfsdk:"limits"`
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
		Description: "Manages a Tekton Task and StepAction for Kubernetes-based workflows. " +
			"This resource automatically injects Kubernetes credentials (FACETS_USER_KUBECONFIG) " +
			"via a setup-credentials step, which is populated by the Facets UI when users run actions. " +
			"The kubeconfig is scoped to the user's RBAC permissions.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource identifier",
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Display name of the Tekton Task",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.LengthAtMost(253),
				},
			},
			"description": schema.StringAttribute{
				Description: "Description of the Tekton Task",
				Optional:    true,
			},
			"facets_resource_name": schema.StringAttribute{
				Description: "Resource name as defined in the Facets blueprint. " +
					"Used to map the Tekton task back to the blueprint resource in Facets.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.LengthAtMost(253),
				},
			},
			"facets_environment": schema.SingleNestedAttribute{
				Description: "Facets-managed environment configuration. " +
					"Specifies which environment this action runs in.",
				Required: true,
				Attributes: map[string]schema.Attribute{
					"unique_name": schema.StringAttribute{
						Description: "Unique name of the Facets-managed environment",
						Required:    true,
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
							stringvalidator.LengthAtMost(253),
						},
					},
				},
			},
			"facets_resource": schema.SingleNestedAttribute{
				Description: "Resource definition as specified in the Facets blueprint. " +
					"Only the 'kind' field is used by the provider (in resource labels). " +
					"Other fields like 'flavor', 'version', and 'spec' can be provided but are silently ignored.",
				Required: true,
				Attributes: map[string]schema.Attribute{
					"kind": schema.StringAttribute{
						Description: "Resource kind (used in resource labels)",
						Required:    true,
					},
				},
			},
			"namespace": schema.StringAttribute{
				Description: "Kubernetes namespace for Tekton resources",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`),
						"must be a valid Kubernetes namespace name (lowercase alphanumeric and hyphens, cannot start or end with hyphen)",
					),
					stringvalidator.LengthAtMost(63),
				},
			},
			"steps": schema.ListNestedAttribute{
				Description: "List of steps for the Tekton Task",
				Required:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "Step name",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
								stringvalidator.LengthAtMost(253),
							},
						},
						"image": schema.StringAttribute{
							Description: "Container image for the step",
							Required:    true,
						},
						"script": schema.StringAttribute{
							Description: "Script to execute in the step",
							Required:    true,
						},
						"resources": schema.SingleNestedAttribute{
							Description: "Compute resources (requests and limits) for the step",
							Optional:    true,
							Attributes: map[string]schema.Attribute{
								"requests": schema.MapAttribute{
									Description: "Minimum compute resources required (e.g., cpu, memory)",
									Optional:    true,
									ElementType: types.StringType,
								},
								"limits": schema.MapAttribute{
									Description: "Maximum compute resources allowed (e.g., cpu, memory)",
									Optional:    true,
									ElementType: types.StringType,
								},
							},
						},
						"env": schema.ListNestedAttribute{
							Description: "Environment variables for the step",
							Optional:    true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										Description: "Environment variable name",
										Required:    true,
										Validators: []validator.String{
											stringvalidator.LengthAtLeast(1),
											stringvalidator.RegexMatches(
												regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`),
												"must be a valid environment variable name (uppercase letters, numbers, and underscores, cannot start with a number)",
											),
										},
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
			"params": schema.ListNestedAttribute{
				Description: "List of params for the Tekton Task",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "Parameter name",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
								stringvalidator.LengthAtMost(253),
							},
						},
						"type": schema.StringAttribute{
							Description: "Parameter type (e.g., string, array)",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.OneOf("string", "array", "object"),
							},
						},
					},
				},
			},
			"task_name": schema.StringAttribute{
				Description: "Generated Tekton Task name (computed from hash of resource_name, environment, and name). " +
					"This is the actual Kubernetes resource name and may be truncated to 63 characters.",
				Computed: true,
			},
			"step_action_name": schema.StringAttribute{
				Description: "Generated StepAction name for credential setup (computed from hash). " +
					"This StepAction automatically configures Kubernetes access for the workflow steps.",
				Computed: true,
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
	var facetsEnv FacetsEnvironmentModel
	resp.Diagnostics.Append(plan.FacetsEnvironment.As(ctx, &facetsEnv, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Extract resource_kind from facets_resource object
	var facetsRes FacetsResourceModel
	resp.Diagnostics.Append(plan.FacetsResource.As(ctx, &facetsRes, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Generate names using hash for uniqueness
	taskName, stepActionName := generateResourceNames(
		plan.FacetsResourceName.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		plan.Name.ValueString(),
	)
	plan.TaskName = types.StringValue(taskName)
	plan.StepActionName = types.StringValue(stepActionName)
	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", plan.Namespace.ValueString(), taskName))

	// Read cluster_id from environment variable
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		clusterID = "na"
	}

	// Create labels
	labels := buildLabels(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		facetsRes.Kind.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		clusterID,
	)

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
	var state TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Use state values for computed fields (StepActionName, TaskName)
	// These are computed and unknown in the plan
	plan.StepActionName = state.StepActionName
	plan.TaskName = state.TaskName
	plan.ID = state.ID
	plan.Namespace = state.Namespace

	// Extract environment unique_name from environment object
	var facetsEnv FacetsEnvironmentModel
	resp.Diagnostics.Append(plan.FacetsEnvironment.As(ctx, &facetsEnv, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Extract resource_kind from facets_resource object
	var facetsRes FacetsResourceModel
	resp.Diagnostics.Append(plan.FacetsResource.As(ctx, &facetsRes, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read cluster_id from environment variable
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		clusterID = "na"
	}

	// Create labels
	labels := buildLabels(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		facetsRes.Kind.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		clusterID,
	)

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

func (r *TektonActionKubernetesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: namespace/taskName
	// Example: tekton-pipelines/59f6f855860ddc99a32e2944c96db5fa

	idParts := regexp.MustCompile(`^([^/]+)/([^/]+)$`).FindStringSubmatch(req.ID)
	if len(idParts) != 3 {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected import ID in format: namespace/taskName, got: %s", req.ID),
		)
		return
	}

	namespace := idParts[1]
	taskName := idParts[2]

	// Verify Task exists
	gvr := k8sschema.GroupVersionResource{
		Group:    "tekton.dev",
		Version:  "v1beta1",
		Resource: "tasks",
	}

	task, err := r.client.Resource(gvr).Namespace(namespace).Get(ctx, taskName, metav1.GetOptions{})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error importing resource",
			fmt.Sprintf("Could not find Task %s/%s: %s", namespace, taskName, err.Error()),
		)
		return
	}

	// Extract metadata from labels
	labels, found, _ := unstructured.NestedStringMap(task.Object, "metadata", "labels")
	if !found {
		resp.Diagnostics.AddError(
			"Error importing resource",
			"Task does not have required labels for import",
		)
		return
	}

	displayName, hasDisplayName := labels["display_name"]
	resourceName, hasResourceName := labels["resource_name"]
	_, hasResourceKind := labels["resource_kind"]
	_, hasEnvUniqueName := labels["environment_unique_name"]

	if !hasDisplayName || !hasResourceName || !hasResourceKind || !hasEnvUniqueName {
		resp.Diagnostics.AddError(
			"Error importing resource",
			"Task missing required labels: display_name, resource_name, resource_kind, environment_unique_name",
		)
		return
	}

	// Find StepAction (convention: setup-credentials-{hash})
	stepActionName := fmt.Sprintf("setup-credentials-%s", taskName)

	// Set state with imported values
	state := TektonActionKubernetesResourceModel{
		ID:                 types.StringValue(fmt.Sprintf("%s/%s", namespace, taskName)),
		Name:               types.StringValue(displayName),
		FacetsResourceName: types.StringValue(resourceName),
		Namespace:          types.StringValue(namespace),
		TaskName:           types.StringValue(taskName),
		StepActionName:     types.StringValue(stepActionName),
	}

	// Note: We cannot fully reconstruct facets_environment, facets_resource, steps, params from the Task
	// User will need to manually specify these in their configuration
	resp.Diagnostics.AddWarning(
		"Partial Import",
		"Only basic fields were imported. You must manually specify: facets_environment, facets_resource, steps, and params in your configuration.",
	)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Helper functions for testability

// generateResourceNames creates deterministic names for Task and StepAction
// Returns (taskName, stepActionName)
func generateResourceNames(resourceName, envName, displayName string) (string, string) {
	hashInput := fmt.Sprintf("%s-%s-%s", resourceName, envName, displayName)
	nameHash := fmt.Sprintf("%x", md5.Sum([]byte(hashInput)))

	// Build stepActionName with prefix
	stepActionName := fmt.Sprintf("setup-credentials-%s", nameHash)
	if len(stepActionName) > 63 {
		// Keep last 63 chars to preserve unique hash suffix
		stepActionName = stepActionName[len(stepActionName)-63:]
	}

	// TaskName is just the hash
	taskName := nameHash
	if len(taskName) > 63 {
		taskName = taskName[len(taskName)-63:]
	}

	return taskName, stepActionName
}

// buildLabels creates the standard label map for Tekton resources
func buildLabels(displayName, resourceName, resourceKind, envUniqueName, clusterID string) map[string]interface{} {
	return map[string]interface{}{
		"display_name":            displayName,
		"resource_name":           resourceName,
		"resource_kind":           resourceKind,
		"environment_unique_name": envUniqueName,
		"cluster_id":              clusterID,
	}
}

// extractMetadata extracts namespace and name from an unstructured object
// Returns (namespace, name, error)
func extractMetadata(obj *unstructured.Unstructured) (string, string, error) {
	metadata, hasMetadata := obj.Object["metadata"]
	if !hasMetadata {
		return "", "", fmt.Errorf("no metadata key in object")
	}

	metadataMap, isMap := metadata.(map[string]interface{})
	if !isMap {
		return "", "", fmt.Errorf("metadata is not a map: %T", metadata)
	}

	namespace, hasNS := metadataMap["namespace"].(string)
	name, hasName := metadataMap["name"].(string)

	if !hasNS || !hasName || namespace == "" || name == "" {
		return "", "", fmt.Errorf("missing or empty namespace/name: hasNS=%v ns=%s, hasName=%v name=%s", hasNS, namespace, hasName, name)
	}

	return namespace, name, nil
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

		// Add computeResources if provided
		if !step.Resources.IsNull() {
			var computeRes ComputeResourcesModel
			diags := step.Resources.As(ctx, &computeRes, basetypes.ObjectAsOptions{})
			if diags.HasError() {
				// Skip this step's resources if conversion fails
				// The error will be logged but won't fail the entire build
				continue
			}

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

	// Add user-defined params
	if !plan.Params.IsNull() {
		var params []ParamModel
		plan.Params.ElementsAs(ctx, &params, false)
		for _, param := range params {
			taskParams = append(taskParams, map[string]interface{}{
				"name": param.Name.ValueString(),
				"type": param.Type.ValueString(),
			})
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

	// Extract namespace and name from metadata
	namespace, name, err := extractMetadata(obj)
	if err != nil {
		return err
	}

	// Get current resource to preserve resourceVersion
	current, err := r.client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get current resource %s/%s: %w", namespace, name, err)
	}

	// Preserve resourceVersion for optimistic locking
	obj.SetResourceVersion(current.GetResourceVersion())

	_, err = r.client.Resource(gvr).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
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
