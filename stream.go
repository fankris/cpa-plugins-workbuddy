// stream.go owns the upstream SSE data plane: emitting cleaned chunks back to
// the host stream (streamEmit/close), pumping the upstream SSE in a goroutine
// (pumpUpstreamStream), collecting it synchronously (collectUpstreamStream),
// and the SSE-frame helpers that re-frame, filter, and aggregate chunks.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// streamEmit pushes one chunk payload to the host stream. Returns an error if
// the host rejected it (e.g. the client already disconnected and the stream
// was closed), which the pump uses to stop reading a dead upstream.
func streamEmit(streamID string, payload []byte) error {
	if streamID == "" {
		return fmt.Errorf("no stream id")
	}
	body, _ := json.Marshal(map[string]any{"stream_id": streamID, "payload": payload})
	_, err := hostCall(pluginabi.MethodHostStreamEmit, body)
	return err
}

// streamEmitError reports a stream failure to the host.
//
// CRITICAL: the failure must ride the chunk's `error` field, NOT the payload.
//
// The host decodes rpcStreamEmitRequest and does:
//
//	chunk := pluginapi.ExecutorStreamChunk{Payload: req.Payload}
//	if req.Error != "" { chunk.Err = fmt.Errorf("%s", req.Error) }
//
// Only `chunk.Err` marks the request as failed
// (adapters_executors.go: `if chunk.Err != nil { streamErr = chunk.Err }` →
// reporter.PublishFailure). A JSON error body sent as Payload is
// indistinguishable from ordinary model output, so the host recorded the
// request as SUCCEEDED — which is exactly the reported symptom: a throttled
// stream (200 + framing + no content) surfacing as a successful request, with
// the credential never cooled.
//
// The message is still redacted: the host copies this string into its error
// path, and upstream bodies can carry Bearer/JWT.
func streamEmitError(streamID, message string) {
	if streamID == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"stream_id": streamID,
		"error":     redactSecrets(message),
	})
	_, _ = hostCall(pluginabi.MethodHostStreamEmit, body)
}

func streamClose(streamID string) {
	if streamID == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"stream_id": streamID})
	_, _ = hostCall(pluginabi.MethodHostStreamClose, body)
}

func streamHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	return h
}

// scanSSEDataFrames reads standard SSE events and joins repeated data: lines
// with the protocol-mandated newline. The callback returns false to stop after
// [DONE] without reading or forwarding trailing garbage.
func scanSSEDataFrames(r io.Reader, callback func(string) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var dataLines []string
	flush := func() bool {
		if len(dataLines) == 0 {
			return true
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		return callback(data)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if !flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(dataLines) > 0 {
		_ = flush()
	}
	return nil
}

// pumpUpstreamStream reads the upstream SSE response in the background and
// emits each cleaned chunk to the host stream. It closes the stream when done.
// An emit failure (client disconnected → host closed the stream) aborts the
// pump so we stop reading a dead upstream. cancel is invoked on every exit so
// the underlying http request context is released promptly.
//
// v0.7.0: requests now route via host.http.do_stream so request-log captures
// the outbound call and host transport policy applies. The host bridge emits
// arbitrary 32KB chunks, so we adapt to io.Reader and keep the bufio.Scanner
// SSE line framing unchanged.
func pumpUpstreamStream(httpReq *http.Request, lifecycleStream *pluginAsyncStream, streamID string, sseFramed bool, authUID, authID string, sa *storedAuth, requestBody ...[]byte) {
	if lifecycleStream == nil {
		return
	}
	defer lifecycleStream.finish()
	defer lifecycleStream.closeHostStream()
	defer func() {
		if lifecycleStream.cancel != nil {
			lifecycleStream.cancel()
		}
	}()

	var body []byte
	if len(requestBody) > 0 {
		body = requestBody[0]
	}
	stream, statusCode, _, err := openChatStreamWithReasoningRetry(httpReq, body)
	if stream != nil {
		lifecycleStream.setCloser(stream.Close)
	}
	if err != nil {
		streamEmitError(streamID, fmt.Sprintf("http_error: %v", err))
		return
	}
	defer stream.Close()
	if statusCode >= 400 {
		// Drain the error body via the same bridge so the message is complete.
		errPayload, _ := io.ReadAll(newHostStreamReader(stream))
		if authUID != "" {
			startPluginWorker(func() { reconcileByUID(authUID, statusCode, string(errPayload)) })
		}
		_, errMsg := translateChatUpstreamError(statusCode, string(errPayload), sa)
		streamEmitError(streamID, errMsg.Error())
		return
	}
	seenData := false
	validChunks := 0
	contentChunks := 0
	var emitErr error
	scanErr := scanSSEDataFrames(newHostStreamReader(stream), func(content string) bool {
		if content == "[DONE]" {
			return false
		}
		if content == "" {
			return true
		}
		seenData = true
		cleaned := cleanChunkJSON(content)
		if cleaned == "" {
			return true
		}
		// Classify BEFORE adding the SSE frame: chunkHasModelOutput understands
		// both shapes, but keeping the check on the raw JSON makes the intent
		// obvious and independent of the framing decision.
		if chunkHasModelOutput(cleaned) {
			contentChunks++
		}
		if sseFramed {
			cleaned = "data: " + cleaned
		}
		validChunks++
		if err := streamEmit(streamID, []byte(cleaned)); err != nil {
			emitErr = err
			return false
		}
		return true
	})
	// A mid-stream read failure means the client received a truncated stream:
	// surface it as an error frame.
	if emitErr != nil {
		return
	}
	if scanErr != nil {
		streamEmitError(streamID, fmt.Sprintf("upstream stream read error: %v", scanErr))
		return
	}

	if !seenData || validChunks == 0 {
		msg := "upstream stream was empty"
		if seenData {
			msg = "upstream stream contained no valid data"
		}
		streamEmitError(streamID, msg)
		return
	}
	// Framing-only stream (role / finish_reason / usage, no content): the
	// throttled-account shape. The chunks were already forwarded, so the client
	// needs an explicit terminal error frame — otherwise it renders an empty
	// reply as a successful completion and the account is never cooled.
	if contentChunks == 0 {
		streamEmitError(streamID, (&emptyAnswerError{Stage: "pump"}).Error())
		return
	}
	invalidateAccountCredits(authID, authUID)
}

// collectUpstreamStream is the synchronous fallback (no async stream id): drain
// the upstream, clean each chunk, return them as a slice. statusCode is the
// upstream HTTP status (0 for transport-level failures).
func collectUpstreamStream(body []byte, sa *storedAuth, sseFramed bool) ([]pluginapi.ExecutorStreamChunk, int, error) {
	httpReq, err := http.NewRequest(http.MethodPost, endpointChatFor(sa), bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	backendHeaders(httpReq, sa)
	// Compliance: route via host.http.do_stream so request-log captures the call.
	stream, statusCode, _, err := openChatStreamWithReasoningRetry(httpReq, body)
	if err != nil {
		return nil, 0, fmt.Errorf("http_error: %w", err)
	}
	defer stream.Close()
	reader := newHostStreamReader(stream)
	if statusCode >= 400 {
		errPayload, _ := io.ReadAll(reader)
		if sa != nil && sa.Account.UID != "" {
			startPluginWorker(func() { reconcileByUID(sa.Account.UID, statusCode, string(errPayload)) })
		}
		errStatus, errChat := translateChatUpstreamError(statusCode, string(errPayload), sa)
		return nil, errStatus, errChat
	}
	chunks, errAgg := aggregateSSEWithCollector(reader, sseFramed)
	if errAgg != nil {
		return chunks, statusCode, errAgg
	}
	return chunks, statusCode, nil
}

// clientNeedsSSEFrame reports whether chunk payloads must carry their own
// "data: " SSE framing. CPA's chat-completions passthrough adds the prefix
// itself, but every cross-format response translator (claude/gemini/codex/...)
// only consumes payloads already framed as "data: " lines. The host hands the
// plugin the inbound request path in Metadata, so we frame chunks ourselves for
// any entry path other than the native OpenAI chat-completions one.
func clientNeedsSSEFrame(metadata map[string]any) bool {
	path, _ := metadata["request_path"].(string)
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "/v1/chat/completions", "/v1/completions":
		return false
	default:
		return true
	}
}

// aggregateSSEWithCollector reads an upstream SSE stream and emits one chunk
// per data event. Empty tool-call shells are stripped and the trailing [DONE]
// is dropped (the host appends its own stream terminator). When sseFramed is
// true each payload is emitted as a "data: " line for cross-format
// translators; otherwise the payload is the raw JSON object and the host
// chat-completions writer adds the framing itself. A mid-stream read error
// aborts collection and is returned so the caller records the attempt as
// failed.
func aggregateSSEWithCollector(r io.Reader, sseFramed bool) ([]pluginapi.ExecutorStreamChunk, error) {
	var chunks []pluginapi.ExecutorStreamChunk
	seenData := false
	validChunks := 0
	scanErr := scanSSEDataFrames(r, func(content string) bool {
		if content == "[DONE]" {
			return false
		}
		if content == "" {
			return true
		}
		seenData = true
		cleaned := cleanChunkJSON(content)
		if cleaned == "" {
			return true
		}
		// Classify on the raw JSON (see the pump above for the rationale).
		if chunkHasModelOutput(cleaned) {
			validChunks++
		}
		if sseFramed {
			cleaned = "data: " + cleaned
		}
		chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: []byte(cleaned)})
		return true
	})
	if scanErr != nil {
		return chunks, fmt.Errorf("upstream stream read error: %w", scanErr)
	}
	if !seenData {
		return chunks, fmt.Errorf("upstream stream was empty")
	}
	if len(chunks) == 0 {
		return chunks, fmt.Errorf("upstream stream contained no valid data")
	}
	// Framing-only stream (role / finish_reason / usage, no content): the
	// throttled-account shape. Fail as a rate limit instead of handing the
	// client an empty-but-successful completion.
	if validChunks == 0 {
		return chunks, &emptyAnswerError{Stage: "collect"}
	}
	return chunks, nil
}

// cleanChunkJSON strips only the known-problematic empty tool-call shells
// from choice deltas: a null/empty function_call and an empty tool_calls array
// (CodeBuddy emits these on the terminal chunk, and strict clients interpret
// them as a truncated tool call). Other empty-but-legal values are preserved:
// content:"" is a valid delta (pure tool-call chunk) and the role-only first
// chunk must survive so clients can establish the message role.
func cleanChunkJSON(s string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(s), &obj) != nil {
		return ""
	}
	choices, ok := obj["choices"].([]any)
	if !ok {
		return s
	}
	changed := false
	kept := make([]any, 0, len(choices))
	for _, c := range choices {
		choice, ok := c.(map[string]any)
		if !ok {
			kept = append(kept, c)
			continue
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			kept = append(kept, choice)
			continue
		}
		if v, present := delta["function_call"]; present && isEmptyValue(v) {
			delete(delta, "function_call")
			changed = true
		}
		if v, present := delta["tool_calls"]; present {
			if arr, isArr := v.([]any); isArr && len(arr) == 0 {
				delete(delta, "tool_calls")
				changed = true
			}
		}
		for _, noise := range []string{"extra_fields", "refusal", "reasoning_content"} {
			if v, present := delta[noise]; present && isEmptyValue(v) {
				delete(delta, noise)
				changed = true
			}
		}
		if len(delta) == 0 {
			if fr, _ := choice["finish_reason"].(string); fr == "" {
				changed = true
				continue
			}
		}
		kept = append(kept, choice)
	}
	if len(kept) != len(choices) {
		obj["choices"] = kept
		changed = true
	}
	if len(kept) == 0 {
		return ""
	}
	if !changed {
		return s
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return string(out)
}

// emptyAnswerError reports an upstream 200 whose stream carried no model output
// at all — only framing (role / finish_reason / usage).
//
// A throttled WorkBuddy account answers exactly this way: the HTTP status is
// 200 and the SSE framing is well-formed, but no content ever arrives. The
// previous code counted "at least one valid JSON chunk" as success, so the
// folded completion went out as a normal reply with content:"" — the user saw
// an empty answer reported as a healthy request, and the credential was never
// cooled (so every following request hit the same throttled account).
//
// It is classified as a rate limit (429) on purpose: that is the semantic CPA
// acts on — cool this credential and route the retry to another account —
// rather than a gateway fault (5xx), which clients back off from instead.
type emptyAnswerError struct {
	// Stage names where it was detected, for logs: "execute" (folded
	// non-stream), "collect" (sync stream), "pump" (async stream).
	Stage string
}

func (e *emptyAnswerError) Error() string {
	return "上游返回空响应：本次请求未产生任何内容（无正文/无工具调用），已判定为上游限流或降级，请稍后重试或改用其他账号" +
		" // upstream returned an empty answer (no content and no tool calls); classified as upstream rate limiting/degradation — retry later or use another account"
}

// chatErrorStatus maps an upstream chat failure to the client-facing HTTP
// status. Empty answers become 429 so CPA cools the credential and retries the
// next account; everything else keeps the caller's fallback (502 for a
// malformed stream, the upstream's own status when it was an HTTP rejection).
func chatErrorStatus(err error, fallback int) int {
	var empty *emptyAnswerError
	if errors.As(err, &empty) {
		return http.StatusTooManyRequests
	}
	return fallback
}

// completionHasOutput reports whether a folded completion carries anything the
// client can consume: text, reasoning, or at least one tool call. Tool calls
// count even when content is empty (that is a normal tool-only turn), which is
// why this checks all three rather than content alone.
func completionHasOutput(content, reasoning string, toolCalls int) bool {
	if strings.TrimSpace(content) != "" {
		return true
	}
	if strings.TrimSpace(reasoning) != "" {
		return true
	}
	return toolCalls > 0
}

// chunkHasModelOutput reports whether one SSE chunk carries actual model output
// rather than only framing metadata. Used by the streaming paths, which must not
// treat a role-only or usage-only chunk as a completed answer.
//
// Callers pass payloads in both wire shapes: raw JSON (native chat-completions
// clients) and already "data: "-framed lines (every cross-format translator).
// The prefix is stripped first — treating a framed chunk as opaque JSON would
// classify every Claude/Gemini/Codex stream as an empty answer.
func chunkHasModelOutput(cleaned string) bool {
	var obj map[string]any
	if json.Unmarshal([]byte(stripDataPrefix(cleaned)), &obj) != nil {
		return false
	}
	choices, _ := obj["choices"].([]any)
	for _, c := range choices {
		choice, ok := c.(map[string]any)
		if !ok {
			continue
		}
		// Deltas are the streaming shape; "message" is the aggregated shape some
		// gateways emit on the terminal chunk.
		if delta, ok := choice["delta"].(map[string]any); ok {
			if messageHasModelOutput(delta) {
				return true
			}
		}
		if msg, ok := choice["message"].(map[string]any); ok {
			if messageHasModelOutput(msg) {
				return true
			}
		}
	}
	return false
}

// messageHasModelOutput is the shared field check behind chunkHasModelOutput.
// Empty-but-legal values are ignored: content:"" is a valid delta and must not
// be mistaken for real output.
func messageHasModelOutput(m map[string]any) bool {
	for _, key := range []string{"content", "reasoning_content", "reasoning", "function_call"} {
		if v, ok := m[key]; ok && !isEmptyValue(v) {
			return true
		}
	}
	if tcs, ok := m["tool_calls"].([]any); ok {
		for _, tc := range tcs {
			if !isEmptyValue(tc) {
				return true
			}
		}
	}
	return false
}

func aggregateCompletion(r io.Reader, model string) ([]byte, error) {
	var content, reasoning, role, respModel, respID, finish string
	var created int64
	var usage map[string]any
	seenData := false
	validJSON := 0
	// tool_calls arrive as streaming deltas: each chunk carries an index plus a
	// partial call (id/type/function.name on the first delta, argument text
	// fragments afterwards). Merge by index instead of appending raw fragments
	// so the folded completion holds whole calls.
	toolCalls := map[int]map[string]any{}
	var toolOrder []int
	var scanErr error

	scanErr = scanSSEDataFrames(r, func(data string) bool {
		if data == "[DONE]" {
			return false
		}
		if data == "" {
			return true
		}
		seenData = true
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return true
		}
		validJSON++
		if v, ok := chunk["id"].(string); ok && v != "" {
			respID = v
		}
		if v, ok := chunk["model"].(string); ok && v != "" {
			respModel = v
		}
		if v, ok := chunk["created"].(float64); ok {
			created = int64(v)
		}
		if v, ok := chunk["usage"].(map[string]any); ok && usageFrameWins(v, usage) {
			usage = v
		}
		choices, _ := chunk["choices"].([]any)
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			if delta, ok := choice["delta"].(map[string]any); ok {
				if v, ok := delta["role"].(string); ok && v != "" {
					role = v
				}
				if v, ok := delta["content"].(string); ok {
					content += v
				}
				if v, ok := delta["reasoning_content"].(string); ok {
					reasoning += v
				}
				if tcs, ok := delta["tool_calls"].([]any); ok {
					for _, tc := range tcs {
						call, ok := tc.(map[string]any)
						if !ok {
							continue
						}
						idx := 0
						if v, ok := call["index"].(float64); ok {
							idx = int(v)
						}
						merged, seen := toolCalls[idx]
						if !seen {
							merged = map[string]any{"index": idx}
							toolCalls[idx] = merged
							toolOrder = append(toolOrder, idx)
						}
						mergeToolCallDelta(merged, call)
					}
				}
			}
			if v, ok := choice["finish_reason"].(string); ok && v != "" {
				finish = v
			}
		}
		return true
	})
	// A mid-stream read failure means the folded completion is truncated. The
	// host discards the payload entirely when the plugin returns an error
	// (sdk/api/handlers executeWithPluginExecutor), so fail fast here instead
	// of assembling a partial completion nobody can safely consume.
	if scanErr != nil {
		return nil, fmt.Errorf("upstream stream read error: %w", scanErr)
	}
	if !seenData {
		return nil, fmt.Errorf("upstream stream was empty")
	}
	if validJSON == 0 {
		return nil, fmt.Errorf("upstream stream contained no valid data")
	}
	// A well-formed stream with no actual output is not a successful answer:
	// throttled accounts return exactly this shape (200 + framing + no content).
	// Reporting it as success is what let an empty reply reach the user as a
	// normal completion while the credential stayed in rotation.
	if !completionHasOutput(content, reasoning, len(toolOrder)) {
		return nil, &emptyAnswerError{Stage: "execute"}
	}

	message := map[string]any{"role": firstNonEmpty(role, "assistant"), "content": content}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	if len(toolOrder) > 0 {
		sort.Ints(toolOrder)
		calls := make([]map[string]any, 0, len(toolOrder))
		for _, idx := range toolOrder {
			calls = append(calls, toolCalls[idx])
		}
		message["tool_calls"] = calls
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	result := map[string]any{
		"id":      firstNonEmpty(respID, "chatcmpl-workbuddy"),
		"object":  "chat.completion",
		"created": created,
		"model":   firstNonEmpty(respModel, model),
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": firstNonEmpty(finish, "stop"),
		}},
	}
	if usage != nil {
		result["usage"] = usage
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// usageFrameWins decides whether a newly seen stream usage frame may replace
// the stored one. GPT-series upstreams emit mid-stream usage PLACEHOLDER
// frames whose totals are all zero; taking the last frame blindly let a
// trailing placeholder overwrite the real accounting. Usage within one request
// is monotonic, so a frame only wins when it carries at least the stored
// total. Learned from workbuddy2api-hub (v1.4.5).
func usageFrameWins(next, prev map[string]any) bool {
	if prev == nil {
		return true
	}
	return usageTotalTokens(next) >= usageTotalTokens(prev)
}

// usageTotalTokens reads a frame's total token count, falling back to
// prompt+completion when total_tokens itself is absent.
func usageTotalTokens(m map[string]any) float64 {
	if v, ok := m["total_tokens"].(float64); ok {
		return v
	}
	var prompt, completion float64
	if v, ok := m["prompt_tokens"].(float64); ok {
		prompt = v
	}
	if v, ok := m["completion_tokens"].(float64); ok {
		completion = v
	}
	return prompt + completion
}

// mergeToolCallDelta folds one streaming tool_call fragment into the merged
// call: scalar fields (id/type) are taken when first seen, function.name is
// concatenated (upstream may split it), and function.arguments text fragments
// are appended in arrival order.
func mergeToolCallDelta(merged, delta map[string]any) {
	for _, k := range []string{"id", "type"} {
		if _, present := merged[k]; !present {
			if v, ok := delta[k].(string); ok && v != "" {
				merged[k] = v
			}
		}
	}
	dfn, _ := delta["function"].(map[string]any)
	if dfn == nil {
		return
	}
	mfn, _ := merged["function"].(map[string]any)
	if mfn == nil {
		mfn = map[string]any{}
		merged["function"] = mfn
	}
	if v, ok := dfn["name"].(string); ok && v != "" {
		cur, _ := mfn["name"].(string)
		mfn["name"] = cur + v
	}
	if v, ok := dfn["arguments"].(string); ok && v != "" {
		cur, _ := mfn["arguments"].(string)
		mfn["arguments"] = cur + v
	}
}

func stripDataPrefix(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "data:") {
		return s
	}
	for strings.HasPrefix(s, "data:") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "data:"))
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
