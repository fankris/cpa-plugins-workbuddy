// models.go implements the ModelProvider capability: static and per-auth
// model lists, realm-aware model discovery where supported, alias reverse
// resolution (client-facing alias → upstream model id), and the host-config
// oauth-excluded-models filter.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// wbModels is the CN realm static catalog (copilot.tencent.com), mirroring
// Tencent's official CN built-in model table. v0.12.19: this list is NO
// LONGER the fallback for Intl/Global credentials — see staticModelsForRealm.
// Sharing it across realms is exactly what made Intl credentials advertise
// deepseek-v4-flash and die with upstream 11102 "service info not found".
func wbModels() []pluginapi.ModelInfo {
	// Desktop-aligned CN list per workbuddy2api-hub's measured CN_UI_ORDER
	// (2026-09), with that catalog's context/output specs. Dynamic discovery
	// is the primary source; this covers the discovery-failure fallback and
	// the 11102 error hint. Legacy entries stay at the tail so credentials
	// already routing to them keep working; pin more via models_cn.
	return []pluginapi.ModelInfo{
		{ID: "hy4-preview-f", Name: "Hy4 Preview F", ContextLength: 1000000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "hy3", Name: "Hy3", ContextLength: 192000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		// DeepSeek V4.1 Flash: released 2026-09-10, official launch partner
		// WorkBuddy/CodeBuddy (deepseek.com news260910). 552B-backbone MoE,
		// 1M context; the only model with a known daily free allowance.
		{ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", ContextLength: 1000000, MaxCompletionTokens: 128000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "glm-5.3", Name: "GLM-5.3", ContextLength: 1000000, MaxCompletionTokens: 48000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "glm-5.3-flash", Name: "GLM-5.3 Flash", ContextLength: 1000000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "glm-5.2", Name: "GLM-5.2", ContextLength: 1000000, MaxCompletionTokens: 48000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "glm-5.1", Name: "GLM-5.1", ContextLength: 200000, MaxCompletionTokens: 48000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "glm-5v-turbo", Name: "GLM-5V Turbo", ContextLength: 200000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "minimax-m3", Name: "MiniMax M3", ContextLength: 512000, MaxCompletionTokens: 128000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kimi-k3-1", Name: "Kimi K3.1", ContextLength: 1000000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kimi-k2.8-preview", Name: "Kimi K2.8 Preview", ContextLength: 1000000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kimi-k2.7", Name: "Kimi K2.7", ContextLength: 256000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kimi-k2.6", Name: "Kimi K2.6", ContextLength: 256000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", ContextLength: 1000000, MaxCompletionTokens: 50000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		// Legacy tail: superseded or alias IDs that remain real upstream
		// entries. Not part of the desktop-aligned list above.
		{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash (legacy)", ContextLength: 1000000, MaxCompletionTokens: 50000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "hy3-preview", Name: "Hy3 Preview (legacy)", ContextLength: 192000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "hy3-preview-agent", Name: "Hy3 Preview Agent (legacy)", ContextLength: 192000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "hy4-preview", Name: "Hy4 Preview (legacy)", ContextLength: 1000000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
	}
}

// Realm static catalogs are the conservative fallback shown when discovery is
// unavailable. CN and Global/Intl intentionally keep separate catalogs so a
// credential is not advertised models from another realm. The Global/Intl
// list is workbuddy2api-hub's measured desktop catalog for the international
// gateway (2026-09): www.workbuddy.ai and www.codebuddy.ai resolve to the
// same address, so the measured list covers both realms. Legacy entries stay
// at the tail; additional models are configured via the custom models array.
func staticModelsGlobal() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{
		{ID: "hy4-preview-f", Name: "Hy4 Preview F", ContextLength: 1000000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "hy3", Name: "Hy3", ContextLength: 192000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", ContextLength: 1000000, MaxCompletionTokens: 128000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gpt-6-astra", Name: "GPT-6 Astra", ContextLength: 1000000, MaxCompletionTokens: 128000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", ContextLength: 1000000, MaxCompletionTokens: 128000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", ContextLength: 1000000, MaxCompletionTokens: 128000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", ContextLength: 1000000, MaxCompletionTokens: 128000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gpt-5.5", Name: "GPT-5.5", ContextLength: 1000000, MaxCompletionTokens: 128000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gpt-5.4", Name: "GPT-5.4", ContextLength: 272000, MaxCompletionTokens: 72000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gemini-3.5-flash", Name: "Gemini 3.5 Flash", ContextLength: 1000000, MaxCompletionTokens: 65536, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "glm-5.3-flash", Name: "GLM-5.3 Flash", ContextLength: 1000000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "glm-5.3", Name: "GLM-5.3", ContextLength: 1000000, MaxCompletionTokens: 48000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "glm-5.2", Name: "GLM-5.2", ContextLength: 1000000, MaxCompletionTokens: 48000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kimi-k3", Name: "Kimi K3", ContextLength: 1000000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kimi-k2.6", Name: "Kimi K2.6", ContextLength: 256000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kimi-k2.8-preview", Name: "Kimi K2.8 Preview", ContextLength: 1000000, MaxCompletionTokens: 32000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		// Legacy tail: the pre-1.0.32 fallback entry, kept for credentials
		// already routing to it.
		{ID: "hy4-preview", Name: "Hy4 Preview (legacy)", ContextLength: 1000000, MaxCompletionTokens: 64000, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
	}
}

// staticModelsIntl is the codebuddy.ai (Intl) fallback catalog. See
// staticModelsGlobal for the inclusion policy.
func staticModelsIntl() []pluginapi.ModelInfo {
	return staticModelsGlobal()
}

// customStaticModel describes a user-supplied static model entry. IDs must be
// real upstream IDs; aliases remain CPA host configuration.
type customStaticModel struct {
	ID        string
	Name      string
	Channel   string
	Context   int64
	MaxTokens int64
	Enabled   bool
}

var customStaticModels = struct {
	sync.RWMutex
	models []customStaticModel
}{}

func customModelsForRealm(realm string) []pluginapi.ModelInfo {
	customStaticModels.RLock()
	defer customStaticModels.RUnlock()
	seen := map[string]struct{}{}
	var out []pluginapi.ModelInfo
	for _, model := range customStaticModels.models {
		if !model.Enabled || !customModelChannelMatches(model.Channel, realm) {
			continue
		}
		key := strings.ToLower(model.ID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		name := model.Name
		if name == "" {
			name = model.ID
		}
		out = append(out, pluginapi.ModelInfo{
			ID: model.ID, Name: name, ContextLength: model.Context,
			MaxCompletionTokens: model.MaxTokens, OwnedBy: providerName,
			SupportedGenerationMethods: []string{"chat"}, UserDefined: true,
		})
	}
	return out
}

func customModelChannelMatches(channel, realm string) bool {
	switch channel {
	case "all":
		return true
	case "cn":
		return realm == regionCN
	case "intl_global", "global", "intl":
		return realm == regionIntl || realm == regionGlobal
	default:
		return false
	}
}

func appendCustomModels(realm string, models []pluginapi.ModelInfo) []pluginapi.ModelInfo {
	custom := customModelsForRealm(realm)
	if len(custom) == 0 {
		return models
	}
	out := append([]pluginapi.ModelInfo(nil), models...)
	seen := make(map[string]struct{}, len(out)+len(custom))
	for _, model := range out {
		seen[strings.ToLower(model.ID)] = struct{}{}
	}
	for _, model := range custom {
		if _, ok := seen[strings.ToLower(model.ID)]; ok {
			continue
		}
		seen[strings.ToLower(model.ID)] = struct{}{}
		out = append(out, model)
	}
	return out
}

func modelSourceWithCustom(realm, source string, base []pluginapi.ModelInfo) string {
	seen := make(map[string]struct{}, len(base))
	for _, model := range base {
		seen[strings.ToLower(model.ID)] = struct{}{}
	}
	for _, model := range customModelsForRealm(realm) {
		if _, ok := seen[strings.ToLower(model.ID)]; !ok {
			return source + " + custom"
		}
	}
	return source
}

func builtinStaticModelsForRealm(realm string) []pluginapi.ModelInfo {
	switch strings.ToLower(strings.TrimSpace(realm)) {
	case regionIntl, regionGlobal:
		return staticModelsIntl()
	default:
		return wbModels()
	}
}

// staticModelsForRealm dispatches a realm key to its static catalog. The
// legacy default stays the CN list for unknown realms.
func staticModelsForRealm(realm string) []pluginapi.ModelInfo {
	return appendCustomModels(realm, builtinStaticModelsForRealm(realm))
}

// pinnedModelsForRealm returns the ModelInfo list pinned via config_yaml
// models_cn / models_global / models_intl for this realm, or nil. Pinned
// lists are authoritative: they replace discovery for that realm entirely,
// so the credential output is exactly the user-written "supported models"
// list (the v0.12.19 formalization of writing supported models into the
// credential output).
func pinnedModelsForRealm(realm string) []pluginapi.ModelInfo {
	ids := pinnedModelIDsForRealm(realm)
	if len(ids) == 0 {
		return nil
	}
	return buildModelInfosForRealm(ids, realm)
}

// buildModelInfos maps raw upstream model IDs to ModelInfo, reusing the
// built-in and matching custom static metadata where available. Unknown IDs
// still get advertised because the user pinned them deliberately.
func buildModelInfos(ids []string) []pluginapi.ModelInfo {
	return buildModelInfosForRealm(ids, "")
}

func buildModelInfosForRealm(ids []string, realm string) []pluginapi.ModelInfo {
	meta := map[string]pluginapi.ModelInfo{}
	for _, m := range wbModels() {
		meta[strings.ToLower(m.ID)] = m
	}
	for _, m := range staticModelsGlobal() {
		meta[strings.ToLower(m.ID)] = m
	}
	// Custom entries are metadata overrides only for IDs that are not already
	// part of a built-in catalog; realm filtering keeps same-ID entries isolated.
	for _, m := range customModelsForRealm(realm) {
		key := strings.ToLower(m.ID)
		if _, exists := meta[key]; !exists {
			meta[key] = m
		}
	}
	out := make([]pluginapi.ModelInfo, 0, len(ids))
	for _, id := range ids {
		if m, ok := meta[strings.ToLower(id)]; ok {
			out = append(out, m)
			continue
		}
		out = append(out, pluginapi.ModelInfo{ID: id, Name: id, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}})
	}
	return out
}

// parsePinnedModelList decodes a models_* config value: a comma-separated
// upstream model ID list, optionally quoted or YAML flow-style ([a, b]).
// Returns trimmed, de-duplicated (case-insensitive), non-empty IDs in order.
func parsePinnedModelList(raw string) []string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "\"'")
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = raw[1 : len(raw)-1]
	}
	seen := map[string]struct{}{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	return out
}

// discoverModelsFn is the seam for upstream realm discovery; tests swap it
// out to stay off the network.
// Test-only discovery seam. Production preserves the request context below.
var discoverModelsFn func(accessToken, realm string) ([]pluginapi.ModelInfo, error)

// fetchDynamicModelsFromStorage resolves ONE credential's advertised model
// list, in priority order:
//  1. the realm's pinned config (models_cn / models_global / models_intl) —
//     the user-written "supported models" list, which also skips discovery;
//  2. a previously fetched dynamic cache for CN/Global;
//  3. dynamic discovery for CN/Global;
//  4. the realm's static catalog.
//
// Intl is deliberately static by default. The historical Intl catalog route
// (including the experimental Global-catalog probe) returns gateway 500 for
// valid Intl credentials, so it must not add latency or advertise unverified
// models. Users may still explicitly pin verified IDs with models_intl.
func modelCacheKey(realm string, storageJSON []byte, accessToken string) string {
	var probe struct {
		Auth struct {
			UID          string `json:"uid"`
			EnterpriseID string `json:"enterpriseId"`
		} `json:"auth"`
		Account struct {
			UID          string `json:"uid"`
			EnterpriseID string `json:"enterpriseId"`
		} `json:"account"`
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
	}
	_ = json.Unmarshal(storageJSON, &probe)
	identity := strings.TrimSpace(probe.Auth.UID)
	if identity == "" {
		identity = strings.TrimSpace(probe.Account.UID)
	}
	if identity == "" {
		identity = strings.TrimSpace(probe.UID)
	}
	if identity == "" {
		identity = strings.TrimSpace(probe.Auth.EnterpriseID)
	}
	if identity == "" {
		identity = strings.TrimSpace(probe.Account.EnterpriseID)
	}
	if identity == "" {
		identity = strings.TrimSpace(probe.EnterpriseID)
	}
	if identity == "" {
		sum := sha256.Sum256([]byte(accessToken))
		identity = hex.EncodeToString(sum[:])
	}
	return strings.ToLower(strings.TrimSpace(realm)) + ":" + identity
}

func fetchDynamicModelsFromStorage(storageJSON []byte) []pluginapi.ModelInfo {
	return resolveCredentialModels(pluginContext(), storageJSON, false).Models
}

// realmModelsState is the dashboard-facing snapshot of one realm's model
// source — the answer to "why does this realm list these models".
type realmModelsState struct {
	Source     string `json:"source"`
	Count      int    `json:"count"`
	FetchedAt  string `json:"fetched_at,omitempty"`
	AgeSeconds int64  `json:"age_seconds,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	LastErrorA string `json:"last_error_at,omitempty"`
}

// noteRealmSource records that realm's advertised list came from a non-
// discovery source (config pin or static fallback). Any previous discovery
// result/error is cleared so the dashboard does not report stale state after
// a reconfigure or an expired cache falls back to a static catalog.
func noteRealmSource(realm, source string, count int) {
	realm = strings.ToLower(strings.TrimSpace(realm))
	dynamicModelsCache.Lock()
	defer dynamicModelsCache.Unlock()
	entry := dynamicModelsCache.realms[realm]
	entry.models = nil
	entry.fetched = time.Time{}
	entry.source = source
	entry.srcCount = count
	entry.lastErr = ""
	entry.lastErrAt = time.Time{}
	dynamicModelsCache.realms[realm] = entry
}

// noteRealmError records a discovery failure for the realm and logs it with a
// per-realm throttle: immediately on a NEW message, otherwise at most once a
// minute (model.for_auth can fire per models query, and silent failure is
// exactly what made thin/stale model lists undiagnosable).
func noteRealmError(realm, msg string) {
	realm = strings.ToLower(strings.TrimSpace(realm))
	now := time.Now()
	dynamicModelsCache.Lock()
	entry := dynamicModelsCache.realms[realm]
	base := builtinStaticModelsForRealm(realm)
	fallback := appendCustomModels(realm, base)
	entry.models = nil
	entry.source = modelSourceWithCustom(realm, "static (discovery failed)", base)
	entry.srcCount = len(fallback)
	entry.lastErr = msg
	entry.lastErrAt = now
	shouldLog := entry.lastLogAt.IsZero() || now.Sub(entry.lastLogAt) >= time.Minute
	if shouldLog {
		entry.lastLogAt = now
	}
	dynamicModelsCache.realms[realm] = entry
	dynamicModelsCache.Unlock()
	if shouldLog {
		source := modelSourceWithCustom(realm, "static (discovery failed)", base)
		log.Printf("models: realm=%s discovery failed (%s) — serving %s (%d model(s)) until next successful discovery", realm, msg, source, len(fallback))
	}
}

// realmModelStateFor snapshots the realm's diagnostics for the dashboard.
// Returns nil when the realm has never been resolved (host has not queried
// models for it yet).
func realmModelStateFor(realm string) *realmModelsState {
	realm = strings.ToLower(strings.TrimSpace(realm))
	dynamicModelsCache.RLock()
	entry, ok := dynamicModelsCache.realms[realm]
	if !ok {
		dynamicModelsCache.RUnlock()
		return nil
	}
	st := &realmModelsState{
		Source:    entry.source,
		Count:     len(entry.models),
		LastError: entry.lastErr,
	}
	if st.Count == 0 {
		st.Count = entry.srcCount
	}
	if !entry.fetched.IsZero() {
		st.FetchedAt = entry.fetched.Format("2006-01-02 15:04:05")
		st.AgeSeconds = int64(time.Since(entry.fetched).Seconds())
	}
	if !entry.lastErrAt.IsZero() {
		st.LastErrorA = entry.lastErrAt.Format("2006-01-02 15:04:05")
	}
	dynamicModelsCache.RUnlock()
	if st.Source == "" && st.LastError == "" {
		return nil
	}
	return st
}

// cachedDynamicModels returns one credential-scoped cache entry. Keys used by
// model-for-auth are realm plus stable account identity/token hash; callers get
// a copy so filtering cannot mutate the shared entry.
func cachedDynamicModels(key string) ([]pluginapi.ModelInfo, bool) {
	dynamicModelsCache.RLock()
	defer dynamicModelsCache.RUnlock()
	entry, ok := dynamicModelsCache.realms[key]
	if !ok || len(entry.models) == 0 || time.Since(entry.fetched) >= dynamicModelsCacheTTL {
		return nil, false
	}
	return append([]pluginapi.ModelInfo(nil), entry.models...), true
}

// invalidateDynamicModelsForRealm drops the cached discovery result for a realm
// so the next resolve performs a fresh upstream read.
//
// Needed by the panel's manual model refresh: without it the resolver serves
// the 5-minute cache and a refresh button would appear to do nothing, which
// reads as "the button is broken" rather than "the list is already current".
func invalidateDynamicModelsForRealm(realm string) {
	realm = strings.ToLower(strings.TrimSpace(realm))
	if realm == "" {
		return
	}
	prefix := realm + ":"
	dynamicModelsCache.Lock()
	for key, entry := range dynamicModelsCache.realms {
		if key != realm && !strings.HasPrefix(key, prefix) {
			continue
		}
		entry.fetched = time.Time{}
		dynamicModelsCache.realms[key] = entry
	}
	dynamicModelsCache.Unlock()
}

func cachedDynamicModelsForRealm(realm string) ([]pluginapi.ModelInfo, bool) {
	dynamicModelsCache.RLock()
	defer dynamicModelsCache.RUnlock()
	prefix := strings.ToLower(strings.TrimSpace(realm)) + ":"
	var newest realmModelsEntry
	found := false
	for key, entry := range dynamicModelsCache.realms {
		if key != strings.TrimSuffix(prefix, ":") && !strings.HasPrefix(key, prefix) || len(entry.models) == 0 || time.Since(entry.fetched) >= dynamicModelsCacheTTL {
			continue
		}
		if !found || entry.fetched.After(newest.fetched) {
			newest = entry
			found = true
		}
	}
	if !found {
		return nil, false
	}
	return append([]pluginapi.ModelInfo(nil), newest.models...), true
}

func storeDynamicModels(key string, models []pluginapi.ModelInfo, details ...map[string]modelDetails) {
	var meta map[string]modelDetails
	if len(details) > 0 {
		meta = details[0]
	}
	copyModels := append([]pluginapi.ModelInfo(nil), models...)
	now := time.Now()
	dynamicModelsCache.Lock()
	dynamicModelsCache.realms[key] = realmModelsEntry{models: copyModels, details: meta, fetched: now, source: "discovery"}
	// Keep a realm-level diagnostic snapshot for the panel and legacy tests, but
	// never use this aggregate entry as a model-for-auth cache hit.
	if i := strings.IndexByte(key, ':'); i > 0 {
		realm := key[:i]
		dynamicModelsCache.realms[realm] = realmModelsEntry{models: append([]pluginapi.ModelInfo(nil), copyModels...), fetched: now, source: "discovery"}
	}
	dynamicModelsCache.Unlock()
}

// realmForStorage classifies an auth storage blob into the public CN/Intl
// grouping. Model discovery uses serviceRealmForStorage to choose the WB
// default entrypoint for either login brand. Inputs may be nested OAuth files, flat CPA imports, or legacy files
// whose JWT issuer is the only remaining realm signal.
func realmForStorage(raw []byte, accessToken string) string {
	return displayRegionForService(serviceRealmForStorage(raw, accessToken))
}

func serviceRealmForStorage(raw []byte, accessToken string) string {
	var probe struct {
		Auth struct {
			Domain string `json:"domain"`
			Region string `json:"region"`
			Realm  string `json:"realm"`
		} `json:"auth"`
		Domain string `json:"domain"`
		Region string `json:"region"`
		Realm  string `json:"realm"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil {
		if service := realmFromRegionDomain(firstNonEmptyTrimmed(probe.Auth.Region, probe.Auth.Realm), probe.Auth.Domain); service != "" {
			return wbServiceForRegion(service)
		}
		if service := realmFromRegionDomain(firstNonEmptyTrimmed(probe.Region, probe.Realm), probe.Domain); service != "" {
			return wbServiceForRegion(service)
		}
	}
	if isGlobalToken(accessToken) {
		return regionGlobal
	}
	return regionCN
}

// realmFromRegionDomain maps one (region, domain) pair to a realm key, or ""
// when neither field identifies a realm (empty/legacy files).
func realmFromRegionDomain(region, domain string) string {
	if isGlobalDomain(domain) {
		return regionGlobal
	}
	if isIntlDomain(domain) {
		return regionIntl
	}
	if isCNDomain(domain) {
		return regionCN
	}
	switch strings.ToLower(strings.TrimSpace(region)) {
	case regionGlobal:
		return regionGlobal
	case regionIntl:
		return regionIntl
	case regionCN:
		return regionCN
	default:
		return ""
	}
}

// fetchDynamicModels calls the WorkBuddy API to get the latest model list.
// Falls back to the hardcoded list on any error.
// extractAccessToken handles both flat (CPA UI) and nested (plugin OAuth) auth file shapes.
func extractAccessToken(raw []byte) (string, bool) {
	// flat shape from CPA-Manager-Plus UI
	var flat struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(raw, &flat); err == nil && strings.TrimSpace(flat.AccessToken) != "" {
		return flat.AccessToken, true
	}
	// nested shape from plugin OAuth
	var nested storedAuth
	if err := json.Unmarshal(raw, &nested); err == nil && strings.TrimSpace(nested.Auth.AccessToken) != "" {
		return nested.Auth.AccessToken, true
	}
	return "", false
}

// realmFromToken decodes the JWT iss claim to determine the account realm.
// Global tokens have iss=...workbuddy.ai...; CN tokens have iss=...codebuddy.cn...
// Returns true if the token is Global.
func isGlobalToken(accessToken string) bool {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return false
	}
	payload := parts[1]
	// base64url padding
	if pad := len(payload) % 4; pad != 0 {
		payload += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	var claims struct {
		ISS string `json:"iss"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return false
	}
	return isGlobalDomain(claims.ISS) || isIntlDomain(claims.ISS)
}

// modelsEndpointFor returns the service-specific model URL and Origin/Referer.
// Both international legacy keys resolve to the default WB entrypoint.
func modelsEndpointFor(realm string) (modelsURL, origin string) {
	switch strings.ToLower(strings.TrimSpace(realm)) {
	case regionGlobal, regionIntl:
		return upstreamBaseGlobal + "/console/enterprises/personal/models", originRefererGlobal
	default:
		return endpointModels, originReferer
	}
}

// callModelsAPI GETs the console model directory for CN and legacy
// international credentials through the WB entrypoint, irrespective of login brand. The request carries a per-request 15s budget
// through the CPA host HTTP bridge.
//
// v0.9.32: this is a thin retry wrapper around callModelsAPIOnce. The gateway
// answers model-directory requests with 429 (and, under load, with a
// contextWindow=0 payload that callModelsAPIOnce reclassifies as 429), which
// used to fail discovery outright for the whole 5-minute cache window. Retries
// honour Retry-After via the shared typed-error helpers so the backoff matches
// the billing path instead of inventing a second policy.
func callModelsAPI(accessToken string, realm ...string) ([]pluginapi.ModelInfo, error) {
	return callModelsAPIContext(pluginContext(), accessToken, realm...)
}

func callModelsAPIContext(parent context.Context, accessToken string, realm ...string) ([]pluginapi.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt <= len(modelsDiscoveryRetryDelays); attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		models, err := callModelsAPIOnceContext(ctx, accessToken, realm...)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			return models, nil
		}
		lastErr = err
		if attempt == len(modelsDiscoveryRetryDelays) || !isTransientUpstreamErr(err) {
			break
		}
		delay := retryDelayFor(err, modelsDiscoveryRetryDelays[attempt])
		if delay > modelsDiscoveryMaxBackoff {
			delay = modelsDiscoveryMaxBackoff
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

// modelsDiscoveryRetryDelays is the discovery backoff ladder. Shorter than the
// billing ladder because model.for_auth is on the client's model-list path: a
// user waiting for /v1/models must not block for tens of seconds, and the
// 5-minute cache plus the static fallback cover a still-throttled gateway.
var modelsDiscoveryRetryDelays = []time.Duration{700 * time.Millisecond, 2 * time.Second}

// modelsDiscoveryMaxBackoff bounds the whole retry ladder so a hostile/large
// upstream Retry-After cannot stall the model-list request indefinitely.
const modelsDiscoveryMaxBackoff = 5 * time.Second

func callModelsAPIOnce(accessToken string, realm ...string) ([]pluginapi.ModelInfo, error) {
	return callModelsAPIOnceContext(pluginContext(), accessToken, realm...)
}

func callModelsAPIOnceContext(parent context.Context, accessToken string, realm ...string) ([]pluginapi.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	// Model discovery is per-realm (v0.12.18): Global tokens query
	// workbuddy.ai, Intl (codebuddy.ai) tokens query codebuddy.ai, and CN
	// tokens query copilot.tencent.com. The old code only special-cased
	// Global and sent Intl tokens to the CN endpoint, whose answer (or the
	// static fallback) then advertised CN-only models like deepseek-v4-flash
	// to Intl accounts — the Intl gateway rejected those with code 11102
	// "model [...] service info not found". An empty realm keeps the legacy
	// JWT-iss derivation (Global vs CN) for old callers.
	r := ""
	if len(realm) > 0 {
		r = realm[0]
	}
	if r == "" {
		if isGlobalToken(accessToken) {
			r = regionGlobal
		} else {
			r = regionCN
		}
	}

	r = wbServiceForRegion(r)
	serviceRealm := r
	modelsURL, origin := modelsEndpointFor(serviceRealm)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", userAgentForRealm(r))
	applyModelsDiscoveryHeaders(req, r)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	body := resp.Body
	if resp.StatusCode != http.StatusOK {
		// v0.12.49: carry the URL and a body snippet in the error — the
		// panel/log then shows whether the gateway answered with a login
		// redirect (302 HTML), an auth wall (401), or a server fault (5xx)
		// instead of a bare status code.
		snippet := strings.TrimSpace(string(body))
		snippet = strings.Map(func(r rune) rune {
			if r == 0x09 || r == 0x0A || r == 0x0D || (r >= 0x20 && r != 0x7F) {
				return r
			}
			return -1
		}, snippet)
		snippet = truncateRedacted(snippet, 200)
		if snippet == "" {
			snippet = "(empty body)"
		}
		return nil, &upstreamError{StatusCode: resp.StatusCode, Path: modelsURL, Snippet: snippet, RetryAfter: parseRetryAfter(resp.Headers.Get("Retry-After"))}
	}
	var apiResp struct {
		Code int `json:"code"`
		Data struct {
			Models []discoveredModel `json:"models"`
			Agents []struct {
				Name   string   `json:"name"`
				Models []string `json:"models"`
			} `json:"agents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if apiResp.Code != 0 {
		return nil, fmt.Errorf("models API code %d", apiResp.Code)
	}
	var cliModelIDs []string
	for _, a := range apiResp.Data.Agents {
		if a.Name == "cli" {
			cliModelIDs = a.Models
			break
		}
	}
	// Degraded-payload guard (v0.9.32): the discovery endpoint answers with
	// contextWindow=0 for EVERY entry while the account is being throttled —
	// the payload still parses, so the old code cached a catalog of
	// "0 context" models and advertised it to clients (which then fall back to
	// their own defaults and compress context far too early). A catalog with no
	// usable context window is not a catalog: classify it as the upstream rate
	// limit it is, so the caller serves the realm's static fallback (real
	// context windows) and the panel shows the reason instead of caching zeros.
	if discoveryContextDegraded(apiResp.Data.Models) {
		return nil, &upstreamError{
			StatusCode: http.StatusTooManyRequests,
			Path:       modelsURL,
			Snippet: fmt.Sprintf("discovery payload carried no usable contextWindow for %d enabled model(s) — throttled/degraded upstream response",
				len(apiResp.Data.Models)),
		}
	}
	out := modelsFromDiscovery(apiResp.Data.Models, cliModelIDs)
	if collector, ok := ctx.Value(modelDetailsKey{}).(*modelDetailsCollector); ok {
		collector.Rows = detailsFromDiscovery(apiResp.Data.Models)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no user-facing models in discovery payload (cli agent list empty and data.models empty/disabled)")
	}
	return out, nil
}

// discoveryContextDegraded reports whether a discovery payload carries enabled
// models but not one usable context window. Individual entries may legitimately
// omit contextWindow, so a partially-zero payload is NOT degraded; only a
// payload where every enabled entry lost its context window is the throttled
// answer the guard above describes.
func discoveryContextDegraded(models []discoveredModel) bool {
	enabled := 0
	for _, m := range models {
		if m.ID == "" || m.Disabled {
			continue
		}
		enabled++
		if rawJSONI64(m.ContextWindow) > 0 || rawJSONI64(m.MaxInputTokens) > 0 {
			return false
		}
	}
	return enabled > 0
}

// applyModelsDiscoveryHeaders applies only the realm-specific headers required by
// the models discovery endpoint. Keep this separate from chat/billing headers so
// a discovery compatibility fix cannot change unrelated request paths.
func applyModelsDiscoveryHeaders(req *http.Request, realm string) {
	if req == nil || realm != regionIntl {
		return
	}
	// Kept for compatibility with explicit callers of callModelsAPI; Intl is
	// not discovered by the normal model-for-auth path.
	req.Header.Set("X-Domain", "www.codebuddy.ai")
	req.Header.Set("X-Product", "cloud")
	req.Header.Set("X-IDE-Type", "IDE")
	req.Header.Set("X-IDE-Name", "CodeBuddy")
	req.Header.Set("X-IDE-Version", "1.100.0")
	req.Header.Set("X-Product-Version", "1.100.0")
}

// discoveredModel is one entry of the discovery payload's data.models array.
type discoveredModel struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	Credits            string          `json:"credits"`
	Configurable       bool            `json:"configurable"`
	Configured         bool            `json:"configured"`
	IsDefault          bool            `json:"isDefault"`
	SupportsImages     bool            `json:"-"`
	ImageDeclaration   *bool           `json:"supportsImages"`
	Vendor             string          `json:"vendor"`
	MaxInputTokens     json.RawMessage `json:"maxInputTokens"`
	MaxOutputTokens    json.RawMessage `json:"maxOutputTokens"`
	SupportsReasoning  bool            `json:"supportsReasoning"`
	OnlyReasoning      bool            `json:"onlyReasoning"`
	Reasoning          json.RawMessage `json:"reasoning"`
	DisabledMultimodal bool            `json:"disabledMultimodal"`
	Disabled           bool            `json:"disabled"`
	DisabledReason     string          `json:"disabledReason"`
	ContextWindow      json.RawMessage `json:"contextWindow"`
	MaxTokens          json.RawMessage `json:"maxTokens"`
}

// rawJSONI64 decodes a JSON number field that may be number, numeric string
// or null; any other shape decodes to 0.
func rawJSONI64(raw json.RawMessage) int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err == nil {
		return int64(v)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var f float64
		if _, err := fmt.Sscanf(s, "%g", &f); err == nil {
			return int64(f)
		}
	}
	return 0
}

// modelsFromDiscovery builds the advertised list from one discovery payload.
// v0.9.8: the cli agent's model IDs form the base (order preserved), then any
// ENABLED data.models entry missing from that list is PROMOTED. Tencent's
// data.models is the account's own registration table — the official client
// picker shows exactly these — so a freshly rolled-out model (e.g.
// deepseek-v4.1-flash on 2026-09-10) must surface even while the cli agent
// list still lags; the old cli-only filter made the plugin trail the official
// client on every model launch. Promotions are logged so a potential upstream
// 11102 ("service info not found") chat failure is traceable to this decision.
// A renamed/missing cli agent no longer nukes discovery either: enabled
// data.models alone still produce the list (before: hard error → stale static
// fallback).
// Virtual-alias / non-chat model pruning, ported from workbuddy2api-hub's
// is_chat_model (measured against the same upstream): the models API and the
// desktop cache advertise entries that are not chat-capable — internal helpers
// ("lite" backs title generation/compaction and upstream rejects it with
// 11102), quick-preset aliases, IDE inline completion models, and image
// variants. Advertising them makes clients pick a model the gateway can't
// serve.
var (
	nonChatModels = map[string]struct{}{
		"lite":           {},
		"default-model":  {},
		"fast-model":     {},
		"balanced-model": {},
		"primary-model":  {},
		"deep-model":     {},
		// Inline-completion Hunyuan builds the desktop UI hides (the chat
		// hunyuan entries stay).
		"hunyuan-3b": {},
		"hunyuan-7b": {},
	}
	nonChatPrefixes = []string{"codewise-", "completion-"}
	nonChatSuffixes = []string{"-image-alpha", "-image-alpha-edit", "-taco-completion"}
)

// isChatModelID reports whether an advertised model ID is a chat-capable
// model. Case-insensitive on the exact-match table; prefixes/suffixes are
// matched on the lowercased ID.
func isChatModelID(id string) bool {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return false
	}
	if _, hit := nonChatModels[strings.ToLower(trimmed)]; hit {
		return false
	}
	low := strings.ToLower(trimmed)
	for _, p := range nonChatPrefixes {
		if strings.HasPrefix(low, p) {
			return false
		}
	}
	for _, s := range nonChatSuffixes {
		if strings.HasSuffix(low, s) {
			return false
		}
	}
	return true
}

func modelsFromDiscovery(dataModels []discoveredModel, cliModelIDs []string) []pluginapi.ModelInfo {
	rememberModelDefaultEfforts(dataModels)
	byID := make(map[string]discoveredModel, len(dataModels))
	for _, m := range dataModels {
		if m.ID != "" && isChatModelID(m.ID) {
			byID[m.ID] = m
		}
	}
	toInfo := func(m discoveredModel) pluginapi.ModelInfo {
		info := pluginapi.ModelInfo{
			ID:                         m.ID,
			Name:                       m.Name,
			ContextLength:              rawJSONI64(m.ContextWindow),
			MaxCompletionTokens:        rawJSONI64(m.MaxTokens),
			OwnedBy:                    providerName,
			SupportedGenerationMethods: []string{"chat"},
		}
		if info.ContextLength == 0 {
			info.ContextLength = rawJSONI64(m.MaxInputTokens)
		}
		if info.MaxCompletionTokens == 0 {
			info.MaxCompletionTokens = rawJSONI64(m.MaxOutputTokens)
		}
		var reason struct {
			Levels []string `json:"supportedEfforts"`
		}
		_ = json.Unmarshal(m.Reasoning, &reason)
		if len(reason.Levels) > 0 {
			info.Thinking = &pluginapi.ThinkingSupport{Levels: reason.Levels}
		}
		if m.ImageDeclaration != nil {
			info.SupportedInputModalities = []string{"text"}
			if *m.ImageDeclaration && !m.DisabledMultimodal {
				info.SupportedInputModalities = append(info.SupportedInputModalities, "image")
			}
		}
		if info.Name == "" {
			info.Name = info.ID
		}
		return info
	}
	seen := make(map[string]bool, len(cliModelIDs)+len(dataModels))
	out := make([]pluginapi.ModelInfo, 0, len(cliModelIDs)+len(dataModels))
	for _, id := range cliModelIDs {
		m, ok := byID[id]
		if !ok || m.Disabled || !isChatModelID(id) {
			continue
		}
		key := strings.ToLower(id)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, toInfo(m))
	}
	var promoted []string
	for _, m := range dataModels {
		if m.ID == "" || m.Disabled || !isChatModelID(m.ID) {
			continue
		}
		key := strings.ToLower(m.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, toInfo(m))
		promoted = append(promoted, m.ID)
	}
	if len(promoted) > 0 {
		log.Printf("models: promoted %d upstream model(s) not in cli agent list: %s",
			len(promoted), strings.Join(promoted, ", "))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// modelDefaultEfforts caches the reasoning tier each model's upstream catalog
// declares (discovery payload: reasoning.defaultEffort, with reasoning.effort
// as the older spelling). Keyed by lowercased model ID. The executor's
// deepseek effort injection reads this so the tier it fills into a request
// cannot disagree with what the model list promised the client — the same
// source-of-truth rule workbuddy2api-hub's model_default_effort follows.
var (
	modelDefaultEffortsMu sync.Mutex
	modelDefaultEfforts   = map[string]string{}
)

// rememberModelDefaultEfforts records exactly the reasoning defaults declared
// by the latest discovery payload. An omitted default clears stale catalog data.
func rememberModelDefaultEfforts(dataModels []discoveredModel) {
	modelDefaultEffortsMu.Lock()
	defer modelDefaultEffortsMu.Unlock()
	for _, m := range dataModels {
		id := strings.ToLower(strings.TrimSpace(m.ID))
		if id == "" {
			continue
		}
		if eff := parseDefaultEffort(m.Reasoning); eff != "" {
			modelDefaultEfforts[id] = eff
		} else {
			delete(modelDefaultEfforts, id)
		}
	}
}

// parseDefaultEffort reads the declared default reasoning tier out of one
// discovery entry's reasoning object. Empty when absent or not an object.
func parseDefaultEffort(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var shape struct {
		DefaultEffort string `json:"defaultEffort"`
		Effort        string `json:"effort"`
	}
	if json.Unmarshal(raw, &shape) != nil {
		return ""
	}
	if v := strings.ToLower(strings.TrimSpace(shape.DefaultEffort)); v != "" {
		return v
	}
	return strings.ToLower(strings.TrimSpace(shape.Effort))
}

// defaultEffortForUpstreamModel returns only a reasoning tier explicitly
// declared by the upstream model catalog. Guessing a tier can turn an otherwise
// valid request into upstream code 11150 (invalid_reasoning_effort).
func defaultEffortForUpstreamModel(model string) string {
	key := strings.ToLower(strings.TrimSpace(model))
	modelDefaultEffortsMu.Lock()
	defer modelDefaultEffortsMu.Unlock()
	return modelDefaultEfforts[key]
}

func cacheModelAliases(host pluginapi.HostConfigSummary) {
	// Host.AuthDir only rides on model-discovery RPCs, so this is where the
	// daily-quota counter file learns where to persist. Idempotent: it returns
	// immediately once the path is set.
	setDailyQuotaPath(host.AuthDir)

	entries := host.OAuthModelAlias[providerName]
	if len(entries) == 0 {
		// Host may key the channel case-insensitively; fall back to a scan.
		for channel, list := range host.OAuthModelAlias {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				entries = list
				break
			}
		}
	}
	byAlias := make(map[string]string, len(entries))
	for _, e := range entries {
		name := strings.TrimSpace(e.Name)
		alias := strings.TrimSpace(e.Alias)
		if name == "" || alias == "" || strings.EqualFold(name, alias) {
			continue
		}
		byAlias[strings.ToLower(alias)] = name
	}
	modelAliasCache.Lock()
	modelAliasCache.byAlias = byAlias
	modelAliasCache.Unlock()
}

// resolveUpstreamModel maps an aliased requested model back to the real
// upstream model ID. Returns the input unchanged when nothing matches.
func resolveUpstreamModel(model string, attributes map[string]string) string {
	m := strings.TrimSpace(model)
	if m == "" {
		return model
	}
	key := strings.ToLower(m)
	if name, ok := parseModelAliasAttribute(attributes)[key]; ok {
		return name
	}
	modelAliasCache.RLock()
	name, ok := modelAliasCache.byAlias[key]
	modelAliasCache.RUnlock()
	if ok {
		return name
	}
	return m
}

// parseModelAliasAttribute decodes a per-auth alias override from auth
// attributes. Accepts JSON ([{"name":...,"alias":...}] or {alias:name}) or
// comma-separated "alias=name" pairs.
func parseModelAliasAttribute(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	raw := ""
	for _, k := range []string{"model_alias", "model-alias", "oauth-model-alias"} {
		if v := strings.TrimSpace(attributes[k]); v != "" {
			raw = v
			break
		}
	}
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	add := func(name, alias string) {
		name, alias = strings.TrimSpace(name), strings.TrimSpace(alias)
		if name != "" && alias != "" && !strings.EqualFold(name, alias) {
			out[strings.ToLower(alias)] = name
		}
	}
	if strings.HasPrefix(raw, "[") {
		var list []struct {
			Name  string `json:"name"`
			Alias string `json:"alias"`
		}
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, e := range list {
				add(e.Name, e.Alias)
			}
			return out
		}
	}
	if strings.HasPrefix(raw, "{") {
		var m map[string]string
		if json.Unmarshal([]byte(raw), &m) == nil {
			for alias, name := range m {
				add(name, alias)
			}
			return out
		}
	}
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			add(kv[1], kv[0])
		}
	}
	return out
}

func globalModelSettingsSnapshot() (disabled, enabled []string) {
	globalModelSettingsMu.RLock()
	defer globalModelSettingsMu.RUnlock()
	return append([]string(nil), globallyDisabledModels...), append([]string(nil), globallyEnabledModels...)
}

func currentGloballyDisabledModels() []string {
	disabled, _ := globalModelSettingsSnapshot()
	return disabled
}

func currentGloballyEnabledModels() []string {
	_, enabled := globalModelSettingsSnapshot()
	return enabled
}

func setGloballyDisabledModels(ids []string) {
	globalModelSettingsMu.Lock()
	globallyDisabledModels = parsePinnedModelList(strings.Join(ids, ","))
	globalModelSettingsMu.Unlock()
}

func setGloballyEnabledModels(ids []string) {
	globalModelSettingsMu.Lock()
	globallyEnabledModels = parsePinnedModelList(strings.Join(ids, ","))
	globalModelSettingsMu.Unlock()
}

func isGloballyDisabledModel(id string) bool {
	key := strings.ToLower(strings.TrimSpace(id))
	if key == "" {
		return true
	}
	disabled, enabled := globalModelSettingsSnapshot()
	for _, id := range disabled {
		if strings.EqualFold(key, strings.TrimSpace(id)) {
			return true
		}
	}
	for _, id := range enabled {
		if strings.EqualFold(key, strings.TrimSpace(id)) {
			return false
		}
	}
	// A model is opt-in: an empty or missing models_enabled entry keeps it off.
	return true
}

func filterGloballyDisabledModels(models []pluginapi.ModelInfo) []pluginapi.ModelInfo {
	disabled, enabled := globalModelSettingsSnapshot()
	disabledSet := make(map[string]struct{}, len(disabled))
	for _, id := range disabled {
		disabledSet[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
	}
	enabledSet := make(map[string]struct{}, len(enabled))
	for _, id := range enabled {
		enabledSet[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
	}
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, model := range models {
		key := strings.ToLower(strings.TrimSpace(model.ID))
		if key == "" {
			continue
		}
		if _, blocked := disabledSet[key]; blocked {
			continue
		}
		if _, allowed := enabledSet[key]; allowed {
			out = append(out, model)
		}
	}
	return out
}

// filterExcludedModels removes models listed in oauth-excluded-models for
// the workbuddy provider. The host passes this config via HostConfigSummary.
func filterExcludedModels(models []pluginapi.ModelInfo, host pluginapi.HostConfigSummary) []pluginapi.ModelInfo {
	if len(host.ExcludedModels) == 0 {
		return models
	}
	// Try exact provider match, then case-insensitive scan.
	excluded := host.ExcludedModels[providerName]
	if len(excluded) == 0 {
		for channel, list := range host.ExcludedModels {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				excluded = list
				break
			}
		}
	}
	if len(excluded) == 0 {
		return models
	}
	excludeSet := make(map[string]struct{}, len(excluded))
	for _, m := range excluded {
		excludeSet[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
	}
	// Use a fresh slice — models[:0] would alias the input's backing array,
	// which may be the dynamicModelsCache's own slice. Mutating it in place
	// would corrupt the cache for subsequent callers (P0 bug: after one
	// filterExcludedModels call, cache returns the filtered list as the
	// "full" list on the next fetch).
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, m := range models {
		if _, skip := excludeSet[strings.ToLower(m.ID)]; skip {
			continue
		}
		out = append(out, m)
	}
	return out
}

func handleGlobalModelCatalog() []panelModel {
	seen := make(map[string]pluginapi.ModelInfo)
	meta := map[string]modelDetails{}
	conflict := map[string]bool{}
	add := func(model pluginapi.ModelInfo) {
		id := strings.TrimSpace(model.ID)
		key := strings.ToLower(id)
		if id == "" {
			return
		}
		if _, exists := seen[key]; !exists {
			model.ID = id
			seen[key] = model
		}
	}
	files, err := hostAuthList()
	if err == nil {
		for _, file := range files {
			sa, getErr := hostAuthGet(file.AuthIndex)
			if getErr != nil {
				continue
			}
			raw, marshalErr := storedAuthJSON(sa)
			if marshalErr != nil {
				continue
			}
			resolved := resolveCredentialModels(pluginContext(), raw, false)
			for _, model := range resolved.Models {
				add(model)
				if d, ok := resolved.Details[model.ID]; ok {
					key := strings.ToLower(model.ID)
					old, exists := meta[key]
					a, _ := json.Marshal(old)
					b, _ := json.Marshal(d)
					if exists && string(a) != string(b) {
						conflict[key] = true
					}
					meta[key] = d
				}
			}
		}
	}
	for _, model := range globalModelRegistryList() {
		add(model)
	}
	// Keep saved enabled/disabled IDs manageable even when the upstream catalog no longer advertises them.
	savedIDs := append(currentGloballyDisabledModels(), currentGloballyEnabledModels()...)
	for _, id := range savedIDs {
		add(pluginapi.ModelInfo{ID: id, Name: "当前目录未返回"})
	}
	out := make([]panelModel, 0, len(seen))
	for _, model := range seen {
		out = append(out, panelModel{
			ID:                  model.ID,
			Name:                model.Name,
			ContextLength:       model.ContextLength,
			MaxCompletionTokens: model.MaxCompletionTokens,
			Disabled:            isGloballyDisabledModel(model.ID),
		})
	}
	for i := range out {
		key := strings.ToLower(out[i].ID)
		if strings.HasPrefix(key, "hy3") || strings.HasPrefix(key, "hy4") {
			out[i].EffectiveEffort = "high"
		}
		if !conflict[key] {
			out[i].modelDetails = meta[key]
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
	return out
}

func handleModelStatic(raw []byte) ([]byte, error) {
	var req pluginapi.StaticModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cacheModelAliases(req.Host)
	models := staticModelsForRealm(regionCN)
	models = filterGloballyDisabledModels(models)
	models = filterExcludedModels(models, req.Host)
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models})
}

func globalModelRegistryList() []pluginapi.ModelInfo {
	seen := make(map[string]struct{})
	var models []pluginapi.ModelInfo
	for _, realm := range []string{regionCN, regionIntl} {
		for _, model := range staticModelsForRealm(realm) {
			key := strings.ToLower(strings.TrimSpace(model.ID))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			models = append(models, model)
		}
	}
	customStaticModels.RLock()
	for _, model := range customStaticModels.models {
		key := strings.ToLower(strings.TrimSpace(model.ID))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, pluginapi.ModelInfo{ID: model.ID, Name: model.Name, ContextLength: model.Context,
			MaxCompletionTokens: model.MaxTokens, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}, UserDefined: true})
	}
	customStaticModels.RUnlock()
	return models
}

func handleModelForAuth(raw []byte) ([]byte, error) {
	var req struct {
		pluginapi.AuthModelRequest
		HostCallbackID string `json:"host_callback_id,omitempty"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	// Always return the plugin's canonical provider key. The host skips any
	// response whose Provider doesn't match the auth's provider, so echoing
	// req.AuthProvider back would silently drop the model list whenever the
	// auth file carries a non-canonical provider string.
	cacheModelAliases(req.Host)
	models := resolveCredentialModels(withHostCallbackID(pluginContext(), req.HostCallbackID), req.StorageJSON, false).Models
	models = filterGloballyDisabledModels(models)
	models = filterExcludedModels(models, req.Host)
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models})
}
