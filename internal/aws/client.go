package aws

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// ProviderModel represents the Facets provider configuration
// Note: This duplicates the structure from internal/provider to avoid import cycles
type ProviderModel struct {
	AWS types.Object `tfsdk:"aws"`
}

// ProviderAWSConfig represents AWS configuration from the provider
type ProviderAWSConfig struct {
	Region     types.String `tfsdk:"region"`
	AccessKey  types.String `tfsdk:"access_key"`
	SecretKey  types.String `tfsdk:"secret_key"`
	AssumeRole types.Object `tfsdk:"assume_role"`
}

// ProviderAWSAssumeRoleConfig represents assume_role configuration
type ProviderAWSAssumeRoleConfig struct {
	RoleARN     types.String `tfsdk:"role_arn"`
	SessionName types.String `tfsdk:"session_name"`
	ExternalID  types.String `tfsdk:"external_id"`
	Duration    types.Int64  `tfsdk:"duration"`
}

// AWSAuthConfig represents processed AWS authentication configuration
// This is returned by GetAWSConfig and contains either inline creds or assume_role config
type AWSAuthConfig struct {
	Region string
	// Inline credentials (nil if using assume_role without inline creds)
	InlineCredentials *InlineCredentials
	// AssumeRole configuration (nil if using inline credentials only)
	AssumeRoleConfig *AssumeRoleConfig
}

// InlineCredentials represents static AWS credentials
type InlineCredentials struct {
	AccessKey string
	SecretKey string
}

// AssumeRoleConfig represents processed assume_role configuration
type AssumeRoleConfig struct {
	RoleARN     string
	SessionName string
	ExternalID  string
	Duration    int64
	// Base credentials needed to assume the role
	BaseCredentials *InlineCredentials
}

// GetAWSConfig extracts and validates AWS configuration from provider data
// Returns the processed AWS auth config or an error if missing/invalid
//
// Validation rules:
// 1. Region is always required
// 2. Must have either:
//    a) Inline credentials (access_key + secret_key) - for static auth
//    b) Assume role (assume_role block) - uses ambient/pod credentials
// 3. Priority: If both inline creds and assume_role are provided, inline creds take priority
func GetAWSConfig(ctx context.Context, providerModel *ProviderModel) (*AWSAuthConfig, error) {
	if providerModel == nil {
		return nil, fmt.Errorf("provider model is nil")
	}

	// Check if AWS configuration is present
	if providerModel.AWS.IsNull() {
		return nil, fmt.Errorf("AWS configuration is required for facets_tekton_action_aws resource. " +
			"Please add an 'aws' block to your provider configuration")
	}

	// Extract AWS configuration
	var awsConfig ProviderAWSConfig
	diags := providerModel.AWS.As(ctx, &awsConfig, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, fmt.Errorf("failed to extract AWS configuration: %v", diags.Errors())
	}

	// Validate region (always required)
	if awsConfig.Region.IsNull() || awsConfig.Region.ValueString() == "" {
		return nil, fmt.Errorf("AWS region is required in the provider configuration. " +
			"Please specify 'region' in the aws block")
	}

	region := awsConfig.Region.ValueString()

	// Check if inline credentials are provided
	hasInlineCreds := !awsConfig.AccessKey.IsNull() && awsConfig.AccessKey.ValueString() != "" &&
		!awsConfig.SecretKey.IsNull() && awsConfig.SecretKey.ValueString() != ""

	// Check if assume_role is configured
	hasAssumeRole := !awsConfig.AssumeRole.IsNull()

	// Case 1: No auth method at all
	if !hasInlineCreds && !hasAssumeRole {
		return nil, fmt.Errorf("AWS authentication is required. " +
			"Please provide either (access_key + secret_key) OR assume_role configuration in the aws block")
	}

	// Case 2: Priority - inline credentials take precedence
	// If inline creds are provided, use them regardless of assume_role
	if hasInlineCreds {
		return &AWSAuthConfig{
			Region: region,
			InlineCredentials: &InlineCredentials{
				AccessKey: awsConfig.AccessKey.ValueString(),
				SecretKey: awsConfig.SecretKey.ValueString(),
			},
			AssumeRoleConfig: nil,
		}, nil
	}

	// Case 3: Only assume_role (no inline creds) - use ambient credentials
	// This means the pod/environment has IAM permissions to assume the role
	if hasAssumeRole {
		// Extract and validate assume_role configuration
		var assumeRoleConfig ProviderAWSAssumeRoleConfig
		diags := awsConfig.AssumeRole.As(ctx, &assumeRoleConfig, basetypes.ObjectAsOptions{})
		if diags.HasError() {
			return nil, fmt.Errorf("failed to extract assume_role configuration: %v", diags.Errors())
		}

		// Validate role_arn
		if assumeRoleConfig.RoleARN.IsNull() || assumeRoleConfig.RoleARN.ValueString() == "" {
			return nil, fmt.Errorf("role_arn is required in the assume_role block")
		}

		roleARN := assumeRoleConfig.RoleARN.ValueString()

		// Validate ARN format
		if len(roleARN) < 20 || roleARN[:13] != "arn:aws:iam::" {
			return nil, fmt.Errorf("invalid role_arn format: %s. Expected format: arn:aws:iam::ACCOUNT_ID:role/ROLE_NAME", roleARN)
		}

		// Set defaults for optional fields
		sessionName := "terraform-provider-session"
		if !assumeRoleConfig.SessionName.IsNull() && assumeRoleConfig.SessionName.ValueString() != "" {
			sessionName = assumeRoleConfig.SessionName.ValueString()
		}

		externalID := ""
		if !assumeRoleConfig.ExternalID.IsNull() {
			externalID = assumeRoleConfig.ExternalID.ValueString()
		}

		duration := int64(3600) // 1 hour default
		if !assumeRoleConfig.Duration.IsNull() {
			duration = assumeRoleConfig.Duration.ValueInt64()
			// Validate duration range
			if duration < 900 || duration > 43200 {
				return nil, fmt.Errorf("assume_role duration must be between 900 (15 minutes) and 43200 (12 hours), got: %d", duration)
			}
		}

		return &AWSAuthConfig{
			Region:            region,
			InlineCredentials: nil,
			AssumeRoleConfig: &AssumeRoleConfig{
				RoleARN:         roleARN,
				SessionName:     sessionName,
				ExternalID:      externalID,
				Duration:        duration,
				BaseCredentials: nil, // No base credentials - using ambient/pod credentials
			},
		}, nil
	}

	// This should never be reached, but handle it gracefully
	return nil, fmt.Errorf("invalid AWS configuration state")
}

