package tekton

import (
	"context"
	"fmt"
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

// VerifyOIDCFederationPossible checks, at apply time, that the cluster can actually
// mint the token OIDC federation needs.
//
// This exists because `use_oidc_federation = true` on its own is indistinguishable
// from a deliberate choice, so it applies cleanly and then fails at RUN time with
// "federated token not found" -- discovered by whoever clicks the action, not by
// whoever configured it. The likely cause is not a deliberate choice at all: an
// output-type mapping on a cloud_account module can inject the flag into every
// project using that account.
//
// Tekton supplies the pod template from the config-defaults ConfigMap. If
// default-pod-template does not mention the exchange audience, no action pod will
// carry a usable token, and that is knowable now rather than later.
func (r *ResourceOperations) VerifyOIDCFederationPossible(ctx context.Context, tektonNamespace string) error {
	gvr := k8sschema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

	cm, err := r.client.Resource(gvr).Namespace(tektonNamespace).Get(ctx, "config-defaults", metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsForbidden(err) || k8serrors.IsNotFound(err) {
			// Cannot determine it; do not block an apply on a permissions gap.
			return nil
		}
		return nil
	}

	data, ok := cm.Object["data"].(map[string]interface{})
	if !ok {
		data = map[string]interface{}{}
	}
	tmpl, _ := data["default-pod-template"].(string)

	if !strings.Contains(tmpl, azureTokenExchangeAudience) {
		return fmt.Errorf("use_oidc_federation is set, but this cluster cannot issue the "+
			"required token: the %q ConfigMap in namespace %q has no default-pod-template "+
			"projecting audience %q.\n"+
			"Action pods would fail at run time with \"federated token not found\".\n\n"+
			"If you did not intend OIDC federation, remove use_oidc_federation and set "+
			"client_secret instead -- the provider then manages the credentials Secret for you.\n"+
			"To enable OIDC, add default-pod-template projecting that audience and register a "+
			"federated identity credential on the app registration.",
			"config-defaults", tektonNamespace, azureTokenExchangeAudience)
	}
	return nil
}

// azureTokenExchangeAudience is the audience Microsoft Entra ID requires on a
// federated token presented for credential exchange.
const azureTokenExchangeAudience = "api://AzureADTokenExchange"

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

	// Adopt whatever is there. A pre-existing hand-created Secret is updated in
	// place rather than rejected, so switching to provider-managed credentials
	// needs no manual cleanup.
	desired.SetResourceVersion(existing.GetResourceVersion())
	if _, uerr := r.client.Resource(gvr).Namespace(namespace).Update(ctx, desired, metav1.UpdateOptions{}); uerr != nil {
		if k8serrors.IsForbidden(uerr) {
			return nil
		}
		if k8serrors.IsConflict(uerr) {
			// Concurrent write of the same identity's credential; converges.
			return nil
		}
		return fmt.Errorf("could not update Secret %q in namespace %q: %w", name, namespace, uerr)
	}
	return nil
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
