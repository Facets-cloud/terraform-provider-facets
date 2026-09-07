package tekton

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton/testfake"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// fetch reads a Secret's decoded data out of the fake cluster.
func fetch(t *testing.T, c dynamic.Interface, ns, name string) (*unstructured.Unstructured, map[string]string) {
	t.Helper()
	obj, err := c.Resource(testfake.SecretGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading %s/%s: %v", ns, name, err)
	}
	raw, _, _ := unstructured.NestedStringMap(obj.Object, "data")
	out := map[string]string{}
	for k, v := range raw {
		d, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			t.Fatalf("key %q is not valid base64: %v", k, err)
		}
		out[k] = string(d)
	}
	return obj, out
}

func TestReconcileCredentialsSecret_CreatesWhenAbsent(t *testing.T) {
	c := testfake.NewClient()
	ops := NewResourceOperations(c)

	changed, err := ops.ReconcileCredentialsSecret(context.Background(), "tekton-pipelines", "creds",
		map[string]string{"AWS_ACCESS_KEY_ID": "AKIA", "AWS_SECRET_ACCESS_KEY": "shhh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("creating a Secret should report changed=true")
	}

	obj, data := fetch(t, c, "tekton-pipelines", "creds")
	if data["AWS_ACCESS_KEY_ID"] != "AKIA" || data["AWS_SECRET_ACCESS_KEY"] != "shhh" {
		t.Errorf("credentials not stored: %v", data)
	}
	labels, _, _ := unstructured.NestedStringMap(obj.Object, "metadata", "labels")
	if labels[managedByLabel] != managedByValue {
		t.Errorf("missing managed-by label: %v", labels)
	}
}

// Read calls this on every plan. If it wrote unconditionally, `terraform plan`
// would mutate the cluster and require update permission it should not need.
func TestReconcileCredentialsSecret_NoWriteWhenUnchanged(t *testing.T) {
	creds := map[string]string{"AZURE_CLIENT_SECRET": "shhh"}
	c := testfake.NewClient(testfake.Secret("tekton-pipelines", "creds", creds, nil))
	ops := NewResourceOperations(c)

	// Any write would fail this reactor, proving no write was attempted.
	testfake.WithError(c, "update", testfake.SecretGVR,
		k8serrors.NewForbidden(k8sschema.GroupResource{Resource: "secrets"}, "creds", nil))

	changed, err := ops.ReconcileCredentialsSecret(context.Background(), "tekton-pipelines", "creds", creds)
	if err != nil {
		t.Fatalf("unchanged credentials must not write: %v", err)
	}
	if changed {
		t.Error("expected changed=false when the cluster already matches")
	}
}

func TestReconcileCredentialsSecret_RotationOverwrites(t *testing.T) {
	c := testfake.NewClient(testfake.Secret("tekton-pipelines", "creds",
		map[string]string{"AZURE_CLIENT_SECRET": "old"}, nil))
	ops := NewResourceOperations(c)

	changed, err := ops.ReconcileCredentialsSecret(context.Background(), "tekton-pipelines", "creds",
		map[string]string{"AZURE_CLIENT_SECRET": "new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("a rotated credential should report changed=true")
	}
	if _, data := fetch(t, c, "tekton-pipelines", "creds"); data["AZURE_CLIENT_SECRET"] != "new" {
		t.Errorf("rotation did not land: %v", data)
	}
}

// The Secret belongs to one action, so a key it no longer supplies must be
// REMOVED. Leaving it behind kept injecting a revoked credential through envFrom
// forever -- the merge semantics this replaced could never clear one.
func TestReconcileCredentialsSecret_RemovesKeysNoLongerSupplied(t *testing.T) {
	c := testfake.NewClient(testfake.Secret("tekton-pipelines", "creds",
		map[string]string{"REVOKED_KEY": "old-static-key", "AWS_ACCESS_KEY_ID": "old"},
		map[string]string{"owner": "platform"}))
	ops := NewResourceOperations(c)

	changed, err := ops.ReconcileCredentialsSecret(context.Background(), "tekton-pipelines", "creds",
		map[string]string{"AWS_ACCESS_KEY_ID": "new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("dropping a key should report changed=true")
	}

	obj, data := fetch(t, c, "tekton-pipelines", "creds")
	if _, still := data["REVOKED_KEY"]; still {
		t.Errorf("key no longer supplied was left in place: %v", data)
	}
	if data["AWS_ACCESS_KEY_ID"] != "new" || len(data) != 1 {
		t.Errorf("data should be exactly the supplied set, got %v", data)
	}

	// Labels set by others are still none of our business.
	labels, _, _ := unstructured.NestedStringMap(obj.Object, "metadata", "labels")
	if labels["owner"] != "platform" {
		t.Errorf("unmanaged label was destroyed: %v", labels)
	}
	if labels[managedByLabel] != managedByValue {
		t.Errorf("provider label not added: %v", labels)
	}
}

// An extra key in the cluster is a mismatch, so Read must not report "unchanged"
// and leave a stale credential in place.
func TestReconcileCredentialsSecret_ExtraKeyCountsAsMismatch(t *testing.T) {
	c := testfake.NewClient(testfake.Secret("tekton-pipelines", "creds",
		map[string]string{"WANTED": "v", "STALE": "v"}, nil))
	ops := NewResourceOperations(c)

	changed, err := ops.ReconcileCredentialsSecret(context.Background(), "tekton-pipelines", "creds",
		map[string]string{"WANTED": "v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("an extra stored key must count as a mismatch")
	}
	if _, data := fetch(t, c, "tekton-pipelines", "creds"); len(data) != 1 {
		t.Errorf("expected exactly the supplied set, got %v", data)
	}
}

// A Secret appearing between the Get and the Create is this action's own -- a
// retry after a partial apply. Treating AlreadyExists as success skipped the
// write entirely, so the losing writer's keys never landed.
func TestReconcileCredentialsSecret_AlreadyExistsStillReconciles(t *testing.T) {
	c := testfake.NewClient()
	ops := NewResourceOperations(c)

	// Seed after constructing the client so the Get misses and the Create collides.
	if _, err := c.Resource(testfake.SecretGVR).Namespace("tekton-pipelines").
		Create(context.Background(),
			testfake.Secret("tekton-pipelines", "creds", map[string]string{"K": "stale"}, nil),
			metav1.CreateOptions{}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if _, err := ops.ReconcileCredentialsSecret(context.Background(), "tekton-pipelines", "creds",
		map[string]string{"K": "fresh"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, data := fetch(t, c, "tekton-pipelines", "creds"); data["K"] != "fresh" {
		t.Errorf("AlreadyExists path skipped the write: %v", data)
	}
}

// Nothing deleted the Secret while it was environment-scoped, so a live
// credential outlived every teardown.
func TestDeleteCredentialsSecret_RemovesAndIsIdempotent(t *testing.T) {
	c := testfake.NewClient(testfake.Secret("tekton-pipelines", "creds",
		map[string]string{"K": "v"}, nil))
	ops := NewResourceOperations(c)

	if err := ops.DeleteCredentialsSecret(context.Background(), "tekton-pipelines", "creds"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := c.Resource(testfake.SecretGVR).Namespace("tekton-pipelines").
		Get(context.Background(), "creds", metav1.GetOptions{}); err == nil {
		t.Error("Secret still present after delete")
	}
	// Destroy retries must not fail on a Secret that is already gone.
	if err := ops.DeleteCredentialsSecret(context.Background(), "tekton-pipelines", "creds"); err != nil {
		t.Errorf("delete should be idempotent on NotFound, got %v", err)
	}
}

// A Forbidden write reporting success is how a rotated credential silently fails
// to propagate while apply stays green.
func TestReconcileCredentialsSecret_ForbiddenSurfaces(t *testing.T) {
	for _, verb := range []string{"get", "update", "create"} {
		t.Run(verb, func(t *testing.T) {
			seed := []interface{}{}
			_ = seed
			c := testfake.NewClient()
			if verb != "create" {
				c = testfake.NewClient(testfake.Secret("tekton-pipelines", "creds",
					map[string]string{"K": "old"}, nil))
			}
			ops := NewResourceOperations(c)
			testfake.WithError(c, verb, testfake.SecretGVR,
				k8serrors.NewForbidden(k8sschema.GroupResource{Resource: "secrets"}, "creds", nil))

			_, err := ops.ReconcileCredentialsSecret(context.Background(), "tekton-pipelines", "creds",
				map[string]string{"K": "new"})
			if err == nil {
				t.Fatalf("a Forbidden %s must surface as an error, not success", verb)
			}
			if !strings.Contains(err.Error(), "creds") {
				t.Errorf("error should name the Secret, got: %v", err)
			}
		})
	}
}

func TestReconcileCredentialsSecret_RequiresNamespace(t *testing.T) {
	ops := NewResourceOperations(testfake.NewClient())
	if _, err := ops.ReconcileCredentialsSecret(context.Background(), "", "creds",
		map[string]string{"K": "v"}); err == nil {
		t.Fatal("an empty namespace must be rejected, not silently applied to the default")
	}
}
