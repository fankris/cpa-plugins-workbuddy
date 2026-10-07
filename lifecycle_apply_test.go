package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// fakeAuthStore is an in-memory stand-in for the host's auth store. It answers
// host.auth.list / host.auth.get / host.auth.save so the lifecycle state
// machine (disable → re-enable → retain) can be exercised end-to-end without a
// live CPA process. Writes are recorded so tests can assert on the exact auth
// records the plugin produced.
type fakeAuthStore struct {
	mu      sync.Mutex
	order   []string // auth_index order for list
	byIndex map[string]*fakeAuth
	saves   []fakeAuthSave
}

type fakeAuth struct {
	authIndex string
	name      string
	path      string
	json      []byte
}

type fakeAuthSave struct {
	name string
	json []byte
}

func newFakeAuthStore() *fakeAuthStore {
	return &fakeAuthStore{byIndex: map[string]*fakeAuth{}}
}

// put registers a credential; disabled is reflected in the stored JSON.
func (s *fakeAuthStore) put(authIndex, name, uid, region string, disabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body := map[string]any{
		"auth": map[string]any{
			"accessToken":  "access-" + authIndex,
			"refreshToken": "refresh-" + authIndex,
			"expiresAt":    time.Now().Add(time.Hour).Unix(),
		},
		"account": map[string]any{"uid": uid, "nickname": "acct-" + uid},
	}
	if region != "" {
		body["auth"].(map[string]any)["region"] = region
	}
	if disabled {
		body["disabled"] = true
	}
	raw, _ := json.Marshal(body)
	s.byIndex[authIndex] = &fakeAuth{authIndex: authIndex, name: name, path: "/tmp/" + name, json: raw}
	s.order = append(s.order, authIndex)
}

// stored returns the current JSON for an auth index.
func (s *fakeAuthStore) stored(authIndex string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.byIndex[authIndex]; ok {
		return append([]byte(nil), a.json...)
	}
	return nil
}

// savedRecords returns every host.auth.save payload in order.
func (s *fakeAuthStore) savedRecords() []fakeAuthSave {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fakeAuthSave(nil), s.saves...)
}

// handler implements the host RPC seam.
func (s *fakeAuthStore) handler(method string, request []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case pluginabi.MethodHostAuthList:
		files := make([]pluginapi.HostAuthFileEntry, 0, len(s.order))
		for _, idx := range s.order {
			a, ok := s.byIndex[idx]
			if !ok {
				continue
			}
			files = append(files, pluginapi.HostAuthFileEntry{
				ID:        a.name,
				AuthIndex: a.authIndex,
				Name:      a.name,
				Type:      providerName,
				Provider:  providerName,
				Disabled:  parseDisabledFromAuthJSON(a.json),
			})
		}
		return okEnvelope(rpcHostAuthListResponse{Files: files})

	case pluginabi.MethodHostAuthGet:
		var req struct {
			AuthIndex string `json:"auth_index"`
		}
		_ = json.Unmarshal(request, &req)
		a, ok := s.byIndex[req.AuthIndex]
		if !ok {
			return errorEnvelope("not_found", "no such auth index"), nil
		}
		return okEnvelope(rpcHostAuthGetResponse{
			AuthIndex: a.authIndex,
			Name:      a.name,
			Path:      a.path,
			JSON:      a.json,
		})

	case pluginabi.MethodHostAuthSave:
		var req pluginapi.HostAuthSaveRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		s.saves = append(s.saves, fakeAuthSave{name: req.Name, json: append([]byte(nil), req.JSON...)})
		// Mirror the write back into the store so follow-up reads observe it.
		for _, a := range s.byIndex {
			if a.name == req.Name {
				a.json = append([]byte(nil), req.JSON...)
				break
			}
		}
		return okEnvelope(map[string]any{"saved": true})
	}
	return nil, fmt.Errorf("fakeAuthStore: unhandled method %s", method)
}

// installFakeAuthStore wires the store into the plugin and restores state after.
func installFakeAuthStore(t *testing.T, store *fakeAuthStore) {
	t.Helper()
	old := hostRPCTestOverride
	hostRPCTestOverride = store.handler
	t.Cleanup(func() { hostRPCTestOverride = old })
}

// resetLifecycleForTest clears cached lifecycle/cache state between tests.
func resetLifecycleForTest() {
	lifecycleState = sync.Map{}
	accountCache = sync.Map{}
	checkinLocks = sync.Map{}
	resetLifecycleStateForTest()
}

func resetLifecycleStateForTest() {
	lifecycleState.Range(func(k, _ any) bool {
		lifecycleState.Delete(k)
		return true
	})
}

// A CN account with zero remaining credits must be disabled (auth record
// retained, disabled:true written). Credit recovery alone must not enable it.
func TestLifecycleDisablesButNeverAutomaticallyReenablesCNAccount(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-cn", "workbuddy-CN-u1.json", "u1", regionCN, false)
	installFakeAuthStore(t, store)
	resetLifecycleForTest()

	oldLifecycle := lifecycleAuto
	lifecycleAutoMu.Lock()
	lifecycleAuto = true
	lifecycleAutoMu.Unlock()
	t.Cleanup(func() {
		lifecycleAutoMu.Lock()
		lifecycleAuto = oldLifecycle
		lifecycleAutoMu.Unlock()
	})

	sa, _, err := hostAuthGetBundle("idx-cn")
	if err != nil {
		t.Fatalf("hostAuthGetBundle: %v", err)
	}

	exhausted := &creditsSummary{TotalRemain: 0, TotalUsed: 10, TotalSize: 10, PackCount: 1}
	if act := lifecycleActionFor(accountRegion(sa), exhausted); act != lifecycleDisable {
		t.Fatalf("exhausted CN should disable, got %v", act)
	}
	if err := applyExhaustedPolicy("idx-cn", "workbuddy-CN-u1.json", sa, exhausted, "耗尽"); err != nil {
		t.Fatalf("applyExhaustedPolicy: %v", err)
	}

	saves := store.savedRecords()
	if len(saves) == 0 {
		t.Fatal("disabling must write the auth record through host.auth.save")
	}
	var written struct {
		Disabled bool   `json:"disabled"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(saves[len(saves)-1].json, &written); err != nil {
		t.Fatalf("saved record is not valid JSON: %v", err)
	}
	if !written.Disabled {
		t.Fatal("exhausted CN account must be written with disabled:true")
	}
	if !strings.Contains(written.Note, "CN") {
		t.Fatalf("note should keep the region label, got %q", written.Note)
	}
	// The credential itself must survive: disabling is not deletion.
	if !strings.Contains(string(saves[len(saves)-1].json), "refresh-idx-cn") {
		t.Fatal("disabling must retain the refresh token (never delete a CN record)")
	}

	// Recovery is not authorization to undo disabled:true.
	restored := &creditsSummary{TotalRemain: 100, TotalUsed: 10, TotalSize: 110, PackCount: 1}
	if shouldReenableCN(true, restored) {
		t.Fatal("credit recovery must require explicit CPA enable")
	}
	before := len(store.savedRecords())
	if err := reenableAuth("idx-cn", "workbuddy-CN-u1.json", sa, restored); err == nil {
		t.Fatal("legacy auto-enable guard must reject")
	}
	if len(store.savedRecords()) != before {
		t.Fatal("automatic recovery wrote a credential")
	}

}

// A Global account that is genuinely exhausted is retained disabled, never
// deleted (the host auth record is the only copy of the credential).
func TestLifecycleRetainsGlobalAccountInsteadOfDeleting(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-gl", "workbuddy-Global-u2.json", "u2", regionGlobal, false)
	installFakeAuthStore(t, store)
	resetLifecycleForTest()

	sa, _, err := hostAuthGetBundle("idx-gl")
	if err != nil {
		t.Fatalf("hostAuthGetBundle: %v", err)
	}
	exhausted := &creditsSummary{TotalRemain: 0, TotalUsed: 5, TotalSize: 5, PackCount: 1}
	if act := lifecycleActionFor(accountServiceRegion(sa), exhausted); act != lifecycleDelete {
		t.Fatalf("exhausted Global should select the delete action, got %v", act)
	}
	if err := deleteAuth("idx-gl", "workbuddy-Global-u2.json", sa); err != nil {
		t.Fatalf("deleteAuth: %v", err)
	}
	saves := store.savedRecords()
	if len(saves) == 0 {
		t.Fatal("Global retention must still write through host.auth.save")
	}
	var written struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.Unmarshal(saves[len(saves)-1].json, &written); err != nil {
		t.Fatalf("retained record is not valid JSON: %v", err)
	}
	if !written.Disabled {
		t.Fatal("retained Global record must be disabled")
	}
	if !strings.Contains(string(saves[len(saves)-1].json), "refresh-idx-gl") {
		t.Fatal("retention must keep the credential material")
	}
}

// Unknown credits must never trigger a lifecycle action: a failed billing
// fetch is not evidence of exhaustion.
func TestLifecycleIgnoresUnknownCredits(t *testing.T) {
	for name, cr := range map[string]*creditsSummary{
		"nil":          nil,
		"empty":        {},
		"zero-pack":    {PackCount: 0},
		"zero-remain":  {TotalRemain: 0, TotalUsed: 0, TotalSize: 0},
		"negative-rem": {TotalRemain: -1, TotalUsed: 0},
	} {
		if act := lifecycleActionFor(regionCN, cr); act != lifecycleNone {
			t.Errorf("%s: unknown credits must not act, got %v", name, act)
		}
		if act := lifecycleActionFor(regionGlobal, cr); act != lifecycleNone {
			t.Errorf("%s: unknown credits must not delete a Global account, got %v", name, act)
		}
	}
}

// Re-enable only applies to disabled CN accounts that actually have credits.
func TestShouldReenableCNBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		disabled bool
		cr       *creditsSummary
		want     bool
	}{
		{"not disabled", false, &creditsSummary{TotalRemain: 10}, false},
		{"disabled, nil credits", true, nil, false},
		{"disabled, still exhausted", true, &creditsSummary{TotalRemain: 0, TotalUsed: 5, TotalSize: 5}, false},
		{"disabled, credits restored", true, &creditsSummary{TotalRemain: 5, TotalUsed: 1, TotalSize: 6}, false},
	}
	for _, tc := range cases {
		if got := shouldReenableCN(tc.disabled, tc.cr); got != tc.want {
			t.Errorf("%s: shouldReenableCN = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A soft rate limit must never be mistaken for credit exhaustion: throttling
// is transient, disabling an account for it would be destructive.
func TestSoftRateLimitIsNotCreditExhaustion(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{429, `{"msg":"too many requests"}`},
		{429, `{"msg":"rate limit exceeded"}`},
		{503, `{"msg":"upstream throttled"}`},
	} {
		if isHardCreditError(tc.status, tc.body) {
			t.Errorf("status %d body %q must not be a hard credit error", tc.status, tc.body)
		}
		if !isSoftRateLimit(tc.status, tc.body) {
			t.Errorf("status %d body %q should classify as a soft rate limit", tc.status, tc.body)
		}
	}
	// 402 and credit wording ARE hard credit errors.
	for _, tc := range []struct {
		status int
		body   string
	}{
		{402, `{"msg":"payment required"}`},
		{200, `{"msg":"积分不足"}`},
		{400, `{"msg":"insufficient credits"}`},
	} {
		if !isHardCreditError(tc.status, tc.body) {
			t.Errorf("status %d body %q should be a hard credit error", tc.status, tc.body)
		}
		if isSoftRateLimit(tc.status, tc.body) {
			t.Errorf("status %d body %q must not classify as a soft rate limit", tc.status, tc.body)
		}
	}
}

// The note written to the auth record must never leak key material and must
// stay within the host's display budget.
func TestDisplayNoteIsBoundedAndSecretFree(t *testing.T) {
	sa := &storedAuth{Account: storedAccount{UID: "u1", Nickname: "Nick"}}
	sa.Auth.Region = regionCN
	note := displayNote(sa, &creditsSummary{TotalRemain: 3, TotalUsed: 7, TotalSize: 10}, false)
	if strings.Contains(note, "access-") || strings.Contains(note, "refresh-") {
		t.Fatalf("note leaked credential material: %s", note)
	}
	if len(note) > 80 {
		t.Fatalf("note exceeds the display budget (%d chars): %s", len(note), note)
	}
	if !strings.Contains(note, "CN") {
		t.Fatalf("note should carry the region label: %s", note)
	}
}

// labelForAuth drives the host auth-row label; it must always carry the region.
func TestLabelForAuthCarriesRegion(t *testing.T) {
	cases := []struct {
		region string
		want   string
	}{
		{regionCN, "Nick [CN]"},
		{regionGlobal, "Nick [Intl]"},
		{regionIntl, "Nick [Intl]"},
	}
	for _, tc := range cases {
		sa := &storedAuth{Account: storedAccount{Nickname: "Nick"}}
		sa.Auth.Region = tc.region
		if got := labelForAuth(sa); got != tc.want {
			t.Errorf("region %q: labelForAuth = %q, want %q", tc.region, got, tc.want)
		}
	}
	if got := labelForAuth(nil); !strings.Contains(got, "WorkBuddy") {
		t.Errorf("nil auth should fall back to the product name, got %q", got)
	}
}
