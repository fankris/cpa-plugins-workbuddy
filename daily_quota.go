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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	// dailyQuotaPath is the legacy single-file location. It is only read once,
	// to migrate an older build's data into per-account shards.
	dailyQuotaPath string
	// dailyQuotaUsageDir holds one file per account. Writing a shard costs one
	// small file instead of re-serialising every account on every request.
	dailyQuotaUsageDir string
	// dailyQuotaDirty names the accounts whose counters changed since the last
	// flush. Guarded by dailyQuotaMu.
	dailyQuotaDirty = map[string]struct{}{}
	// dailyQuotaFlush wakes the single writer goroutine. Buffered so recording a
	// usage never blocks on the flusher.
	dailyQuotaFlush = make(chan struct{}, 1)
	// dailyQuotaFlusherOnce starts the writer goroutine exactly once.
	dailyQuotaFlusherOnce sync.Once
	// dailyQuotaFlushInterval bounds how long a counter can sit only in memory.
	// A crash can therefore lose at most this window, which is acceptable for a
	// usage display that is recomputed from live traffic.
	dailyQuotaFlushInterval = 5 * time.Second
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

// Plugin-owned state. The host exposes only Host.AuthDir, and writing plugin
// state there made CPA synthesise a credential from the file on every rescan
// ("skipping auth file ...") and list it among the credentials. The state now
// lives in the plugin's own directory; the auth dir is touched once, only to
// carry an existing file over and delete it.
var (
	dailyQuotaPersist   = true
	dailyQuotaStateRoot string
)

// setDailyQuotaPersist toggles on-disk persistence. Off means the counters live
// in memory only: nothing is created anywhere, at the cost of a restart
// resetting the day's measured usage.
func setDailyQuotaPersist(enabled bool) {
	dailyQuotaMu.Lock()
	dailyQuotaPersist = enabled
	if !enabled {
		dailyQuotaPath = ""
	}
	dailyQuotaMu.Unlock()
}

// setDailyQuotaStateRoot overrides where the state file lives. An empty value
// restores the default (the plugin's own cache directory).
func setDailyQuotaStateRoot(dir string) {
	dailyQuotaMu.Lock()
	dailyQuotaStateRoot = strings.TrimSpace(dir)
	dailyQuotaMu.Unlock()
}

// dailyQuotaStateDir resolves the plugin-owned directory for its own state.
// It never returns the host auth dir: that directory is scanned for *.json and
// is not a place for a plugin to keep bookkeeping.
func dailyQuotaStateDir() string {
	dailyQuotaMu.Lock()
	override := dailyQuotaStateRoot
	dailyQuotaMu.Unlock()
	if override != "" {
		return override
	}
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		return ""
	}
	return filepath.Join(base, "cpa-workbuddy")
}

// setDailyQuotaPath is called from the model-discovery handlers, which are the
// only RPCs carrying Host.AuthDir. The auth dir is used solely to migrate a
// file left there by an older build; it is never written to.
func setDailyQuotaPath(authDir string) {
	authDir = strings.TrimSpace(authDir)

	dailyQuotaMu.Lock()
	persist := dailyQuotaPersist
	dailyQuotaMu.Unlock()
	if !persist {
		return
	}
	dir := dailyQuotaStateDir()
	if dir == "" {
		// No writable location: stay in memory rather than guessing a path.
		return
	}
	usageDir := filepath.Join(dir, "usage")
	legacyPath := filepath.Join(dir, dailyQuotaFileName)

	dailyQuotaMu.Lock()
	if dailyQuotaUsageDir == usageDir {
		dailyQuotaMu.Unlock()
		return
	}
	dailyQuotaUsageDir = usageDir
	dailyQuotaPath = legacyPath
	dailyQuotaMu.Unlock()

	if authDir != "" {
		migrateLegacyDailyQuotaFiles(authDir, legacyPath)
	}
	loadDailyQuotaState(usageDir, legacyPath)
	startDailyQuotaFlusher()
}

// migrateLegacyDailyQuotaFiles carries state out of the host auth dir and
// removes the file from it. Both the pre-1.0.65 ".json" name and the 1.0.65
// ".state" name are handled, because the auth dir must end up clean either way.
// A file is only removed once its content is safely at the new location.
func migrateLegacyDailyQuotaFiles(authDir, path string) {
	for _, name := range []string{legacyDailyQuotaFileName, dailyQuotaFileName} {
		legacy := filepath.Join(authDir, name)
		// Never treat the destination as a source: when an operator points
		// daily_quota_state_dir at the auth dir, the second pass would
		// otherwise delete the file the first pass just wrote.
		if filepath.Clean(legacy) == filepath.Clean(path) {
			continue
		}
		raw, errRead := os.ReadFile(legacy)
		if errRead != nil {
			continue
		}
		if _, errStat := os.Stat(path); errStat == nil {
			// The destination already holds state. MERGE rather than drop: the
			// auth-dir copy can carry counters the destination does not, and
			// deleting it outright lost a whole account's usage.
			if mergeQuotaStateFiles(path, legacy) {
				_ = os.Remove(legacy)
			}
			continue
		}
		if errWrite := writeFileAtomic(path, raw); errWrite != nil {
			// Keep the auth-dir copy: dropping it would lose the counters.
			continue
		}
		_ = os.Remove(legacy)
	}
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
	// Mark the account dirty instead of writing here: the previous code
	// re-serialised EVERY account on EVERY request, and because the snapshot was
	// taken under the lock but written outside it, two concurrent requests could
	// rename their temp files out of order and an older snapshot could win.
	dailyQuotaDirty[authID] = struct{}{}
	dailyQuotaMu.Unlock()

	startDailyQuotaFlusher()
	select {
	case dailyQuotaFlush <- struct{}{}:
	default: // a flush is already pending; it will pick this change up
	}
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

// startDailyQuotaFlusher starts the single writer goroutine. One goroutine
// performs every write, so flushes are serialised and no two writers can rename
// their temp files out of order.
func startDailyQuotaFlusher() {
	dailyQuotaFlusherOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(dailyQuotaFlushInterval)
			defer ticker.Stop()
			ctx := pluginContext()
			for {
				select {
				case <-ctx.Done():
					flushDailyQuota()
					return
				case <-dailyQuotaFlush:
				case <-ticker.C:
				}
				flushDailyQuota()
			}
		}()
	})
}

// flushDailyQuota writes one small file per dirty account and clears the dirty
// set. A failed write re-marks its account so the next tick retries rather than
// dropping the counters.
func flushDailyQuota() {
	dailyQuotaMu.Lock()
	if len(dailyQuotaDirty) == 0 || dailyQuotaUsageDir == "" {
		dailyQuotaMu.Unlock()
		return
	}
	dirty := dailyQuotaDirty
	dailyQuotaDirty = map[string]struct{}{}
	dir := dailyQuotaUsageDir
	byAuth := make(map[string][]dailyQuotaFileEntry, len(dirty))
	for key, usage := range dailyQuotaStore {
		if _, ok := dirty[key.AuthID]; !ok {
			continue
		}
		byAuth[key.AuthID] = append(byAuth[key.AuthID], dailyQuotaFileEntry{
			AuthID: key.AuthID, Model: key.Model, Day: key.Day, dailyUsage: *usage,
		})
	}
	dailyQuotaMu.Unlock()

	for authID, entries := range byAuth {
		// Stable ordering keeps each shard diff-friendly and tests deterministic.
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Day != entries[j].Day {
				return entries[i].Day < entries[j].Day
			}
			return entries[i].Model < entries[j].Model
		})
		raw, errMarshal := json.Marshal(dailyQuotaFile{Version: 1, Usage: entries})
		if errMarshal != nil {
			continue
		}
		if errWrite := writeFileAtomic(filepath.Join(dir, quotaShardName(authID)), raw); errWrite != nil {
			dailyQuotaMu.Lock()
			dailyQuotaDirty[authID] = struct{}{}
			dailyQuotaMu.Unlock()
		}
	}
}

// flushDailyQuotaSync is the shutdown path: counters must not be left only in
// memory when the process is about to end.
func flushDailyQuotaSync() {
	flushDailyQuota()
}

// mergeQuotaStateFiles folds src's counters into dst and rewrites dst. Counters
// are summed by (auth, model, day), so a copy left in the auth dir by an older
// build is absorbed instead of discarded. It reports whether dst now holds src's
// data, which is what lets the caller delete src safely.
func mergeQuotaStateFiles(dst, src string) bool {
	srcRaw, errSrc := os.ReadFile(src)
	if errSrc != nil {
		return false
	}
	var srcPayload dailyQuotaFile
	if json.Unmarshal(srcRaw, &srcPayload) != nil {
		// Unreadable content: keep the file so an operator can inspect it.
		return false
	}
	if len(srcPayload.Usage) == 0 {
		// Valid but empty: nothing to lose, so the copy may go.
		return true
	}
	var dstPayload dailyQuotaFile
	if dstRaw, errDst := os.ReadFile(dst); errDst == nil {
		_ = json.Unmarshal(dstRaw, &dstPayload)
	}
	index := make(map[string]int, len(dstPayload.Usage))
	for i, entry := range dstPayload.Usage {
		index[quotaEntryKey(entry)] = i
	}
	absorbed := false
	for _, entry := range srcPayload.Usage {
		if entry.AuthID == "" || entry.Model == "" || entry.Day == "" {
			continue
		}
		key := quotaEntryKey(entry)
		if i, ok := index[key]; ok {
			dstPayload.Usage[i].InputTokens += entry.InputTokens
			dstPayload.Usage[i].OutputTokens += entry.OutputTokens
			dstPayload.Usage[i].TotalTokens += entry.TotalTokens
			dstPayload.Usage[i].Requests += entry.Requests
			dstPayload.Usage[i].Failed += entry.Failed
		} else {
			index[key] = len(dstPayload.Usage)
			dstPayload.Usage = append(dstPayload.Usage, entry)
		}
		absorbed = true
	}
	if !absorbed {
		// Every entry was unusable; keep the file rather than silently drop it.
		return false
	}
	dstPayload.Version = 1
	raw, errMarshal := json.Marshal(dstPayload)
	if errMarshal != nil {
		return false
	}
	return writeFileAtomic(dst, raw) == nil
}

func quotaEntryKey(entry dailyQuotaFileEntry) string {
	return entry.AuthID + "\x00" + normalizeModelKey(entry.Model) + "\x00" + entry.Day
}

// quotaShardName maps an auth ID to a shard file name. An already-safe ID is
// used verbatim so the file is recognisable; anything else is sanitised and
// suffixed with a hash so two different IDs can never collide onto one file.
func quotaShardName(authID string) string {
	if quotaSafeShardName(authID) {
		// Auth IDs are often already file names (workbuddy-<uid>.json); do not
		// double the extension.
		if strings.HasSuffix(strings.ToLower(authID), ".json") {
			return authID
		}
		return authID + ".json"
	}
	sum := sha256.Sum256([]byte(authID))
	return fmt.Sprintf("%s-%s.json", sanitizeShardStem(authID), hex.EncodeToString(sum[:4]))
}

func quotaSafeShardName(authID string) bool {
	if authID == "" || len(authID) > 120 || strings.Contains(authID, "..") {
		return false
	}
	for _, r := range authID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func sanitizeShardStem(authID string) string {
	var b strings.Builder
	for _, r := range authID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 48 {
			break
		}
	}
	if b.Len() == 0 {
		return "account"
	}
	return b.String()
}

// writeFileAtomic writes raw to path via a temp file in the same directory.
// It reports failure so a caller that is about to discard another copy of the
// data (the legacy-file migration) can keep it instead of losing it.
func writeFileAtomic(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, dailyQuotaFileName+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName) // no-op once the rename succeeded
	}()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// 0600: the file holds per-credential usage counts, not secrets, but it
	// lives beside auth material and there is no reason for it to be readable.
	_ = os.Chmod(tmpName, 0o600)
	return os.Rename(tmpName, path)
}

// loadDailyQuotaFile merges persisted counters into the in-memory store.
// Counters are summed rather than replaced: a reload during the same day should
// not lose what the previous process measured.
// loadDailyQuotaState restores counters from the per-account shards, then
// absorbs the legacy single file (splitting it into shards) and removes it.
func loadDailyQuotaState(usageDir, legacyPath string) {
	if entries, errRead := os.ReadDir(usageDir); errRead == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
				continue
			}
			raw, errFile := os.ReadFile(filepath.Join(usageDir, entry.Name()))
			if errFile != nil {
				continue
			}
			mergeDailyQuotaPayload(raw)
		}
	}
	raw, errLegacy := os.ReadFile(legacyPath)
	if errLegacy != nil {
		return
	}
	if !mergeDailyQuotaPayload(raw) {
		return
	}
	// The legacy file's counters now live in memory; mark them dirty so the next
	// flush writes shards, and only then drop the old file.
	dailyQuotaMu.Lock()
	for key := range dailyQuotaStore {
		dailyQuotaDirty[key.AuthID] = struct{}{}
	}
	dailyQuotaMu.Unlock()
	flushDailyQuota()
	_ = os.Remove(legacyPath)
}

// mergeDailyQuotaPayload folds one state payload into the in-memory store.
// Counters are summed rather than replaced: a reload during the same day must
// not lose what the previous process measured. It reports whether any usable
// entry was merged, so the caller can tell real data from a corrupt file.
func mergeDailyQuotaPayload(raw []byte) bool {
	var payload dailyQuotaFile
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	today := dailyQuotaNow().Local().Format("2006-01-02")
	cutoff := today
	if t, err := time.ParseInLocation("2006-01-02", today, time.Local); err == nil {
		cutoff = t.AddDate(0, 0, -dailyQuotaRetainDays).Format("2006-01-02")
	}

	merged := false
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
		merged = true
	}
	return merged
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
		if authID == "" || key.AuthID == authID {
			dailyQuotaDirty[key.AuthID] = struct{}{}
		}
	}
	dailyQuotaMu.Unlock()

	flushDailyQuota()
}
