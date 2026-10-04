package main

import (
	"net/http"
	"testing"
	"time"
)

// The keepalive runner is the batch face of refreshOneAuth: it walks every
// account, records a per-account row, and stores a summary for the status
// endpoint. Its rows are a panel contract.

// A full run must produce one row per account and record the summary.
func TestRunTokenKeepaliveProducesRowsAndSummary(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-1", "workbuddy-CN-1.json", "u1", regionCN, false)
	store.put("idx-2", "workbuddy-CN-2.json", "u2", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	stub := &keepaliveStub{body: `{"code":0,"msg":"OK","data":{"accessToken":"rotated","expires_in":3600}}`}
	installKeepaliveStub(t, stub)

	oldKeepalive := keepaliveAuto
	keepaliveAutoMu.Lock()
	keepaliveAuto = true
	keepaliveAutoMu.Unlock()
	t.Cleanup(func() {
		keepaliveAutoMu.Lock()
		keepaliveAuto = oldKeepalive
		keepaliveAutoMu.Unlock()
	})

	sum := runTokenKeepalive()
	if len(sum.Results) != 2 {
		t.Fatalf("expected one row per account, got %d (%+v)", len(sum.Results), sum.Results)
	}
	for _, row := range sum.Results {
		if row.Status != "refreshed" {
			t.Errorf("row %s status = %q, want refreshed", row.AuthIndex, row.Status)
		}
		if row.Region == "" {
			t.Errorf("row %s is missing its region label", row.AuthIndex)
		}
	}
	// The summary must be retrievable for the status endpoint.
	if got := getLastKeepalive(); got == nil || len(got.Results) != 2 {
		t.Fatalf("summary not recorded: %+v", got)
	}
}

// When the automation switch is off the scheduled run must do nothing.
func TestRunTokenKeepaliveRespectsSwitch(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-off", "workbuddy-CN-off.json", "off", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	oldKeepalive := keepaliveAuto
	keepaliveAutoMu.Lock()
	keepaliveAuto = false
	keepaliveAutoMu.Unlock()
	t.Cleanup(func() {
		keepaliveAutoMu.Lock()
		keepaliveAuto = oldKeepalive
		keepaliveAutoMu.Unlock()
	})

	stub := &keepaliveStub{body: `{"code":0,"msg":"OK","data":{"accessToken":"x"}}`}
	installKeepaliveStub(t, stub)

	sum := runTokenKeepalive()
	if len(sum.Results) != 0 {
		t.Fatalf("a disabled keepalive must not refresh anything, got %+v", sum.Results)
	}
	if len(store.savedRecords()) != 0 {
		t.Fatal("a disabled keepalive must not write credentials")
	}
}

// The manual endpoint must refresh one account when asked, and ignore the
// automation switch (manual means manual).
func TestHandleKeepaliveNowSingleAccount(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-manual", "workbuddy-CN-manual.json", "m", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	oldKeepalive := keepaliveAuto
	keepaliveAutoMu.Lock()
	keepaliveAuto = false // the switch is off; manual must still work
	keepaliveAutoMu.Unlock()
	t.Cleanup(func() {
		keepaliveAutoMu.Lock()
		keepaliveAuto = oldKeepalive
		keepaliveAutoMu.Unlock()
	})

	stub := &keepaliveStub{body: `{"code":0,"msg":"OK","data":{"accessToken":"manual-rotated","expires_in":3600}}`}
	installKeepaliveStub(t, stub)

	_, out := managementCall(t, http.MethodPost, "/keepalive", `{"auth_index":"idx-manual"}`)
	results, _ := out["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("manual keepalive should return one row, got %+v", out)
	}
	row, _ := results[0].(map[string]any)
	if row["status"] != "refreshed" {
		t.Fatalf("manual refresh status = %v, want refreshed", row["status"])
	}
	if len(store.savedRecords()) == 0 {
		t.Fatal("a manual refresh must persist the rotated token")
	}
}

// An unknown account must be a clean error.
func TestHandleKeepaliveNowUnknownAccount(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)
	resetCheckinState()

	_, out := managementCall(t, http.MethodPost, "/keepalive", `{"auth_index":"nope"}`)
	if out["error"] == nil {
		t.Fatalf("an unknown account must produce an error, got %+v", out)
	}
}

// The status endpoint must expose the schedule and the last run.
func TestHandleKeepaliveStatusShape(t *testing.T) {
	status, out := managementCall(t, http.MethodGet, "/keepalive/status", "")
	if status != http.StatusOK {
		t.Fatalf("status endpoint returned %d", status)
	}
	for _, key := range []string{"enabled", "schedule", "last_run"} {
		if _, ok := out[key]; !ok {
			t.Errorf("keepalive status is missing %q", key)
		}
	}
	schedule, _ := out["schedule"].([]any)
	if len(schedule) == 0 {
		t.Error("the schedule must name at least one hour")
	}
}

// The keepalive window helper must accept only the hour after its schedule.
func TestShouldRunKeepaliveNow(t *testing.T) {
	for _, hour := range keepaliveHours {
		at := time.Date(2026, 9, 19, hour, 5, 0, 0, time.Local)
		if !shouldRunKeepaliveNow(at) {
			t.Errorf("hour %02d: should fire at the scheduled hour", hour)
		}
		// One minute before the hour is outside the window.
		before := time.Date(2026, 9, 19, hour, 0, 0, 0, time.Local).Add(-time.Minute)
		if shouldRunKeepaliveNow(before) {
			t.Errorf("hour %02d: must not fire before the scheduled hour", hour)
		}
		// Two hours later is outside the window.
		after := time.Date(2026, 9, 19, hour, 0, 0, 0, time.Local).Add(2 * time.Hour)
		if shouldRunKeepaliveNow(after) {
			t.Errorf("hour %02d: must not fire two hours late", hour)
		}
	}
}

// nextKeepaliveTime must always return a future time.
func TestNextKeepaliveTimeIsInTheFuture(t *testing.T) {
	now := time.Now()
	next := nextKeepaliveTime(now)
	if !next.After(now) {
		t.Fatalf("next keepalive %v is not after %v", next, now)
	}
	// And it must fall on a scheduled hour.
	found := false
	for _, h := range keepaliveHours {
		if next.Hour() == h && next.Minute() == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("next keepalive %v is not on a scheduled hour %v", next, keepaliveHours)
	}
}
