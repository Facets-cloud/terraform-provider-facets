package tekton

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ResourceMetadata contains the metadata for a Tekton resource
type ResourceMetadata struct {
	DisplayName   string
	ResourceName  string
	ResourceKind  string
	EnvUniqueName string
	ClusterID     string
	IsCloudAction bool
	CustomLabels  map[string]string
}

// NewResourceMetadata creates ResourceMetadata with cluster ID from environment
// customLabels are merged with auto-generated labels, with auto-generated taking precedence
func NewResourceMetadata(displayName, resourceName, resourceKind, envUniqueName string, isCloudAction bool, customLabels map[string]string) *ResourceMetadata {
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		clusterID = "na"
	}

	return &ResourceMetadata{
		DisplayName:   displayName,
		ResourceName:  resourceName,
		ResourceKind:  resourceKind,
		EnvUniqueName: envUniqueName,
		ClusterID:     clusterID,
		IsCloudAction: isCloudAction,
		CustomLabels:  customLabels,
	}
}

// Labels returns Kubernetes labels for this resource
// Custom labels are included, with auto-generated labels taking precedence
func (m *ResourceMetadata) Labels() map[string]string {
	labels := make(map[string]string)

	// First, add custom labels (if any)
	for k, v := range m.CustomLabels {
		labels[k] = v
	}

	// Then, add auto-generated labels (these take precedence)
	labels["display_name"] = sanitizeLabelValue(m.DisplayName)
	labels["resource_name"] = m.ResourceName
	labels["resource_kind"] = m.ResourceKind
	labels["environment_unique_name"] = m.EnvUniqueName
	labels["cluster_id"] = m.ClusterID
	labels["cloud_action"] = formatBool(m.IsCloudAction)

	return labels
}

// LabelsAsInterface returns labels as map[string]interface{} for unstructured objects
func (m *ResourceMetadata) LabelsAsInterface() map[string]interface{} {
	labels := m.Labels()
	result := make(map[string]interface{}, len(labels))
	for k, v := range labels {
		result[k] = v
	}
	return result
}

var labelInvalidChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// sanitizeLabelValue coerces an arbitrary string into a valid Kubernetes label
// value: at most 63 characters of [A-Za-z0-9._-], beginning and ending with an
// alphanumeric.
//
// display_name is a human-facing string -- "Stop Database", "Restart & Verify" --
// and Kubernetes rejects the space, so passing it through unmodified made the whole
// apply fail with `metadata.labels: Invalid value`. The action name is chosen by
// whoever writes the module, so the provider cannot assume it is label-safe.
//
// Labels are used to correlate Tekton objects back to a resource, so a lossy but
// deterministic transformation is fine here; the exact display name is preserved on
// the Task spec itself, not in this label.
func sanitizeLabelValue(v string) string {
	v = labelInvalidChars.ReplaceAllString(v, "-")

	// Collapse runs of separators so "Stop  &  Start" does not become "Stop---Start".
	for strings.Contains(v, "--") {
		v = strings.ReplaceAll(v, "--", "-")
	}

	if len(v) > 63 {
		v = v[:63]
	}

	// Must begin and end alphanumeric. Trimming can empty the string entirely (e.g.
	// a name of only punctuation), which is itself a valid label value.
	v = strings.Trim(v, "-._")
	if len(v) > 63 {
		v = v[:63]
	}
	return v
}

func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ExtractMetadata extracts namespace and name from an unstructured object
// Returns (namespace, name, error)
func ExtractMetadata(obj *unstructured.Unstructured) (string, string, error) {
	metadata, hasMetadata := obj.Object["metadata"]
	if !hasMetadata {
		return "", "", fmt.Errorf("no metadata key in object")
	}

	metadataMap, isMap := metadata.(map[string]interface{})
	if !isMap {
		return "", "", fmt.Errorf("metadata is not a map: %T", metadata)
	}

	namespace, hasNS := metadataMap["namespace"].(string)
	name, hasName := metadataMap["name"].(string)

	if !hasNS || !hasName || namespace == "" || name == "" {
		return "", "", fmt.Errorf("missing or empty namespace/name: hasNS=%v ns=%s, hasName=%v name=%s", hasNS, namespace, hasName, name)
	}

	return namespace, name, nil
}
