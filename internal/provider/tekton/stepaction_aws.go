package tekton

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/facets-cloud/terraform-provider-facets/internal/aws"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BuildAWSStepAction creates a StepAction for AWS credential setup using IRSA
// This StepAction configures AWS credentials using IRSA (pod's IAM role) to assume a target role
func BuildAWSStepAction(stepActionName, namespace string, labels map[string]interface{}, awsConfig *aws.AWSAuthConfig) (*unstructured.Unstructured, error) {
	// Generate script using IRSA + source_profile for role assumption
	script := GenerateAssumeRoleScript(awsConfig)

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tekton.dev/v1beta1",
			"kind":       "StepAction",
			"metadata": map[string]interface{}{
				"name":      stepActionName,
				"namespace": namespace,
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"image":  "facetscloud/actions-base-image:v1.1.0",
				"script": script,
				// No params needed - AWS SDK uses IRSA from pod automatically
				// No env vars needed - IRSA injected by EKS webhook
			},
		},
	}, nil
}

// GenerateAssumeRoleScript creates an AWS config file with source_profile
// Uses IRSA (pod's IAM role) via source_profile to automatically assume the target role
// The AWS SDK handles the role assumption automatically - no manual STS calls needed
func GenerateAssumeRoleScript(config *aws.AWSAuthConfig) string {
	if config.AssumeRoleConfig == nil {
		return ""
	}

	assumeRole := config.AssumeRoleConfig

	// Generate session name if not provided
	sessionName := assumeRole.SessionName
	if sessionName == "" {
		sessionName = generateRandomSessionName()
	}

	// Build the config file with source_profile for role chaining
	// The parent-cp-account profile uses IRSA (web identity token from pod)
	// The default profile uses source_profile to chain to the target role
	script := `#!/bin/bash
set -e

mkdir -p /workspace/.aws

PARENT_ROLE_ARN="${AWS_ROLE_ARN}"
if [ -z "$PARENT_ROLE_ARN" ]; then
    echo "ERROR: AWS_ROLE_ARN environment variable not set. IRSA may not be configured." >&2
    exit 1
fi

cat > /workspace/.aws/config <<EOFCONFIG
[profile irsa]
web_identity_token_file = /var/run/secrets/eks.amazonaws.com/serviceaccount/token
role_arn = ${PARENT_ROLE_ARN}

[default]
source_profile = irsa
role_arn = %s
role_session_name = %s
region = %s
`

	// Add optional external_id if provided
	if assumeRole.ExternalID != "" {
		script += fmt.Sprintf("external_id = %s\n", assumeRole.ExternalID)
	}

	script += `EOFCONFIG

chmod 600 /workspace/.aws/config
`

	return fmt.Sprintf(script,
		assumeRole.RoleARN, // For [default] role_arn (target role)
		sessionName,        // For [default] role_session_name
		config.Region,      // For [default] region
	)
}

// generateRandomSessionName creates a random session name using crypto/rand
// Returns a string in format "terraform-XXXXXXXX" where X is a random hex character
func generateRandomSessionName() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to a simpler approach if crypto/rand fails
		return fmt.Sprintf("terraform-session-%d", os.Getpid())
	}
	return fmt.Sprintf("terraform-%s", hex.EncodeToString(bytes))
}
