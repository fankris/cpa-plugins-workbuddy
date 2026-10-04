package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestCountTokensWireShapeSurvivesHostTranslation is THE regression lock for the
// silent-zero bug that shipped in 0.9.32.
//
// The plugin declares ExecutorOutputFormats=["chat-completions"], so the host
// adapter translates its count payload with
// TranslateNonStream(openai → claude) — the openai→claude RESPONSE translator,
// which extracts `usage.prompt_tokens`. A Claude-native `{"input_tokens":N}`
// (what 0.9.32 returned) is silently rewritten to `usage.input_tokens: 0`
// before the client sees it, which is exactly the reported symptom: a real
// estimate computed locally, then thrown away in translation.
//
// The wire shape is asserted structurally here (a top-level "input_tokens" must
// NOT be the only field) and verified end-to-end against the real host
// translator in TestCountTokensWireShapeMatchesHostTranslator (build tag
// `hostcheck`, run against a CLIProxyAPI checkout).
func TestCountTokensWireShapeCarriesOpenAIUsage(t *testing.T) {
	out := countTokensWireShape(1234)
	var decoded struct {
		InputTokens *int64 `json:"input_tokens"`
		Usage       *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("wire shape is not valid JSON: %s (%v)", out, err)
	}
	if decoded.InputTokens != nil {
		t.Errorf("wire shape must not rely on a top-level input_tokens (the host's openai→claude translator drops it): %s", out)
	}
	if decoded.Usage == nil {
		t.Fatalf("wire shape must carry usage.prompt_tokens: %s", out)
	}
	if decoded.Usage.PromptTokens != 1234 || decoded.Usage.TotalTokens != 1234 {
		t.Errorf("usage.prompt_tokens/total_tokens not set from the count: %s", out)
	}
	if decoded.Usage.CompletionTokens != 0 {
		t.Errorf("completion_tokens must stay 0 for a count-only response: %s", out)
	}
}

// TestCountTokensWireShapeTracksEstimate: the dispatch path must put the real
// estimate on the wire, not a constant.
func TestCountTokensWireShapeTracksEstimate(t *testing.T) {
	short := countTokensWireShape(estimateInputTokens([]byte(`{"messages":[{"role":"user","content":"hi"}]}`)))
	long := countTokensWireShape(estimateInputTokens([]byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("w", 900) + `"}]}`)))
	if string(short) == string(long) {
		t.Fatalf("wire payload is a constant: short=%s long=%s", short, long)
	}
	var parsed struct {
		Usage struct {
			PromptTokens int64 `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(long, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Usage.PromptTokens < 200 {
		t.Errorf("900 chars should estimate well above 200 tokens, got %d", parsed.Usage.PromptTokens)
	}
}

// TestEmitWireShapeForHostCheck hands the REAL wire payload to
// scripts/verify-count-tokens-wire.sh, which runs it through the host's actual
// translator (the plugin module cannot import the host's internal/ packages, so
// this half has to happen in a checkout of CLIProxyAPI).
//
// Dormant unless WORKBUDDY_WIRESHAPE_OUT is set, so ordinary test runs ignore it.
func TestEmitWireShapeForHostCheck(t *testing.T) {
	out := os.Getenv("WORKBUDDY_WIRESHAPE_OUT")
	if out == "" {
		t.Skip("set WORKBUDDY_WIRESHAPE_OUT to emit the wire payload for the host-side check")
	}
	payload := countTokensWireShape(hostCheckSentinelCount)
	if err := os.WriteFile(out, payload, 0o600); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("emitted wire payload: %s", payload)
}

// hostCheckSentinelCount is an unmistakable number: the host-side check fails
// loudly if any translator rewrites or drops it.
const hostCheckSentinelCount = 4321

// ---------------------------------------------------------------------------
// count_tokens 不能再返回 0
//
// 客户端（Claude Code 每轮都调 POST /v1/messages/count_tokens）用这个数字做
// 上下文预算。旧实现硬编码 {"input_tokens":0}，等于告诉客户端「prompt 不占
// 空间」——客户端因此从不压缩历史，真正请求时才撞上游上限失败。这里锁定
// 「本地估算、偏保守高估、绝不为 0」的契约。
// ---------------------------------------------------------------------------

func TestEstimateInputTokensIsNeverZero(t *testing.T) {
	cases := map[string]string{
		"empty body":     ``,
		"empty object":   `{}`,
		"no text":        `{"model":"glm-5.2","stream":true}`,
		"invalid json":   `not-json`,
		"null":           `null`,
		"empty messages": `{"messages":[]}`,
	}
	for name, body := range cases {
		if got := estimateInputTokens([]byte(body)); got < 1 {
			t.Errorf("%s: estimateInputTokens = %d, must never be 0 (clients read 0 as unknown)", name, got)
		}
	}
}

func TestEstimateInputTokensScalesWithContent(t *testing.T) {
	short := estimateInputTokens([]byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	long := estimateInputTokens([]byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("word ", 300) + `"}]}`))
	if long <= short {
		t.Fatalf("estimate must grow with content: short=%d long=%d", short, long)
	}
	// The long prompt is ~1500 chars → /3 ≈ 500 tokens; assert the right ballpark
	// rather than an exact number so the heuristic can be tuned deliberately.
	if long < 300 || long > 700 {
		t.Errorf("estimate %d outside the expected range for ~1500 chars", long)
	}
}

func TestEstimateInputTokensCountsSystemMessagesAndTools(t *testing.T) {
	base := estimateInputTokens([]byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	withSystem := estimateInputTokens([]byte(`{"system":"` + strings.Repeat("s", 300) + `","messages":[{"role":"user","content":"hi"}]}`))
	if withSystem <= base {
		t.Errorf("system prompt not counted: base=%d withSystem=%d", base, withSystem)
	}
	withTools := estimateInputTokens([]byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","description":"` + strings.Repeat("d", 300) + `","parameters":{"type":"object"}}}]}`))
	if withTools <= base {
		t.Errorf("tool descriptions not counted: base=%d withTools=%d", base, withTools)
	}
	// Anthropic block-array shape.
	anthropic := estimateInputTokens([]byte(`{"system":[{"type":"text","text":"` + strings.Repeat("t", 300) + `"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`))
	if anthropic <= base {
		t.Errorf("Anthropic system blocks not counted: base=%d anthropic=%d", base, anthropic)
	}
}

// TestEstimateInputTokensIgnoresStructuralFields: roles/types/ids are protocol
// plumbing the upstream never bills for; counting them would inflate the
// estimate on every tool-heavy request.
func TestEstimateInputTokensIgnoresStructuralFields(t *testing.T) {
	plain := estimateInputTokens([]byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	noisy := estimateInputTokens([]byte(`{"model":"glm-5.2","stream":true,"temperature":0.7,"max_tokens":8192,` +
		`"metadata":{"user_id":"` + strings.Repeat("x", 500) + `"},` +
		`"messages":[{"role":"user","content":"hello","tool_call_id":"` + strings.Repeat("y", 500) + `"}]}`))
	if noisy != plain {
		t.Errorf("structural fields leaked into the estimate: plain=%d noisy=%d", plain, noisy)
	}
}

// TestEstimateInputTokensPayloadReadsExecutorEnvelope: the RPC envelope carries
// the body in Payload/OriginalRequest, and the response must be the JSON shape
// clients expect. The host marshals pluginapi.ExecutorRequest without JSON tags,
// so the wire uses the Go field names and []byte fields arrive base64-encoded —
// mustJSON (host_bridge.go) reproduces that exactly.
func TestEstimateInputTokensPayloadReadsExecutorEnvelope(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("a", 300) + `"}]}`)
	for _, raw := range [][]byte{
		mustJSON(map[string]any{"Payload": body}),
		mustJSON(map[string]any{"OriginalRequest": body}),
	} {
		out := estimateInputTokensPayload(raw)
		var resp struct {
			Usage struct {
				PromptTokens int64 `json:"prompt_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("response is not valid JSON: %s (%v)", out, err)
		}
		if resp.Usage.PromptTokens < 90 {
			t.Errorf("payload not measured: %d tokens", resp.Usage.PromptTokens)
		}
	}
}

// TestEstimateInputTokensPayloadPrefersPayloadOverOriginalRequest: the host
// sends both; the translated payload is the authoritative one.
func TestEstimateInputTokensPayloadPrefersPayloadOverOriginalRequest(t *testing.T) {
	short := []byte(`{"messages":[{"role":"user","content":"x"}]}`)
	long := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("z", 900) + `"}]}`)
	out := estimateInputTokensPayload(mustJSON(map[string]any{"Payload": long, "OriginalRequest": short}))
	var resp struct {
		Usage struct {
			PromptTokens int64 `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Usage.PromptTokens < 200 {
		t.Errorf("expected the longer payload to be measured, got %d", resp.Usage.PromptTokens)
	}
}

// TestEstimateInputTokensPayloadSurvivesGarbage: a malformed envelope must still
// produce a usable, non-zero answer instead of failing the RPC.
func TestEstimateInputTokensPayloadSurvivesGarbage(t *testing.T) {
	for _, raw := range []string{"", "not json", "[]", `{"Payload":"not-an-object"}`, `{"Payload":null}`} {
		out := estimateInputTokensPayload([]byte(raw))
		var resp struct {
			Usage struct {
				PromptTokens int64 `json:"prompt_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("raw=%q produced invalid JSON: %s", raw, out)
		}
		if resp.Usage.PromptTokens < 1 {
			t.Errorf("raw=%q produced %d, must be >= 1", raw, resp.Usage.PromptTokens)
		}
	}
}

// TestCountTokensDispatchNoLongerReturnsHardcodedZero is the regression lock on
// the RPC entry point itself.
func TestCountTokensDispatchNoLongerReturnsHardcodedZero(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("q", 600) + `"}]}`)
	raw, err := handleMethod("executor.count_tokens", mustJSON(map[string]any{"Payload": body}))
	if err != nil {
		t.Fatalf("handleMethod: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("count_tokens must succeed, got %s", env.Result)
	}
	var resp struct {
		Payload []byte `json:"Payload"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("result decode: %v (%s)", err, env.Result)
	}
	var inner struct {
		Usage struct {
			PromptTokens int64 `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(resp.Payload, &inner); err != nil {
		t.Fatalf("payload decode: %v (%s)", err, resp.Payload)
	}
	if inner.Usage.PromptTokens < 100 {
		t.Fatalf("count_tokens returned %d for a ~600-char prompt; the old hardcoded 0 is back", inner.Usage.PromptTokens)
	}
}
