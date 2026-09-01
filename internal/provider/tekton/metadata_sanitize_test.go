package tekton

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// The apply-breaking bug was never specific to display_name: every generated
// label value is a human-authored string from a blueprint.
func TestLabels_EveryGeneratedValueIsValid(t *testing.T) {
	m := NewResourceMetadata(
		"Stop Database",    // display name with a space
		"My Resource Name", // resource name with spaces
		"post gres",        // kind with a space
		"env one",          // environment with a space
		true,
		map[string]string{"bad label key": "has space"},
	)

	for k, v := range m.Labels() {
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			t.Errorf("label key %q is invalid: %v", k, errs)
		}
		if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
			t.Errorf("label %q value %q is invalid: %v", k, v, errs)
		}
	}
}

// A long resource name must not silently collapse two distinct resources onto
// one label.
func TestSanitizeLabelValue_LongValuesStayDistinct(t *testing.T) {
	prefix := "a-very-long-resource-name-that-exceeds-the-limit-comfortably"
	a := SanitizeLabelValue(prefix + "-one")
	b := SanitizeLabelValue(prefix + "-two")

	for _, v := range []string{a, b} {
		if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
			t.Errorf("%q invalid: %v", v, errs)
		}
	}
	if a == b {
		t.Errorf("distinct names collapsed to the same label: %q", a)
	}
}

// A name in a non-Latin script strips to nothing. Two such actions must still be
// distinguishable, or anything selecting on the label cannot tell them apart.
func TestSanitizeLabelValue_NonLatinStaysDistinct(t *testing.T) {
	a := SanitizeLabelValue("スタート")
	b := SanitizeLabelValue("ストップ")

	if a == "" || b == "" {
		t.Fatalf("expected a stable fallback, got %q and %q", a, b)
	}
	if a == b {
		t.Errorf("distinct names collapsed to %q", a)
	}
	for _, v := range []string{a, b} {
		if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
			t.Errorf("%q invalid: %v", v, errs)
		}
	}
}

func TestSanitizeLabelValue_IsDeterministic(t *testing.T) {
	for _, in := range []string{"Stop Database", "スタート", "", "ok-already"} {
		if SanitizeLabelValue(in) != SanitizeLabelValue(in) {
			t.Errorf("not deterministic for %q", in)
		}
	}
}

// Sanitization must be a no-op on anything Kubernetes already accepts.
//
// The _aws and _kubernetes resources share this function, so a value that works
// today must produce the identical label tomorrow. Rewriting one would force an
// in-place Task update and break lookups that match on display_name --
// "restart--db" collapsing to "restart-db" did exactly that before the guard.
func TestSanitizeLabelValue_NoOpOnAlreadyValidValues(t *testing.T) {
	for _, v := range []string{
		"start-database", "stop-database", "rollout-restart-application",
		"scale-up", "Scale_Down", "v1.2.3", "a", "restart--db", "a--b--c",
		"ends.with.dot0", "UPPER_and_lower.mixed-1",
	} {
		if got := SanitizeLabelValue(v); got != v {
			t.Errorf("already-valid %q was rewritten to %q", v, got)
		}
	}
}

// The same guarantee for keys, which the _aws and _kubernetes variants also emit.
func TestSanitizeLabelKey_NoOpOnAlreadyValidKeys(t *testing.T) {
	for _, k := range []string{
		"display_name", "resource_name", "cloud_action",
		"facets.cloud/release-type", "app.kubernetes.io/managed-by",
	} {
		if got := SanitizeLabelKey(k); got != k {
			t.Errorf("already-valid key %q was rewritten to %q", k, got)
		}
	}
}
