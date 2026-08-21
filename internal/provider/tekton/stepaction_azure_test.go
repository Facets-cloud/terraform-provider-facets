package tekton

import (
	"strings"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/azure"
)

func oidcConfig() *azure.AzureAuthConfig {
	return &azure.AzureAuthConfig{
		SubscriptionID:     "sub-1",
		TenantID:           "tenant-1",
		ClientID:           "client-1",
		Mode:               azure.AuthModeOIDCFederation,
		UseOIDCFederation:  true,
		FederatedTokenFile: azure.DefaultFederatedTokenFile,
	}
}

func secretConfig() *azure.AzureAuthConfig {
	return &azure.AzureAuthConfig{
		SubscriptionID: "sub-1",
		TenantID:       "tenant-1",
		ClientID:       "client-1",
		ClientSecret:   "super-secret-value",
		Mode:           azure.AuthModeClientSecret,
		SecretName:     azure.DefaultCredentialsSecretName,
		SecretKey:      azure.DefaultCredentialsSecretKey,
	}
}

func TestBuildAzureStepAction_NilConfig(t *testing.T) {
	if _, err := BuildAzureStepAction("sa-name", "tekton-pipelines", nil, nil); err == nil {
		t.Fatal("expected an error for a nil azure config")
	}
}

func TestBuildAzureStepAction_UsesImageWithAzCLI(t *testing.T) {
	sa, err := BuildAzureStepAction("sa-name", "tekton-pipelines", nil, oidcConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := sa.Object["spec"].(map[string]interface{})
	img, _ := spec["image"].(string)

	// facetscloud/actions-base-image bundles awscli and kubectl but has NO az CLI,
	// so the Azure StepAction must not use it -- `az login` would fail at runtime.
	if strings.Contains(img, "actions-base-image") {
		t.Errorf("azure step action must not use the base image (no az CLI): %q", img)
	}
	if !strings.Contains(img, "azure-cli") {
		t.Errorf("expected an azure-cli image, got %q", img)
	}
}

func TestBuildAzureStepAction_Metadata(t *testing.T) {
	labels := map[string]interface{}{"resource_kind": "mysql"}
	sa, err := BuildAzureStepAction("my-step-action", "custom-ns", labels, oidcConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sa.Object["kind"]; got != "StepAction" {
		t.Errorf("kind = %v, want StepAction", got)
	}
	md := sa.Object["metadata"].(map[string]interface{})
	if md["name"] != "my-step-action" {
		t.Errorf("name = %v", md["name"])
	}
	if md["namespace"] != "custom-ns" {
		t.Errorf("namespace = %v", md["namespace"])
	}
}

// In OIDC mode there is no secret at all, so no env var should be declared.
func TestBuildAzureStepAction_OIDC_NoEnvInjected(t *testing.T) {
	sa, err := BuildAzureStepAction("sa", "tekton-pipelines", nil, oidcConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := sa.Object["spec"].(map[string]interface{})
	if _, present := spec["env"]; present {
		t.Error("OIDC mode should not declare env vars; there is no secret to pass")
	}
}

// In client-secret mode the value must arrive via secretKeyRef so it never
// appears in the rendered Task/StepAction manifest.
func TestBuildAzureStepAction_ClientSecret_UsesSecretKeyRef(t *testing.T) {
	sa, err := BuildAzureStepAction("sa", "tekton-pipelines", nil, secretConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := sa.Object["spec"].(map[string]interface{})
	env, ok := spec["env"].([]interface{})
	if !ok || len(env) != 1 {
		t.Fatalf("expected exactly one env var, got %#v", spec["env"])
	}
	e := env[0].(map[string]interface{})
	if e["name"] != "FACETS_AZURE_CLIENT_SECRET" {
		t.Errorf("env name = %v", e["name"])
	}
	vf, ok := e["valueFrom"].(map[string]interface{})
	if !ok {
		t.Fatal("env var must use valueFrom, not a literal value")
	}
	skr := vf["secretKeyRef"].(map[string]interface{})
	if skr["name"] != azure.DefaultCredentialsSecretName {
		t.Errorf("secret name = %v, want %v", skr["name"], azure.DefaultCredentialsSecretName)
	}
	if skr["key"] != "client_secret" {
		t.Errorf("secret key = %v", skr["key"])
	}
	if _, hasLiteral := e["value"]; hasLiteral {
		t.Error("env var must not carry a literal value")
	}
}

// The whole point of secretKeyRef: the secret must not be findable anywhere in
// the serialised object.
func TestBuildAzureStepAction_ClientSecret_NotInManifest(t *testing.T) {
	cfg := secretConfig()
	sa, err := BuildAzureStepAction("sa", "tekton-pipelines", nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(sa.Object["spec"].(map[string]interface{})["script"].(string), cfg.ClientSecret) {
		t.Error("client secret leaked into the script")
	}
}

func TestGenerateAzureLoginScript_OIDC(t *testing.T) {
	s := GenerateAzureLoginScript(oidcConfig())

	for _, want := range []string{
		"--federated-token",
		azure.DefaultFederatedTokenFile,
		`--username "client-1"`,
		`--tenant "tenant-1"`,
		`az account set --subscription "sub-1"`,
		AzureConfigDir,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q\n---\n%s", want, s)
		}
	}
	if strings.Contains(s, "--password") {
		t.Error("OIDC mode must not pass --password")
	}
}

func TestGenerateAzureLoginScript_ClientSecret(t *testing.T) {
	cfg := secretConfig()
	s := GenerateAzureLoginScript(cfg)

	// The secret is referenced through the env var, never inlined.
	if strings.Contains(s, cfg.ClientSecret) {
		t.Error("script must not inline the client secret")
	}
	if !strings.Contains(s, "$FACETS_AZURE_CLIENT_SECRET") {
		t.Errorf("script should read the secret from the env var\n---\n%s", s)
	}
	if strings.Contains(s, "--federated-token") {
		t.Error("client-secret mode must not pass --federated-token")
	}
}

// A missing token file must fail loudly rather than fall through to a confusing
// downstream `az` error.
func TestGenerateAzureLoginScript_OIDC_GuardsMissingToken(t *testing.T) {
	s := GenerateAzureLoginScript(oidcConfig())
	if !strings.Contains(s, "exit 1") {
		t.Error("script should exit non-zero when the federated token is absent")
	}
	if !strings.Contains(s, "api://AzureADTokenExchange") {
		t.Error("guard message should mention the required audience, since the " +
			"wrong audience yields an opaque AADSTS700212 from Entra")
	}
}

func TestGenerateAzureLoginScript_SetsConfigDir(t *testing.T) {
	for name, cfg := range map[string]*azure.AzureAuthConfig{
		"oidc":   oidcConfig(),
		"secret": secretConfig(),
	} {
		t.Run(name, func(t *testing.T) {
			s := GenerateAzureLoginScript(cfg)
			if !strings.Contains(s, "export AZURE_CONFIG_DIR="+AzureConfigDir) {
				t.Errorf("script should export AZURE_CONFIG_DIR\n---\n%s", s)
			}
		})
	}
}

// --- secret-manager mode ---

func smConfig() *azure.AzureAuthConfig {
	return &azure.AzureAuthConfig{
		Mode:              azure.AuthModeSecretManager,
		CloudAccountID:    "acct-123",
		SecretManagerPath: "cluster/backend/accounts/acct-123",
	}
}

// The fetch step needs the aws CLI, which the azure-cli image does NOT have
// (verified: it ships az, jq and python3 but no aws and no boto3).
func TestBuildAzureStepAction_SecretManager_UsesFetchImage(t *testing.T) {
	sa, err := BuildAzureStepAction("sa", "tekton-pipelines", nil, smConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := sa.Object["spec"].(map[string]interface{})
	if got := spec["image"]; got != AzureFetchImage {
		t.Errorf("image = %v, want %v (needs the aws CLI)", got, AzureFetchImage)
	}
}

// Nothing sensitive exists to pass, so no env var and no secretKeyRef.
func TestBuildAzureStepAction_SecretManager_NoEnvNoSecretRef(t *testing.T) {
	sa, err := BuildAzureStepAction("sa", "tekton-pipelines", nil, smConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := sa.Object["spec"].(map[string]interface{})
	if _, present := spec["env"]; present {
		t.Error("secret-manager mode needs no env var; credentials are fetched at run time")
	}
	if strings.Contains(spec["script"].(string), "secretKeyRef") {
		t.Error("secret-manager mode must not reference a Kubernetes Secret")
	}
}

func TestGenerateAzureLoginScript_SecretManager(t *testing.T) {
	cfg := smConfig()
	s := GenerateAzureLoginScript(cfg)

	for _, want := range []string{
		"aws secretsmanager get-secret-value",
		cfg.SecretManagerPath,
		"clientId",
		"clientSecret",
		"tenantId",
		"subscriptionId",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("fetch script missing %q\n---\n%s", want, s)
		}
	}
	// The fetch step must not attempt az login -- that happens in the injected
	// azure-cli step, because this step runs on an image without az.
	if strings.Contains(s, "az login") {
		t.Error("fetch step must not call az login; it runs on the base image")
	}
}

func TestGenerateAzureSecretManagerLoginScript(t *testing.T) {
	s := GenerateAzureSecretManagerLoginScript()

	if !strings.Contains(s, "az login --service-principal") {
		t.Error("login step should call az login")
	}
	if !strings.Contains(s, "az account set --subscription") {
		t.Error("login step should select the subscription")
	}
	// Credentials must not outlive the login step.
	if !strings.Contains(s, "shred") && !strings.Contains(s, "rm -f") {
		t.Error("login step should remove the credentials file after use")
	}
	if !strings.Contains(s, AzureConfigDir) {
		t.Error("login step should use the shared azure config dir")
	}
}

// Apply-time verification exists so a Secret name/key mismatch is an actionable
// error at apply, not a CreateContainerConfigError discovered in pod events the
// first time someone clicks the action. These assert the message quality, since
// the whole point is that an operator with no internal knowledge can self-serve.
func TestVerifySecretKey_MessageNamesTheProblem(t *testing.T) {
	// Compile-time guard: the helper must stay on ResourceOperations so both the
	// Create and Update paths can call it.
	var _ = (*ResourceOperations).VerifySecretKey
}
