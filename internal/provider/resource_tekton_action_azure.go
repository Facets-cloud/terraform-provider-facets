package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/facets-cloud/terraform-provider-facets/internal/azure"
	"github.com/facets-cloud/terraform-provider-facets/internal/k8s"
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = &TektonActionAzureResource{}
	_ resource.ResourceWithConfigure   = &TektonActionAzureResource{}
	_ resource.ResourceWithImportState = &TektonActionAzureResource{}
)

// NewTektonActionAzureResource creates a new Azure action resource
func NewTektonActionAzureResource() resource.Resource {
	return &TektonActionAzureResource{
		clientFactory: k8s.GetKubernetesClient,
	}
}

// TektonActionAzureResource manages Tekton Tasks and StepActions for Azure workflows
type TektonActionAzureResource struct {
	providerData *FacetsProviderModel
	// No cached client - fresh client created per operation for thread safety.
	//
	// clientFactory produces a Kubernetes dynamic client. Defaults to
	// k8s.GetKubernetesClient in production via NewTektonActionAzureResource.
	// Tests in the same package may override this field directly to inject a
	// fake client. Do not access from outside the provider package.
	clientFactory func() (dynamic.Interface, error)
}

// TektonActionAzureResourceModel represents the resource data model
// This is identical to the Kubernetes action model since the schema is the same
type TektonActionAzureResourceModel struct {
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

func (r *TektonActionAzureResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tekton_action_azure"
}

func (r *TektonActionAzureResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Tekton Task and StepAction for Azure-based workflows. " +
			"This resource automatically injects Azure credentials (configured at provider level) " +
			"via a setup-credentials step, which runs 'az login' and writes an Azure CLI profile. " +
			"Users triggering the action never supply credentials.",
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
				Description: "Generated StepAction name for AWS credential setup (computed from hash). " +
					"This StepAction automatically configures AWS access for the workflow steps.",
				Computed: true,
			},
		},
	}
}

func (r *TektonActionAzureResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Client will be created lazily when needed during CRUD operations.
	// This allows terraform validate to pass without requiring a kubeconfig.

	// Store provider data for accessing AWS config during Create/Update
	// Note: We validate AWS config lazily during CRUD operations, not here,
	// to allow terraform validate to succeed without AWS credentials.
	if req.ProviderData != nil {
		// Type assert to get provider model
		providerModel, ok := req.ProviderData.(*FacetsProviderModel)
		if !ok {
			resp.Diagnostics.AddError(
				"Unexpected Provider Data Type",
				fmt.Sprintf("Expected *FacetsProviderModel, got: %T", req.ProviderData),
			)
			return
		}
		r.providerData = providerModel
	}
}

// getClient returns a fresh Kubernetes client and operations for each call.
// This pattern matches terraform-provider-helm best practices - no cached state,
// thread-safe, and avoids stale client issues.
//
// In production, clientFactory is k8s.GetKubernetesClient (set by
// NewTektonActionAzureResource). In tests, the field can be overridden to
// inject a fake dynamic.Interface for unit-testing CRUD lifecycle paths.
func (r *TektonActionAzureResource) getClient() (dynamic.Interface, *tekton.ResourceOperations, error) {
	factory := r.clientFactory
	if factory == nil {
		// Safety net: a zero-valued struct (e.g. constructed without
		// NewTektonActionAzureResource) still produces a real client rather
		// than panicking with a nil-pointer dereference.
		factory = k8s.GetKubernetesClient
	}
	client, err := factory()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	return client, tekton.NewResourceOperations(client), nil
}

func (r *TektonActionAzureResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TektonActionAzureResourceModel

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
	// Resolve the namespace BEFORE anything derives from it. A StepAction is only
	// resolvable from a TaskRun in the same namespace, so the Task, StepAction,
	// Secret, the ID and every later Read/Delete must all agree on this one value.
	if plan.Namespace.IsNull() || plan.Namespace.ValueString() == "" {
		plan.Namespace = types.StringValue(tektonPipelinesNamespace)
	}
	ns := plan.Namespace.ValueString()

	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", ns, names.TaskName))

	customLabels := make(map[string]string)
	if !plan.Labels.IsNull() {
		resp.Diagnostics.Append(plan.Labels.ElementsAs(ctx, &customLabels, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	metadata := tekton.NewResourceMetadata(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		facetsRes.Kind.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		true,         // cloud_action: true for Azure actions
		customLabels, // customLabels: not supported for Azure actions yet
	)

	// Validate provider data is available
	if r.providerData == nil {
		resp.Diagnostics.AddError(
			"Provider Configuration Error",
			"Provider data is not configured. Ensure the provider block is properly configured.",
		)
		return
	}

	// Get AWS config
	azureProviderModel := &azure.ProviderModel{
		Azure: r.providerData.Azure,
	}
	azureConfig, err := azure.GetAzureConfig(ctx, azureProviderModel)
	if err != nil {
		resp.Diagnostics.AddError(
			"Azure Configuration Error",
			err.Error(),
		)
		return
	}

	// The provider owns the credentials Secret in client-secret mode. Creating it
	// here -- rather than requiring it out of band -- is what lets the derived name
	// stay an implementation detail: nobody has to be told a name, because nobody
	// has to type one. See ReconcileCredentialsSecret for why the Secret is shared
	// rather than owned by a single action.
	if azureConfig.Mode == azure.AuthModeClientSecret {
		if err := operations.ReconcileCredentialsSecret(
			ctx, plan.Namespace.ValueString(),
			azureConfig.SecretName, azureConfig.SecretKey, azureConfig.ClientSecret,
			map[string]string{
				"tenant-id":       azureConfig.TenantID,
				"client-id":       azureConfig.ClientID,
				"subscription-id": azureConfig.SubscriptionID,
			},
		); err != nil {
			resp.Diagnostics.AddError("Could not provision the Azure credentials Secret", err.Error())
			return
		}
	}

	// Create StepAction
	stepAction, err := tekton.BuildAzureStepAction(
		plan.StepActionName.ValueString(),
		ns,
		metadata.LabelsAsInterface(),
		azureConfig,
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error building StepAction",
			fmt.Sprintf("Could not build StepAction: %s", err.Error()),
		)
		return
	}
	// Build Task
	task := r.buildAzureTask(ctx, plan, metadata.LabelsAsInterface(), metadata.AnnotationsAsInterface(), azureConfig.Mode)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.createResources(ctx, operations, stepAction, task)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// createResources creates the StepAction and Task in cluster. Mirrors the K8s variant.
//
// Fix for issue #10 / Bug #1: if Task creation fails after StepAction creation
// succeeded, the StepAction is rolled back via DeleteResource (idempotent on
// NotFound per fix #11). If rollback itself fails, a warning is surfaced
// alongside the original Task-create error so the operator knows manual
// cleanup may be required.
func (r *TektonActionAzureResource) createResources(ctx context.Context, operations *tekton.ResourceOperations, stepAction, task *unstructured.Unstructured) diag.Diagnostics {
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

func (r *TektonActionAzureResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TektonActionAzureResourceModel

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

	// Reconcile the credentials Secret here as well as in Create/Update, because a
	// rotated client secret changes only PROVIDER configuration -- no resource
	// attribute moves, so Terraform reports "No changes" and never calls Update.
	// Without this, rotating the password silently leaves the old value in the
	// cluster and every action keeps authenticating with a credential the operator
	// believes they have replaced.
	//
	// Read runs on every plan and refresh, so this is where convergence has to
	// happen. Failures are warnings, not errors: Read must not break a plan, and
	// Create/Update still surface a hard error on the paths that can.
	if r.providerData != nil && !r.providerData.Azure.IsNull() {
		azureConfig, cfgErr := azure.GetAzureConfig(ctx, &azure.ProviderModel{Azure: r.providerData.Azure})
		if cfgErr == nil && azureConfig.Mode == azure.AuthModeClientSecret {
			operations := tekton.NewResourceOperations(client)
			if secErr := operations.ReconcileCredentialsSecret(
				ctx, state.Namespace.ValueString(),
				azureConfig.SecretName, azureConfig.SecretKey, azureConfig.ClientSecret,
				map[string]string{
					"tenant-id":       azureConfig.TenantID,
					"client-id":       azureConfig.ClientID,
					"subscription-id": azureConfig.SubscriptionID,
				},
			); secErr != nil {
				resp.Diagnostics.AddWarning(
					"Could not reconcile the Azure credentials Secret",
					fmt.Sprintf("The action will keep using whatever credential is already in the cluster: %s", secErr),
				)
			}
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// readResourceState performs the cluster-side existence check for the AWS
// Tekton Action resource. Mirrors the K8s variant's helper.
//
// Fix for issue #9: classifies errors via apierrors.IsNotFound so that only
// genuine NotFound responses trigger state removal. Transient errors (5xx,
// RBAC, timeout, context cancellation) surface as diagnostics and retain
// state. Both Task and StepAction are checked; asymmetric in-cluster drift
// (one present, one missing) surfaces a warning and retains state.
//
// The namespace comes from STATE, not a constant: a StepAction is only resolvable
// from a TaskRun in the same namespace, so reading the wrong one reports the
// objects as deleted and produces a permanent drift warning.
func (r *TektonActionAzureResource) readResourceState(ctx context.Context, client dynamic.Interface, state TektonActionAzureResourceModel) (removeFromState bool, diags diag.Diagnostics) {
	taskGVR := k8sschema.GroupVersionResource{Group: "tekton.dev", Version: "v1beta1", Resource: "tasks"}
	stepActionGVR := k8sschema.GroupVersionResource{Group: "tekton.dev", Version: "v1beta1", Resource: "stepactions"}

	taskExists := true
	ns := state.Namespace.ValueString()
	if ns == "" {
		ns = tektonPipelinesNamespace
	}

	_, err := client.Resource(taskGVR).Namespace(ns).Get(ctx, state.TaskName.ValueString(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			taskExists = false
		} else {
			diags.AddError(
				"Error reading Task",
				fmt.Sprintf("Could not read Task %s/%s: %s", ns, state.TaskName.ValueString(), err.Error()),
			)
			return false, diags
		}
	}

	stepActionExists := true
	_, err = client.Resource(stepActionGVR).Namespace(ns).Get(ctx, state.StepActionName.ValueString(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			stepActionExists = false
		} else {
			diags.AddError(
				"Error reading StepAction",
				fmt.Sprintf("Could not read StepAction %s/%s: %s", ns, state.StepActionName.ValueString(), err.Error()),
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

func (r *TektonActionAzureResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TektonActionAzureResourceModel
	var state TektonActionAzureResourceModel

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

	// Use state values for computed fields (StepActionName, TaskName)
	// These are computed and unknown in the plan
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

	if plan.Namespace.IsNull() || plan.Namespace.ValueString() == "" {
		plan.Namespace = types.StringValue(tektonPipelinesNamespace)
	}
	ns := plan.Namespace.ValueString()

	customLabels := make(map[string]string)
	if !plan.Labels.IsNull() {
		resp.Diagnostics.Append(plan.Labels.ElementsAs(ctx, &customLabels, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	metadata := tekton.NewResourceMetadata(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		facetsRes.Kind.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		true,         // cloud_action: true for Azure actions
		customLabels, // customLabels: not supported for Azure actions yet
	)

	// Validate provider data is available
	if r.providerData == nil {
		resp.Diagnostics.AddError(
			"Provider Configuration Error",
			"Provider data is not configured. Ensure the provider block is properly configured.",
		)
		return
	}

	// Get AWS config
	azureProviderModel := &azure.ProviderModel{
		Azure: r.providerData.Azure,
	}
	azureConfig, err := azure.GetAzureConfig(ctx, azureProviderModel)
	if err != nil {
		resp.Diagnostics.AddError(
			"Azure Configuration Error",
			err.Error(),
		)
		return
	}

	// Update StepAction
	// Reconcile on update too, so a rotated client secret propagates: the Secret is
	// rewritten in place under the same derived name, and every action sharing that
	// service principal picks up the new value on its next run.
	if azureConfig.Mode == azure.AuthModeClientSecret {
		if err := operations.ReconcileCredentialsSecret(
			ctx, plan.Namespace.ValueString(),
			azureConfig.SecretName, azureConfig.SecretKey, azureConfig.ClientSecret,
			map[string]string{
				"tenant-id":       azureConfig.TenantID,
				"client-id":       azureConfig.ClientID,
				"subscription-id": azureConfig.SubscriptionID,
			},
		); err != nil {
			resp.Diagnostics.AddError("Could not provision the Azure credentials Secret", err.Error())
			return
		}
	}

	stepAction, err := tekton.BuildAzureStepAction(
		plan.StepActionName.ValueString(),
		ns,
		metadata.LabelsAsInterface(),
		azureConfig,
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error building StepAction",
			fmt.Sprintf("Could not build StepAction: %s", err.Error()),
		)
		return
	}
	// Build Task
	task := r.buildAzureTask(ctx, plan, metadata.LabelsAsInterface(), metadata.AnnotationsAsInterface(), azureConfig.Mode)

	resp.Diagnostics.Append(r.updateResources(ctx, operations, stepAction, task)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// updateResources updates the Task and StepAction in cluster. Mirrors the K8s variant.
//
// Task-first ordering rationale: updating Task before StepAction ensures that
// if Task update fails (validation, RBAC, webhook), the StepAction is never
// touched and the cluster remains in a coherent pre-Update state — zero
// divergence. If Task succeeds but StepAction fails, the cluster is still
// functional because the Task references the StepAction by immutable ref.name;
// the old StepAction spec still resolves. The operator re-runs to retry the
// StepAction update only.
func (r *TektonActionAzureResource) updateResources(ctx context.Context, operations *tekton.ResourceOperations, stepAction, task *unstructured.Unstructured) diag.Diagnostics {
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

func (r *TektonActionAzureResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TektonActionAzureResourceModel

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

	delNS := state.Namespace.ValueString()
	if delNS == "" {
		delNS = tektonPipelinesNamespace
	}
	resp.Diagnostics.Append(r.deleteResources(ctx, operations, delNS, state.TaskName.ValueString(), state.StepActionName.ValueString())...)
}

// deleteResources attempts to delete both the Task and the StepAction, using
// best-effort semantics — if one fails, the other is still attempted, and both
// errors are aggregated into the returned diagnostics. Combined with the
// idempotent DeleteResource (which treats NotFound as success), this means
// destroy retries are safe.
//
// Note: the AWS variant pins the namespace to tektonPipelinesNamespace
// (the AWS model has no Namespace field).
func (r *TektonActionAzureResource) deleteResources(ctx context.Context, operations *tekton.ResourceOperations, namespace, taskName, stepActionName string) diag.Diagnostics {
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

// schemaAttrElemType returns the element type the SCHEMA declares for a list
// attribute. Derived rather than hand-written so it cannot drift from the schema
// as steps/params gain fields.
func schemaAttrElemType(r *TektonActionAzureResource, name string) attr.Type {
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if a, ok := resp.Schema.Attributes[name]; ok {
		if lt, ok := a.GetType().(types.ListType); ok {
			return lt.ElementType()
		}
	}
	// Unreachable for the current schema; a bare object keeps the null typed
	// rather than panicking if the attribute is ever restructured.
	return types.ObjectType{}
}

func (r *TektonActionAzureResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: taskName or namespace/taskName.
	// Example: 59f6f855860ddc99a32e2944c96db5fa
	// Example: my-namespace/59f6f855860ddc99a32e2944c96db5fa
	//
	// The namespace is HONOURED when supplied. It used to be parsed and discarded,
	// which meant an action living outside tekton-pipelines could not be imported at
	// all -- the lookup went to the wrong namespace and reported the Task missing.
	taskName := req.ID
	importNS := tektonPipelinesNamespace
	if strings.Contains(req.ID, "/") {
		parts := strings.SplitN(req.ID, "/", 2)
		if parts[0] != "" {
			importNS = parts[0]
		}
		taskName = parts[1]
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

	// Verify Task exists
	gvr := k8sschema.GroupVersionResource{
		Group:    "tekton.dev",
		Version:  "v1beta1",
		Resource: "tasks",
	}

	task, err := client.Resource(gvr).Namespace(importNS).Get(ctx, taskName, metav1.GetOptions{})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error importing resource",
			fmt.Sprintf("Could not find Task %s/%s: %s", importNS, taskName, err.Error()),
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

	// Prefer the annotation: the label is sanitized and therefore lossy, so
	// reconstructing `name` from it produced a spurious diff on the first plan
	// ("Stop-Database" -> "Stop Database"). Fall back to the label for Tasks created
	// before the annotation existed.
	annotations := task.GetAnnotations()
	displayName, hasDisplayName := annotations[tekton.DisplayNameAnnotation]
	if !hasDisplayName || displayName == "" {
		displayName, hasDisplayName = labels["display_name"]
	}
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
	state := TektonActionAzureResourceModel{
		ID:                 types.StringValue(fmt.Sprintf("%s/%s", importNS, taskName)),
		Name:               types.StringValue(displayName),
		FacetsResourceName: types.StringValue(resourceName),
		TaskName:           types.StringValue(taskName),
		StepActionName:     types.StringValue(stepActionName),
		// Record the namespace the objects were actually found in. Leaving this
		// unset makes it unknown in state, and because it carries RequiresReplace
		// the next plan would want to destroy and recreate the imported action.
		Namespace: types.StringValue(importNS),

		// The object- and list-typed attributes MUST carry their element types even
		// when null. A zero-value types.Object has no attribute types, which the
		// framework rejects: "Value Conversion Error ... Received framework type
		// types.ObjectType[]". That made `terraform import` fail outright for this
		// resource type -- a pre-existing bug, surfaced once the namespace fix let
		// import get far enough to reach the type check.
		//
		// These cannot be reconstructed from the Task, so they are TYPED nulls; the
		// operator supplies them in configuration and the next plan reconciles.
		FacetsEnvironment: types.ObjectNull(map[string]attr.Type{
			"unique_name": types.StringType,
		}),
		FacetsResource: types.ObjectNull(map[string]attr.Type{
			"kind": types.StringType,
		}),
		Labels: types.MapNull(types.StringType),
		Steps:  types.ListNull(schemaAttrElemType(r, "steps")),
		Params: types.ListNull(schemaAttrElemType(r, "params")),
	}

	// Note: We cannot fully reconstruct facets_environment, facets_resource, steps, params from the Task
	// User will need to manually specify these in their configuration
	resp.Diagnostics.AddWarning(
		"Partial Import",
		"Only basic fields were imported. You must manually specify: facets_environment, facets_resource, steps, and params in your configuration.",
	)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// buildAzureTask creates the Tekton Task for Azure workflows
func (r *TektonActionAzureResource) buildAzureTask(ctx context.Context, plan TektonActionAzureResourceModel, labels, annotations map[string]interface{}, azureMode azure.AuthMode) *unstructured.Unstructured {
	// Build steps
	var steps []tekton.StepModel
	plan.Steps.ElementsAs(ctx, &steps, false)

	// First step: setup-credentials (references StepAction, no params needed)
	tektonSteps := []interface{}{
		map[string]interface{}{
			"name": "setup-credentials",
			"ref": map[string]interface{}{
				"name": plan.StepActionName.ValueString(),
			},
		},
	}

	// Add user-defined steps with AZURE_CONFIG_DIR pointing at the shared profile
	for _, step := range steps {
		tektonStep := tekton.BuildStepWithResources(ctx, step)
		// Point the Azure CLI at the profile written by the setup-credentials step
		tekton.AddEnvVar(tektonStep, "AZURE_CONFIG_DIR", tekton.AzureConfigDir)
		tektonSteps = append(tektonSteps, tektonStep)
	}

	// Build params (only user-defined params, no AWS params needed)
	taskParams := []interface{}{}
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

	description := plan.TaskName.ValueString()
	if !plan.Description.IsNull() && plan.Description.ValueString() != "" {
		description = plan.Description.ValueString()
	}

	return tekton.BuildTask(tekton.TaskSpec{
		TaskName:    plan.TaskName.ValueString(),
		Namespace:   plan.Namespace.ValueString(),
		Description: description,
		Labels:      labels,
		Annotations: annotations,
	}, tektonSteps, taskParams)
}
