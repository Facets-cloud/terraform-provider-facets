package tekton

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// display_name carries a human-facing action name like "Stop Database". Kubernetes
// rejects spaces in label values, so an unsanitized name failed the entire apply
// with `metadata.labels: Invalid value`. Every name below is one a module author
// could plausibly write, and each must survive the real K8s validator -- not just
// this package's own regex.
func TestLabels_DisplayNameAlwaysValid(t *testing.T) {
	names := []string{
		"Stop Database",
		"Start Database",
		"Restart & Verify",
		"Scale Up/Down",
		"Take Snapshot (prod)",
		"stop-db",
		"  leading and trailing  ",
		"--dashes--",
		"...",
		"",
		"Ünïcödé Ãction",
		strings.Repeat("Very Long Action Name ", 10),
		"100% CPU check",
		"a",
	}

	for _, n := range names {
		m := NewResourceMetadata(n, "db-1", "postgres", "env-1", true, nil)
		got := m.Labels()["display_name"]

		if errs := validation.IsValidLabelValue(got); len(errs) > 0 {
			t.Errorf("display_name %q -> %q is not a valid label: %v", n, got, errs)
		}
	}
}

// Every auto-generated label value must be valid, not just display_name -- a single
// bad value fails the whole object.
func TestLabels_AllGeneratedValuesValid(t *testing.T) {
	m := NewResourceMetadata("Stop Database", "db-1", "postgres", "env-1", true, nil)
	for k, v := range m.Labels() {
		if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
			t.Errorf("label %s=%q invalid: %v", k, v, errs)
		}
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			t.Errorf("label key %q invalid: %v", k, errs)
		}
	}
}

// Distinct names should stay distinguishable -- the label is used to find the
// Tekton objects belonging to an action.
func TestLabels_DisplayNameRemainsMeaningful(t *testing.T) {
	stop := NewResourceMetadata("Stop Database", "db", "postgres", "e", true, nil).Labels()["display_name"]
	start := NewResourceMetadata("Start Database", "db", "postgres", "e", true, nil).Labels()["display_name"]

	if stop == start {
		t.Fatalf("Stop and Start collapsed to the same label %q", stop)
	}
	if !strings.Contains(stop, "Stop") {
		t.Errorf("sanitized label %q lost the original wording", stop)
	}
}

// Sanitizing display_name made the LABEL lossy, and ImportState reconstructs
// `name` from it -- so import produced a spurious "Stop-Database" -> "Stop
// Database" diff on the first plan, in all three action types. The raw name now
// travels in an annotation, which has no charset restriction.
func TestAnnotations_CarryRawDisplayNameForImport(t *testing.T) {
	for _, name := range []string{
		"Stop Database",
		"Restart & Verify",
		"Scale Up/Down",
		"Ünïcödé Ãction",
		"100% CPU check",
	} {
		m := NewResourceMetadata(name, "db-1", "postgres", "env-1", true, nil)

		if got := m.Annotations()[DisplayNameAnnotation]; got != name {
			t.Errorf("annotation must round-trip exactly: got %q, want %q", got, name)
		}
		// and the label is still valid, i.e. we did not simply stop sanitizing
		if errs := validation.IsValidLabelValue(m.Labels()["display_name"]); len(errs) > 0 {
			t.Errorf("label for %q is invalid: %v", name, errs)
		}
	}
}

// Only display_name was sanitized; resource_name, resource_kind,
// environment_unique_name and custom labels went through raw. Each of those comes
// from blueprint data and is only conventionally label-safe -- one bad character in
// any of them fails the whole apply.
func TestLabels_EveryValueIsSanitized(t *testing.T) {
	nasty := "has space/and&symbols"
	m := NewResourceMetadata(nasty, nasty, nasty, nasty, true,
		map[string]string{"custom": nasty})

	for k, v := range m.Labels() {
		if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
			t.Errorf("label %s=%q not sanitized: %v", k, v, errs)
		}
	}
}
