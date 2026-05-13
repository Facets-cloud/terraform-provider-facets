package provider

import (
	"context"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton/testfake"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// Phase 2 tests for the Kubernetes Tekton Action resource Read path.
//
// These tests target r.readResourceState directly with a fake dynamic client
// and a constructed state model — bypassing the terraform-plugin-framework
// req/resp plumbing that would otherwise require building tfsdk.State by
// hand. The lifecycle method (Read) is a thin wrapper that delegates to this
// helper; the bug-prone code lives entirely inside the helper.
//
// The headline tests are
// TestK8sReadResourceState_TaskGet*_StateRetainedAndErrorSurfaced, which
// reproduce issue #9 (Read silently wipes state on any K8s API error, not
// just NotFound). They assert the POST-FIX behavior: state is retained and a
// diagnostic error is surfaced. They FAIL on `main` today and PASS once #9
// lands.

const (
	k8sReadTestNamespace = "tekton-pipelines"
	k8sReadTestTaskName  = "task-abc123"
)

// stateForRead returns a minimal TektonActionKubernetesResourceModel populated
// with just the namespace and task name fields that readResourceState reads.
// Other fields are left zero-valued — they aren't relevant to Read.
func stateForRead(namespace, taskName string) TektonActionKubernetesResourceModel {
	return TektonActionKubernetesResourceModel{
		Namespace: types.StringValue(namespace),
		TaskName:  types.StringValue(taskName),
	}
}

// resourceWithFake constructs a TektonActionKubernetesResource wired to a
// fake dynamic client via the package-private clientFactory field. Any seed
// objects are passed through to the fake client unchanged.
//
// The concrete *FakeDynamicClient type is returned (rather than dynamic.Interface)
// so callers can pass it to testfake.WithError to install reactors.
func resourceWithFake(seed ...*unstructured.Unstructured) (*TektonActionKubernetesResource, *dynamicfake.FakeDynamicClient) {
	objs := make([]runtime.Object, 0, len(seed))
	for _, s := range seed {
		objs = append(objs, s)
	}
	c := testfake.NewClient(objs...)
	r := &TektonActionKubernetesResource{
		clientFactory: func() (dynamic.Interface, error) { return c, nil },
	}
	return r, c
}

// --- Happy path -----------------------------------------------------------

func TestK8sReadResourceState_TaskHealthy_KeepState(t *testing.T) {
	task := testfake.Task(k8sReadTestNamespace, k8sReadTestTaskName, nil)
	r, c := resourceWithFake(task)

	remove, diags := r.readResourceState(context.Background(), c, stateForRead(k8sReadTestNamespace, k8sReadTestTaskName))
	if remove {
		t.Errorf("expected state retained for healthy Task, got removeFromState=true")
	}
	if diags.HasError() {
		t.Errorf("unexpected error diagnostics: %v", diags)
	}
}

// --- Genuine deletion (correct current behavior) -------------------------

func TestK8sReadResourceState_TaskNotFound_RemoveFromState(t *testing.T) {
	r, c := resourceWithFake() // empty cluster

	remove, diags := r.readResourceState(context.Background(), c, stateForRead(k8sReadTestNamespace, k8sReadTestTaskName))
	if !remove {
		t.Errorf("expected removeFromState=true when Task is genuinely missing, got false")
	}
	if diags.HasError() {
		t.Errorf("unexpected error diagnostics: %v", diags)
	}
}

// --- Bug #9 post-fix assertions (FAIL on main, PASS after fix) -----------

// TestK8sReadResourceState_TaskGet503_StateRetainedAndErrorSurfaced asserts
// the POST-FIX behavior for issue #9. A transient 503 on Task Get must NOT
// wipe state — instead, state is retained and a diagnostic error surfaces
// telling the operator the apiserver call failed.
//
// FAILS on `main` because the current Read clears state on any error.
// Passes once issue #9 fix lands.
//
// See: https://github.com/Facets-cloud/terraform-provider-facets/issues/9
//      RCA §9.1 (broader-framing reframe)
func TestK8sReadResourceState_TaskGet503_StateRetainedAndErrorSurfaced(t *testing.T) {
	task := testfake.Task(k8sReadTestNamespace, k8sReadTestTaskName, nil)
	r, c := resourceWithFake(task)
	testfake.WithError(c, "get", testfake.TaskGVR, testfake.ErrServiceUnavailable("apiserver overloaded"))

	remove, diags := r.readResourceState(context.Background(), c, stateForRead(k8sReadTestNamespace, k8sReadTestTaskName))
	if remove {
		t.Errorf("expected state retained on transient 503 (post-fix), got removeFromState=true")
	}
	if !diags.HasError() {
		t.Errorf("expected diagnostic error to surface on 503 (post-fix), got none")
	}
}

// TestK8sReadResourceState_TaskGetForbidden_StateRetainedAndErrorSurfaced
// asserts post-fix behavior. Forbidden must retain state and surface an error.
// FAILS on `main`; passes after issue #9 fix.
func TestK8sReadResourceState_TaskGetForbidden_StateRetainedAndErrorSurfaced(t *testing.T) {
	task := testfake.Task(k8sReadTestNamespace, k8sReadTestTaskName, nil)
	r, c := resourceWithFake(task)
	testfake.WithError(c, "get", testfake.TaskGVR, testfake.ErrForbidden(testfake.TaskGVR, k8sReadTestTaskName))

	remove, diags := r.readResourceState(context.Background(), c, stateForRead(k8sReadTestNamespace, k8sReadTestTaskName))
	if remove {
		t.Errorf("expected state retained on Forbidden (post-fix), got removeFromState=true")
	}
	if !diags.HasError() {
		t.Errorf("expected diagnostic error to surface on Forbidden (post-fix), got none")
	}
}

// TestK8sReadResourceState_TaskGetServerTimeout_StateRetainedAndErrorSurfaced
// asserts post-fix behavior. ServerTimeout must retain state and surface an
// error. FAILS on `main`; passes after issue #9 fix.
func TestK8sReadResourceState_TaskGetServerTimeout_StateRetainedAndErrorSurfaced(t *testing.T) {
	task := testfake.Task(k8sReadTestNamespace, k8sReadTestTaskName, nil)
	r, c := resourceWithFake(task)
	testfake.WithError(c, "get", testfake.TaskGVR, testfake.ErrServerTimeout("read"))

	remove, diags := r.readResourceState(context.Background(), c, stateForRead(k8sReadTestNamespace, k8sReadTestTaskName))
	if remove {
		t.Errorf("expected state retained on ServerTimeout (post-fix), got removeFromState=true")
	}
	if !diags.HasError() {
		t.Errorf("expected diagnostic error to surface on ServerTimeout (post-fix), got none")
	}
}

// TestK8sReadResourceState_TaskGetContextCanceled_StateRetainedAndErrorSurfaced
// asserts post-fix behavior. context.Canceled must retain state and surface an
// error. FAILS on `main`; passes after issue #9 fix.
func TestK8sReadResourceState_TaskGetContextCanceled_StateRetainedAndErrorSurfaced(t *testing.T) {
	task := testfake.Task(k8sReadTestNamespace, k8sReadTestTaskName, nil)
	r, c := resourceWithFake(task)
	testfake.WithError(c, "get", testfake.TaskGVR, testfake.ErrContextCanceled())

	remove, diags := r.readResourceState(context.Background(), c, stateForRead(k8sReadTestNamespace, k8sReadTestTaskName))
	if remove {
		t.Errorf("expected state retained on context.Canceled (post-fix), got removeFromState=true")
	}
	if !diags.HasError() {
		t.Errorf("expected diagnostic error to surface on context.Canceled (post-fix), got none")
	}
}

// --- Factory seam smoke test (Option B verification) --------------------

func TestK8sResource_FactorySeam_InjectsFakeClient(t *testing.T) {
	called := false
	fakeClient := testfake.NewClient()
	r := &TektonActionKubernetesResource{
		clientFactory: func() (dynamic.Interface, error) {
			called = true
			return fakeClient, nil
		},
	}

	got, _, err := r.getClient()
	if err != nil {
		t.Fatalf("getClient unexpectedly errored: %v", err)
	}
	if !called {
		t.Error("expected clientFactory to be invoked by getClient")
	}
	if got != fakeClient {
		t.Error("getClient returned a different client than the factory produced")
	}
}
