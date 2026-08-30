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

// Replacing the whole object destroyed unrelated keys and labels in an earlier
// design. Reconcile owns only the keys it was given.
func TestReconcileCredentialsSecret_PreservesUnmanagedKeysAndLabels(t *testing.T) {
	c := testfake.NewClient(testfake.Secret("tekton-pipelines", "creds",
		map[string]string{"KEEP_ME": "precious", "AWS_ACCESS_KEY_ID": "old"},
		map[string]string{"owner": "platform"}))
	ops := NewResourceOperations(c)

	if _, err := ops.ReconcileCredentialsSecret(context.Background(), "tekton-pipelines", "creds",
		map[string]string{"AWS_ACCESS_KEY_ID": "new"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj, data := fetch(t, c, "tekton-pipelines", "creds")
	if data["KEEP_ME"] != "precious" {
		t.Errorf("unmanaged key was destroyed: %v", data)
	}
	if data["AWS_ACCESS_KEY_ID"] != "new" {
		t.Errorf("managed key not updated: %v", data)
	}
	labels, _, _ := unstructured.NestedStringMap(obj.Object, "metadata", "labels")
	if labels["owner"] != "platform" {
		t.Errorf("unmanaged label was destroyed: %v", labels)
	}
	if labels[managedByLabel] != managedByValue {
		t.Errorf("provider label not added: %v", labels)
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
