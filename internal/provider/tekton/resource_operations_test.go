package tekton

import (
	"context"
	"errors"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton/testfake"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Phase 1 tests for ResourceOperations — the low-level CRUD wrapper around
// dynamic.Interface. Lives here (not in testfake/) because it tests the
// production helper directly.
//
// The crown jewel is TestDeleteResource_NotFound_IdempotentReturnsNil, which
// reproduces issue #11 (DeleteResource non-idempotency). It asserts the
// POST-FIX behavior — Delete must treat NotFound as already-deleted and
// return nil. The test FAILS on `main` today (where NotFound is propagated)
// and PASSES once the #11 fix lands. The Forbidden_Propagates test is the
// regression guard ensuring the eventual fix doesn't accidentally swallow
// non-NotFound errors.

const testNamespace = "tekton-pipelines"

// --- CreateResource -------------------------------------------------------

func TestCreateResource_Success(t *testing.T) {
	c := testfake.NewClient()
	ops := NewResourceOperations(c)

	task := testfake.Task(testNamespace, "task-1", nil)
	if err := ops.CreateResource(context.Background(), task, "tekton.dev", "v1beta1", "tasks"); err != nil {
		t.Fatalf("CreateResource failed: %v", err)
	}

	// Verify it actually landed in the fake.
	if _, err := c.Resource(testfake.TaskGVR).Namespace(testNamespace).Get(context.Background(), "task-1", metav1GetOptions()); err != nil {
		t.Errorf("expected task in fake after Create, Get failed: %v", err)
	}
}

func TestCreateResource_AlreadyExists_ErrorPropagates(t *testing.T) {
	existing := testfake.Task(testNamespace, "task-1", nil)
	c := testfake.NewClient(existing)
	ops := NewResourceOperations(c)

	dup := testfake.Task(testNamespace, "task-1", nil)
	err := ops.CreateResource(context.Background(), dup, "tekton.dev", "v1beta1", "tasks")
	if err == nil {
		t.Fatal("expected AlreadyExists error, got nil")
	}
	if !apierrors.IsAlreadyExists(err) {
		t.Errorf("expected AlreadyExists, got %T: %v", err, err)
	}
}

// --- UpdateResource -------------------------------------------------------

func TestUpdateResource_Success(t *testing.T) {
	old := testfake.Task(testNamespace, "task-1", map[string]string{"v": "1"})
	c := testfake.NewClient(old)
	ops := NewResourceOperations(c)

	updated := testfake.Task(testNamespace, "task-1", map[string]string{"v": "2"})
	if err := ops.UpdateResource(context.Background(), updated, "tekton.dev", "v1beta1", "tasks"); err != nil {
		t.Fatalf("UpdateResource failed: %v", err)
	}

	got, err := c.Resource(testfake.TaskGVR).Namespace(testNamespace).Get(context.Background(), "task-1", metav1GetOptions())
	if err != nil {
		t.Fatalf("Get after Update failed: %v", err)
	}
	if got.GetLabels()["v"] != "2" {
		t.Errorf("label v = %q, want %q (update did not take effect)", got.GetLabels()["v"], "2")
	}
}

func TestUpdateResource_NotFound_ErrorPropagates(t *testing.T) {
	c := testfake.NewClient() // empty
	ops := NewResourceOperations(c)

	obj := testfake.Task(testNamespace, "missing", nil)
	err := ops.UpdateResource(context.Background(), obj, "tekton.dev", "v1beta1", "tasks")
	if err == nil {
		t.Fatal("expected error updating missing object, got nil")
	}
	// UpdateResource wraps the error with fmt.Errorf, so use errors.Is or
	// substring fallback. The unwrapped cause must remain a NotFound for
	// callers that classify.
	var statusErr *apierrors.StatusError
	if !errors.As(err, &statusErr) || !apierrors.IsNotFound(statusErr) {
		// Update first does a Get internally; that's where NotFound comes from.
		// Either way, ensure NotFound is detectable downstream.
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected wrapped NotFound, got %T: %v", err, err)
		}
	}
}

// --- DeleteResource (the headline) ---------------------------------------

func TestDeleteResource_Success(t *testing.T) {
	task := testfake.Task(testNamespace, "task-1", nil)
	c := testfake.NewClient(task)
	ops := NewResourceOperations(c)

	if err := ops.DeleteResource(context.Background(), testNamespace, "task-1", "tekton.dev", "v1beta1", "tasks"); err != nil {
		t.Fatalf("DeleteResource failed: %v", err)
	}

	// Verify gone.
	if _, err := c.Resource(testfake.TaskGVR).Namespace(testNamespace).Get(context.Background(), "task-1", metav1GetOptions()); !apierrors.IsNotFound(err) {
		t.Errorf("expected task gone after Delete; Get returned: %v", err)
	}
}

// TestDeleteResource_NotFound_IdempotentReturnsNil asserts the POST-FIX
// behavior for issue #11. Delete must treat a NotFound response as
// already-deleted and return nil — destroys must be idempotent.
//
// FAILS on `main` because DeleteResource currently propagates NotFound.
// Will pass once issue #11 fix lands (the fix adds
// `if k8serrors.IsNotFound(err) { return nil }` in the Delete path).
//
// See: https://github.com/Facets-cloud/terraform-provider-facets/issues/11
//      RCA at ~/.flow/tasks/mis-tekton-leak/updates/2026-05-06-rca.md §3.4
func TestDeleteResource_NotFound_IdempotentReturnsNil(t *testing.T) {
	c := testfake.NewClient() // empty — object not present
	ops := NewResourceOperations(c)

	err := ops.DeleteResource(context.Background(), testNamespace, "missing", "tekton.dev", "v1beta1", "tasks")
	if err != nil {
		t.Fatalf("expected DeleteResource to be idempotent on NotFound (return nil), got err=%v. "+
			"This test asserts the post-fix behavior; it will fail on `main` until issue #11 lands.", err)
	}
}

// TestDeleteResource_Forbidden_Propagates is the regression guard for the
// eventual #11 fix. After fix: NotFound returns nil, but every other error
// class — including Forbidden — must still propagate. This test catches an
// over-eager "swallow all errors" mistake.
func TestDeleteResource_Forbidden_Propagates(t *testing.T) {
	c := testfake.NewClient()
	testfake.WithError(c, "delete", testfake.TaskGVR, testfake.ErrForbidden(testfake.TaskGVR, "task-1"))
	ops := NewResourceOperations(c)

	err := ops.DeleteResource(context.Background(), testNamespace, "task-1", "tekton.dev", "v1beta1", "tasks")
	if err == nil {
		t.Fatal("expected Forbidden to propagate, got nil")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected Forbidden, got %T: %v", err, err)
	}
}

func TestDeleteResource_InternalServer_Propagates(t *testing.T) {
	c := testfake.NewClient()
	testfake.WithError(c, "delete", testfake.TaskGVR, testfake.ErrInternalServer("etcd unavailable"))
	ops := NewResourceOperations(c)

	err := ops.DeleteResource(context.Background(), testNamespace, "task-1", "tekton.dev", "v1beta1", "tasks")
	if err == nil {
		t.Fatal("expected InternalServer error to propagate, got nil")
	}
	if !apierrors.IsInternalError(err) {
		t.Errorf("expected InternalError, got %T: %v", err, err)
	}
}

// --- GetResource ----------------------------------------------------------

func TestGetResource_Success(t *testing.T) {
	task := testfake.Task(testNamespace, "task-1", map[string]string{"role": "rollout"})
	c := testfake.NewClient(task)
	ops := NewResourceOperations(c)

	got, err := ops.GetResource(context.Background(), testNamespace, "task-1", "tekton.dev", "v1beta1", "tasks")
	if err != nil {
		t.Fatalf("GetResource failed: %v", err)
	}
	if got.GetLabels()["role"] != "rollout" {
		t.Errorf("label role = %q, want %q", got.GetLabels()["role"], "rollout")
	}
}

func TestGetResource_NotFound_ErrorPropagates(t *testing.T) {
	c := testfake.NewClient()
	ops := NewResourceOperations(c)

	_, err := ops.GetResource(context.Background(), testNamespace, "missing", "tekton.dev", "v1beta1", "tasks")
	if err == nil {
		t.Fatal("expected NotFound error, got nil")
	}
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %T: %v", err, err)
	}
}

// --- helpers --------------------------------------------------------------

// metav1GetOptions returns a zero-value GetOptions; thin wrapper to avoid
// importing metav1 across every test.
func metav1GetOptions() metav1.GetOptions { return metav1.GetOptions{} }
