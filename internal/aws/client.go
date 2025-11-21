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
	AssumeRole types.Object `tfsdk:"assume_role"`
}

// ProviderAWSAssumeRoleConfig represents assume_role configuration
type ProviderAWSAssumeRoleConfig struct {
	RoleARN     types.String `tfsdk:"role_arn"`
	ExternalID  types.String `tfsdk:"external_id"`
	SessionName types.String `tfsdk:"session_name"`
}

// AWSAuthConfig represents processed AWS authentication configuration
// This contains only assume_role configuration for IRSA-based authentication
type AWSAuthConfig struct {
	Region           string
	AssumeRoleConfig *AssumeRoleConfig
}

// AssumeRoleConfig represents processed assume_role configuration
// Uses IRSA (pod's IAM role) to assume the target role - no static credentials
type AssumeRoleConfig struct {
	RoleARN     string
	ExternalID  string
	SessionName string
}

// GetAWSConfig extracts and validates AWS configuration from provider data
// Returns the processed AWS auth config or an error if missing/invalid
//
// Validation rules:
// 1. Region is required
// 2. assume_role block is required (must provide role_arn)
// 3. external_id is optional (only needed if target role's trust policy requires it)
//
// Authentication flow:
// - Uses IRSA (IAM Roles for Service Accounts) from the pod's service account
// - Pod's IRSA role must have permission to assume the target role
// - At runtime, AWS SDK uses pod's IRSA credentials to assume the specified role
func GetAWSConfig(ctx context.Context, providerModel *ProviderModel) (*AWSAuthConfig, error) {
	if providerModel == nil {
		return nil, fmt.Errorf("provider model is nil")
	}

	// Check if AWS configuration is present
	if providerModel.AWS.IsNull() {
		return nil, fmt.Errorf("AWS configuration is required for facets_tekton_action_aws resource. " +
			"Please add an 'aws' block to your provider configuration with region and assume_role")
	}

	// Extract AWS configuration
	var awsConfig ProviderAWSConfig
	diags := providerModel.AWS.As(ctx, &awsConfig, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, fmt.Errorf("failed to extract AWS configuration: %v", diags.Errors())
	}

	// Validate region (required)
	if awsConfig.Region.IsNull() || awsConfig.Region.ValueString() == "" {
		return nil, fmt.Errorf("AWS region is required in the provider configuration. " +
			"Please specify 'region' in the aws block")
	}

	region := awsConfig.Region.ValueString()

	// Validate assume_role (required)
	if awsConfig.AssumeRole.IsNull() {
		return nil, fmt.Errorf("assume_role configuration is required in the aws block. " +
			"Please provide an assume_role block with role_arn")
	}

	// Extract and validate assume_role configuration
	var assumeRoleConfig ProviderAWSAssumeRoleConfig
	diags = awsConfig.AssumeRole.As(ctx, &assumeRoleConfig, basetypes.ObjectAsOptions{})
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

	// Extract optional external_id
	externalID := ""
	if !assumeRoleConfig.ExternalID.IsNull() {
		externalID = assumeRoleConfig.ExternalID.ValueString()
	}

	// Extract optional session_name
	sessionName := ""
	if !assumeRoleConfig.SessionName.IsNull() {
		sessionName = assumeRoleConfig.SessionName.ValueString()
	}

	return &AWSAuthConfig{
		Region: region,
		AssumeRoleConfig: &AssumeRoleConfig{
			RoleARN:     roleARN,
			ExternalID:  externalID,
			SessionName: sessionName,
		},
	}, nil
}
