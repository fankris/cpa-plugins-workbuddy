package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The device identity must be stable per account (anti-abuse: one account =
// one virtual machine, forever) and distinct across accounts (no cross-account
// correlation).
func TestFingerprintStableAndIsolated(t *testing.T) {
	uidA, uidB := "user-aaa", "user-bbb"

	if machineIDFor(uidA) != machineIDFor(uidA) {
		t.Fatal("machine id must be stable for the same uid")
	}
	if machineIDFor(uidA) == machineIDFor(uidB) {
		t.Fatal("different accounts must not share a machine id")
	}
	if sessionIDFor(uidA) == machineIDFor(uidA) {
		t.Fatal("machine and session ids must be derived independently")
	}
	if sessionIDFor(uidA) == sessionIDFor(uidB) {
		t.Fatal("different accounts must not share a session id")
	}
	if len(machineIDFor(uidA)) != 32 {
		t.Fatalf("machine id length = %d, want 32 hex chars", len(machineIDFor(uidA)))
	}
}

func TestRequestIDKeepsStablePrefixAndVariesSuffix(t *testing.T) {
	uid := "user-ccc"
	a := requestIDFor(uid)
	b := requestIDFor(uid)
	if !strings.HasPrefix(a, deriveDeviceID(uid, fingerprintSaltRequest)[:24]) {
		t.Fatalf("request id %q lacks the stable account prefix", a)
	}
	if len(a) != 24+1+6 {
		t.Fatalf("request id %q length = %d, want 31", a, len(a))
	}
	if a == b {
		// Two calls inside the same microsecond would be equal; require the
		// suffix to differ across a small sleep instead of flaking.
		time.Sleep(2 * time.Millisecond)
		if requestIDFor(uid) == a {
			t.Fatal("request id suffix must change over time")
		}
	}
}

func TestFingerprintHeadersApplied(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://example.invalid/x", nil)
	applyFingerprintHeaders("user-ddd", req.Header.Set)
	if req.Header.Get("X-Machine-ID") == "" || req.Header.Get("X-Session-ID") == "" || req.Header.Get("X-Request-ID") == "" {
		t.Fatalf("headers=%v", req.Header)
	}
	// Unknown UID: no fingerprint at all (never present a shared anonymous id).
	req2, _ := http.NewRequest(http.MethodPost, "https://example.invalid/x", nil)
	applyFingerprintHeaders("", req2.Header.Set)
	if req2.Header.Get("X-Machine-ID") != "" {
		t.Fatal("empty uid must not produce a machine id")
	}
}

// Auth headers must carry the fingerprint on every credential-bound request.
func TestAuthHeadersCarryFingerprint(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "at", Domain: "codebuddy.cn"},
		Account: storedAccount{UID: "user-eee"},
	}
	req, _ := http.NewRequest(http.MethodPost, "https://example.invalid/chat", nil)
	authHeadersFor(req, sa, false)
	if req.Header.Get("X-Machine-ID") != machineIDFor("user-eee") {
		t.Fatalf("auth headers missing stable machine id: %v", req.Header)
	}
}

// Expert/team events must rotate ids: the upstream dedupes by (eventCode, id)
// and repeating one id never advances progress.
func TestExpertEventsRotateDistinctIDs(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < len(expertIDPool); i++ {
		ev := buildGrowthEvent("expert", "user-fff", i, nil)
		id, _ := ev["id"].(string)
		if id == "" {
			t.Fatal("expert event without id")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate expert id %q at idx %d", id, i)
		}
		seen[id] = struct{}{}
	}
	teamSeen := map[string]struct{}{}
	for i := 0; i < len(teamIDPool); i++ {
		ev := buildGrowthEvent("team", "user-fff", i, nil)
		id, _ := ev["id"].(string)
		if _, dup := teamSeen[id]; dup {
			t.Fatalf("duplicate team id %q", id)
		}
		teamSeen[id] = struct{}{}
		if ev["expertType"] != "team" {
			t.Fatalf("team event expertType=%v", ev["expertType"])
		}
	}
}

func TestGrowthEventShapes(t *testing.T) {
	uid := "user-ggg"
	cases := map[string]string{
		"canvas":     "wbx_design_canvas_task_create",
		"template":   "agent_task_created_with_template",
		"skill":      "skill_info",
		"automation": "automated_task_create_suc",
		"playbook":   "playbook_prompt_send",
		"skin":       "appearance_skin_apply",
		"chat":       "chat_request_send",
		"glmchat":    "chat_request_send",
		"cat":        "chat_request_send",
		"lighthouse": "expert_actual_use",
	}
	for kind, wantCode := range cases {
		ev := buildGrowthEvent(kind, uid, 0, nil)
		if ev["eventCode"] != wantCode {
			t.Errorf("kind %s eventCode=%v want %s", kind, ev["eventCode"], wantCode)
		}
		if ev["userId"] != uid {
			t.Errorf("kind %s userId=%v", kind, ev["userId"])
		}
		if _, ok := ev["conversationId"]; !ok {
			t.Errorf("kind %s missing conversationId", kind)
		}
	}
	// Night task uses night mode; GLM task names the GLM model.
	cat := buildGrowthEvent("cat", uid, 0, nil)
	if cat["mode"] != "night" || cat["requestModelId"] != "glm-5.2" {
		t.Fatalf("cat event=%v", cat)
	}
	glm := buildGrowthEvent("glmchat", uid, 0, nil)
	if glm["requestModelId"] != "glm-5.2" {
		t.Fatalf("glmchat event=%v", glm)
	}
}

func TestGrowthEventKindMapping(t *testing.T) {
	if growthEventKindFor("chat_5") != "chat" {
		t.Fatal("chat_5 must map to chat")
	}
	if growthEventKindFor("Buddy_App") != "desktop_only" {
		t.Fatal("Buddy_App must be desktop-only (cannot be forged)")
	}
	if growthEventKindFor("Expert_Philanthropy") != "desktop_only" {
		t.Fatal("philanthropy must be desktop-only")
	}
	if growthEventKindFor("unknown_task") != "" {
		t.Fatal("unknown codes must not map to an event")
	}
}

func TestNightWindow(t *testing.T) {
	night := []int{23, 0, 3, 7}
	day := []int{8, 12, 18, 22}
	for _, h := range night {
		if !inNightWindow(time.Date(2026, 9, 19, h, 30, 0, 0, time.Local)) {
			t.Errorf("hour %d should be inside the night window", h)
		}
	}
	for _, h := range day {
		if inNightWindow(time.Date(2026, 9, 19, h, 30, 0, 0, time.Local)) {
			t.Errorf("hour %d should be outside the night window", h)
		}
	}
}

// Retired synthetic reporting cannot be enabled by a leftover configuration key.
func TestGrowthAutoConfigToggle(t *testing.T) {
	for _, raw := range []string{"", "growth_auto: false", "growth_auto: True"} {
		configure(configYAMLEnvelope(raw))
		if growthAutoEnabled() {
			t.Fatal("retired reporting was enabled")
		}
	}
}
