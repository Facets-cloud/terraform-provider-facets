package testfake

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Canonical error constructors mirroring the K8s API surface. These are the
// concrete error classes the provider's CRUD paths must distinguish between —
// see the RCA at ~/.flow/tasks/mis-tekton-leak/updates/2026-05-06-rca.md
// (especially §9.1 on the missing IsNotFound classification).

// ErrNotFound returns a typed NotFound StatusError for the given GVR + name.
// This is the only error class that should ever be treated as "this object
// has been deleted." Every other error class indicates a transient condition,
// authorization issue, or genuine apiserver fault — never a cue to wipe state.
func ErrNotFound(gvr schema.GroupVersionResource, name string) error {
	return apierrors.NewNotFound(gvr.GroupResource(), name)
}

// ErrAlreadyExists returns a typed AlreadyExists StatusError. This is the
// error users see at the "stepactions.tekton.dev <name> already exists"
// surface when an orphaned StepAction collides with a fresh Create.
func ErrAlreadyExists(gvr schema.GroupVersionResource, name string) error {
	return apierrors.NewAlreadyExists(gvr.GroupResource(), name)
}

// ErrForbidden simulates a 403 — typical when an RBAC reconcile briefly
// strips permissions, or when a service account is rotating tokens.
func ErrForbidden(gvr schema.GroupVersionResource, name string) error {
	return apierrors.NewForbidden(gvr.GroupResource(), name, nil)
}

// ErrServerTimeout simulates a 504 — apiserver overloaded, request timed
// out at the LB or kube-apiserver edge.
func ErrServerTimeout(operation string) error {
	return apierrors.NewServerTimeout(schema.GroupResource{}, operation, 0)
}

// ErrInternalServer simulates a 500 — anything from etcd contention to a
// webhook misconfiguration. Provider must not interpret this as deletion.
func ErrInternalServer(reason string) error {
	return apierrors.NewInternalError(&internalError{msg: reason})
}

// ErrServiceUnavailable simulates a 503 — the canonical "transient apiserver
// outage" the broader-framing of Bug #2 (RCA §9.1) flags as a state-corruption
// trigger.
func ErrServiceUnavailable(reason string) error {
	return apierrors.NewServiceUnavailable(reason)
}

// ErrContextCanceled returns context.Canceled, simulating a client-side
// cancellation (e.g. signal received mid-operation, or parent context
// cancelled by terraform). The provider must treat this as transient,
// not as object deletion.
func ErrContextCanceled() error {
	return context.Canceled
}

// internalError is a thin error type to satisfy NewInternalError's
// signature without introducing a runtime/serializer dependency.
type internalError struct {
	msg string
}

func (e *internalError) Error() string { return e.msg }

// _ asserts we're using the metav1 import so the linter doesn't trip if
// future helpers stop referencing it directly.
var _ = metav1.StatusReasonNotFound
