package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// resolveAuthIndexAndID is the bridge between executor errors (which carry an
// AuthID: an auth_index, a file ID, or an account UID) and the host auth store.
// A miss here means a dead account keeps receiving traffic, so the lookup order
// matters.

// An auth_index must resolve directly.
func TestResolveAuthIndexAndIDByIndex(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-direct", "workbuddy-CN-direct.json", "direct", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	idx, id := resolveAuthIndexAndID("idx-direct")
	if idx != "idx-direct" {
		t.Fatalf("auth_index lookup failed: %q", idx)
	}
	if id != "workbuddy-CN-direct.json" {
		t.Fatalf("auth ID should be the file name, got %q", id)
	}
}

// A file name must resolve too (the executor sometimes passes auth.ID).
func TestResolveAuthIndexAndIDByFileName(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-name", "workbuddy-CN-name.json", "name", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	idx, id := resolveAuthIndexAndID("workbuddy-CN-name.json")
	if idx != "idx-name" || id != "workbuddy-CN-name.json" {
		t.Fatalf("file-name lookup failed: idx=%q id=%q", idx, id)
	}
}

// A bare UID must resolve via the canonical/legacy name candidates.
func TestResolveAuthIndexAndIDByUID(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-uid", "workbuddy-CN-uid123.json", "uid123", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	idx, id := resolveAuthIndexAndID("uid123")
	if idx != "idx-uid" {
		t.Fatalf("UID lookup failed: idx=%q", idx)
	}
	if id == "" {
		t.Fatal("UID lookup must also return the auth ID")
	}
}

// A legacy codebuddy-cn name must still resolve after adoption renames it.
func TestResolveAuthIndexAndIDFindsLegacyNames(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-legacy", "codebuddy-cn-legacy.json", "legacy", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	idx, _ := resolveAuthIndexAndID("codebuddy-cn-legacy.json")
	if idx != "idx-legacy" {
		t.Fatalf("legacy name lookup failed: %q", idx)
	}
	// The UID must resolve to the same record through the candidate list.
	if idx2, _ := resolveAuthIndexAndID("legacy"); idx2 != "idx-legacy" {
		t.Fatalf("UID lookup for a legacy record failed: %q", idx2)
	}
}

// Unknown identifiers must resolve to nothing rather than the first account.
func TestResolveAuthIndexAndIDUnknown(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-known", "workbuddy-CN-known.json", "known", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	if idx, id := resolveAuthIndexAndID("nope"); idx != "" || id != "" {
		t.Fatalf("an unknown id must not resolve, got idx=%q id=%q", idx, id)
	}
	if idx, id := resolveAuthIndexAndID(""); idx != "" || id != "" {
		t.Fatalf("an empty id must not resolve, got idx=%q id=%q", idx, id)
	}
}

// invalidateAccountCredits must drop only the credits field: the plan and
// check-in snapshot stay usable, and an unknown id is a no-op.
func TestInvalidateAccountCreditsKeepsPlanAndCheckin(t *testing.T) {
	resetAccountCache()
	accountCache.Store("auth-inv", &accountCacheEntry{
		plan:    "Pro",
		checkin: &checkinSummary{TodayCheckedIn: true},
		credits: &creditsSummary{TotalRemain: 99},
	})

	invalidateAccountCredits("auth-inv", "")

	v, ok := accountCache.Load("auth-inv")
	if !ok {
		t.Fatal("invalidation must not delete the cache entry")
	}
	e := v.(*accountCacheEntry)
	if e.credits != nil {
		t.Error("credits must be invalidated so the next fetch hits upstream")
	}
	if e.plan != "Pro" {
		t.Errorf("plan must survive invalidation, got %q", e.plan)
	}
	if e.checkin == nil || !e.checkin.TodayCheckedIn {
		t.Error("check-in state must survive invalidation")
	}

	// Unknown ids must not panic.
	invalidateAccountCredits("does-not-exist", "")
	invalidateAccountCredits("", "")
}

// The executor-error hook must only act on hard credit failures — a rate limit
// or a transient 5xx must never disable an account.
func TestReconcileAfterExecutorErrorGuards(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-err", "workbuddy-CN-err.json", "err", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	oldLifecycle := lifecycleAuto
	lifecycleAutoMu.Lock()
	lifecycleAuto = true
	lifecycleAutoMu.Unlock()
	t.Cleanup(func() {
		lifecycleAutoMu.Lock()
		lifecycleAuto = oldLifecycle
		lifecycleAutoMu.Unlock()
	})

	// A soft rate limit must be ignored entirely (no reconcile, no write).
	reconcileAfterExecutorError("idx-err", http.StatusTooManyRequests, `{"msg":"too many requests"}`)
	if len(store.savedRecords()) != 0 {
		t.Fatal("a 429 must not trigger a lifecycle write")
	}
	// A generic 500 must be ignored too.
	reconcileAfterExecutorError("idx-err", 500, `{"msg":"internal"}`)
	if len(store.savedRecords()) != 0 {
		t.Fatal("a 5xx must not trigger a lifecycle write")
	}
	// An empty auth id is a no-op.
	reconcileAfterExecutorError("", 402, `{"msg":"payment required"}`)
}

// reconcileByUID must resolve the account by UID and respect the guards.
func TestReconcileByUIDGuards(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-byuid", "workbuddy-CN-byuid.json", "byuid", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	oldLifecycle := lifecycleAuto
	lifecycleAutoMu.Lock()
	lifecycleAuto = true
	lifecycleAutoMu.Unlock()
	t.Cleanup(func() {
		lifecycleAutoMu.Lock()
		lifecycleAuto = oldLifecycle
		lifecycleAutoMu.Unlock()
	})

	// Non-credit failures are ignored.
	reconcileByUID("byuid", 429, `{"msg":"throttled"}`)
	if len(store.savedRecords()) != 0 {
		t.Fatal("a rate limit must not trigger a write")
	}
	// An unknown UID resolves to nothing and must not panic.
	reconcileByUID("ghost", 402, `{"msg":"payment required"}`)
	// An empty UID is a no-op.
	reconcileByUID("", 402, `{"msg":"payment required"}`)
}

// reconcileOneAccount with lifecycle disabled must do nothing at all.
func TestReconcileOneAccountDisabled(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-off", "workbuddy-CN-off.json", "off", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	oldLifecycle := lifecycleAuto
	lifecycleAutoMu.Lock()
	lifecycleAuto = false
	lifecycleAutoMu.Unlock()
	t.Cleanup(func() {
		lifecycleAutoMu.Lock()
		lifecycleAuto = oldLifecycle
		lifecycleAutoMu.Unlock()
	})

	action, err := reconcileOneAccount("idx-off", "workbuddy-CN-off.json", true)
	if err != nil {
		t.Fatalf("reconcile with lifecycle off should not error: %v", err)
	}
	if action != lifecycleNone {
		t.Fatalf("lifecycle off must yield no action, got %v", action)
	}
	if len(store.savedRecords()) != 0 {
		t.Fatal("lifecycle off must not write any auth record")
	}
}

// syncAuthNote must be idempotent: repeating the same note must not rewrite the
// record every tick (the host would churn files forever).
func TestSyncAuthNoteIsIdempotent(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-note", "workbuddy-CN-note.json", "note", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	sa, _, err := hostAuthGetBundle("idx-note")
	if err != nil {
		t.Fatalf("hostAuthGetBundle: %v", err)
	}
	cr := &creditsSummary{TotalRemain: 5, TotalUsed: 1, TotalSize: 6}

	if err := syncAuthNote("idx-note", "workbuddy-CN-note.json", sa, cr, false); err != nil {
		t.Fatalf("syncAuthNote: %v", err)
	}
	first := len(store.savedRecords())

	// The same note again must be recognised as unchanged.
	if err := syncAuthNote("idx-note", "workbuddy-CN-note.json", sa, cr, false); err != nil {
		t.Fatalf("syncAuthNote (repeat): %v", err)
	}
	if got := len(store.savedRecords()); got != first {
		t.Fatalf("a repeated identical note must not rewrite the record (%d -> %d writes)", first, got)
	}
}

// reconcileAllAccounts reports only accounts that produced an action or an
// error, so a healthy fleet yields no action rows. It does still refresh each
// record's display note once (region/credits summary on the auth row) — that
// write is expected and must be idempotent on the next pass.
func TestReconcileAllAccountsHealthyFleetIsNoop(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-r1", "workbuddy-CN-r1.json", "r1", regionCN, false)
	store.put("idx-r2", "workbuddy-Global-r2.json", "r2", regionGlobal, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	srv, _ := billingStub(t, 50)
	restore := setBillingBase(srv.URL)
	defer restore()

	oldLifecycle := lifecycleAuto
	lifecycleAutoMu.Lock()
	lifecycleAuto = true
	lifecycleAutoMu.Unlock()
	t.Cleanup(func() {
		lifecycleAutoMu.Lock()
		lifecycleAuto = oldLifecycle
		lifecycleAutoMu.Unlock()
	})

	rows := reconcileAllAccounts(false)
	if len(rows) != 0 {
		t.Fatalf("a healthy fleet should produce no action rows, got %+v", rows)
	}
	// The first pass refreshes each record's note (region/credits summary).
	if len(store.savedRecords()) == 0 {
		t.Fatal("the first reconcile should refresh the auth note")
	}
	firstPassWrites := len(store.savedRecords())

	// A second pass must be a no-op: nothing changed, so nothing is rewritten.
	reconcileAllAccounts(false)
	if got := len(store.savedRecords()); got != firstPassWrites {
		t.Fatalf("a repeated reconcile must not rewrite unchanged notes (%d -> %d)", firstPassWrites, got)
	}

	// Healthy accounts must never be disabled by a reconcile pass.
	for _, rec := range store.savedRecords() {
		var written struct {
			Disabled bool `json:"disabled"`
		}
		_ = json.Unmarshal(rec.json, &written)
		if written.Disabled {
			t.Fatalf("a healthy account was disabled: %s", rec.json)
		}
	}
}

// With lifecycle disabled the whole walk is skipped.
func TestReconcileAllAccountsDisabled(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-off", "workbuddy-CN-off.json", "off", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	oldLifecycle := lifecycleAuto
	lifecycleAutoMu.Lock()
	lifecycleAuto = false
	lifecycleAutoMu.Unlock()
	t.Cleanup(func() {
		lifecycleAutoMu.Lock()
		lifecycleAuto = oldLifecycle
		lifecycleAutoMu.Unlock()
	})

	if rows := reconcileAllAccounts(true); rows != nil {
		t.Fatalf("a disabled lifecycle must not walk the fleet, got %+v", rows)
	}
}
