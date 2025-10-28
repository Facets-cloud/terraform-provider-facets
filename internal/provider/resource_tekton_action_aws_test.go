package provider

import (
	"strings"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/aws"
)

func TestGenerateAWSResourceNames(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		envName      string
		displayName  string
		wantPrefix   string
	}{
		{
			name:         "short names",
			resourceName: "my-app",
			envName:      "prod",
			displayName:  "restart",
			wantPrefix:   "setup-aws-credentials-",
		},
		{
			name:         "long names",
			resourceName: "my-very-long-application-name-that-should-be-hashed",
			envName:      "production-environment",
			displayName:  "very-long-display-name-for-testing",
			wantPrefix:   "setup-aws-credentials-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskName, stepActionName := generateAWSResourceNames(tt.resourceName, tt.envName, tt.displayName)

			// Task name should be a hash
			if len(taskName) == 0 {
				t.Errorf("taskName is empty")
			}
			if len(taskName) > 63 {
				t.Errorf("taskName exceeds 63 chars: %d", len(taskName))
			}

			// StepAction name should have AWS-specific prefix
			if len(stepActionName) == 0 {
				t.Errorf("stepActionName is empty")
			}
			if len(stepActionName) > 63 {
				t.Errorf("stepActionName exceeds 63 chars: %d", len(stepActionName))
			}

			// Verify StepAction name starts with correct prefix or contains hash
			if len(stepActionName) < len(tt.wantPrefix) {
				t.Errorf("stepActionName too short to contain prefix")
			}

			// StepAction name should be setup-aws-credentials-{hash}
			// After truncation to 63 chars, it might not have full prefix
			// But it should contain "aws-credentials-" at minimum
			if len(stepActionName) == 63 {
				// Truncated - check it contains expected substring
				if stepActionName[0:len("aws-credentials-")] != "aws-credentials-" &&
					stepActionName[0:len("setup-aws-cred")] != "setup-aws-cred" {
					t.Errorf("stepActionName doesn't contain expected AWS prefix pattern: %s", stepActionName)
				}
			} else {
				// Not truncated - should have full prefix
				if stepActionName[0:len(tt.wantPrefix)] != tt.wantPrefix {
					t.Errorf("stepActionName prefix = %s, want %s", stepActionName[0:len(tt.wantPrefix)], tt.wantPrefix)
				}
			}
		})
	}
}

func TestBuildAWSStepAction(t *testing.T) {
	// Note: This test validates structure but can't test actual credential injection
	// since we don't have real provider data in unit tests
	// The credential injection is tested in integration tests

	// Skip this test since it requires provider data with AWS config
	// This is tested in integration tests instead
	t.Skip("Skipping unit test - requires provider data with AWS config. See integration tests.")
}

// Helper functions for tests

func unstructuredNestedMap(obj map[string]interface{}, fields ...string) (map[string]interface{}, bool, error) {
	current := obj
	for _, field := range fields {
		val, found := current[field]
		if !found {
			return nil, false, nil
		}
		if field == fields[len(fields)-1] {
			m, ok := val.(map[string]interface{})
			return m, ok, nil
		}
		next, ok := val.(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		current = next
	}
	return current, true, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

// Test script generation for inline credentials
func TestGenerateInlineCredentialsScript(t *testing.T) {
	config := &aws.AWSAuthConfig{
		Region: "us-west-2",
		InlineCredentials: &aws.InlineCredentials{
			AccessKey: "AKIAIOSFODNN7EXAMPLE",
			SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	script := generateInlineCredentialsScript(config)

	// Validate script contains expected elements
	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script missing shebang")
	}
	if !strings.Contains(script, "set -e") {
		t.Error("Script missing error handling")
	}
	if !strings.Contains(script, "mkdir -p /workspace/.aws") {
		t.Error("Script missing directory creation")
	}
	if !strings.Contains(script, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("Script missing access key")
	}
	if !strings.Contains(script, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY") {
		t.Error("Script missing secret key")
	}
	if !strings.Contains(script, "region = us-west-2") {
		t.Error("Script missing region")
	}
	if !strings.Contains(script, "chmod 600") {
		t.Error("Script missing permissions setting")
	}
	// Note: AWS env vars are injected in buildAWSTask, not in the script
}

// Test script generation for assume role
func TestGenerateAssumeRoleScript(t *testing.T) {
	config := &aws.AWSAuthConfig{
		Region: "us-east-1",
		AssumeRoleConfig: &aws.AssumeRoleConfig{
			RoleARN:         "arn:aws:iam::123456789012:role/my-role",
			SessionName:     "test-session",
			ExternalID:      "my-external-id",
			Duration:        3600,
			BaseCredentials: nil, // Always uses ambient/IRSA credentials now
		},
	}

	script := generateAssumeRoleScript(config)

	// Validate script contains expected elements
	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script missing shebang")
	}
	if !strings.Contains(script, "set -e") {
		t.Error("Script missing error handling")
	}
	if !strings.Contains(script, "arn:aws:iam::123456789012:role/my-role") {
		t.Error("Script missing role ARN")
	}
	if !strings.Contains(script, "--role-session-name \"test-session\"") {
		t.Error("Script missing session name")
	}
	if !strings.Contains(script, "--external-id \"my-external-id\"") {
		t.Error("Script missing external ID")
	}
	if !strings.Contains(script, "--duration-seconds 3600") {
		t.Error("Script missing duration")
	}
	if !strings.Contains(script, "aws sts assume-role") {
		t.Error("Script missing AWS STS assume-role command")
	}
	if !strings.Contains(script, "jq -r '.Credentials.AccessKeyId'") {
		t.Error("Script missing credential extraction with jq")
	}
	if !strings.Contains(script, "aws_session_token") {
		t.Error("Script missing session token")
	}
	if !strings.Contains(script, "chmod 600") {
		t.Error("Script missing permissions setting")
	}
	// Note: Always uses ambient credentials (IRSA), not base credentials
}

// Test assume role script without external ID
func TestGenerateAssumeRoleScriptWithoutExternalID(t *testing.T) {
	config := &aws.AWSAuthConfig{
		Region: "us-east-1",
		AssumeRoleConfig: &aws.AssumeRoleConfig{
			RoleARN:     "arn:aws:iam::123456789012:role/my-role",
			SessionName: "test-session",
			ExternalID:  "", // No external ID
			Duration:    7200,
			BaseCredentials: &aws.InlineCredentials{
				AccessKey: "AKIAIOSFODNN7EXAMPLE",
				SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		},
	}

	script := generateAssumeRoleScript(config)

	// Validate external_id is NOT in the command when empty
	if strings.Contains(script, "--external-id \"\"") {
		t.Error("Script should not include empty external-id flag")
	}

	// But should still contain other elements
	if !strings.Contains(script, "aws sts assume-role") {
		t.Error("Script missing AWS STS assume-role command")
	}
	if !strings.Contains(script, "--duration-seconds 7200") {
		t.Error("Script has wrong duration")
	}
}

// Test assume role script with ambient/pod credentials (no base credentials)
func TestGenerateAssumeRoleScriptWithAmbientCredentials(t *testing.T) {
	config := &aws.AWSAuthConfig{
		Region: "us-east-1",
		AssumeRoleConfig: &aws.AssumeRoleConfig{
			RoleARN:         "arn:aws:iam::123456789012:role/my-role",
			SessionName:     "test-session",
			ExternalID:      "external-123",
			Duration:        3600,
			BaseCredentials: nil, // No base credentials - using ambient/pod credentials
		},
	}

	script := generateAssumeRoleScript(config)

	// Should NOT hardcode static credentials (only temp creds from STS response)
	if strings.Contains(script, "aws_access_key_id = AKIA") && !strings.Contains(script, "$AWS_ACCESS_KEY_ID") {
		t.Error("Script should not hardcode static credentials when using ambient auth")
	}

	// Should still call assume-role
	if !strings.Contains(script, "aws sts assume-role") {
		t.Error("Script missing AWS STS assume-role command")
	}

	// Should have role ARN
	if !strings.Contains(script, "arn:aws:iam::123456789012:role/my-role") {
		t.Error("Script missing role ARN")
	}

	// Should have external ID
	if !strings.Contains(script, "--external-id \"external-123\"") {
		t.Error("Script missing external ID")
	}

	// Should create config file with region only
	if !strings.Contains(script, "region = us-east-1") {
		t.Error("Script missing region in config")
	}

	// Note: AWS env vars are injected in buildAWSTask, not in the script
}

// Test script generation returns empty for nil configs
func TestGenerateScriptWithNilConfig(t *testing.T) {
	inlineScript := generateInlineCredentialsScript(&aws.AWSAuthConfig{})
	if inlineScript != "" {
		t.Error("Expected empty script for nil InlineCredentials")
	}

	assumeRoleScript := generateAssumeRoleScript(&aws.AWSAuthConfig{})
	if assumeRoleScript != "" {
		t.Error("Expected empty script for nil AssumeRoleConfig")
	}
}
