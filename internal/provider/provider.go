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
	AccessKey  types.String `tfsdk:"access_key"`
	SecretKey  types.String `tfsdk:"secret_key"`
	AssumeRole types.Object `tfsdk:"assume_role"`
}

type ProviderAWSAssumeRoleConfig struct {
	RoleARN     types.String `tfsdk:"role_arn"`
	SessionName types.String `tfsdk:"session_name"`
	ExternalID  types.String `tfsdk:"external_id"`
	Duration    types.Int64  `tfsdk:"duration"`
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
					"If only using Kubernetes actions, this can be omitted. " +
					"Supports either inline credentials (access_key + secret_key) or assume_role configuration with ambient credentials.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"region": schema.StringAttribute{
						Description: "AWS region (e.g., us-west-2)",
						Optional:    true,
					},
					"access_key": schema.StringAttribute{
						Description: "AWS Access Key ID. Optional - only required for inline authentication. " +
							"When using assume_role with ambient/pod credentials (IRSA, instance profile), this can be omitted.",
						Optional:  true,
						Sensitive: true,
					},
					"secret_key": schema.StringAttribute{
						Description: "AWS Secret Access Key. Optional - only required for inline authentication. " +
							"When using assume_role with ambient/pod credentials (IRSA, instance profile), this can be omitted.",
						Optional:  true,
						Sensitive: true,
					},
					"assume_role": schema.SingleNestedAttribute{
						Description: "Configuration for assuming an IAM role. When specified, the provider will use AWS STS " +
							"AssumeRole to obtain temporary credentials at Task runtime. If access_key and secret_key are omitted, " +
							"the provider will use ambient credentials (IRSA, instance profile, etc.) to assume the role.",
						Optional: true,
						Attributes: map[string]schema.Attribute{
							"role_arn": schema.StringAttribute{
								Description: "ARN of the IAM role to assume (e.g., arn:aws:iam::123456789012:role/my-role)",
								Required:    true,
							},
							"session_name": schema.StringAttribute{
								Description: "Session name for the assumed role session. Used for CloudTrail auditing. " +
									"If not specified, defaults to 'terraform-provider-session'.",
								Optional: true,
							},
							"external_id": schema.StringAttribute{
								Description: "External ID for assuming the role. Required when the role's trust policy " +
									"specifies an external ID condition.",
								Optional: true,
							},
							"duration": schema.Int64Attribute{
								Description: "Duration of the assumed role session in seconds. " +
									"Must be between 900 (15 minutes) and 43200 (12 hours). Defaults to 3600 (1 hour).",
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
