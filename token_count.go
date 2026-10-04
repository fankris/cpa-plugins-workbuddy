// token_count.go implements Executor.CountTokens for the WorkBuddy executor.
//
// Upstream CodeBuddy exposes no count_tokens endpoint, so this is a LOCAL
// estimate that never touches the network and never consumes credits. It exists
// because context-budgeting clients (Claude Code calls
// POST /v1/messages/count_tokens before every turn) use the number to decide
// when to compact history. Returning a hardcoded 0 — the pre-0.9.32 behaviour —
// told those clients the prompt was free, so they never compacted and the real
// request eventually exceeded the upstream limit and failed.
//
// The estimate is deliberately biased HIGH: over-estimating costs a little
// premature compaction, under-estimating costs a hard request failure. The
// reference implementation (workbuddy2api, MIT) uses the same
// "characters / 3" heuristic for the same reason.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// charsPerTokenEstimate is the divisor for the character→token estimate.
// Real tokenizers average ~3.5-4 characters per token for English prose and
// fewer for CJK (a Chinese character is often 1-2 tokens). Dividing by 3
// therefore over-estimates both, which is the intended direction.
const charsPerTokenEstimate = 3

// minEstimatedInputTokens keeps the answer non-zero for tiny prompts: a 0 would
// be read as "nothing to count" by clients that treat 0 as unknown.
const minEstimatedInputTokens = 1

// countTokensWireShape is the ONLY payload shape that survives the host's
// translation, and getting it wrong is silent — the client just sees 0.
//
// The plugin declares ExecutorOutputFormats=["chat-completions"], so when a
// Claude client calls POST /v1/messages/count_tokens the host adapter
// (internal/pluginhost/adapters_executors.go translateExecutorResponse) runs:
//
//	TranslateNonStream(outputFormat=openai, requestedFormat=claude, …)
//
// That dispatches to the openai→claude *response* translator
// (internal/translator/openai/claude/openai_claude_response.go), whose
// ConvertOpenAIResponseToClaudeNonStream reads `usage.prompt_tokens` via
// extractOpenAIUsage. It does NOT read a top-level `input_tokens`, and it never
// touches the `TokenCount` hook — that hook is only reachable by native
// executors (e.g. ClaudeExecutor), which call TranslateTokenCount directly with
// the count as an argument.
//
// So returning the Claude-native `{"input_tokens":N}` (the pre-0.9.33
// behaviour, and what the host's own ClaudeExecutor returns) is silently
// rewritten to `usage.input_tokens: 0` before it reaches the client. This
// mirrors helps.BuildOpenAIUsageJSON exactly, which is the shape every
// openai-compatible native executor returns and therefore the best-tested one.
func countTokensWireShape(count int64) []byte {
	return []byte(fmt.Sprintf(`{"usage":{"prompt_tokens":%d,"completion_tokens":0,"total_tokens":%d}}`, count, count))
}

// estimateInputTokensPayload extracts the request body from the executor RPC
// envelope and returns the token-count response payload. The SDK type is used
// directly (rather than a local mirror) so a field rename cannot silently make
// this read nothing and fall back to a constant.
func estimateInputTokensPayload(raw []byte) []byte {
	var req pluginapi.ExecutorRequest
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &req)
	}
	body := req.Payload
	if len(body) == 0 {
		body = req.OriginalRequest
	}
	return countTokensWireShape(estimateInputTokens(body))
}

// estimateInputTokens counts the text-bearing parts of a request body. Only
// content that actually reaches the model is measured: message text, system
// prompts, tool names/descriptions/schemas, and reasoning hints. Structural
// fields (ids, roles, types, flags) are skipped because counting them would
// inflate the estimate with tokens the upstream never sees.
func estimateInputTokens(body []byte) int64 {
	if len(body) == 0 {
		return minEstimatedInputTokens
	}
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		// Not JSON (or not an object): fall back to measuring the raw bytes so
		// the answer still scales with the request size.
		return estimateFromChars(len(body))
	}
	var chars int
	// Entry-protocol fields that carry prompt text.
	chars += measureValue(obj["system"])      // Anthropic: string or block array
	chars += measureValue(obj["messages"])    // both protocols
	chars += measureValue(obj["input"])       // OpenAI Responses
	chars += measureValue(obj["tools"])       // both protocols
	chars += measureValue(obj["functions"])   // legacy OpenAI
	chars += measureValue(obj["tool_choice"]) // may carry a schema
	chars += measureValue(obj["prompt"])      // legacy completions
	chars += measureValue(obj["instructions"])
	if count := estimateFromChars(chars); count > 0 {
		return count
	}
	return minEstimatedInputTokens
}

// measureValue walks a decoded JSON value and returns the number of characters
// in its string leaves. Numbers, booleans, nulls and keys contribute nothing:
// keys are field names (role/type/id), and scalars are control values, not
// prompt content.
func measureValue(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case string:
		return len(t)
	case []any:
		total := 0
		for _, item := range t {
			total += measureValue(item)
		}
		return total
	case map[string]any:
		total := 0
		for key, item := range t {
			// "role"/"type"/"id"/"index"/"name" are structural. Tool and
			// function NAMES are model-visible, so they are counted.
			if isStructuralKey(key) {
				continue
			}
			total += measureValue(item)
		}
		return total
	default:
		return 0
	}
}

// isStructuralKey reports JSON keys whose values are protocol plumbing rather
// than model-visible content.
func isStructuralKey(key string) bool {
	switch strings.ToLower(key) {
	case "role", "type", "id", "index", "tool_call_id", "tool_use_id",
		"finish_reason", "object", "created", "model", "stream",
		"stop", "stop_sequences", "temperature", "top_p", "top_k",
		"max_tokens", "max_completion_tokens", "n", "seed",
		"presence_penalty", "frequency_penalty", "user", "metadata",
		"logprobs", "top_logprobs", "response_format", "stream_options",
		"parallel_tool_calls", "service_tier", "store", "previous_response_id",
		"truncation", "include", "reasoning_effort", "reasoning_summary",
		"cache_control", "beta", "safety_settings", "generation_config",
		"anthropic_version", "anthropic_beta":
		return true
	default:
		return false
	}
}

// estimateFromChars converts a character count to a token estimate, biased high
// and never below the floor.
func estimateFromChars(chars int) int64 {
	if chars <= 0 {
		return 0
	}
	count := int64(chars / charsPerTokenEstimate)
	if count < minEstimatedInputTokens {
		count = minEstimatedInputTokens
	}
	return count
}
