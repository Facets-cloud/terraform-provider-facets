package tekton

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

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

	// Custom labels are sanitized too: they are user-supplied, and one invalid
	// value rejects the whole object.
	for k, v := range m.CustomLabels {
		labels[k] = sanitizeLabelValue(v)
	}

	// EVERY generated value is sanitized, not just display_name. resource_name,
	// resource_kind and environment_unique_name all originate from blueprint data
	// and are only conventionally label-safe -- a single invalid character in any
	// one of them fails the entire apply, exactly as display_name did.
	labels["display_name"] = sanitizeLabelValue(m.DisplayName)
	labels["resource_name"] = sanitizeLabelValue(m.ResourceName)
	labels["resource_kind"] = sanitizeLabelValue(m.ResourceKind)
	labels["environment_unique_name"] = sanitizeLabelValue(m.EnvUniqueName)
	labels["cluster_id"] = sanitizeLabelValue(m.ClusterID)
	labels["cloud_action"] = formatBool(m.IsCloudAction)

	return labels
}

// DisplayNameAnnotation carries the RAW display name, which the label cannot.
//
// Sanitizing display_name made the label lossy ("Stop Database" -> Stop-Database),
// and ImportState reconstructs `name` from it -- so import produced a spurious
// "Stop-Database" -> "Stop Database" diff on the first plan. Annotations have no
// charset restriction, so the exact value round-trips while the label stays valid.
const DisplayNameAnnotation = "facets.cloud/display-name"

// Annotations returns metadata that must survive verbatim and so cannot live in a
// label.
func (m *ResourceMetadata) Annotations() map[string]string {
	return map[string]string{
		DisplayNameAnnotation: m.DisplayName,
	}
}

// AnnotationsAsInterface returns Annotations for unstructured objects.
func (m *ResourceMetadata) AnnotationsAsInterface() map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range m.Annotations() {
		out[k] = v
	}
	return out
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
// alphanumeric, and NEVER empty for a non-empty input.
//
// These values are human-facing strings -- "Stop Database", "Restart & Verify" --
// and Kubernetes rejects the space, so passing them through unmodified made the
// whole apply fail with `metadata.labels: Invalid value`. Names are chosen by
// whoever writes the module, so the provider cannot assume they are label-safe.
//
// The awkward case is a value with NO label-safe characters at all: CJK, Cyrillic,
// emoji, or pure punctuation. Stripping those yields "" -- a technically valid
// label, but it collapses distinct actions onto one indistinguishable value, so
// "データベース停止" and "データベース開始" became the same label. When that happens,
// fall back to a short content hash so the label stays unique and stable. The exact
// original is preserved in the facets.cloud/display-name annotation, which has no
// charset restriction.
func sanitizeLabelValue(v string) string {
	if v == "" {
		return ""
	}

	out := labelInvalidChars.ReplaceAllString(v, "-")

	// Collapse runs of separators so "Stop  &  Start" does not become "Stop---Start".
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}

	out = truncateLabel(out)

	// Must begin and end alphanumeric.
	out = strings.Trim(out, "-._")
	out = truncateLabel(out)

	// Nothing label-safe survived. Derive a deterministic, unique value from the
	// original rather than returning "" and colliding with every other such name.
	if out == "" {
		sum := sha256.Sum256([]byte(v))
		return "x-" + hex.EncodeToString(sum[:])[:16]
	}
	return out
}

// truncateLabel cuts to the 63-character limit on a RUNE boundary. Slicing bytes
// can split a multi-byte character and leave an invalid trailing fragment.
func truncateLabel(v string) string {
	if len(v) <= 63 {
		return v
	}
	trimmed := v[:63]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
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
