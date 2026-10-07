package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func Test28ManualPauseSurvivesEveryBalanceAndForceMode(t *testing.T) {
	for _, region := range []string{regionCN, regionGlobal, regionIntl} {
		for _, force := range []bool{false, true} {
			for _, remain := range []int64{0, 100} {
				store := newFakeAuthStore()
				store.put("paused", "paused.json", "paused", region, true)
				installFakeAuthStore(t, store)
				resetLifecycleForTest()
				old := lifecycleAuto
				lifecycleAuto = true
				original := string(store.stored("paused"))
				publishAccountSnapshot("paused.json", accountSnapshotPatch{creditsSet: true, credits: &creditsSummary{TotalRemain: remain, TotalSize: 100, TotalUsed: 100 - remain}})
				act, err := reconcileOneAccount("paused", "paused.json", force)
				lifecycleAuto = old
				if err != nil || act != lifecycleNone || len(store.savedRecords()) != 0 || string(store.stored("paused")) != original {
					t.Fatalf("manual pause changed: region=%s force=%v remain=%d act=%v err=%v", region, force, remain, act, err)
				}
			}
		}
	}
}

func Test28FailedCreditRefreshNeverWritesAccountState(t *testing.T) {
	for _, remain := range []int64{0, 100} {
		t.Run(itoa(remain), func(t *testing.T) {
			store := newFakeAuthStore()
			store.put("a", "a.json", "a", regionCN, false)
			installFakeAuthStore(t, store)
			resetLifecycleForTest()
			old := lifecycleAuto
			lifecycleAuto = true
			defer func() { lifecycleAuto = old }()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
			defer srv.Close()
			restore := setBillingBase(srv.URL)
			defer restore()
			publishAccountSnapshot("a.json", accountSnapshotPatch{creditsSet: true, credits: &creditsSummary{TotalRemain: remain, TotalSize: 100, TotalUsed: 100 - remain, FetchedAt: time.Now().Add(-time.Hour).Format(time.RFC3339)}})
			act, err := reconcileOneAccount("a", "a.json", true)
			if act != lifecycleNone || err == nil || len(store.savedRecords()) != 0 {
				t.Fatalf("failed refresh acted: %v %v saves=%d", act, err, len(store.savedRecords()))
			}
			e, _ := accountCache.Load("a.json")
			entry := e.(*accountCacheEntry)
			if entry.credits == nil || entry.credits.TotalRemain != remain || !hasCreditError(entry.errs) {
				t.Fatal("display fallback or error lost")
			}
			// Updating only the check-in must not erase a credit error or renew its timestamp.
			stamp := entry.credits.FetchedAt
			mergeCheckinCache("a.json", &checkinSummary{TodayCheckedIn: true})
			e, _ = accountCache.Load("a.json")
			entry = e.(*accountCacheEntry)
			if !hasCreditError(entry.errs) || entry.credits.FetchedAt != stamp || trustedCachedCredits("a.json", time.Now(), time.Minute) != nil {
				t.Fatal("check-in promoted old credits to decision evidence")
			}
		})
	}
}

func Test28ConfirmedFreshExhaustionStillDisables(t *testing.T) {
	store := newFakeAuthStore()
	store.put("a", "a.json", "a", regionCN, false)
	installFakeAuthStore(t, store)
	resetLifecycleForTest()
	old := lifecycleAuto
	lifecycleAuto = true
	defer func() { lifecycleAuto = old }()
	publishAccountSnapshot("a.json", accountSnapshotPatch{creditsSet: true, credits: &creditsSummary{TotalRemain: 0, TotalUsed: 10, TotalSize: 10, PackCount: 1}})
	act, err := reconcileOneAccount("a", "a.json", false)
	if err != nil || act != lifecycleDisable || !parseDisabledFromAuthJSON(store.stored("a")) {
		t.Fatalf("confirmed exhaustion not handled: %v %v", act, err)
	}
}

func Test28FieldGenerationsRejectOutOfOrderCompletion(t *testing.T) {
	resetAccountCache()
	older, newer := nextAccountSnapshot(), nextAccountSnapshot()
	publishAccountSnapshot("a", accountSnapshotPatch{seq: newer, creditsSet: true, credits: &creditsSummary{TotalRemain: 90}, checkinSet: true, checkin: &checkinSummary{TodayCheckedIn: true}})
	entry := publishAccountSnapshot("a", accountSnapshotPatch{seq: older, creditsSet: true, credits: &creditsSummary{TotalRemain: 1}, checkinSet: true, checkin: &checkinSummary{TodayCheckedIn: false}, planSet: true, plan: "Pro", errors: []string{"credits: old error"}})
	if entry.credits.TotalRemain != 90 || !entry.checkin.TodayCheckedIn || entry.plan != "Pro" || hasCreditError(entry.errs) {
		t.Fatalf("late old response overwrote fields: %+v", entry)
	}
}

func Test28ConcurrentDisjointCacheUpdatesAreNotLost(t *testing.T) {
	resetAccountCache()
	for n := 0; n < 100; n++ {
		id := itoa(int64(n))
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			publishAccountSnapshot(id, accountSnapshotPatch{creditsSet: true, credits: &creditsSummary{TotalRemain: 20}})
		}()
		go func() { defer wg.Done(); mergeCheckinCache(id, &checkinSummary{TodayCheckedIn: true}) }()
		go func() { defer wg.Done(); publishAccountSnapshot(id, accountSnapshotPatch{planSet: true, plan: "Pro"}) }()
		wg.Wait()
		v, _ := accountCache.Load(id)
		e := v.(*accountCacheEntry)
		if e.credits == nil || e.checkin == nil || e.plan != "Pro" || !creditSnapshotFresh(e, time.Now(), time.Minute) {
			t.Fatalf("lost update: %+v", e)
		}
	}
}

func Test28InvalidationIsBarrierAgainstInFlightOldReads(t *testing.T) {
	resetAccountCache()
	seq := nextAccountSnapshot()
	invalidateCachedCredits("a")
	e := publishAccountSnapshot("a", accountSnapshotPatch{seq: seq, creditsSet: true, credits: &creditsSummary{TotalRemain: 10}})
	if e.credits != nil {
		t.Fatal("old in-flight read resurrected invalidated balance")
	}
	e = publishAccountSnapshot("a", accountSnapshotPatch{creditsSet: true, credits: &creditsSummary{TotalRemain: 30}})
	if e.credits.TotalRemain != 30 {
		t.Fatal("new read cannot recover invalidation")
	}
}

func Test28FreshnessIsSharedByCreditScheduling(t *testing.T) {
	resetAccountCache()
	now := time.Now()
	cases := []struct {
		name, at string
		errs     []string
		fresh    bool
	}{
		{"fresh", now.Add(-time.Second).Format(time.RFC3339Nano), nil, true},
		{"old", now.Add(-6 * time.Minute).Format(time.RFC3339Nano), nil, false},
		{"future", now.Add(time.Minute).Format(time.RFC3339Nano), nil, false},
		{"missing", "", nil, false},
		{"credit failure", now.Format(time.RFC3339Nano), []string{"credits: offline"}, false},
		{"other field failure", now.Add(-time.Second).Format(time.RFC3339Nano), []string{"checkin: offline"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accountCache.Store("a", &accountCacheEntry{credits: &creditsSummary{TotalRemain: 20, FetchedAt: tc.at, Packages: []packageSummary{{Remain: 20, CycleEnd: now.Add(time.Hour).Format(time.RFC3339)}}}, errs: tc.errs})
			cr := trustedCachedCredits("a", now, 5*time.Minute)
			if (cr != nil) != tc.fresh {
				t.Fatal("freshness mismatch")
			}
			remain, _ := cachedCreditsScore("a")
			if (remain >= 0) != tc.fresh {
				t.Fatal("credits mode disagrees")
			}
			result := pickExpiringCredits([]pluginapi.SchedulerAuthCandidate{{ID: "a", Provider: providerName}}, now)
			if result.Handled != tc.fresh {
				t.Fatal("expiry mode disagrees")
			}
		})
	}
}

func Test28BillingMutationsNeverRetryAndKeepUncertainty(t *testing.T) {
	for _, trial := range []bool{false, true} {
		for _, status := range []int{429, 500} {
			t.Run(itoa(int64(status))+map[bool]string{false: "checkin", true: "trial"}[trial], func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(status) }))
				defer srv.Close()
				restore := setBillingBase(srv.URL)
				defer restore()
				restoreGlobal := setBillingBaseGlobal(srv.URL)
				defer restoreGlobal()
				sa := &storedAuth{Auth: storedTokens{Domain: "www.codebuddy.cn"}}
				var out map[string]any
				var err error
				if trial {
					sa.Auth.Domain = "www.workbuddy.ai"
					out, err = performTrialCall(sa)
				} else {
					out, err = performCheckinCall(sa)
				}
				if err != nil || calls.Load() != 1 {
					t.Fatalf("write replayed: calls=%d err=%v", calls.Load(), err)
				}
				if (out["uncertain"] == true) != (status == 500) {
					t.Fatalf("wrong uncertainty: %+v", out)
				}
			})
		}
	}
}

func Test28CreditAcquisitionIsTimestampedBeforeCachePublication(t *testing.T) {
	srv, _ := billingStub(t, 50)
	restore := setBillingBase(srv.URL)
	defer restore()
	cr, err := fetchUserResource(&storedAuth{})
	if err != nil || cr.FetchedAt == "" {
		t.Fatalf("missing read timestamp: %v", err)
	}
	at := cr.FetchedAt
	seq := nextAccountSnapshot()
	e := publishAccountSnapshot("timed", accountSnapshotPatch{seq: seq, creditsSet: true, credits: cr})
	if e.credits == cr || e.credits.FetchedAt != at {
		t.Fatal("publication mutated/shared acquisition snapshot")
	}
	raw, _ := json.Marshal(e.credits)
	if len(raw) == 0 {
		t.Fatal("invalid snapshot")
	}
}
