package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Buddy travel (猫猫旅行) is a state machine driven entirely by the upstream:
//   arrived  → claim
//   idle     → depart (requires a location_id from the config endpoint)
//   traveling→ nothing to do
// These tests drive each branch through the host HTTP seam, since the protocol
// shape (especially the mandatory depart body) is what broke in the field.

// travelStub answers the four travel endpoints. seen records the depart body so
// tests can assert the mandatory location_id is present.
type travelStub struct {
	state         string
	dailyLimit    bool
	locationName  string
	configID      any    // nil → config returns no destinations
	departStatus  int    // non-zero overrides the depart response status
	claimResponse string // optional override
	seen          map[string][]byte
	posts         map[string]int
}

func (s *travelStub) handler(req *http.Request) (*hostHTTPResponse, error) {
	if s.seen == nil {
		s.seen = map[string][]byte{}
	}
	if s.posts == nil {
		s.posts = map[string]int{}
	}
	body, _ := io.ReadAll(req.Body)
	s.seen[req.URL.Path] = body
	s.posts[req.URL.Path]++

	envelope := func(data string) *hostHTTPResponse {
		return &hostHTTPResponse{
			StatusCode: 200,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"code":0,"msg":"OK","data":` + data + `}`),
		}
	}

	switch req.URL.Path {
	case growthTravelStatusPath:
		loc := "{}"
		if s.locationName != "" {
			loc = `{"name":` + jsonString(s.locationName) + `}`
		}
		return envelope(`{"state":` + jsonString(s.state) +
			`,"daily_limit_reached":` + boolStr(s.dailyLimit) + `,"location":` + loc + `}`), nil

	case growthTravelConfigPath:
		if s.configID == nil {
			return envelope(`{"locations":[]}`), nil
		}
		raw, _ := json.Marshal(s.configID)
		return envelope(`{"locations":[{"id":` + string(raw) + `}]}`), nil

	case growthTravelDepartPath:
		if s.departStatus != 0 {
			return &hostHTTPResponse{StatusCode: s.departStatus, Body: []byte("invalid request")}, nil
		}
		return envelope(`{"location":{"name":"海边"}}`), nil

	case growthTravelClaimPath:
		if s.claimResponse != "" {
			return envelope(s.claimResponse), nil
		}
		return envelope(`{"reward_credit":30}`), nil
	}
	return &hostHTTPResponse{StatusCode: 404, Body: []byte("unexpected path " + req.URL.Path)}, nil
}

func jsonString(v string) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// installTravelStub wires the stub into the host HTTP seam.
func installTravelStub(t *testing.T, stub *travelStub) {
	t.Helper()
	old := hostHTTPTestOverride
	hostHTTPTestOverride = stub.handler
	t.Cleanup(func() { hostHTTPTestOverride = old })
}

func cnTravelAuth() *storedAuth {
	sa := &storedAuth{}
	sa.Auth.AccessToken = "token"
	sa.Auth.Region = regionCN
	sa.Account.UID = "u-travel"
	return sa
}

// arrived → claim, and the claimed credits invalidate the account cache.
func TestTravelClaimsWhenArrived(t *testing.T) {
	stub := &travelStub{state: "arrived"}
	installTravelStub(t, stub)
	resetAccountCache()
	accountCache.Store("u-travel", &accountCacheEntry{credits: &creditsSummary{TotalRemain: 1}})

	out := runBuddyTravel(cnTravelAuth())
	if out["ok"] != true || out["action"] != "claim" {
		t.Fatalf("arrived must claim, got %+v", out)
	}
	if credit, _ := out["credit"].(int64); credit != 30 {
		t.Fatalf("claim reward not surfaced, got %+v", out["credit"])
	}
	if stub.posts[growthTravelClaimPath] != 1 {
		t.Fatalf("expected exactly one claim POST, got %d", stub.posts[growthTravelClaimPath])
	}
	if stub.posts[growthTravelDepartPath] != 0 {
		t.Fatal("arrived must not also depart")
	}
}

// idle → depart, and the depart body MUST carry location_id (upstream 400s
// without it).
func TestTravelDepartsWhenIdleWithLocationID(t *testing.T) {
	stub := &travelStub{state: "idle", configID: float64(7)}
	installTravelStub(t, stub)

	out := runBuddyTravel(cnTravelAuth())
	if out["ok"] != true || out["action"] != "depart" {
		t.Fatalf("idle must depart, got %+v", out)
	}
	body := stub.seen[growthTravelDepartPath]
	if len(body) == 0 {
		t.Fatal("depart must send a body")
	}
	var payload struct {
		LocationID any `json:"location_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("depart body is not JSON: %v (%s)", err, body)
	}
	if payload.LocationID == nil {
		t.Fatalf("depart body must include location_id, got %s", body)
	}
	if id, _ := payload.LocationID.(float64); id != 7 {
		t.Fatalf("depart should use the configured destination, got %v", payload.LocationID)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "海边") {
		t.Fatalf("depart message should name the destination, got %q", msg)
	}
}

// Missing configuration is not permission to invent a destination.
func TestTravelMissingConfigDoesNotDepart(t *testing.T) {
	stub := &travelStub{state: "idle", configID: nil}
	installTravelStub(t, stub)
	out := runBuddyTravel(cnTravelAuth())
	if out["ok"] != false || stub.posts[growthTravelDepartPath] != 0 {
		t.Fatal(out, stub.posts)
	}
}

// traveling → no action at all (no claim, no depart).
func TestTravelNoopWhenTraveling(t *testing.T) {
	stub := &travelStub{state: "traveling"}
	installTravelStub(t, stub)

	out := runBuddyTravel(cnTravelAuth())
	if out["ok"] != true || out["action"] != "traveling" {
		t.Fatalf("traveling must be a no-op, got %+v", out)
	}
	if stub.posts[growthTravelClaimPath] != 0 || stub.posts[growthTravelDepartPath] != 0 {
		t.Fatal("traveling must not call claim or depart")
	}
}

// idle + daily limit reached → stop for the day, never depart again.
func TestTravelStopsAfterDailyLimit(t *testing.T) {
	stub := &travelStub{state: "idle", dailyLimit: true, configID: float64(1)}
	installTravelStub(t, stub)

	out := runBuddyTravel(cnTravelAuth())
	if out["ok"] != true || out["action"] != "idle" {
		t.Fatalf("daily-limit idle must be a clean stop, got %+v", out)
	}
	if stub.posts[growthTravelDepartPath] != 0 {
		t.Fatal("daily limit reached must not depart")
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "明日") {
		t.Fatalf("message should explain the reset, got %q", msg)
	}
}

// Upstream failures must surface as ok:false with a redacted message, never as
// a panic or a bogus success.
func TestTravelSurfacesUpstreamErrors(t *testing.T) {
	stub := &travelStub{state: "idle", configID: float64(1), departStatus: 400}
	installTravelStub(t, stub)

	out := runBuddyTravel(cnTravelAuth())
	if out["ok"] != false {
		t.Fatalf("a failed depart must report ok:false, got %+v", out)
	}
	if out["error"] == nil {
		t.Fatal("failure must carry an error message")
	}
}

// The travel loop is CN-only: Global/Intl accounts must be refused before any
// upstream call is made.
func TestTravelUnsupportedForNonCNAccounts(t *testing.T) {
	stub := &travelStub{state: "idle"}
	installTravelStub(t, stub)

	for _, region := range []string{regionGlobal, regionIntl} {
		sa := &storedAuth{}
		sa.Auth.Region = region
		sa.Account.UID = "u-" + region
		out := runBuddyTravel(sa)
		if out["ok"] != false {
			t.Errorf("region %s must not run buddy travel, got %+v", region, out)
		}
	}
	if len(stub.posts) != 0 {
		t.Fatalf("non-CN accounts must not reach the upstream, saw %v", stub.posts)
	}
}
