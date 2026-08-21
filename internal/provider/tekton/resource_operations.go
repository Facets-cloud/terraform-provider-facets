package tekton

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

// VerifySecretKey checks that a Secret exists in the namespace and carries the
// given key. It returns a diagnostic-quality error naming exactly what is wrong
// and, where possible, what keys ARE present.
//
// This exists so a misconfigured Secret fails at APPLY time with an actionable
// message, rather than at action-run time with a CreateContainerConfigError that
// the operator can only diagnose by inspecting pod events.
func (r *ResourceOperations) VerifySecretKey(ctx context.Context, namespace, name, key string) error {
	gvr := k8sschema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}

	secret, err := r.client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return fmt.Errorf("Secret %q not found in namespace %q. Create it before the action "+
				"runs, e.g.:\n  kubectl create secret generic %s -n %s --from-literal=%s=<client-secret>",
				name, namespace, name, namespace, key)
		}
		if k8serrors.IsForbidden(err) {
			// Cannot verify -- do not fail the apply over a permissions gap.
			return nil
		}
		return fmt.Errorf("could not verify Secret %q in namespace %q: %w", name, namespace, err)
	}

	data, found, err := unstructuredNestedMap(secret.Object, "data")
	if err != nil || !found {
		return fmt.Errorf("Secret %q in namespace %q has no data", name, namespace)
	}
	if _, ok := data[key]; !ok {
		present := make([]string, 0, len(data))
		for k := range data {
			present = append(present, k)
		}
		sort.Strings(present)
		return fmt.Errorf("Secret %q in namespace %q has no key %q (present keys: %s). "+
			"Either add that key or set secret_key in the provider's azure block to match",
			name, namespace, key, strings.Join(present, ", "))
	}
	return nil
}

func unstructuredNestedMap(obj map[string]interface{}, field string) (map[string]interface{}, bool, error) {
	v, ok := obj[field]
	if !ok {
		return nil, false, nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("%s is not a map", field)
	}
	return m, true, nil
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
