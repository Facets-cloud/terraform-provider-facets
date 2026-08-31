package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/facets-cloud/terraform-provider-facets/internal/credentials"
	"github.com/facets-cloud/terraform-provider-facets/internal/k8s"
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
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
	_ resource.Resource                = &FacetsActionsResource{}
	_ resource.ResourceWithConfigure   = &FacetsActionsResource{}
	_ resource.ResourceWithImportState = &FacetsActionsResource{}
)

var actionTaskGVR = k8sschema.GroupVersionResource{Group: "tekton.dev", Version: "v1beta1", Resource: "tasks"}

// credentialsSecretPrefix names the Secret holding an environment's action
// credentials.
const credentialsSecretPrefix = "facets-action-creds"

func NewFacetsActionsResource() resource.Resource {
	return &FacetsActionsResource{
		clientFactory: k8s.GetKubernetesClient,
		credsFromEnv:  credentials.FromEnv,
	}
}

// FacetsActionsResource manages a Tekton Task for a Facets action, for any cloud
// or none.
//
// Two things distinguish it from the per-cloud resources it replaces.
//
// It has no provider credential configuration and no cloud-specific code. The
// credentials an action needs are read from the provider process's environment
// and written to a Kubernetes Secret, which every step consumes through envFrom.
// Because envFrom injects a Secret's keys verbatim, the provider never learns
// what any of them mean -- AWS static keys, an assumed-role session, an Azure
// service principal, a managed identity or a GCP service account all travel the
// same path. The module's own script does whatever login its cloud requires.
//
// It creates no StepAction. That object existed only to inject credentials, and
// Tekton forbids `env` on a step carrying a `ref` while StepActionSpec has no
// `envFrom` at all -- so a StepAction cannot do key-agnostic injection under any
// configuration. Dropping it leaves a single object to manage, which removes the
// create-rollback, the two-object drift detection, and the partial-failure states
// that come with keeping a pair in sync.
type FacetsActionsResource struct {
	// clientFactory and credsFromEnv are seams for tests in this package to
	// inject a fake client and a fixed credential set. Not for use elsewhere.
	clientFactory func() (dynamic.Interface, error)
	credsFromEnv  func() (map[string]string, error)
}

type FacetsActionsResourceModel struct {
	ID                    types.String `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	Description           types.String `tfsdk:"description"`
	FacetsResourceName    types.String `tfsdk:"facets_resource_name"`
	FacetsEnvironment     types.Object `tfsdk:"facets_environment"`
	FacetsResource        types.Object `tfsdk:"facets_resource"`
	Namespace             types.String `tfsdk:"namespace"`
	CloudAction           types.Bool   `tfsdk:"cloud_action"`
	Labels                types.Map    `tfsdk:"labels"`
	Steps                 types.List   `tfsdk:"steps"`
	Params                types.List   `tfsdk:"params"`
	TaskName              types.String `tfsdk:"task_name"`
	Credentials           types.Map    `tfsdk:"credentials"`
	CredentialsSecretName types.String `tfsdk:"credentials_secret_name"`
	CredentialsSecret     types.String `tfsdk:"credentials_secret"`
}

func (r *FacetsActionsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_actions"
}

func (r *FacetsActionsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Tekton Task for a Facets action, for any cloud or none.\n\n" +
			"Cloud credentials are not configured here and never pass through Terraform. The provider " +
			"reads them from its own environment (variables prefixed `" + credentials.EnvPrefix + "`), " +
			"writes them to a Kubernetes Secret, and attaches that Secret to every step with `envFrom`, " +
			"so each key arrives as an environment variable under its own name. Nothing sensitive is " +
			"written to Terraform state, the plan file, or the rendered Task.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource identifier, in the form <namespace>/<task_name>",
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Display name of the action, as shown in Facets",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.LengthAtMost(253),
				},
			},
			"description": schema.StringAttribute{
				Description: "Description of what the action does",
				Optional:    true,
			},
			"facets_resource_name": schema.StringAttribute{
				Description: "Resource name from the Facets blueprint, used to map the action back to it",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.LengthAtMost(253),
				},
			},
			"facets_environment": schema.SingleNestedAttribute{
				Description: "Facets-managed environment configuration",
				Required:    true,
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
				Description: "Resource definition from the blueprint. Only `kind` is used, in labels.",
				Required:    true,
				Attributes: map[string]schema.Attribute{
					"kind": schema.StringAttribute{
						Description: "Resource kind",
						Required:    true,
					},
				},
			},
			"namespace": schema.StringAttribute{
				Description: "Namespace for the Task and the credentials Secret. Changing it forces " +
					"replacement, since Kubernetes objects cannot move between namespaces.",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(tektonPipelinesNamespace),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"cloud_action": schema.BoolAttribute{
				Description: "Whether this action mutates cloud infrastructure. Sets the `cloud_action` " +
					"label, which decides whether Facets requires RUN_CLOUD_ACTION rather than RUN_ACTION. " +
					"Set it true for anything that changes state outside the cluster -- stopping a " +
					"database, scaling a node group -- because the default grants the weaker permission.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"labels": schema.MapAttribute{
				Description: "Extra labels for the Task. Merged with the generated ones, which win on " +
					"conflict. Keys and values are sanitized to what Kubernetes accepts.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"steps": schema.ListNestedAttribute{
				Description: "Steps to run, in order",
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
							Description: "Container image. Must already contain whatever CLI the script " +
								"uses -- there is no shared base image.",
							Required: true,
						},
						"script": schema.StringAttribute{
							Description: "Script to run. Credentials arrive as environment variables, so " +
								"a cloud login is just the usual command for that cloud.",
							Required: true,
						},
						"env": schema.MapAttribute{
							Description: "Non-sensitive environment variables as name => value, emitted in " +
								"sorted key order. These appear verbatim in the Task, so never put a " +
								"credential here -- credentials come from the environment automatically.",
							Optional:    true,
							ElementType: types.StringType,
						},
						"resources": schema.SingleNestedAttribute{
							Description: "Compute requests and limits",
							Optional:    true,
							Attributes: map[string]schema.Attribute{
								"requests": schema.MapAttribute{
									Description: "Minimum compute resources (cpu, memory)",
									Optional:    true,
									ElementType: types.StringType,
								},
								"limits": schema.MapAttribute{
									Description: "Maximum compute resources (cpu, memory)",
									Optional:    true,
									ElementType: types.StringType,
								},
							},
						},
					},
				},
			},
			"params": schema.ListNestedAttribute{
				Description: "Parameters the action accepts when triggered",
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
							Description: "Parameter type",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.OneOf("string", "array", "object"),
							},
						},
					},
				},
			},
			"task_name": schema.StringAttribute{
				Description: "Generated Task name: a hash of resource name, environment and action name",
				Computed:    true,
			},
			"credentials": schema.MapAttribute{
				Description: "Cloud credentials as name => value, which the provider writes to a " +
					"Kubernetes Secret and attaches to every step with envFrom. Opaque to the " +
					"provider: whatever keys are supplied arrive in the pod under the same names, so " +
					"AWS keys, an Azure service principal and a GCP service account all work here.\n\n" +
					"Intended for wiring a cloud_account input directly, e.g. " +
					"AZURE_CLIENT_SECRET = var.inputs.cloud_account.attributes.client_secret.\n\n" +
					"Values do NOT appear in the rendered Task -- the Task carries only a secretRef. " +
					"They ARE persisted in Terraform state, because a resource attribute always is " +
					"before Terraform 1.11's write-only arguments; on 1.11+ prefer those, and where " +
					"the credential must never transit Terraform at all use credentials_secret_name " +
					"or the provider's own environment instead.",
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"credentials_secret_name": schema.StringAttribute{
				Description: "Name of an EXISTING Secret to attach to every step, for credentials that " +
					"come from somewhere other than the provider's environment -- a secret manager, a " +
					"Secret created by another resource, or one provisioned out of band. The provider " +
					"only references it and never writes to it, so its contents are managed entirely by " +
					"whoever created it. When set, credentials in the provider's environment are ignored.",
				Optional: true,
			},
			"credentials_secret": schema.StringAttribute{
				Description: "Name of the Secret actually attached to the steps: either " +
					"credentials_secret_name, or one derived from the environment when the provider " +
					"manages credentials itself. The name only -- never the credential values.",
				Computed: true,
			},
		},
	}
}

func (r *FacetsActionsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Nothing to configure: this resource takes no provider credentials, which is
	// the point of it. Clients are built lazily per operation so `terraform
	// validate` works without a kubeconfig.
}

func (r *FacetsActionsResource) client() (dynamic.Interface, *tekton.ResourceOperations, error) {
	factory := r.clientFactory
	if factory == nil {
		factory = k8s.GetKubernetesClient
	}
	c, err := factory()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	return c, tekton.NewResourceOperations(c), nil
}

// creds resolves the credentials to store, preferring those declared on the
// resource over the provider's environment. A module that wires its cloud_account
// input in knows exactly which account the action should act as; the environment
// is the fallback for runners configured centrally.
func (r *FacetsActionsResource) creds(ctx context.Context, m *FacetsActionsResourceModel) (map[string]string, error) {
	if m != nil && !m.Credentials.IsNull() && !m.Credentials.IsUnknown() {
		out := map[string]string{}
		if diags := m.Credentials.ElementsAs(ctx, &out, false); diags.HasError() {
			return nil, fmt.Errorf("reading credentials: %v", diags.Errors())
		}
		if err := credentials.ValidateNames(out); err != nil {
			return nil, err
		}
		return out, nil
	}
	if r.credsFromEnv != nil {
		return r.credsFromEnv()
	}
	return credentials.FromEnv()
}

// credentialsSecretName derives the Secret name from the environment.
//
// Credentials belong to an environment, not to an individual action, so every
// action in an environment shares one Secret: one object to rotate rather than
// one per action. The name is a hash so it cannot collide with anything else in
// a namespace shared by every project on a control plane.
func credentialsSecretName(envUniqueName string) string {
	sum := sha256.Sum256([]byte(envUniqueName))
	return fmt.Sprintf("%s-%s", credentialsSecretPrefix, hex.EncodeToString(sum[:])[:16])
}

func (r *FacetsActionsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan FacetsActionsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, ops, err := r.client()
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Kubernetes client", err.Error())
		return
	}

	task, diags := r.buildTask(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Credentials first: a Task whose steps reference a Secret that does not
	// exist yet starts and fails at the pod, which is a much worse signal than a
	// failed apply.
	resp.Diagnostics.Append(r.reconcileCredentials(ctx, ops, &plan, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := ops.CreateResource(ctx, task, actionTaskGVR.Group, actionTaskGVR.Version, actionTaskGVR.Resource); err != nil {
		resp.Diagnostics.AddError("Error creating Task", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *FacetsActionsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state FacetsActionsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, ops, err := r.client()
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Kubernetes client", err.Error())
		return
	}

	ns := namespaceOrDefault(state.Namespace)

	if _, err := client.Resource(actionTaskGVR).Namespace(ns).
		Get(ctx, state.TaskName.ValueString(), metav1.GetOptions{}); err != nil {
		// Only a genuine NotFound means it is gone. A transient failure -- 503,
		// Forbidden, timeout -- must retain state, or the next apply tries to
		// recreate an object that is still there.
		if apierrors.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Task",
			fmt.Sprintf("Could not read Task %s/%s: %s", ns, state.TaskName.ValueString(), err))
		return
	}

	// Credentials live outside Terraform, so no attribute changes when they
	// rotate and Terraform would otherwise never call Update. Reconciling here is
	// what makes rotation converge. The reconcile compares before writing, so a
	// plan against unchanged credentials performs no write.
	resp.Diagnostics.Append(r.reconcileCredentials(ctx, ops, &state, true)...)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *FacetsActionsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state FacetsActionsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, ops, err := r.client()
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Kubernetes client", err.Error())
		return
	}

	task, diags := r.buildTask(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.reconcileCredentials(ctx, ops, &plan, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := ops.UpdateResource(ctx, task, actionTaskGVR.Group, actionTaskGVR.Version, actionTaskGVR.Resource); err != nil {
		resp.Diagnostics.AddError("Error updating Task", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *FacetsActionsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state FacetsActionsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, ops, err := r.client()
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Kubernetes client", err.Error())
		return
	}

	// Only the Task is removed. The credentials Secret is shared by every action
	// in the environment, so deleting it here would break the ones that remain;
	// it is reclaimed with the environment, not with an individual action.
	if err := ops.DeleteResource(ctx, namespaceOrDefault(state.Namespace), state.TaskName.ValueString(),
		actionTaskGVR.Group, actionTaskGVR.Version, actionTaskGVR.Resource); err != nil {
		resp.Diagnostics.AddError("Error deleting Task", err.Error())
	}
}

// ImportState accepts "<task-name>" or "<namespace>/<task-name>".
//
// `name` is deliberately not reconstructed from the display_name label: that
// label is sanitized for Kubernetes and cannot round-trip a name containing a
// space or any other rejected character. Importing a silently wrong value is
// worse than leaving it for the operator to supply.
func (r *FacetsActionsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	namespace, taskName := tektonPipelinesNamespace, req.ID
	if idx := strings.Index(req.ID, "/"); idx >= 0 {
		namespace, taskName = req.ID[:idx], req.ID[idx+1:]
	}
	if taskName == "" || namespace == "" {
		resp.Diagnostics.AddError("Invalid import ID",
			`Expected "<task-name>" or "<namespace>/<task-name>", got `+req.ID)
		return
	}

	client, _, err := r.client()
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Kubernetes client", err.Error())
		return
	}

	task, err := client.Resource(actionTaskGVR).Namespace(namespace).Get(ctx, taskName, metav1.GetOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Error importing resource",
			fmt.Sprintf("Could not find Task %s/%s: %s", namespace, taskName, err))
		return
	}

	labels, _, _ := unstructured.NestedStringMap(task.Object, "metadata", "labels")
	envUnique := labels["environment_unique_name"]

	state := FacetsActionsResourceModel{
		ID:                    types.StringValue(namespace + "/" + taskName),
		Namespace:             types.StringValue(namespace),
		TaskName:              types.StringValue(taskName),
		FacetsResourceName:    types.StringValue(labels["resource_name"]),
		CloudAction:           types.BoolValue(labels["cloud_action"] == "true"),
		CredentialsSecret:     types.StringValue(credentialsSecretName(envUnique)),
		CredentialsSecretName: types.StringNull(),
	}

	resp.Diagnostics.AddWarning("Partial import",
		"Imported namespace, task_name, facets_resource_name and cloud_action. You must supply name, "+
			"facets_environment, facets_resource, steps and params in configuration: the Task does not "+
			"retain them losslessly.")
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// reconcileCredentials writes the environment's credentials into the Secret.
//
// When soft is true (the Read path) failures surface as warnings, because Read
// must not break a plan. Create and Update fail hard: a missing or stale Secret
// there means the action cannot work, and reporting success would hide it.
func (r *FacetsActionsResource) reconcileCredentials(
	ctx context.Context, ops *tekton.ResourceOperations, m *FacetsActionsResourceModel, soft bool,
) diag.Diagnostics {
	var diags diag.Diagnostics

	// An externally supplied Secret is owned by whoever created it. Writing to it
	// would clobber credentials this provider did not put there.
	if !m.CredentialsSecretName.IsNull() && m.CredentialsSecretName.ValueString() != "" {
		return diags
	}

	creds, err := r.creds(ctx, m)
	if err != nil {
		diags.AddError("Invalid action credentials", err.Error())
		return diags
	}
	if len(creds) == 0 {
		// Legitimate: an action driving only the cluster needs no cloud
		// credentials, and the pod's service account already covers it.
		return diags
	}

	ns := namespaceOrDefault(m.Namespace)
	secret := m.CredentialsSecret.ValueString()
	if secret == "" {
		return diags
	}

	if _, err := ops.ReconcileCredentialsSecret(ctx, ns, secret, creds); err != nil {
		msg := fmt.Sprintf("Could not write the action credentials Secret %s/%s: %s", ns, secret, err)
		if soft {
			diags.AddWarning("Action credentials not reconciled",
				msg+"\n\nThe action will keep using whatever credentials are already in the cluster.")
		} else {
			diags.AddError("Action credentials not reconciled", msg)
		}
	}
	return diags
}

func namespaceOrDefault(ns types.String) string {
	if ns.IsNull() || ns.ValueString() == "" {
		return tektonPipelinesNamespace
	}
	return ns.ValueString()
}

// buildTask assembles the Task and fills in the computed fields on the model.
func (r *FacetsActionsResource) buildTask(ctx context.Context, plan *FacetsActionsResourceModel) (*unstructured.Unstructured, diag.Diagnostics) {
	var diags diag.Diagnostics

	var env tekton.FacetsEnvironmentModel
	diags.Append(plan.FacetsEnvironment.As(ctx, &env, basetypes.ObjectAsOptions{})...)
	var res tekton.FacetsResourceModel
	diags.Append(plan.FacetsResource.As(ctx, &res, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, diags
	}

	namespace := namespaceOrDefault(plan.Namespace)
	names := tekton.GenerateNames(
		plan.FacetsResourceName.ValueString(),
		env.UniqueName.ValueString(),
		plan.Name.ValueString(),
	)

	plan.Namespace = types.StringValue(namespace)
	plan.TaskName = types.StringValue(names.TaskName)
	plan.ID = types.StringValue(namespace + "/" + names.TaskName)
	// An explicitly named Secret wins: the module knows where its credentials come
	// from, and the provider is not required to have any of its own.
	if !plan.CredentialsSecretName.IsNull() && plan.CredentialsSecretName.ValueString() != "" {
		plan.CredentialsSecret = plan.CredentialsSecretName
	} else {
		plan.CredentialsSecret = types.StringValue(credentialsSecretName(env.UniqueName.ValueString()))
	}

	customLabels := map[string]string{}
	if !plan.Labels.IsNull() {
		diags.Append(plan.Labels.ElementsAs(ctx, &customLabels, false)...)
		if diags.HasError() {
			return nil, diags
		}
	}

	metadata := tekton.NewResourceMetadata(
		plan.Name.ValueString(),
		plan.FacetsResourceName.ValueString(),
		res.Kind.ValueString(),
		env.UniqueName.ValueString(),
		plan.CloudAction.ValueBool(),
		customLabels,
	)

	var steps []tekton.ActionStepModel
	diags.Append(plan.Steps.ElementsAs(ctx, &steps, false)...)
	if diags.HasError() {
		return nil, diags
	}
	if len(steps) == 0 {
		diags.AddError("No steps", "An action must declare at least one step.")
		return nil, diags
	}

	// Attach the Secret only when credentials exist. Referencing a Secret that
	// was never created would leave every pod stuck in CreateContainerConfigError.
	secretForSteps := ""
	if !plan.CredentialsSecretName.IsNull() && plan.CredentialsSecretName.ValueString() != "" {
		secretForSteps = plan.CredentialsSecretName.ValueString()
	} else {
		creds, err := r.creds(ctx, plan)
		if err != nil {
			diags.AddError("Invalid action credentials in the environment", err.Error())
			return nil, diags
		}
		if len(creds) > 0 {
			secretForSteps = plan.CredentialsSecret.ValueString()
		}
	}

	tektonSteps := make([]interface{}, 0, len(steps))
	for _, s := range steps {
		built, err := tekton.BuildActionStep(ctx, s, secretForSteps)
		if err != nil {
			diags.AddError("Invalid step", err.Error())
			return nil, diags
		}
		tektonSteps = append(tektonSteps, built)
	}

	taskParams := []interface{}{}
	if !plan.Params.IsNull() {
		var params []tekton.ParamModel
		diags.Append(plan.Params.ElementsAs(ctx, &params, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for _, p := range params {
			taskParams = append(taskParams, map[string]interface{}{
				"name": p.Name.ValueString(),
				"type": p.Type.ValueString(),
			})
		}
	}

	description := plan.Name.ValueString()
	if !plan.Description.IsNull() && plan.Description.ValueString() != "" {
		description = plan.Description.ValueString()
	}

	return tekton.BuildTask(tekton.TaskSpec{
		TaskName:    names.TaskName,
		Namespace:   namespace,
		Description: description,
		Labels:      metadata.LabelsAsInterface(),
	}, tektonSteps, taskParams), diags
}
