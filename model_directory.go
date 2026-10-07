package main

// Account directory is observational. It deliberately does not call
// modelsFromDiscovery, populate the routing cache, or change CPA configuration.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type directoryModel struct {
	panelModel
	Description        string           `json:"description,omitempty"`
	Tags               []string         `json:"tags,omitempty"`
	IsDefault          bool             `json:"is_default,omitempty"`
	SupportsReasoning  *bool            `json:"supports_reasoning,omitempty"`
	SupportsToolCall   *bool            `json:"supports_tool_call,omitempty"`
	ReasoningSummary   string           `json:"reasoning_summary,omitempty"`
	DisabledReason     string           `json:"disabled_reason,omitempty"`
	Sources            []string         `json:"sources"`
	ImageInputConflict bool             `json:"image_input_conflict,omitempty"`
	ImageInputSources  map[string]*bool `json:"image_input_sources"`
	Family             string           `json:"family"`
	FamilyDerived      bool             `json:"family_derived"`
	RoutingStatus      string           `json:"routing_status"`
}
type directoryAttempt struct {
	Path         string `json:"path"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	UpstreamCode *int   `json:"upstream_code,omitempty"`
	Error        string `json:"error,omitempty"`
}
type directorySource struct {
	Attempts []directoryAttempt `json:"attempts,omitempty"`

	Source string `json:"source"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Count  int    `json:"count"`
	Error  string `json:"error,omitempty"`
}
type directoryResult struct {
	Status    string            `json:"status"`
	Models    []directoryModel  `json:"models"`
	Sources   []directorySource `json:"sources"`
	FetchedAt string            `json:"fetched_at,omitempty"`
	Cached    bool              `json:"cached"`
	Service   string            `json:"service"`
	Warning   string            `json:"warning,omitempty"`
}
type directoryEntry struct {
	body    []byte
	fetched time.Time
}

var accountDirectoryCache = struct {
	sync.Mutex
	entries map[string]directoryEntry
}{entries: map[string]directoryEntry{}}

func directoryFamily(id, vendor string) (string, bool) {
	if vendor = strings.TrimSpace(vendor); vendor != "" {
		return vendor, false
	}
	low := strings.ToLower(id)
	for _, p := range []struct{ prefix, name string }{{"deepseek", "DeepSeek"}, {"kimi", "Kimi"}, {"glm", "智谱 GLM"}, {"hunyuan", "腾讯混元"}, {"hy3", "腾讯混元"}, {"hy4", "腾讯混元"}} {
		if strings.HasPrefix(low, p.prefix) {
			return p.name, true
		}
	}
	return "", true
}

// Accept either data.models objects or a narrow data string array. Missing or
// malformed capability declarations remain unknown, not false.
func parseDirectoryPayload(raw json.RawMessage, source string) ([]directoryModel, error) {
	var entries []json.RawMessage
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing data")
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("invalid model array")
		}
	} else {
		var object struct {
			Models json.RawMessage `json:"models"`
		}
		if json.Unmarshal(raw, &object) != nil || len(object.Models) == 0 || string(object.Models) == "null" {
			return nil, fmt.Errorf("missing models array")
		}
		if json.Unmarshal(object.Models, &entries) != nil {
			return nil, fmt.Errorf("invalid models array")
		}
	}
	rows := []directoryModel{}
	seen := map[string]bool{}
	for _, entry := range entries {
		var id string
		var fields map[string]json.RawMessage
		if json.Unmarshal(entry, &id) == nil {
			fields = map[string]json.RawMessage{"id": mustJSON(id)}
		} else if json.Unmarshal(entry, &fields) != nil || fields == nil {
			return nil, fmt.Errorf("invalid model entry")
		}
		_ = json.Unmarshal(fields["id"], &id)
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		str := func(key string) string {
			var s string
			_ = json.Unmarshal(fields[key], &s)
			return strings.TrimSpace(s)
		}
		boolean := func(key string) *bool {
			var b bool
			if json.Unmarshal(fields[key], &b) != nil || string(fields[key]) == "null" {
				return nil
			}
			return &b
		}
		isTrue := func(key string) bool { v := boolean(key); return v != nil && *v }
		var tags []string
		_ = json.Unmarshal(fields["tags"], &tags)
		var reason struct {
			Efforts []string `json:"supportedEfforts"`
			Summary string   `json:"summary"`
		}
		_ = json.Unmarshal(fields["reasoning"], &reason)
		effortSeen := map[string]bool{}
		efforts := []string{}
		for _, effort := range reason.Efforts {
			effort = strings.TrimSpace(effort)
			if effort != "" && !effortSeen[effort] {
				efforts = append(efforts, effort)
				effortSeen[effort] = true
			}
		}
		ctx := rawJSONI64(fields["maxInputTokens"])
		if ctx <= 0 {
			ctx = rawJSONI64(fields["contextWindow"])
		}
		output := rawJSONI64(fields["maxOutputTokens"])
		if output <= 0 {
			output = rawJSONI64(fields["maxTokens"])
		}
		if ctx < 0 {
			ctx = 0
		}
		if output < 0 {
			output = 0
		}
		images := boolean("supportsImages")
		if isTrue("disabledMultimodal") {
			v := false
			images = &v
		}
		credits := str("credits")
		if credits == "" {
			var number json.Number
			if json.Unmarshal(fields["credits"], &number) == nil {
				credits = number.String()
			}
		}
		name := str("name")
		if name == "" {
			name = id
		}
		description := str("descriptionZh")
		if description == "" {
			description = str("description")
		}
		family, derived := directoryFamily(id, str("vendor"))
		row := directoryModel{panelModel: panelModel{ID: id, Name: name, ContextLength: ctx, MaxCompletionTokens: output, Disabled: isTrue("disabled"), modelDetails: modelDetails{Credits: credits, Vendor: str("vendor"), Efforts: efforts, DefaultEffort: parseDefaultEffort(fields["reasoning"]), SupportsImages: images, OnlyReasoning: isTrue("onlyReasoning"), Source: source}}, Description: description, Tags: tags, IsDefault: isTrue("isDefault"), SupportsReasoning: boolean("supportsReasoning"), SupportsToolCall: boolean("supportsToolCall"), ReasoningSummary: reason.Summary, DisabledReason: str("disabledReason"), Family: family, FamilyDerived: derived, Sources: []string{source}, ImageInputSources: map[string]*bool{source: images}, RoutingStatus: "directoryOnly"}
		// Full directory includes disabled/preset/non-chat entries, but never
		// labels their presence as proof of routing or actual model capabilities.
		rows = append(rows, row)
	}
	return rows, nil
}
func mergeDirectory(enterprise, v3 []directoryModel) []directoryModel {
	out := []directoryModel{}
	seen := map[string]int{}
	for _, rows := range [][]directoryModel{v3, enterprise} {
		for _, row := range rows {
			if i, ok := seen[row.ID]; ok {
				p := &out[i]
				p.Sources = append(p.Sources, row.Sources...)
				for name, v := range row.ImageInputSources {
					p.ImageInputSources[name] = v
				}
				var known *bool
				conflict := false
				for _, v := range p.ImageInputSources {
					if v == nil {
						continue
					}
					if known != nil && *known != *v {
						conflict = true
					}
					known = v
				}
				p.ImageInputConflict = conflict
				if conflict {
					p.SupportsImages = nil
				} else {
					p.SupportsImages = known
				}
				continue
			}
			// Copy the map to avoid modifying source snapshots while merging.
			copied := map[string]*bool{}
			for k, v := range row.ImageInputSources {
				copied[k] = v
			}
			row.ImageInputSources = copied
			seen[row.ID] = len(out)
			out = append(out, row)
		}
	}
	return out
}

func directoryPaths(service string) (string, string, []string) {
	if service == regionGlobal {
		return upstreamBaseGlobal, originRefererGlobal, []string{"/v2/enterprises/personal/models", "/console/enterprises/personal/models"}
	}
	return upstreamBaseCN, originReferer, []string{"/console/enterprises/personal/models"}
}
func fetchDirectorySource(ctx context.Context, token, uid, service, source string, paths []string) ([]directoryModel, directorySource) {
	base, origin, _ := directoryPaths(service)
	state := directorySource{Source: source, Status: "error"}
	for i, path := range paths {
		state.Path = path
		attempt := directoryAttempt{Path: path}
		finish := func(message string) {
			attempt.Error = message
			state.Error = message
			state.Attempts = append(state.Attempts, attempt)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			finish("invalid endpoint")
			return nil, state
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("Referer", origin+"/")
		platform, lang := "WorkBuddy", "zh-CN"
		if service == regionGlobal {
			platform, lang = "WorkBuddy AI", "en-US"
		}
		req.Header.Set("User-Agent", "WorkBuddy/5.5.4 "+platform+"/5.5.4 CLI/2.137.1")
		req.Header.Set("Accept-Language", lang)
		req.Header.Set("X-CodeBuddy-Request", "1")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		applyFingerprintHeaders(uid, req.Header.Set)
		resp, err := hostHTTPDo(req)
		if err != nil {
			message := "host HTTP request failed"
			if ctx.Err() != nil {
				message = "request canceled or timed out"
			}
			// Do not expose host error bodies, proxy passwords or bearer tokens.
			if strings.Contains(err.Error(), "operation open") {
				message = "host HTTP operation open failed"
			}
			if strings.Contains(err.Error(), "bridge unavailable") {
				message = "CPA host.http bridge unavailable"
			}
			finish(message)
			return nil, state
		}
		attempt.HTTPStatus = resp.StatusCode
		more := i+1 < len(paths)
		if resp.StatusCode != http.StatusOK {
			finish(fmt.Sprintf("HTTP %d", resp.StatusCode))
			// Fixed same-service read-only compatibility paths; no retries for
			// denied credentials, rate limits, or transient server failures.
			if more && (resp.StatusCode == 400 || resp.StatusCode == 404 || resp.StatusCode == 405 || resp.StatusCode == 501) {
				continue
			}
			return nil, state
		}
		if len(resp.Body) > 4<<20 {
			finish("directory response too large")
			return nil, state
		}
		var envelope struct {
			Code json.RawMessage `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		var code int
		valid := json.Unmarshal(resp.Body, &envelope) == nil && len(envelope.Code) > 0 && string(envelope.Code) != "null"
		if valid && json.Unmarshal(envelope.Code, &code) != nil {
			var value string
			valid = json.Unmarshal(envelope.Code, &value) == nil
			if valid {
				code, err = strconv.Atoi(value)
				valid = err == nil
			}
		}
		if !valid {
			finish("invalid directory envelope")
			if more {
				continue
			}
			return nil, state
		}
		attempt.UpstreamCode = &code
		if code != 0 {
			finish(fmt.Sprintf("upstream code %d", code))
			// Do not negotiate around explicit authentication/access/throttle errors.
			if more && code != 401 && code != 403 && code != 429 {
				continue
			}
			return nil, state
		}
		rows, err := parseDirectoryPayload(envelope.Data, source)
		if err != nil {
			finish(err.Error())
			if more {
				continue
			}
			return nil, state
		}
		finish("")
		state.Status = "ok"
		state.Count = len(rows)
		return rows, state
	}
	return nil, state
}

func resolveAccountDirectory(parent context.Context, sa *storedAuth, force bool) directoryResult {
	result := directoryResult{Status: "failed", Models: []directoryModel{}, Sources: []directorySource{}}
	raw, err := storedAuthJSON(sa)
	if err != nil || sa == nil {
		result.Warning = "invalid credential"
		return result
	}
	token, _ := extractAccessToken(raw)
	service := serviceRealmForStorage(raw, token)
	result.Service = service
	if service != regionCN && service != regionGlobal {
		result.Status = "unsupported"
		result.Warning = "CodeBuddy International directory is not verified; no cross-service probing"
		return result
	}
	if token == "" {
		result.Warning = "credential has no access token"
		return result
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	tokenHash := sha256.Sum256([]byte(token))
	key := modelCacheKey(service, raw, token) + ":" + hex.EncodeToString(tokenHash[:])
	release, err := lockDiscovery(ctx, "directory:"+key)
	if err != nil {
		result.Warning = "request canceled or timed out"
		return result
	}
	defer release()
	accountDirectoryCache.Lock()
	entry, exists := accountDirectoryCache.entries[key]
	if force {
		delete(accountDirectoryCache.entries, key)
	}
	accountDirectoryCache.Unlock()
	if !force && exists && time.Since(entry.fetched) < dynamicModelsCacheTTL {
		if json.Unmarshal(entry.body, &result) == nil {
			result.Cached = true
			return result
		}
	}
	_, _, paths := directoryPaths(service)
	uid := sa.Account.UID

	var ent, v3 []directoryModel
	var es, vs directorySource
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ent, es = fetchDirectorySource(ctx, token, uid, service, "enterprise_models", paths)
	}()
	go func() {
		defer wg.Done()
		v3, vs = fetchDirectorySource(ctx, token, uid, service, "v3_config", []string{"/v3/config"})
	}()
	wg.Wait()
	result.Sources = []directorySource{es, vs}
	if ctx.Err() != nil {
		result.Warning = "request canceled or timed out"
		return result
	}
	if es.Status != "ok" && vs.Status != "ok" {
		result.Warning = "both directory sources failed"
		return result
	}
	result.Models = mergeDirectory(ent, v3)
	result.Status = "ok"
	if es.Status != "ok" || vs.Status != "ok" {
		result.Status = "partial"
		result.Warning = "one directory source failed"
	}
	result.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	// Only complete successful snapshots are cached. Partial failures are retried
	// on the next read; forced failure never silently returns an old snapshot.
	if result.Status == "ok" {
		accountDirectoryCache.Lock()
		for k, v := range accountDirectoryCache.entries {
			if time.Since(v.fetched) >= dynamicModelsCacheTTL {
				delete(accountDirectoryCache.entries, k)
			}
		}
		if len(accountDirectoryCache.entries) >= 128 {
			for k := range accountDirectoryCache.entries {
				delete(accountDirectoryCache.entries, k)
				break
			}
		}
		accountDirectoryCache.entries[key] = directoryEntry{body: mustJSON(result), fetched: time.Now()}
		accountDirectoryCache.Unlock()
	}
	return result
}
func handleAccountDirectory(req pluginapi.ManagementRequest, ctx context.Context, force bool) map[string]any {
	id := queryParam(req, "auth_index")
	if id == "" {
		id = requestBodyString(req, "auth_index")
	}
	if id == "" {
		return map[string]any{"error": "auth_index is required"}
	}
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": "cannot list CPA credentials"}
	}
	for _, f := range files {
		if f.AuthIndex == id {
			sa, _, err := hostAuthGetBundle(id)
			if err != nil {
				return map[string]any{"error": "cannot load CPA credential"}
			}
			result := resolveAccountDirectory(ctx, sa, force)
			return map[string]any{"auth_index": id, "name": f.Name, "region": panelRegion(sa), "status": result.Status, "models": result.Models, "sources": result.Sources, "fetched_at": result.FetchedAt, "cached": result.Cached, "service": result.Service, "warning": result.Warning, "count": len(result.Models)}
		}
	}
	return map[string]any{"error": "auth_index not found"}
}
