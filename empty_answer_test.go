package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// ---------------------------------------------------------------------------
// 症状 1：上游返回「空回答」却被当成成功
//
// 被限流的 WorkBuddy 账号会回 HTTP 200 + 合法 SSE 框架，但没有任何内容：
// 只有 role / finish_reason / usage。旧代码把「至少有一个合法 JSON chunk」
// 当作成功，于是折叠出一个 content:"" 的正常回复交给客户端，账号也不会被
// 冷却 —— 用户看到的是「成功但什么都没有」，后续每个请求继续打到同一个
// 被限流的账号。这些测试锁定修复后的契约：空回答 = 限流（429）。
// ---------------------------------------------------------------------------

// framingOnlySSE is the exact throttled-account shape: valid framing, no output.
const framingOnlySSE = "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"id\":\"c1\",\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":0,\"total_tokens\":12}}\n\n" +
	"data: [DONE]\n\n"

// TestAggregateCompletionRejectsFramingOnlyStream: a 200 stream with framing but
// no content must NOT fold into a successful completion.
func TestAggregateCompletionRejectsFramingOnlyStream(t *testing.T) {
	out, err := aggregateCompletion(strings.NewReader(framingOnlySSE), "glm-5.2")
	if err == nil {
		t.Fatalf("framing-only stream must not be reported as success, got completion: %s", out)
	}
	var empty *emptyAnswerError
	if !errors.As(err, &empty) {
		t.Fatalf("error must be *emptyAnswerError, got %T: %v", err, err)
	}
	if empty.Stage != "execute" {
		t.Errorf("stage = %q, want execute", empty.Stage)
	}
}

// TestChatErrorStatusMapsEmptyAnswerToRateLimit is the contract CPA acts on:
// 429 makes it cool the credential and retry another account, 502 would only
// make the client back off.
func TestChatErrorStatusMapsEmptyAnswerToRateLimit(t *testing.T) {
	if got := chatErrorStatus(&emptyAnswerError{Stage: "execute"}, http.StatusBadGateway); got != http.StatusTooManyRequests {
		t.Errorf("empty answer status = %d, want 429", got)
	}
	// Wrapped errors must still classify (errors.As, not a type assertion).
	wrapped := errors.Join(errors.New("outer"), &emptyAnswerError{Stage: "pump"})
	if got := chatErrorStatus(wrapped, http.StatusBadGateway); got != http.StatusTooManyRequests {
		t.Errorf("wrapped empty answer status = %d, want 429", got)
	}
	// Unrelated failures keep the caller's fallback.
	if got := chatErrorStatus(errors.New("malformed stream"), http.StatusBadGateway); got != http.StatusBadGateway {
		t.Errorf("unrelated error status = %d, want the 502 fallback", got)
	}
	if got := chatErrorStatus(nil, http.StatusBadGateway); got != http.StatusBadGateway {
		t.Errorf("nil error status = %d, want the fallback", got)
	}
}

// TestEmptyAnswerErrorIsBilingual: the message reaches end users through CPA's
// error passthrough, so it must explain the cause in both languages.
func TestEmptyAnswerErrorIsBilingual(t *testing.T) {
	msg := (&emptyAnswerError{Stage: "collect"}).Error()
	if !strings.Contains(msg, "空响应") || !strings.Contains(msg, "empty answer") {
		t.Fatalf("message must be bilingual, got %q", msg)
	}
}

// TestChunkHasModelOutput pins the discriminator used by both streaming paths.
// It must accept real output (text / reasoning / tool calls) and reject the
// framing-only shapes (role-only delta, empty delta, usage-only chunk).
func TestChunkHasModelOutput(t *testing.T) {
	cases := []struct {
		name  string
		chunk string
		want  bool
	}{
		{"content delta", `{"choices":[{"delta":{"content":"hi"}}]}`, true},
		{"reasoning delta", `{"choices":[{"delta":{"reasoning_content":"think"}}]}`, true},
		{"legacy reasoning key", `{"choices":[{"delta":{"reasoning":"think"}}]}`, true},
		{"tool call", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"f","arguments":"{}"}}]}}]}`, true},
		{"legacy function_call", `{"choices":[{"delta":{"function_call":{"name":"f","arguments":"{}"}}}]}`, true},
		{"aggregated message", `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`, true},
		{"role only", `{"choices":[{"delta":{"role":"assistant"}}]}`, false},
		{"empty delta with finish", `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, false},
		{"content empty string", `{"choices":[{"delta":{"content":""}}]}`, false},
		{"empty tool_calls array", `{"choices":[{"delta":{"tool_calls":[]}}]}`, false},
		{"empty function_call shell", `{"choices":[{"delta":{"function_call":{"name":"","arguments":""}}}]}`, false},
		{"usage only", `{"choices":[],"usage":{"total_tokens":12}}`, false},
		{"no choices", `{"id":"c1"}`, false},
		{"invalid json", `not-json`, false},
	}
	for _, c := range cases {
		if got := chunkHasModelOutput(c.chunk); got != c.want {
			t.Errorf("%s: chunkHasModelOutput = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestCompletionHasOutput: tool-only turns are legitimate (content empty,
// tool_calls present), so the check must consider all three channels.
func TestCompletionHasOutput(t *testing.T) {
	cases := []struct {
		content, reasoning string
		tools              int
		want               bool
	}{
		{"hello", "", 0, true},
		{"", "thinking", 0, true},
		{"", "", 1, true},
		{"   ", "", 0, false},
		{"", "  ", 0, false},
		{"", "", 0, false},
	}
	for _, c := range cases {
		if got := completionHasOutput(c.content, c.reasoning, c.tools); got != c.want {
			t.Errorf("completionHasOutput(%q,%q,%d) = %v, want %v", c.content, c.reasoning, c.tools, got, c.want)
		}
	}
}

// TestAggregateSSEWithCollectorRejectsFramingOnlyStream covers the synchronous
// stream path (no async stream id), which returns the error to the host as an
// error envelope instead of an error frame.
func TestAggregateSSEWithCollectorRejectsFramingOnlyStream(t *testing.T) {
	_, err := aggregateSSEWithCollector(strings.NewReader(framingOnlySSE), false)
	if err == nil {
		t.Fatal("framing-only stream must not be collected as success")
	}
	var empty *emptyAnswerError
	if !errors.As(err, &empty) {
		t.Fatalf("error must be *emptyAnswerError, got %T: %v", err, err)
	}
	if empty.Stage != "collect" {
		t.Errorf("stage = %q, want collect", empty.Stage)
	}
}

// TestAggregateSSEWithCollectorKeepsRealOutput: the guard must not reject a
// normal stream, including a tool-only turn.
func TestAggregateSSEWithCollectorKeepsRealOutput(t *testing.T) {
	chunks, err := aggregateSSEWithCollector(strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"+
			"data: [DONE]\n\n"), false)
	if err != nil {
		t.Fatalf("normal stream rejected: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("normal stream produced no chunks")
	}
}

// TestPumpUpstreamStreamEmitsErrorForFramingOnlyStream drives the async pump
// end to end with a stubbed host bridge: the client must receive a terminal
// error frame (not a silent, apparently-successful empty answer).
func TestPumpUpstreamStreamEmitsErrorForFramingOnlyStream(t *testing.T) {
	restoreHTTP := stubStreamHostBridge(t, 200, framingOnlySSE)
	defer restoreHTTP()

	emitted := captureStreamEmits(t)

	stream, ok := registerAsyncStream("s-empty", func() {})
	if !ok {
		t.Fatal("stream registration failed")
	}
	req := mustReq(t, `{"model":"glm-5.2"}`)
	pumpUpstreamStream(req, stream, "s-empty", false, "uid-1", "auth-1", nil)

	payloads := emitted()
	if len(payloads) == 0 {
		t.Fatal("pump emitted nothing — the client would see a silent empty success")
	}
	joined := strings.Join(payloads, "\n")
	if !strings.Contains(joined, "空响应") {
		t.Fatalf("expected an empty-answer error frame, got: %s", joined)
	}
	// The failure MUST ride the chunk's error field: the host only marks a
	// request failed when that field is set. Sending it as payload instead made
	// the host record a throttled stream as SUCCEEDED (the reported bug).
	if !strings.Contains(joined, "[error]") {
		t.Fatalf("the error must be emitted on the `error` channel, not as payload — the host ignores payload errors: %s", joined)
	}
}

// TestPumpUpstreamStreamForwardsRealContent guards against the new check
// swallowing a normal streaming answer.
func TestPumpUpstreamStreamForwardsRealContent(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: [DONE]\n\n"
	restoreHTTP := stubStreamHostBridge(t, 200, body)
	defer restoreHTTP()

	emitted := captureStreamEmits(t)

	stream, ok := registerAsyncStream("s-ok", func() {})
	if !ok {
		t.Fatal("stream registration failed")
	}
	pumpUpstreamStream(mustReq(t, `{"model":"glm-5.2"}`), stream, "s-ok", false, "uid-1", "auth-1", nil)

	joined := strings.Join(emitted(), "\n")
	if !strings.Contains(joined, "hello") {
		t.Fatalf("real content was not forwarded: %s", joined)
	}
	if strings.Contains(joined, "空响应") {
		t.Fatalf("real content wrongly reported as an empty answer: %s", joined)
	}
}

// TestChunkHasModelOutputToleratesSSEFrame: the helper is called on payloads
// that may already carry their "data: " framing (cross-format clients). It must
// see through the prefix — treating a framed chunk as opaque would classify
// every Claude/Gemini/Codex stream as an empty answer.
func TestChunkHasModelOutputToleratesSSEFrame(t *testing.T) {
	for _, framed := range []string{
		`data: {"choices":[{"delta":{"content":"hi"}}]}`,
		`data:{"choices":[{"delta":{"content":"hi"}}]}`,
		`data: {"choices":[{"delta":{"content":""}}]}`,
	} {
		want := strings.Contains(framed, `"hi"`)
		if got := chunkHasModelOutput(framed); got != want {
			t.Errorf("chunkHasModelOutput(%q) = %v, want %v", framed, got, want)
		}
	}
}

// TestAggregateSSEWithCollectorFramedKeepsRealOutput is the regression lock for
// the ordering bug above: with sseFramed=true (every non-chat-completions entry
// path) a normal stream must NOT be rejected as empty.
func TestAggregateSSEWithCollectorFramedKeepsRealOutput(t *testing.T) {
	chunks, err := aggregateSSEWithCollector(strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"+
			"data: [DONE]\n\n"), true)
	if err != nil {
		t.Fatalf("framed stream with real content was rejected: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("framed stream produced no chunks")
	}
	if !strings.HasPrefix(string(chunks[0].Payload), "data: ") {
		t.Errorf("framed chunk lost its prefix: %s", chunks[0].Payload)
	}
}

// TestAggregateSSEWithCollectorFramedRejectsFramingOnlyStream: the empty-answer
// detection must still fire when the payload is SSE-framed.
func TestAggregateSSEWithCollectorFramedRejectsFramingOnlyStream(t *testing.T) {
	_, err := aggregateSSEWithCollector(strings.NewReader(framingOnlySSE), true)
	if err == nil {
		t.Fatal("framed framing-only stream must be rejected")
	}
	var empty *emptyAnswerError
	if !errors.As(err, &empty) {
		t.Fatalf("error must be *emptyAnswerError, got %T: %v", err, err)
	}
}

// TestPumpUpstreamStreamFramedKeepsRealContent drives the async pump with
// sseFramed=true (the cross-format client path) to prove the ordering bug is
// fixed end to end.
func TestPumpUpstreamStreamFramedKeepsRealContent(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"bonjour\"}}]}\n\n" +
		"data: [DONE]\n\n"
	restoreHTTP := stubStreamHostBridge(t, 200, body)
	defer restoreHTTP()

	emitted := captureStreamEmits(t)

	stream, ok := registerAsyncStream("s-framed", func() {})
	if !ok {
		t.Fatal("stream registration failed")
	}
	pumpUpstreamStream(mustReq(t, `{"model":"glm-5.2"}`), stream, "s-framed", true, "uid-1", "auth-1", nil)

	joined := strings.Join(emitted(), "\n")
	if !strings.Contains(joined, "bonjour") {
		t.Fatalf("framed real content was not forwarded: %s", joined)
	}
	if strings.Contains(joined, "空响应") {
		t.Fatalf("framed real content wrongly reported as an empty answer: %s", joined)
	}
}

// TestPumpUpstreamStreamFramedEmitsErrorForFramingOnlyStream: and the throttled
// shape must still be caught when framed.
func TestPumpUpstreamStreamFramedEmitsErrorForFramingOnlyStream(t *testing.T) {
	restoreHTTP := stubStreamHostBridge(t, 200, framingOnlySSE)
	defer restoreHTTP()

	emitted := captureStreamEmits(t)

	stream, ok := registerAsyncStream("s-framed-empty", func() {})
	if !ok {
		t.Fatal("stream registration failed")
	}
	pumpUpstreamStream(mustReq(t, `{"model":"glm-5.2"}`), stream, "s-framed-empty", true, "uid-1", "auth-1", nil)

	joined := strings.Join(emitted(), "\n")
	if !strings.Contains(joined, "空响应") {
		t.Fatalf("expected an empty-answer error frame, got: %s", joined)
	}
	if !strings.Contains(joined, "[error]") {
		t.Fatalf("the error must be emitted on the `error` channel, not as payload: %s", joined)
	}
}

// ---------------------------------------------------------------------------
// Test seams for the pump: a host HTTP stream stub plus an emit capture.
// ---------------------------------------------------------------------------

// stubStreamHostBridge makes hostHTTPDoStream return a buffered body with the
// given status, and records nothing else. Restore the returned func.
func stubStreamHostBridge(t *testing.T, status int, body string) func() {
	t.Helper()
	oldOverride := hostHTTPTestOverride
	hostHTTPTestOverride = func(*http.Request) (*hostHTTPResponse, error) {
		return &hostHTTPResponse{StatusCode: status, Body: []byte(body)}, nil
	}
	// The pump reads through hostHTTPDoStream, which in a unit-test process has
	// no bridge. Install a stream-level override by patching the call path.
	oldStream := hostStreamTestOverride
	hostStreamTestOverride = func(*http.Request) (io.ReadCloser, int, error) {
		return io.NopCloser(strings.NewReader(body)), status, nil
	}
	return func() {
		hostHTTPTestOverride = oldOverride
		hostStreamTestOverride = oldStream
	}
}

// captureStreamEmits records every payload the pump sends via host.stream.emit
// and returns an accessor. It also absorbs stream.close so the pump can finish.
//
// The wire carries Payload as a []byte, which json.Marshal renders as base64 —
// decode it so assertions can look at the actual chunk text.
func captureStreamEmits(t *testing.T) func() []string {
	t.Helper()
	var payloads []string
	oldRPC := hostRPCTestOverride
	hostRPCTestOverride = func(method string, request []byte) ([]byte, error) {
		switch method {
		case "host.stream.emit":
			var req struct {
				StreamID string `json:"stream_id"`
				Payload  []byte `json:"payload"`
				// The host reads Error to decide whether the request FAILED
				// (host_callbacks.go: chunk.Err = req.Error). Capturing it here
				// keeps the assertions honest about which channel is used.
				Error string `json:"error"`
			}
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, err
			}
			if req.Error != "" {
				payloads = append(payloads, "[error] "+req.Error)
			}
			if len(req.Payload) > 0 {
				payloads = append(payloads, string(req.Payload))
			}
			return okEnvelope(map[string]any{})
		case "host.stream.close":
			return okEnvelope(map[string]any{})
		default:
			return okEnvelope(map[string]any{})
		}
	}
	t.Cleanup(func() { hostRPCTestOverride = oldRPC })
	return func() []string { return payloads }
}

// ---------------------------------------------------------------------------
// 症状 1（第二形态）：模型目录 contextWindow 全为 0
//
// 被限流时目录接口仍回 200、JSON 仍能解析，但每个条目的 contextWindow 都是
// 0。旧代码照单全收，把「0 上下文」的目录缓存 5 分钟并下发给客户端（客户端
// 因此退回默认值、过早压缩上下文）。全 0 目录不是目录，是限流信号。
// ---------------------------------------------------------------------------

// TestDiscoveryContextDegraded pins the detector: only an all-zero payload of
// enabled entries counts, and individual omissions stay legitimate.
func TestDiscoveryContextDegraded(t *testing.T) {
	cases := []struct {
		name   string
		models []discoveredModel
		want   bool
	}{
		{"all zero", []discoveredModel{disc("a", "A", 0, false), disc("b", "B", 0, false)}, true},
		{"one real window", []discoveredModel{disc("a", "A", 0, false), disc("b", "B", 128000, false)}, false},
		{"disabled zeros ignored", []discoveredModel{disc("a", "A", 128000, false), disc("b", "B", 0, true)}, false},
		{"only disabled entries", []discoveredModel{disc("a", "A", 0, true)}, false},
		{"empty payload", nil, false},
		{"nameless entries", []discoveredModel{{ID: "", Disabled: false}}, false},
	}
	for _, c := range cases {
		if got := discoveryContextDegraded(c.models); got != c.want {
			t.Errorf("%s: discoveryContextDegraded = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestCallModelsAPIRejectsDegradedCatalog: a 200 with all-zero contextWindow
// must surface as a typed 429 so the caller falls back to the realm's static
// catalog instead of caching a context-free one.
func TestCallModelsAPIRejectsDegradedCatalog(t *testing.T) {
	restore := stubHostHTTPJSON(t, 200, `{"code":0,"data":{"models":[
		{"id":"glm-5.2","name":"GLM-5.2","disabled":false},
		{"id":"deepseek-v4-flash","name":"DeepSeek V4 Flash","disabled":false}
	],"agents":[{"name":"cli","models":["glm-5.2"]}]}}`)
	defer restore()
	restoreRetry := silenceDiscoveryRetries(t)
	defer restoreRetry()

	_, err := callModelsAPI("tok", "cn")
	if err == nil {
		t.Fatal("all-zero contextWindow catalog must not be accepted")
	}
	var ue *upstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error must be *upstreamError, got %T: %v", err, err)
	}
	if ue.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 (rate limit)", ue.StatusCode)
	}
	if !strings.Contains(ue.Snippet, "contextWindow") {
		t.Errorf("snippet must name the cause, got %q", ue.Snippet)
	}
}

// TestCallModelsAPIAcceptsHealthyCatalog: the guard must not fire for a normal
// catalog, including one with a few context-less entries.
func TestCallModelsAPIAcceptsHealthyCatalog(t *testing.T) {
	restore := stubHostHTTPJSON(t, 200, `{"code":0,"data":{"models":[
		{"id":"glm-5.2","name":"GLM-5.2","contextWindow":1000000,"disabled":false},
		{"id":"mystery","name":"Mystery","disabled":false}
	],"agents":[{"name":"cli","models":["glm-5.2","mystery"]}]}}`)
	defer restore()

	models, err := callModelsAPI("tok", "cn")
	if err != nil {
		t.Fatalf("healthy catalog rejected: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
}

// TestFetchDynamicModelsFallsBackOnDegradedCatalog is the end-to-end contract
// the user sees: a throttled directory yields the realm's static catalog (with
// real context windows) and records the reason for the panel.
func TestFetchDynamicModelsFallsBackOnDegradedCatalog(t *testing.T) {
	resetDynamicModelsCache()
	defer resetDynamicModelsCache()
	orig := discoverModelsFn
	defer func() { discoverModelsFn = orig }()

	discoverModelsFn = func(accessToken, realm string) ([]pluginapi.ModelInfo, error) {
		return nil, &upstreamError{StatusCode: http.StatusTooManyRequests, Path: "models", Snippet: "no usable contextWindow"}
	}
	got := fetchDynamicModelsFromStorage([]byte(`{"accessToken":"tok","region":"cn"}`))
	if len(got) != len(wbModels()) {
		t.Fatalf("degraded discovery must serve the CN static catalog: got %d want %d", len(got), len(wbModels()))
	}
	for _, m := range got {
		if m.ContextLength <= 0 {
			t.Errorf("static fallback must carry a real context window, model %q has %d", m.ID, m.ContextLength)
		}
	}
	st := realmModelStateFor("cn")
	if st == nil {
		t.Fatal("degraded discovery must record realm state")
	}
	if st.Source != "static (discovery failed)" {
		t.Errorf("state source = %q, want the discovery-failed static source", st.Source)
	}
	if !strings.Contains(st.LastError, "contextWindow") {
		t.Errorf("state last_error must name the cause, got %q", st.LastError)
	}
}

// ---------------------------------------------------------------------------
// Test seams for the discovery tests.
// ---------------------------------------------------------------------------

// stubHostHTTPJSON answers every bridged HTTP call with one canned JSON body.
func stubHostHTTPJSON(t *testing.T, status int, body string) func() {
	t.Helper()
	old := hostHTTPTestOverride
	hostHTTPTestOverride = func(*http.Request) (*hostHTTPResponse, error) {
		return &hostHTTPResponse{StatusCode: status, Headers: http.Header{}, Body: []byte(body)}, nil
	}
	return func() { hostHTTPTestOverride = old }
}

// silenceDiscoveryRetries removes the backoff ladder so the degraded-catalog
// test does not sleep for seconds while asserting the classification.
func silenceDiscoveryRetries(t *testing.T) func() {
	t.Helper()
	old := modelsDiscoveryRetryDelays
	modelsDiscoveryRetryDelays = nil
	return func() { modelsDiscoveryRetryDelays = old }
}
