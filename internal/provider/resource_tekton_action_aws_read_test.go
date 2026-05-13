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

// Phase 2 / Phase 3 (AWS variant parity) tests for the AWS Tekton Action
// resource Read path. The AWS variant is bug-for-bug identical to the K8s
// variant — these tests mirror the K8s ones so the regression guard covers
// both files. The headline reproducer for issue #9 lives in K8s; this file
// extends the same coverage to AWS.

const (
	awsReadTestTaskName = "aws-task-xyz789"
)

func awsStateForRead(taskName string) TektonActionAWSResourceModel {
	return TektonActionAWSResourceModel{
		TaskName: types.StringValue(taskName),
	}
}

// awsResourceWithFake constructs a TektonActionAWSResource wired to a fake
// dynamic client for unit testing. Mirrors resourceWithFake in the K8s tests.
func awsResourceWithFake(seed ...*unstructured.Unstructured) (*TektonActionAWSResource, *dynamicfake.FakeDynamicClient) {
	objs := make([]runtime.Object, 0, len(seed))
	for _, s := range seed {
		objs = append(objs, s)
	}
	c := testfake.NewClient(objs...)
	r := &TektonActionAWSResource{
		clientFactory: func() (dynamic.Interface, error) { return c, nil },
	}
	return r, c
}

// --- Happy path -----------------------------------------------------------

func TestAWSReadResourceState_TaskHealthy_KeepState(t *testing.T) {
	task := testfake.Task(tektonPipelinesNamespace, awsReadTestTaskName, nil)
	r, c := awsResourceWithFake(task)

	remove, diags := r.readResourceState(context.Background(), c, awsStateForRead(awsReadTestTaskName))
	if remove {
		t.Errorf("expected state retained for healthy Task, got removeFromState=true")
	}
	if diags.HasError() {
		t.Errorf("unexpected error diagnostics: %v", diags)
	}
}

// --- Genuine deletion ----------------------------------------------------

func TestAWSReadResourceState_TaskNotFound_RemoveFromState(t *testing.T) {
	r, c := awsResourceWithFake()

	remove, diags := r.readResourceState(context.Background(), c, awsStateForRead(awsReadTestTaskName))
	if !remove {
		t.Errorf("expected removeFromState=true when Task is genuinely missing, got false")
	}
	if diags.HasError() {
		t.Errorf("unexpected error diagnostics: %v", diags)
	}
}

// --- Bug #9 post-fix assertions (FAIL on main, PASS after fix) -----------

// TestAWSReadResourceState_TaskGet503_StateRetainedAndErrorSurfaced asserts
// the POST-FIX behavior for issue #9 on the AWS variant: a transient 503 on
// Task Get must NOT wipe state — state retained, diagnostic error surfaced.
// FAILS on `main`; passes after issue #9 fix lands.
func TestAWSReadResourceState_TaskGet503_StateRetainedAndErrorSurfaced(t *testing.T) {
	task := testfake.Task(tektonPipelinesNamespace, awsReadTestTaskName, nil)
	r, c := awsResourceWithFake(task)
	testfake.WithError(c, "get", testfake.TaskGVR, testfake.ErrServiceUnavailable("apiserver overloaded"))

	remove, diags := r.readResourceState(context.Background(), c, awsStateForRead(awsReadTestTaskName))
	if remove {
		t.Errorf("expected state retained on transient 503 (post-fix), got removeFromState=true")
	}
	if !diags.HasError() {
		t.Errorf("expected diagnostic error on 503 (post-fix), got none")
	}
}

// TestAWSReadResourceState_TaskGetForbidden_StateRetainedAndErrorSurfaced
// asserts post-fix behavior. FAILS on `main`; passes after issue #9 fix.
func TestAWSReadResourceState_TaskGetForbidden_StateRetainedAndErrorSurfaced(t *testing.T) {
	task := testfake.Task(tektonPipelinesNamespace, awsReadTestTaskName, nil)
	r, c := awsResourceWithFake(task)
	testfake.WithError(c, "get", testfake.TaskGVR, testfake.ErrForbidden(testfake.TaskGVR, awsReadTestTaskName))

	remove, diags := r.readResourceState(context.Background(), c, awsStateForRead(awsReadTestTaskName))
	if remove {
		t.Errorf("expected state retained on Forbidden (post-fix), got removeFromState=true")
	}
	if !diags.HasError() {
		t.Errorf("expected diagnostic error on Forbidden (post-fix), got none")
	}
}

// TestAWSReadResourceState_TaskGetServerTimeout_StateRetainedAndErrorSurfaced
// asserts post-fix behavior. FAILS on `main`; passes after issue #9 fix.
func TestAWSReadResourceState_TaskGetServerTimeout_StateRetainedAndErrorSurfaced(t *testing.T) {
	task := testfake.Task(tektonPipelinesNamespace, awsReadTestTaskName, nil)
	r, c := awsResourceWithFake(task)
	testfake.WithError(c, "get", testfake.TaskGVR, testfake.ErrServerTimeout("read"))

	remove, diags := r.readResourceState(context.Background(), c, awsStateForRead(awsReadTestTaskName))
	if remove {
		t.Errorf("expected state retained on ServerTimeout (post-fix), got removeFromState=true")
	}
	if !diags.HasError() {
		t.Errorf("expected diagnostic error on ServerTimeout (post-fix), got none")
	}
}

// TestAWSReadResourceState_TaskGetContextCanceled_StateRetainedAndErrorSurfaced
// asserts post-fix behavior. FAILS on `main`; passes after issue #9 fix.
func TestAWSReadResourceState_TaskGetContextCanceled_StateRetainedAndErrorSurfaced(t *testing.T) {
	task := testfake.Task(tektonPipelinesNamespace, awsReadTestTaskName, nil)
	r, c := awsResourceWithFake(task)
	testfake.WithError(c, "get", testfake.TaskGVR, testfake.ErrContextCanceled())

	remove, diags := r.readResourceState(context.Background(), c, awsStateForRead(awsReadTestTaskName))
	if remove {
		t.Errorf("expected state retained on context.Canceled (post-fix), got removeFromState=true")
	}
	if !diags.HasError() {
		t.Errorf("expected diagnostic error on context.Canceled (post-fix), got none")
	}
}

// --- Factory seam smoke test --------------------------------------------

func TestAWSResource_FactorySeam_InjectsFakeClient(t *testing.T) {
	called := false
	fakeClient := testfake.NewClient()
	r := &TektonActionAWSResource{
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
