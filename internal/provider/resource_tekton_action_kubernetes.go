package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/facets-cloud/terraform-provider-facets/internal/k8s"
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	return &TektonActionKubernetesResource{
		clientFactory: k8s.GetKubernetesClient,
	}
}

type TektonActionKubernetesResource struct {
	// No cached client - fresh client created per operation for thread safety.
	//
	// clientFactory produces a Kubernetes dynamic client. Defaults to
	// k8s.GetKubernetesClient in production via NewTektonActionKubernetesResource.
	// Tests in the same package may override this field directly to inject a
	// fake client. Do not access from outside the provider package.
	clientFactory func() (dynamic.Interface, error)
}

type TektonActionKubernetesResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	FacetsResourceName types.String `tfsdk:"facets_resource_name"`
	FacetsEnvironment  types.Object `tfsdk:"facets_environment"`
	FacetsResource     types.Object `tfsdk:"facets_resource"`
	Namespace          types.String `tfsdk:"namespace"`
	Labels             types.Map    `tfsdk:"labels"`
	Steps              types.List   `tfsdk:"steps"`
	Params             types.List   `tfsdk:"params"`
	TaskName           types.String `tfsdk:"task_name"`
	StepActionName     types.String `tfsdk:"step_action_name"`
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
				Description: "Kubernetes namespace for Tekton resources. Changing this forces recreation of the resource.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`),
						"must be a valid Kubernetes namespace name (lowercase alphanumeric and hyphens, cannot start or end with hyphen)",
					),
					stringvalidator.LengthAtMost(63),
				},
			},
			"labels": schema.MapAttribute{
				Description: "Custom labels to apply to the Tekton Task and StepAction resources. " +
					"These labels are merged with auto-generated labels (display_name, resource_name, " +
					"resource_kind, environment_unique_name, cluster_id). Auto-generated labels take " +
					"precedence and cannot be overwritten.",
				Optional:    true,
				ElementType: types.StringType,
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
	// Client will be created lazily when needed during CRUD operations.
	// This allows terraform validate to pass without requiring a kubeconfig.
}

// getClient returns a fresh Kubernetes client and operations for each call.
// This pattern matches terraform-provider-helm best practices - no cached state,
// thread-safe, and avoids stale client issues.
//
// In production, clientFactory is k8s.GetKubernetesClient (set by
// NewTektonActionKubernetesResource). In tests, the field can be overridden
// to inject a fake dynamic.Interface for unit-testing CRUD lifecycle paths.
func (r *TektonActionKubernetesResource) getClient() (dynamic.Interface, *tekton.ResourceOperations, error) {
	factory := r.clientFactory
	if factory == nil {
		// Safety net: a zero-valued struct (e.g. constructed without
		// NewTektonActionKubernetesResource) still produces a real client
		// rather than panicking with a nil-pointer dereference.
		factory = k8s.GetKubernetesClient
	}
	client, err := factory()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	return client, tekton.NewResourceOperations(client), nil
}

func (r *TektonActionKubernetesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create fresh client for this operation
	_, operations, err := r.getClient()
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Kubernetes Client",
			err.Error(),
		)
		return
	}

	// Set defaults
	if plan.Namespace.IsNull() || plan.Namespace.ValueString() == "" {
		plan.Namespace = types.StringValue("tekton-pipelines")
	}

	// Extract environment unique_name from environment object
	var facetsEnv tekton.FacetsEnvironmentModel
	resp.Diagnostics.Append(plan.FacetsEnvironment.As(ctx, &facetsEnv, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Extract resource_kind from facets_resource object
	var facetsRes tekton.FacetsResourceModel
	resp.Diagnostics.Append(plan.FacetsResource.As(ctx, &facetsRes, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Generate names using hash for uniqueness
	names := tekton.GenerateNames(
		plan.FacetsResourceName.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		plan.Name.ValueString(),
	)
	plan.TaskName = types.StringValue(names.TaskName)
	plan.StepActionName = types.StringValue(names.StepActionName)
	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", plan.Namespace.ValueString(), names.TaskName))

	// Extract custom labels
	customLabels := make(map[string]string)
	if !plan.Labels.IsNull() {
		resp.Diagnostics.Append(plan.Labels.ElementsAs(ctx, &customLabels, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Create metadata
	metadata := tekton.NewResourceMetadata(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		facetsRes.Kind.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		false, // cloud_action: false for Kubernetes actions
		customLabels,
	)

	// Build StepAction
	stepAction := tekton.BuildKubernetesStepAction(
		plan.StepActionName.ValueString(),
		plan.Namespace.ValueString(),
		metadata.LabelsAsInterface(),
	)

	// Build Task
	task := r.buildTask(ctx, plan, metadata.LabelsAsInterface())
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.createResources(ctx, operations, stepAction, task)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// createResources creates the StepAction and Task in cluster. Returns
// diagnostics. Extracted from Create so unit tests can exercise the
// orphan-on-Task-fail path with a fake dynamic client.
//
// Fix for issue #10 / Bug #1: if Task creation fails after StepAction
// creation succeeded, the StepAction is rolled back via DeleteResource
// (which is idempotent on NotFound per fix #11). If rollback itself fails,
// a warning is surfaced alongside the original Task-create error so the
// operator knows manual cleanup may be required.
func (r *TektonActionKubernetesResource) createResources(ctx context.Context, operations *tekton.ResourceOperations, stepAction, task *unstructured.Unstructured) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := operations.CreateResource(ctx, stepAction, "tekton.dev", "v1beta1", "stepactions"); err != nil {
		diags.AddError(
			"Error creating StepAction",
			fmt.Sprintf("Could not create StepAction: %s", err.Error()),
		)
		return diags
	}
	if err := operations.CreateResource(ctx, task, "tekton.dev", "v1beta1", "tasks"); err != nil {
		// Roll back the StepAction we just created so it doesn't orphan in cluster.
		// DeleteResource is idempotent on NotFound (per fix #11), so this is safe
		// even if the StepAction is somehow already gone.
		if rollbackErr := operations.DeleteResource(
			ctx,
			stepAction.GetNamespace(),
			stepAction.GetName(),
			"tekton.dev", "v1beta1", "stepactions",
		); rollbackErr != nil {
			diags.AddWarning(
				"Rollback of orphaned StepAction failed",
				fmt.Sprintf("Task creation failed; could not clean up StepAction %s/%s: %s. Manual cleanup may be required.",
					stepAction.GetNamespace(), stepAction.GetName(), rollbackErr.Error()),
			)
		}
		diags.AddError(
			"Error creating Task",
			fmt.Sprintf("Could not create Task: %s", err.Error()),
		)
		return diags
	}
	return diags
}

func (r *TektonActionKubernetesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create fresh client for this operation
	client, _, err := r.getClient()
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Kubernetes Client",
			err.Error(),
		)
		return
	}

	remove, diags := r.readResourceState(ctx, client, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if remove {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// readResourceState performs the cluster-side existence check for the Tekton
// Action resource. Returns whether Read should clear state from the response,
// and any diagnostics to surface to the operator.
//
// Extracted from Read to enable unit testing against a fake dynamic.Interface
// without constructing tfsdk.State / tfsdk.ReadRequest plumbing.
//
// Fix for issue #9: classifies errors via apierrors.IsNotFound so that only
// genuine NotFound responses trigger state removal. Transient errors (5xx,
// RBAC, timeout, context cancellation) surface as diagnostics and retain
// state. Both Task and StepAction are checked; asymmetric in-cluster drift
// (one present, one missing) surfaces a warning and retains state.
func (r *TektonActionKubernetesResource) readResourceState(ctx context.Context, client dynamic.Interface, state TektonActionKubernetesResourceModel) (removeFromState bool, diags diag.Diagnostics) {
	taskGVR := k8sschema.GroupVersionResource{Group: "tekton.dev", Version: "v1beta1", Resource: "tasks"}
	stepActionGVR := k8sschema.GroupVersionResource{Group: "tekton.dev", Version: "v1beta1", Resource: "stepactions"}

	taskExists := true
	_, err := client.Resource(taskGVR).Namespace(state.Namespace.ValueString()).Get(ctx, state.TaskName.ValueString(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			taskExists = false
		} else {
			diags.AddError(
				"Error reading Task",
				fmt.Sprintf("Could not read Task %s/%s: %s", state.Namespace.ValueString(), state.TaskName.ValueString(), err.Error()),
			)
			return false, diags
		}
	}

	stepActionExists := true
	_, err = client.Resource(stepActionGVR).Namespace(state.Namespace.ValueString()).Get(ctx, state.StepActionName.ValueString(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			stepActionExists = false
		} else {
			diags.AddError(
				"Error reading StepAction",
				fmt.Sprintf("Could not read StepAction %s/%s: %s", state.Namespace.ValueString(), state.StepActionName.ValueString(), err.Error()),
			)
			return false, diags
		}
	}

	switch {
	case !taskExists && !stepActionExists:
		// Both genuinely deleted — clean removal from state.
		return true, diags
	case taskExists != stepActionExists:
		// Asymmetric drift — refuse to silently mutate state.
		diags.AddWarning(
			"Tekton resource cluster drift detected",
			fmt.Sprintf(
				"Asymmetric cluster state for resource %s: Task exists=%v, StepAction exists=%v. "+
					"This typically means a prior partial create or out-of-band cluster cleanup. "+
					"Resolve by either (a) deleting the surviving cluster object and re-applying, "+
					"or (b) running `terraform import` to bring the missing piece back into state.",
				state.ID.ValueString(), taskExists, stepActionExists,
			),
		)
		return false, diags
	default:
		// Both present and healthy.
		return false, diags
	}
}

func (r *TektonActionKubernetesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TektonActionKubernetesResourceModel
	var state TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create fresh client for this operation
	_, operations, err := r.getClient()
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Kubernetes Client",
			err.Error(),
		)
		return
	}

	// Use state values for computed fields (StepActionName, TaskName, ID).
	// Namespace has RequiresReplace(), so Update is never called with a
	// changed namespace — no need to overwrite plan.Namespace.
	plan.StepActionName = state.StepActionName
	plan.TaskName = state.TaskName
	plan.ID = state.ID

	// Extract environment unique_name from environment object
	var facetsEnv tekton.FacetsEnvironmentModel
	resp.Diagnostics.Append(plan.FacetsEnvironment.As(ctx, &facetsEnv, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Extract resource_kind from facets_resource object
	var facetsRes tekton.FacetsResourceModel
	resp.Diagnostics.Append(plan.FacetsResource.As(ctx, &facetsRes, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Extract custom labels
	customLabels := make(map[string]string)
	if !plan.Labels.IsNull() {
		resp.Diagnostics.Append(plan.Labels.ElementsAs(ctx, &customLabels, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Create metadata
	metadata := tekton.NewResourceMetadata(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		facetsRes.Kind.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		false, // cloud_action: false for Kubernetes actions
		customLabels,
	)

	// Build StepAction and Task
	stepAction := tekton.BuildKubernetesStepAction(
		plan.StepActionName.ValueString(),
		plan.Namespace.ValueString(),
		metadata.LabelsAsInterface(),
	)
	task := r.buildTask(ctx, plan, metadata.LabelsAsInterface())

	resp.Diagnostics.Append(r.updateResources(ctx, operations, stepAction, task)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// updateResources updates the Task and StepAction in cluster. Returns
// diagnostics. Extracted from Update so unit tests can exercise the
// ordering invariant with a fake dynamic client.
//
// Task-first ordering rationale: updating Task before StepAction ensures that
// if Task update fails (validation, RBAC, webhook), the StepAction is never
// touched and the cluster remains in a coherent pre-Update state — zero
// divergence. If Task succeeds but StepAction fails, the cluster is still
// functional because the Task references the StepAction by immutable ref.name;
// the old StepAction spec still resolves. The operator re-runs to retry the
// StepAction update only.
func (r *TektonActionKubernetesResource) updateResources(ctx context.Context, operations *tekton.ResourceOperations, stepAction, task *unstructured.Unstructured) diag.Diagnostics {
	var diags diag.Diagnostics
	// Update Task FIRST. If it fails (validation, RBAC, webhook), the StepAction
	// is never touched and the cluster remains in a coherent pre-Update state.
	if err := operations.UpdateResource(ctx, task, "tekton.dev", "v1beta1", "tasks"); err != nil {
		diags.AddError(
			"Error updating Task",
			fmt.Sprintf("Could not update Task: %s", err.Error()),
		)
		return diags
	}
	// Task already updated. If StepAction update fails the cluster is still
	// functional — the Task references the StepAction by immutable ref.name,
	// and the StepAction at its old spec still resolves. Operator re-runs to
	// retry the StepAction update.
	if err := operations.UpdateResource(ctx, stepAction, "tekton.dev", "v1beta1", "stepactions"); err != nil {
		diags.AddError(
			"Error updating StepAction",
			fmt.Sprintf("Task updated successfully but StepAction update failed: %s. "+
				"Re-run terraform apply to retry the StepAction update.", err.Error()),
		)
		return diags
	}
	return diags
}

func (r *TektonActionKubernetesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TektonActionKubernetesResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create fresh client for this operation
	_, operations, err := r.getClient()
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Kubernetes Client",
			err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(r.deleteResources(ctx, operations, state.Namespace.ValueString(), state.TaskName.ValueString(), state.StepActionName.ValueString())...)
}

// deleteResources attempts to delete both the Task and the StepAction, using
// best-effort semantics — if one fails, the other is still attempted, and both
// errors are aggregated into the returned diagnostics. Combined with the
// idempotent DeleteResource (which treats NotFound as success), this means
// destroy retries are safe.
func (r *TektonActionKubernetesResource) deleteResources(ctx context.Context, operations *tekton.ResourceOperations, namespace, taskName, stepActionName string) diag.Diagnostics {
	var diags diag.Diagnostics
	taskErr := operations.DeleteResource(ctx, namespace, taskName, "tekton.dev", "v1beta1", "tasks")
	stepActionErr := operations.DeleteResource(ctx, namespace, stepActionName, "tekton.dev", "v1beta1", "stepactions")
	if taskErr != nil {
		diags.AddError(
			"Error deleting Task",
			fmt.Sprintf("Could not delete Task: %s", taskErr.Error()),
		)
	}
	if stepActionErr != nil {
		diags.AddError(
			"Error deleting StepAction",
			fmt.Sprintf("Could not delete StepAction: %s", stepActionErr.Error()),
		)
	}
	return diags
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

	// Create fresh client for this operation
	client, _, err := r.getClient()
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Kubernetes Client",
			err.Error(),
		)
		return
	}

	// Verify Task exists
	gvr := k8sschema.GroupVersionResource{
		Group:    "tekton.dev",
		Version:  "v1beta1",
		Resource: "tasks",
	}

	task, err := client.Resource(gvr).Namespace(namespace).Get(ctx, taskName, metav1.GetOptions{})
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

// buildTask creates the Tekton Task for Kubernetes workflows
func (r *TektonActionKubernetesResource) buildTask(ctx context.Context, plan TektonActionKubernetesResourceModel, labels map[string]interface{}) *unstructured.Unstructured {
	// Build steps
	var steps []tekton.StepModel
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
		tektonStep := tekton.BuildStepWithResources(ctx, step)
		tekton.AddEnvVar(tektonStep, "KUBECONFIG", "/workspace/.kube/config")
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
		var params []tekton.ParamModel
		plan.Params.ElementsAs(ctx, &params, false)
		for _, param := range params {
			taskParams = append(taskParams, map[string]interface{}{
				"name": param.Name.ValueString(),
				"type": param.Type.ValueString(),
			})
		}
	}

	return tekton.BuildTask(tekton.TaskSpec{
		TaskName:    plan.TaskName.ValueString(),
		Namespace:   plan.Namespace.ValueString(),
		Description: plan.Description.ValueString(),
		Labels:      labels,
	}, tektonSteps, taskParams)
}
