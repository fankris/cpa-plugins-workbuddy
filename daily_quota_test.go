package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// ---------------------------------------------------------------------------
// 每日免费额度：本地统计契约
//
// 上游不提供每模型额度接口，所以「今日已用」只能由插件按宿主上报的用量记录
// 累加得到。这组测试锁定三件事：累加正确、按账号/模型/日期隔离、绝不为 0
// 或负数（面板把负数当作未知会显示成「剩余 0」）。
// ---------------------------------------------------------------------------

// resetDailyQuotaState clears global state so tests do not leak into each other.
func resetDailyQuotaState(t *testing.T) {
	t.Helper()
	dailyQuotaMu.Lock()
	dailyQuotaStore = map[dailyQuotaKey]*dailyUsage{}
	path := dailyQuotaPath
	dailyQuotaPath = ""
	dailyQuotaMu.Unlock()
	setDailyFreeLimits(nil)
	t.Cleanup(func() {
		dailyQuotaMu.Lock()
		dailyQuotaStore = map[dailyQuotaKey]*dailyUsage{}
		dailyQuotaPath = path
		dailyQuotaMu.Unlock()
		setDailyFreeLimits(nil)
	})
}

func usageRecord(authID, model string, total int64) pluginapi.UsageRecord {
	return pluginapi.UsageRecord{
		Provider:    providerName,
		AuthID:      authID,
		Model:       model,
		RequestedAt: time.Now(),
		Detail:      pluginapi.UsageDetail{TotalTokens: total},
	}
}

func TestRecordDailyUsageAccumulates(t *testing.T) {
	resetDailyQuotaState(t)
	recordDailyUsage(usageRecord("acct-1", "deepseek-v4.1-flash", 1000))
	recordDailyUsage(usageRecord("acct-1", "deepseek-v4.1-flash", 2500))

	rows := dailyQuotaSnapshotForAuth("acct-1")
	if len(rows) == 0 {
		t.Fatal("no rows returned")
	}
	var found *modelDailyQuota
	for i := range rows {
		if rows[i].Model == "deepseek-v4.1-flash" {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("model row missing")
	}
	if found.Used != 3500 {
		t.Errorf("Used = %d, want 3500", found.Used)
	}
	if found.Requests != 2 {
		t.Errorf("Requests = %d, want 2", found.Requests)
	}
	if !found.HasLimit || found.Limit != 200_000_000 {
		t.Errorf("limit not applied: has=%v limit=%d", found.HasLimit, found.Limit)
	}
	if found.Remaining != 200_000_000-3500 {
		t.Errorf("Remaining = %d, want %d", found.Remaining, 200_000_000-3500)
	}
	if found.UsedPercent != 0 {
		t.Errorf("UsedPercent = %d, want 0 (3500 of 200M)", found.UsedPercent)
	}
}

// TestRecordDailyUsageIsolatesByAuthAndModel: a free allowance is per
// credential AND per model. Sharing a bucket across either would let one
// account's traffic consume another's allowance in the panel.
func TestRecordDailyUsageIsolatesByAuthAndModel(t *testing.T) {
	resetDailyQuotaState(t)
	recordDailyUsage(usageRecord("acct-a", "deepseek-v4.1-flash", 1000))
	recordDailyUsage(usageRecord("acct-b", "deepseek-v4.1-flash", 7000))
	recordDailyUsage(usageRecord("acct-a", "glm-5.2", 3000))

	pick := func(auth, model string) int64 {
		for _, row := range dailyQuotaSnapshotForAuth(auth) {
			if row.Model == model {
				return row.Used
			}
		}
		return -1
	}
	if got := pick("acct-a", "deepseek-v4.1-flash"); got != 1000 {
		t.Errorf("acct-a flash = %d, want 1000", got)
	}
	if got := pick("acct-b", "deepseek-v4.1-flash"); got != 7000 {
		t.Errorf("acct-b flash = %d, want 7000", got)
	}
	if got := pick("acct-a", "glm-5.2"); got != 3000 {
		t.Errorf("acct-a glm = %d, want 3000", got)
	}
}

// TestRecordDailyUsageFallsBackToPartSum: some upstreams report only
// prompt/completion. A record with total=0 but non-zero parts must not be
// recorded as a zero-token request.
func TestRecordDailyUsageFallsBackToPartSum(t *testing.T) {
	resetDailyQuotaState(t)
	recordDailyUsage(pluginapi.UsageRecord{
		Provider:    providerName,
		AuthID:      "acct-parts",
		Model:       "glm-5.2",
		RequestedAt: time.Now(),
		Detail:      pluginapi.UsageDetail{InputTokens: 400, OutputTokens: 100},
	})
	for _, row := range dailyQuotaSnapshotForAuth("acct-parts") {
		if row.Model != "glm-5.2" {
			continue
		}
		if row.Used != 500 {
			t.Fatalf("Used = %d, want 500 (parts summed when total missing)", row.Used)
		}
		return
	}
	t.Fatal("glm-5.2 row missing")
}

// TestRecordDailyUsageRejectsOtherProviders: the host may deliver records for
// any provider; counting those would inflate WorkBuddy's free-tier usage with
// unrelated traffic.
func TestRecordDailyUsageRejectsOtherProviders(t *testing.T) {
	resetDailyQuotaState(t)
	recordDailyUsage(pluginapi.UsageRecord{
		Provider:    "claude",
		AuthID:      "acct-x",
		Model:       "deepseek-v4.1-flash",
		RequestedAt: time.Now(),
		Detail:      pluginapi.UsageDetail{TotalTokens: 999999},
	})
	for _, row := range dailyQuotaSnapshotForAuth("acct-x") {
		if row.Used != 0 {
			t.Fatalf("foreign provider usage leaked into WorkBuddy counters: %d", row.Used)
		}
	}
}

func TestRecordDailyUsageRejectsMissingProvider(t *testing.T) {
	resetDailyQuotaState(t)
	recordDailyUsage(pluginapi.UsageRecord{
		AuthID:      "unattributed-acct",
		Model:       "deepseek-v4.1-flash",
		RequestedAt: time.Now(),
		Detail:      pluginapi.UsageDetail{TotalTokens: 999999},
	})
	dailyQuotaMu.Lock()
	defer dailyQuotaMu.Unlock()
	for key := range dailyQuotaStore {
		if key.AuthID == "unattributed-acct" {
			t.Fatalf("missing provider created a stored quota bucket for model %q", key.Model)
		}
	}
}

// TestDailyQuotaNeverNegativeOrZeroLimit: a corrupted/negative counter would
// render as "remaining 0" or a negative percentage. Both are clamped.
func TestDailyQuotaNeverNegativeOrZeroLimit(t *testing.T) {
	resetDailyQuotaState(t)
	recordDailyUsage(usageRecord("acct-over", "glm-5.2", 999_999_999))
	for _, row := range dailyQuotaSnapshotForAuth("acct-over") {
		if row.Model != "glm-5.2" {
			continue
		}
		if row.Remaining < 0 {
			t.Errorf("Remaining = %d, must clamp at 0", row.Remaining)
		}
		if row.UsedPercent > 100 {
			t.Errorf("UsedPercent = %d, must clamp at 100", row.UsedPercent)
		}
		return
	}
}

// TestDailyQuotaUnknownModelHasNoInventedLimit: a model absent from the table
// must be reported as measured-but-unlimited rather than with a guessed cap.
func TestDailyQuotaUnknownModelHasNoInventedLimit(t *testing.T) {
	resetDailyQuotaState(t)
	recordDailyUsage(usageRecord("acct-unk", "some-new-model", 1234))
	for _, row := range dailyQuotaSnapshotForAuth("acct-unk") {
		if row.Model != "some-new-model" {
			continue
		}
		if row.HasLimit {
			t.Fatalf("unknown model must not get an invented limit (limit=%d)", row.Limit)
		}
		if row.Used != 1234 {
			t.Fatalf("measured usage must still be reported, got %d", row.Used)
		}
		return
	}
	t.Fatal("row missing")
}

// TestDailyQuotaListsUnusedKnownModels: a free model with no traffic today
// should still appear (0 / limit) — hiding it is indistinguishable from the
// model not being free.
func TestDailyQuotaListsUnusedKnownModels(t *testing.T) {
	resetDailyQuotaState(t)
	rows := dailyQuotaSnapshotForAuth("acct-idle")
	var flash *modelDailyQuota
	for i := range rows {
		if rows[i].Model == "deepseek-v4.1-flash" {
			flash = &rows[i]
		}
	}
	if flash == nil {
		t.Fatal("known free model must be listed even with zero usage")
	}
	if flash.Used != 0 || flash.Limit != 200_000_000 {
		t.Fatalf("unused row wrong: used=%d limit=%d", flash.Used, flash.Limit)
	}
}

// ---------------------------------------------------------------------------
// 配置覆盖
// ---------------------------------------------------------------------------

func TestParseDailyFreeLimitsShapes(t *testing.T) {
	cases := map[string]string{
		"mapping":  "daily_free_limits:\n  my-model: 123456\n",
		"inline":   "daily_free_limits: my-model=123456\n",
		"sequence": "daily_free_limits:\n  - my-model=123456\n",
		"colon":    "daily_free_limits:\n  - \"my-model: 123456\"\n",
		"suffix-m": "daily_free_limits:\n  my-model: 50m\n",
		"suffix-b": "daily_free_limits:\n  my-model: 1.5b\n",
		"exp":      "daily_free_limits:\n  my-model: 2e8\n",
		"undersc":  "daily_free_limits:\n  my-model: 200_000_000\n",
	}
	for name, yamlText := range cases {
		got := parseDailyFreeLimits([]byte(yamlText))
		if got["my-model"] <= 0 {
			t.Errorf("%s: my-model limit not parsed: %v", name, got)
		}
	}
	// 50m / 1.5b / 2e8 must all scale correctly, not be read as raw digits.
	if got := parseDailyFreeLimits([]byte("daily_free_limits:\n  a: 50m\n"))["a"]; got != 50_000_000 {
		t.Errorf("50m parsed as %d, want 50000000", got)
	}
	if got := parseDailyFreeLimits([]byte("daily_free_limits:\n  a: 1.5b\n"))["a"]; got != 1_500_000_000 {
		t.Errorf("1.5b parsed as %d, want 1500000000", got)
	}
	if got := parseDailyFreeLimits([]byte("daily_free_limits:\n  a: 2e8\n"))["a"]; got != 200_000_000 {
		t.Errorf("2e8 parsed as %d, want 200000000", got)
	}
}

// TestSetDailyFreeLimitsOverridesAndReverts: operator overrides must win over
// built-ins, and removing the override must fall back to the built-in default
// rather than leaving the old override in place.
func TestSetDailyFreeLimitsOverridesAndReverts(t *testing.T) {
	resetDailyQuotaState(t)

	setDailyFreeLimits(map[string]int64{"deepseek-v4.1-flash": 999})
	if got := dailyQuotaLimitFor("deepseek-v4.1-flash"); got != 999 {
		t.Fatalf("override not applied: %d", got)
	}
	// Model names are normalized, so a differently-cased override still lands.
	setDailyFreeLimits(map[string]int64{"DEEPSEEK-V4.1-FLASH": 555})
	if got := dailyQuotaLimitFor("deepseek-v4.1-flash"); got != 555 {
		t.Fatalf("case-insensitive override not applied: %d", got)
	}
	// Nil reverts to built-ins.
	setDailyFreeLimits(nil)
	if got := dailyQuotaLimitFor("deepseek-v4.1-flash"); got != 200_000_000 {
		t.Fatalf("built-in default not restored: %d", got)
	}
}

// dailyQuotaLimitFor is a test helper reading the effective table.
func dailyQuotaLimitFor(model string) int64 {
	dailyQuotaMu.Lock()
	defer dailyQuotaMu.Unlock()
	return dailyQuotaLimits[normalizeModelKey(model)]
}

// ---------------------------------------------------------------------------
// 持久化
// ---------------------------------------------------------------------------

// TestDailyQuotaPersistsAcrossReload: a plugin reload must not zero the day's
// counters, which would show a misleading "100% remaining" after every restart.
func TestDailyQuotaPersistsAcrossReload(t *testing.T) {
	resetDailyQuotaState(t)
	dir := t.TempDir()
	path := filepath.Join(dir, dailyQuotaFileName)

	dailyQuotaMu.Lock()
	dailyQuotaPath = path
	dailyQuotaMu.Unlock()

	recordDailyUsage(usageRecord("acct-p", "deepseek-v4.1-flash", 4242))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("counter file not written: %v", err)
	}

	// Simulate a reload: drop memory, keep the file.
	dailyQuotaMu.Lock()
	dailyQuotaStore = map[dailyQuotaKey]*dailyUsage{}
	dailyQuotaMu.Unlock()
	loadDailyQuotaFile(path)

	rows := dailyQuotaSnapshotForAuth("acct-p")
	for _, row := range rows {
		if row.Model == "deepseek-v4.1-flash" {
			if row.Used != 4242 {
				t.Fatalf("usage lost across reload: %d", row.Used)
			}
			return
		}
	}
	t.Fatal("row missing after reload")
}

// TestLoadDailyQuotaFileIgnoresStaleAndCorruptInput: stale days and malformed
// files must be dropped, not merged into today's counters.
func TestLoadDailyQuotaFileIgnoresStaleAndCorruptInput(t *testing.T) {
	resetDailyQuotaState(t)
	dir := t.TempDir()

	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadDailyQuotaFile(corrupt) // must not panic
	// The corrupt file must contribute no USAGE. Known models are still listed
	// (with zero usage) by design, so assert on usage rather than on row count.
	for _, row := range dailyQuotaSnapshotForAuth("acct-c") {
		if row.Used != 0 || row.Requests != 0 {
			t.Fatalf("corrupt file contributed usage: %+v", row)
		}
	}

	stale := filepath.Join(dir, "stale.json")
	payload := dailyQuotaFile{Version: 1, Usage: []dailyQuotaFileEntry{{
		AuthID: "acct-s", Model: "glm-5.2", Day: "2020-01-01",
		dailyUsage: dailyUsage{TotalTokens: 5000, Requests: 1},
	}}}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(stale, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loadDailyQuotaFile(stale)
	for _, row := range dailyQuotaSnapshotForAuth("acct-s") {
		if row.Model == "glm-5.2" && row.Used != 0 {
			t.Fatalf("stale day merged into today: %d", row.Used)
		}
	}
}

// TestDailyQuotaResetClearsOnlyToday: reset is an operator escape hatch, and it
// must not wipe history that a midnight-straddling request still needs.
func TestDailyQuotaResetClearsOnlyToday(t *testing.T) {
	resetDailyQuotaState(t)
	day := dailyQuotaNow().Local().Format("2006-01-02")

	dailyQuotaMu.Lock()
	dailyQuotaStore[dailyQuotaKey{AuthID: "acct-r", Model: "glm-5.2", Day: day}] = &dailyUsage{TotalTokens: 100}
	dailyQuotaStore[dailyQuotaKey{AuthID: "acct-r", Model: "glm-5.2", Day: "2020-01-01"}] = &dailyUsage{TotalTokens: 200}
	dailyQuotaMu.Unlock()

	resetDailyQuota("acct-r")

	dailyQuotaMu.Lock()
	defer dailyQuotaMu.Unlock()
	if _, ok := dailyQuotaStore[dailyQuotaKey{AuthID: "acct-r", Model: "glm-5.2", Day: day}]; ok {
		t.Error("today's bucket not cleared")
	}
	if _, ok := dailyQuotaStore[dailyQuotaKey{AuthID: "acct-r", Model: "glm-5.2", Day: "2020-01-01"}]; !ok {
		t.Error("historical bucket must survive a reset")
	}
}

// ---------------------------------------------------------------------------
// RPC 入口
// ---------------------------------------------------------------------------

// TestUsageHandleRPCAcceptsAndRecords: the host calls usage.handle per request.
// A malformed record must be ACKNOWLEDGED (not error), because failing here
// would mark the plugin faulty for a request that already succeeded upstream.
func TestUsageHandleRPCAcceptsAndRecords(t *testing.T) {
	resetDailyQuotaState(t)

	for _, raw := range []string{
		"",
		"{not json",
		`{"Provider":"workbuddy","Model":"deepseek-v4.1-flash","AuthID":"acct-rpc","Detail":{"TotalTokens":777}}`,
	} {
		out, err := handleUsageRecord([]byte(raw))
		if err != nil {
			t.Fatalf("raw=%q must not error: %v", raw, err)
		}
		var env envelope
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("raw=%q produced invalid envelope: %s", raw, out)
		}
		if !env.OK {
			t.Fatalf("raw=%q must be acknowledged, got %s", raw, out)
		}
	}

	var got int64
	for _, row := range dailyQuotaSnapshotForAuth("acct-rpc") {
		if row.Model == "deepseek-v4.1-flash" {
			got = row.Used
		}
	}
	if got != 777 {
		t.Fatalf("valid record not recorded through RPC: %d", got)
	}
}

// TestUsageHandleDispatchIsRegistered locks the RPC method name: the host
// dispatches on the literal "usage.handle", so a rename here would silently
// stop all usage accounting.
func TestUsageHandleDispatchIsRegistered(t *testing.T) {
	resetDailyQuotaState(t)
	raw, err := handleMethod("usage.handle", mustJSON(map[string]any{
		"Provider": providerName,
		"AuthID":   "acct-dispatch",
		"Model":    "deepseek-v4.1-flash",
		"Detail":   map[string]any{"TotalTokens": 321},
	}))
	if err != nil {
		t.Fatalf("handleMethod: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("usage.handle must succeed, got %s", env.Result)
	}
	var got int64
	for _, row := range dailyQuotaSnapshotForAuth("acct-dispatch") {
		if row.Model == "deepseek-v4.1-flash" {
			got = row.Used
		}
	}
	if got != 321 {
		t.Fatalf("usage.handle did not record: %d", got)
	}
}

// TestRegistrationDeclaresUsagePlugin: without this capability the host never
// delivers usage records, so the whole feature silently reports zero.
func TestRegistrationDeclaresUsagePlugin(t *testing.T) {
	if !wbRegistration().Capabilities.UsagePlugin {
		t.Fatal("UsagePlugin capability must be declared or no usage is ever delivered")
	}
}

// TestLimitSourceDistinguishesOverrideFromDefault is a regression lock on a bug
// found only in the live e2e run: a model present in BOTH the built-in table and
// the operator config reported limit_source="default", because the label was
// derived from "is it in the defaults table?" instead of "did config override
// it?". The VALUE was right, so nothing looked wrong — only the provenance label
// lied, which is exactly what an operator reads to decide whether their config
// took effect.
func TestLimitSourceDistinguishesOverrideFromDefault(t *testing.T) {
	resetDailyQuotaState(t)
	sourceFor := func(model string) string {
		for _, row := range dailyQuotaSnapshotForAuth("acct-src") {
			if row.Model == model {
				return row.LimitSource
			}
		}
		return "<missing>"
	}
	// Phase 1: no config overrides at all. The one built-in must say "default".
	if got := sourceFor("deepseek-v4.1-flash"); got != "default" {
		t.Fatalf("untouched built-in reported source=%q, want \"default\"", got)
	}

	// Phase 2: override that same built-in, and add a config-only model.
	// deepseek-v4.1-flash is the regression case: it exists in BOTH tables, and
	// the effective value now comes from config, so the label must say so.
	setDailyFreeLimits(map[string]int64{
		"deepseek-v4.1-flash": 123,
		"glm-5.2":             456,
	})
	if got := sourceFor("deepseek-v4.1-flash"); got != "config" {
		t.Errorf("overridden built-in reported source=%q, want \"config\" (the value came from config)", got)
	}
	if got := sourceFor("glm-5.2"); got != "config" {
		t.Errorf("config-only model reported source=%q, want \"config\"", got)
	}
	for _, row := range dailyQuotaSnapshotForAuth("acct-src") {
		if row.Model == "deepseek-v4.1-flash" && row.Limit != 123 {
			t.Errorf("override not applied: limit=%d", row.Limit)
		}
	}
}

// TestOnlyDeepSeekHasBuiltInFreeAllowance locks the built-in table's contents.
//
// hy4-preview was removed in 0.9.36 because its free window (14 days from the
// 2026-08-28 launch) had passed. It must NOT come back by accident: a stale
// built-in renders a percentage for a budget that no longer exists, which makes
// a model burning paid credits look like it is consuming a free tier.
//
// hy4-preview must still be MEASURED (usage shown without a percentage) — it is
// delisted, not blacklisted.
func TestOnlyDeepSeekHasBuiltInFreeAllowance(t *testing.T) {
	resetDailyQuotaState(t)

	if _, ok := defaultDailyFreeLimits["hy4-preview"]; ok {
		t.Error("hy4-preview must not be a built-in: its free window has passed")
	}
	if _, ok := defaultDailyFreeLimits["deepseek-v4.1-flash"]; !ok {
		t.Error("deepseek-v4.1-flash must remain a built-in")
	}
	if len(defaultDailyFreeLimits) != 1 {
		t.Errorf("built-in table has %d entries; expected exactly 1 (deepseek-v4.1-flash)", len(defaultDailyFreeLimits))
	}

	// A delisted model must still report its measured usage, just no limit.
	recordDailyUsage(usageRecord("acct-hy", "hy4-preview", 4321))
	for _, row := range dailyQuotaSnapshotForAuth("acct-hy") {
		if row.Model != "hy4-preview" {
			continue
		}
		if row.HasLimit {
			t.Errorf("hy4-preview must not carry a limit (limit=%d)", row.Limit)
		}
		if row.Used != 4321 {
			t.Errorf("hy4-preview usage must still be measured, got %d", row.Used)
		}
		return
	}
	t.Fatal("hy4-preview row missing: a delisted model must still be listed when it has usage")
}

// TestParseTokenLimitChineseSuffixes is a regression lock on a bug found in the
// live e2e run: "2亿" did not parse at all, so the override was silently
// dropped and the built-in default applied instead. The VALUE looked correct
// (both are 200,000,000), so nothing appeared broken — only the limit_source
// label revealed it. An operator writing "2.5亿" would have silently got the
// default 2亿, i.e. a wrong limit with no error anywhere.
func TestParseTokenLimitChineseSuffixes(t *testing.T) {
	cases := map[string]int64{
		"2亿":          200_000_000,
		"2.5亿":        250_000_000,
		"5000万":       50_000_000,
		"200万":        2_000_000,
		"1亿":          100_000_000,
		"200000000":   200_000_000,
		"200m":        200_000_000,
		"2e8":         200_000_000,
		"1.5b":        1_500_000_000,
		"50k":         50_000,
		"200_000_000": 200_000_000,
	}
	for raw, want := range cases {
		got, ok := parseTokenLimit(raw)
		if !ok {
			t.Errorf("parseTokenLimit(%q) failed to parse — a silent drop here means the built-in default applies instead", raw)
			continue
		}
		if got != want {
			t.Errorf("parseTokenLimit(%q) = %d, want %d", raw, got, want)
		}
	}
	// Unparseable input must be REPORTED as unparseable, not coerced to 0,
	// so the caller can log it rather than silently using a default.
	for _, raw := range []string{"", "abc", "亿", "m"} {
		if _, ok := parseTokenLimit(raw); ok {
			t.Errorf("parseTokenLimit(%q) should fail", raw)
		}
	}
}

// TestChineseSuffixOverrideActuallyApplies: the end-to-end consequence of the
// parse bug — a 亿-suffixed override must change the effective limit AND be
// labelled as coming from config.
func TestChineseSuffixOverrideActuallyApplies(t *testing.T) {
	resetDailyQuotaState(t)
	overrides := parseDailyFreeLimits([]byte("daily_free_limits:\n  deepseek-v4.1-flash: 2.5亿\n"))
	if overrides["deepseek-v4.1-flash"] != 250_000_000 {
		t.Fatalf("亿 suffix not parsed through config: %v", overrides)
	}
	setDailyFreeLimits(overrides)

	for _, row := range dailyQuotaSnapshotForAuth("acct-cn") {
		if row.Model != "deepseek-v4.1-flash" {
			continue
		}
		if row.Limit != 250_000_000 {
			t.Errorf("limit = %d, want 250000000", row.Limit)
		}
		if row.LimitSource != "config" {
			t.Errorf("source = %q, want \"config\"", row.LimitSource)
		}
		return
	}
	t.Fatal("row missing")
}
