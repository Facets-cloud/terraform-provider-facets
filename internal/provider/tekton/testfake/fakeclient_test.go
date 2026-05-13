package testfake

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Sanity tests for the harness itself. If these fail, every downstream test
// is meaningless — fix here first.

func TestNewClient_EmptySeed(t *testing.T) {
	c := NewClient()
	_, err := c.Resource(TaskGVR).Namespace("default").Get(context.Background(), "missing", metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected NotFound error from empty client, got nil")
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound error, got %T: %v", err, err)
	}
}

func TestNewClient_SeededTaskRetrievable(t *testing.T) {
	task := Task("default", "task-1", map[string]string{"app": "demo"})
	c := NewClient(task)

	got, err := c.Resource(TaskGVR).Namespace("default").Get(context.Background(), "task-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.GetName() != "task-1" {
		t.Errorf("name = %q, want %q", got.GetName(), "task-1")
	}
	if got.GetLabels()["app"] != "demo" {
		t.Errorf("label app = %q, want %q", got.GetLabels()["app"], "demo")
	}
}

func TestNewClient_SeededStepActionRetrievable(t *testing.T) {
	sa := StepAction("default", "setup-credentials-abc", nil)
	c := NewClient(sa)

	got, err := c.Resource(StepActionGVR).Namespace("default").Get(context.Background(), "setup-credentials-abc", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.GetKind() != "StepAction" {
		t.Errorf("kind = %q, want %q", got.GetKind(), "StepAction")
	}
}

func TestWithError_AllVerbsReturnInjectedError(t *testing.T) {
	c := NewClient()
	injected := errors.New("synthetic failure")
	WithError(c, "get", TaskGVR, injected)

	_, err := c.Resource(TaskGVR).Namespace("default").Get(context.Background(), "anything", metav1.GetOptions{})
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestWithError_SpecificVerbDoesNotAffectOthers(t *testing.T) {
	task := Task("default", "task-1", nil)
	c := NewClient(task)
	WithError(c, "delete", TaskGVR, ErrForbidden(TaskGVR, "task-1"))

	// Get should still succeed.
	if _, err := c.Resource(TaskGVR).Namespace("default").Get(context.Background(), "task-1", metav1.GetOptions{}); err != nil {
		t.Fatalf("Get unexpectedly failed: %v", err)
	}

	// Delete should fail with the injected Forbidden.
	err := c.Resource(TaskGVR).Namespace("default").Delete(context.Background(), "task-1", metav1.DeleteOptions{})
	if !apierrors.IsForbidden(err) {
		t.Fatalf("expected Forbidden, got %v", err)
	}
}

func TestWithErrorOnGVRAndName_ScopesToTargetName(t *testing.T) {
	taskA := Task("default", "task-a", nil)
	taskB := Task("default", "task-b", nil)
	c := NewClient(taskA, taskB)
	WithErrorOnGVRAndName(c, "get", TaskGVR, "task-b", ErrServiceUnavailable("test"))

	// task-a should fetch fine.
	if _, err := c.Resource(TaskGVR).Namespace("default").Get(context.Background(), "task-a", metav1.GetOptions{}); err != nil {
		t.Errorf("task-a Get unexpectedly errored: %v", err)
	}

	// task-b should hit the injected error.
	_, err := c.Resource(TaskGVR).Namespace("default").Get(context.Background(), "task-b", metav1.GetOptions{})
	if !apierrors.IsServiceUnavailable(err) {
		t.Errorf("expected ServiceUnavailable for task-b, got %v", err)
	}
}

func TestErrorBuilders_ProduceCorrectStatusReasons(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		checker func(error) bool
	}{
		{"NotFound", ErrNotFound(TaskGVR, "x"), apierrors.IsNotFound},
		{"AlreadyExists", ErrAlreadyExists(TaskGVR, "x"), apierrors.IsAlreadyExists},
		{"Forbidden", ErrForbidden(TaskGVR, "x"), apierrors.IsForbidden},
		{"ServerTimeout", ErrServerTimeout("get"), apierrors.IsServerTimeout},
		{"InternalServer", ErrInternalServer("etcd hiccup"), apierrors.IsInternalError},
		{"ServiceUnavailable", ErrServiceUnavailable("503"), apierrors.IsServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.checker(tt.err) {
				t.Errorf("error %T failed reason check: %v", tt.err, tt.err)
			}
		})
	}
}
