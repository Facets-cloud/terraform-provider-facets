package tekton

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton/testfake"
)

const (
	secretNS   = "tekton-pipelines"
	secretName = "facets-azure-creds-14a6527d65db732e"
	secretKey  = "client_secret"
)

func identity() map[string]string {
	return map[string]string{
		"tenant-id":       "tenant-1",
		"client-id":       "client-1",
		"subscription-id": "sub-1",
	}
}

// The provider creates the Secret so that nobody has to be told its name. If
// this regresses, every action fails at run time with CreateContainerConfigError
// and the only fix requires access to a namespace the customer cannot see.
func TestReconcileCredentialsSecret_CreatesWhenAbsent(t *testing.T) {
	c := testfake.NewClient()
	ops := NewResourceOperations(c)

	if err := ops.ReconcileCredentialsSecret(
		context.Background(), secretNS, secretName, secretKey, "s3cr3t", identity(),
	); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := c.Resource(testfake.SecretGVR).Namespace(secretNS).
		Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret was not created: %v", err)
	}

	sd, _, _ := unstructuredString(got.Object, "stringData", secretKey)
	if sd != "s3cr3t" {
		t.Errorf("stored value = %q, want %q", sd, "s3cr3t")
	}

	// Traceability: the name is a hash, so the identity must be recoverable by
	// inspection or an operator cannot tell which SP a Secret belongs to.
	ann := got.GetAnnotations()
	for k, want := range map[string]string{
		"facets.cloud/tenant-id":       "tenant-1",
		"facets.cloud/client-id":       "client-1",
		"facets.cloud/subscription-id": "sub-1",
	} {
		if ann[k] != want {
			t.Errorf("annotation %s = %q, want %q", k, ann[k], want)
		}
	}
	if got.GetLabels()["app.kubernetes.io/managed-by"] != "terraform-provider-facets" {
		t.Error("Secret must be labelled provider-managed")
	}
}

// Two actions sharing one service principal derive the same name. The second
// apply must adopt, not fail -- otherwise adding a second action to a project
// breaks the first.
func TestReconcileCredentialsSecret_IdempotentAcrossActions(t *testing.T) {
	c := testfake.NewClient()
	ops := NewResourceOperations(c)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := ops.ReconcileCredentialsSecret(ctx, secretNS, secretName, secretKey, "s3cr3t", identity()); err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
	}

	list, err := c.Resource(testfake.SecretGVR).Namespace(secretNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Errorf("got %d Secrets, want exactly 1 shared Secret", len(list.Items))
	}
}

// A rotated client secret must propagate: same derived name, new value, so every
// action on that service principal picks it up without being reconfigured.
func TestReconcileCredentialsSecret_RotationOverwritesValue(t *testing.T) {
	c := testfake.NewClient(testfake.Secret(secretNS, secretName, map[string]string{secretKey: "old-password"}))
	ops := NewResourceOperations(c)
	ctx := context.Background()

	if err := ops.ReconcileCredentialsSecret(ctx, secretNS, secretName, secretKey, "new-password", identity()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := c.Resource(testfake.SecretGVR).Namespace(secretNS).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v, _, _ := unstructuredString(got.Object, "stringData", secretKey)
	if v != "new-password" {
		t.Errorf("value = %q, want the rotated password", v)
	}
}

// A hand-created Secret at the derived name is adopted rather than rejected, so
// migrating to provider-managed credentials needs no manual deletion first.
func TestReconcileCredentialsSecret_AdoptsPreExisting(t *testing.T) {
	c := testfake.NewClient(testfake.Secret(secretNS, secretName, map[string]string{"some_other_key": "x"}))
	ops := NewResourceOperations(c)
	ctx := context.Background()

	if err := ops.ReconcileCredentialsSecret(ctx, secretNS, secretName, secretKey, "s3cr3t", identity()); err != nil {
		t.Fatalf("adoption failed: %v", err)
	}
	got, _ := c.Resource(testfake.SecretGVR).Namespace(secretNS).Get(ctx, secretName, metav1.GetOptions{})
	if v, _, _ := unstructuredString(got.Object, "stringData", secretKey); v != "s3cr3t" {
		t.Errorf("adopted Secret must carry the expected key; got %q", v)
	}
}

// A lost race for the same derived name is success, not an error: identical
// identity means identical credential.
func TestReconcileCredentialsSecret_AlreadyExistsRaceIsNotAnError(t *testing.T) {
	c := testfake.NewClient()
	testfake.WithError(c, "create", testfake.SecretGVR, testfake.ErrAlreadyExists(testfake.SecretGVR, secretName))
	ops := NewResourceOperations(c)

	if err := ops.ReconcileCredentialsSecret(
		context.Background(), secretNS, secretName, secretKey, "s3cr3t", identity(),
	); err != nil {
		t.Errorf("a lost create race must not fail the apply: %v", err)
	}
}

// Missing RBAC must produce a message naming the namespace and the permission
// needed, since the operator hitting this has no other signal.
func TestReconcileCredentialsSecret_ForbiddenCreateIsActionable(t *testing.T) {
	c := testfake.NewClient()
	testfake.WithError(c, "create", testfake.SecretGVR, testfake.ErrForbidden(testfake.SecretGVR, secretName))
	ops := NewResourceOperations(c)

	err := ops.ReconcileCredentialsSecret(
		context.Background(), secretNS, secretName, secretKey, "s3cr3t", identity(),
	)
	if err == nil {
		t.Fatal("a forbidden create must fail the apply")
	}
	for _, want := range []string{secretNS, secretName, "secrets"} {
		if !contains(err.Error(), want) {
			t.Errorf("message must mention %q; got: %v", want, err)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func unstructuredString(obj map[string]interface{}, fields ...string) (string, bool, error) {
	cur := obj
	for i, f := range fields {
		v, ok := cur[f]
		if !ok {
			return "", false, nil
		}
		if i == len(fields)-1 {
			s, _ := v.(string)
			return s, true, nil
		}
		cur, ok = v.(map[string]interface{})
		if !ok {
			return "", false, nil
		}
	}
	return "", false, nil
}
