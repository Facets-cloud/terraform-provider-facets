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

// Test script generation for IRSA with source_profile
func TestGenerateAssumeRoleScriptWithSourceProfile(t *testing.T) {
	config := &aws.AWSAuthConfig{
		Region: "us-west-2",
		AssumeRoleConfig: &aws.AssumeRoleConfig{
			RoleARN:    "arn:aws:iam::123456789012:role/target-role",
			ExternalID: "my-external-id",
		},
	}

	script := generateAssumeRoleScript(config)

	// Validate script contains expected elements for source_profile approach
	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script missing shebang")
	}
	if !strings.Contains(script, "set -e") {
		t.Error("Script missing error handling")
	}
	if !strings.Contains(script, "mkdir -p /workspace/.aws") {
		t.Error("Script missing directory creation")
	}
	if !strings.Contains(script, "PARENT_ROLE_ARN=\"${AWS_ROLE_ARN}\"") {
		t.Error("Script missing PARENT_ROLE_ARN environment variable extraction")
	}
	if !strings.Contains(script, "[profile irsa]") {
		t.Error("Script missing IRSA profile")
	}
	if !strings.Contains(script, "web_identity_token_file = /var/run/secrets/eks.amazonaws.com/serviceaccount/token") {
		t.Error("Script missing IRSA token file path")
	}
	if !strings.Contains(script, "role_arn = ${PARENT_ROLE_ARN}") {
		t.Error("Script missing parent role ARN variable in IRSA profile")
	}
	if !strings.Contains(script, "[default]") {
		t.Error("Script missing default profile")
	}
	if !strings.Contains(script, "source_profile = irsa") {
		t.Error("Script missing source_profile for role chaining")
	}
	if !strings.Contains(script, "role_arn = arn:aws:iam::123456789012:role/target-role") {
		t.Error("Script missing target role ARN")
	}
	if !strings.Contains(script, "region = us-west-2") {
		t.Error("Script missing region")
	}
	if !strings.Contains(script, "external_id = my-external-id") {
		t.Error("Script missing external ID")
	}
	if !strings.Contains(script, "role_session_name = ") {
		t.Error("Script missing role_session_name")
	}
	if !strings.Contains(script, "chmod 600") {
		t.Error("Script missing permissions setting")
	}
	// Should NOT contain manual AWS STS assume-role call, jq, or debug output
	if strings.Contains(script, "aws sts assume-role") {
		t.Error("Script should not contain manual STS assume-role (AWS SDK handles it)")
	}
	if strings.Contains(script, "aws sts get-caller-identity") {
		t.Error("Script should not contain test commands")
	}
	if strings.Contains(script, "jq") {
		t.Error("Script should not use jq (AWS SDK handles credential extraction)")
	}
	if strings.Contains(script, "cat /workspace/.aws/config") {
		t.Error("Script should not contain debug output")
	}
}

// Test assume role script without external ID
func TestGenerateAssumeRoleScriptWithoutExternalID(t *testing.T) {
	config := &aws.AWSAuthConfig{
		Region: "us-east-1",
		AssumeRoleConfig: &aws.AssumeRoleConfig{
			RoleARN:    "arn:aws:iam::123456789012:role/my-role",
			ExternalID: "", // No external ID
		},
	}

	script := generateAssumeRoleScript(config)

	// Should have role ARN
	if !strings.Contains(script, "arn:aws:iam::123456789012:role/my-role") {
		t.Error("Script missing role ARN")
	}

	// Should have region
	if !strings.Contains(script, "region = us-east-1") {
		t.Error("Script missing region")
	}

	// Should have source_profile
	if !strings.Contains(script, "source_profile = irsa") {
		t.Error("Script missing source_profile")
	}

	// Validate external_id is NOT in the config when empty
	if strings.Contains(script, "external_id =") {
		t.Error("Script should not include external_id field when it's empty")
	}
}

// Test script with explicit session name
func TestGenerateAssumeRoleScriptWithSessionName(t *testing.T) {
	config := &aws.AWSAuthConfig{
		Region: "us-east-1",
		AssumeRoleConfig: &aws.AssumeRoleConfig{
			RoleARN:     "arn:aws:iam::123456789012:role/my-role",
			ExternalID:  "test-external-id",
			SessionName: "my-custom-session",
		},
	}

	script := generateAssumeRoleScript(config)

	// Should have the explicit session name
	if !strings.Contains(script, "role_session_name = my-custom-session") {
		t.Error("Script missing explicit session name")
	}
}

// Test script generation returns empty for nil AssumeRoleConfig
func TestGenerateScriptWithNilConfig(t *testing.T) {
	assumeRoleScript := generateAssumeRoleScript(&aws.AWSAuthConfig{})
	if assumeRoleScript != "" {
		t.Error("Expected empty script for nil AssumeRoleConfig")
	}
}
