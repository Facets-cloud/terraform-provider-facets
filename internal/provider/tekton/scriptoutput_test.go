package tekton

import (
	"strings"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/azure"
)

// The setup step used to emit NOTHING on success: mkdir, the env guard, `az login
// --output none` and `az account set` are all silent, so a working step produced an
// empty log and looked broken. Whoever clicks the action has no other signal.
func TestGenerateAzureLoginScript_ReportsProgress(t *testing.T) {
	cfg := &azure.AzureAuthConfig{
		SubscriptionID: "sub-1", TenantID: "tenant-1", ClientID: "client-1",
		ClientSecret: "super-secret-value", Mode: azure.AuthModeClientSecret,
		SecretName: "n", SecretKey: "k",
	}
	s := GenerateAzureLoginScript(cfg)

	for _, want := range []string{
		"Authenticating to Azure",
		"Login succeeded.",
		"az account show",
		"later steps inherit it",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script must report progress; missing %q", want)
		}
	}

	// Progress output must never include the password itself.
	if strings.Contains(s, "super-secret-value") {
		t.Error("the client secret must never be inlined in the script")
	}
	// It must still reach the pod only via the env var.
	if !strings.Contains(s, `"$FACETS_AZURE_CLIENT_SECRET"`) {
		t.Error("password must come from the secretKeyRef env var")
	}
}
