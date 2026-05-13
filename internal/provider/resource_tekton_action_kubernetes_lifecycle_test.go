package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton/testfake"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Bug repros for Create (issue #10 / Bug #1), Update (issue #10 / Bug #4),
// and the asymmetric-drift Read case (issue #9 / Bug #2 narrow framing) on
// the Kubernetes Tekton Action resource. Each test asserts the POST-FIX
// behavior so it FAILS on `main` and PASSES once the corresponding fix lands.

// diagsContainKeyword reports whether any diagnostic in diags has a Summary
// or Detail that contains the given keyword (case-insensitive). Used by the
// Bug #4 divergence assertion to require explicit divergence-keyword
// surfacing in post-fix diagnostics.
func diagsContainKeyword(diags diag.Diagnostics, keyword string) bool {
	needle := strings.ToLower(keyword)
	for _, d := range diags {
		hay := strings.ToLower(d.Summary() + " " + d.Detail())
		if strings.Contains(hay, needle) {
			return true
		}
	}
	return false
}

const (
	k8sTaskName       = "k8s-task-1"
	k8sStepActionName = "setup-credentials-k8s-1"
)

// --- Bug #1: Create orphans StepAction on Task fail ---------------------

// TestK8sCreate_TaskFails_RollsBackStepAction asserts the POST-FIX behavior
// for Bug #1 / issue #10: when Task create fails after StepAction create
// succeeded, the StepAction MUST be rolled back (deleted) before returning,
// and the surfaced diagnostic must still report the original failure.
//
// FAILS on `main` because today the StepAction is left as an orphan.
// Passes once issue #10 fix lands.
func TestK8sCreate_TaskFails_RollsBackStepAction(t *testing.T) {
	r, c := resourceWithFake() // empty cluster
	testfake.WithError(c, "create", testfake.TaskGVR, testfake.ErrInternalServer("etcd unavailable"))

	stepAction := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, nil)
	task := testfake.Task(k8sReadTestNamespace, k8sTaskName, nil)
	ops := tekton.NewResourceOperations(c)

	diags := r.createResources(context.Background(), ops, stepAction, task)
	if !diags.HasError() {
		t.Fatal("expected Task-create error to surface in diagnostics")
	}

	// Post-fix: StepAction must be rolled back (NotFound).
	_, err := c.Resource(testfake.StepActionGVR).Namespace(k8sReadTestNamespace).Get(context.Background(), k8sStepActionName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected StepAction rolled back (NotFound) after Task-create failed (post-fix), got err=%v", err)
	}

	// Task is absent — its create failed, never landed.
	_, err = c.Resource(testfake.TaskGVR).Namespace(k8sReadTestNamespace).Get(context.Background(), k8sTaskName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected Task to be absent (its create failed), got err=%v", err)
	}
}

// TestK8sCreate_StepActionFails_NoOrphan is the control test: when StepAction
// create itself fails, neither object lands in cluster — this should remain
// true after the fix.
func TestK8sCreate_StepActionFails_NoOrphan(t *testing.T) {
	r, c := resourceWithFake()
	testfake.WithError(c, "create", testfake.StepActionGVR, testfake.ErrInternalServer("etcd unavailable"))

	stepAction := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, nil)
	task := testfake.Task(k8sReadTestNamespace, k8sTaskName, nil)
	ops := tekton.NewResourceOperations(c)

	diags := r.createResources(context.Background(), ops, stepAction, task)
	if !diags.HasError() {
		t.Fatal("expected StepAction-create error to surface")
	}

	if _, err := c.Resource(testfake.StepActionGVR).Namespace(k8sReadTestNamespace).Get(context.Background(), k8sStepActionName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected StepAction absent (its create failed), got err=%v", err)
	}
	if _, err := c.Resource(testfake.TaskGVR).Namespace(k8sReadTestNamespace).Get(context.Background(), k8sTaskName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected Task absent (never attempted), got err=%v", err)
	}
}

// --- Bug #4: Update divergence on Task fail -----------------------------

// TestK8sUpdate_TaskFails_SurfacesDivergenceDiagnostic asserts the POST-FIX
// behavior for Bug #4 / issue #10. When Task update fails after StepAction
// update succeeded, the surfaced diagnostics must explicitly mention the
// divergence (e.g. via keywords like "divergent", "manual", or "cluster
// state") — not just the underlying Task-update error.
//
// FAILS on `main` because today the only diagnostic is a generic
// "Error updating Task" message with no divergence keyword. Passes once
// issue #10 fix enriches the diagnostic (or adds a second one).
func TestK8sUpdate_TaskFails_SurfacesDivergenceDiagnostic(t *testing.T) {
	// Seed cluster with old objects (label v: "1").
	oldTask := testfake.Task(k8sReadTestNamespace, k8sTaskName, map[string]string{"v": "1"})
	oldSA := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, map[string]string{"v": "1"})
	r, c := resourceWithFake(oldTask, oldSA)

	// Inject error on Task update only — StepAction update will succeed.
	testfake.WithError(c, "update", testfake.TaskGVR, testfake.ErrInternalServer("etcd unavailable"))

	// New objects with label v: "2".
	newSA := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, map[string]string{"v": "2"})
	newTask := testfake.Task(k8sReadTestNamespace, k8sTaskName, map[string]string{"v": "2"})
	ops := tekton.NewResourceOperations(c)

	diags := r.updateResources(context.Background(), ops, newSA, newTask)
	if !diags.HasError() {
		t.Fatal("expected Task-update error to surface")
	}

	// Post-fix: at least one diagnostic must explicitly call out the
	// divergence (keyword check across all diagnostic summaries+details).
	hasDivergenceKeyword := diagsContainKeyword(diags, "divergent") ||
		diagsContainKeyword(diags, "divergence") ||
		diagsContainKeyword(diags, "manual") ||
		diagsContainKeyword(diags, "cluster state")
	if !hasDivergenceKeyword {
		t.Errorf("expected diagnostics to explicitly surface divergence (keywords: divergent/divergence/manual/cluster state); got diags=%+v", diags)
	}
}

// --- Bug #2: Read silently misses asymmetric drift ----------------------

// TestK8sReadResourceState_StepActionMissing_AsymmetricDriftSurfaced asserts
// the POST-FIX behavior for Bug #2 narrow framing (issue #9 / RCA §9.4).
// When Task is healthy but the paired StepAction is missing, Read must
// retain state (removeFromState=false) and surface a diagnostic — error or
// warning — telling the operator about the asymmetric drift.
//
// FAILS on `main` because today readResourceState only checks Task and
// silently masks the asymmetry. Passes once issue #9 fix lands.
func TestK8sReadResourceState_StepActionMissing_AsymmetricDriftSurfaced(t *testing.T) {
	// Only Task seeded — StepAction is intentionally missing.
	task := testfake.Task(k8sReadTestNamespace, k8sReadTestTaskName, nil)
	r, c := resourceWithFake(task)

	state := stateForRead(k8sReadTestNamespace, k8sReadTestTaskName)
	remove, diags := r.readResourceState(context.Background(), c, state)

	if remove {
		t.Errorf("expected state retained on asymmetric drift (post-fix), got removeFromState=true")
	}
	if !diags.HasError() && diags.WarningsCount() == 0 {
		t.Errorf("expected a diagnostic (error or warning) surfacing asymmetric drift (post-fix); got none")
	}
}
