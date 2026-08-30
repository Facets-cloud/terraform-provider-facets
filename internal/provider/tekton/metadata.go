package tekton

import (
	"crypto/sha256"
	"encoding/hex"
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

	// First, add custom labels (if any). Keys as well as values are sanitized:
	// a label key is a qualified name and a label value has its own charset, and
	// either one being invalid fails the whole apply, not just that label.
	for k, v := range m.CustomLabels {
		key := SanitizeLabelKey(k)
		if key == "" {
			continue
		}
		labels[key] = SanitizeLabelValue(v)
	}

	// Then, add auto-generated labels (these take precedence).
	//
	// Every value is sanitized, not just display_name. These are human-authored
	// strings from a blueprint -- an action called "Stop Database", a resource
	// named with a space, a 100-character resource name -- and Kubernetes rejects
	// all of them, failing the entire apply with `metadata.labels: Invalid value`.
	labels["display_name"] = SanitizeLabelValue(m.DisplayName)
	labels["resource_name"] = SanitizeLabelValue(m.ResourceName)
	labels["resource_kind"] = SanitizeLabelValue(m.ResourceKind)
	labels["environment_unique_name"] = SanitizeLabelValue(m.EnvUniqueName)
	labels["cluster_id"] = SanitizeLabelValue(m.ClusterID)
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

var (
	labelValueInvalid = regexp.MustCompile(`[^A-Za-z0-9._-]`)
	labelKeyInvalid   = regexp.MustCompile(`[^A-Za-z0-9._/-]`)
)

// SanitizeLabelValue coerces a string into a valid Kubernetes label value: at
// most 63 characters of [A-Za-z0-9._-], beginning and ending alphanumeric.
//
// The transformation is lossy, so the exact original is NOT recoverable from the
// label -- callers that need the original must keep it elsewhere. It is also not
// injective: a value made entirely of rejected characters collapses to a hash
// rather than to the empty string, because two differently-named actions both
// labelled "" would be indistinguishable to anything selecting on the label.
func SanitizeLabelValue(v string) string {
	if v == "" {
		return ""
	}

	out := labelValueInvalid.ReplaceAllString(v, "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-._")

	if out == "" {
		// Everything was stripped (a name in a non-Latin script, or pure
		// punctuation). Fall back to a stable digest so distinct inputs stay
		// distinguishable.
		return "x-" + shortHash(v)
	}

	if len(out) > 63 {
		// Truncation alone would collapse two long values sharing a prefix, so
		// keep a digest of the original in the surviving suffix.
		out = strings.Trim(out[:54], "-._") + "-" + shortHash(v)
	}
	return out
}

// SanitizeLabelKey coerces a string into a valid label key. Returns "" when
// nothing usable remains, which the caller treats as "drop this label".
func SanitizeLabelKey(k string) string {
	out := labelKeyInvalid.ReplaceAllString(k, "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-._/")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-._/")
	}
	return out
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}
