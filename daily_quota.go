// daily_quota.go tracks the per-model DAILY FREE allowance.
//
// This is a different thing from the paid credits reported by quota.go, and
// conflating the two would be wrong:
//
//	               paid credits              daily free allowance
//	nature         purchased quota           free tier
//	dimension      account-wide total        PER MODEL
//	resets         package cycle             every day
//	source         upstream billing API      measured locally (no upstream API)
//
// Upstream CodeBuddy/WorkBuddy publishes no per-model quota endpoint, so the
// "used today" figure cannot be fetched — it has to be measured. The host
// delivers one UsageRecord per completed request to plugins that declare the
// UsagePlugin capability, so this file accumulates those records into
// (auth, model, day) buckets.
//
// Deliberate design choices:
//
//   - Display only. Exceeding an allowance does NOT make this plugin reject the
//     request: the upstream remains the authority on its own free tier, and a
//     local counter that drifted (restart, missed record, clock skew) must
//     never be able to block a paying request. The panel shows the percentage;
//     the upstream's own rejection is what actually stops traffic.
//   - Day boundary is the LOCAL calendar day of the host, matching how a user
//     reads "today". Upstream reset semantics are not documented per model.
//   - Counters live in memory plus a small JSON file, so a plugin reload does
//     not zero out the day's usage (a zeroed counter would show a misleading
//     100% remaining).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Default daily free allowances, in tokens, keyed by upstream model ID.
//
// These are the free-tier caps WorkBuddy/CodeBuddy advertise for the models
// that are free to call. Models absent from this table are treated as
// "no free allowance known" and are shown without a limit rather than with an
// invented one — guessing a limit would produce a confidently wrong percentage.
//
// Operators override or extend this table through the `daily_free_limits`
// config key (see parseDailyFreeLimits), which is the supported way to keep up
// with upstream changes without a plugin release.
// defaultDailyFreeLimits lists the models with a KNOWN daily free allowance.
//
// Only models that verifiably have a free tier belong here. Listing one that
// does not is worse than omitting it: the panel would render a limit AND a
// percentage for a budget that does not exist, making a model that is actually
// burning paid credits look like it is consuming a free tier.
//
// hy4-preview was removed in 0.9.36: its free window (14 days from the
// 2026-08-28 launch) has passed, so advertising "2亿/day remaining" for it was
// misleading. It is NOT blacklisted — the plugin still measures and displays
// its usage, just without a percentage. Re-add it here if the window is
// confirmed open again, or override via daily_free_limits.
var defaultDailyFreeLimits = map[string]int64{
	// DeepSeek V4.1 Flash: 200M tokens/day free during the launch window.
	"deepseek-v4.1-flash": 200_000_000,
}

const (
	// dailyQuotaFileName is the persistence file under the host auth dir.
	//
	// It deliberately does NOT end in ".json". The host scans the auth dir for
	// *.json and tries to synthesise a credential from every match, so the old
	// .json name made CPA log "skipping auth file workbuddy-daily-quota.json" on
	// every rescan and list the file as an auth entry with an empty type.
	dailyQuotaFileName = "workbuddy-daily-quota.state"
	// legacyDailyQuotaFileName is the pre-1.0.65 name. It is read once, rewritten
	// under the new name and removed, so an existing auth dir stops showing it.
	legacyDailyQuotaFileName = "workbuddy-daily-quota.json"
	// dailyQuotaRetainDays bounds how many past days are kept on disk. Only
	// today's bucket is displayed; older ones are kept briefly so a request
	// straddling midnight still finds its bucket, then pruned.
	dailyQuotaRetainDays = 3
)

// dailyQuotaKey identifies one counter bucket. Auth is part of the key because
// free allowances are per credential: two accounts have independent budgets.
type dailyQuotaKey struct {
	AuthID string
	Model  string
	Day    string // YYYY-MM-DD, host-local
}

// dailyUsage is one accumulated bucket.
type dailyUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	Requests     int64 `json:"requests"`
	Failed       int64 `json:"failed_requests"`
}

var (
	dailyQuotaMu    sync.Mutex
	dailyQuotaStore = map[dailyQuotaKey]*dailyUsage{}
	// dailyQuotaLimits is the effective table: defaults overlaid with operator
	// config. Guarded by dailyQuotaMu.
	dailyQuotaLimits = map[string]int64{}
	// dailyQuotaOverridden records which models came from config rather than
	// the built-in table. Needed because a model can appear in BOTH: the
	// effective value would be right but a naive "is it in defaults?" check
	// would label an operator's override as "default".
	dailyQuotaOverridden = map[string]struct{}{}
	// dailyQuotaPath is where the store is persisted; empty disables persistence
	// (tests, or a host that never reported an auth dir).
	dailyQuotaPath string
	// dailyQuotaNow is a test seam for the day boundary.
	dailyQuotaNow = time.Now
)

func init() {
	for model, limit := range defaultDailyFreeLimits {
		dailyQuotaLimits[model] = limit
	}
}

// setDailyFreeLimits replaces the configured overrides. Called from configure()
// on every reconfigure, so it must be safe to call with nil (which reverts to
// the built-in defaults).
func setDailyFreeLimits(overrides map[string]int64) {
	dailyQuotaMu.Lock()
	defer dailyQuotaMu.Unlock()
	dailyQuotaLimits = make(map[string]int64, len(defaultDailyFreeLimits)+len(overrides))
	dailyQuotaOverridden = make(map[string]struct{}, len(overrides))
	for model, limit := range defaultDailyFreeLimits {
		dailyQuotaLimits[model] = limit
	}
	for model, limit := range overrides {
		model = normalizeModelKey(model)
		if model == "" || limit <= 0 {
			continue
		}
		dailyQuotaLimits[model] = limit
		dailyQuotaOverridden[model] = struct{}{}
	}
}

// setDailyQuotaPath points persistence at the host auth dir. Called from the
// model-discovery handlers, which are the only RPCs carrying Host.AuthDir; an
// empty dir disables persistence rather than writing to the process working
// directory (the plugin has no other way to learn a writable location).
func setDailyQuotaPath(authDir string) {
	authDir = strings.TrimSpace(authDir)
	if authDir == "" {
		return
	}
	path := filepath.Join(authDir, dailyQuotaFileName)

	dailyQuotaMu.Lock()
	if dailyQuotaPath == path {
		dailyQuotaMu.Unlock()
		return
	}
	dailyQuotaPath = path
	dailyQuotaMu.Unlock()

	migrateLegacyDailyQuotaFile(authDir, path)
	loadDailyQuotaFile(path)
}

// migrateLegacyDailyQuotaFile moves the pre-1.0.65 ".json" state file onto the
// new extension. Leaving it in place kept the host's auth-dir scan logging
// "skipping auth file" and listing a typeless entry among the credentials.
func migrateLegacyDailyQuotaFile(authDir, path string) {
	legacy := filepath.Join(authDir, legacyDailyQuotaFileName)
	if _, errStat := os.Stat(legacy); errStat != nil {
		return
	}
	if _, errStat := os.Stat(path); errStat == nil {
		// The new file already exists, so the legacy one is stale leftover.
		_ = os.Remove(legacy)
		return
	}
	raw, errRead := os.ReadFile(legacy)
	if errRead != nil {
		return
	}
	writeFileAtomic(path, raw)
	_ = os.Remove(legacy)
}

// normalizeModelKey lowercases and trims a model ID so config keys and upstream
// IDs match regardless of how either side spells them.
func normalizeModelKey(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// handleUsageRecord is the usage.handle RPC entry point. The host sends one
// record per completed request; this only accumulates counters and never
// returns an error, because failing here would surface as a plugin fault for a
// purely observational feature.
func handleUsageRecord(raw []byte) ([]byte, error) {
	var record pluginapi.UsageRecord
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &record); err != nil {
			// Malformed record: acknowledge and drop. Returning an error would
			// make the host mark the plugin as failing for a request that
			// already succeeded upstream.
			return okEnvelope(map[string]any{})
		}
	}
	recordDailyUsage(record)
	return okEnvelope(map[string]any{})
}

// recordDailyUsage folds one usage record into the store.
//
// Only records that belong to THIS provider are counted: the host may deliver
// records for other providers, and counting those would inflate WorkBuddy's
// free-tier usage with unrelated traffic.
func recordDailyUsage(record pluginapi.UsageRecord) {
	if !isWorkBuddyUsage(record) {
		return
	}
	model := normalizeModelKey(record.Model)
	if model == "" {
		model = normalizeModelKey(record.ResponseModel)
	}
	if model == "" {
		return
	}
	authID := strings.TrimSpace(record.AuthID)
	if authID == "" {
		authID = strings.TrimSpace(record.AuthIndex)
	}
	if authID == "" {
		return
	}

	// TotalTokens is authoritative when present; otherwise sum the parts. Some
	// upstreams report only prompt/completion, and a record with a total of 0
	// but non-zero parts must not be recorded as a zero-token request.
	total := record.Detail.TotalTokens
	if total <= 0 {
		total = record.Detail.InputTokens + record.Detail.OutputTokens
	}
	if total < 0 {
		total = 0
	}

	when := record.RequestedAt
	if when.IsZero() {
		when = dailyQuotaNow()
	}
	key := dailyQuotaKey{AuthID: authID, Model: model, Day: when.Local().Format("2006-01-02")}

	dailyQuotaMu.Lock()
	usage := dailyQuotaStore[key]
	if usage == nil {
		usage = &dailyUsage{}
		dailyQuotaStore[key] = usage
	}
	usage.InputTokens += record.Detail.InputTokens
	usage.OutputTokens += record.Detail.OutputTokens
	usage.TotalTokens += total
	usage.Requests++
	if record.Failed {
		usage.Failed++
	}
	pruneDailyQuotaLocked(key.Day)
	path := dailyQuotaPath
	dailyQuotaMu.Unlock()

	persistDailyQuota(path)
}

// isWorkBuddyUsage reports whether a usage record came from this provider.
// A missing provider is ambiguous because model IDs can overlap across
// providers, so fail closed rather than creating unrelated WorkBuddy quota rows.
func isWorkBuddyUsage(record pluginapi.UsageRecord) bool {
	provider := normalizeModelKey(record.Provider)
	switch provider {
	case providerName, "codebuddy", "workbuddy-cn", "workbuddy-global", "workbuddy-intl",
		"codebuddy-cn", "codebuddy-intl":
		return true
	default:
		return false
	}
}

// pruneDailyQuotaLocked drops buckets older than the retention window. Caller
// must hold dailyQuotaMu.
func pruneDailyQuotaLocked(today string) {
	if len(dailyQuotaStore) == 0 {
		return
	}
	cutoff := today
	if t, err := time.ParseInLocation("2006-01-02", today, time.Local); err == nil {
		cutoff = t.AddDate(0, 0, -dailyQuotaRetainDays).Format("2006-01-02")
	}
	for key := range dailyQuotaStore {
		if key.Day < cutoff {
			delete(dailyQuotaStore, key)
		}
	}
}

// ---------------------------------------------------------------------------
// persistence
// ---------------------------------------------------------------------------

type dailyQuotaFile struct {
	Version int                          `json:"version"`
	Limits  map[string]int64             `json:"limits,omitempty"`
	Days    map[string]map[string]string `json:"days"`
	Usage   []dailyQuotaFileEntry        `json:"usage"`
}

type dailyQuotaFileEntry struct {
	AuthID string `json:"auth_id"`
	Model  string `json:"model"`
	Day    string `json:"day"`
	dailyUsage
}

// persistDailyQuota writes the store atomically (temp file + rename) so a crash
// mid-write cannot leave a truncated JSON file that fails to load and silently
// zeroes the day's counters.
func persistDailyQuota(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	dailyQuotaMu.Lock()
	entries := make([]dailyQuotaFileEntry, 0, len(dailyQuotaStore))
	for key, usage := range dailyQuotaStore {
		entries = append(entries, dailyQuotaFileEntry{
			AuthID: key.AuthID, Model: key.Model, Day: key.Day, dailyUsage: *usage,
		})
	}
	dailyQuotaMu.Unlock()

	// Stable ordering keeps the file diff-friendly and makes tests deterministic.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Day != entries[j].Day {
			return entries[i].Day < entries[j].Day
		}
		if entries[i].AuthID != entries[j].AuthID {
			return entries[i].AuthID < entries[j].AuthID
		}
		return entries[i].Model < entries[j].Model
	})

	payload := dailyQuotaFile{Version: 1, Usage: entries}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	writeFileAtomic(path, raw)
}

// writeFileAtomic writes raw to path via a temp file in the same directory.
func writeFileAtomic(path string, raw []byte) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, dailyQuotaFileName+".tmp*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName) // no-op once the rename succeeded
	}()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	// 0600: the file holds per-credential usage counts, not secrets, but it
	// lives beside auth material and there is no reason for it to be readable.
	_ = os.Chmod(tmpName, 0o600)
	_ = os.Rename(tmpName, path)
}

// loadDailyQuotaFile merges persisted counters into the in-memory store.
// Counters are summed rather than replaced: a reload during the same day should
// not lose what the previous process measured.
func loadDailyQuotaFile(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var payload dailyQuotaFile
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	today := dailyQuotaNow().Local().Format("2006-01-02")
	cutoff := today
	if t, err := time.ParseInLocation("2006-01-02", today, time.Local); err == nil {
		cutoff = t.AddDate(0, 0, -dailyQuotaRetainDays).Format("2006-01-02")
	}

	dailyQuotaMu.Lock()
	defer dailyQuotaMu.Unlock()
	for _, entry := range payload.Usage {
		if entry.Day == "" || entry.Day < cutoff || entry.AuthID == "" || entry.Model == "" {
			continue
		}
		key := dailyQuotaKey{AuthID: entry.AuthID, Model: normalizeModelKey(entry.Model), Day: entry.Day}
		usage := dailyQuotaStore[key]
		if usage == nil {
			usage = &dailyUsage{}
			dailyQuotaStore[key] = usage
		}
		usage.InputTokens += entry.InputTokens
		usage.OutputTokens += entry.OutputTokens
		usage.TotalTokens += entry.TotalTokens
		usage.Requests += entry.Requests
		usage.Failed += entry.Failed
	}
}

// ---------------------------------------------------------------------------
// read model for the panel
// ---------------------------------------------------------------------------

// modelDailyQuota is the per-model view rendered by the panel.
type modelDailyQuota struct {
	Model        string `json:"model"`
	Limit        int64  `json:"limit"`
	Used         int64  `json:"used"`
	Remaining    int64  `json:"remaining"`
	UsedPercent  int    `json:"used_percent"`
	Requests     int64  `json:"requests"`
	Failed       int64  `json:"failed_requests"`
	HasLimit     bool   `json:"has_limit"`
	LimitSource  string `json:"limit_source,omitempty"` // "default" | "config"
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// dailyQuotaSnapshotForAuth returns today's per-model counters for one account,
// sorted by usage descending so the models actually in use appear first.
//
// Models with a known limit but no usage yet are included with zero usage: the
// panel should show "0 / 200,000,000" for an available free model rather than
// hide it, because the absence of a row is indistinguishable from the model
// not being free.
func dailyQuotaSnapshotForAuth(authID string) []modelDailyQuota {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil
	}
	day := dailyQuotaNow().Local().Format("2006-01-02")

	dailyQuotaMu.Lock()
	usageByModel := make(map[string]dailyUsage, 8)
	for key, usage := range dailyQuotaStore {
		if key.AuthID != authID || key.Day != day {
			continue
		}
		usageByModel[key.Model] = *usage
	}
	limits := make(map[string]int64, len(dailyQuotaLimits))
	for model, limit := range dailyQuotaLimits {
		limits[model] = limit
	}
	overridden := make(map[string]struct{}, len(dailyQuotaOverridden))
	for model := range dailyQuotaOverridden {
		overridden[model] = struct{}{}
	}
	dailyQuotaMu.Unlock()

	out := make([]modelDailyQuota, 0, len(limits)+len(usageByModel))
	seen := make(map[string]struct{}, len(limits))
	for model, limit := range limits {
		usage := usageByModel[model]
		_, fromConfig := overridden[model]
		out = append(out, buildModelDailyQuota(model, limit, true, fromConfig, usage))
		seen[model] = struct{}{}
	}
	for model, usage := range usageByModel {
		if _, ok := seen[model]; ok {
			continue
		}
		// Observed usage for a model with no configured limit: report the
		// measurement without inventing a cap.
		out = append(out, buildModelDailyQuota(model, 0, false, false, usage))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Used != out[j].Used {
			return out[i].Used > out[j].Used
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func buildModelDailyQuota(model string, limit int64, hasLimit, fromConfig bool, usage dailyUsage) modelDailyQuota {
	entry := modelDailyQuota{
		Model:        model,
		Limit:        limit,
		Used:         usage.TotalTokens,
		Requests:     usage.Requests,
		Failed:       usage.Failed,
		HasLimit:     hasLimit,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
	}
	// fromConfig is passed in rather than derived from defaultDailyFreeLimits:
	// a model can be in BOTH tables (operator tuned a built-in default), and in
	// that case the effective value came from config, so the label must say so.
	if hasLimit {
		if fromConfig {
			entry.LimitSource = "config"
		} else {
			entry.LimitSource = "default"
		}
	}
	if hasLimit && limit > 0 {
		remaining := limit - usage.TotalTokens
		if remaining < 0 {
			remaining = 0
		}
		entry.Remaining = remaining
		percent := int(usage.TotalTokens * 100 / limit)
		if percent > 100 {
			percent = 100
		}
		if percent < 0 {
			percent = 0
		}
		entry.UsedPercent = percent
	}
	return entry
}

// resetDailyQuota clears counters for one account (all models) or every
// account. Exposed for the panel's manual reset; the plugin never resets on its
// own, because a locally-cleared counter would under-report real upstream usage.
func resetDailyQuota(authID string) {
	authID = strings.TrimSpace(authID)
	day := dailyQuotaNow().Local().Format("2006-01-02")

	dailyQuotaMu.Lock()
	for key := range dailyQuotaStore {
		if key.Day != day {
			continue
		}
		if authID != "" && key.AuthID != authID {
			continue
		}
		delete(dailyQuotaStore, key)
	}
	path := dailyQuotaPath
	dailyQuotaMu.Unlock()

	persistDailyQuota(path)
}
