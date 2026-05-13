// Package testfake provides a dynamic-client test harness for the Tekton
// Action resources, enabling unit tests of CRUD lifecycle behavior against
// a fake Kubernetes API without a real cluster.
//
// The harness wraps client-go/dynamic/fake with helpers that:
//   - Pre-seed Task and StepAction objects in any combination
//   - Inject specific error classes (NotFound, Forbidden, ServerTimeout, etc.)
//     on a specific verb against a specific GVR
//   - Provide canonical fixtures so tests don't repeat unstructured-object
//     boilerplate
//
// This package is test-only and should not be imported by production code.
package testfake

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/testing"
)

// GVRs the Tekton Action resources operate on. Mirrors the literals used in
// the production code (resource_tekton_action_kubernetes.go,
// resource_tekton_action_aws.go, tekton/resource_operations.go).
var (
	TaskGVR = schema.GroupVersionResource{
		Group:    "tekton.dev",
		Version:  "v1beta1",
		Resource: "tasks",
	}
	StepActionGVR = schema.GroupVersionResource{
		Group:    "tekton.dev",
		Version:  "v1beta1",
		Resource: "stepactions",
	}
)

// gvrToListKind maps the Tekton GVRs to their list kind. NewSimpleDynamicClient
// requires this for unstructured types not registered in any scheme.
var gvrToListKind = map[schema.GroupVersionResource]string{
	TaskGVR:       "TaskList",
	StepActionGVR: "StepActionList",
}

// NewClient returns a fake dynamic.Interface seeded with the given objects.
// Pass *unstructured.Unstructured values built via the fixtures in this
// package (Task, StepAction).
func NewClient(seed ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		gvrToListKind,
		seed...,
	)
}

// WithError installs a reactor on the fake client that returns `err` for the
// given verb against the given GVR. Verb is one of "get", "create", "update",
// "delete", "list", "patch", or "*" for all verbs. Resource matches by GVR
// resource name (e.g. "tasks", "stepactions", or "*").
//
// Reactors are evaluated in LIFO order, so calls to WithError installed later
// take precedence over earlier ones for overlapping (verb, resource) pairs.
func WithError(c *dynamicfake.FakeDynamicClient, verb string, gvr schema.GroupVersionResource, err error) {
	c.PrependReactor(verb, gvr.Resource, func(action testing.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
}

// WithErrorOnGVRAndName is like WithError but only fires when the action's
// target name matches. Useful for asymmetric-drift scenarios where one of
// (Task, StepAction) errors and the other doesn't, or where the same GVR
// is queried for different objects in the same test.
func WithErrorOnGVRAndName(c *dynamicfake.FakeDynamicClient, verb string, gvr schema.GroupVersionResource, name string, err error) {
	c.PrependReactor(verb, gvr.Resource, func(action testing.Action) (bool, runtime.Object, error) {
		actionName, ok := extractActionName(action)
		if !ok || actionName != name {
			// Fall through to default fake-client behavior.
			return false, nil, nil
		}
		return true, nil, err
	})
}

// extractActionName returns the target object name on an action and whether
// extraction succeeded. testing.GetAction and testing.DeleteAction share the
// same method set so we can't disambiguate them via type-switch; route by
// verb instead and cast to whichever interface carries name access.
func extractActionName(action testing.Action) (string, bool) {
	switch action.GetVerb() {
	case "get", "delete":
		// Both verbs use interfaces with GetName(); the GetAction interface is
		// structurally identical to DeleteAction so this cast covers both.
		if named, ok := action.(testing.GetAction); ok {
			return named.GetName(), true
		}
	case "create":
		if a, ok := action.(testing.CreateAction); ok {
			if obj := a.GetObject(); obj != nil {
				if accessor, ok := obj.(interface{ GetName() string }); ok {
					return accessor.GetName(), true
				}
			}
		}
	case "update":
		if a, ok := action.(testing.UpdateAction); ok {
			if obj := a.GetObject(); obj != nil {
				if accessor, ok := obj.(interface{ GetName() string }); ok {
					return accessor.GetName(), true
				}
			}
		}
	}
	return "", false
}
