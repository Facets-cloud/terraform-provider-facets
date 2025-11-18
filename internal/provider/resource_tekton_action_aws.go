package provider

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"

	"github.com/facets-cloud/terraform-provider-facets/internal/aws"
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
	client       dynamic.Interface
	providerData *FacetsProviderModel
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
	Namespace          types.String `tfsdk:"namespace"`
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
				Description: "Generated StepAction name for AWS credential setup (computed from hash). " +
					"This StepAction automatically configures AWS access for the workflow steps.",
				Computed: true,
			},
		},
	}
}

func (r *TektonActionAWSResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Create Kubernetes client
	// Note: We need the Kubernetes client because we're creating Tekton CRDs (Tasks, StepActions)
	// in the control plane cluster. The AWS credentials are only used at Tekton runtime.
	client, err := k8s.GetKubernetesClient()
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Kubernetes Client",
			fmt.Sprintf("Failed to create Kubernetes client: %s", err.Error()),
		)
		return
	}

	r.client = client

	// Store provider data for accessing AWS config during Create/Update
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

		// Convert to aws.ProviderModel for validation
		// This avoids import cycles while maintaining type safety
		awsProviderModel := &aws.ProviderModel{
			AWS: providerModel.AWS,
		}

		// Validate AWS configuration is present
		_, err := aws.GetAWSConfig(ctx, awsProviderModel)
		if err != nil {
			resp.Diagnostics.AddError(
				"AWS Configuration Error",
				err.Error(),
			)
			return
		}
		r.providerData = providerModel
	}
}

func (r *TektonActionAWSResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TektonActionAWSResourceModel

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
	taskName, stepActionName := generateAWSResourceNames(
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
		true, // cloud_action: true for AWS actions
	)

	// Create StepAction
	stepAction, err := r.buildAWSStepAction(ctx, plan, labels)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error building StepAction",
			fmt.Sprintf("Could not build StepAction: %s", err.Error()),
		)
		return
	}
	if err := r.createResource(ctx, stepAction, "tekton.dev", "v1beta1", "stepactions"); err != nil {
		resp.Diagnostics.AddError(
			"Error creating StepAction",
			fmt.Sprintf("Could not create StepAction: %s", err.Error()),
		)
		return
	}

	// Create Task
	task := r.buildAWSTask(ctx, plan, labels)
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

func (r *TektonActionAWSResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TektonActionAWSResourceModel

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

func (r *TektonActionAWSResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TektonActionAWSResourceModel
	var state TektonActionAWSResourceModel

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
		true, // cloud_action: true for AWS actions
	)

	// Update StepAction
	stepAction, err := r.buildAWSStepAction(ctx, plan, labels)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error building StepAction",
			fmt.Sprintf("Could not build StepAction: %s", err.Error()),
		)
		return
	}
	if err := r.updateResource(ctx, stepAction, "tekton.dev", "v1beta1", "stepactions"); err != nil {
		resp.Diagnostics.AddError(
			"Error updating StepAction",
			fmt.Sprintf("Could not update StepAction: %s", err.Error()),
		)
		return
	}

	// Update Task
	task := r.buildAWSTask(ctx, plan, labels)
	if err := r.updateResource(ctx, task, "tekton.dev", "v1beta1", "tasks"); err != nil {
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

func (r *TektonActionAWSResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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

	// Find StepAction (convention: setup-aws-credentials-{hash})
	stepActionName := fmt.Sprintf("setup-aws-credentials-%s", taskName)

	// Set state with imported values
	state := TektonActionAWSResourceModel{
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

// Helper functions

// generateAWSResourceNames creates deterministic names for Task and StepAction (AWS version)
// Returns (taskName, stepActionName)
func generateAWSResourceNames(resourceName, envName, displayName string) (string, string) {
	hashInput := fmt.Sprintf("%s-%s-%s", resourceName, envName, displayName)
	nameHash := fmt.Sprintf("%x", md5.Sum([]byte(hashInput)))

	// Build stepActionName with AWS-specific prefix
	stepActionName := fmt.Sprintf("setup-aws-credentials-%s", nameHash)
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

// buildAWSStepAction creates the StepAction for AWS credential setup using IRSA
func (r *TektonActionAWSResource) buildAWSStepAction(ctx context.Context, plan TektonActionAWSResourceModel, labels map[string]interface{}) (*unstructured.Unstructured, error) {
	// Convert provider data to aws.ProviderModel for extraction
	awsProviderModel := &aws.ProviderModel{
		AWS: r.providerData.AWS,
	}

	// Get AWS config from provider data
	awsConfig, err := aws.GetAWSConfig(ctx, awsProviderModel)
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS config: %w", err)
	}

	// Generate script using IRSA + source_profile for role assumption
	// AWS SDK automatically handles role chaining from pod's IRSA to target role
	script := generateAssumeRoleScript(awsConfig)

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
				"image":  "facetscloud/actions-base-image:v1.0.0",
				"script": script,
				// No params needed - AWS SDK uses IRSA from pod automatically
				// No env vars needed - IRSA injected by EKS webhook
			},
		},
	}

	return stepAction, nil
}

// generateAssumeRoleScript creates an AWS config file with source_profile
// Uses IRSA (pod's IAM role) via source_profile to automatically assume the target role
// The AWS SDK handles the role assumption automatically - no manual STS calls needed
func generateAssumeRoleScript(config *aws.AWSAuthConfig) string {
	if config.AssumeRoleConfig == nil {
		return ""
	}

	assumeRole := config.AssumeRoleConfig

	// Generate session name if not provided
	sessionName := assumeRole.SessionName
	if sessionName == "" {
		sessionName = generateRandomSessionName()
	}

	// Build the config file with source_profile for role chaining
	// The parent-cp-account profile uses IRSA (web identity token from pod)
	// The default profile uses source_profile to chain to the target role
	script := `#!/bin/bash
set -e

mkdir -p /workspace/.aws

PARENT_ROLE_ARN="${AWS_ROLE_ARN}"
if [ -z "$PARENT_ROLE_ARN" ]; then
    echo "ERROR: AWS_ROLE_ARN environment variable not set. IRSA may not be configured." >&2
    exit 1
fi

cat > /workspace/.aws/config <<EOFCONFIG
[profile irsa]
web_identity_token_file = /var/run/secrets/eks.amazonaws.com/serviceaccount/token
role_arn = ${PARENT_ROLE_ARN}

[default]
source_profile = irsa
role_arn = %s
role_session_name = %s
region = %s
`

	// Add optional external_id if provided
	if assumeRole.ExternalID != "" {
		script += fmt.Sprintf("external_id = %s\n", assumeRole.ExternalID)
	}

	script += `EOFCONFIG

chmod 600 /workspace/.aws/config
`

	return fmt.Sprintf(script,
		assumeRole.RoleARN, // For [default] role_arn (target role)
		sessionName,        // For [default] role_session_name
		config.Region,      // For [default] region
	)
}

// generateRandomSessionName creates a random session name using crypto/rand
// Returns a string in format "terraform-XXXXXXXX" where X is a random hex character
func generateRandomSessionName() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to a simpler approach if crypto/rand fails
		return fmt.Sprintf("terraform-session-%d", os.Getpid())
	}
	return fmt.Sprintf("terraform-%s", hex.EncodeToString(bytes))
}

// buildAWSTask creates the Tekton Task for AWS workflows
func (r *TektonActionAWSResource) buildAWSTask(ctx context.Context, plan TektonActionAWSResourceModel, labels map[string]interface{}) *unstructured.Unstructured {
	// Build steps
	var steps []StepModel
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

	// Add user-defined steps
	for _, step := range steps {
		tektonStep := map[string]interface{}{
			"name":   step.Name.ValueString(),
			"image":  step.Image.ValueString(),
			"script": step.Script.ValueString(),
		}

		// Add env vars - user-provided vars plus AWS config file path
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

		// Inject AWS config file path
		// AWS SDK will use IRSA + source_profile for authentication
		envList = append(envList, map[string]interface{}{
			"name":  "AWS_CONFIG_FILE",
			"value": "/workspace/.aws/config",
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

	// Build params (only user-defined params, no AWS params needed)
	taskParams := []interface{}{}

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

	// Build task metadata
	metadata := map[string]interface{}{
		"name":      plan.TaskName.ValueString(),
		"namespace": plan.Namespace.ValueString(),
		"labels":    labels,
	}

	// Build task object using unstructured (idiomatic for dynamic K8s resources)
	task := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tekton.dev/v1beta1",
			"kind":       "Task",
			"metadata":   metadata,
			"spec": map[string]interface{}{
				"description": description,
				"steps":       tektonSteps,
				"params":      taskParams,
			},
		},
	}

	return task
}

// createResource creates a Kubernetes resource
func (r *TektonActionAWSResource) createResource(ctx context.Context, obj *unstructured.Unstructured, group, version, resource string) error {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	namespace := obj.GetNamespace()
	_, err := r.client.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	return err
}

// updateResource updates a Kubernetes resource
func (r *TektonActionAWSResource) updateResource(ctx context.Context, obj *unstructured.Unstructured, group, version, resource string) error {
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

// deleteResource deletes a Kubernetes resource
func (r *TektonActionAWSResource) deleteResource(ctx context.Context, namespace, name, group, version, resource string) error {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	return r.client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}
