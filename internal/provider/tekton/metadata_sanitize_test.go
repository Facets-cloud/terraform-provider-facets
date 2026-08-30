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
