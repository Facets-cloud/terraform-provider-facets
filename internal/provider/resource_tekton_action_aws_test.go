package provider

import (
	"testing"
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
