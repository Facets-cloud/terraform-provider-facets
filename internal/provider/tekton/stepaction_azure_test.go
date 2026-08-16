package tekton

import (
	"strings"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/azure"
)

func TestBuildAzureStepAction_WorkloadIdentity(t *testing.T) {
	cfg := &azure.AzureAuthConfig{
		SubscriptionID:      "sub-1",
		TenantID:            "tenant-1",
		ClientID:            "client-1",
		Environment:         "AzureCloud",
		UseWorkloadIdentity: true,
	}

	sa, err := BuildAzureStepAction("setup-credentials-abc", "tekton-pipelines", map[string]interface{}{"cloud_action": "true"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spec := sa.Object["spec"].(map[string]interface{})
	script := spec["script"].(string)

	if !strings.Contains(script, "--federated-token") {
		t.Error("workload identity login must use --federated-token")
	}
	if strings.Contains(script, "--password") {
		t.Error("workload identity login must not reference a password")
	}

	// No secret reference at all on the workload-identity path.
	for _, e := range spec["env"].([]interface{}) {
		env := e.(map[string]interface{})
		if _, ok := env["valueFrom"]; ok {
			t.Errorf("unexpected valueFrom on env %v under workload identity", env["name"])
		}
		if env["name"] == "AZURE_CLIENT_SECRET" {
			t.Error("AZURE_CLIENT_SECRET must not be set under workload identity")
		}
	}
}

func TestBuildAzureStepAction_ClientSecretRefUsesSecretKeyRef(t *testing.T) {
	cfg := &azure.AzureAuthConfig{
		SubscriptionID: "sub-1",
		TenantID:       "tenant-1",
		ClientID:       "client-1",
		Environment:    "AzureCloud",
		ClientSecretRef: &azure.ClientSecretRef{
			SecretName: "facets-azure-sp",
			SecretKey:  "client-secret",
		},
	}

	sa, err := BuildAzureStepAction("setup-credentials-abc", "tekton-pipelines", nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spec := sa.Object["spec"].(map[string]interface{})

	// The whole point of the secretKeyRef path: the password must never be
	// materialised into the StepAction object.
	var found bool
	for _, e := range spec["env"].([]interface{}) {
		env := e.(map[string]interface{})
		if env["name"] != "AZURE_CLIENT_SECRET" {
			continue
		}
		found = true
		if _, hasLiteral := env["value"]; hasLiteral {
			t.Error("AZURE_CLIENT_SECRET must use valueFrom, never a literal value")
		}
		ref := env["valueFrom"].(map[string]interface{})["secretKeyRef"].(map[string]interface{})
		if ref["name"] != "facets-azure-sp" || ref["key"] != "client-secret" {
			t.Errorf("unexpected secretKeyRef: %v", ref)
		}
	}
	if !found {
		t.Fatal("expected an AZURE_CLIENT_SECRET env entry")
	}

	script := spec["script"].(string)
	if !strings.Contains(script, "--password") {
		t.Error("client secret login must pass --password")
	}
	if strings.Contains(script, "facets-azure-sp") {
		t.Error("secret name/value must not be interpolated into the script")
	}
}

func TestBuildAzureStepAction_NilConfig(t *testing.T) {
	if _, err := BuildAzureStepAction("n", "tekton-pipelines", nil, nil); err == nil {
		t.Fatal("expected an error for nil azure config")
	}
}
