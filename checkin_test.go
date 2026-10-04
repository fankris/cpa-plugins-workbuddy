package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// checkinStub serves the check-in flow endpoints and counts the check-in POSTs
// so tests can prove the daily call happens at most once per account.
func checkinStub(t *testing.T, opts struct {
	active          bool
	todayCheckedIn  bool
	checkinResponse string
}) (*httptest.Server, *int32) {
	t.Helper()
	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "checkin-activity-status"):
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"active":` + boolStr(opts.active) +
				`,"today_checked_in":` + boolStr(opts.todayCheckedIn) + `}}`))
		case strings.Contains(r.URL.Path, "checkin-status"):
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"active":` + boolStr(opts.active) +
				`,"today_checked_in":` + boolStr(opts.todayCheckedIn) + `}}`))
		case strings.Contains(r.URL.Path, "daily-checkin"):
			atomic.AddInt32(&posts, 1)
			body := opts.checkinResponse
			if body == "" {
				body = `{"code":0,"msg":"OK","data":{"success":true}}`
			}
			_, _ = w.Write([]byte(body))
		case strings.Contains(r.URL.Path, "user-resource"):
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"packages":[{"remain":10,"used":1,"size":11,"name":"Free"}]}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &posts
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// resetCheckinState clears the per-account locks and cache between tests.
func resetCheckinState() {
	checkinLocks = sync.Map{}
	resetAccountCache()
}

// A Global account must never be checked in (it uses one-shot trial claims).
func TestCheckinSkipsGlobalAccounts(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-gl", "workbuddy-Global-u1.json", "u1", regionGlobal, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	srv, posts := checkinStub(t, struct {
		active          bool
		todayCheckedIn  bool
		checkinResponse string
	}{active: true})
	restore := setBillingBase(srv.URL)
	defer restore()

	out := checkinOneAccount(pluginapi.HostAuthFileEntry{AuthIndex: "idx-gl", ID: "workbuddy-Global-u1.json"})
	if out["success"] != false || out["skipped"] != true || out["reason"] != "global" {
		t.Fatalf("Global account must be skipped, got %+v", out)
	}
	if got := atomic.LoadInt32(posts); got != 0 {
		t.Fatalf("Global account must not POST a check-in, got %d calls", got)
	}
}

// Already-checked-in accounts must short-circuit without a second POST.
func TestCheckinSkipsWhenAlreadyCheckedInToday(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-cn", "workbuddy-CN-u2.json", "u2", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	srv, posts := checkinStub(t, struct {
		active          bool
		todayCheckedIn  bool
		checkinResponse string
	}{active: true, todayCheckedIn: true})
	restore := setBillingBase(srv.URL)
	defer restore()

	out := checkinOneAccount(pluginapi.HostAuthFileEntry{AuthIndex: "idx-cn", ID: "workbuddy-CN-u2.json"})
	if out["success"] != true || out["skipped"] != true || out["reason"] != "already" {
		t.Fatalf("already-checked-in must be a clean skip, got %+v", out)
	}
	if got := atomic.LoadInt32(posts); got != 0 {
		t.Fatalf("already checked in must not POST again, got %d calls", got)
	}
}

// An account without a check-in plan must be skipped cleanly — the raw
// upstream business error ("没有签到计划") must never reach the panel.
func TestCheckinSkipsInactivePlanWithFriendlyMessage(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-cn", "workbuddy-CN-u3.json", "u3", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	srv, posts := checkinStub(t, struct {
		active          bool
		todayCheckedIn  bool
		checkinResponse string
	}{active: false})
	restore := setBillingBase(srv.URL)
	defer restore()

	out := checkinOneAccount(pluginapi.HostAuthFileEntry{AuthIndex: "idx-cn", ID: "workbuddy-CN-u3.json"})
	if out["skipped"] != true || out["reason"] != "inactive" {
		t.Fatalf("inactive plan must be a clean skip, got %+v", out)
	}
	msg, _ := out["message"].(string)
	if !strings.Contains(msg, "签到计划") {
		t.Fatalf("skip message should explain the missing plan, got %q", msg)
	}
	if got := atomic.LoadInt32(posts); got != 0 {
		t.Fatalf("inactive plan must not POST a check-in, got %d calls", got)
	}
}

// The happy path performs exactly one check-in and merges (never wipes) the
// cached credits/plan that the panel and lifecycle depend on.
func TestCheckinPerformsOneCallAndMergesCache(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-cn", "workbuddy-CN-u4.json", "u4", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	srv, posts := checkinStub(t, struct {
		active          bool
		todayCheckedIn  bool
		checkinResponse string
	}{active: true})
	restore := setBillingBase(srv.URL)
	defer restore()

	// Pre-existing cache entry whose credits/plan must survive the merge.
	accountCache.Store("workbuddy-CN-u4.json", &accountCacheEntry{
		plan:    "Free",
		credits: &creditsSummary{TotalRemain: 3, TotalUsed: 1, TotalSize: 4},
		fetched: time.Now(),
	})

	out := checkinOneAccount(pluginapi.HostAuthFileEntry{AuthIndex: "idx-cn", ID: "workbuddy-CN-u4.json"})
	if out["success"] != true {
		t.Fatalf("check-in should succeed, got %+v", out)
	}
	if got := atomic.LoadInt32(posts); got != 1 {
		t.Fatalf("exactly one check-in POST expected, got %d", got)
	}
	v, ok := accountCache.Load("workbuddy-CN-u4.json")
	if !ok {
		t.Fatal("check-in must update the account cache")
	}
	entry := v.(*accountCacheEntry)
	if entry.checkin == nil {
		t.Fatal("cache entry must carry the check-in snapshot")
	}
	if entry.plan != "Free" {
		t.Errorf("merge must preserve the cached plan, got %q", entry.plan)
	}
}

// mergeCheckinCache must preserve credits/plan and never leave a partial entry.
func TestMergeCheckinCachePreservesCreditsAndPlan(t *testing.T) {
	resetAccountCache()
	accountCache.Store("auth-m", &accountCacheEntry{
		plan:    "Pro",
		credits: &creditsSummary{TotalRemain: 42},
		fetched: time.Now().Add(-time.Hour),
	})
	mergeCheckinCache("auth-m", &checkinSummary{TodayCheckedIn: true})

	v, _ := accountCache.Load("auth-m")
	entry := v.(*accountCacheEntry)
	if entry.plan != "Pro" || entry.credits == nil || entry.credits.TotalRemain != 42 {
		t.Fatalf("merge wiped plan/credits: %+v", entry)
	}
	if entry.checkin == nil || !entry.checkin.TodayCheckedIn {
		t.Fatalf("merge did not store the check-in snapshot: %+v", entry.checkin)
	}

	// A merge with no previous entry must still produce a usable entry.
	resetAccountCache()
	mergeCheckinCache("auth-new", &checkinSummary{TodayCheckedIn: false})
	v, ok := accountCache.Load("auth-new")
	if !ok {
		t.Fatal("merge into an empty cache must still store an entry")
	}
	if e := v.(*accountCacheEntry); e.credits != nil || e.plan != "" {
		t.Fatalf("a fresh entry must not invent credits/plan: %+v", e)
	}
}

// checkinLockFor must hand out one stable mutex per account so concurrent
// check-ins for the same account cannot double-POST.
func TestCheckinLockIsStablePerAccount(t *testing.T) {
	resetCheckinState()
	a := checkinLockFor("idx-1")
	b := checkinLockFor("idx-1")
	if a != b {
		t.Fatal("checkinLockFor must return the same mutex for one account")
	}
	if c := checkinLockFor("idx-2"); c == a {
		t.Fatal("different accounts must not share a lock")
	}

	// Serialisation proof: the second holder cannot enter while the first holds.
	var concurrent int32
	var maxConcurrent int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu := checkinLockFor("idx-3")
			mu.Lock()
			defer mu.Unlock()
			n := atomic.AddInt32(&concurrent, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if n <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
		}()
	}
	wg.Wait()
	if maxConcurrent != 1 {
		t.Fatalf("the per-account lock must serialise check-ins, max concurrent = %d", maxConcurrent)
	}
}

// pruneCheckinLocks must drop locks for accounts the host no longer knows.
func TestPruneCheckinLocksDropsUnknownAccounts(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-live", "workbuddy-CN-live.json", "live", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	checkinLockFor("idx-live")
	checkinLockFor("idx-gone")
	pruneCheckinLocks()

	if _, ok := checkinLocks.Load("idx-live"); !ok {
		t.Fatal("a live account's lock must be retained")
	}
	if _, ok := checkinLocks.Load("idx-gone"); ok {
		t.Fatal("a removed account's lock must be pruned")
	}
}

// Business soft-fails reported by the upstream ("already checked in") must be
// presented as success, not as an error, so the panel does not show a failure.
func TestCheckinSoftFailMessagesAreNormalised(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-cn", "workbuddy-CN-u5.json", "u5", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	srv, _ := checkinStub(t, struct {
		active          bool
		todayCheckedIn  bool
		checkinResponse string
	}{active: true, checkinResponse: `{"code":0,"msg":"OK","data":{"success":false,"message":"您今日已签到"}}`})
	restore := setBillingBase(srv.URL)
	defer restore()

	out := checkinOneAccount(pluginapi.HostAuthFileEntry{AuthIndex: "idx-cn", ID: "workbuddy-CN-u5.json"})
	if out["success"] != true {
		t.Fatalf("an 'already checked in' soft-fail must be success, got %+v", out)
	}
	if out["skipped"] != true || out["reason"] != "already" {
		t.Fatalf("soft-fail should be reported as an 'already' skip, got %+v", out)
	}
}
