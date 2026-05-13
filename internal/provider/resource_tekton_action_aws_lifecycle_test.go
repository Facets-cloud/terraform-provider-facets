package provider

import (
	"context"
	"testing"

	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton"
	"github.com/facets-cloud/terraform-provider-facets/internal/provider/tekton/testfake"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AWS variant parity for the lifecycle bug repros: Create orphan (#1),
// Update divergence (#4), Read asymmetric drift (#2).

const (
	awsTaskName       = "aws-task-1"
	awsStepActionName = "setup-credentials-aws-1"
)

// --- Bug #1 (AWS): Create orphans StepAction on Task fail ---------------

// TestAWSCreate_TaskFails_RollsBackStepAction asserts the POST-FIX behavior
// for Bug #1 / issue #10 on the AWS variant. FAILS on `main`; passes after
// issue #10 fix lands.
func TestAWSCreate_TaskFails_RollsBackStepAction(t *testing.T) {
	r, c := awsResourceWithFake()
	testfake.WithError(c, "create", testfake.TaskGVR, testfake.ErrInternalServer("etcd unavailable"))

	stepAction := testfake.StepAction(tektonPipelinesNamespace, awsStepActionName, nil)
	task := testfake.Task(tektonPipelinesNamespace, awsTaskName, nil)
	ops := tekton.NewResourceOperations(c)

	diags := r.createResources(context.Background(), ops, stepAction, task)
	if !diags.HasError() {
		t.Fatal("expected Task-create error to surface")
	}

	if _, err := c.Resource(testfake.StepActionGVR).Namespace(tektonPipelinesNamespace).Get(context.Background(), awsStepActionName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected StepAction rolled back (NotFound) after Task-create failed (post-fix), got err=%v", err)
	}
	if _, err := c.Resource(testfake.TaskGVR).Namespace(tektonPipelinesNamespace).Get(context.Background(), awsTaskName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected Task absent, got err=%v", err)
	}
}

// --- Bug #4 (AWS): Update divergence on Task fail -----------------------

// --- Bug #2 (AWS): Read silently misses asymmetric drift ----------------

// TestAWSReadResourceState_StepActionMissing_AsymmetricDriftSurfaced asserts
// the POST-FIX behavior for Bug #2 narrow framing on the AWS variant. State
// is retained, a diagnostic (error or warning) surfaces. FAILS on `main`;
// passes once issue #9 fix lands.
func TestAWSReadResourceState_StepActionMissing_AsymmetricDriftSurfaced(t *testing.T) {
	task := testfake.Task(tektonPipelinesNamespace, awsReadTestTaskName, nil)
	r, c := awsResourceWithFake(task)

	state := awsStateForRead(awsReadTestTaskName)
	remove, diags := r.readResourceState(context.Background(), c, state)

	if remove {
		t.Errorf("expected state retained on asymmetric drift (post-fix), got removeFromState=true")
	}
	if !diags.HasError() && diags.WarningsCount() == 0 {
		t.Errorf("expected a diagnostic (error or warning) surfacing asymmetric drift (post-fix); got none")
	}
}

// TestAWSUpdate_TaskFails_StepActionUntouched asserts the POST-FIX
// behavior for Update ordering (Unni's review, PR #12 Critique 2) on
// the AWS variant. The fix reorders updateResources to update Task FIRST,
// then StepAction. When Task update fails, StepAction is never touched —
// the cluster remains in a coherent pre-Update state.
//
// FAILS on `main` (current order is SA-first → Task, so SA is updated
// to v=2 before Task-update-fail is detected). PASSES after the
// Task-first reorder fix lands.
func TestAWSUpdate_TaskFails_StepActionUntouched(t *testing.T) {
	// Seed both objects with v=1.
	oldTask := testfake.Task(tektonPipelinesNamespace, awsTaskName, map[string]string{"v": "1"})
	oldSA := testfake.StepAction(tektonPipelinesNamespace, awsStepActionName, map[string]string{"v": "1"})
	r, c := awsResourceWithFake(oldTask, oldSA)

	// Inject Task-update failure only — StepAction update would succeed
	// if reached, which is exactly the bug.
	testfake.WithError(c, "update", testfake.TaskGVR, testfake.ErrInternalServer("etcd unavailable"))

	// Plan would have these as v=2.
	newSA := testfake.StepAction(tektonPipelinesNamespace, awsStepActionName, map[string]string{"v": "2"})
	newTask := testfake.Task(tektonPipelinesNamespace, awsTaskName, map[string]string{"v": "2"})
	ops := tekton.NewResourceOperations(c)

	diags := r.updateResources(context.Background(), ops, newSA, newTask)
	if !diags.HasError() {
		t.Fatal("expected Task-update error to surface")
	}

	// Post-fix invariant: StepAction must be at OLD spec (v=1).
	saInCluster, err := c.Resource(testfake.StepActionGVR).Namespace(tektonPipelinesNamespace).Get(context.Background(), awsStepActionName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("StepAction missing from cluster: %v", err)
	}
	if got := saInCluster.GetLabels()["v"]; got != "1" {
		t.Errorf("BUG: StepAction was updated to v=%q before Task-update-fail. "+
			"Post-fix invariant requires StepAction at v=1 (untouched). "+
			"FAILS on main until the Task-first reorder fix lands.", got)
	}
}

// TestAWSDelete_TaskFails_StepActionStillAttempted locks the post-fix
// behavior for Delete orchestration on the AWS variant. When Task delete
// fails (Forbidden, 5xx, etc.), the StepAction delete MUST still be
// attempted — the helper uses best-effort + aggregated diagnostics. Combined
// with the idempotent DeleteResource (NotFound -> nil), destroy retries
// don't leave orphans.
//
// PASSES today on this branch after the deleteResources helper is in place.
// FAILS on main because the lifecycle Delete early-returns on Task-fail.
func TestAWSDelete_TaskFails_StepActionStillAttempted(t *testing.T) {
	task := testfake.Task(tektonPipelinesNamespace, awsTaskName, nil)
	sa := testfake.StepAction(tektonPipelinesNamespace, awsStepActionName, nil)
	r, c := awsResourceWithFake(task, sa)

	// Inject Forbidden on Task delete only — StepAction delete is left
	// free to proceed.
	testfake.WithError(c, "delete", testfake.TaskGVR, testfake.ErrForbidden(testfake.TaskGVR, awsTaskName))

	ops := tekton.NewResourceOperations(c)
	diags := r.deleteResources(context.Background(), ops, tektonPipelinesNamespace, awsTaskName, awsStepActionName)

	if !diags.HasError() {
		t.Fatal("expected Task-delete Forbidden error to surface")
	}

	// Task must STILL be in cluster (its delete failed).
	if _, err := c.Resource(testfake.TaskGVR).Namespace(tektonPipelinesNamespace).Get(context.Background(), awsTaskName, metav1.GetOptions{}); err != nil {
		t.Errorf("expected Task still in cluster (its delete failed), got err=%v", err)
	}

	// StepAction must be GONE (its delete was still attempted and succeeded).
	if _, err := c.Resource(testfake.StepActionGVR).Namespace(tektonPipelinesNamespace).Get(context.Background(), awsStepActionName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected StepAction deleted (best-effort), got err=%v", err)
	}
}
