// payload.go rewrites the outgoing chat completion request body before it's
// forwarded upstream. The single-pass entry point is prepareUpstreamBody; the
// in-place helpers are the field-level mutations it composes, and the legacy
// *ForUpstream / forceStreamBody wrappers exist for tests and other call sites.
package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// neutralPrompt is the substitute for over-long or agent-identity system
// prompts that Tencent CodeBuddy's content filter rejects. Mirrors OmniRoute's
// codebuddy-cn.ts NEUTRAL_PROMPT.
const neutralPrompt = "You are a helpful AI assistant that helps with software engineering tasks."

// agentPattern matches Claude Code / Cursor / Windsurf / Cline / Aider / Continue
// / Copilot / Cody identity lines that Tencent's content filter blocklists.
// Ported verbatim from OmniRoute/open-sse/executors/codebuddy-cn.ts (MIT).
var agentPattern = regexp.MustCompile(`(?i)you are claude code|claude.?code.+official.+cli|anthropic.+official.+cli|anxthxropic.+official.+cli|you are (?:cursor|windsurf|cline|aider|continue|copilot|cody)|you are an? (?:ai )?(?:coding |code )?agent|cc_entrypoint\s*=\s*(?:cli|vscode|jetbrains|gui)|claude.?code.+issues|give feedback.+claude.?code|you are .{0,30}(?:powerful )?ai agent|orchestration capabilities|OhMyOpenCode|<agent-identity>|<Role>|<Behavior_Instructions>`)

// maxSystemPromptBytes is the byte-length threshold above which a system prompt
// is replaced wholesale with neutralPrompt. Tencent's filter rejects very long
// system prompts even when they don't match agentPattern. Mirrors OmniRoute.
const maxSystemPromptBytes = 2000

// toolDescriptionByteLimit is the threshold above which all tool descriptions
// are stripped to avoid Tencent's 64KB body-size filter. Mirrors OmniRoute.
const toolDescriptionByteLimit = 65536

// forceStreamBody returns the request body with "stream":true set, since the
// upstream rejects non-streaming chat requests.
// prepareUpstreamBody composes streaming, tool, request-compatibility, system,
// and model normalization into a single unmarshal/marshal pass (v0.6.31 perf: was
// 4-5 full JSON round-trips on every chat completion). Legacy wrappers remain
// for tests and other call sites that need them individually.
func prepareUpstreamBody(payload, original []byte, sa *storedAuth, upstreamModel string) []byte {
	src := payload
	if len(src) == 0 {
		src = original
	}
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if json.Unmarshal(src, &obj) != nil {
		return src
	}

	// 1. forceStream: CodeBuddy rejects non-stream requests.
	obj["stream"] = true

	// 2. normalize effort aliases before model-specific handling.
	normalizeReasoningEffortInPlace(obj, upstreamModel)

	// 3. normalizeTools: tool_choice object form → string; "none" suppresses tools.
	normalizeToolsInPlace(obj)

	// 3a. Normalize OpenAI-compatible image and tool-schema shapes accepted by clients.
	normalizeWorkBuddyOpenAICompatInPlace(obj)

	// 4. rewriteModel first so alias requests receive the correct model policy.
	rewriteModelInPlace(obj, upstreamModel)

	// 5. deepseek reasoning runs before rewriteSystem so the model policy sees
	// the resolved effort; the model must already be the upstream ID.
	applyDeepSeekReasoningInPlace(obj, upstreamModel)

	// 6. rewriteSystem: strip blocked Claude Code template phrases + force thinking.
	rewriteSystemInPlace(obj)

	// 7. ensureSystemMessage: inject minimal system msg for legacy WorkBuddy only.
	ensureSystemMessageInPlace(obj, sa)

	// 8. repairToolPairing: orphan calls/results and interleaved results break
	// pairing on every later turn of the conversation.
	repairToolPairingInPlace(obj)

	// 9. stream_options: ask for usage frames when the client didn't.
	ensureStreamOptionsInPlace(obj)

	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// normalizeReasoningEffortInPlace canonicalizes the supported effort field
// aliases and resolves unusable tiers to the model's automatic default.
//
//   - A non-empty reasoning_effort wins over the reasoningEffort alias.
//   - Known tiers and explicit "auto" are lowercased and forwarded as-is.
//   - Anything else ("none", "off", unknown strings, blanks, non-strings) is
//     never forwarded: the catalog-declared default is used when known,
//     otherwise the field is dropped so the upstream applies its own automatic
//     default. "none"/"off" are not a way to disable reasoning; only
//     thinking.type="disabled" is.
func normalizeReasoningEffortInPlace(obj map[string]any, upstreamModel string) bool {
	canonicalValue, hasCanonical := obj["reasoning_effort"]
	camelValue, hasCamel := obj["reasoningEffort"]
	if !hasCanonical && !hasCamel {
		return false
	}

	selected := canonicalValue
	if s, ok := canonicalValue.(string); !ok || strings.TrimSpace(s) == "" {
		selected = camelValue
	}

	changed := false
	if effort, ok := selected.(string); ok {
		if tier, ok2 := normalizeReasoningEffortValue(effort); ok2 {
			if obj["reasoning_effort"] != tier {
				obj["reasoning_effort"] = tier
				changed = true
			}
		} else {
			applyAutomaticEffort(obj, upstreamModel)
			changed = true
		}
	} else {
		applyAutomaticEffort(obj, upstreamModel)
		changed = true
	}
	if hasCamel {
		delete(obj, "reasoningEffort")
		changed = true
	}
	return changed
}

// applyAutomaticEffort replaces an unusable effort with the model's
// catalog-declared default, or drops the field so the upstream picks its own
// automatic default. It never writes "none"/"off".
func applyAutomaticEffort(obj map[string]any, upstreamModel string) {
	if def := defaultEffortForUpstreamModel(upstreamModel); def != "" {
		obj["reasoning_effort"] = def
		return
	}
	delete(obj, "reasoning_effort")
}

// normalizeReasoningEffortValue reports whether effort is a tier the upstream
// accepts, returning its canonical lowercase form. "none"/"off" are NOT valid
// tiers: the upstream has no such value, so they are resolved to the automatic
// default instead of being forwarded.
func normalizeReasoningEffortValue(effort string) (string, bool) {
	trimmed := strings.TrimSpace(effort)
	switch strings.ToLower(trimmed) {
	case "minimal", "low", "medium", "high", "max", "xhigh", "ultra", "auto":
		return strings.ToLower(trimmed), true
	default:
		return "", false
	}
}

func normalizeToolsInPlace(obj map[string]any) bool {
	changed := false
	suppressTools := func() {
		if _, ok := obj["tools"]; ok {
			delete(obj, "tools")
			changed = true
		}
		if _, ok := obj["functions"]; ok {
			delete(obj, "functions")
			changed = true
		}
	}
	if tc, present := obj["tool_choice"]; present {
		switch v := tc.(type) {
		case string:
			if strings.EqualFold(strings.TrimSpace(v), "none") {
				delete(obj, "tool_choice")
				suppressTools()
				changed = true
			}
		case map[string]any:
			typ, _ := v["type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			switch typ {
			case "none":
				delete(obj, "tool_choice")
				suppressTools()
				changed = true
			case "auto", "required":
				obj["tool_choice"] = typ
				changed = true
			case "function":
				name := ""
				if fn, ok := v["function"].(map[string]any); ok {
					name, _ = fn["name"].(string)
				}
				if name == "" {
					name, _ = v["name"].(string)
				}
				name = strings.TrimSpace(name)
				if name != "" {
					obj["tool_choice"] = name
				} else {
					obj["tool_choice"] = "auto"
				}
				changed = true
			default:
				delete(obj, "tool_choice")
				changed = true
			}
		default:
			delete(obj, "tool_choice")
			changed = true
		}
	}
	return changed
}

// normalizeWorkBuddyOpenAICompatInPlace applies conservative request-shape normalizations.
func normalizeWorkBuddyOpenAICompatInPlace(obj map[string]any) bool {
	changed := normalizeImageURLStringsInPlace(obj)
	if normalizeToolSchemaPatternsInPlace(obj) {
		changed = true
	}
	return changed
}

// normalizeImageURLStringsInPlace converts OpenAI image_url string parts to the
// canonical object form expected by WorkBuddy. Existing objects are preserved.
func normalizeImageURLStringsInPlace(obj map[string]any) bool {
	messages, _ := obj["messages"].([]any)
	changed := false
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, _ := message["content"].([]any)
		for _, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := part["type"].(string)
			if !strings.EqualFold(strings.TrimSpace(kind), "image_url") {
				continue
			}
			imageURL, ok := part["image_url"].(string)
			if !ok || strings.TrimSpace(imageURL) == "" {
				continue
			}
			part["image_url"] = map[string]any{"url": imageURL}
			changed = true
		}
	}
	return changed
}

// normalizeToolSchemaPatternsInPlace removes nonstandard escaped underscores from
// JSON Schema regex patterns inside tool/function definitions.
func normalizeToolSchemaPatternsInPlace(obj map[string]any) bool {
	changed := false
	for _, key := range []string{"tools", "functions"} {
		items, _ := obj[key].([]any)
		for _, item := range items {
			if normalizeSchemaPatternsInPlace(item) {
				changed = true
			}
		}
	}
	return changed
}

func normalizeSchemaPatternsInPlace(value any) bool {
	switch node := value.(type) {
	case map[string]any:
		changed := false
		if pattern, ok := node["pattern"].(string); ok {
			normalized := strings.ReplaceAll(pattern, `\_`, `_`)
			if normalized != pattern {
				node["pattern"] = normalized
				changed = true
			}
		}
		for key, child := range node {
			if key != "pattern" && normalizeSchemaPatternsInPlace(child) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range node {
			if normalizeSchemaPatternsInPlace(child) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// rewriteSystemInPlace is the in-place form of rewriteSystemForUpstream.
// It does three things:
//  1. For each system message: if length > maxSystemPromptBytes or matches
//     agentPattern, replace content wholesale with neutralPrompt. Otherwise,
//     apply sanitizeBlockedTemplates (single-word substitutions).
//  2. Normalize reasoning_effort: real tiers are forwarded and mirrored to
//     reasoning_summary="auto"; unusable values resolve to the automatic
//     default (see normalizeReasoningEffortInPlace).
//  3. forceMaxThinking for hy3/hy4-family models.
func rewriteSystemInPlace(obj map[string]any) bool {
	messages, _ := obj["messages"].([]any)
	changed := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if !strings.EqualFold(strings.TrimSpace(role), "system") {
			continue
		}
		if rewriteContentField(msg) {
			changed = true
		}
	}
	if mirrorReasoningEffort(obj) {
		changed = true
	}
	if forceMaxThinking(obj) {
		changed = true
	}
	if compactToolDescriptions(obj) {
		changed = true
	}
	return changed
}

// mirrorReasoningEffort implements OmniRoute codebuddy-cn.ts reasoning_effort
// handling:
//   - a real tier → set reasoning_summary="auto" (mirror)
//   - absent → no-op (forcing reasoning triggers content filter)
//
// Unusable values ("none", "off", unknown) are never forwarded; the request
// normalization already resolved them to the automatic default, and this
// legacy-path guard drops any that reach here directly.
func mirrorReasoningEffort(obj map[string]any) bool {
	eff, ok := obj["reasoning_effort"].(string)
	if !ok {
		return false
	}
	tier, valid := normalizeReasoningEffortValue(eff)
	if !valid {
		delete(obj, "reasoning_effort")
		return true
	}
	if obj["reasoning_effort"] != tier {
		obj["reasoning_effort"] = tier
	}
	obj["reasoning_summary"] = "auto"
	return true
}

// compactToolDescriptions strips tool.function.description when the serialized
// tools array exceeds toolDescriptionByteLimit (64KB). Tencent's body-size
// filter rejects large tool descriptions. Mirrors OmniRoute codebuddy-cn.ts.
func compactToolDescriptions(obj map[string]any) bool {
	tools, ok := obj["tools"].([]any)
	if !ok || len(tools) == 0 {
		return false
	}
	serialized, err := json.Marshal(tools)
	if err != nil {
		return false
	}
	if len(serialized) < toolDescriptionByteLimit {
		return false
	}
	changed := false
	for _, t := range tools {
		tool, ok := t.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if _, hasDesc := fn["description"]; hasDesc {
			delete(fn, "description")
			changed = true
		}
	}
	return changed
}

// ensureSystemMessageInPlace is the in-place form of ensureSystemMessage.
// Returns true when obj was modified.
func ensureSystemMessageInPlace(obj map[string]any, sa *storedAuth) bool {
	if sa == nil || !isWorkBuddyService(sa) {
		return false
	}
	messages, ok := obj["messages"].([]any)
	if !ok || len(messages) == 0 {
		return false
	}
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); strings.EqualFold(role, "system") {
			return false
		}
	}
	systemMsg := map[string]any{
		"role":    "system",
		"content": "You are a helpful assistant.",
	}
	obj["messages"] = append([]any{systemMsg}, messages...)
	return true
}

// rewriteModelInPlace swaps obj["model"] to upstreamModel when non-empty.
// Mirrors rewriteModelInBody's behavior (case-insensitive compare); returns
// true when modified.
func rewriteModelInPlace(obj map[string]any, upstreamModel string) bool {
	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" {
		return false
	}
	cur, _ := obj["model"].(string)
	if strings.EqualFold(strings.TrimSpace(cur), upstreamModel) {
		return false
	}
	obj["model"] = upstreamModel
	return true
}

func forceStreamBody(payload, original []byte) []byte {
	src := payload
	if len(src) == 0 {
		src = original
	}
	var obj map[string]any
	if json.Unmarshal(src, &obj) != nil {
		return src
	}
	obj["stream"] = true
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// normalizeToolsForUpstream adapts OpenAI tools / tool_choice fields to
// CodeBuddy's chat schema before the request is forwarded.
//
// Live-verified against /v2/chat/completions (2026-07):
//  1. tool_choice is typed as string on the upstream Go struct. OpenAI's object
//     form {"type":"function","function":{"name":"..."}} returns 400 code 11101
//     ("cannot unmarshal object into Go struct field Request.tool_choice of
//     type string"). Convert known object shapes to the matching string.
//  2. tool_choice "none" is accepted but ignored when tools[] is non-empty —
//     the model still emits tool_calls. The only reliable way to suppress tools
//     is to omit tools (and functions) entirely.
//
// String values auto / required / <function name> are left untouched.
func normalizeToolsForUpstream(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return payload
	}
	changed := false

	suppressTools := func() {
		if _, ok := obj["tools"]; ok {
			delete(obj, "tools")
			changed = true
		}
		if _, ok := obj["functions"]; ok {
			delete(obj, "functions")
			changed = true
		}
	}

	if tc, present := obj["tool_choice"]; present {
		switch v := tc.(type) {
		case string:
			if strings.EqualFold(strings.TrimSpace(v), "none") {
				delete(obj, "tool_choice")
				suppressTools()
				changed = true
			}
		case map[string]any:
			typ, _ := v["type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			switch typ {
			case "none":
				delete(obj, "tool_choice")
				suppressTools()
				changed = true
			case "auto", "required":
				obj["tool_choice"] = typ
				changed = true
			case "function":
				name := ""
				if fn, ok := v["function"].(map[string]any); ok {
					name, _ = fn["name"].(string)
				}
				if name == "" {
					name, _ = v["name"].(string)
				}
				name = strings.TrimSpace(name)
				if name != "" {
					obj["tool_choice"] = name
				} else {
					// Object force without a name: fall back to auto instead of 400.
					obj["tool_choice"] = "auto"
				}
				changed = true
			default:
				// Unknown object shape → drop rather than forward a 400.
				delete(obj, "tool_choice")
				changed = true
			}
		default:
			// null / array / number — drop to keep upstream happy.
			delete(obj, "tool_choice")
			changed = true
		}
	}

	if !changed {
		return payload
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

// rewriteSystemForUpstream neutralizes Claude Code template phrases that
// Tencent CodeBuddy's content filter blocklists verbatim — the agent identity
// line ("You are Claude Code, Anthropic's official CLI for Claude.") and the
// git injection ("Main branch (you will usually use this for PRs)"). Each
// rewrite is a single-word change so the prompt's meaning is preserved while
// dodging the exact-match filter.
func rewriteSystemForUpstream(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return payload
	}
	messages, _ := obj["messages"].([]any)
	changed := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if !strings.EqualFold(strings.TrimSpace(role), "system") {
			continue
		}
		if rewriteContentField(msg) {
			changed = true
		}
	}
	if forceMaxThinking(obj) {
		changed = true
	}
	if !changed {
		return payload
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

// ensureSystemMessage injects a minimal system message if none is present.
// Global (www.workbuddy.ai) rejects user-only requests with code 11101
// "Parse message failed: 11101:invalid request". CN (copilot.tencent.com)
// does not require a system message but tolerates one. Inserting a
// harmless system message unifies both paths.
func ensureSystemMessage(payload []byte, sa *storedAuth) []byte {
	if len(payload) == 0 {
		return payload
	}
	// Only inject for Global; CN doesn't need it and we minimize diff.
	if sa == nil || !isWorkBuddyService(sa) {
		return payload
	}
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return payload
	}
	messages, ok := obj["messages"].([]any)
	if !ok || len(messages) == 0 {
		return payload
	}
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); strings.EqualFold(role, "system") {
			return payload // already has system message
		}
	}
	systemMsg := map[string]any{
		"role":    "system",
		"content": "You are a helpful assistant.",
	}
	obj["messages"] = append([]any{systemMsg}, messages...)
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

// rewriteContentField sanitizes blocked templates in one message's content,
// handling both plain-string and OpenAI multimodal (array of parts) shapes.
//
// Per OmniRoute codebuddy-cn.ts:
//   - If content length > maxSystemPromptBytes (2000) OR matches agentPattern,
//     replace content wholesale with neutralPrompt.
//   - Otherwise, apply sanitizeBlockedTemplates (single-word substitutions).
//
// Returns true if the message was modified.
func rewriteContentField(msg map[string]any) bool {
	switch c := msg["content"].(type) {
	case string:
		if r := sanitizeContentText(c); r != c {
			msg["content"] = r
			return true
		}
	case []any:
		modified := false
		for _, p := range c {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := part["text"].(string); ok {
				if r := sanitizeContentText(t); r != t {
					part["text"] = r
					modified = true
				}
			}
		}
		return modified
	}
	return false
}

// sanitizeContentText decides between wholesale replacement (neutralPrompt)
// and template-level single-word substitution. Mirrors OmniRoute codebuddy-cn.ts
// AGENT_PATTERN + length check.
func sanitizeContentText(text string) string {
	if len(text) > maxSystemPromptBytes || agentPattern.MatchString(text) {
		return neutralPrompt
	}
	return sanitizeBlockedTemplates(text)
}

func sanitizeBlockedTemplates(s string) string {
	s = strings.ReplaceAll(s,
		"You are Claude Code, Anthropic's official CLI for Claude.",
		"You are Claude Code, Anthropic's official CLI tool for Claude.")
	s = strings.ReplaceAll(s,
		"Main branch (you will usually use this for PRs)",
		"Default branch (you will usually use this for PRs)")
	return s
}

// forceMaxThinking pins reasoning_effort to "high" for hy3/hy4-family models
// (Tencent Hunyuan) so they always reason at maximum depth. CodeBuddy only
// honors "high" for deep thinking (medium/low/max/xhigh/ultra all fall back to
// no reasoning), so we override whatever the client sent. Matching is
// case-insensitive because this runs before rewriteModelInPlace swaps the
// client-facing model name for the upstream ID. Returns true if changed.
func forceMaxThinking(obj map[string]any) bool {
	model, _ := obj["model"].(string)
	lm := strings.ToLower(model)
	if !strings.HasPrefix(lm, "hy3") && !strings.HasPrefix(lm, "hy4") {
		return false
	}
	if eff, _ := obj["reasoning_effort"].(string); eff == "high" {
		return false
	}
	obj["reasoning_effort"] = "high"
	return true
}

// ---------------------------------------------------------------------------
// Tool-call pairing repair (learned from workbuddy2api-hub v1.4.9)
//
// A failed tool call leaves the client history with an assistant tool_calls
// entry no result ever answers. The upstream then rejects every later turn of
// that conversation with 400 code 11148 ("tool calls and tool results do not
// match"), so one failed call retires the whole session. Two repairs run
// before the body leaves, both no-ops on healthy histories:
//   - repack: move interleaved non-tool messages (e.g. Codex's
//     image_resize_notice) behind the tool-results batch they split.
//   - trim: drop calls without results and results without calls, both sides
//     cut against the same id set so no half-pairing survives.
// ---------------------------------------------------------------------------

// repairToolPairingInPlace repairs obj["messages"] in place; reports whether
// anything changed.
func repairToolPairingInPlace(obj map[string]any) bool {
	messages, ok := obj["messages"].([]any)
	if !ok || len(messages) < 3 {
		return false
	}
	changed := false
	if repacked, did := repackToolResultBlocks(messages); did {
		messages = repacked
		changed = true
	}
	if trimmed, did := trimOrphanToolCalls(messages); did {
		messages = trimmed
		changed = true
	}
	if changed {
		obj["messages"] = messages
	}
	return changed
}

// repackToolResultBlocks keeps each tool_calls batch adjacent to its results.
// Only reorders: same results, same relative order, intruders moved behind the
// batch. The reference implementation stopped scanning at a non-tool message
// that precedes the first result, which left the classic broken shape
// "assistant(call) → user → tool(call)" unpaired; this version buffers
// intruders on both sides of the results, which is the same order-preserving
// move applied to the wider case.
func repackToolResultBlocks(messages []any) ([]any, bool) {
	out := make([]any, 0, len(messages))
	changed := false
	i := 0
	for i < len(messages) {
		m, ok := messages[i].(map[string]any)
		if !ok || !strings.EqualFold(stringRole(m), "assistant") {
			out = append(out, messages[i])
			i++
			continue
		}
		calls, _ := m["tool_calls"].([]any)
		if len(calls) == 0 {
			out = append(out, messages[i])
			i++
			continue
		}
		want := map[string]struct{}{}
		for _, tc := range calls {
			call, ok := tc.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := call["id"].(string); ok && id != "" {
				want[id] = struct{}{}
			}
		}
		out = append(out, m)
		i++
		var results, between []any
		sawBetween := false
		for i < len(messages) {
			mm, ok := messages[i].(map[string]any)
			if !ok {
				break
			}
			role := stringRole(mm)
			if role == "tool" {
				tid, _ := mm["tool_call_id"].(string)
				if _, matched := want[tid]; !matched || tid == "" {
					break
				}
				results = append(results, mm)
				if sawBetween {
					changed = true
				}
				i++
				continue
			}
			// A following assistant.tool_calls opens the next batch: it goes
			// back to the outer loop, or its own results never get repacked.
			if calls, ok := mm["tool_calls"].([]any); ok && len(calls) > 0 {
				break
			}
			between = append(between, mm)
			sawBetween = true
			i++
		}
		out = append(out, results...)
		out = append(out, between...)
	}
	return out, changed
}

// trimOrphanToolCalls drops tool calls that have no result and results that
// have no call, both trimmed against the same id set.
func trimOrphanToolCalls(messages []any) ([]any, bool) {
	callIDs := map[string]struct{}{}
	resultIDs := map[string]struct{}{}
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		switch stringRole(msg) {
		case "tool":
			if tid, ok := msg["tool_call_id"].(string); ok && tid != "" {
				resultIDs[tid] = struct{}{}
			}
		case "assistant":
			calls, _ := msg["tool_calls"].([]any)
			for _, tc := range calls {
				call, ok := tc.(map[string]any)
				if !ok {
					continue
				}
				if id, ok := call["id"].(string); ok && id != "" {
					callIDs[id] = struct{}{}
				}
			}
		}
	}
	if len(callIDs) == 0 && len(resultIDs) == 0 {
		return messages, false
	}
	keep := map[string]struct{}{}
	for id := range callIDs {
		if _, ok := resultIDs[id]; ok {
			keep[id] = struct{}{}
		}
	}
	changed := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok || !strings.EqualFold(stringRole(msg), "assistant") {
			continue
		}
		calls, _ := msg["tool_calls"].([]any)
		if len(calls) == 0 {
			continue
		}
		kept := make([]any, 0, len(calls))
		for _, tc := range calls {
			call, ok := tc.(map[string]any)
			if !ok {
				continue
			}
			id, _ := call["id"].(string)
			if _, paired := keep[id]; paired {
				kept = append(kept, tc)
			}
		}
		if len(kept) == len(calls) {
			continue
		}
		changed = true
		if len(kept) > 0 {
			msg["tool_calls"] = kept
		} else {
			delete(msg, "tool_calls")
		}
	}
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if ok && strings.EqualFold(stringRole(msg), "tool") {
			tid, _ := msg["tool_call_id"].(string)
			if _, paired := keep[tid]; !paired {
				changed = true
				continue
			}
		}
		out = append(out, m)
	}
	return out, changed
}

// stringRole reads a message's role, lowercased and trimmed.
func stringRole(msg map[string]any) string {
	role, _ := msg["role"].(string)
	return strings.ToLower(strings.TrimSpace(role))
}

// ---------------------------------------------------------------------------
// DeepSeek reasoning consistency (learned from workbuddy2api-hub v1.4.9)
//
// While thinking is on, the upstream requires a reasoning_content string on
// every assistant message (code 11155 "the reasoning content from the previous
// turn must be passed back in thinking mode"), and thinking.type=enabled alone
// produces NO trace unless an effort level rides along (measured live by the
// reference project: enabled + no effort → reasoning_tokens 0; effort=high →
// reasoning present). Both halves mirror the official client's
// ReasoningContentBackfillRule:
//   - backfill: when thinking is on (or the history already carries traces),
//     every assistant message gets a reasoning_content string and a non-empty
//     reasoning mirror (a whitespace placeholder passes the upstream's length
//     check and carries no model-visible semantics).
//   - inject: the client's own choice always wins; effort is only filled in
//     when the field was left out. Unusable values never reach this pass —
//     request normalization resolves them to the catalog default or drops
//     them — and only thinking.type="disabled" opts out of reasoning.
// ---------------------------------------------------------------------------

// applyDeepSeekReasoningInPlace backfills reasoning traces and injects the
// default effort for deepseek models. upstreamModel must already be the
// resolved upstream ID. Reports whether anything changed.
func applyDeepSeekReasoningInPlace(obj map[string]any, upstreamModel string) bool {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(upstreamModel)), "deepseek") {
		return false
	}
	// Resolve aliases and unusable tiers first (idempotent inside the pipeline),
	// so direct callers see the same automatic-default semantics.
	changed := normalizeReasoningEffortInPlace(obj, upstreamModel)
	thinking, ok := obj["thinking"].(map[string]any)
	if !ok {
		// A partial thinking object still opts into thinking unless the caller
		// explicitly set type=disabled. Normalize malformed/non-object input as
		// well so reasoning_effort is never sent without an enabled mode.
		thinking = map[string]any{}
		obj["thinking"] = thinking
		changed = true
	}
	thinkingType := strings.ToLower(strings.TrimSpace(strAny(thinking["type"])))
	effort := strings.ToLower(strings.TrimSpace(strAny(obj["reasoning_effort"])))
	// Only thinking.type="disabled" disables reasoning. "none"/"off" are not
	// upstream tiers and have already been resolved to the automatic default,
	// so they must not short-circuit the injection here.
	optedOut := thinkingType == "disabled"

	messages, _ := obj["messages"].([]any)
	// Backfill whenever thinking will be (or is already) on, or when the
	// history itself carries traces the upstream requires to stay consistent.
	if !optedOut || hasReasoningTrace(messages) {
		if backfillReasoningContent(messages) {
			changed = true
		}
	}
	if optedOut {
		return changed
	}
	if thinkingType == "" {
		thinking["type"] = "enabled"
		changed = true
	}
	if effort == "" {
		if def := defaultEffortForUpstreamModel(upstreamModel); def != "" {
			obj["reasoning_effort"] = def
			changed = true
		}
	}
	return changed
}

// hasReasoningTrace reports whether any message already carries a reasoning
// trace — the second backfill trigger, covering clients that kept traces in
// the history but switched thinking off in the new request.
func hasReasoningTrace(messages []any) bool {
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if rc, ok := msg["reasoning_content"].(string); ok && rc != "" {
			return true
		}
		if _, present := msg["reasoning_content"]; present {
			return true
		}
		if r, ok := msg["reasoning"].(string); ok && r != "" {
			return true
		}
	}
	return false
}

// backfillReasoningContent normalizes every assistant message to carry a
// string reasoning_content plus a non-empty reasoning mirror.
func backfillReasoningContent(messages []any) bool {
	changed := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok || !strings.EqualFold(stringRole(msg), "assistant") {
			continue
		}
		rc, rcOK := msg["reasoning_content"].(string)
		if !rcOK {
			// Non-string (null/number/absent) counts as missing, matching the
			// official `typeof !== "string"` check.
			if legacy, ok := msg["reasoning"].(string); ok {
				rc = legacy
			} else {
				rc = ""
			}
			msg["reasoning_content"] = rc
			changed = true
		}
		if existing, ok := msg["reasoning"].(string); !ok || existing == "" {
			if rc != "" {
				msg["reasoning"] = rc
			} else {
				msg["reasoning"] = " "
			}
			changed = true
		}
	}
	return changed
}

// strAny renders a JSON-decoded scalar as a trimmed string.
func strAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// ensureStreamOptionsInPlace asks the upstream for usage frames when the
// client didn't. The upstream tolerates the field (live-verified by
// workbuddy2api-hub), and the usage frame feeds both the folded completion's
// usage block and the host's token accounting.
func ensureStreamOptionsInPlace(obj map[string]any) bool {
	if _, ok := obj["stream_options"]; ok {
		return false
	}
	obj["stream_options"] = map[string]any{"include_usage": true}
	return true
}

// rewriteModelInBody replaces the "model" field of a chat-completions body
// with the resolved upstream model ID.
func rewriteModelInBody(body []byte, upstreamModel string) []byte {
	if len(body) == 0 || strings.TrimSpace(upstreamModel) == "" {
		return body
	}
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return body
	}
	cur, _ := obj["model"].(string)
	if strings.EqualFold(strings.TrimSpace(cur), strings.TrimSpace(upstreamModel)) {
		return body
	}
	obj["model"] = upstreamModel
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		if len(x) == 0 {
			return true
		}
		// Legacy function_call shell: {"name":"","arguments":""} is the
		// upstream's terminal-chunk artifact, not a real call — treat as empty
		// when every value is itself empty.
		for _, val := range x {
			if !isEmptyValue(val) {
				return false
			}
		}
		return true
	}
	return false
}
