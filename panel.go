// panel.go serves the management dashboard: the aggregated account list the
// web UI consumes (buildDashboardEx) and the embedded HTML page itself.
package main

import (
	_ "embed"
	"html"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// wbAccount is one row of the dashboard.
type wbAccount struct {
	AuthIndex     string            `json:"auth_index"`
	AuthID        string            `json:"auth_id,omitempty"`
	Name          string            `json:"name"`
	Label         string            `json:"label"`
	Nickname      string            `json:"nickname"`
	UID           string            `json:"uid"`
	Region        string            `json:"region"` // "cn" | "intl"
	TrialEligible bool              `json:"trial_eligible,omitempty"`
	Plan          string            `json:"plan"`
	Status        string            `json:"status"`
	Disabled      bool              `json:"disabled"`
	Exhausted     bool              `json:"exhausted"`
	Selected      bool              `json:"selected"` // panel active routing card
	Credits       *creditsSummary   `json:"credits,omitempty"`
	Checkin       *checkinSummary   `json:"checkin,omitempty"`
	TrialClaimed  bool              `json:"trial_claimed,omitempty"` // Global: expert trial already claimed
	Models        *realmModelsState `json:"models,omitempty"`        // v0.9.9: where this realm's model list came from
	// DailyFree is today's per-model free-token usage. Deliberately separate
	// from Credits: credits are PURCHASED (account-wide, package cycles) while
	// this is the FREE tier (per model, resets daily). Merging them would put
	// two unrelated budgets behind one progress bar.
	DailyFree []modelDailyQuota `json:"daily_free,omitempty"`
	// Runtime is the HOST's authoritative view of this credential (cooldown
	// expiry, availability, success/failure counts). Present so the panel can
	// show what CPA will actually route on, instead of only the plugin's own
	// guess. nil when the host does not support host.auth.get_runtime.
	Runtime *wbAccountRuntime `json:"runtime,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// wbAccountRuntime is the panel-facing subset of the host runtime view.
type wbAccountRuntime struct {
	// Status is the host's status string ("active", "error", "disabled", ...).
	Status string `json:"status,omitempty"`
	// StatusMessage explains Status when the host provides a reason.
	StatusMessage string `json:"status_message,omitempty"`
	// Unavailable reports whether the host currently excludes this credential
	// from routing (cooldown, quota, manual disable).
	Unavailable bool `json:"unavailable,omitempty"`
	// CooldownSeconds is seconds until the host will consider this credential
	// usable again; 0 means no cooldown is active. Computed from the host's
	// NextRetryAfter so the panel does not have to parse timestamps.
	CooldownSeconds int64 `json:"cooldown_seconds,omitempty"`
	// CooldownUntil is the raw RFC3339 deadline for display.
	CooldownUntil string `json:"cooldown_until,omitempty"`
	Priority      int    `json:"priority,omitempty"`
	Success       int64  `json:"success,omitempty"`
	Failed        int64  `json:"failed,omitempty"`
	// Health is the HOST's per-credential time series (20 x 10 min).
	Health []healthBarBucket `json:"health,omitempty"`
	// Stale is true when the host last refreshed this credential a long time
	// ago; the panel surfaces it as a hint that counts may be outdated.
	Stale bool `json:"stale,omitempty"`
}

// healthBarBucket is one block of the per-credential health bar.
//
// The host owns this series: it already rings 20 x 10-minute success/failure
// buckets per credential (sdk/cliproxy/auth RecentRequestsSnapshot), records
// every routed request into them, and exposes them through
// host.auth.get_runtime. The plugin therefore DISPLAYS the host's numbers
// rather than maintaining its own — a plugin-side counter can only ever see
// the requests that reached this plugin's executor, while the host sees every
// routing outcome, including requests that never got here.
type healthBarBucket struct {
	Label   string `json:"label"`
	Success int64  `json:"success"`
	Failed  int64  `json:"failed"`
}

// hostAuthRuntimeFor adapts the host runtime view for the panel, or returns nil
// when it is unavailable. Never returns an error: this is read-only enrichment.
func hostAuthRuntimeFor(authIndex string) *wbAccountRuntime {
	rt, err := hostAuthGetRuntime(authIndex)
	if err != nil || rt == nil {
		return nil
	}
	out := &wbAccountRuntime{
		Status:        strings.TrimSpace(rt.Status),
		StatusMessage: strings.TrimSpace(rt.StatusMessage),
		Unavailable:   rt.Unavailable || rt.Disabled,
		Priority:      rt.Priority,
		Success:       rt.Success,
		Failed:        rt.Failed,
	}
	if !rt.NextRetryAfter.IsZero() {
		remaining := time.Until(rt.NextRetryAfter)
		if remaining > 0 {
			out.CooldownSeconds = int64(remaining.Seconds())
			out.CooldownUntil = rt.NextRetryAfter.Format(time.RFC3339)
			// A future deadline IS unavailability, even if the host has not
			// flagged the boolean yet (the two are written at different times).
			out.Unavailable = true
		}
	}
	if !rt.LastRefresh.IsZero() && time.Since(rt.LastRefresh) > 24*time.Hour {
		out.Stale = true
	}
	for _, b := range rt.RecentRequests {
		out.Health = append(out.Health, healthBarBucket{
			Label:   strings.TrimSpace(b.Time),
			Success: b.Success,
			Failed:  b.Failed,
		})
	}
	return out
}

// credits/checkin/plan fields are left empty — the panel renders skeletons
// and fetches them lazily via /credits?auth_index=<idx>. This avoids hitting
// upstream billing APIs for all accounts simultaneously on page load (which
// causes 500 from rate-limited /v2/billing/meter/get-user-resource).
func buildDashboardEx(force, fetchCredits bool) map[string]any {
	if pluginQuiescing() {
		return map[string]any{"error": errPluginQuiescing.Error()}
	}
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	// Prune cache entries for accounts that no longer exist (auth deleted via
	// CPA UI) or whose TTL expired long ago. Without this, accountCache grows
	// monotonically for the lifetime of the process.
	live := make(map[string]struct{}, len(files))
	for _, f := range files {
		live[f.ID] = struct{}{}
	}
	accountCache.Range(func(key, value any) bool {
		idx, _ := key.(string)
		if _, ok := live[idx]; !ok {
			accountCache.Delete(key)
			checkinLocks.Delete(key)
			lifecycleState.Delete(key)
			return true
		}
		if e, ok := value.(*accountCacheEntry); ok && time.Since(e.fetched) > 4*accountCacheTTL {
			accountCache.Delete(key)
		}
		return true
	})
	// Also prune stale lifecycle state and checkin locks for gone accounts.
	pruneLifecycleState()
	pruneCheckinLocks()
	// Request lifecycle counters ride the dashboard top level (they are
	// provider-wide, not per-account) so the panel can warn that traffic is
	// arriving but nothing can serve it.
	lifecycle := requestLifecycleSnapshot()
	out := make([]wbAccount, len(files))
	// Accounts are independent — fetch their dashboards concurrently. With 4
	// accounts this cuts cold-load latency from ~4×(3 serial upstream calls)
	// to roughly one slowest account.
	var wg sync.WaitGroup
	for i, f := range files {
		wg.Add(1)
		go func(i int, f pluginapi.HostAuthFileEntry) {
			defer wg.Done()
			acct := wbAccount{
				AuthIndex: f.AuthIndex,
				AuthID:    f.ID,
				Name:      f.Name,
				Label:     f.Label,
				Status:    f.Status,
				Disabled:  f.Disabled,
			}
			sa, phys, err := hostAuthGetBundle(f.AuthIndex)
			if err != nil {
				acct.Error = "load auth: " + err.Error()
				out[i] = acct
				return
			}
			// Physical file is source of truth for disabled (host list may lag).
			if phys != nil {
				acct.Disabled = phys.Disabled
				if phys.Name != "" {
					acct.Name = phys.Name
				}
			}
			acct.Nickname = sa.Account.Nickname
			acct.UID = sa.Account.UID
			acct.Region = panelRegion(sa)
			acct.TrialEligible = isWorkBuddyService(sa)
			acct.Models = realmModelStateFor(accountServiceRegion(sa))

			// Local, in-memory read: no upstream call, so it is safe to attach
			// on every dashboard build (unlike credits, which the panel fetches
			// lazily to avoid hammering the billing API).
			acct.DailyFree = dailyQuotaSnapshotForAuth(f.ID)
			// Host's authoritative runtime view (cooldown / availability /
			// success-failure counts). Enrichment only: nil when the host
			// predates the RPC, so the panel degrades to plugin-side state
			// rather than failing to render.
			acct.Runtime = hostAuthRuntimeFor(acct.AuthIndex)
			if fetchCredits {
				plan, ci, cr, errs := cachedAccountDetails(f.ID, sa, force)
				acct.Plan = plan
				acct.Checkin = ci
				acct.Credits = cr
				acct.Exhausted = isCreditsExhausted(cr)
				if isWorkBuddyService(sa) {
					acct.TrialClaimed = hasTrialPack(cr)
				}
				// Keep note in sync (throttled); do not block dashboard on save errors.
				_ = syncAuthNote(f.AuthIndex, f.ID, sa, cr, acct.Disabled)
				acct.Error = strings.Join(errs, "; ")
			} else {
				// Light load: use cached values if available, but don't fetch upstream.
				if v, ok := accountCache.Load(f.ID); ok {
					if e, ok2 := v.(*accountCacheEntry); ok2 {
						acct.Plan = e.plan
						acct.Checkin = e.checkin
						acct.Credits = e.credits
						acct.Exhausted = isCreditsExhausted(e.credits)
						if isWorkBuddyService(sa) {
							acct.TrialClaimed = hasTrialPack(e.credits)
						}
					}
				}
			}
			out[i] = acct
		}(i, f)
	}
	wg.Wait()
	// After refresh (force), run lifecycle so exhaust→disable/delete is immediate.
	var life []map[string]any
	if force && lifecycleEnabled() {
		life = reconcileAllAccounts(true)
		// Drop accounts deleted during reconcile (Global exhaust) and refresh
		// disabled/exhausted from disk/cache (host list may lag after save).
		if files2, err2 := hostAuthList(); err2 == nil {
			live := make(map[string]struct{}, len(files2))
			disabledBy := make(map[string]bool, len(files2))
			for _, f := range files2 {
				live[f.AuthIndex] = struct{}{}
				// Prefer host list Disabled after reconcile; avoids N extra host.auth.get.
				// Dashboard row load already used hostAuthGetBundle for physical truth.
				disabledBy[f.AuthIndex] = f.Disabled
			}
			filtered := out[:0]
			for _, a := range out {
				if _, ok := live[a.AuthIndex]; !ok {
					continue
				}
				if d, ok := disabledBy[a.AuthIndex]; ok {
					a.Disabled = d
				}
				// Credits may have been refreshed during reconcile — re-read cache.
				if v, ok := accountCache.Load(a.AuthID); ok {
					if e, ok2 := v.(*accountCacheEntry); ok2 {
						if e.credits != nil {
							a.Credits = e.credits
							a.Exhausted = isCreditsExhausted(e.credits)
						}
						if e.plan != "" {
							a.Plan = e.plan
						}
						if e.checkin != nil {
							a.Checkin = e.checkin
						}
					}
				}
				filtered = append(filtered, a)
			}
			out = filtered
		}
	}
	checkinAutoMu.RLock()
	auto := checkinAuto
	checkinAutoMu.RUnlock()
	// Ensure default selection for panel + scheduler (first usable card).
	activeID := ensureDefaultActiveAuth(out)
	// Aggregate credits for panel/API consumers (all accounts currently in out).
	sum := summarizeCredits(out)
	// Mark selected account in list for UI.
	for i := range out {
		out[i].Selected = out[i].AuthID == activeID
	}
	resp := map[string]any{
		"accounts":       out,
		"active_auth":    activeID,
		"checkin_auto":   auto,
		"lifecycle_auto": lifecycleEnabled(),
		"keepalive_auto": keepaliveEnabled(),
		"growth_auto":    growthAutoEnabled(),
		"travel_auto":    travelAutoEnabled(),
		"schedule":       []string{"09:00", "21:00"},
		"server_time":    time.Now().Format("2006-01-02 15:04:05"),
		"summary":        sum,
		// Provider-wide request terminal outcomes (succeeded/failed/rejected/
		// canceled). Distinct from per-account credits: this is the only place
		// the panel can see requests that arrived but were rejected before
		// reaching the executor.
		"requests": lifecycle,
	}
	if len(life) > 0 {
		resp["lifecycle"] = life
	}
	return resp
}

// summarizeCredits aggregates remain/used across dashboard accounts.
func summarizeCredits(accounts []wbAccount) map[string]any {
	var remain, used, size, cnRemain, cnUsed, cnSize, intlRemain, intlUsed, intlSize int64
	var known, disabledN, exhaustedN, packs int
	for _, a := range accounts {
		if a.Disabled {
			disabledN++
		}
		if a.Exhausted {
			exhaustedN++
		}
		if a.Credits == nil {
			continue
		}
		cr := a.Credits
		if cr.TotalRemain == 0 && cr.TotalUsed == 0 && cr.TotalSize == 0 && len(cr.Packages) == 0 {
			continue
		}
		known++
		remain += cr.TotalRemain
		used += cr.TotalUsed
		size += cr.TotalSize
		packs += cr.PackCount
		if a.Region == regionIntl {
			intlRemain += cr.TotalRemain
			intlUsed += cr.TotalUsed
			intlSize += cr.TotalSize
		} else {
			cnRemain += cr.TotalRemain
			cnUsed += cr.TotalUsed
			cnSize += cr.TotalSize
		}
	}
	total := remain + used
	if size > total {
		total = size
	}
	return map[string]any{
		"account_count":   len(accounts),
		"known_count":     known,
		"disabled_count":  disabledN,
		"exhausted_count": exhaustedN,
		"pack_count":      packs,
		"total_remain":    remain,
		"total_used":      used,
		"total_size":      size,
		"total":           total,
		"cn_remain":       cnRemain,
		"cn_used":         cnUsed,
		"cn_size":         cnSize,
		"intl_remain":     intlRemain,
		"intl_used":       intlUsed,
		"intl_size":       intlSize,
		"global_remain":   intlRemain,
		"global_used":     intlUsed,
		"global_size":     intlSize,
	}
}

// Web panel (markup + styles inline, logic in panel.js)
// -----------------------------------------------------------------------------

// panelAsset is one servable panel file: its content type and bytes.
type panelAsset struct {
	statusCode  int
	contentType string
	body        []byte
}

// servePanel resolves a resource sub-path to a panel asset. The HTML shell and
// its script are the only servable files; anything else 404s so the route
// cannot be used to probe the plugin's own file set.
func servePanel(sub string) panelAsset {
	if idx := strings.Index(sub, "?"); idx != -1 {
		sub = sub[:idx]
	}
	sub = strings.TrimSpace(sub)
	if sub == "" || sub == "/" || sub == "/panel" || sub == "/panel.html" || strings.HasSuffix(sub, "/panel") || strings.HasSuffix(sub, "/panel.html") {
		return panelAsset{contentType: "text/html; charset=utf-8", body: localizedPanelHTML()}
	}
	if sub == "/panel-i18n.js" || strings.HasSuffix(sub, "/panel-i18n.js") {
		return panelAsset{contentType: "application/javascript; charset=utf-8", body: panelI18N}
	}
	if sub == "/panel.js" || strings.HasSuffix(sub, "/panel.js") {
		return panelAsset{contentType: "application/javascript; charset=utf-8", body: panelJS}
	}
	return panelAsset{contentType: "text/html; charset=utf-8", body: []byte("<h1>404</h1>"), statusCode: 404}
}

//go:embed panel.html
var panelHTML []byte

//go:embed panel.js
var panelJS []byte

//go:embed panel-i18n.js
var panelI18N []byte

// Locale is browser-local. This only injects host-declared path context; never keys.
func localizedPanelHTML() []byte {
	page := strings.ReplaceAll(string(panelHTML), "__WB_MANAGEMENT_BASE__", html.EscapeString(loadedManagementBasePath()+"/plugins/"+providerName))
	page = strings.ReplaceAll(page, "__WB_RESOURCE_BASE__", html.EscapeString(loadedResourceBasePath()))
	return []byte(page)
}
