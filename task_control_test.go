package main

import (
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"testing"
	"time"
)

func testAcceptRun(t *testing.T) *growthTaskRun {
	t.Helper()
	r := newTaskRun("local-task-control-test")
	r.Kind = "accept"
	t.Cleanup(func() { taskRuns.Delete(r.ID); taskRunsByAuth.Delete(r.AuthIndex) })
	return r
}
func TestAcceptRunCancellationIsCooperativeAndScoped(t *testing.T) {
	r := testAcceptRun(t)
	wrong := handleGrowthTaskCancel(pluginapi.ManagementRequest{Body: mustJSON(map[string]any{"run_id": r.ID, "auth_index": "wrong"})})
	if wrong["error"] == nil || taskRunCanceled(r) {
		t.Fatal("mismatched account canceled run")
	}
	got := handleGrowthTaskCancel(pluginapi.ManagementRequest{Body: mustJSON(map[string]any{"run_id": r.ID, "auth_index": r.AuthIndex})})
	if got["cancel_requested"] != true || got["status"] != "running" {
		t.Fatalf("request must not claim completion: %#v", got)
	}
	runAcceptAll(r.AuthIndex, nil, r) // nil auth proves cancellation happens before an upstream read.
	if snap := taskRunSnapshot(r.ID); snap["status"] != "canceled" {
		t.Fatalf("worker did not confirm cancellation: %#v", snap)
	}
}
func TestAcceptRunStatusSupportsLatestAccountAndExpiry(t *testing.T) {
	r := testAcceptRun(t)
	req := pluginapi.ManagementRequest{Query: map[string][]string{"auth_index": {r.AuthIndex}}}
	if got := handleGrowthTaskStatus(req); got["run_id"] != r.ID {
		t.Fatalf("latest lookup: %#v", got)
	}
	finishTaskRun(r, "succeeded", "")
	r.Mu.Lock()
	r.ExpiresAt = time.Now().Add(-time.Second)
	r.Mu.Unlock()
	if got := handleGrowthTaskStatus(req); got["status"] != "idle" {
		t.Fatalf("expired run must not be returned: %#v", got)
	}
}
func TestCancellationDoesNotRewriteCompletedOrUnsupportedRun(t *testing.T) {
	r := testAcceptRun(t)
	finishTaskRun(r, "succeeded", "")
	req := pluginapi.ManagementRequest{Body: mustJSON(map[string]any{"run_id": r.ID, "auth_index": r.AuthIndex})}
	if got := handleGrowthTaskCancel(req); got["status"] != "succeeded" || got["cancel_requested"] != false {
		t.Fatalf("completed state changed: %#v", got)
	}
	r.Mu.Lock()
	r.Kind = "legacy"
	r.Mu.Unlock()
	if got := handleGrowthTaskCancel(req); got["error"] == nil {
		t.Fatal("unsupported operation accepted cancellation")
	}
}
