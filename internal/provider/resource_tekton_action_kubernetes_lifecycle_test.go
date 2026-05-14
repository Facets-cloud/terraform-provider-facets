package provider

import (
	"context"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton/testfake"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Bug repros for Create (issue #10 / Bug #1), Update (issue #10 / Bug #4),
// and the asymmetric-drift Read case (issue #9 / Bug #2 narrow framing) on
// the Kubernetes Tekton Action resource. Each test asserts the POST-FIX
// behavior so it FAILS on `main` and PASSES once the corresponding fix lands.

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

// TestK8sUpdate_TaskFails_StepActionUntouched asserts the POST-FIX
// behavior for Update ordering (Unni's review, PR #12 Critique 2).
// The fix reorders updateResources to update Task FIRST, then
// StepAction. When Task update fails, StepAction is never touched —
// the cluster remains in a coherent pre-Update state and no field-level
// divergence is introduced.
//
// FAILS on `main` (current order is SA-first → Task, so SA is updated
// to v=2 before Task-update-fail is detected). PASSES after the
// Task-first reorder fix lands.
func TestK8sUpdate_TaskFails_StepActionUntouched(t *testing.T) {
	// Seed both objects with v=1.
	oldTask := testfake.Task(k8sReadTestNamespace, k8sTaskName, map[string]string{"v": "1"})
	oldSA := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, map[string]string{"v": "1"})
	r, c := resourceWithFake(oldTask, oldSA)

	// Inject Task-update failure only — StepAction update would succeed
	// if reached, which is exactly the bug.
	testfake.WithError(c, "update", testfake.TaskGVR, testfake.ErrInternalServer("etcd unavailable"))

	// Plan would have these as v=2.
	newSA := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, map[string]string{"v": "2"})
	newTask := testfake.Task(k8sReadTestNamespace, k8sTaskName, map[string]string{"v": "2"})
	ops := tekton.NewResourceOperations(c)

	diags := r.updateResources(context.Background(), ops, newSA, newTask)
	if !diags.HasError() {
		t.Fatal("expected Task-update error to surface")
	}

	// Post-fix invariant: StepAction must be at OLD spec (v=1).
	saInCluster, err := c.Resource(testfake.StepActionGVR).Namespace(k8sReadTestNamespace).Get(context.Background(), k8sStepActionName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("StepAction missing from cluster: %v", err)
	}
	if got := saInCluster.GetLabels()["v"]; got != "1" {
		t.Errorf("BUG: StepAction was updated to v=%q before Task-update-fail. "+
			"Post-fix invariant requires StepAction at v=1 (untouched). "+
			"FAILS on main until the Task-first reorder fix lands.", got)
	}
}

// TestK8sCreate_TaskFailsAndRollbackFails_BothDiagnosticsSurface locks
// the rollback-failure diagnostic contract. When the StepAction rollback
// itself fails (Forbidden, persistent 5xx, etc.) after a Task-create
// failure, BOTH diagnostics must surface so the operator sees the
// original cause AND knows manual cleanup may be required.
//
// Passes today (fix #10 produces both diagnostics). The cluster still
// has the orphan StepAction; that pathological case is what the
// follow-up IsAlreadyExists-adopt fix (Unni Critique 1) will address.
func TestK8sCreate_TaskFailsAndRollbackFails_BothDiagnosticsSurface(t *testing.T) {
	r, c := resourceWithFake()
	// Task create fails.
	testfake.WithError(c, "create", testfake.TaskGVR, testfake.ErrInternalServer("etcd unavailable"))
	// AND the rollback Delete also fails.
	testfake.WithError(c, "delete", testfake.StepActionGVR, testfake.ErrForbidden(testfake.StepActionGVR, k8sStepActionName))

	stepAction := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, nil)
	task := testfake.Task(k8sReadTestNamespace, k8sTaskName, nil)
	ops := tekton.NewResourceOperations(c)

	diags := r.createResources(context.Background(), ops, stepAction, task)

	if !diags.HasError() {
		t.Fatal("expected Task-create error to surface")
	}
	if diags.WarningsCount() == 0 {
		t.Errorf("expected a warning surfacing the rollback failure; got none")
	}

	// Document the known orphan: StepAction is still in cluster because
	// both the original create succeeded AND the rollback delete failed.
	// The follow-up IsAlreadyExists-adopt fix will address this on the
	// next apply by adopting via Update.
	if _, err := c.Resource(testfake.StepActionGVR).Namespace(k8sReadTestNamespace).Get(context.Background(), k8sStepActionName, metav1.GetOptions{}); err != nil {
		t.Errorf("expected orphan StepAction (rollback failed), got err=%v. "+
			"If this test fails, the rollback-failure path may have changed.", err)
	}
}

// TestK8sDelete_TaskFails_StepActionStillAttempted locks the post-fix
// behavior for Delete orchestration. When Task delete fails (Forbidden,
// 5xx, etc.), the StepAction delete MUST still be attempted — the helper
// uses best-effort + aggregated diagnostics. Combined with the idempotent
// DeleteResource (NotFound -> nil), this means destroy retries don't leave
// orphans.
//
// PASSES today on this branch after the deleteResources helper is in place.
// FAILS on main because the lifecycle Delete early-returns on Task-fail.
func TestK8sDelete_TaskFails_StepActionStillAttempted(t *testing.T) {
	task := testfake.Task(k8sReadTestNamespace, k8sTaskName, nil)
	sa := testfake.StepAction(k8sReadTestNamespace, k8sStepActionName, nil)
	r, c := resourceWithFake(task, sa)

	// Inject Forbidden on Task delete only — StepAction delete is left
	// free to proceed.
	testfake.WithError(c, "delete", testfake.TaskGVR, testfake.ErrForbidden(testfake.TaskGVR, k8sTaskName))

	ops := tekton.NewResourceOperations(c)
	diags := r.deleteResources(context.Background(), ops, k8sReadTestNamespace, k8sTaskName, k8sStepActionName)

	if !diags.HasError() {
		t.Fatal("expected Task-delete Forbidden error to surface")
	}

	// Task must STILL be in cluster (its delete failed).
	if _, err := c.Resource(testfake.TaskGVR).Namespace(k8sReadTestNamespace).Get(context.Background(), k8sTaskName, metav1.GetOptions{}); err != nil {
		t.Errorf("expected Task still in cluster (its delete failed), got err=%v", err)
	}

	// StepAction must be GONE (its delete was still attempted and succeeded).
	if _, err := c.Resource(testfake.StepActionGVR).Namespace(k8sReadTestNamespace).Get(context.Background(), k8sStepActionName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected StepAction deleted (best-effort), got err=%v", err)
	}
}
