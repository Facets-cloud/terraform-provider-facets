package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/facets-cloud/terraform-provider-facets/internal/aws"
	"github.com/facets-cloud/terraform-provider-facets/internal/k8s"
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
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

const tektonPipelinesNamespace = "tekton-pipelines"

var (
	_ resource.Resource                = &TektonActionAWSResource{}
	_ resource.ResourceWithConfigure   = &TektonActionAWSResource{}
	_ resource.ResourceWithImportState = &TektonActionAWSResource{}
)

// NewTektonActionAWSResource creates a new AWS action resource
func NewTektonActionAWSResource() resource.Resource {
	return &TektonActionAWSResource{}
}

// TektonActionAWSResource manages Tekton Tasks and StepActions for AWS workflows
type TektonActionAWSResource struct {
	providerData *FacetsProviderModel
	// No cached client - fresh client created per operation for thread safety
}

// TektonActionAWSResourceModel represents the resource data model
// This is identical to the Kubernetes action model since the schema is the same
type TektonActionAWSResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	FacetsResourceName types.String `tfsdk:"facets_resource_name"`
	FacetsEnvironment  types.Object `tfsdk:"facets_environment"`
	FacetsResource     types.Object `tfsdk:"facets_resource"`
	Steps              types.List   `tfsdk:"steps"`
	Params             types.List   `tfsdk:"params"`
	TaskName           types.String `tfsdk:"task_name"`
	StepActionName     types.String `tfsdk:"step_action_name"`
}

func (r *TektonActionAWSResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tekton_action_aws"
}

func (r *TektonActionAWSResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Tekton Task and StepAction for AWS-based workflows. " +
			"This resource automatically injects AWS credentials (configured at provider level) " +
			"via a setup-credentials step, which creates ~/.aws/credentials and ~/.aws/config files. " +
			"The credentials are scoped to the AWS account configured in the provider.",
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

func (r *TektonActionAWSResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *TektonActionAWSResource) getClient() (dynamic.Interface, *tekton.ResourceOperations, error) {
	client, err := k8s.GetKubernetesClient()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	return client, tekton.NewResourceOperations(client), nil
}

func (r *TektonActionAWSResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TektonActionAWSResourceModel

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
	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", tektonPipelinesNamespace, names.TaskName))

	// Create metadata (no custom labels for AWS actions currently)
	metadata := tekton.NewResourceMetadata(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		facetsRes.Kind.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		true, // cloud_action: true for AWS actions
		nil,  // customLabels: not supported for AWS actions yet
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
	awsProviderModel := &aws.ProviderModel{
		AWS: r.providerData.AWS,
	}
	awsConfig, err := aws.GetAWSConfig(ctx, awsProviderModel)
	if err != nil {
		resp.Diagnostics.AddError(
			"AWS Configuration Error",
			err.Error(),
		)
		return
	}

	// Create StepAction
	stepAction, err := tekton.BuildAWSStepAction(
		plan.StepActionName.ValueString(),
		tektonPipelinesNamespace,
		metadata.LabelsAsInterface(),
		awsConfig,
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error building StepAction",
			fmt.Sprintf("Could not build StepAction: %s", err.Error()),
		)
		return
	}
	if err := operations.CreateResource(ctx, stepAction, "tekton.dev", "v1beta1", "stepactions"); err != nil {
		resp.Diagnostics.AddError(
			"Error creating StepAction",
			fmt.Sprintf("Could not create StepAction: %s", err.Error()),
		)
		return
	}

	// Create Task
	task := r.buildAWSTask(ctx, plan, metadata.LabelsAsInterface())
	if resp.Diagnostics.HasError() {
		return
	}

	if err := operations.CreateResource(ctx, task, "tekton.dev", "v1beta1", "tasks"); err != nil {
		resp.Diagnostics.AddError(
			"Error creating Task",
			fmt.Sprintf("Could not create Task: %s", err.Error()),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *TektonActionAWSResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TektonActionAWSResourceModel

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

	// Verify Task exists
	gvr := k8sschema.GroupVersionResource{
		Group:    "tekton.dev",
		Version:  "v1beta1",
		Resource: "tasks",
	}

	_, err = client.Resource(gvr).Namespace(tektonPipelinesNamespace).Get(ctx, state.TaskName.ValueString(), metav1.GetOptions{})
	if err != nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TektonActionAWSResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TektonActionAWSResourceModel
	var state TektonActionAWSResourceModel

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

	// Create metadata (no custom labels for AWS actions currently)
	metadata := tekton.NewResourceMetadata(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		facetsRes.Kind.ValueString(),
		facetsEnv.UniqueName.ValueString(),
		true, // cloud_action: true for AWS actions
		nil,  // customLabels: not supported for AWS actions yet
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
	awsProviderModel := &aws.ProviderModel{
		AWS: r.providerData.AWS,
	}
	awsConfig, err := aws.GetAWSConfig(ctx, awsProviderModel)
	if err != nil {
		resp.Diagnostics.AddError(
			"AWS Configuration Error",
			err.Error(),
		)
		return
	}

	// Update StepAction
	stepAction, err := tekton.BuildAWSStepAction(
		plan.StepActionName.ValueString(),
		tektonPipelinesNamespace,
		metadata.LabelsAsInterface(),
		awsConfig,
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error building StepAction",
			fmt.Sprintf("Could not build StepAction: %s", err.Error()),
		)
		return
	}
	if err := operations.UpdateResource(ctx, stepAction, "tekton.dev", "v1beta1", "stepactions"); err != nil {
		resp.Diagnostics.AddError(
			"Error updating StepAction",
			fmt.Sprintf("Could not update StepAction: %s", err.Error()),
		)
		return
	}

	// Update Task
	task := r.buildAWSTask(ctx, plan, metadata.LabelsAsInterface())
	if err := operations.UpdateResource(ctx, task, "tekton.dev", "v1beta1", "tasks"); err != nil {
		resp.Diagnostics.AddError(
			"Error updating Task",
			fmt.Sprintf("Could not update Task: %s", err.Error()),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *TektonActionAWSResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TektonActionAWSResourceModel

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

	// Delete Task
	if err := operations.DeleteResource(ctx, tektonPipelinesNamespace, state.TaskName.ValueString(), "tekton.dev", "v1beta1", "tasks"); err != nil {
		resp.Diagnostics.AddError(
			"Error deleting Task",
			fmt.Sprintf("Could not delete Task: %s", err.Error()),
		)
		return
	}

	// Delete StepAction
	if err := operations.DeleteResource(ctx, tektonPipelinesNamespace, state.StepActionName.ValueString(), "tekton.dev", "v1beta1", "stepactions"); err != nil {
		resp.Diagnostics.AddError(
			"Error deleting StepAction",
			fmt.Sprintf("Could not delete StepAction: %s", err.Error()),
		)
		return
	}
}

func (r *TektonActionAWSResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: taskName or namespace/taskName (namespace is ignored, always uses tekton-pipelines)
	// Example: 59f6f855860ddc99a32e2944c96db5fa
	// Example: tekton-pipelines/59f6f855860ddc99a32e2944c96db5fa

	taskName := req.ID
	// Support legacy format namespace/taskName - extract just the task name
	if strings.Contains(req.ID, "/") {
		parts := strings.SplitN(req.ID, "/", 2)
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

	task, err := client.Resource(gvr).Namespace(tektonPipelinesNamespace).Get(ctx, taskName, metav1.GetOptions{})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error importing resource",
			fmt.Sprintf("Could not find Task %s/%s: %s", tektonPipelinesNamespace, taskName, err.Error()),
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
	state := TektonActionAWSResourceModel{
		ID:                 types.StringValue(fmt.Sprintf("%s/%s", tektonPipelinesNamespace, taskName)),
		Name:               types.StringValue(displayName),
		FacetsResourceName: types.StringValue(resourceName),
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

// buildAWSTask creates the Tekton Task for AWS workflows
func (r *TektonActionAWSResource) buildAWSTask(ctx context.Context, plan TektonActionAWSResourceModel, labels map[string]interface{}) *unstructured.Unstructured {
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

	// Add user-defined steps with AWS_CONFIG_FILE env var
	for _, step := range steps {
		tektonStep := tekton.BuildStepWithResources(ctx, step)
		// Inject AWS config file path - AWS SDK will use IRSA + source_profile for authentication
		tekton.AddEnvVar(tektonStep, "AWS_CONFIG_FILE", "/workspace/.aws/config")
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
		Namespace:   tektonPipelinesNamespace,
		Description: description,
		Labels:      labels,
	}, tektonSteps, taskParams)
}
