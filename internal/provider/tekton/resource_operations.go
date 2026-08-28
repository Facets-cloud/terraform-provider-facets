package tekton

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ResourceOperations provides CRUD operations for Tekton resources
type ResourceOperations struct {
	client dynamic.Interface
}

// NewResourceOperations creates a new ResourceOperations instance
func NewResourceOperations(client dynamic.Interface) *ResourceOperations {
	return &ResourceOperations{client: client}
}

// CreateResource creates a Kubernetes resource. If the object already exists
// (AlreadyExists error), it adopts the existing cluster object by updating it
// in-place with the new spec. This handles two cases safely:
//
//  1. A prior apply's rollback failed, leaving an orphaned object in cluster.
//  2. The deterministic-hash name collides — a collision IS the same logical
//     resource, so updating it is correct.
//
// Combined with idempotent DeleteResource, this means apply retries are fully
// safe and will never get stuck on stale cluster objects.
func (r *ResourceOperations) CreateResource(ctx context.Context, obj *unstructured.Unstructured, group, version, resource string) error {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}
	namespace := obj.GetNamespace()
	_, err := r.client.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !k8serrors.IsAlreadyExists(err) {
		return err
	}
	// Adopt: a prior attempt's rollback may have failed, or the deterministic-
	// hash name collided. Update in place to bring the existing object to the
	// desired spec.
	current, getErr := r.client.Resource(gvr).Namespace(namespace).Get(ctx, obj.GetName(), metav1.GetOptions{})
	if getErr != nil {
		return getErr
	}
	obj.SetResourceVersion(current.GetResourceVersion())
	_, err = r.client.Resource(gvr).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

// UpdateResource updates a Kubernetes resource
func (r *ResourceOperations) UpdateResource(ctx context.Context, obj *unstructured.Unstructured, group, version, resource string) error {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	// Extract namespace and name from metadata
	namespace, name, err := ExtractMetadata(obj)
	if err != nil {
		return err
	}

	// Get current resource to preserve resourceVersion
	current, err := r.client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get current resource %s/%s: %w", namespace, name, err)
	}

	// Preserve resourceVersion for optimistic locking
	obj.SetResourceVersion(current.GetResourceVersion())

	_, err = r.client.Resource(gvr).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

// DeleteResource deletes a Kubernetes resource. The operation is idempotent:
// a NotFound error from the API is treated as a no-op and nil is returned.
func (r *ResourceOperations) DeleteResource(ctx context.Context, namespace, name, group, version, resource string) error {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	err := r.client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && k8serrors.IsNotFound(err) {
		return nil
	}
	return err
}

// ReconcileCredentialsSecret creates or updates the Secret holding the Azure
// service principal password, and returns nothing but an error.
//
// The provider owns this Secret rather than asking anyone to create it, because
// the two parties who would otherwise have to agree on its name cannot talk to
// each other: the Secret lives in the control plane's namespace, while the module
// referencing it is configured by a customer who cannot see that namespace. The
// name is derived from the service principal identity (see azure.DeriveSecretName),
// so it is reproducible from data both sides already hold.
//
// Two properties matter for multi-account control planes:
//
//   - Same service principal across projects -> same derived name -> ONE Secret,
//     shared. Applying twice is idempotent, not a conflict.
//   - Different service principals -> different names, so no project can
//     overwrite another's credentials.
//
// Ownership is deliberately NOT expressed with ownerReferences. A shared Secret
// has many legitimate owners, and a single ownerReference would let the first
// action's deletion garbage-collect credentials still in use by the others.
// Instead the Secret is labelled as provider-managed and left in place; it is
// inert without an action referencing it.
func (r *ResourceOperations) ReconcileCredentialsSecret(ctx context.Context, namespace, name, key, clientSecret string, identity map[string]string) error {
	gvr := k8sschema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}

	labels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "terraform-provider-facets",
		"facets.cloud/credential-type": "azure-service-principal",
	}
	// The derived name is a hash, so carry the identity in annotations to keep the
	// Secret traceable back to a service principal by inspection. The client
	// secret is the only sensitive value here; tenant/client/subscription ids are
	// not secrets and appear in plan output already.
	annotations := map[string]interface{}{}
	for k, v := range identity {
		annotations["facets.cloud/"+k] = v
	}

	desired := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":        name,
			"namespace":   namespace,
			"labels":      labels,
			"annotations": annotations,
		},
		"type": "Opaque",
		"stringData": map[string]interface{}{
			key: clientSecret,
		},
	}}

	existing, err := r.client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			if _, cerr := r.client.Resource(gvr).Namespace(namespace).Create(ctx, desired, metav1.CreateOptions{}); cerr != nil {
				if k8serrors.IsAlreadyExists(cerr) {
					// Another apply won the race for the same derived name. Same
					// identity means the same credential, so this is success.
					return nil
				}
				if k8serrors.IsForbidden(cerr) {
					return fmt.Errorf("not permitted to create Secret %q in namespace %q: %w\n"+
						"The provider needs create/update on secrets in that namespace, or the "+
						"Secret must be pre-created with key %q", name, namespace, cerr, key)
				}
				return fmt.Errorf("could not create Secret %q in namespace %q: %w", name, namespace, cerr)
			}
			return nil
		}
		if k8serrors.IsForbidden(err) {
			// Cannot read it to reconcile. If it already exists with the right
			// contents the action still works, so do not fail the apply here --
			// the failure, if any, surfaces at run time with a clear pod event.
			return nil
		}
		return fmt.Errorf("could not read Secret %q in namespace %q: %w", name, namespace, err)
	}

	// Adopt whatever is there, but MUTATE THE EXISTING OBJECT rather than replacing
	// it with `desired`. A full Update carrying only stringData drops every other
	// key: the apiserver merges StringData into a data map that, for a
	// stringData-only object, starts empty. A Secret shared with other tooling would
	// silently lose its other entries.
	//
	// `type` is immutable (ValidateSecretUpdate), so it must be left exactly as
	// stored -- hardcoding Opaque made adopting a non-Opaque Secret fail outright.
	existing.SetLabels(mergeStringMap(existing.GetLabels(), labels))
	existing.SetAnnotations(mergeStringMap(existing.GetAnnotations(), annotations))

	// stringData is write-only sugar the apiserver folds into data and clears, but
	// do not assume it is empty: MERGE our key in rather than replacing the map, so
	// a caller (or a fake apiserver) that leaves values there keeps them.
	sd, _ := existing.Object["stringData"].(map[string]interface{})
	if sd == nil {
		sd = map[string]interface{}{}
	}
	sd[key] = clientSecret
	existing.Object["stringData"] = sd

	if _, uerr := r.client.Resource(gvr).Namespace(namespace).Update(ctx, existing, metav1.UpdateOptions{}); uerr != nil {
		// Do NOT swallow these. A failed update means the OLD credential is still
		// live while the apply reports success -- the silent-stale-credential
		// failure this reconcile exists to prevent. Surface it and let the operator
		// decide.
		if k8serrors.IsForbidden(uerr) {
			return fmt.Errorf("not permitted to update Secret %q in namespace %q: %w\n"+
				"The stored credential is unchanged, so actions will keep using the "+
				"previous password. Grant update on secrets in that namespace, or "+
				"update the Secret out of band", name, namespace, uerr)
		}
		if k8serrors.IsConflict(uerr) {
			return fmt.Errorf("conflict updating Secret %q in namespace %q: %w\n"+
				"Another writer changed it concurrently. Re-run to retry; the stored "+
				"credential may still be the previous password", name, namespace, uerr)
		}
		return fmt.Errorf("could not update Secret %q in namespace %q: %w", name, namespace, uerr)
	}
	return nil
}

// mergeStringMap overlays src onto dst without discarding keys dst already has.
// src is map[string]interface{} because it comes from an unstructured object body.
func mergeStringMap(dst map[string]string, src map[string]interface{}) map[string]string {
	out := map[string]string{}
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		if sv, ok := v.(string); ok {
			out[k] = sv
		}
	}
	return out
}

// GetResource retrieves a Kubernetes resource
func (r *ResourceOperations) GetResource(ctx context.Context, namespace, name, group, version, resource string) (*unstructured.Unstructured, error) {
	gvr := k8sschema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	return r.client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}
