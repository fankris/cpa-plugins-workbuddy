// config.go decodes plugin config from config_yaml on every
// register/reconfigure call. All plugin-level config lives here so the rest
// of the plugin reads consistent, lock-protected snapshots.
//
// Parsing goes through the YAML parser (see scalarConfig): inline comments,
// quoting and nesting all behave the way the user expects. The old line-prefix
// parser silently misread `checkin_auto: true # 开启` as false and
// `login_region: "intl" # 海外` as cn.
//
// Usage monitoring is not configured here (and not implemented at all): the
// CPA host records plugin-executor usage into its own queue
// (/v0/management/usage-queue), which CPAMP's collector pulls. The plugin's
// own direct push — and the usage_report_*/management_key/legacy_usage_fallback
// keys that fed it — were removed in 0.9.30; leftover keys are ignored.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// check-in schedule: 09:00 and 21:00 local time.
var checkinHours = []int{9, 21}

// night-growth schedule: 01:00 local time, the dedicated tick for tasks that
// only count inside the 23:00–08:00 window (夜猫子). The 09:00/21:00 growth
// ticks sit outside that window, so without this run black_cat would never be
// lit automatically. Kept beside checkinHours for the same test-seam reasons.
var nightGrowthHours = []int{1}

// plugin-level config decoded from plugin.register/reconfigure config_yaml.
var (
	checkinAuto   = true // enabled by default
	checkinAutoMu sync.RWMutex

	// loginPlatform selects the client variant used for NEW logins:
	// "CLI" (workbuddy) or "ide" (CodeBuddy IDE). Configured via
	// config_yaml login_platform: and read at auth.login_start time.
	loginPlatform   = "CLI"
	loginPlatformMu sync.RWMutex

	// loginRegion selects the upstream realm for NEW logins: "cn"
	// (copilot.tencent.com, default) or "intl" (codebuddy.ai, IDE client;
	// merged codebuddy-intl plugin v0.11.0).
	loginRegion   = regionCN
	loginRegionMu sync.RWMutex

	// pinnedModels: per-region model ID lists from advanced YAML. Legacy
	// models_global and models_intl keys both map to Intl after the merge.
	pinnedModels   = map[string][]string{}
	pinnedModelsMu sync.RWMutex

	globallyDisabledModels = []string{}
	globallyEnabledModels  = []string{} // models are off unless explicitly enabled
	globalModelSettingsMu  sync.RWMutex
)

// customStaticModelConfig is the user-facing YAML shape for models[].
type customStaticModelConfig struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	Channel   string `yaml:"channel"`
	Realm     string `yaml:"realm"` // legacy alias for Channel
	Context   int64  `yaml:"context"`
	MaxTokens int64  `yaml:"max_tokens"`
	Enabled   *bool  `yaml:"enabled"`
}

func normalizeChannel(raw string) string {
	c := strings.ToLower(strings.TrimSpace(raw))
	switch c {
	case "cn":
		return "cn"
	case "intl_global", "intl", "global", "intl-global", "intl_and_global":
		return "intl_global"
	case "all", "*":
		return "all"
	default:
		return ""
	}
}

func prettifyModelName(id string) string {
	switch strings.ToLower(id) {
	case "gpt-6-astra":
		return "GPT-6 Astra"
	case "gpt-5.6-sol":
		return "GPT-5.6 Sol"
	case "deepseek-v4.1-flash":
		return "DeepSeek V4.1 Flash"
	default:
		return id
	}
}

// scalarConfig is the workbuddy config subtree decoded into a flat
// key → YAML node map. Reading through the YAML parser (instead of matching
// raw line prefixes) makes inline comments, quoting, and nesting behave the
// way the user expects:
//
//	checkin_auto: true # 开启     → true    (line parser: false!)
//	login_region: "intl" # 海外   → intl    (line parser: cn!)
//	models_cn: a, b # note        → [a, b]  (line parser: ["a", "b # note"])
//	nested: {checkin_auto: false} → ignored (line parser: read it!)
type scalarConfig map[string]yaml.Node

// pluginConfigScalars parses config_yaml and returns the workbuddy-scoped
// mapping. Returns nil when the payload is absent or unparseable, which keeps
// every caller on its documented default.
func pluginConfigScalars(configYAML []byte) scalarConfig {
	if len(configYAML) == 0 {
		return nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(configYAML, &root); err != nil {
		log.Printf("config: YAML unmarshal failed: %v", err)
		return nil
	}
	node := findWorkBuddyConfigNode(&root)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	out := make(scalarConfig, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := strings.ToLower(strings.TrimSpace(node.Content[i].Value))
		if key == "" {
			continue
		}
		out[key] = *node.Content[i+1]
	}
	return out
}

// raw returns the raw (comment- and quote-free) scalar text for key.
// Empty when the key is absent or holds a mapping/sequence.
func (c scalarConfig) raw(key string) string {
	n, ok := c[key]
	if !ok || n.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(n.Value)
}

// has reports whether the key is present with a usable value (scalar or
// non-empty sequence), so callers can distinguish "absent → keep default"
// from "explicitly set to something unparseable → off".
func (c scalarConfig) has(key string) bool {
	n, ok := c[key]
	if !ok {
		return false
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return strings.TrimSpace(n.Value) != ""
	case yaml.SequenceNode:
		return len(n.Content) > 0
	default:
		return false
	}
}

// boolValue parses a boolean key with the plugin's tolerant spelling set
// (true/1/yes/on/开/开启 and friends). Absent keys keep def.
func (c scalarConfig) boolValue(key string, def bool) bool {
	if !c.has(key) {
		return def
	}
	return parseBoolConfig(c.raw(key))
}

// listValue returns the string list for key, accepting both YAML sequences
// and comma-separated scalars.
func (c scalarConfig) listValue(key string) []string {
	n, ok := c[key]
	if !ok {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return parsePinnedModelList(n.Value)
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, item := range n.Content {
			if item.Kind == yaml.ScalarNode {
				parts = append(parts, item.Value)
			}
		}
		return parsePinnedModelList(strings.Join(parts, ","))
	default:
		return nil
	}
}

// equalsFold reports whether key holds any of the given values
// (case-insensitive), used for enum-style settings.
func (c scalarConfig) equalsFold(key string, values ...string) bool {
	got := c.raw(key)
	if got == "" {
		return false
	}
	for _, v := range values {
		if strings.EqualFold(got, v) {
			return true
		}
	}
	return false
}

func findWorkBuddyConfigNode(root *yaml.Node) *yaml.Node {
	if root == nil {
		return nil
	}
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i < len(doc.Content); i += 2 {
		k := strings.ToLower(doc.Content[i].Value)
		v := doc.Content[i+1]
		if k == "plugins" && v.Kind == yaml.MappingNode {
			for j := 0; j < len(v.Content); j += 2 {
				pk := strings.ToLower(v.Content[j].Value)
				pv := v.Content[j+1]
				if pk == "configs" && pv.Kind == yaml.MappingNode {
					for m := 0; m < len(pv.Content); m += 2 {
						ck := strings.ToLower(pv.Content[m].Value)
						cv := pv.Content[m+1]
						if (ck == providerName || ck == "workbuddy") && cv.Kind == yaml.MappingNode {
							return cv
						}
					}
				}
			}
		} else if (k == providerName || k == "workbuddy") && v.Kind == yaml.MappingNode {
			return v
		}
	}
	return doc
}

// parseDailyFreeLimits reads the `daily_free_limits` config key, which
// overrides or extends the built-in per-model daily free allowance table.
//
// Accepted shapes (all equivalent, for operator convenience):
//
//	daily_free_limits:
//	  deepseek-v4.1-flash: 200000000
//	  glm-5.2: 50000000
//
//	daily_free_limits: deepseek-v4.1-flash=200000000, glm-5.2=50000000
//
//	daily_free_limits:
//	  - deepseek-v4.1-flash=200000000
//	  - glm-5.2=50000000
//
// A limit of 0 or a negative value removes the model's limit entirely (useful
// for disabling a built-in default that upstream no longer honours).
func parseDailyFreeLimits(configYAML []byte) map[string]int64 {
	if len(configYAML) == 0 {
		return nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(configYAML, &root); err != nil {
		log.Printf("daily quota: config unmarshal failed: %v", err)
		return nil
	}
	cfgMap := findWorkBuddyConfigNode(&root)
	if cfgMap == nil || cfgMap.Kind != yaml.MappingNode {
		return nil
	}

	var node *yaml.Node
	for i := 0; i+1 < len(cfgMap.Content); i += 2 {
		if strings.EqualFold(strings.TrimSpace(cfgMap.Content[i].Value), "daily_free_limits") {
			node = cfgMap.Content[i+1]
			break
		}
	}
	if node == nil {
		return nil
	}

	out := make(map[string]int64)
	record := func(model string, limit int64) {
		model = normalizeModelKey(model)
		if model == "" {
			return
		}
		// Zero/negative removes an override so the built-in default applies
		// again; operators use this to opt out of a stale default.
		if limit <= 0 {
			delete(out, model)
			return
		}
		out[model] = limit
	}

	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			model := strings.TrimSpace(node.Content[i].Value)
			limit, ok := parseTokenLimit(node.Content[i+1].Value)
			if !ok {
				log.Printf("daily quota: ignoring unparseable limit for %q: %q", model, node.Content[i+1].Value)
				continue
			}
			record(model, limit)
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				continue
			}
			model, limit, ok := splitModelLimit(item.Value)
			if !ok {
				log.Printf("daily quota: ignoring malformed entry %q (want model=limit)", item.Value)
				continue
			}
			record(model, limit)
		}
	case yaml.ScalarNode:
		for _, part := range strings.Split(node.Value, ",") {
			model, limit, ok := splitModelLimit(part)
			if !ok {
				continue
			}
			record(model, limit)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitModelLimit parses "model=limit" (also accepting "model: limit").
func splitModelLimit(raw string) (string, int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, false
	}
	sep := "="
	if !strings.Contains(raw, "=") {
		sep = ":"
	}
	parts := strings.SplitN(raw, sep, 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	model := strings.TrimSpace(parts[0])
	if model == "" {
		return "", 0, false
	}
	limit, ok := parseTokenLimit(parts[1])
	if !ok {
		return "", 0, false
	}
	return model, limit, true
}

// parseTokenLimit parses a token count, accepting plain digits, underscores and
// human-friendly suffixes: 200000000, 200_000_000, 200m, 200M, 2e8, 1.5b.
//
// Chinese 亿 (1e8) / 万 (1e4) suffixes are accepted too: this plugin's operators
// read limits like "2亿" (200 million) and would otherwise have the value
// silently ignored — falling back to a default that may coincidentally match,
// which is the worst outcome because nothing looks broken.
func parseTokenLimit(raw string) (int64, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, false
	}
	multiplier := int64(1)
	// Longest suffix first: "亿"/"万" are multi-byte, so HasSuffix is exact.
	switch {
	case strings.HasSuffix(s, "亿"):
		multiplier, s = 100_000_000, strings.TrimSuffix(s, "亿")
	case strings.HasSuffix(s, "万"):
		multiplier, s = 10_000, strings.TrimSuffix(s, "万")
	case strings.HasSuffix(s, "k"):
		multiplier, s = 1_000, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		multiplier, s = 1_000_000, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "b"):
		multiplier, s = 1_000_000_000, strings.TrimSuffix(s, "b")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// Accept scientific notation and decimals (2e8, 1.5b, 2.5亿) via float
	// parsing, then scale. Precision loss is irrelevant at token magnitudes.
	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if value < 0 {
		return 0, true // negative → caller treats as "remove override"
	}
	scaled := value * float64(multiplier)
	if scaled > float64(math.MaxInt64) {
		return 0, false
	}
	return int64(scaled), true
}

func parseCustomStaticModels(configYAML []byte) []customStaticModel {
	if len(configYAML) == 0 {
		return nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(configYAML, &root); err != nil {
		log.Printf("models: custom static config unmarshal failed: %v", err)
		return nil
	}
	cfgMap := findWorkBuddyConfigNode(&root)
	if cfgMap == nil || cfgMap.Kind != yaml.MappingNode {
		return nil
	}

	var out []customStaticModel

	parseList := func(listNode *yaml.Node, defaultChannel string) {
		if listNode == nil || listNode.Kind != yaml.SequenceNode {
			return
		}
		for _, item := range listNode.Content {
			if item.Kind == yaml.ScalarNode {
				id := strings.TrimSpace(item.Value)
				if id == "" {
					continue
				}
				ch := defaultChannel
				if ch == "" {
					ch = "all"
				}
				out = append(out, customStaticModel{
					ID:        id,
					Name:      prettifyModelName(id),
					Channel:   ch,
					Context:   1000000,
					MaxTokens: 8192,
					Enabled:   true,
				})
			} else if item.Kind == yaml.MappingNode {
				var raw customStaticModelConfig
				if err := item.Decode(&raw); err != nil {
					continue
				}
				id := strings.TrimSpace(raw.ID)
				if id == "" {
					continue
				}
				rawCh := strings.TrimSpace(raw.Channel)
				if rawCh == "" {
					rawCh = strings.TrimSpace(raw.Realm)
				}
				var ch string
				if rawCh == "" {
					ch = defaultChannel
					if ch == "" {
						ch = "all"
					}
				} else {
					ch = normalizeChannel(rawCh)
					if ch == "" {
						log.Printf("models: custom static model %q has invalid channel %q; ignored", id, rawCh)
						continue
					}
				}
				if raw.Context < 0 || raw.MaxTokens < 0 {
					continue
				}
				if raw.Enabled != nil && !*raw.Enabled {
					continue
				}
				name := strings.TrimSpace(raw.Name)
				if name == "" {
					name = prettifyModelName(id)
				}
				ctxLen := raw.Context
				if ctxLen <= 0 {
					ctxLen = 1000000
				}
				maxTok := raw.MaxTokens
				if maxTok <= 0 {
					maxTok = 8192
				}
				out = append(out, customStaticModel{
					ID:        id,
					Name:      name,
					Channel:   ch,
					Context:   ctxLen,
					MaxTokens: maxTok,
					Enabled:   true,
				})
			}
		}
	}

	for i := 0; i < len(cfgMap.Content); i += 2 {
		key := strings.ToLower(strings.TrimSpace(cfgMap.Content[i].Value))
		val := cfgMap.Content[i+1]

		if key == "models" {
			if val.Kind == yaml.MappingNode {
				// Grouped channel mapping:
				// models:
				//   intl_global: [deepseek-v4.1-flash, gpt-6-astra]
				//   cn: [...]
				for j := 0; j < len(val.Content); j += 2 {
					groupKey := normalizeChannel(val.Content[j].Value)
					groupVal := val.Content[j+1]
					if groupKey != "" {
						if groupVal.Kind == yaml.ScalarNode {
							for _, id := range parsePinnedModelList(groupVal.Value) {
								out = append(out, customStaticModel{
									ID:        id,
									Name:      prettifyModelName(id),
									Channel:   groupKey,
									Context:   1000000,
									MaxTokens: 8192,
									Enabled:   true,
								})
							}
						} else {
							parseList(groupVal, groupKey)
						}
					}
				}
			} else if val.Kind == yaml.SequenceNode {
				// Flat sequence (backward compatibility):
				// models:
				//   - id: ...
				//     channel: intl_global
				parseList(val, "all")
			}
		} else if key == "models_intl_global" {
			if val.Kind == yaml.ScalarNode {
				for _, id := range parsePinnedModelList(val.Value) {
					out = append(out, customStaticModel{
						ID:        id,
						Name:      prettifyModelName(id),
						Channel:   "intl_global",
						Context:   1000000,
						MaxTokens: 8192,
						Enabled:   true,
					})
				}
			} else if val.Kind == yaml.SequenceNode {
				parseList(val, "intl_global")
			}
		} else if key == "models_cn" && val.Kind == yaml.SequenceNode {
			parseList(val, "cn")
		}
	}
	return out
}

// pinnedModelIDsForRealm snapshots the pinned ID list for a realm (nil when
// the realm has no pins).
func pinnedModelIDsForRealm(realm string) []string {
	pinnedModelsMu.RLock()
	defer pinnedModelsMu.RUnlock()
	ids, ok := pinnedModels[realm]
	if !ok {
		return nil
	}
	return append([]string(nil), ids...)
}

// parseBoolConfig accepts the boolean spellings users actually type in YAML
// (true/True/TRUE, 1, yes/YES, on/On) with optional quotes — foolproof config.
func parseBoolConfig(raw string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(raw), "\"'")) {
	case "true", "1", "yes", "y", "on", "开", "开启":
		return true
	default:
		return false
	}
}

// configure decodes plugin config from the lifecycle request.
func configure(raw []byte) {
	// Parse config without holding any lock (fixes nested-lock hazard).
	nextCheckinAuto := true
	nextLifecycleAuto := true
	nextSchedulerMode := schedulerModeHost // reset to default on reconfigure
	nextKeepaliveAuto := false
	nextLoginPlatform := "CLI"
	nextLoginRegion := regionCN
	nextGrowthAuto := false
	nextTravelAuto := false

	nextPinned := map[string][]string{}
	var nextGloballyDisabledModels []string
	var nextGloballyEnabledModels []string
	var nextCustomModels []customStaticModel
	var nextDailyFreeLimits map[string]int64
	if len(raw) > 0 {
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if err := json.Unmarshal(raw, &req); err == nil {
			nextDailyFreeLimits = parseDailyFreeLimits(req.ConfigYAML)
			nextCustomModels = parseCustomStaticModels(req.ConfigYAML)

			// Single YAML decode for every scalar setting. The previous

			// line-prefix parser silently misread inline comments and quotes
			// (e.g. `checkin_auto: true # 开启` became false, and
			// `login_region: "intl" # 海外` fell back to cn).
			cfg := pluginConfigScalars(req.ConfigYAML)
			nextGloballyDisabledModels = parsePinnedModelList(strings.Join(cfg.listValue("models_disabled"), ","))
			nextGloballyEnabledModels = parsePinnedModelList(strings.Join(cfg.listValue("models_enabled"), ","))

			nextCheckinAuto = cfg.boolValue("checkin_auto", nextCheckinAuto)
			nextLifecycleAuto = cfg.boolValue("lifecycle_auto", nextLifecycleAuto)
			// Synthetic activity reporting is retired; legacy config is ignored.
			nextGrowthAuto = false
			nextTravelAuto = cfg.boolValue("travel_auto", nextTravelAuto)
			nextKeepaliveAuto = cfg.boolValue("token_keepalive", nextKeepaliveAuto)

			if cfg.equalsFold("scheduler_mode", schedulerModeBuiltin, schedulerModeOff) {
				nextSchedulerMode = schedulerModeBuiltin
			}
			if cfg.equalsFold("scheduler_mode", schedulerModeHost) {
				nextSchedulerMode = schedulerModeHost
			}
			if cfg.equalsFold("scheduler_mode", schedulerModeExpiry) {
				nextSchedulerMode = schedulerModeExpiry
			}
			if cfg.equalsFold("scheduler_mode", schedulerModeCredits) {
				nextSchedulerMode = schedulerModeCredits
			}
			if cfg.equalsFold("login_platform", "ide", "ide模式") {
				nextLoginPlatform = "ide"
			}
			if cfg.equalsFold("login_region", "intl", "海外", "国际") {
				nextLoginRegion = regionIntl
			}

			if ids := cfg.listValue("models_cn"); len(ids) > 0 {
				nextPinned[regionCN] = ids
			}
			for _, key := range []string{"models_intl", "models_global", "models_intl_global"} {
				if ids := cfg.listValue(key); len(ids) > 0 {
					nextPinned[regionIntl] = append(nextPinned[regionIntl], ids...)
				}
			}
			nextPinned[regionIntl] = parsePinnedModelList(strings.Join(nextPinned[regionIntl], ","))

		}
	}

	// Apply each setting under its own lock — no nesting.
	checkinAutoMu.Lock()
	checkinAuto = nextCheckinAuto
	checkinAutoMu.Unlock()

	loginPlatformMu.Lock()
	loginPlatform = nextLoginPlatform
	loginPlatformMu.Unlock()

	loginRegionMu.Lock()
	loginRegion = nextLoginRegion
	loginRegionMu.Unlock()

	lifecycleAutoMu.Lock()
	lifecycleAuto = nextLifecycleAuto
	lifecycleAutoMu.Unlock()

	schedulerModeMu.Lock()
	schedulerMode = nextSchedulerMode
	schedulerModeMu.Unlock()

	// Daily free-allowance table: built-in defaults overlaid with the
	// operator's daily_free_limits. Applied on every reconfigure so removing
	// an override reverts to the default rather than sticking.
	setDailyFreeLimits(nextDailyFreeLimits)

	keepaliveAutoMu.Lock()
	keepaliveAuto = nextKeepaliveAuto
	keepaliveAutoMu.Unlock()

	growthAutoMu.Lock()
	growthAuto = nextGrowthAuto
	growthAutoMu.Unlock()

	travelAutoMu.Lock()
	travelAuto = nextTravelAuto
	travelAutoMu.Unlock()

	pinnedModelsMu.Lock()
	pinnedModels = nextPinned
	pinnedModelsMu.Unlock()

	customStaticModels.Lock()
	customStaticModels.models = nextCustomModels
	customStaticModels.Unlock()

	globalModelSettingsMu.Lock()
	globallyDisabledModels = nextGloballyDisabledModels
	globallyEnabledModels = nextGloballyEnabledModels
	globalModelSettingsMu.Unlock()

	ensureScheduler()
	// Migrate legacy codebuddy-cn auth files (merged plugin, v0.9.0).
	startAdoption()

	// One-line config summary for self-diagnosis: every effective setting at a
	// glance, key material never included. Defaults shown so a zero-config
	// deployment is visibly "everything on, nothing required". The host calls
	// register/reconfigure several times per boot with identical payloads, so
	// only log when the summary actually changes.
	//
	// usage_report is always "host": CPA reports plugin usage itself (see
	// usage.go) — there is no plugin-side push anymore.
	reportState := "host (由 CPA 上报)"
	summary := fmt.Sprintf("checkin_auto=%t lifecycle_auto=%t keepalive=%t growth_auto=%t travel_auto=%t region=%s platform=%s scheduler=%s usage_report=%s",
		nextCheckinAuto, nextLifecycleAuto, nextKeepaliveAuto, nextGrowthAuto, nextTravelAuto, nextLoginRegion, nextLoginPlatform, nextSchedulerMode, reportState)
	configSummaryMu.Lock()
	changed := summary != lastConfigSummary
	lastConfigSummary = summary
	configSummaryMu.Unlock()
	if changed {
		log.Printf("workbuddy: config %s", summary)
	}
}

var (
	configSummaryMu   sync.Mutex
	lastConfigSummary string
)

// currentLoginPlatform returns the configured platform for NEW logins.
func currentLoginPlatform() string {
	loginPlatformMu.RLock()
	defer loginPlatformMu.RUnlock()
	if p := strings.TrimSpace(loginPlatform); p != "" {
		return p
	}
	return "CLI"
}

// loadedLoginRegion returns the configured realm for NEW logins.
func loadedLoginRegion() string {
	loginRegionMu.RLock()
	defer loginRegionMu.RUnlock()
	return loginRegion
}

// platformForAuth returns the login platform recorded for an existing
// account. Legacy files without the field default to CLI-style headers.
func platformForAuth(sa *storedAuth) string {
	if sa != nil {
		if p := strings.TrimSpace(sa.Auth.LoginPlatform); p != "" {
			return p
		}
	}
	return "CLI"
}

// applyPlatformHeaders sets the CodeBuddy IDE client headers for ide-
// platform tokens (parity with the former codebuddy-cn plugin). CLI
// tokens keep the historical workbuddy header set (no X-IDE-*).
func applyPlatformHeaders(req *http.Request, platform string) {
	if strings.EqualFold(strings.TrimSpace(platform), "ide") {
		req.Header.Set("X-IDE-Type", "CodeBuddyIDE")
		req.Header.Set("X-IDE-Name", "CodeBuddyIDE")
		req.Header.Set("X-IDE-Version", "4.9.7")
		req.Header.Set("X-Product-Version", "4.9.7")
	}
}
