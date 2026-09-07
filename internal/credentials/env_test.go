package credentials

import (
	"strings"
	"testing"
)

func TestFromEnviron_StripsPrefixAndIgnoresOthers(t *testing.T) {
	got, err := fromEnviron([]string{
		EnvPrefix + "AWS_ACCESS_KEY_ID=AKIA123",
		EnvPrefix + "AWS_SECRET_ACCESS_KEY=shhh",
		"PATH=/usr/bin",
		"AWS_ACCESS_KEY_ID=not-prefixed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 credentials, got %d: %v", len(got), got)
	}
	if got["AWS_ACCESS_KEY_ID"] != "AKIA123" || got["AWS_SECRET_ACCESS_KEY"] != "shhh" {
		t.Errorf("wrong values: %v", got)
	}
}

// The provider must stay ignorant of clouds: whatever keys arrive are carried
// through, so one code path serves AWS, Azure and GCP.
func TestFromEnviron_IsCloudAgnostic(t *testing.T) {
	for name, env := range map[string][]string{
		"aws static": {EnvPrefix + "AWS_ACCESS_KEY_ID=a", EnvPrefix + "AWS_SESSION_TOKEN=b"},
		"azure sp":   {EnvPrefix + "AZURE_CLIENT_ID=a", EnvPrefix + "AZURE_CLIENT_SECRET=b", EnvPrefix + "AZURE_TENANT_ID=c"},
		"gcp sa":     {EnvPrefix + "GOOGLE_APPLICATION_CREDENTIALS=/creds.json"},
		"managed id": {EnvPrefix + "AZURE_USE_MSI=true"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := fromEnviron(env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(env) {
				t.Errorf("expected %d credentials, got %v", len(env), got)
			}
		})
	}
}

// Values legitimately contain '=' -- padded base64 and PEM blocks both do -- so
// only the first separator may be treated as one.
func TestFromEnviron_ValueMayContainEquals(t *testing.T) {
	got, _ := fromEnviron([]string{EnvPrefix + "TOKEN=abc==def="})
	if got["TOKEN"] != "abc==def=" {
		t.Errorf("value truncated at '=': %q", got["TOKEN"])
	}
}

// Kubernetes injects Secret keys verbatim as variable names and silently skips
// any that are not C identifiers, reporting it only as a pod event. Failing at
// apply is far better than an action that starts with a missing credential.
func TestFromEnviron_RejectsNonCIdentifierKeys(t *testing.T) {
	for _, bad := range []string{"AWS-ACCESS-KEY", "1STKEY", "has.dot", "has space"} {
		t.Run(bad, func(t *testing.T) {
			_, err := fromEnviron([]string{EnvPrefix + bad + "=v"})
			if err == nil {
				t.Fatalf("expected %q to be rejected", bad)
			}
			if !strings.Contains(err.Error(), bad) {
				t.Errorf("error should name the offending key, got: %v", err)
			}
		})
	}
}

// No credentials is a normal case: an action that only drives the cluster uses
// the pod's service account.
func TestFromEnviron_EmptyIsNotAnError(t *testing.T) {
	got, err := fromEnviron([]string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("expected an empty non-nil map, got %v", got)
	}
}

// A credential declared on a resource must fail the same way as one exported to
// the runner -- otherwise one of the two paths silently accepts a name
// Kubernetes will skip.
func TestValidateNames_MatchesEnvRules(t *testing.T) {
	if err := ValidateNames(map[string]string{"AZURE_CLIENT_ID": "a", "_OK": "b"}); err != nil {
		t.Errorf("valid names rejected: %v", err)
	}
	for _, bad := range []string{"AZURE-CLIENT-ID", "1ST", "has.dot", "has space"} {
		t.Run(bad, func(t *testing.T) {
			err := ValidateNames(map[string]string{bad: "v"})
			if err == nil {
				t.Fatalf("expected %q to be rejected", bad)
			}
			if !strings.Contains(err.Error(), bad) {
				t.Errorf("error should name the offending key, got: %v", err)
			}
		})
	}
	if err := ValidateNames(map[string]string{}); err != nil {
		t.Errorf("empty map should be fine: %v", err)
	}
}
