// credits_handler.go implements the management API endpoints that mutate or
// read account state: import credential, toggle check-in, claim trial, select
// active auth, and query credits for one account or all.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// handleImportAuth accepts nested or flat credential JSON and persists via host.auth.save.
func handleImportAuth(req pluginapi.ManagementRequest) map[string]any {
	var body struct {
		JSON json.RawMessage `json:"json"`
		Raw  string          `json:"raw"`
	}
	_ = json.Unmarshal(req.Body, &body)
	raw := []byte(strings.TrimSpace(body.Raw))
	if len(body.JSON) > 0 {
		raw = body.JSON
	}
	if len(raw) == 0 {
		return map[string]any{"success": false, "error": "missing json/raw credential payload"}
	}
	// Foreign-credential guard: parseStored only requires an accessToken, so
	// a qoder/trae auth file parses fine here and would be saved as a
	// workbuddy account that can never authenticate (its token belongs to a
	// different upstream realm — every call ends in the upstream's APISIX 401
	// HTML page, surfaced as "parse failed: invalid character '<'"). Reject
	// payloads that explicitly declare another plugin and tell the user where
	// the credential belongs. Untyped flat exports (legacy CPA-Manager-Plus
	// files) keep working — filename/type conventions did not always exist.
	var typed struct {
		Type     string `json:"type"`
		Provider string `json:"provider"`
	}
	if json.Unmarshal(raw, &typed) == nil {
		t := strings.ToLower(strings.TrimSpace(typed.Type))
		if t == "" {
			t = strings.ToLower(strings.TrimSpace(typed.Provider))
		}
		switch t {
		case "", "workbuddy", "workbuddy-cn", "workbuddy-global", "workbuddy-intl",
			"codebuddy", "codebuddy-cn", "codebuddy-intl":
			// this plugin's own family (or untyped) — accept below
		default:
			return map[string]any{"success": false, "error": fmt.Sprintf(
				"credential type %q belongs to another plugin — use that plugin's own login/import instead", t)}
		}
	}
	sa, err := parseStored(raw)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	// Persist nested storage + top-level type/note/logo/disabled for Auth page.
	fileJSON, err := buildAuthFileJSON(sa, false, displayNote(sa, nil, false), nil)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	auth := toAuthData(sa)
	saveReq := pluginapi.HostAuthSaveRequest{
		Name: auth.FileName,
		JSON: fileJSON,
	}
	saveBody, _ := json.Marshal(saveReq)
	rawResp, err := hostCall(pluginabi.MethodHostAuthSave, saveBody)
	if err != nil {
		return map[string]any{"success": false, "error": "host.auth.save: " + err.Error()}
	}
	var env envelope
	if err := json.Unmarshal(rawResp, &env); err != nil || !env.OK {
		msg := "host.auth.save failed"
		if env.Error != nil && env.Error.Message != "" {
			msg = env.Error.Message
		}
		return map[string]any{"success": false, "error": msg}
	}
	var saveResp pluginapi.HostAuthSaveResponse
	_ = json.Unmarshal(env.Result, &saveResp)
	// Do not remove a legacy record after import. CPA owns auth-record cleanup;
	// importing the canonical record must not silently destroy workbuddy.json.
	return map[string]any{
		"success":  true,
		"name":     saveResp.Name,
		"path":     saveResp.Path,
		"uid":      sa.Account.UID,
		"nickname": sa.Account.Nickname,
		"file":     auth.FileName,
	}
}

func handleCheckinConfig(req pluginapi.ManagementRequest) map[string]any {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	_ = json.Unmarshal(req.Body, &body)
	checkinAutoMu.Lock()
	if body.Enabled != nil {
		// Runtime-only toggle: the CPA host exposes no plugin-config write
		// callback, so persisting would mean editing the host's config.yaml
		// from inside the plugin (fragile under docker volume mounts). The
		// value from config_yaml wins again on CPA restart.
		checkinAuto = *body.Enabled
	}
	cur := checkinAuto
	checkinAutoMu.Unlock()
	return map[string]any{"checkin_auto": cur, "persistent": false}
}

// handleClaimTrial claims the expert trial pack for an eligible WorkBuddy-service
// account. CN and CodeBuddy Intl accounts are rejected by service identity.
func handleClaimTrial(req pluginapi.ManagementRequest) map[string]any {
	var body struct {
		AuthIndex string `json:"auth_index"`
	}
	_ = json.Unmarshal(req.Body, &body)
	authIndex := strings.TrimSpace(body.AuthIndex)
	if authIndex == "" {
		return map[string]any{"error": "auth_index is required"}
	}
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	for _, f := range files {
		if f.AuthIndex != authIndex {
			continue
		}
		sa, err := hostAuthGet(f.AuthIndex)
		if err != nil {
			return map[string]any{"auth_index": authIndex, "error": err.Error()}
		}
		if !isWorkBuddyService(sa) {
			return map[string]any{"auth_index": authIndex, "error": "专家加油包仅适用于国际版账号"}
		}
		res, err := performTrialCall(sa)
		out := map[string]any{"auth_index": authIndex, "nickname": sa.Account.Nickname}
		if err != nil {
			out["error"] = err.Error()
		} else {
			for k, v := range res {
				out[k] = v
			}
		}
		// Invalidate credits cache (copy entry, set credits=nil, keep plan/checkin).
		if v, ok := accountCache.Load(f.ID); ok {
			if e, ok2 := v.(*accountCacheEntry); ok2 {
				fresh := *e
				fresh.credits = nil
				fresh.fetched = time.Now()
				accountCache.Store(f.ID, &fresh)
			}
		}
		if lifecycleEnabled() {
			_, _ = reconcileOneAccount(authIndex, f.ID, true)
		}
		return out
	}
	return map[string]any{"error": "account not found"}
}

// handleSelectAuth sets the panel-selected account used for chat routing.
// Service routing identity is read from the stored credential on each request; the panel displays only CN/Intl.
func handleSelectAuth(req pluginapi.ManagementRequest) map[string]any {
	var body struct {
		AuthIndex string `json:"auth_index"`
	}
	_ = json.Unmarshal(req.Body, &body)
	authIndex := strings.TrimSpace(body.AuthIndex)
	if authIndex == "" {
		return map[string]any{"error": "auth_index is required", "active_auth": getActiveAuthID()}
	}
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	for _, f := range files {
		if f.AuthIndex != authIndex {
			continue
		}
		if f.Disabled {
			return map[string]any{"error": "账号已禁用，无法选中", "auth_index": authIndex}
		}
		sa, err := hostAuthGet(f.AuthIndex)
		if err != nil {
			return map[string]any{"error": err.Error(), "auth_index": authIndex}
		}
		setActiveAuthID(f.ID)
		return map[string]any{
			"ok":          true,
			"active_auth": f.ID,
			"region":      panelRegion(sa),
			"nickname":    sa.Account.Nickname,
			"uid":         sa.Account.UID,
		}
	}
	return map[string]any{"error": "account not found", "auth_index": authIndex}
}

// handleCreditsQuery returns real-time credits for one or all accounts.
// Pass ?auth_index=<idx> to query a single account; omit for all.
// Single-account mode returns full account info (nickname, region, credits,
// exhausted, trial_claimed) so the panel can update one card without
// reloading the entire dashboard.
// refreshCreditsRow reads one account's business snapshot. Unlike /refresh,
// it never reconciles lifecycle, selects routing, or saves an auth record.
func refreshCreditsRow(f pluginapi.HostAuthFileEntry) map[string]any {
	acct := map[string]any{"auth_index": f.AuthIndex}
	sa, err := hostAuthGet(f.AuthIndex)
	if err != nil {
		acct["error"] = "credential read failed"
		return acct
	}
	acct["service"] = accountServiceRegion(sa)
	acct["nickname"] = sa.Account.Nickname
	acct["uid"] = sa.Account.UID
	acct["region"] = panelRegion(sa)
	acct["name"] = f.Name
	acct["label"] = f.Label
	acct["disabled"] = f.Disabled
	acct["selected"] = getActiveAuthID() == f.ID
	cr, err := fetchUserResource(sa)
	if err != nil {
		acct["error"] = safeManagementError(err)
		acct["data_error"] = acct["error"]
		return acct
	}
	now := time.Now()
	if cr != nil {
		cr.FetchedAt = now.UTC().Format(time.RFC3339)
	}
	acct["credits"] = cr
	acct["plan"] = fetchPaymentType(sa)
	if isWorkBuddyService(sa) {
		acct["trial_claimed"] = hasTrialPack(cr)
	}
	acct["exhausted"] = isCreditsExhausted(cr)
	acct["data_error"] = ""
	acct["error"] = ""
	// Copy the existing cache entry: no shared snapshot is mutated in place.
	next := accountCacheEntry{credits: cr, fetched: now}
	if v, ok := accountCache.Load(f.ID); ok {
		if previous, ok := v.(*accountCacheEntry); ok {
			next.checkin = previous.checkin
			next.plan = previous.plan
		}
	}
	next.plan, _ = acct["plan"].(string)
	accountCache.Store(f.ID, &next)
	return acct
}
func handleCreditsQuery(req pluginapi.ManagementRequest) map[string]any {
	id := strings.TrimSpace(queryParam(req, "auth_index"))
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": "cannot list CPA credentials"}
	}
	selected := make([]pluginapi.HostAuthFileEntry, 0, len(files))
	for _, f := range files {
		if id == "" || id == f.AuthIndex {
			selected = append(selected, f)
		}
	}
	if id != "" && len(selected) == 0 {
		return map[string]any{"error": "account not found"}
	}
	out := make([]map[string]any, len(selected))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, f := range selected {
		slots <- struct{}{}
		wg.Add(1)
		go func(i int, f pluginapi.HostAuthFileEntry) {
			defer wg.Done()
			defer func() { <-slots }()
			out[i] = refreshCreditsRow(f)
		}(i, f)
	}
	wg.Wait()
	return map[string]any{"accounts": out, "server_time_iso": time.Now().UTC().Format(time.RFC3339)}
}
