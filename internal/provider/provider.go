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
	AWS types.Object `tfsdk:"aws"`
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
