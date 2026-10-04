package main

import (
	"encoding/json"
	"testing"
)

func mustMessages(t *testing.T, body string) []any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	msgs, _ := obj["messages"].([]any)
	return msgs
}

// --- tool pairing repair ---

func TestTrimOrphanToolCalls_DropsUnansweredCall(t *testing.T) {
	msgs := mustMessages(t, `{"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"calling","tool_calls":[{"id":"call_1","type":"function","function":{"name":"run","arguments":"{}"}}]},
		{"role":"user","content":"continue"}
	]}`)
	out, changed := trimOrphanToolCalls(msgs)
	if !changed {
		t.Fatal("expected a change")
	}
	if len(out) != 3 {
		t.Fatalf("message count = %d, want 3 (user stays)", len(out))
	}
	assistant := out[1].(map[string]any)
	if _, has := assistant["tool_calls"]; has {
		t.Error("orphan tool_calls must be removed")
	}
	if assistant["content"] != "calling" {
		t.Errorf("assistant text content must survive, got %v", assistant["content"])
	}
}

func TestTrimOrphanToolCalls_DropsResultWithoutCall(t *testing.T) {
	msgs := mustMessages(t, `{"messages":[
		{"role":"user","content":"hi"},
		{"role":"tool","tool_call_id":"ghost","content":"stale result"},
		{"role":"user","content":"continue"}
	]}`)
	out, changed := trimOrphanToolCalls(msgs)
	if !changed {
		t.Fatal("expected a change")
	}
	for _, m := range out {
		if stringRole(m.(map[string]any)) == "tool" {
			t.Error("orphan tool result must be removed")
		}
	}
}

func TestTrimOrphanToolCalls_KeepsPairedCalls(t *testing.T) {
	body := `{"messages":[
		{"role":"assistant","tool_calls":[{"id":"a","function":{"name":"x","arguments":"{}"}},{"id":"b","function":{"name":"y","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"a","content":"one"},
		{"role":"tool","tool_call_id":"b","content":"two"}
	]}`
	msgs := mustMessages(t, body)
	out, changed := trimOrphanToolCalls(msgs)
	if changed {
		t.Fatal("paired history must not change")
	}
	if len(out) != 3 {
		t.Fatalf("message count = %d, want 3", len(out))
	}
}

func TestTrimOrphanToolCalls_TrimsWithinABatch(t *testing.T) {
	msgs := mustMessages(t, `{"messages":[
		{"role":"assistant","tool_calls":[{"id":"a"},{"id":"b"}]},
		{"role":"tool","tool_call_id":"a","content":"one"},
		{"role":"tool","tool_call_id":"c","content":"unrelated"}
	]}`)
	out, changed := trimOrphanToolCalls(msgs)
	if !changed {
		t.Fatal("expected a change")
	}
	assistant := out[0].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["id"] != "a" {
		t.Errorf("only the answered call should survive, got %v", calls)
	}
}

func TestRepackToolResultBlocks_MovesIntruderBehindBatch(t *testing.T) {
	msgs := mustMessages(t, `{"messages":[
		{"role":"assistant","tool_calls":[{"id":"a"},{"id":"b"}]},
		{"role":"tool","tool_call_id":"a","content":"one"},
		{"role":"user","content":"image_resize_notice"},
		{"role":"tool","tool_call_id":"b","content":"two"}
	]}`)
	out, changed := repackToolResultBlocks(msgs)
	if !changed {
		t.Fatal("split batch must be repacked")
	}
	roles := []string{}
	for _, m := range out {
		roles = append(roles, stringRole(m.(map[string]any)))
	}
	want := []string{"assistant", "tool", "tool", "user"}
	for i, w := range want {
		if roles[i] != w {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
	// Same results, same relative order — only the intruder moved.
	second := out[1].(map[string]any)
	third := out[2].(map[string]any)
	if second["tool_call_id"] != "a" || third["tool_call_id"] != "b" {
		t.Errorf("result order must be preserved, got %v then %v", second["tool_call_id"], third["tool_call_id"])
	}
}

func TestRepackToolResultBlocks_HealthyHistoryUnchanged(t *testing.T) {
	msgs := mustMessages(t, `{"messages":[
		{"role":"assistant","tool_calls":[{"id":"a"}]},
		{"role":"tool","tool_call_id":"a","content":"one"},
		{"role":"user","content":"next"}
	]}`)
	if _, changed := repackToolResultBlocks(msgs); changed {
		t.Fatal("healthy history must not change")
	}
}

func TestRepairToolPairingInPlace_FullPipeline(t *testing.T) {
	var obj map[string]any
	body := `{"model":"m","messages":[
		{"role":"assistant","tool_calls":[{"id":"a"},{"id":"b"}]},
		{"role":"tool","tool_call_id":"a","content":"one"},
		{"role":"user","content":"interrupt"},
		{"role":"tool","tool_call_id":"c","content":"unrelated"},
		{"role":"assistant","tool_calls":[{"id":"ghost"}]}
	]}`
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatal(err)
	}
	if !repairToolPairingInPlace(obj) {
		t.Fatal("expected repair to report changes")
	}
	msgs := obj["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, stringRole(m.(map[string]any)))
	}
	// The foreign result c stops the repack scan, so the interrupting user
	// stays where it is; trim then drops call b (no result), the orphan
	// result c, and the ghost-only assistant.
	want := []string{"assistant", "tool", "user", "assistant"}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	for i, w := range want {
		if roles[i] != w {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
	first := msgs[0].(map[string]any)
	calls := first["tool_calls"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["id"] != "a" {
		t.Errorf("only the answered call should survive, got %v", calls)
	}
}

// The classic broken shape: a user turn wedged between the call batch and its
// results. Repack must move it behind the results so the upstream's pairing
// check passes instead of rejecting every later turn (code 11148).
func TestRepackToolResultBlocks_IntruderBeforeFirstResult(t *testing.T) {
	msgs := mustMessages(t, `{"messages":[
		{"role":"assistant","tool_calls":[{"id":"a"},{"id":"b"}]},
		{"role":"user","content":"interrupt"},
		{"role":"tool","tool_call_id":"a","content":"one"},
		{"role":"tool","tool_call_id":"b","content":"two"}
	]}`)
	out, changed := repackToolResultBlocks(msgs)
	if !changed {
		t.Fatal("split batch must be repacked")
	}
	roles := []string{}
	for _, m := range out {
		roles = append(roles, stringRole(m.(map[string]any)))
	}
	want := []string{"assistant", "tool", "tool", "user"}
	for i, w := range want {
		if roles[i] != w {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
	// Same results, same relative order — only the intruder moved.
	first := out[1].(map[string]any)
	second := out[2].(map[string]any)
	if first["tool_call_id"] != "a" || second["tool_call_id"] != "b" {
		t.Errorf("result order must be preserved, got %v then %v", first["tool_call_id"], second["tool_call_id"])
	}
}

// --- deepseek reasoning backfill + effort injection ---

func setEffortCache(t *testing.T, entries map[string]string) {
	t.Helper()
	modelDefaultEffortsMu.Lock()
	prev := map[string]string{}
	for k, v := range modelDefaultEfforts {
		prev[k] = v
	}
	for k, v := range entries {
		modelDefaultEfforts[k] = v
	}
	modelDefaultEffortsMu.Unlock()
	t.Cleanup(func() {
		modelDefaultEffortsMu.Lock()
		modelDefaultEfforts = prev
		modelDefaultEffortsMu.Unlock()
	})
}

func TestApplyDeepSeekReasoning_BackfillsAssistantMessages(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	var obj map[string]any
	body := `{"model":"deepseek-v4.1-flash","messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"part one"},
		{"role":"user","content":"go on"}
	]}`
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatal(err)
	}
	if !applyDeepSeekReasoningInPlace(obj, "deepseek-v4.1-flash") {
		t.Fatal("expected changes")
	}
	msgs := obj["messages"].([]any)
	assistant := msgs[1].(map[string]any)
	if rc, ok := assistant["reasoning_content"].(string); !ok {
		t.Errorf("assistant must carry string reasoning_content, got %T", assistant["reasoning_content"])
	} else if rc != "" {
		t.Errorf("backfilled reasoning_content should be empty string, got %q", rc)
	}
	if r, ok := assistant["reasoning"].(string); !ok || r == "" {
		t.Errorf("reasoning mirror must be non-empty, got %v", assistant["reasoning"])
	}
	// Injection: thinking on + default effort.
	thinking, ok := obj["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Errorf("thinking must be injected as enabled, got %v", obj["thinking"])
	}
	if obj["reasoning_effort"] != "high" {
		t.Errorf("default effort = %v, want high", obj["reasoning_effort"])
	}
}

func TestApplyDeepSeekReasoning_UsesCatalogDefaultEffort(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "low"})
	obj := map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": []any{},
	}
	applyDeepSeekReasoningInPlace(obj, "deepseek-v4.1-flash")
	if obj["reasoning_effort"] != "low" {
		t.Errorf("catalog-declared effort = %v, want low", obj["reasoning_effort"])
	}
}

func TestApplyDeepSeekReasoning_EnablesPartialThinkingOptions(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	cases := []struct {
		name    string
		options map[string]any
	}{
		{name: "empty thinking object", options: map[string]any{}},
		{name: "partial options", options: map[string]any{"budget_tokens": 8192, "custom": "preserve"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := make(map[string]any, len(tc.options))
			for key, value := range tc.options {
				options[key] = value
			}
			obj := map[string]any{
				"model":    "deepseek-v4.1-flash",
				"thinking": options,
				"messages": []any{},
			}
			if !applyDeepSeekReasoningInPlace(obj, "deepseek-v4.1-flash") {
				t.Fatal("expected thinking mode to be normalized")
			}
			got, ok := obj["thinking"].(map[string]any)
			if !ok || got["type"] != "enabled" {
				t.Fatalf("thinking = %v, want type=enabled", obj["thinking"])
			}
			for key, want := range tc.options {
				if got[key] != want {
					t.Errorf("thinking[%q] = %v, want preserved value %v", key, got[key], want)
				}
			}
			if obj["reasoning_effort"] != "high" {
				t.Errorf("reasoning_effort = %v, want high", obj["reasoning_effort"])
			}
		})
	}
}

func TestApplyDeepSeekReasoning_RespectsExplicitEffort(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	obj := map[string]any{
		"model":            "deepseek-v4.1-flash",
		"reasoning_effort": "medium",
		"messages":         []any{},
	}
	// Reference behavior: an explicit effort is never overridden, but thinking
	// still gets injected when the field was absent (effort alone yields no
	// trace on this upstream).
	if obj["reasoning_effort"] != "medium" {
		t.Errorf("effort = %v, want medium", obj["reasoning_effort"])
	}
	applyDeepSeekReasoningInPlace(obj, "deepseek-v4.1-flash")
	if obj["reasoning_effort"] != "medium" {
		t.Errorf("explicit effort was overridden to %v, want medium", obj["reasoning_effort"])
	}
	thinking, ok := obj["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Errorf("thinking = %v, want enabled (effort alone yields no trace)", obj["thinking"])
	}
}

func TestApplyDeepSeekReasoning_HonorsOptOuts(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	// Only thinking.type="disabled" opts out. "none"/"off" are not upstream
	// tiers: they resolve to the model's automatic default instead.
	obj := map[string]any{
		"model":    "deepseek-v4.1-flash",
		"thinking": map[string]any{"type": "disabled"},
		"messages": []any{},
	}
	applyDeepSeekReasoningInPlace(obj, "deepseek-v4.1-flash")
	thinking, ok := obj["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Errorf("thinking disabled must be left exactly as sent, got %v", obj["thinking"])
	}
	if _, has := obj["reasoning_effort"]; has {
		t.Errorf("effort must not be injected for a disabled-thinking request, got %v", obj["reasoning_effort"])
	}

	none := map[string]any{
		"model":            "deepseek-v4.1-flash",
		"reasoning_effort": "none",
		"messages":         []any{},
	}
	applyDeepSeekReasoningInPlace(none, "deepseek-v4.1-flash")
	if none["reasoning_effort"] != "high" {
		t.Errorf(`"none" must resolve to the automatic default, got %v`, none["reasoning_effort"])
	}
	if thinking, ok := none["thinking"].(map[string]any); !ok || thinking["type"] != "enabled" {
		t.Errorf("resolved automatic effort must still enable thinking, got %v", none["thinking"])
	}
}

func TestApplyDeepSeekReasoning_NonDeepSeekNoop(t *testing.T) {
	obj := map[string]any{
		"model":    "glm-5.3",
		"messages": []any{},
	}
	if applyDeepSeekReasoningInPlace(obj, "glm-5.3") {
		t.Fatal("non-deepseek models must be untouched")
	}
	if _, has := obj["thinking"]; has {
		t.Error("thinking must not be injected for non-deepseek")
	}
}

func TestApplyDeepSeekReasoning_TraceInHistoryTriggersBackfill(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	obj := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "assistant", "content": "earlier turn", "reasoning_content": "prior trace"},
			map[string]any{"role": "assistant", "content": "no trace"},
		},
	}
	if !applyDeepSeekReasoningInPlace(obj, "deepseek-v4.1-flash") {
		t.Fatal("trace in history should trigger backfill of the bare assistant")
	}
	second := obj["messages"].([]any)[1].(map[string]any)
	if _, ok := second["reasoning_content"].(string); !ok {
		t.Error("bare assistant must be backfilled once the history carries traces")
	}
}

// --- stream_options ---

func TestEnsureStreamOptionsInPlace(t *testing.T) {
	obj := map[string]any{}
	if !ensureStreamOptionsInPlace(obj) {
		t.Fatal("expected stream_options to be added")
	}
	so, ok := obj["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options = %v, want include_usage:true", obj["stream_options"])
	}
	obj["stream_options"] = map[string]any{"include_usage": false}
	if ensureStreamOptionsInPlace(obj) {
		t.Fatal("client-provided stream_options must never be replaced")
	}
}

// --- prepareUpstreamBody ordering ---

// An unusable value must not disable reasoning: the request resolves to the
// catalog default and keeps thinking on, and the model must already be the
// upstream ID by the time the deepseek pass runs.
func TestPrepareUpstreamBody_UnusableEffortResolvesToAutomaticDefault(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	sa := &storedAuth{}
	body := `{"model":"alias-ds","reasoning_effort":"none","messages":[{"role":"user","content":"hi"}]}`
	out := prepareUpstreamBody([]byte(body), nil, sa, "deepseek-v4.1-flash")
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	thinking, ok := obj["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Errorf("resolved automatic effort must keep thinking on, got %v", obj["thinking"])
	}
	if obj["reasoning_effort"] != "high" {
		t.Errorf("unusable effort must resolve to the catalog default, got %v", obj["reasoning_effort"])
	}
}

// Without a catalog default the unusable value is dropped (upstream applies its
// own automatic default); "none"/"off" are never forwarded.
func TestPrepareUpstreamBody_UnusableEffortDroppedWithoutCatalogDefault(t *testing.T) {
	sa := &storedAuth{}
	body := `{"model":"alias-ds","reasoning_effort":"off","messages":[{"role":"user","content":"hi"}]}`
	out := prepareUpstreamBody([]byte(body), nil, sa, "deepseek-v4-pro")
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if _, has := obj["reasoning_effort"]; has {
		t.Errorf("unusable effort must be dropped when no default is declared, got %v", obj["reasoning_effort"])
	}
}

func TestPrepareUpstreamBody_DeepSeekDefaultInjectsEffortAndStreamOptions(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	sa := &storedAuth{}
	body := `{"model":"whatever","messages":[{"role":"user","content":"hi"}]}`
	out := prepareUpstreamBody([]byte(body), nil, sa, "deepseek-v4.1-flash")
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	thinking, ok := obj["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Errorf("thinking = %v, want enabled", obj["thinking"])
	}
	if obj["reasoning_effort"] != "high" {
		t.Errorf("effort = %v, want high", obj["reasoning_effort"])
	}
	so, ok := obj["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage:true", obj["stream_options"])
	}
	if obj["stream"] != true {
		t.Errorf("stream must be forced, got %v", obj["stream"])
	}
}

func TestPrepareUpstreamBody_NormalizesReasoningEffortTransmission(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	cases := []struct {
		name          string
		body          string
		upstreamModel string
		wantEffort    string
		wantSummary   string
		wantThinking  bool
	}{
		{
			name:          "camel-case medium is canonicalized",
			body:          `{"model":"alias-ds","reasoningEffort":" MeDiUm ","messages":[]}`,
			upstreamModel: "deepseek-v4.1-flash",
			wantEffort:    "medium",
			wantSummary:   "auto",
			wantThinking:  true,
		},
		{
			name:          "camel-case low is canonicalized",
			body:          `{"model":"alias-ds","reasoningEffort":"LOW","messages":[]}`,
			upstreamModel: "deepseek-v4.1-flash",
			wantEffort:    "low",
			wantSummary:   "auto",
			wantThinking:  true,
		},
		{
			name:          "uppercase snake-case high is normalized",
			body:          `{"model":"alias-ds","reasoning_effort":" HIGH ","messages":[]}`,
			upstreamModel: "deepseek-v4.1-flash",
			wantEffort:    "high",
			wantSummary:   "auto",
			wantThinking:  true,
		},
		{
			name:          "non-empty canonical effort wins over alias",
			body:          `{"model":"alias-ds","reasoning_effort":"medium","reasoningEffort":"HIGH","messages":[]}`,
			upstreamModel: "deepseek-v4.1-flash",
			wantEffort:    "medium",
			wantSummary:   "auto",
			wantThinking:  true,
		},
		{
			name:          "unusable canonical value resolves to automatic default",
			body:          `{"model":"alias-ds","reasoning_effort":"none","reasoningEffort":"HIGH","messages":[]}`,
			upstreamModel: "deepseek-v4.1-flash",
			wantEffort:    "high",
			wantSummary:   "auto",
			wantThinking:  true,
		},
		{
			name:          "unknown effort resolves to automatic default",
			body:          `{"model":"alias-ds","reasoningEffort":"custom-tier","messages":[]}`,
			upstreamModel: "deepseek-v4.1-flash",
			wantEffort:    "high",
			wantSummary:   "auto",
			wantThinking:  true,
		},
		{
			name:          "explicit auto is forwarded",
			body:          `{"model":"alias-ds","reasoning_effort":"Auto","messages":[]}`,
			upstreamModel: "deepseek-v4.1-flash",
			wantEffort:    "auto",
			wantSummary:   "auto",
			wantThinking:  true,
		},
		{
			name:          "off alias resolves to automatic default",
			body:          `{"model":"alias-ds","reasoning_effort":"  ","reasoningEffort":"OFF","messages":[]}`,
			upstreamModel: "deepseek-v4.1-flash",
			wantEffort:    "high",
			wantSummary:   "auto",
			wantThinking:  true,
		},
		{
			name:          "unusable value without catalog default is dropped",
			body:          `{"model":"alias-ds","reasoning_effort":"none","messages":[]}`,
			upstreamModel: "deepseek-v4-pro",
			wantThinking:  true,
		},
		{
			name:          "Hy3 alias does not conflict with forced high",
			body:          `{"model":"hy3-alias","reasoningEffort":"low","messages":[]}`,
			upstreamModel: "hy3-std",
			wantEffort:    "high",
			wantSummary:   "auto",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := prepareUpstreamBody([]byte(tc.body), nil, &storedAuth{}, tc.upstreamModel)
			var obj map[string]any
			if err := json.Unmarshal(out, &obj); err != nil {
				t.Fatalf("unmarshal prepared body: %v", err)
			}
			if got := obj["reasoning_effort"]; got != tc.wantEffort && !(got == nil && tc.wantEffort == "") {
				t.Errorf("reasoning_effort = %v, want %q", got, tc.wantEffort)
			}
			if _, has := obj["reasoningEffort"]; has {
				t.Errorf("camel-case alias leaked to upstream: %v", obj["reasoningEffort"])
			}
			if got := obj["reasoning_summary"]; got != tc.wantSummary && !(got == nil && tc.wantSummary == "") {
				t.Errorf("reasoning_summary = %v, want %q", got, tc.wantSummary)
			}
			_, hasThinking := obj["thinking"]
			if hasThinking != tc.wantThinking {
				t.Errorf("thinking present = %v, want %v (%v)", hasThinking, tc.wantThinking, obj["thinking"])
			}
		})
	}
}

// --- default effort cache ---

func TestParseDefaultEffort(t *testing.T) {
	if got := parseDefaultEffort([]byte(`{"defaultEffort":"high","supportedEfforts":["low","high"]}`)); got != "high" {
		t.Errorf("defaultEffort = %q, want high", got)
	}
	if got := parseDefaultEffort([]byte(`{"effort":"medium","summary":"auto"}`)); got != "medium" {
		t.Errorf("legacy effort = %q, want medium", got)
	}
	if got := parseDefaultEffort([]byte(`null`)); got != "" {
		t.Errorf("null = %q, want empty", got)
	}
	if got := parseDefaultEffort([]byte(`"hy3"`)); got != "" {
		t.Errorf("string form = %q, want empty", got)
	}
}

func TestDefaultEffortForUpstreamModel(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "low"})
	if got := defaultEffortForUpstreamModel("DeepSeek-V4.1-Flash"); got != "low" {
		t.Errorf("catalog hit = %q, want low", got)
	}
	if got := defaultEffortForUpstreamModel("deepseek-v4-pro"); got != "" {
		t.Errorf("undeclared effort fallback = %q, want empty", got)
	}
	if got := defaultEffortForUpstreamModel("glm-5.3"); got != "" {
		t.Errorf("non-deepseek = %q, want empty", got)
	}
}

func TestRememberModelDefaultEffortsClearsOmittedDefault(t *testing.T) {
	setEffortCache(t, map[string]string{"deepseek-v4.1-flash": "high"})
	rememberModelDefaultEfforts([]discoveredModel{{ID: "DeepSeek-V4.1-Flash", Reasoning: json.RawMessage(`{"supportedEfforts":["low","high"]}`)}})
	if got := defaultEffortForUpstreamModel("deepseek-v4.1-flash"); got != "" {
		t.Fatalf("omitted catalog default retained stale effort %q", got)
	}
}
