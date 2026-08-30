package tekton

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"

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

// ReconcileCredentialsSecret makes the named Secret hold exactly the supplied
// credential keys, and reports whether it had to write.
//
// Three properties matter, each of them a bug found in an earlier iteration of
// this idea:
//
//   - It MERGES. Keys already present that we do not manage are preserved, as are
//     labels and annotations set by anyone else. Replacing the whole object
//     silently destroyed unrelated keys when the Secret already existed.
//   - It COMPARES before writing, and returns changed=false when the cluster
//     already matches. That is what lets Read call this without turning every
//     `terraform plan` into a cluster write.
//   - It does NOT swallow permission errors. A Forbidden update returning success
//     is how a rotated credential silently fails to propagate while apply reports
//     green -- the precise failure this whole mechanism exists to prevent.
//
// `type` is set only at creation. It is immutable on a Secret, so sending it on
// update would reject adoption of any Secret that is not already Opaque.
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
		return true, r.createCredentialsSecret(ctx, namespace, name, creds)
	}

	if credentialsMatch(existing, creds) {
		return false, nil
	}

	desired := existing.DeepCopy()
	mergeCredentials(desired, creds)
	ensureLabels(desired)

	if _, err := r.client.Resource(secretGVR).Namespace(namespace).Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("could not update Secret %s/%s: %w", namespace, name, err)
	}
	return true, nil
}

func (r *ResourceOperations) createCredentialsSecret(ctx context.Context, namespace, name string, creds map[string]string) error {
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

	_, err := r.client.Resource(secretGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if k8serrors.IsAlreadyExists(err) {
		// Another apply won the race. Both wrote the same credential set, so the
		// cluster already holds the desired state.
		return nil
	}
	return fmt.Errorf("could not create Secret %s/%s: %w", namespace, name, err)
}

// credentialsMatch reports whether every supplied credential is already stored
// with the same value. Extra keys in the cluster are ignored: this function owns
// only the keys it was given.
func credentialsMatch(secret *unstructured.Unstructured, creds map[string]string) bool {
	data, _, _ := unstructured.NestedStringMap(secret.Object, "data")
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

// mergeCredentials writes the managed keys into the object without disturbing
// keys it does not manage.
//
// The apiserver returns base64 `data`; writing `stringData` for our keys while
// leaving `data` intact would be ambiguous, so everything is normalised into
// `data` and encoded here.
func mergeCredentials(secret *unstructured.Unstructured, creds map[string]string) {
	data, _, _ := unstructured.NestedStringMap(secret.Object, "data")
	if data == nil {
		data = map[string]string{}
	}
	merged := maps.Clone(data)
	for k, v := range creds {
		merged[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	unstructured.SetNestedStringMap(secret.Object, merged, "data")
	unstructured.RemoveNestedField(secret.Object, "stringData")
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
