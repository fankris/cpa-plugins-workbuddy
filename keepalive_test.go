package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Token keepalive refreshes each credential's access token daily. The
// dangerous cases are (a) a revoked offline session, which must disable the
// account rather than retry forever, and (b) any refresh that must not lose
// the stored refresh token.

// keepaliveStub answers the token-refresh endpoint. status/body control the
// response; refreshed records the new token so tests can assert persistence.
type keepaliveStub struct {
	status int
	body   string
	seen   []byte
}

func (s *keepaliveStub) handler(req *http.Request) (*hostHTTPResponse, error) {
	if req.Body != nil {
		s.seen, _ = io.ReadAll(req.Body)
	}
	if s.status != 0 {
		return &hostHTTPResponse{StatusCode: s.status, Body: []byte(s.body)}, nil
	}
	return &hostHTTPResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       []byte(s.body),
	}, nil
}

func installKeepaliveStub(t *testing.T, stub *keepaliveStub) {
	t.Helper()
	old := hostHTTPTestOverride
	hostHTTPTestOverride = stub.handler
	t.Cleanup(func() { hostHTTPTestOverride = old })
}

// A successful refresh must persist the rotated access token (and keep the
// refresh token when the upstream does not rotate it).
func TestKeepaliveRefreshesAndPersistsTokens(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-ka", "workbuddy-CN-ka.json", "ka", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	stub := &keepaliveStub{body: `{"code":0,"msg":"OK","data":{"accessToken":"rotated-access","expires_in":3600}}`}
	installKeepaliveStub(t, stub)

	status, err := refreshOneAuth("idx-ka", "workbuddy-CN-ka.json")
	if err != nil {
		t.Fatalf("refreshOneAuth: %v", err)
	}
	if status != "refreshed" {
		t.Fatalf("status = %q, want refreshed", status)
	}
	saves := store.savedRecords()
	if len(saves) == 0 {
		t.Fatal("a successful refresh must persist the credential")
	}
	written := saves[len(saves)-1].json
	if !strings.Contains(string(written), "rotated-access") {
		t.Fatalf("rotated access token not persisted: %s", written)
	}
	if !strings.Contains(string(written), "refresh-idx-ka") {
		t.Fatalf("refresh token must be preserved when upstream does not rotate it: %s", written)
	}
}

// A rotated refresh token must be stored, otherwise the next refresh fails.
func TestKeepalivePersistsRotatedRefreshToken(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-rot", "workbuddy-CN-rot.json", "rot", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	stub := &keepaliveStub{body: `{"code":0,"msg":"OK","data":{"accessToken":"new-access","refreshToken":"new-refresh","expires_in":7200}}`}
	installKeepaliveStub(t, stub)

	if _, err := refreshOneAuth("idx-rot", "workbuddy-CN-rot.json"); err != nil {
		t.Fatalf("refreshOneAuth: %v", err)
	}
	saves := store.savedRecords()
	written := string(saves[len(saves)-1].json)
	if !strings.Contains(written, "new-refresh") {
		t.Fatalf("rotated refresh token must be persisted: %s", written)
	}
}

// A revoked offline session (12153) must disable the account so the scheduler
// stops routing traffic to it, and must say re-login is required.
func TestKeepaliveFlagsSessionDeadAccount(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-dead", "workbuddy-CN-dead.json", "dead", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	stub := &keepaliveStub{
		status: 401,
		body:   `{"code":12153,"msg":"Offline user session not found"}`,
	}
	installKeepaliveStub(t, stub)

	status, err := refreshOneAuth("idx-dead", "workbuddy-CN-dead.json")
	if status != "session-dead" {
		t.Fatalf("status = %q, want session-dead", status)
	}
	if err == nil {
		t.Fatal("session-dead must return an explanatory error")
	}
	saves := store.savedRecords()
	if len(saves) == 0 {
		t.Fatal("session-dead must persist the disabled flag")
	}
	var written struct {
		Disabled bool   `json:"disabled"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(saves[len(saves)-1].json, &written); err != nil {
		t.Fatalf("persisted record is not JSON: %v", err)
	}
	if !written.Disabled {
		t.Fatal("a dead session must disable the auth record")
	}
	if !strings.Contains(written.Note, "re-login") && !strings.Contains(written.Note, "重新登录") {
		t.Fatalf("note should tell the user to re-login, got %q", written.Note)
	}
	// The credential must survive: disabling is not deletion.
	if !strings.Contains(string(saves[len(saves)-1].json), "refresh-idx-dead") {
		t.Fatal("disabling must retain the credential material")
	}
}

// An ordinary 401 (not the 12153 marker) is a failure, not a dead session —
// disabling on a generic 401 would take healthy accounts offline.
func TestKeepaliveGenericAuthErrorDoesNotDisable(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-401", "workbuddy-CN-401.json", "401", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	stub := &keepaliveStub{status: 401, body: `{"code":40001,"msg":"invalid token"}`}
	installKeepaliveStub(t, stub)

	status, err := refreshOneAuth("idx-401", "workbuddy-CN-401.json")
	if status == "session-dead" {
		t.Fatal("a generic 401 must not be treated as a dead session")
	}
	if err == nil {
		t.Fatal("a failed refresh must report an error")
	}
	for _, s := range store.savedRecords() {
		var written struct {
			Disabled bool `json:"disabled"`
		}
		_ = json.Unmarshal(s.json, &written)
		if written.Disabled {
			t.Fatal("a generic 401 must not disable the account")
		}
	}
}

// An account without a refresh token is skipped, not failed: PAT-style
// credentials legitimately have nothing to refresh.
func TestKeepaliveSkipsAccountWithoutRefreshToken(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-nopat", "workbuddy-CN-nopat.json", "nopat", regionCN, false)
	// Overwrite with a credential that has no refresh token.
	store.mu.Lock()
	store.byIndex["idx-nopat"].json = []byte(`{"auth":{"accessToken":"only-access"},"account":{"uid":"nopat"}}`)
	store.mu.Unlock()
	installFakeAuthStore(t, store)
	resetCheckinState()

	stub := &keepaliveStub{body: `{"code":0,"msg":"OK","data":{}}`}
	installKeepaliveStub(t, stub)

	status, err := refreshOneAuth("idx-nopat", "workbuddy-CN-nopat.json")
	if status != "skipped" {
		t.Fatalf("status = %q, want skipped", status)
	}
	if err == nil {
		t.Fatal("skip should carry an explanatory reason")
	}
	if len(store.savedRecords()) != 0 {
		t.Fatal("a skipped refresh must not write the credential")
	}
}

// isSessionDeadError must match the documented markers only.
func TestIsSessionDeadErrorMarkers(t *testing.T) {
	if !isSessionDeadError(`{"code":12153,"msg":"Offline user session not found"}`) {
		t.Error("the 12153 / offline-session marker must be recognised")
	}
	if isSessionDeadError(`{"code":40001,"msg":"invalid token"}`) {
		t.Error("a generic auth error must not be a dead session")
	}
	if isSessionDeadError("") {
		t.Error("an empty body must not be a dead session")
	}
}
