package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &FacetsProvider{}

type FacetsProvider struct {
	version string
}

type FacetsProviderModel struct {
	AWS   types.Object `tfsdk:"aws"`
	Azure types.Object `tfsdk:"azure"`
}

type ProviderAzureConfig struct {
	SubscriptionID     types.String `tfsdk:"subscription_id"`
	TenantID           types.String `tfsdk:"tenant_id"`
	ClientID           types.String `tfsdk:"client_id"`
	ClientSecret       types.String `tfsdk:"client_secret"`
	UseOIDCFederation  types.Bool   `tfsdk:"use_oidc_federation"`
	FederatedTokenFile types.String `tfsdk:"federated_token_file"`
}

type ProviderAWSConfig struct {
	Region     types.String `tfsdk:"region"`
	AssumeRole types.Object `tfsdk:"assume_role"`
}

type ProviderAWSAssumeRoleConfig struct {
	RoleARN     types.String `tfsdk:"role_arn"`
	ExternalID  types.String `tfsdk:"external_id"`
	SessionName types.String `tfsdk:"session_name"`
}

func (p *FacetsProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "facets"
	resp.Version = p.version
}

func (p *FacetsProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Facets Terraform Provider for creating Tekton actions and other Facets resources",
		Attributes: map[string]schema.Attribute{
			"aws": schema.SingleNestedAttribute{
				Description: "AWS configuration for facets_tekton_action_aws resources. " +
					"This block is optional and only required when using AWS actions. " +
					"Uses IRSA (IAM Roles for Service Accounts) for authentication - the pod's service account " +
					"must be configured with IAM role annotation. The AWS CLI will use the pod's IRSA credentials " +
					"to assume the target role specified in assume_role configuration.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"region": schema.StringAttribute{
						Description: "AWS region (e.g., us-west-2)",
						Required:    true,
					},
					"assume_role": schema.SingleNestedAttribute{
						Description: "Configuration for assuming an IAM role using IRSA. The pod's service account " +
							"must have permissions to assume the specified role. At runtime, the AWS SDK will use " +
							"the pod's IRSA credentials to assume this role via AWS STS AssumeRole.",
						Required: true,
						Attributes: map[string]schema.Attribute{
							"role_arn": schema.StringAttribute{
								Description: "ARN of the IAM role to assume (e.g., arn:aws:iam::123456789012:role/my-role). " +
									"This role's trust policy must allow the pod's IRSA role to assume it.",
								Required: true,
							},
							"external_id": schema.StringAttribute{
								Description: "External ID for assuming the role. Required when the role's trust policy " +
									"specifies an external ID condition. This provides additional security against " +
									"the confused deputy problem.",
								Optional: true,
							},
							"session_name": schema.StringAttribute{
								Description: "Session name to use when assuming the role. If not provided, a random " +
									"session name will be generated. This appears in CloudTrail logs and can be used " +
									"for tracking and auditing purposes.",
								Optional: true,
							},
						},
					},
				},
			},
			"azure": schema.SingleNestedAttribute{
				Description: "Azure configuration for facets_tekton_action_azure resources. " +
					"Optional; only required when using Azure actions. Two authentication modes are " +
					"supported: OIDC federation (preferred -- Microsoft Entra ID exchanges the pod's " +
					"projected service-account token for an Azure token, so no secret exists anywhere, " +
					"and it works cross-cloud from an EKS-hosted pod), or a service principal client " +
					"secret read from a Kubernetes Secret at pod start. Either way the user triggering " +
					"the action never supplies credentials.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"cloud_account_id": schema.StringAttribute{
						Description: "RECOMMENDED. ID of the Facets-linked Azure cloud account. The action " +
							"resolves that account's credentials at run time using the pod's own cloud " +
							"identity, so nothing sensitive is stored in Terraform state, in the Tekton " +
							"Task manifest, or in a Kubernetes Secret -- and no per-cluster setup is " +
							"required. Mutually exclusive with client_secret and use_oidc_federation; " +
							"when set, subscription_id / tenant_id / client_id are resolved at run time " +
							"and need not be supplied.",
						Optional: true,
					},
					"secret_manager_path": schema.StringAttribute{
						Description: "OPTIONAL override for the secret id holding the cloud account " +
							"credentials. Normally omit this: the action derives the id at run time from " +
							"the control plane's own environment, so end users never need to know the " +
							"internal secret layout -- they supply only cloud_account_id. Set this only " +
							"when the credentials live somewhere non-standard.",
						Optional: true,
					},
					"subscription_id": schema.StringAttribute{
						Description: "Azure subscription ID that owns the target resources. Not required " +
							"in cloud_account_id mode.",
						Optional: true,
					},
					"tenant_id": schema.StringAttribute{
						Description: "Microsoft Entra ID (Azure AD) tenant ID. Not required in " +
							"cloud_account_id mode.",
						Optional: true,
					},
					"client_id": schema.StringAttribute{
						Description: "Application (client) ID of the service principal / managed identity. " +
							"Not required in cloud_account_id mode.",
						Optional: true,
					},
					"client_secret": schema.StringAttribute{
						Description: "Service principal client secret. Mutually exclusive with " +
							"use_oidc_federation. Prefer OIDC federation where the federated credential " +
							"can be registered, since a secret set here is persisted in Terraform state.",
						Optional:  true,
						Sensitive: true,
					},
					"secret_name": schema.StringAttribute{
						Description: "Name of the Kubernetes Secret in the Tekton namespace holding the " +
							"service principal secret, used by client-secret mode. Created out of band " +
							"(e.g. by a k8s_resource module); this provider only references it, so the " +
							"value never appears in the Task manifest. Defaults to " +
							"\"facets-azure-credentials\". NOTE: Tekton actions share one namespace " +
							"across all projects on a control plane, so set this per project to avoid " +
							"two tenants colliding on one Secret.",
						Optional: true,
					},
					"secret_key": schema.StringAttribute{
						Description: "Key within secret_name holding the client secret. Defaults to " +
							"\"client_secret\".",
						Optional: true,
					},
					"use_oidc_federation": schema.BoolAttribute{
						Description: "Authenticate by exchanging the pod's projected service-account token " +
							"with Microsoft Entra ID instead of using a client secret. Requires a federated " +
							"identity credential on the app registration trusting the cluster OIDC issuer, " +
							"with audience api://AzureADTokenExchange.",
						Optional: true,
					},
					"federated_token_file": schema.StringAttribute{
						Description: "In-pod path of the projected service-account token. Defaults to " +
							"/var/run/secrets/azure/tokens/azure-identity-token. The token must be projected " +
							"with audience api://AzureADTokenExchange -- reusing the default service-account " +
							"token fails with AADSTS700212.",
						Optional: true,
					},
				},
			},
		},
	}
}

func (p *FacetsProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config FacetsProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Store provider data for resource access
	// AWS config validation happens in the resource's Configure() method
	resp.ResourceData = &config
}

func (p *FacetsProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewTektonActionKubernetesResource,
		NewTektonActionAWSResource,
		NewTektonActionAzureResource,
	}
}

func (p *FacetsProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &FacetsProvider{
			version: version,
		}
	}
}
