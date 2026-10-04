package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// errTestBridgeDown simulates a host that cannot service a callback.
var errTestBridgeDown = errors.New("host bridge unavailable")

// installHostRPTOverride swaps the host RPC table for one test and restores it.
func installHostRPTOverride(fn func(method string, request []byte) ([]byte, error)) func() {
	old := hostRPCTestOverride
	hostRPCTestOverride = fn
	return func() { hostRPCTestOverride = old }
}

// ---------------------------------------------------------------------------
// request.complete（RequestLifecyclePlugin）
//
// 补上 UsagePlugin 看不到的盲区：请求到达 CPA 但在执行器之前被拒绝时，
// 不产生 usage 记录，插件对"所有账号都被禁用"这件事完全无感。
// ---------------------------------------------------------------------------

func TestRequestCompleteCountsOutcomes(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	now := time.Now()
	for _, outcome := range []pluginapi.RequestCompletionOutcome{
		pluginapi.RequestCompletionSucceeded,
		pluginapi.RequestCompletionSucceeded,
		pluginapi.RequestCompletionFailed,
		pluginapi.RequestCompletionRejected,
		pluginapi.RequestCompletionCanceled,
	} {
		recordRequestCompletion(pluginapi.RequestCompletion{
			Outcome:     outcome,
			Model:       "hy4-preview",
			StartedAt:   now.Add(-2 * time.Second),
			CompletedAt: now,
		})
	}

	snap := requestLifecycleSnapshot()
	counts, _ := snap["counts"].(map[string]int64)
	if counts["succeeded"] != 2 {
		t.Errorf("succeeded = %d, want 2", counts["succeeded"])
	}
	if counts["failed"] != 1 || counts["rejected"] != 1 || counts["canceled"] != 1 {
		t.Errorf("outcome counts wrong: %v", counts)
	}
}

// TestRequestCompleteTracksFailStreak: a single failure is noise; a RUN of
// non-successes with no success between is the actionable "nothing is being
// served" signal.
//
// Note this counts failures, not rejects: verified against CPA v7.3.12,
// `rejected` is emitted only when an INTERCEPTOR terminates a request, so it is
// not the auth-exhaustion signal it looks like. A provider with no usable
// credential yields `failed`.
func TestRequestCompleteTracksFailStreak(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	fail := func() {
		recordRequestCompletion(pluginapi.RequestCompletion{Outcome: pluginapi.RequestCompletionFailed})
	}
	for i := 0; i < 4; i++ {
		fail()
	}
	snap := requestLifecycleSnapshot()
	if snap["all_failing"] != false {
		t.Errorf("4 failures must not yet flag all_failing (threshold is 5): %v", snap["all_failing"])
	}
	if snap["fail_streak"] != int64(4) {
		t.Errorf("fail_streak = %v, want 4", snap["fail_streak"])
	}

	fail() // 5th
	if snap = requestLifecycleSnapshot(); snap["all_failing"] != true {
		t.Errorf("5 consecutive failures must flag all_failing, got %v", snap["all_failing"])
	}

	// A success must clear the streak: the condition is "consecutive".
	recordRequestCompletion(pluginapi.RequestCompletion{Outcome: pluginapi.RequestCompletionSucceeded})
	if snap = requestLifecycleSnapshot(); snap["fail_streak"] != int64(0) {
		t.Errorf("a success must reset the streak, got %v", snap["fail_streak"])
	}
	if snap["all_failing"] != false {
		t.Errorf("all_failing must clear once a request succeeds")
	}
}

// TestRequestCompleteCancelDoesNotBreakStreak: a client disconnect proves
// nothing about the provider. It must neither extend the failure streak (it is
// not a failure) nor reset it (it is not a success).
func TestRequestCompleteCancelDoesNotBreakStreak(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	for i := 0; i < 3; i++ {
		recordRequestCompletion(pluginapi.RequestCompletion{Outcome: pluginapi.RequestCompletionFailed})
	}
	recordRequestCompletion(pluginapi.RequestCompletion{Outcome: pluginapi.RequestCompletionCanceled})
	snap := requestLifecycleSnapshot()
	if snap["fail_streak"] != int64(3) {
		t.Fatalf("a cancel must leave the streak intact, got %v", snap["fail_streak"])
	}

	// And rejects (interceptor terminations) DO count as non-successes.
	recordRequestCompletion(pluginapi.RequestCompletion{Outcome: pluginapi.RequestCompletionRejected})
	if snap = requestLifecycleSnapshot(); snap["fail_streak"] != int64(4) {
		t.Fatalf("a reject must extend the streak, got %v", snap["fail_streak"])
	}
}

// TestRequestCompleteRetainsFailuresNotSuccesses: the ring exists to show WHY
// requests fail. On a busy instance a ring that also kept successes would push
// the interesting events out within seconds.
func TestRequestCompleteRetainsFailuresNotSuccesses(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	recordRequestCompletion(pluginapi.RequestCompletion{Outcome: pluginapi.RequestCompletionSucceeded, Model: "ok"})
	recordRequestCompletion(pluginapi.RequestCompletion{
		Outcome: pluginapi.RequestCompletionFailed, Model: "bad", StatusCode: 429, Error: "rate limited",
	})

	snap := requestLifecycleSnapshot()
	events, _ := snap["events"].([]lifecycleEvent)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (failures only)", len(events))
	}
	if events[0].Model != "bad" || events[0].Status != 429 {
		t.Errorf("retained event wrong: %+v", events[0])
	}
	if !strings.Contains(events[0].Error, "rate limited") {
		t.Errorf("error text not retained: %q", events[0].Error)
	}
}

func TestRequestCompleteRingIsBounded(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	for i := 0; i < lifecycleMaxEvents+20; i++ {
		recordRequestCompletion(pluginapi.RequestCompletion{
			Outcome: pluginapi.RequestCompletionFailed,
			Error:   "e",
		})
	}
	snap := requestLifecycleSnapshot()
	events, _ := snap["events"].([]lifecycleEvent)
	if len(events) != lifecycleMaxEvents {
		t.Fatalf("ring holds %d events, want the cap %d", len(events), lifecycleMaxEvents)
	}
}

// TestRequestCompleteComputesDuration: StartedAt/CompletedAt are both provided
// by the host; the panel wants a duration, and deriving it here keeps the event
// self-describing if the host later stops sending one of the timestamps.
func TestRequestCompleteComputesDuration(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	start := time.Now().Add(-1500 * time.Millisecond)
	recordRequestCompletion(pluginapi.RequestCompletion{
		Outcome:     pluginapi.RequestCompletionFailed,
		StartedAt:   start,
		CompletedAt: start.Add(1500 * time.Millisecond),
	})
	snap := requestLifecycleSnapshot()
	events, _ := snap["events"].([]lifecycleEvent)
	if len(events) != 1 {
		t.Fatal("event missing")
	}
	if events[0].Duration != 1500 {
		t.Errorf("duration_ms = %d, want 1500", events[0].Duration)
	}
}

// TestRequestCompleteRPCAcceptsGarbage: the described request is already over,
// so a malformed event must be ACKNOWLEDGED — returning an error would mark the
// plugin faulty for something it cannot influence.
func TestRequestCompleteRPCAcceptsGarbage(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	for _, raw := range []string{"", "{not json", `{"Outcome":"failed"}`} {
		out, err := handleRequestComplete([]byte(raw))
		if err != nil {
			t.Fatalf("raw=%q must not error: %v", raw, err)
		}
		var env envelope
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("raw=%q invalid envelope: %s", raw, out)
		}
		if !env.OK {
			t.Fatalf("raw=%q must be acknowledged, got %s", raw, out)
		}
	}
}

// TestRequestCompleteDispatchIsRegistered locks the RPC method name: the host
// dispatches on the literal "request.complete".
func TestRequestCompleteDispatchIsRegistered(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	raw, err := handleMethod("request.complete", mustJSON(map[string]any{
		"Outcome": "rejected",
		"Model":   "hy4-preview",
	}))
	if err != nil {
		t.Fatalf("handleMethod: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("request.complete must succeed, got %s", env.Result)
	}
	snap := requestLifecycleSnapshot()
	counts, _ := snap["counts"].(map[string]int64)
	if counts["rejected"] != 1 {
		t.Fatalf("request.complete did not record: %v", counts)
	}
}

func TestRegistrationDeclaresRequestLifecycle(t *testing.T) {
	if !wbRegistration().Capabilities.RequestLifecyclePlugin {
		t.Fatal("RequestLifecyclePlugin must be declared or request.complete is never delivered")
	}
}

// TestTruncateForPanelIsRuneSafe: host error strings can contain CJK. A byte
// slice would split a multi-byte rune and emit invalid UTF-8 into the JSON.
func TestTruncateForPanelIsRuneSafe(t *testing.T) {
	long := strings.Repeat("错", 400)
	got := truncateForPanel(long, 300)
	if !utf8ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8")
	}
	if len([]rune(got)) != 301 { // 300 runes + the ellipsis
		t.Errorf("rune length = %d, want 301", len([]rune(got)))
	}
	if short := truncateForPanel("ok", 300); short != "ok" {
		t.Errorf("short string altered: %q", short)
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// host.auth.get_runtime
// ---------------------------------------------------------------------------

// TestHostAuthRuntimeForAdaptsCooldown: a future NextRetryAfter IS
// unavailability, even when the host has not yet set the boolean — the two
// fields are written at different points in the cooldown path, so deriving
// availability from the deadline is the safe reading.
func TestHostAuthRuntimeForAdaptsCooldown(t *testing.T) {
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		if method != "host.auth.get_runtime" {
			return nil, nil
		}
		payload := map[string]any{
			"auth": map[string]any{
				"auth_index":       "idx-1",
				"status":           "error",
				"status_message":   "quota",
				"unavailable":      false, // host has NOT flagged it yet
				"next_retry_after": time.Now().Add(90 * time.Second).Format(time.RFC3339Nano),
				"priority":         2,
				"success":          10,
				"failed":           3,
			},
		}
		return mustJSON(map[string]any{"ok": true, "result": payload}), nil
	})
	defer restore()

	got := hostAuthRuntimeFor("idx-1")
	if got == nil {
		t.Fatal("runtime view missing")
	}
	if !got.Unavailable {
		t.Error("a future cooldown deadline must read as unavailable")
	}
	if got.CooldownSeconds <= 0 || got.CooldownSeconds > 90 {
		t.Errorf("cooldown_seconds = %d, want ~90", got.CooldownSeconds)
	}
	if got.Status != "error" || got.Priority != 2 || got.Success != 10 || got.Failed != 3 {
		t.Errorf("fields not mapped: %+v", got)
	}
}

// TestHostAuthRuntimeForDegradesQuietly: a host without the RPC, or a credential
// deleted mid-flight, must yield nil rather than an error — the panel falls back
// to plugin-side state instead of failing to render.
func TestHostAuthRuntimeForDegradesQuietly(t *testing.T) {
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		return mustJSON(map[string]any{"ok": false, "error": map[string]any{"code": "unknown_method"}}), nil
	})
	defer restore()

	if got := hostAuthRuntimeFor("idx-x"); got != nil {
		t.Fatalf("unsupported host must yield nil, got %+v", got)
	}
	if got := hostAuthRuntimeFor(""); got != nil {
		t.Fatalf("empty auth_index must yield nil, got %+v", got)
	}
}

// TestHostAuthRuntimeForExpiredCooldownIsAvailable: a PAST deadline must not
// render as cooling — that would show a stale cooldown forever.
func TestHostAuthRuntimeForExpiredCooldownIsAvailable(t *testing.T) {
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		return mustJSON(map[string]any{"ok": true, "result": map[string]any{
			"auth": map[string]any{
				"auth_index":       "idx-2",
				"status":           "active",
				"next_retry_after": time.Now().Add(-time.Minute).Format(time.RFC3339Nano),
			},
		}}), nil
	})
	defer restore()

	got := hostAuthRuntimeFor("idx-2")
	if got == nil {
		t.Fatal("runtime view missing")
	}
	if got.Unavailable {
		t.Error("an expired cooldown must not read as unavailable")
	}
	if got.CooldownSeconds != 0 {
		t.Errorf("cooldown_seconds = %d, want 0", got.CooldownSeconds)
	}
}

// TestHostAuthRuntimeForFlagsStaleCounts: a credential not refreshed in over a
// day gets a hint that its counters may be outdated.
func TestHostAuthRuntimeForFlagsStaleCounts(t *testing.T) {
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		return mustJSON(map[string]any{"ok": true, "result": map[string]any{
			"auth": map[string]any{
				"auth_index":   "idx-3",
				"last_refresh": time.Now().Add(-48 * time.Hour).Format(time.RFC3339Nano),
			},
		}}), nil
	})
	defer restore()

	got := hostAuthRuntimeFor("idx-3")
	if got == nil || !got.Stale {
		t.Fatalf("stale counts not flagged: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// host.log
// ---------------------------------------------------------------------------

// TestHostLogFallsBackWhenBridgeUnavailable: the bridge is an improvement, not a
// requirement. Losing diagnostics because host.log is missing would be a bad
// trade, so the fallback must not panic or swallow the message.
func TestHostLogFallsBackWhenBridgeUnavailable(t *testing.T) {
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		return nil, errTestBridgeDown
	})
	defer restore()

	// Must not panic.
	hostLog(logLevelWarn, "bridge down", map[string]any{"k": "v"})
	hostLogf(logLevelInfo, "formatted %d", 42)
	hostLog("", "", nil) // empty message: no-op
}

// TestHostLogSendsLevelMessageAndFields locks the wire shape the host decodes
// (rpcHostLogRequest: level / message / fields).
func TestHostLogSendsLevelMessageAndFields(t *testing.T) {
	var captured []byte
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		if method == "host.log" {
			captured = request
		}
		return mustJSON(map[string]any{"ok": true, "result": map[string]any{}}), nil
	})
	defer restore()

	hostLog(logLevelError, "boom", map[string]any{"account": "a@b.c"})
	if len(captured) == 0 {
		t.Fatal("host.log was not called")
	}
	var decoded struct {
		Level   string         `json:"level"`
		Message string         `json:"message"`
		Fields  map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(captured, &decoded); err != nil {
		t.Fatalf("payload: %v (%s)", err, captured)
	}
	if decoded.Level != "error" || decoded.Message != "boom" {
		t.Errorf("level/message wrong: %+v", decoded)
	}
	if decoded.Fields["account"] != "a@b.c" {
		t.Errorf("fields not sent: %+v", decoded.Fields)
	}
}

// ---------------------------------------------------------------------------
// streamEmitError 的线形
//
// 这是"限流时仍显示成功"的根因：宿主只在 chunk 的 `error` 字段非空时才把请求
// 标记为失败（host_callbacks.go: `if req.Error != "" { chunk.Err = ... }`）。
// 把错误当 payload 发出去，宿主视作普通模型输出 → 请求记为成功 → 凭据永不冷却。
// ---------------------------------------------------------------------------

// TestStreamEmitErrorUsesErrorFieldNotPayload is THE regression lock.
func TestStreamEmitErrorUsesErrorFieldNotPayload(t *testing.T) {
	var captured []byte
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		if method == "host.stream.emit" {
			captured = request
		}
		return mustJSON(map[string]any{"ok": true, "result": map[string]any{}}), nil
	})
	defer restore()

	streamEmitError("stream-1", "upstream returned an empty answer (throttled)")

	if len(captured) == 0 {
		t.Fatal("host.stream.emit was not called")
	}
	var decoded struct {
		StreamID string `json:"stream_id"`
		Payload  []byte `json:"payload"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(captured, &decoded); err != nil {
		t.Fatalf("payload: %v (%s)", err, captured)
	}
	if decoded.Error == "" {
		t.Fatalf("the failure MUST ride the `error` field — the host only marks a request failed when it is set; got %s", captured)
	}
	if len(decoded.Payload) != 0 {
		t.Errorf("payload must stay empty on an error emit, got %s", decoded.Payload)
	}
	if decoded.StreamID != "stream-1" {
		t.Errorf("stream_id = %q", decoded.StreamID)
	}
	if !strings.Contains(decoded.Error, "throttled") {
		t.Errorf("error message lost: %q", decoded.Error)
	}
}

// TestStreamEmitErrorRedactsSecrets: the host copies this string into its error
// path, and upstream bodies can carry Bearer/JWT.
func TestStreamEmitErrorRedactsSecrets(t *testing.T) {
	var captured []byte
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		if method == "host.stream.emit" {
			captured = request
		}
		return mustJSON(map[string]any{"ok": true, "result": map[string]any{}}), nil
	})
	defer restore()

	streamEmitError("s2", `upstream 401: {"Authorization":"Bearer sk-abcdefghijklmnopqrstuvwxyz012345"}`)

	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(captured, &decoded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(decoded.Error, "sk-abcdefghijklmnopqrstuvwxyz012345") {
		t.Fatalf("secret leaked into the host error path: %q", decoded.Error)
	}
}

// TestStreamEmitErrorIgnoresEmptyStreamID: no stream to report to.
func TestStreamEmitErrorIgnoresEmptyStreamID(t *testing.T) {
	called := false
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		if method == "host.stream.emit" {
			called = true
		}
		return mustJSON(map[string]any{"ok": true, "result": map[string]any{}}), nil
	})
	defer restore()

	streamEmitError("", "boom")
	if called {
		t.Error("an empty stream id must not emit")
	}
}

// ---------------------------------------------------------------------------
// 每账号健康条：数据来自宿主，不由插件统计
//
// 实测结论（决定了这里的断言方向）：宿主已经按凭据维护 20 × 10 分钟的
// success/failed 分桶（sdk/cliproxy/auth RecentRequestsSnapshot），把每个被
// 路由到该凭据的请求记进去，并通过 host.auth.get_runtime 下发。
//
// 所以插件**只做展示**。自己统计是错的：只能看到到达本插件执行器的请求，
// 会漏掉从未到达的（全部凭据不可用时宿主直接拒绝），也无法归属到账号。
// ---------------------------------------------------------------------------

// TestHostAuthRuntimeCarriesHealthSeries locks the pass-through: the host's
// recent_requests must reach the panel payload unchanged.
func TestHostAuthRuntimeCarriesHealthSeries(t *testing.T) {
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		if method != "host.auth.get_runtime" {
			return nil, nil
		}
		return mustJSON(map[string]any{"ok": true, "result": map[string]any{
			"auth": map[string]any{
				"auth_index": "idx-h",
				"status":     "active",
				"recent_requests": []map[string]any{
					{"time": "19:20-19:30", "success": 7, "failed": 3},
					{"time": "19:30-19:40", "success": 0, "failed": 0},
					{"time": "19:40-19:50", "success": 2, "failed": 0},
				},
			},
		}}), nil
	})
	defer restore()

	got := hostAuthRuntimeFor("idx-h")
	if got == nil {
		t.Fatal("runtime view missing")
	}
	if len(got.Health) != 3 {
		t.Fatalf("health buckets = %d, want 3 (passed through unchanged)", len(got.Health))
	}
	if got.Health[0].Label != "19:20-19:30" || got.Health[0].Success != 7 || got.Health[0].Failed != 3 {
		t.Errorf("bucket 0 not mapped: %+v", got.Health[0])
	}
	// An all-zero bucket is the host's way of saying "no traffic"; it must
	// survive rather than be dropped, or the bar would lose its time axis.
	if got.Health[1].Success != 0 || got.Health[1].Failed != 0 {
		t.Errorf("idle bucket altered: %+v", got.Health[1])
	}
}

// TestHostAuthRuntimeWithoutHealthIsStillUsable: a host that predates the
// series, or a credential with no traffic yet, must yield a usable runtime view
// with an empty Health — not nil and not an error.
func TestHostAuthRuntimeWithoutHealthIsStillUsable(t *testing.T) {
	restore := installHostRPTOverride(func(method string, request []byte) ([]byte, error) {
		return mustJSON(map[string]any{"ok": true, "result": map[string]any{
			"auth": map[string]any{"auth_index": "idx-n", "status": "active"},
		}}), nil
	})
	defer restore()

	got := hostAuthRuntimeFor("idx-n")
	if got == nil {
		t.Fatal("runtime view missing")
	}
	if len(got.Health) != 0 {
		t.Errorf("health should be empty, got %d buckets", len(got.Health))
	}
	if got.Status != "active" {
		t.Errorf("status lost when health absent: %q", got.Status)
	}
}

// TestPluginDoesNotKeepItsOwnHealthBuckets is a structural guard against
// reintroducing the duplicate implementation. The plugin must not export a
// provider-wide health series: only the host has per-credential routing data.
func TestPluginDoesNotKeepItsOwnHealthBuckets(t *testing.T) {
	resetRequestLifecycleForTest()
	t.Cleanup(resetRequestLifecycleForTest)

	recordRequestCompletion(pluginapi.RequestCompletion{
		Outcome: pluginapi.RequestCompletionFailed, CompletedAt: time.Now(),
	})
	snap := requestLifecycleSnapshot()
	if _, ok := snap["buckets"]; ok {
		t.Fatal("the plugin must not publish its own health buckets — the host owns that series (see panel.go hostAuthRuntimeFor)")
	}
	// The failure still has to be visible in the event ring, which is what the
	// plugin's own request.complete handling is actually for.
	if _, ok := snap["events"]; !ok {
		t.Fatal("event ring missing")
	}
}

// ---------------------------------------------------------------------------
// streamEmitError 的线形
//
// 这是"限流时仍显示成功"的根因：宿主只在 chunk 的 `error` 字段非空时才把请求
// 标记为失败（host_callbacks.go: `if req.Error != "" { chunk.Err = ... }`）。
// 把错误当 payload 发出去，宿主视作普通模型输出 → 请求记为成功 → 凭据永不冷却。
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
