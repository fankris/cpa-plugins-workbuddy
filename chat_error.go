// chat_error.go translates upstream chat rejections into actionable errors.
//
// v0.12.18: the CodeBuddy Intl gateway (codebuddy.ai) rejects models that are
// not registered on ITS catalog with HTTP 400
// {"code":11102,"msg":"model [X] service info not found"}. The executor used
// to pass the raw payload through ("upstream 400: {...}"), which told the
// user nothing about which models the account's realm actually serves — and
// the model list itself was unreliable for Intl accounts because discovery
// queried the CN endpoint (fixed in models.go the same release). The
// translator detects 11102 and rewrites the error into a bilingual, actionable
// message that names the realm and its best-known catalog.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// upstreamErrorShape mirrors the APISIX/business-JSON error envelope the
// WorkBuddy gateways return on chat rejections.
type upstreamErrorShape struct {
	Code     int    `json:"code"`
	Msg      string `json:"msg"`
	ExtError struct {
		Code string `json:"code"`
	} `json:"extError"`
}

// isInvalidReasoningEffort reports the narrowly scoped upstream rejection for
// a reasoning tier unsupported by the current model.
func isInvalidReasoningEffort(statusCode int, payload string) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	var shape upstreamErrorShape
	if json.Unmarshal([]byte(payload), &shape) != nil {
		return false
	}
	return shape.Code == 11150 || strings.EqualFold(strings.TrimSpace(shape.ExtError.Code), "invalid_reasoning_effort")
}

// removeUnsupportedReasoningEffort removes only effort metadata and its derived
// summary hint. Message-level reasoning traces stay paired with prior assistant
// turns, as required by the upstream conversation protocol.
func removeUnsupportedReasoningEffort(body []byte) ([]byte, bool) {
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil || obj == nil {
		return nil, false
	}
	changed := false
	for _, key := range []string{"reasoning_effort", "reasoningEffort", "reasoning_summary"} {
		if _, ok := obj[key]; ok {
			delete(obj, key)
			changed = true
		}
	}
	if !changed {
		return nil, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
}

// openChatStreamWithReasoningRetry routes every chat attempt through the CPA
// host bridge and retries exactly once without an unsupported effort value.
func openChatStreamWithReasoningRetry(req *http.Request, body []byte) (*hostHTTPStream, int, http.Header, error) {
	if len(body) == 0 && req != nil && req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, 0, nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	stream, statusCode, headers, err := hostHTTPDoStream(req)
	if err != nil || statusCode != http.StatusBadRequest {
		return stream, statusCode, headers, err
	}
	payload, readErr := io.ReadAll(newHostStreamReader(stream))
	stream.Close()
	if readErr != nil {
		return nil, statusCode, headers, readErr
	}
	if !isInvalidReasoningEffort(statusCode, string(payload)) {
		return &hostHTTPStream{reader: io.NopCloser(bytes.NewReader(payload))}, statusCode, headers, nil
	}
	fallbackBody, changed := removeUnsupportedReasoningEffort(body)
	if !changed {
		return &hostHTTPStream{reader: io.NopCloser(bytes.NewReader(payload))}, statusCode, headers, nil
	}
	retryReq, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), bytes.NewReader(fallbackBody))
	if err != nil {
		return nil, 0, nil, err
	}
	retryReq.Header = req.Header.Clone()
	return hostHTTPDoStream(retryReq)
}

// modelThrottleCode is the upstream's business code for a MODEL-level frequency
// limit ("usage exceeds frequency limit ... will reset at ..."). The credential
// itself is fine — its other models keep working — which is why it must not be
// conflated with an account failure.
const modelThrottleCode = 6004

// isModelRateLimited reports an upstream 429 that is the model-level frequency
// limit (code 6004). CPA natively scopes the cooldown that follows a 429 to the
// failing model on that credential, so reporting 429 (instead of rewriting the
// status) gives exactly the reference behavior — one throttled model never
// takes the account's other models down.
func isModelRateLimited(statusCode int, payload string) bool {
	if statusCode != http.StatusTooManyRequests {
		return false
	}
	var shape upstreamErrorShape
	if err := json.Unmarshal([]byte(payload), &shape); err == nil && shape.Code == modelThrottleCode {
		return true
	}
	low := strings.ToLower(payload)
	return strings.Contains(low, "6004") &&
		(strings.Contains(low, "frequency limit") || strings.Contains(low, "usage exceeds"))
}

// parseUpstreamResetAt extracts the recovery stamp the upstream embeds in
// code-6004 bodies ("... your usage will reset at 2026-09-19 18:29:03 UTC+8 ...").
// Tolerant by design: an unparseable body must never break the error path, so
// a zero time is returned when no stamp is found.
func parseUpstreamResetAt(payload string) time.Time {
	m := resetAtPattern.FindStringSubmatch(payload)
	if m == nil {
		return time.Time{}
	}
	stamp := strings.Replace(m[1], "T", " ", 1)
	loc := time.Local
	if m[2] != "" {
		hours, errH := strconv.Atoi(m[2])
		minutes := 0
		if m[3] != "" {
			if v, errM := strconv.Atoi(m[3]); errM == nil {
				minutes = v
			}
		}
		if errH == nil {
			if hours < 0 {
				minutes = -minutes
			}
			loc = time.FixedZone("upstream", hours*3600+minutes*60)
		}
	}
	at, err := time.ParseInLocation("2006-01-02 15:04:05", stamp, loc)
	if err != nil {
		return time.Time{}
	}
	return at
}

// resetAtPattern captures "reset at YYYY-MM-DD HH:MM:SS" plus the optional
// "UTC±H[:MM]" offset that follows it in code-6004 bodies.
var resetAtPattern = regexp.MustCompile(
	`reset at\s+(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2})(?:\s*UTC([+-]\d{1,2})(?::?(\d{2}))?)?`)

// isContentRejected reports an upstream chat 403. On the WorkBuddy chat
// gateways a 403 is the content-review filter blocking THIS request's content
// (code 11140 and friends) — the credential and the model stay healthy, and
// the same request with different content succeeds. Learned from
// workbuddy2api-hub (PR #28): treating 403 as a credential-class failure
// poisons the pool for content that was merely blocked once.
func isContentRejected(statusCode int) bool {
	return statusCode == http.StatusForbidden
}

// isModelNotRegistered reports whether an upstream >=400 chat payload is the
// model-catalog rejection (code 11102 "service info not found"). The JSON
// path is authoritative; the substring path catches envelope variants where
// the payload is not the plain business JSON (e.g. wrapped in HTML or a
// different envelope) but still carries the same markers.
func isModelNotRegistered(statusCode int, payload string) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	var shape upstreamErrorShape
	if err := json.Unmarshal([]byte(payload), &shape); err == nil && shape.Code == 11102 {
		return true
	}
	low := strings.ToLower(payload)
	return strings.Contains(low, "11102") && strings.Contains(low, "service info not found")
}

// realmDisplayName maps a realm key to the human-readable name used in error
// copy so users can tell WHICH gateway rejected the model.
func realmDisplayName(realm string) string {
	switch realm {
	case regionIntl:
		return "Intl / CodeBuddy (codebuddy.ai)"
	case regionGlobal:
		return "Intl / WorkBuddy (workbuddy.ai)"
	default:
		return "WorkBuddy CN (copilot.tencent.com)"
	}
}

// modelHintForRealm renders the best-known model catalog for a realm.
// Cached realm discovery (if fresh) is the truth; otherwise the realm's
// static catalog is shown (v0.12.19: per-realm — the CN list is no longer
// shown for Intl/Global accounts, which used to advertise models their
// gateway rejects with 11102). Network calls are deliberately NOT made here:
// the executor error path must stay fast, and a doomed 15s discovery call
// during error handling would only add latency.
func modelHintForRealm(realm string) string {
	ids := make([]string, 0, 16)
	if ms, ok := cachedDynamicModelsForRealm(realm); ok {
		for _, m := range ms {
			if id := strings.TrimSpace(m.ID); id != "" && !isGloballyDisabledModel(id) {
				ids = append(ids, id)
			}
		}
	}
	label := "该区域缓存目录 / cached realm catalog"
	if len(ids) == 0 {
		for _, m := range staticModelsForRealm(displayRegionForService(realm)) {
			if !isGloballyDisabledModel(m.ID) {
				ids = append(ids, m.ID)
			}
		}
		realmTag := strings.ToUpper(displayRegionForService(realm))
		if realmTag == "" {
			realmTag = "CN"
		}
		label = fmt.Sprintf("静态 %s 目录（内置参考，未验证此账号；动态发现优先，也可用 models_%s 配置写死） / static %s catalog (built-in reference, unverified for this account; dynamic discovery wins, or pin via models_%s)",
			realmTag, strings.ToLower(realmTag), realmTag, strings.ToLower(realmTag))
	}
	if len(ids) > 20 {
		ids = ids[:20]
	}
	return fmt.Sprintf("%s: %s", label, strings.Join(ids, ", "))
}

// translateChatUpstreamError converts one upstream chat failure into the
// effective HTTP status plus the plugin error. Three rejections get dedicated,
// CPA-native handling; every other failure keeps the historical
// "upstream <status>: <payload>" shape so existing log parsers and client
// behavior stay unchanged.
//
//   - 11102 → raw status (400), bilingual realm-aware message.
//   - 6004 → raw status (429). CPA cools only the failing model on this
//     credential, which is the correct scope for a model-level throttle;
//     the message names the reset stamp the upstream declared.
//   - 403 → REMAPPED to 400. CPA's cooldown layer classifies 403 as a
//     credential failure (30-minute model cooldown + auth error state), but a
//     chat 403 here is the content-review filter rejecting the request body.
//     Reporting it request-scoped (400 = no cooldown, rotation continues)
//     keeps one blocked request from poisoning the account — the same
//     "403 直通，不毒化账号池" rule workbuddy2api-hub adopted.
func translateChatUpstreamError(statusCode int, payload string, sa *storedAuth) (int, error) {
	if isModelNotRegistered(statusCode, payload) {
		realm := "cn"
		if sa != nil {
			realm = accountServiceRegion(sa)
		}
		return statusCode, fmt.Errorf(
			"模型未被该账号区域的上游注册（code 11102 service info not found），区域=%s；请改用该区域可用模型后重试，模型列表以 /models 实际返回为准。"+
				" // Model not registered on the %s upstream; pick a model from its catalog and retry. %s | raw: %s",
			realmDisplayName(realm), realmDisplayName(realm), modelHintForRealm(realm),
			truncateRedacted(payload, 200))
	}
	if isModelRateLimited(statusCode, payload) {
		msg := "该模型在当前账号触发上游频控（code 6004 模型级限流）：账号本身可用，其他模型不受影响，CPA 会冷却该账号的此模型并轮询其他账号" +
			" // Upstream model-level rate limit (code 6004): the account is fine, only this model is throttled; CPA cools this model on this credential and rotates on"
		if reset := parseUpstreamResetAt(payload); !reset.IsZero() {
			msg += fmt.Sprintf("，上游声明恢复时间 %s / upstream reset at %s",
				reset.Local().Format("2006-01-02 15:04:05"), reset.Local().Format("2006-01-02 15:04:05 MST"))
		}
		return statusCode, fmt.Errorf("%s | raw: %s", msg, truncateRedacted(payload, 200))
	}
	if isContentRejected(statusCode) {
		return http.StatusBadRequest, fmt.Errorf(
			"上游内容审核拒绝了本次请求（HTTP 403）：被拦截的是本次请求的内容，账号与模型均未失效，修改提示词后重试即可，无需禁用账号。"+
				" // The upstream content filter rejected this request (403). The credential and model are healthy — revise the prompt and retry; the account is not penalized. | raw: %s",
			truncateRedacted(payload, 200))
	}
	return statusCode, fmt.Errorf("upstream %d: %s", statusCode, truncateRedacted(payload, 200))
}
