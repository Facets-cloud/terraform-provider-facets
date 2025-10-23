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
	Region    types.String `tfsdk:"region"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
}

// GetAWSConfig extracts and validates AWS configuration from provider data
// Returns the AWS config or an error if missing/invalid
func GetAWSConfig(ctx context.Context, providerModel *ProviderModel) (*ProviderAWSConfig, error) {
	if providerModel == nil {
		return nil, fmt.Errorf("provider model is nil")
	}

	// Check if AWS configuration is present
	if providerModel.AWS.IsNull() {
		return nil, fmt.Errorf("AWS configuration is required for facets_tekton_action_aws resource. " +
			"Please add an 'aws' block to your provider configuration with region, access_key, and secret_key")
	}

	// Extract AWS configuration
	var awsConfig ProviderAWSConfig
	diags := providerModel.AWS.As(ctx, &awsConfig, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, fmt.Errorf("failed to extract AWS configuration: %v", diags.Errors())
	}

	// Validate all required fields are present and non-empty
	if awsConfig.Region.IsNull() || awsConfig.Region.ValueString() == "" {
		return nil, fmt.Errorf("AWS region is required in the provider configuration. " +
			"Please specify 'region' in the aws block")
	}

	if awsConfig.AccessKey.IsNull() || awsConfig.AccessKey.ValueString() == "" {
		return nil, fmt.Errorf("AWS access_key is required in the provider configuration. " +
			"Please specify 'access_key' in the aws block")
	}

	if awsConfig.SecretKey.IsNull() || awsConfig.SecretKey.ValueString() == "" {
		return nil, fmt.Errorf("AWS secret_key is required in the provider configuration. " +
			"Please specify 'secret_key' in the aws block")
	}

	return &awsConfig, nil
}
