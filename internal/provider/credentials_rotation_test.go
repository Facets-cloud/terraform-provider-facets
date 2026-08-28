package provider

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Rotating a client secret changes only PROVIDER configuration. No resource
// attribute moves, so Terraform reports "No changes" and never calls Update --
// which meant the cluster kept serving the OLD password indefinitely while the
// operator believed it had been replaced. Convergence therefore has to happen in
// Read, which runs on every plan and refresh.
//
// This is a source-level guard rather than a behavioural test because exercising
// Read means standing up the full plugin-framework harness. The live proof is in
// PR #15: charlie-account-password -> charlie-THIRD-password-v3 on a bare
// `terraform plan`, with alpha and bravo untouched.
func TestRead_ReconcilesCredentialsSecret(t *testing.T) {
	src, err := os.ReadFile("resource_tekton_action_azure.go")
	if err != nil {
		t.Fatal(err)
	}

	readBody := funcBody(string(src), "func (r *TektonActionAzureResource) Read(")
	if readBody == "" {
		t.Fatal("could not locate the Read method")
	}

	if !strings.Contains(readBody, "ReconcileCredentialsSecret") {
		t.Error("Read must reconcile the credentials Secret, or a rotated client " +
			"secret never reaches the cluster (Terraform sees no attribute change)")
	}

	// Read must not fail a plan over this -- a transient Secret problem should
	// degrade to a warning, since Create/Update still hard-error where they can.
	if !strings.Contains(readBody, "AddWarning") {
		t.Error("Read must surface Secret failures as a warning, not an error")
	}
	if regexp.MustCompile(`AddError\([^)]*Secret`).MatchString(readBody) {
		t.Error("Read must not AddError on the Secret path; that would break plan")
	}
}

// funcBody returns the source of the function whose signature starts with prefix,
// delimited by brace depth.
func funcBody(src, prefix string) string {
	i := strings.Index(src, prefix)
	if i < 0 {
		return ""
	}
	depth := 0
	for j := i; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i : j+1]
			}
		}
	}
	return ""
}
