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
