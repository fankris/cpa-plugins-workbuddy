// management.go implements the WorkBuddy management API and web panel:
// account dashboard (nickname, credits, plan, check-in streak), manual/auto
// check-in (daily at 09:00 and 21:00 local time), and quota refresh.
package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// billingBase hosts the Buddy-gas-station check-in and resource-package APIs.
// It is a var (not const) so tests can override it with an httptest server.
var billingBase = "https://www.codebuddy.cn"

// billingBaseGlobal is the international (www.workbuddy.ai) billing base.
var billingBaseGlobal = "https://www.workbuddy.ai"

// If the panel later wants to surface "usage export ready", re-add it and wire
// it into buildDashboardEx's response.

// -----------------------------------------------------------------------------
// Account listing via host auth callbacks
// -----------------------------------------------------------------------------

type creditsSummary struct {
	// TotalRemain is currently usable credits across all active packages.
	TotalRemain int64 `json:"total_remain"`
	// TotalUsed is consumed credits in the current cycle (sum of packages).
	TotalUsed int64 `json:"total_used"`
	// TotalSize is the credit capacity/pool (sum of package sizes). remain+used ≈ size.
	TotalSize int64 `json:"total_size"`
	// PackCount is number of resource packages included in the aggregate.
	PackCount int `json:"pack_count"`
	// FetchedAt is when this snapshot was taken (RFC3339). Upstream billing lag
	// can make remain/used look "stuck" for minutes after chat; compare this
	// timestamp — not only the numbers — when diagnosing frozen credits.
	FetchedAt string           `json:"fetched_at,omitempty"`
	Packages  []packageSummary `json:"packages"`
}

type packageSummary struct {
	Name       string `json:"name"`
	Remain     int64  `json:"remain"`
	Used       int64  `json:"used"`
	Size       int64  `json:"size"`
	CycleStart string `json:"cycle_start"`
	CycleEnd   string `json:"cycle_end"`
}

type checkinSummary struct {
	Active          bool     `json:"active"`
	TodayCheckedIn  bool     `json:"today_checked_in"`
	StreakDays      int64    `json:"streak_days"`
	DailyCredit     int64    `json:"daily_credit"`
	TodayCredit     int64    `json:"today_credit"`
	TotalCredits    int64    `json:"total_credits"`
	WeekCheckinDays int64    `json:"week_checkin_days"`
	ActivityName    string   `json:"activity_name"`
	Season          int64    `json:"season"`
	CheckinDates    []string `json:"checkin_dates,omitempty"`
}

// with a transient error (HTTP 5xx or transport error). codebuddy.cn
// intermittently returns 500s; without a retry a single hiccup surfaces as a
// panel error even though the very next request would succeed.
var billingRetryDelays = []time.Duration{300 * time.Millisecond, 900 * time.Millisecond}

// CapacityRemain/Used/Size         — lifetime package totals (Used often ≈0
//
//	for monthly-refresh free packs)
//
// CycleCapacityRemain/Used/Size    — the active billing cycle; Used is
//
//	sometimes omitted entirely
type resourcePackage struct {
	PackageName         string `json:"PackageName"`
	CapacityRemain      int64  `json:"CapacityRemain"`
	CapacityUsed        int64  `json:"CapacityUsed"`
	CapacitySize        int64  `json:"CapacitySize"`
	CycleCapacityRemain int64  `json:"CycleCapacityRemain"`
	CycleCapacityUsed   int64  `json:"CycleCapacityUsed"`
	CycleCapacitySize   int64  `json:"CycleCapacitySize"`
	CycleStartTime      string `json:"CycleStartTime"`
	CycleEndTime        string `json:"CycleEndTime"`
}

// -----------------------------------------------------------------------------
// Auto check-in scheduler (09:00 / 21:00 local)
// -----------------------------------------------------------------------------

// Management API routes + handler
// -----------------------------------------------------------------------------

const managementBodyLimit = 1 << 20

type managementRoute struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Menu        string `json:"menu,omitempty"`
	Description string `json:"description,omitempty"`
}

type resourceRoute struct {
	Path        string `json:"path"`
	Menu        string `json:"menu,omitempty"`
	Description string `json:"description,omitempty"`
}

type managementRegistrationResponse struct {
	Routes    []managementRoute `json:"routes,omitempty"`
	Resources []resourceRoute   `json:"resources,omitempty"`
}

// managementBasePathCache holds the host-injected BasePath so handleManagement
// doesn't hardcode /v0/management. Falls back to the historical default if the
// host doesn't provide one (older CPA builds).
var (
	managementBasePathCache   = "/v0/management"
	managementBasePathCacheMu sync.RWMutex
)

func loadedManagementBasePath() string {
	managementBasePathCacheMu.RLock()
	defer managementBasePathCacheMu.RUnlock()
	return managementBasePathCache
}

func setManagementBasePath(p string) {
	p = cleanHostPath(p)
	if p == "" {
		return
	}
	managementBasePathCacheMu.Lock()
	managementBasePathCache = p
	managementBasePathCacheMu.Unlock()
}

func managementRegistration() managementRegistrationResponse {
	base := "/plugins/" + providerName
	return managementRegistrationResponse{
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: base + "/accounts", Description: "List WorkBuddy accounts with credits, plan and check-in status."},
			{Method: http.MethodPost, Path: base + "/refresh", Description: "Force refresh quota/cache for all accounts."},
			{Method: http.MethodPost, Path: base + "/checkin", Description: "Manually check in one account (auth_index) or all."},
			{Method: http.MethodPost, Path: base + "/checkin/config", Description: "Toggle auto check-in (enabled: true/false)."},
			{Method: http.MethodGet, Path: base + "/credits", Description: "Get real-time credits for one (auth_index query) or all accounts."},
			{Method: http.MethodPost, Path: base + "/import", Description: "Import WorkBuddy credential JSON (nested or flat) into host auth store."},
			{Method: http.MethodPost, Path: base + "/trial", Description: "Claim a one-time expert trial pack for an eligible Intl / WorkBuddy-service account (auth_index)."},
			{Method: http.MethodPost, Path: base + "/select", Description: "Select the active account card used for chat routing (body: {auth_index})."},
			{Method: http.MethodPost, Path: base + "/keepalive", Description: "Manually refresh access tokens for all accounts (or one with auth_index)."},
			{Method: http.MethodGet, Path: base + "/keepalive/status", Description: "Last keepalive run summary + config."},
			{Method: http.MethodGet, Path: base + "/tasks", Description: "List CN growth tasks for one account (auth_index query)."},
			{Method: http.MethodPost, Path: base + "/tasks/accept", Description: "Accept one or more CN growth tasks."},
			{Method: http.MethodPost, Path: base + "/tasks/accept_all", Description: "Accept all currently available CN growth tasks."},
			{Method: http.MethodPost, Path: base + "/tasks/claim", Description: "Claim one completed CN growth task reward."},
			{Method: http.MethodPost, Path: base + "/tasks/light", Description: "Retired: synthetic activity reporting is not supported (HTTP 410)."},
			{Method: http.MethodPost, Path: base + "/tasks/travel", Description: "Run the CN buddy-travel cycle: claim when arrived, depart when idle."},
			{Method: http.MethodPost, Path: base + "/tasks/cancel", Description: "Request cooperative cancellation of an accept-only run; cannot undo completed upstream actions."},
			{Method: http.MethodGet, Path: base + "/tasks/status", Description: "Get growth task operation status."},
			{Method: http.MethodGet, Path: base + "/models", Description: "Per-credential model list resolved through the same path host discovery uses (all accounts, or one with auth_index)."},
			{Method: http.MethodPost, Path: base + "/models/refresh", Description: "Re-run model discovery for one credential (auth_index), bypassing the 5-minute cache."},
			{Method: http.MethodGet, Path: base + "/models/catalog", Description: "List supported models with persistent global-disable state for the panel."},

			{Method: http.MethodGet, Path: base + "/daily-quota", Description: "Today's per-model free-token usage measured from host usage records (all accounts, or one with auth_index)."},
			{Method: http.MethodPost, Path: base + "/daily-quota/reset", Description: "Clear locally recorded daily free-token counters (all accounts, or one with auth_index). Does not restore real upstream allowance."},
		},
		Resources: []resourceRoute{
			{Path: "/panel", Menu: "WorkBuddy", Description: "WorkBuddy dashboard: credits, check-in, plan, import."},
			{Path: "/panel.css", Description: "WorkBuddy panel styles."},
			{Path: "/panel.js", Description: "WorkBuddy panel script."},
			{Path: "/panel-i18n.js", Description: "WorkBuddy language resources."},
		},
	}
}

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if len(req.Body) > managementBodyLimit {
		return okEnvelope(mgmtJSONResponse(http.StatusRequestEntityTooLarge, map[string]any{"error": "管理请求体过大"}))
	}
	path := strings.TrimRight(req.Path, "/")

	// Browser UI resource routes (unauthenticated). The panel shell and its
	// script are served here; each asset carries its own content type.
	resPrefix := loadedResourceBasePath()
	if req.Method == http.MethodGet && (path == resPrefix || strings.HasPrefix(path, resPrefix+"/")) {
		sub := strings.TrimPrefix(path, resPrefix)
		asset := servePanel(sub)
		response := mgmtAssetResponse(asset.contentType, asset.body)
		if asset.statusCode != 0 {
			response.StatusCode = asset.statusCode
		}
		return okEnvelope(response)
	}

	// Rate limit for mutating endpoints (v0.6.31, limits reworked v0.9.25).
	// Plugin-layer key enforcement was removed in 0.9.25 to follow the
	// official plugin spec: management.handle sits solely behind the host
	// management middleware, and every request reaching the plugin has
	// already passed it. The extra management_key gate broke mutating calls
	// (refresh/check-in/import) whenever the configured key differed from
	// the credential the embedding UI actually forwards — CPAMP substitutes
	// its saved CPA key, CPAMC sends the CPA secret — a guaranteed 403 on
	// shared-key misalignment. The per-IP token bucket stays as a cheap
	// abuse guard; capacity covers bulk "check in all" bursts.
	if req.Method == http.MethodPost || mutatingManagementPath(path) {
		if !allowManagementRequest(managementClientIP(req)) {
			return okEnvelope(mgmtJSONResponse(http.StatusTooManyRequests, map[string]any{
				"error": "rate limit exceeded, try again later",
			}))
		}
	}

	base := loadedManagementBasePath() + "/plugins/" + providerName
	switch {
	case req.Method == http.MethodGet && path == base+"/accounts":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, buildDashboardEx(false, false)))
	case req.Method == http.MethodPost && path == base+"/refresh":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, buildDashboardEx(true, true)))
	case req.Method == http.MethodPost && path == base+"/checkin":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleManualCheckin(req)))
	case req.Method == http.MethodPost && path == base+"/checkin/config":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleCheckinConfig(req)))
	case req.Method == http.MethodGet && path == base+"/credits":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleCreditsQuery(req)))
	case req.Method == http.MethodPost && path == base+"/import":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleImportAuth(req)))
	case req.Method == http.MethodPost && path == base+"/trial":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleClaimTrial(req)))
	case req.Method == http.MethodPost && path == base+"/select":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleSelectAuth(req)))
	case req.Method == http.MethodPost && path == base+"/keepalive":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleKeepaliveNow(req)))
	case req.Method == http.MethodGet && path == base+"/keepalive/status":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleKeepaliveStatus()))
	case req.Method == http.MethodGet && path == base+"/tasks":
		result := handleGrowthTaskList(req)
		return okEnvelope(mgmtJSONResponse(taskHTTPStatus(result), result))
	case req.Method == http.MethodPost && path == base+"/tasks/accept":
		result := handleGrowthTaskAccept(req)
		return okEnvelope(mgmtJSONResponse(taskHTTPStatus(result), result))
	case req.Method == http.MethodPost && path == base+"/tasks/accept_all":
		result := handleGrowthTaskAcceptAll(req)
		return okEnvelope(mgmtJSONResponse(taskHTTPStatus(result), result))
	case req.Method == http.MethodPost && path == base+"/tasks/claim":
		result := handleGrowthTaskClaim(req)
		return okEnvelope(mgmtJSONResponse(taskHTTPStatus(result), result))
	case req.Method == http.MethodPost && path == base+"/tasks/light":
		return okEnvelope(mgmtJSONResponse(http.StatusGone, map[string]any{"error": "synthetic activity reporting is not supported; use real task progress and authorized actions", "code": "operation_retired"}))
	case req.Method == http.MethodPost && path == base+"/tasks/travel":
		result := handleGrowthTravel(req)
		return okEnvelope(mgmtJSONResponse(taskHTTPStatus(result), result))
	case req.Method == http.MethodPost && path == base+"/tasks/cancel":
		result := handleGrowthTaskCancel(req)
		return okEnvelope(mgmtJSONResponse(taskHTTPStatus(result), result))
	case req.Method == http.MethodGet && path == base+"/tasks/status":
		result := handleGrowthTaskStatus(req)
		return okEnvelope(mgmtJSONResponse(taskHTTPStatus(result), result))
	case req.Method == http.MethodGet && path == base+"/models":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleModelsQuery(req)))
	case req.Method == http.MethodPost && path == base+"/models/refresh":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleModelsRefresh(req)))
	case req.Method == http.MethodGet && path == base+"/models/catalog":
		models := handleGlobalModelCatalog()
		return okEnvelope(mgmtJSONResponse(http.StatusOK, map[string]any{"models": models, "count": len(models)}))
	case req.Method == http.MethodGet && path == base+"/daily-quota":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleDailyQuotaQuery(req)))
	case req.Method == http.MethodPost && path == base+"/daily-quota/reset":
		return okEnvelope(mgmtJSONResponse(http.StatusOK, handleDailyQuotaReset(req)))
	}
	return okEnvelope(mgmtJSONResponse(http.StatusNotFound, map[string]any{"error": "not found: " + path}))
}

// -----------------------------------------------------------------------------
// Plugin-layer management auth (v0.6.31) was removed in v0.9.25 — the official
// plugin examples implement no plugin-side key check because the host's
// management middleware already guards every /v0/management path, and a second
// key only misaligns with the credential the UI actually forwards. What stays:
//
//   - A per-IP token bucket on mutating endpoints guards against request
//     floods. Capacity must absorb a bulk "check in all" run (one POST per
//     CN account, each bounded by upstream latency); refill is 1/s.

const (
	mgmtRateLimitCapacity = 30
	mgmtRateLimitRefill   = time.Second
	mgmtRateLimitTTL      = 10 * time.Minute
)

type mgmtRateEntry struct {
	tokens   float64
	lastSeen time.Time
}

var (
	mgmtRateLimit   = map[string]*mgmtRateEntry{}
	mgmtRateLimitMu sync.Mutex
)

// allowManagementRequest applies a per-IP token bucket. ip may be empty when the
// host doesn't forward X-Forwarded-For / RemoteAddr — in that case use a single
// global bucket.
func allowManagementRequest(ip string) bool {
	if ip == "" {
		ip = "_global"
	}
	mgmtRateLimitMu.Lock()
	defer mgmtRateLimitMu.Unlock()
	now := time.Now()
	e, ok := mgmtRateLimit[ip]
	if !ok {
		e = &mgmtRateEntry{tokens: mgmtRateLimitCapacity, lastSeen: now}
		mgmtRateLimit[ip] = e
	}
	// Refill.
	elapsed := now.Sub(e.lastSeen)
	e.tokens += float64(elapsed) / float64(mgmtRateLimitRefill)
	if e.tokens > mgmtRateLimitCapacity {
		e.tokens = mgmtRateLimitCapacity
	}
	e.lastSeen = now
	if e.tokens < 1 {
		return false
	}
	e.tokens--
	// Lazy eviction of idle entries (don't grow the map forever).
	if len(mgmtRateLimit) > 1024 {
		for k, v := range mgmtRateLimit {
			if now.Sub(v.lastSeen) > mgmtRateLimitTTL {
				delete(mgmtRateLimit, k)
			}
		}
	}
	return true
}

// managementClientIP extracts a best-effort client identifier for rate limiting.
// CPA host doesn't currently forward RemoteAddr, so fall back to X-Forwarded-For
// / X-Real-IP headers if the deployment adds them via a reverse proxy.
func managementClientIP(req pluginapi.ManagementRequest) string {
	if xff := strings.TrimSpace(req.Headers.Get("X-Forwarded-For")); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return xff
	}
	if xr := strings.TrimSpace(req.Headers.Get("X-Real-Ip")); xr != "" {
		return xr
	}
	return ""
}

// mutatingManagementPath reports whether the path performs a write (checkin,
// import, trial claim, select, refresh, config toggle). Read endpoints pass.
func mutatingManagementPath(path string) bool {
	base := loadedManagementBasePath() + "/plugins/" + providerName
	switch path {
	case base + "/refresh",
		base + "/checkin",
		base + "/checkin/config",
		base + "/import",
		base + "/trial",
		base + "/select",
		base + "/keepalive",
		base + "/tasks/accept",
		base + "/tasks/accept_all",
		base + "/tasks/claim",
		base + "/tasks/light",
		base + "/tasks/travel":
		return true
	}
	return false
}

func mgmtJSONResponse(status int, v any) pluginapi.ManagementResponse {
	body, _ := json.Marshal(v)
	h := http.Header{}
	h.Set("Content-Type", "application/json; charset=utf-8")
	return pluginapi.ManagementResponse{StatusCode: status, Headers: h, Body: body}
}

func mgmtHTMLResponse(body []byte) pluginapi.ManagementResponse {
	return mgmtAssetResponse("text/html; charset=utf-8", body)
}

// mgmtAssetResponse returns one panel asset with an explicit content type.
func mgmtAssetResponse(contentType string, body []byte) pluginapi.ManagementResponse {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: h, Body: body}
}

// checkinLocks serializes per-account manual check-in (B4).
// Entries are pruned during dashboard prune to avoid unbounded growth
// when auth accounts are deleted/rotated.
var (
	checkinLocks sync.Map // auth_index -> *sync.Mutex
)
