package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The account cache is the coordination point between the dashboard, the
// lifecycle reconciler and the scheduler. These tests pin its two critical
// guarantees: (1) a fresh entry is served without touching upstream, and
// (2) concurrent callers for one account share a single upstream fetch
// (singleflight) instead of stampeding the billing API.

// resetAccountCache clears per-test cache and singleflight state.
func resetAccountCache() {
	accountCache = sync.Map{}
	accountDetailFlight = sync.Map{}
}

// billingStub returns an httptest server answering the three billing
// endpoints cachedAccountDetails calls, counting total requests.
func billingStub(t *testing.T, creditsRemain int64) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v2/billing/meter/checkin-activity-status":
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"checked_in":true,"today_checked_in":true}}`))
		case r.URL.Path == "/v2/billing/meter/get-user-resource":
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"Response":{"Data":{"TotalCount":1,"Accounts":[{"CapacityRemain":` + itoa(creditsRemain) + `,"CapacityUsed":1,"CapacitySize":` + itoa(creditsRemain+1) + `,"PackageName":"Free"}]}}}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"payment_type":"Free"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// A fresh cache entry must be returned without any upstream call.
func TestAccountCacheServesFreshEntryWithoutUpstream(t *testing.T) {
	resetAccountCache()
	srv, calls := billingStub(t, 42)
	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{}
	// Prime the cache directly (bypassing upstream) with a fresh entry.
	accountCache.Store("auth-1", &accountCacheEntry{
		plan:    "Free",
		credits: &creditsSummary{TotalRemain: 7, TotalUsed: 1, TotalSize: 8},
		fetched: time.Now(),
	})
	before := atomic.LoadInt32(calls)

	plan, _, cr, errs := cachedAccountDetails("auth-1", sa, false)
	if len(errs) != 0 {
		t.Fatalf("fresh cache hit should not report errors: %v", errs)
	}
	if plan != "Free" || cr == nil || cr.TotalRemain != 7 {
		t.Fatalf("cached values not returned: plan=%q credits=%+v", plan, cr)
	}
	if got := atomic.LoadInt32(calls) - before; got != 0 {
		t.Fatalf("a fresh cache entry must not hit upstream, got %d calls", got)
	}
}

// Concurrent callers for the same account must share ONE upstream fetch.
func TestAccountCacheSingleflightDedupsConcurrentFetches(t *testing.T) {
	resetAccountCache()
	srv, calls := billingStub(t, 100)
	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{}
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start // release together to maximise overlap
			cachedAccountDetails("auth-2", sa, true)
		}()
	}
	close(start)
	wg.Wait()

	// Three upstream endpoints per fetch; singleflight means one round of three
	// (a small tolerance covers a racing second flight after the first completes).
	if got := atomic.LoadInt32(calls); got > 6 {
		t.Fatalf("singleflight failed: %d upstream calls for %d concurrent readers (want ~3)", got, workers)
	}
	if _, ok := accountCache.Load("auth-2"); !ok {
		t.Fatal("the fetch must populate the cache")
	}
	if _, ok := accountDetailFlight.Load("auth-2"); ok {
		t.Fatal("the singleflight entry must be released after completion")
	}
}

// On upstream failure the previous values must survive (stale-while-error):
// a transient 500 must not blank the panel row.
func TestAccountCacheKeepsPreviousValuesOnError(t *testing.T) {
	resetAccountCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()
	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{}
	prevCredits := &creditsSummary{TotalRemain: 55, TotalUsed: 5, TotalSize: 60, FetchedAt: "2026-01-01T00:00:00Z"}
	accountCache.Store("auth-3", &accountCacheEntry{
		plan:    "Pro",
		credits: prevCredits,
		fetched: time.Now().Add(-2 * accountCacheTTL), // stale → forces a fetch
	})

	plan, _, cr, errs := cachedAccountDetails("auth-3", sa, true)
	if len(errs) == 0 {
		t.Fatal("failing upstream should surface errors")
	}
	if plan != "Pro" {
		t.Errorf("previous plan must survive an upstream failure, got %q", plan)
	}
	if cr == nil || cr.TotalRemain != 55 {
		t.Errorf("previous credits must survive an upstream failure, got %+v", cr)
	}
	if cr.FetchedAt != "2026-01-01T00:00:00Z" || prevCredits.FetchedAt != cr.FetchedAt {
		t.Fatal("failed refresh must not advance the old credits timestamp")
	}
	_, _, _, cachedErrors := cachedAccountDetails("auth-3", sa, false)
	if len(cachedErrors) == 0 {
		t.Fatal("cache hit must preserve refresh errors")
	}
}

// The soft cap must bound cache growth under auth churn.
func TestPruneAccountCacheSoftCapBounds(t *testing.T) {
	resetAccountCache()
	base := time.Now()
	for i := 0; i < 20; i++ {
		accountCache.Store("k"+itoa(int64(i)), &accountCacheEntry{
			fetched: base.Add(time.Duration(i) * time.Second),
		})
	}
	pruneAccountCacheSoftCap(5)
	count := 0
	accountCache.Range(func(_, _ any) bool { count++; return true })
	if count != 5 {
		t.Fatalf("soft cap should leave 5 entries, got %d", count)
	}
	// The newest entries must be the survivors.
	if _, ok := accountCache.Load("k19"); !ok {
		t.Fatal("the newest entry must survive pruning")
	}
	if _, ok := accountCache.Load("k0"); ok {
		t.Fatal("the oldest entry should have been dropped")
	}
	// Non-positive cap is a no-op (defensive).
	pruneAccountCacheSoftCap(0)
	count = 0
	accountCache.Range(func(_, _ any) bool { count++; return true })
	if count != 5 {
		t.Fatalf("cap<=0 must be a no-op, got %d entries", count)
	}
}

// cachedCheckinToday reads through the cache and reports absence honestly.
func TestCachedCheckinToday(t *testing.T) {
	resetAccountCache()
	if got := cachedCheckinToday("missing"); got != nil {
		t.Fatalf("unknown auth must return nil, got %v", *got)
	}
	accountCache.Store("auth-4", &accountCacheEntry{}) // entry without checkin
	if got := cachedCheckinToday("auth-4"); got != nil {
		t.Fatalf("entry without checkin must return nil, got %v", *got)
	}
	accountCache.Store("auth-5", &accountCacheEntry{
		checkin: &checkinSummary{TodayCheckedIn: true},
	})
	got := cachedCheckinToday("auth-5")
	if got == nil || !*got {
		t.Fatalf("cached today_checked_in not surfaced: %v", got)
	}
}
