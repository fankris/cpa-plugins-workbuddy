package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The batch check-in endpoint is what the panel's "全部签到" button calls. Its
// summary field names are a panel contract (panel.html checkinAll reads
// success/already/fail/skipped_global/eligible), so they must not drift.

// Mixed fleet: one CN account needing a check-in, one already done, one Global.
func TestManualCheckinBatchSummary(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-new", "workbuddy-CN-new.json", "new", regionCN, false)
	store.put("idx-done", "workbuddy-CN-done.json", "done", regionCN, false)
	store.put("idx-gl", "workbuddy-Global-gl.json", "gl", regionGlobal, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	// Per-request counter: the first CN status call reports not-checked-in,
	// the second reports checked-in (the batch runs concurrently, so exactly
	// one of the two CN accounts ends up "already").
	var statusCalls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkin-activity-status"), strings.Contains(r.URL.Path, "checkin-status"):
			mu.Lock()
			statusCalls++
			checkedIn := statusCalls > 1
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"active":true,"today_checked_in":` + boolStr(checkedIn) + `}}`))
		case strings.Contains(r.URL.Path, "daily-checkin"):
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"success":true}}`))
		case strings.Contains(r.URL.Path, "user-resource"):
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"packages":[{"remain":10,"used":1,"size":11}]}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	restore := setBillingBase(srv.URL)
	defer restore()

	_, out := managementCall(t, http.MethodPost, "/checkin", `{}`)
	summary, _ := out["summary"].(map[string]any)
	if summary == nil {
		t.Fatalf("check-in must return a summary, got %+v", out)
	}
	for _, key := range []string{"total", "eligible", "success", "already", "skipped_global", "fail", "attempted"} {
		if _, ok := summary[key]; !ok {
			t.Errorf("summary is missing the %q counter (panel contract)", key)
		}
	}
	if total, _ := summary["total"].(float64); int(total) != 3 {
		t.Errorf("summary total = %v, want 3", summary["total"])
	}
	if globalN, _ := summary["skipped_global"].(float64); int(globalN) != 1 {
		t.Errorf("the Global account must be counted as skipped_global, got %v", summary["skipped_global"])
	}
	results, _ := out["results"].([]any)
	if len(results) != 3 {
		t.Errorf("results length = %d, want 3 (one per account)", len(results))
	}
}

// A single-account request must only touch that account.
func TestManualCheckinSingleAccount(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-a", "workbuddy-CN-a.json", "a", regionCN, false)
	store.put("idx-b", "workbuddy-CN-b.json", "b", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	srv, _ := checkinStub(t, struct {
		active          bool
		todayCheckedIn  bool
		checkinResponse string
	}{active: true})
	restore := setBillingBase(srv.URL)
	defer restore()

	_, out := managementCall(t, http.MethodPost, "/checkin", `{"auth_index":"idx-a"}`)
	results, _ := out["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("a single-account check-in must return exactly one result, got %d", len(results))
	}
	first, _ := results[0].(map[string]any)
	if first["auth_index"] != "idx-a" {
		t.Fatalf("wrong account checked in: %+v", first)
	}
}

// An unknown auth_index must be a clean error, not a silent success.
func TestManualCheckinUnknownAccount(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)
	resetCheckinState()

	_, out := managementCall(t, http.MethodPost, "/checkin", `{"auth_index":"nope"}`)
	if out["error"] == nil {
		t.Fatalf("an unknown account must produce an error, got %+v", out)
	}
}

// The scheduled tick must run without a host auth list (empty fleet = no-op)
// and must respect the automation switch.
func TestRunAutoCheckinRespectsSwitches(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)
	resetCheckinState()

	oldCheckin := checkinAuto
	oldLifecycle := lifecycleAuto
	t.Cleanup(func() {
		checkinAutoMu.Lock()
		checkinAuto = oldCheckin
		checkinAutoMu.Unlock()
		lifecycleAutoMu.Lock()
		lifecycleAuto = oldLifecycle
		lifecycleAutoMu.Unlock()
	})

	// Both off → the tick returns immediately without touching the host.
	checkinAutoMu.Lock()
	checkinAuto = false
	checkinAutoMu.Unlock()
	lifecycleAutoMu.Lock()
	lifecycleAuto = false
	lifecycleAutoMu.Unlock()
	runAutoCheckin() // must not panic or block

	// Check-in off but lifecycle on → the tick still runs (credit gate).
	lifecycleAutoMu.Lock()
	lifecycleAuto = true
	lifecycleAutoMu.Unlock()
	runAutoCheckin()
}
