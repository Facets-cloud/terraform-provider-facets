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

// TestAWSUpdate_TaskFails_SurfacesDivergenceDiagnostic asserts the POST-FIX
// behavior for Bug #4 / issue #10 on the AWS variant: when Task update
// fails after StepAction update succeeded, a diagnostic must explicitly
// surface the divergence. Uses diagsContainKeyword (defined in the K8s
// lifecycle test file — same package).
//
// FAILS on `main`; passes once issue #10 fix lands.
func TestAWSUpdate_TaskFails_SurfacesDivergenceDiagnostic(t *testing.T) {
	oldTask := testfake.Task(tektonPipelinesNamespace, awsTaskName, map[string]string{"v": "1"})
	oldSA := testfake.StepAction(tektonPipelinesNamespace, awsStepActionName, map[string]string{"v": "1"})
	r, c := awsResourceWithFake(oldTask, oldSA)

	testfake.WithError(c, "update", testfake.TaskGVR, testfake.ErrInternalServer("etcd unavailable"))

	newSA := testfake.StepAction(tektonPipelinesNamespace, awsStepActionName, map[string]string{"v": "2"})
	newTask := testfake.Task(tektonPipelinesNamespace, awsTaskName, map[string]string{"v": "2"})
	ops := tekton.NewResourceOperations(c)

	diags := r.updateResources(context.Background(), ops, newSA, newTask)
	if !diags.HasError() {
		t.Fatal("expected Task-update error to surface")
	}

	hasDivergenceKeyword := diagsContainKeyword(diags, "divergent") ||
		diagsContainKeyword(diags, "divergence") ||
		diagsContainKeyword(diags, "manual") ||
		diagsContainKeyword(diags, "cluster state")
	if !hasDivergenceKeyword {
		t.Errorf("expected diagnostics to explicitly surface divergence (keywords: divergent/divergence/manual/cluster state); got diags=%+v", diags)
	}
}

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
