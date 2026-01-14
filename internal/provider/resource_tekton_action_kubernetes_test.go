package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestGenerateResourceNames tests the name generation logic
func TestGenerateResourceNames(t *testing.T) {
	tests := []struct {
		name             string
		resourceName     string
		envName          string
		displayName      string
		expectedHashLen  int
		expectTruncation bool
	}{
		{
			name:            "short names",
			resourceName:    "app",
			envName:         "dev",
			displayName:     "test",
			expectedHashLen: 32, // MD5 hex = 32 chars
		},
		{
			name:         "long names",
			resourceName: "very-long-application-name-that-exceeds-kubernetes-limits",
			envName:      "production-environment-with-long-name",
			displayName:  "comprehensive-test-action-with-long-display-name",
			expectedHashLen: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskName, stepActionName := generateResourceNames(tt.resourceName, tt.envName, tt.displayName)

			// Task name should be the hash
			if len(taskName) != tt.expectedHashLen {
				t.Errorf("taskName length = %d, want %d", len(taskName), tt.expectedHashLen)
			}

			// StepAction name should be setup-credentials-{hash}
			expectedPrefix := "setup-credentials-"
			if !regexp.MustCompile(`^setup-credentials-[a-f0-9]{32}$`).MatchString(stepActionName) {
				t.Errorf("stepActionName = %s, want pattern %s{hash}", stepActionName, expectedPrefix)
			}

			// Verify Kubernetes name length limit (63 chars)
			if len(taskName) > 63 {
				t.Errorf("taskName too long: %d > 63", len(taskName))
			}
			if len(stepActionName) > 63 {
				t.Errorf("stepActionName too long: %d > 63", len(stepActionName))
			}

			// Verify deterministic - same inputs should produce same outputs
			taskName2, stepActionName2 := generateResourceNames(tt.resourceName, tt.envName, tt.displayName)
			if taskName != taskName2 {
				t.Errorf("taskName not deterministic: %s != %s", taskName, taskName2)
			}
			if stepActionName != stepActionName2 {
				t.Errorf("stepActionName not deterministic: %s != %s", stepActionName, stepActionName2)
			}
		})
	}
}

// TestBuildLabels tests label generation
func TestBuildLabels(t *testing.T) {
	tests := []struct {
		name              string
		displayName       string
		resourceName      string
		resourceKind      string
		envUniqueName     string
		clusterID         string
		customLabels      map[string]string
		expectedLabelKeys []string
	}{
		{
			name:          "all fields present without custom labels",
			displayName:   "my-action",
			resourceName:  "my-app",
			resourceKind:  "application",
			envUniqueName: "production",
			clusterID:     "cluster-01",
			customLabels:  nil,
			expectedLabelKeys: []string{
				"display_name",
				"resource_name",
				"resource_kind",
				"environment_unique_name",
				"cluster_id",
			},
		},
		{
			name:          "default cluster_id",
			displayName:   "test",
			resourceName:  "app",
			resourceKind:  "service",
			envUniqueName: "dev",
			clusterID:     "na",
			customLabels:  nil,
			expectedLabelKeys: []string{
				"display_name",
				"resource_name",
				"resource_kind",
				"environment_unique_name",
				"cluster_id",
			},
		},
		{
			name:          "with custom labels",
			displayName:   "my-action",
			resourceName:  "my-app",
			resourceKind:  "application",
			envUniqueName: "production",
			clusterID:     "cluster-01",
			customLabels: map[string]string{
				"team":        "platform",
				"cost-center": "engineering",
			},
			expectedLabelKeys: []string{
				"display_name",
				"resource_name",
				"resource_kind",
				"environment_unique_name",
				"cluster_id",
				"team",
				"cost-center",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := buildLabels(tt.displayName, tt.resourceName, tt.resourceKind, tt.envUniqueName, tt.clusterID, tt.customLabels)

			// Check all expected keys are present
			for _, key := range tt.expectedLabelKeys {
				if _, exists := labels[key]; !exists {
					t.Errorf("missing label key: %s", key)
				}
			}

			// Check auto-generated values
			if labels["display_name"] != tt.displayName {
				t.Errorf("display_name = %s, want %s", labels["display_name"], tt.displayName)
			}
			if labels["resource_name"] != tt.resourceName {
				t.Errorf("resource_name = %s, want %s", labels["resource_name"], tt.resourceName)
			}
			if labels["resource_kind"] != tt.resourceKind {
				t.Errorf("resource_kind = %s, want %s", labels["resource_kind"], tt.resourceKind)
			}
			if labels["environment_unique_name"] != tt.envUniqueName {
				t.Errorf("environment_unique_name = %s, want %s", labels["environment_unique_name"], tt.envUniqueName)
			}
			if labels["cluster_id"] != tt.clusterID {
				t.Errorf("cluster_id = %s, want %s", labels["cluster_id"], tt.clusterID)
			}

			// Check custom labels are present
			for k, v := range tt.customLabels {
				if labels[k] != v {
					t.Errorf("custom label %s = %v, want %s", k, labels[k], v)
				}
			}
		})
	}
}

// TestBuildLabelsAutoGeneratedPrecedence tests that auto-generated labels take precedence over custom labels
func TestBuildLabelsAutoGeneratedPrecedence(t *testing.T) {
	customLabels := map[string]string{
		"display_name":  "user-override-attempt",
		"resource_name": "user-override-attempt",
		"custom-label":  "custom-value",
	}

	labels := buildLabels("auto-display", "auto-resource", "auto-kind", "auto-env", "auto-cluster", customLabels)

	// Auto-generated labels should take precedence
	if labels["display_name"] != "auto-display" {
		t.Errorf("display_name should be auto-generated value, got %v", labels["display_name"])
	}
	if labels["resource_name"] != "auto-resource" {
		t.Errorf("resource_name should be auto-generated value, got %v", labels["resource_name"])
	}

	// Custom labels that don't conflict should still be present
	if labels["custom-label"] != "custom-value" {
		t.Errorf("custom-label should be present, got %v", labels["custom-label"])
	}
}

// TestBuildStepAction tests the buildStepAction function
func TestBuildStepAction(t *testing.T) {
	r := &TektonActionKubernetesResource{}

	plan := TektonActionKubernetesResourceModel{
		StepActionName: types.StringValue("setup-credentials-abc123"),
		Namespace:      types.StringValue("tekton-pipelines"),
	}

	labels := map[string]interface{}{
		"display_name":            "test-action",
		"resource_name":           "test-app",
		"resource_kind":           "application",
		"environment_unique_name": "dev",
		"cluster_id":              "test-cluster",
	}

	stepAction := r.buildStepAction(plan, labels)

	// Check basic structure
	if stepAction.GetAPIVersion() != "tekton.dev/v1beta1" {
		t.Errorf("apiVersion = %s, want tekton.dev/v1beta1", stepAction.GetAPIVersion())
	}

	if stepAction.GetKind() != "StepAction" {
		t.Errorf("kind = %s, want StepAction", stepAction.GetKind())
	}

	if stepAction.GetName() != "setup-credentials-abc123" {
		t.Errorf("name = %s, want setup-credentials-abc123", stepAction.GetName())
	}

	if stepAction.GetNamespace() != "tekton-pipelines" {
		t.Errorf("namespace = %s, want tekton-pipelines", stepAction.GetNamespace())
	}

	// Check labels are set
	stepLabels, found, _ := unstructured.NestedMap(stepAction.Object, "metadata", "labels")
	if !found {
		t.Fatal("labels not found in metadata")
	}

	if stepLabels["display_name"] != "test-action" {
		t.Errorf("label display_name = %v, want test-action", stepLabels["display_name"])
	}

	// Check spec contains required fields
	image, found, _ := unstructured.NestedString(stepAction.Object, "spec", "image")
	if !found || image == "" {
		t.Error("spec.image not found or empty")
	}

	script, found, _ := unstructured.NestedString(stepAction.Object, "spec", "script")
	if !found || script == "" {
		t.Error("spec.script not found or empty")
	}

	// Verify script contains credential setup logic
	if !regexp.MustCompile(`FACETS_USER_KUBECONFIG`).MatchString(script) {
		t.Error("script does not contain FACETS_USER_KUBECONFIG")
	}

	if !regexp.MustCompile(`base64 -d`).MatchString(script) {
		t.Error("script does not contain base64 decode")
	}
}

// TestExtractMetadataFromObject tests metadata extraction logic
func TestExtractMetadataFromObject(t *testing.T) {
	tests := []struct {
		name          string
		object        map[string]interface{}
		expectError   bool
		expectedNS    string
		expectedName  string
	}{
		{
			name: "valid metadata",
			object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"namespace": "default",
					"name":      "test-resource",
				},
			},
			expectError:  false,
			expectedNS:   "default",
			expectedName: "test-resource",
		},
		{
			name: "missing metadata",
			object: map[string]interface{}{
				"spec": map[string]interface{}{},
			},
			expectError: true,
		},
		{
			name: "metadata not a map",
			object: map[string]interface{}{
				"metadata": "invalid",
			},
			expectError: true,
		},
		{
			name: "missing namespace",
			object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test-resource",
				},
			},
			expectError: true,
		},
		{
			name: "empty namespace",
			object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"namespace": "",
					"name":      "test-resource",
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: tt.object}
			namespace, name, err := extractMetadata(obj)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if namespace != tt.expectedNS {
					t.Errorf("namespace = %s, want %s", namespace, tt.expectedNS)
				}
				if name != tt.expectedName {
					t.Errorf("name = %s, want %s", name, tt.expectedName)
				}
			}
		})
	}
}

// TestValidateNamespaceFormat tests namespace validation regex
func TestValidateNamespaceFormat(t *testing.T) {
	tests := []struct {
		namespace string
		valid     bool
	}{
		{"tekton-pipelines", true},
		{"default", true},
		{"my-namespace-123", true},
		{"a", true},
		{"123", true},
		{"a-b-c-d-e-f", true},
		{"UPPERCASE", false},      // uppercase not allowed
		{"-start-hyphen", false},  // cannot start with hyphen
		{"end-hyphen-", false},    // cannot end with hyphen
		{"under_score", false},    // underscores not allowed
		{"has space", false},      // spaces not allowed
		{"special@char", false},   // special chars not allowed
		{"", false},               // empty not allowed
	}

	// Kubernetes DNS-1123 label regex
	k8sNameRegex := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			isValid := k8sNameRegex.MatchString(tt.namespace) && len(tt.namespace) <= 63

			if isValid != tt.valid {
				t.Errorf("namespace %q: got valid=%v, want %v", tt.namespace, isValid, tt.valid)
			}
		})
	}
}

// TestValidateEnvVarName tests environment variable name validation
func TestValidateEnvVarName(t *testing.T) {
	tests := []struct {
		envVar string
		valid  bool
	}{
		{"LOG_LEVEL", true},
		{"NAMESPACE", true},
		{"MY_VAR_123", true},
		{"_PRIVATE", true},
		{"__DOUBLE", true},
		{"A", true},
		{"lowercase", false},    // lowercase not allowed
		{"123START", false},     // cannot start with number
		{"HAS-DASH", false},     // dashes not allowed
		{"HAS SPACE", false},    // spaces not allowed
		{"HAS.DOT", false},      // dots not allowed
		{"", false},             // empty not allowed
	}

	envVarRegex := regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

	for _, tt := range tests {
		t.Run(tt.envVar, func(t *testing.T) {
			isValid := envVarRegex.MatchString(tt.envVar)

			if isValid != tt.valid {
				t.Errorf("env var %q: got valid=%v, want %v", tt.envVar, isValid, tt.valid)
			}
		})
	}
}
