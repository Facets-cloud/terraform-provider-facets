package tekton

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/azure"
)

// Go's %q is GO quoting, not SHELL quoting: it leaves $(...), backticks and $VAR
// live inside the double quotes it emits. These values come from a cloud_account
// spec, so a malicious or careless client_id executed arbitrary shell in the
// action pod.
//
// This runs the RENDERED SCRIPT through real bash with a stub `az` on PATH, so it
// tests what bash actually does rather than what the quoting looks like.
func TestGenerateAzureLoginScript_NoShellInjection(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "INJECTED")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real external binary: bash must expand argv to invoke it.
	if err := os.WriteFile(filepath.Join(bin, "az"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, payload := range []string{
		`x$(touch ` + marker + `)y`,
		"x`touch " + marker + "`y",
		`x"; touch ` + marker + `; echo "y`,
		`x$(printf %s ` + marker + `)y`,
	} {
		t.Run(payload[:6], func(t *testing.T) {
			os.Remove(marker)
			script := GenerateAzureLoginScript(&azure.AzureAuthConfig{
				SubscriptionID: "sub-1", TenantID: payload, ClientID: payload,
				ClientSecret: "pw", Mode: azure.AuthModeClientSecret,
				SecretName: "n", SecretKey: "k",
			})
			// Drop the /workspace mkdir so the script runs off-cluster.
			script = strings.ReplaceAll(script, AzureConfigDir, dir+"/azure")

			sh := filepath.Join(dir, "s.sh")
			if err := os.WriteFile(sh, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", sh)
			cmd.Env = append(os.Environ(),
				"PATH="+bin+":/usr/bin:/bin",
				"FACETS_AZURE_CLIENT_SECRET=pw")
			out, _ := cmd.CombinedOutput()

			if _, err := os.Stat(marker); err == nil {
				t.Errorf("SHELL INJECTION: payload %q executed\n--- script ---\n%s\n--- output ---\n%s",
					payload, script, out)
			}
		})
	}
}
