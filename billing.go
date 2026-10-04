// billing.go owns the upstream billing API surface: check-in status, user
// resource (credits / packages), payment type, and the perform-* call wrappers
// for daily check-in and trial claim. Includes the shared JSON helpers used
// to tolerate the upstream's loosely-typed response shapes, and the region
// helpers that decide CN vs Global endpoint.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// upstreamClockOffsetMs records the clock offset observed in the Date header of
// the most recent upstream billing response. Native quota responses report it
// (serverTimeOffsetMs) so management UIs can render reset countdowns against
// upstream time instead of assuming the browser and host clocks agree.
var upstreamClockOffsetMs atomic.Int64

// realm values used for login routing and panel display.
const (
	regionCN     = "cn"
	regionGlobal = "global"
	regionIntl   = "intl"
)

// normalizeAuthDomain accepts the bare host values written by official clients
// as well as URL-shaped imports from management tools. Routing must compare
// hostnames, not raw strings: case, a trailing dot, a scheme, or a path must
// not change the selected upstream realm.
func normalizeAuthDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	candidate := raw
	if !strings.Contains(candidate, "://") {
		candidate = "//" + candidate
	}
	if parsed, err := url.Parse(candidate); err == nil {
		if host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(parsed.Hostname())), "."); host != "" {
			return host
		}
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
}

// isGlobalDomain reports whether the domain belongs to the international
// (www.workbuddy.ai) WorkBuddy service. Global tokens must use workbuddy.ai;
// sending them to the CN gateway produces an upstream 401.
func isGlobalDomain(domain string) bool {
	d := normalizeAuthDomain(domain)
	return d == "workbuddy.ai" || strings.HasSuffix(d, ".workbuddy.ai")
}

// isIntlDomain reports whether the domain belongs to the codebuddy.ai (Intl)
// realm adopted from the merged codebuddy-intl plugin (v0.11.0).
func isIntlDomain(domain string) bool {
	d := normalizeAuthDomain(domain)
	return d == "codebuddy.ai" || strings.HasSuffix(d, ".codebuddy.ai")
}

// isCNDomain covers the domestic hosts seen in WorkBuddy/CodeBuddy exports.
// Empty/unknown domains remain compatible with legacy CN files through the
// accountRegion fallback below.
func isCNDomain(domain string) bool {
	d := normalizeAuthDomain(domain)
	return d == "codebuddy.cn" || strings.HasSuffix(d, ".codebuddy.cn") ||
		d == "copilot.tencent.com" || strings.HasSuffix(d, ".tencent.com") ||
		d == "workbuddy.cn" || strings.HasSuffix(d, ".workbuddy.cn")
}

// panelRegion is the user-visible account grouping: CN or Intl. The upstream
// service is tracked separately so legacy workbuddy.ai accounts retain their
// own endpoint and account-specific behavior.
func panelRegion(sa *storedAuth) string {
	return accountRegion(sa)
}

// accountServiceRegion retains the actual upstream gateway identity. "global"
// is only an internal compatibility key for legacy workbuddy.ai credentials.
func accountServiceRegion(sa *storedAuth) string {
	if sa == nil {
		return regionCN
	}
	// The gateway hostname is authoritative when available. Region is a public
	// grouping field and may already be migrated from legacy Global to Intl.
	if isGlobalDomain(sa.Auth.Domain) {
		return regionGlobal
	}
	if isIntlDomain(sa.Auth.Domain) {
		return regionIntl
	}
	if isCNDomain(sa.Auth.Domain) {
		return regionCN
	}
	if strings.TrimSpace(sa.Auth.Domain) == "" && isGlobalToken(sa.Auth.AccessToken) {
		return regionGlobal
	}
	switch strings.ToLower(strings.TrimSpace(sa.Auth.Region)) {
	case regionGlobal:
		return regionGlobal
	case regionIntl:
		return regionIntl
	case regionCN:
		return regionCN
	default:
		return regionCN
	}
}

func isWorkBuddyService(sa *storedAuth) bool {
	return accountServiceRegion(sa) == regionGlobal
}

func displayRegionForService(service string) string {
	if service == regionGlobal || service == regionIntl {
		return regionIntl
	}
	return regionCN
}

// accountRegion is the public/business region. Legacy Global is accepted as
// Intl after the product regions were merged.
func accountRegion(sa *storedAuth) string {
	if sa == nil {
		return regionCN
	}
	if isGlobalDomain(sa.Auth.Domain) || isIntlDomain(sa.Auth.Domain) {
		return regionIntl
	}
	if isCNDomain(sa.Auth.Domain) {
		return regionCN
	}
	if strings.TrimSpace(sa.Auth.Domain) == "" && isGlobalToken(sa.Auth.AccessToken) {
		return regionIntl
	}
	switch strings.ToLower(strings.TrimSpace(sa.Auth.Region)) {
	case regionIntl, regionGlobal:
		return regionIntl
	case regionCN:
		return regionCN
	default:
		return regionCN
	}
}

// upstreamBaseForRegion returns the chat/auth gateway for a login region.
func upstreamBaseForRegion(region string) string {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case regionIntl:
		return upstreamBaseIntl
	case regionGlobal:
		return upstreamBaseGlobal
	default:
		return upstreamBaseCN
	}
}

func upstreamBaseForService(service string) string {
	return upstreamBaseForRegion(service)
}

func upstreamBaseForAuth(sa *storedAuth) string {
	return upstreamBaseForRegion(accountServiceRegion(sa))
}

// applyRealmHeaders adjusts protocol headers for the Intl (codebuddy.ai)
// realm: the Intl gateway expects the IDE client header set (X-IDE-Type
// "IDE", product 1.100.0) and no X-Requested-With. Runs AFTER
// applyPlatformHeaders so Intl values win for adopted codebuddy-intl
// accounts (their LoginPlatform is "ide").
func applyRealmHeaders(req *http.Request, sa *storedAuth) {
	if sa == nil || accountServiceRegion(sa) != regionIntl {
		return
	}
	req.Header.Del("X-Requested-With")
	req.Header.Set("X-IDE-Type", "IDE")
	req.Header.Set("X-IDE-Name", "CodeBuddy")
	req.Header.Set("X-IDE-Version", "1.100.0")
	req.Header.Set("X-Product-Version", "1.100.0")
}

// setBillingBase temporarily overrides billingBase for tests; returns a
// restore func.
func setBillingBase(s string) func() {
	old := billingBase
	billingBase = s
	bridgeRestore := installHostHTTPTestDirect()
	return func() { bridgeRestore(); billingBase = old }
}

// setBillingBaseGlobal temporarily overrides billingBaseGlobal for tests.
func setBillingBaseGlobal(s string) func() {
	old := billingBaseGlobal
	billingBaseGlobal = s
	bridgeRestore := installHostHTTPTestDirect()
	return func() { bridgeRestore(); billingBaseGlobal = old }
}

// billingBaseFor returns the billing API base URL for the given auth's domain.
// CN accounts → https://www.codebuddy.cn; Global → https://www.workbuddy.ai.
// Falls back to the test-overridable billingBase for CN/nil.
// billingBaseIntl is the CodeBuddy Intl (www.codebuddy.ai) billing base.
// v0.12.10 fix: Intl accounts were posting check-in / meter calls to the CN
// gas station (www.codebuddy.cn) where their Bearer tokens are unknown — the
// APISIX gateway answered with the 401 HTML page that surfaced as
// "parse failed: invalid character '<'" right after a successful Intl login.
var billingBaseIntl = "https://www.codebuddy.ai"

func billingBaseFor(sa *storedAuth) string {
	switch accountServiceRegion(sa) {
	case regionGlobal:
		return billingBaseGlobal
	case regionIntl:
		return billingBaseIntl
	default:
		return billingBase
	}
}

// -----------------------------------------------------------------------------
// Billing / check-in API calls
// -----------------------------------------------------------------------------

func billingHeaders(req *http.Request, sa *storedAuth) {
	// Billing is an authenticated existing-account request; use the same realm
	// and identity contract as chat and refresh, but never expose refreshToken.
	authHeadersFor(req, sa, false)
	req.Header.Set("Accept", "application/json")
}

func billingCall(sa *storedAuth, path string, body any) (json.RawMessage, error) {
	return billingCallWithClient(context.Background(), nil, sa, path, body)
}

func billingCallWithClient(ctx context.Context, client pluginapi.HostHTTPClient, sa *storedAuth, path string, body any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := billingCallOnceWithClient(ctx, client, sa, path, body)
	for _, d := range billingRetryDelays {
		if err == nil || !isTransientUpstreamErr(err) {
			break
		}
		// Honour the upstream's own Retry-After (bounded) over our backoff:
		// retrying a rate-limited endpoint too early just burns another 429.
		delay := retryDelayFor(err, d)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		data, err = billingCallOnceWithClient(ctx, client, sa, path, body)
	}
	return data, err
}

// isTransientBillingErr is kept as a thin alias for callers/tests that still
// use the old name; it delegates to the typed classifier.
func isTransientBillingErr(err error) bool { return isTransientUpstreamErr(err) }

func billingCallOnce(sa *storedAuth, path string, body any) (json.RawMessage, error) {
	return billingCallOnceWithClient(context.Background(), nil, sa, path, body)
}

func billingCallOnceWithClient(ctx context.Context, client pluginapi.HostHTTPClient, sa *storedAuth, path string, body any) (json.RawMessage, error) {
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = json.Marshal(body)
	} else {
		bodyBytes = []byte("{}")
	}
	base := billingBaseFor(sa)
	if client != nil {
		req := pluginapi.HTTPRequest{
			Method:  http.MethodPost,
			URL:     base + path,
			Headers: make(http.Header),
			Body:    bodyBytes,
		}
		nativeReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		billingHeaders(nativeReq, sa)
		req.Headers = nativeReq.Header.Clone()
		resp, err := client.Do(ctx, req)
		if err != nil {
			return nil, err
		}
		return parseBillingHTTPResponse(resp, path)
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, reader)
	if err != nil {
		return nil, err
	}
	billingHeaders(req, sa)
	// Route via host.http.do so request-log captures the call (v0.8.1 compliance:
	// was sharedHTTPClient().Do — bypassed host transport policy + logging).
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	return parseBillingHTTPResponse(pluginapi.HTTPResponse{StatusCode: resp.StatusCode, Headers: resp.Headers, Body: resp.Body}, path)
}

// upstreamError is a typed billing/upstream failure. Classification used to be
// string matching on err.Error() ("http 5..."), which broke as soon as another
// layer wrapped the error — and could never express 429, the one status that
// most needs a retry. Callers now use errors.As.
type upstreamError struct {
	StatusCode int           // HTTP status (0 for transport-level failures)
	Path       string        // upstream path, for diagnostics
	Snippet    string        // redacted body excerpt (bounded)
	RetryAfter time.Duration // parsed Retry-After when the upstream sent one
	Err        error         // wrapped transport error, when any
}

func (e *upstreamError) Error() string {
	if e == nil {
		return "upstream error"
	}
	if e.Err != nil {
		return fmt.Sprintf("upstream %s: %v", e.Path, e.Err)
	}
	msg := fmt.Sprintf("http %d from %s", e.StatusCode, e.Path)
	if e.Snippet != "" {
		msg += ": " + e.Snippet
	}
	return msg
}

func (e *upstreamError) Unwrap() error { return e.Err }

// isTransientUpstreamErr reports whether the failure is worth retrying:
// transport errors, 429 (rate limit), and 5xx. 4xx business rejections are not.
func isTransientUpstreamErr(err error) bool {
	if err == nil {
		return false
	}
	var ue *upstreamError
	if errors.As(err, &ue) {
		if ue.Err != nil {
			return true // transport failure: connection reset, timeout, DNS...
		}
		return ue.StatusCode == http.StatusTooManyRequests || ue.StatusCode >= 500
	}
	// Unknown error type (wrapped by another layer without upstreamError in the
	// chain): treat conservatively as non-retryable rather than retrying a
	// possible business rejection.
	return false
}

// retryDelayFor returns how long to wait before the next attempt, honouring an
// upstream Retry-After when present and sane, else the configured backoff.
func retryDelayFor(err error, fallback time.Duration) time.Duration {
	var ue *upstreamError
	if errors.As(err, &ue) && ue.RetryAfter > 0 {
		if ue.RetryAfter > maxRetryAfter {
			return maxRetryAfter
		}
		return ue.RetryAfter
	}
	return fallback
}

// maxRetryAfter caps an upstream Retry-After so a hostile/large value cannot
// stall a dashboard refresh.
const maxRetryAfter = 30 * time.Second

// parseRetryAfter reads a Retry-After header in both documented forms
// (delta-seconds and HTTP-date). Returns 0 when absent or unparseable.
func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

func parseBillingHTTPResponse(resp pluginapi.HTTPResponse, path string) (json.RawMessage, error) {
	raw := resp.Body
	if resp.Headers != nil {
		if dateHeader := resp.Headers.Get("Date"); dateHeader != "" {
			if parsed, errParse := http.ParseTime(dateHeader); errParse == nil {
				upstreamClockOffsetMs.Store(parsed.Sub(time.Now()).Milliseconds())
			}
		}
	}
	// 429 and 5xx are transient — classify them (typed) so billingCall can
	// retry, honouring Retry-After, and keep a redacted snippet for diagnosis.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		var retryAfter time.Duration
		if resp.Headers != nil {
			retryAfter = parseRetryAfter(resp.Headers.Get("Retry-After"))
		}
		return nil, &upstreamError{
			StatusCode: resp.StatusCode,
			Path:       path,
			Snippet:    redactedSnippet(raw, 120),
			RetryAfter: retryAfter,
		}
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// parse failed usually means upstream returned a non-JSON error page
		// (e.g. APISIX 401 HTML for session-dead). Include a redacted snippet
		// so the panel / logs can surface the real cause instead of a bare
		// "parse failed" (P0-2 UX: was impossible to distinguish session dead
		// from a malformed response).
		return nil, fmt.Errorf("parse failed: %w (body: %s)", err, redactedSnippet(raw, 120))
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("code=%d msg=%s", env.Code, truncateRedacted(env.Msg, 120))
	}
	return env.Data, nil
}

// redactedSnippet returns a secret-free, length-bounded body excerpt.
func redactedSnippet(raw []byte, limit int) string {
	snippet := strings.TrimSpace(redactSecrets(string(raw)))
	if len(snippet) > limit {
		snippet = snippet[:limit]
	}
	return snippet
}

func fetchCheckinStatus(sa *storedAuth) (*checkinSummary, error) {
	var data json.RawMessage
	var lastErr error
	for _, path := range []string{"/v2/billing/meter/checkin-activity-status", "/v2/billing/meter/checkin-status"} {
		d, err := billingCall(sa, path, nil)
		if err == nil {
			data = d
			lastErr = nil
			break
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	sum := &checkinSummary{
		Active:          jsonBool(m, "active", "Active"),
		TodayCheckedIn:  jsonBool(m, "today_checked_in", "todayCheckedIn"),
		StreakDays:      jsonI64(m, "streak_days", "streakDays"),
		DailyCredit:     jsonI64(m, "daily_credit", "dailyCredit"),
		TodayCredit:     jsonI64(m, "today_credit", "todayCredit"),
		TotalCredits:    jsonI64(m, "total_credits", "totalCredits"),
		WeekCheckinDays: jsonI64(m, "week_checkin_days", "weekCheckinDays"),
		ActivityName:    jsonStr(m, "activity_name", "activityName"),
		Season:          jsonI64(m, "season", "season"),
	}
	if dates, ok := m["checkin_dates"].([]any); ok {
		for _, d := range dates {
			if s, ok := d.(string); ok {
				sum.CheckinDates = append(sum.CheckinDates, s)
			}
		}
	} else if dates, ok := m["checkinDates"].([]any); ok {
		for _, d := range dates {
			if s, ok := d.(string); ok {
				sum.CheckinDates = append(sum.CheckinDates, s)
			}
		}
	}
	return sum, nil
}

// packageRemainUsed picks current-cycle remain/used/size for one package.
// Prefer cycle metrics whenever CycleCapacitySize is present; used = size−remain
// so missing CycleCapacityUsed never under-reports consumption.
// Fall back to lifetime Capacity* only when cycle fields are absent entirely.
//
// Daily check-in adds NEW packages (size grows) — capacity grant, not negative
// consumption. Track consumption via used (size−remain), not via remain alone.
func packageRemainUsed(a resourcePackage) (remain, used, size int64) {
	if a.CycleCapacitySize > 0 {
		remain = a.CycleCapacityRemain
		size = a.CycleCapacitySize
		if remain < 0 {
			remain = 0
		}
		if remain > size {
			remain = size
		}
		used = size - remain
		// If upstream reports a higher explicit used, trust the larger figure.
		if a.CycleCapacityUsed > used {
			used = a.CycleCapacityUsed
			// Keep remain consistent when possible.
			if size >= used {
				remain = size - used
			}
		}
		return remain, used, size
	}
	if a.CycleCapacityRemain > 0 || a.CycleCapacityUsed > 0 {
		remain = a.CycleCapacityRemain
		used = a.CycleCapacityUsed
		// A-41: clamp negatives (branch1 already clamps; branch2/3 did not).
		if remain < 0 {
			remain = 0
		}
		if used < 0 {
			used = 0
		}
		size = remain + used
		if a.CapacitySize > size {
			size = a.CapacitySize
			if size >= remain {
				used = size - remain
			}
		}
		return remain, used, size
	}
	remain = a.CapacityRemain
	used = a.CapacityUsed
	size = a.CapacitySize
	// A-41: lifetime branch also clamps negative remain/used.
	if remain < 0 {
		remain = 0
	}
	if used < 0 {
		used = 0
	}
	if size <= 0 {
		size = remain + used
	}
	if used == 0 && size > remain {
		used = size - remain
	}
	return remain, used, size
}

func fetchUserResource(sa *storedAuth) (*creditsSummary, error) {
	return fetchUserResourceWithClient(context.Background(), nil, sa)
}

func fetchUserResourceWithClient(ctx context.Context, client pluginapi.HostHTTPClient, sa *storedAuth) (*creditsSummary, error) {
	now := time.Now()
	// Status 0=active, 3=exhausted-but-still-listed. PageSize 100 covers the
	// multi-pack free accounts we see in production; paginate if TotalCount
	// ever exceeds it.
	const pageSize = 100
	body := map[string]any{
		"PageNumber":               1,
		"PageSize":                 pageSize,
		"ProductCode":              "p_tcaca",
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format("2006-01-02 15:04:05"),
		"PackageEndTimeRangeEnd":   now.Add(365 * 101 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
	}
	data, err := billingCallWithClient(ctx, client, sa, "/v2/billing/meter/get-user-resource", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Response struct {
			Data struct {
				TotalCount  int64             `json:"TotalCount"`
				TotalDosage int64             `json:"TotalDosage"` // package capacity pool, NOT consumption
				Accounts    []resourcePackage `json:"Accounts"`
			} `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	// Aggregate ALL packages (体验版 + 多个签到/裂变包 + 其它赠送包).
	// Remain = currently spendable. Used = consumed this cycle. Size = capacity.
	// Daily check-in adds packages → Size and Remain go UP; that is grant, not usage.
	sum := &creditsSummary{}
	for _, a := range resp.Response.Data.Accounts {
		remain, used, size := packageRemainUsed(a)
		sum.TotalRemain += remain
		sum.TotalUsed += used
		sum.TotalSize += size
		sum.Packages = append(sum.Packages, packageSummary{
			Name:       a.PackageName,
			Remain:     remain,
			Used:       used,
			Size:       size,
			CycleStart: a.CycleStartTime,
			CycleEnd:   a.CycleEndTime,
		})
	}
	sum.PackCount = len(sum.Packages)
	// Reconcile used with size-remain so UI totals always add up when size known.
	if sum.TotalSize > 0 {
		derived := sum.TotalSize - sum.TotalRemain
		if derived < 0 {
			derived = 0
		}
		// Prefer the larger of reported-used vs size-remain (never under-report spend).
		if derived > sum.TotalUsed {
			sum.TotalUsed = derived
		}
	}
	// Upstream TotalDosage is the capacity pool (~sum of package sizes), not spend.
	// Use it only as a size floor when pack sizes look incomplete.
	if dosage := resp.Response.Data.TotalDosage; dosage > sum.TotalSize {
		sum.TotalSize = dosage
		derived := sum.TotalSize - sum.TotalRemain
		if derived < 0 {
			derived = 0
		}
		if derived > sum.TotalUsed {
			sum.TotalUsed = derived
		}
	}
	_ = resp.Response.Data.TotalCount
	return sum, nil
}

func fetchPaymentType(sa *storedAuth) string {
	return fetchPaymentTypeWithClient(context.Background(), nil, sa)
}

func fetchPaymentTypeWithClient(ctx context.Context, client pluginapi.HostHTTPClient, sa *storedAuth) string {
	data, err := billingCallWithClient(ctx, client, sa, "/v2/billing/meter/get-payment-type", nil)
	if err != nil {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	if s, ok := m["paymentType"].(string); ok {
		return s
	}
	if s, ok := m["payment_type"].(string); ok {
		return s
	}
	return ""
}

func performCheckinCall(sa *storedAuth) (map[string]any, error) {
	data, err := billingCall(sa, "/v2/billing/meter/daily-checkin", nil)
	if err != nil {
		// billingCall returns business errors (code != 0) as Go errors; surface
		// them as a structured result so the panel can show "already checked in".
		return map[string]any{"success": false, "message": err.Error()}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	// Normalise success to a real bool (upstream sometimes returns the string
	// "true", which silently failed `out["success"] == true` downstream —
	// P1-2). Coerce the upstream's own value instead of forcing true: an
	// explicit `success:false` ("您今日已签到" and friends) must stay false so
	// checkinOneAccount can classify it as an "already" skip instead of
	// reporting a soft failure as a success.
	m["success"] = coerceBool(m["success"], true)
	return m, nil
}

// coerceBool interprets the loosely-typed boolean values the upstream emits
// (bool, "true"/"false", 1/0) and falls back to def when the value is absent
// or unrecognisable.
func coerceBool(v any, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case nil:
		return def
	}
	return def
}

// performTrialCall claims the one-time expert trial pack for a Global account.
// Endpoint: POST /billing/ide/trial (note: NOT under /v2/billing/meter/).
// First call: success, +250 credits, 14-day "CodeBuddy One-time Free 2-Week
// Pro Plan Trial".
// Repeat call: code=14051 "has applied trial" — surfaced as already_claimed.
func performTrialCall(sa *storedAuth) (map[string]any, error) {
	data, err := billingCall(sa, "/billing/ide/trial", nil)
	if err != nil {
		msg := err.Error()
		// code=14051 means the trial has already been claimed — not a real error.
		if strings.Contains(msg, "14051") {
			return map[string]any{
				"success":         false,
				"message":         "已领取过专家加油包",
				"already_claimed": true,
			}, nil
		}
		return map[string]any{"success": false, "message": msg}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	// Same coercion as performCheckinCall: keep an explicit upstream failure.
	m["success"] = coerceBool(m["success"], true)
	return m, nil
}

// hasTrialPack reports whether the credits summary already contains the
// Global expert trial pack (one-time, 14-day, 250 credits). Used for the
// panel "claim trial" button state (trial_claimed).
//
// Do NOT match bare Chinese "体验": CN free-tier is literally named
// "CodeBuddy个人体验版" / "体验版" and must remain unclaimed-looking for Global
// trial UI (A-18). Prefer English trial markers from live Global packs.
func hasTrialPack(cr *creditsSummary) bool {
	if cr == nil {
		return false
	}
	for _, p := range cr.Packages {
		name := strings.ToLower(strings.TrimSpace(p.Name))
		if name == "" {
			continue
		}
		// Live Global: "CodeBuddy One-time Free 2-Week Pro Plan Trial"
		if strings.Contains(name, "trial") {
			return true
		}
		// Alternate English shapes (keep without bare "体验")
		if strings.Contains(name, "pro plan") && (strings.Contains(name, "free") || strings.Contains(name, "one-time") || strings.Contains(name, "2-week") || strings.Contains(name, "2 week")) {
			return true
		}
		// Explicit expert-pack Chinese labels only — never bare 体验/体验版.
		if strings.Contains(name, "专家加油") || strings.Contains(name, "专家体验包") {
			return true
		}
	}
	return false
}

// isCreditsExhausted is the shared "耗尽" definition for panel + scheduler.
// Exhausted = we have usage signal and no remaining credits.
// Missing credits data is NOT exhausted (unknown).
func isCreditsExhausted(cr *creditsSummary) bool {
	if cr == nil {
		return false
	}
	if cr.TotalRemain > 0 {
		return false
	}
	// remain==0: exhausted only when we know there was/is a package total
	// (used>0, size>0, or packages present). Pure zero with no packages = no data.
	if cr.TotalUsed > 0 || cr.TotalSize > 0 {
		return true
	}
	return len(cr.Packages) > 0
}

func jsonBool(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case bool:
				return t
			case float64:
				return t != 0
			case string:
				return t == "true" || t == "1"
			}
		}
	}
	return false
}

func jsonI64(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int64(t)
			case int64:
				return t
			case string:
				var n int64
				fmt.Sscanf(t, "%d", &n)
				return n
			}
		}
	}
	return 0
}

func jsonStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}
