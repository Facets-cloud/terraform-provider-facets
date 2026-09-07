package tekton

import (
	"context"
	"encoding/base64"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

var secretGVR = k8sschema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}

const (
	managedByLabel    = "app.kubernetes.io/managed-by"
	managedByValue    = "terraform-provider-facets"
	credentialTypeKey = "facets.cloud/credential-type"
	credentialTypeVal = "action-credentials"
)

// ReconcileCredentialsSecret makes the Secret hold EXACTLY the supplied
// credential keys, and reports whether it had to write.
//
// The Secret belongs to one action, named from that action's Task. That scoping
// is what makes the rest of this function simple, and getting it wrong caused
// several bugs at once: when the Secret was shared by every action in an
// environment, two modules supplying different service principals wrote the same
// key to the same object, so the last apply won, each Read flipped it back, and
// every pod in the environment authenticated as whichever principal wrote most
// recently -- silently, since nothing errors.
//
// Three properties follow from single ownership:
//
//   - It REPLACES. The supplied map is the whole desired contents, so a key a
//     module stops supplying is removed rather than left behind injecting a
//     revoked credential forever. Merging was only ever needed to avoid
//     clobbering another action's keys, which can no longer happen.
//   - It COMPARES before writing, and returns changed=false when the cluster
//     already matches. That is what lets Read call this without turning every
//     `terraform plan` into a cluster write.
//   - It does NOT swallow permission errors. A Forbidden update returning success
//     is how a rotated credential silently fails to propagate while apply reports
//     green -- the precise failure this whole mechanism exists to prevent.
//
// `type` is set only at creation. It is immutable on a Secret, so sending it on
// update would reject adoption of an existing Secret that is not already Opaque.
func (r *ResourceOperations) ReconcileCredentialsSecret(
	ctx context.Context, namespace, name string, creds map[string]string,
) (changed bool, err error) {
	if namespace == "" {
		return false, fmt.Errorf("namespace is required to reconcile Secret %q", name)
	}

	existing, err := r.client.Resource(secretGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("could not read Secret %s/%s: %w", namespace, name, err)
		}
		created, cerr := r.createCredentialsSecret(ctx, namespace, name, creds)
		if cerr != nil {
			return false, cerr
		}
		if created {
			return true, nil
		}
		// AlreadyExists: something wrote it between the Get and the Create -- a
		// retry after a partial apply, most likely. It is this action's own
		// Secret either way, so fall through and bring it to the desired state
		// rather than assuming the other writer got it right.
		existing, err = r.client.Resource(secretGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("could not read Secret %s/%s after AlreadyExists: %w", namespace, name, err)
		}
	}

	if credentialsMatch(existing, creds) {
		return false, nil
	}

	desired := existing.DeepCopy()
	setCredentials(desired, creds)
	ensureLabels(desired)

	if _, err := r.client.Resource(secretGVR).Namespace(namespace).Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("could not update Secret %s/%s: %w", namespace, name, err)
	}
	return true, nil
}

// DeleteCredentialsSecret removes an action's credentials Secret. Idempotent on
// NotFound, so destroy retries are safe.
//
// The Secret belongs to exactly one action, so removing it with that action
// leaves nothing behind. While it was environment-scoped there was no safe point
// to delete it from and a live credential outlived every teardown.
func (r *ResourceOperations) DeleteCredentialsSecret(ctx context.Context, namespace, name string) error {
	if namespace == "" || name == "" {
		return nil
	}
	err := r.client.Resource(secretGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("could not delete Secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// createCredentialsSecret creates the Secret. Reports created=false on
// AlreadyExists so the caller can reconcile the existing object instead of
// assuming it already holds the right contents.
func (r *ResourceOperations) createCredentialsSecret(ctx context.Context, namespace, name string, creds map[string]string) (created bool, err error) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				managedByLabel:    managedByValue,
				credentialTypeKey: credentialTypeVal,
			},
		},
		"type": "Opaque",
		// `data` (base64) rather than `stringData`. The apiserver would fold
		// stringData into data for us, but writing data directly keeps create and
		// update using one representation -- and keeps a fake client, which does
		// not perform that fold, honest about what the cluster ends up holding.
		"data": encodeCredentials(creds),
	}}

	if _, err := r.client.Resource(secretGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, fmt.Errorf("could not create Secret %s/%s: %w", namespace, name, err)
	}
	return true, nil
}

// credentialsMatch reports whether the stored data is EXACTLY the supplied set.
// An extra key in the cluster counts as a mismatch: the Secret belongs to this
// action alone, so a key not in the desired map is one a module has stopped
// supplying and must be removed.
func credentialsMatch(secret *unstructured.Unstructured, creds map[string]string) bool {
	data, _, _ := unstructured.NestedStringMap(secret.Object, "data")
	if len(data) != len(creds) {
		return false
	}
	for k, want := range creds {
		encoded, ok := data[k]
		if !ok {
			return false
		}
		got, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || string(got) != want {
			return false
		}
	}
	return true
}

// setCredentials replaces the object's data with exactly the supplied keys.
//
// The apiserver returns base64 `data`; writing `stringData` alongside an existing
// `data` would be ambiguous, so everything is normalised into `data` and encoded
// here.
func setCredentials(secret *unstructured.Unstructured, creds map[string]string) {
	unstructured.SetNestedStringMap(secret.Object, encodeStringMap(creds), "data")
	unstructured.RemoveNestedField(secret.Object, "stringData")
}

func encodeStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return out
}

// ensureLabels adds the provider's own labels while leaving any others in place.
func ensureLabels(secret *unstructured.Unstructured) {
	labels, _, _ := unstructured.NestedStringMap(secret.Object, "metadata", "labels")
	if labels == nil {
		labels = map[string]string{}
	}
	labels[managedByLabel] = managedByValue
	labels[credentialTypeKey] = credentialTypeVal
	unstructured.SetNestedStringMap(secret.Object, labels, "metadata", "labels")
}

func encodeCredentials(m map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return out
}
