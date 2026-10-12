package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The host scans the auth dir for *.json and tries to synthesise a credential
// from every match. A state file ending in .json therefore produced a
// "skipping auth file" warning on every rescan and showed up in the CPA
// credential list with an empty type.
func TestDailyQuotaStateFileIsNotAnAuthJSON(t *testing.T) {
	if strings.HasSuffix(strings.ToLower(dailyQuotaFileName), ".json") {
		t.Fatalf("daily quota state file %q must not end in .json", dailyQuotaFileName)
	}
	if !strings.HasSuffix(strings.ToLower(legacyDailyQuotaFileName), ".json") {
		t.Fatalf("legacy name %q should be the old .json name so migration can find it", legacyDailyQuotaFileName)
	}
}

// An auth dir that already holds the old .json state file must end up with the
// new file (payload intact) and no leftover .json.
func TestMigrateLegacyDailyQuotaFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, legacyDailyQuotaFileName)
	next := filepath.Join(dir, dailyQuotaFileName)
	payload := `{"version":1,"usage":[{"auth_id":"a","model":"m","day":"2026-10-09","total_tokens":5}]}`
	if err := os.WriteFile(legacy, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	migrateLegacyDailyQuotaFiles(dir, next)

	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy .json file must be removed so the auth scan stops seeing it")
	}
	raw, err := os.ReadFile(next)
	if err != nil {
		t.Fatalf("new state file missing: %v", err)
	}
	if string(raw) != payload {
		t.Fatalf("payload not preserved: %s", raw)
	}
}

// A stale legacy file must not clobber an existing new-format file.
func TestMigrateLegacyDailyQuotaFileKeepsNewerState(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, legacyDailyQuotaFileName)
	next := filepath.Join(dir, dailyQuotaFileName)
	if err := os.WriteFile(legacy, []byte(`{"version":1,"usage":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte(`{"version":1,"usage":[{"auth_id":"new"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	migrateLegacyDailyQuotaFiles(dir, next)

	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an empty legacy file must be removed")
	}
	raw, _ := os.ReadFile(next)
	if !strings.Contains(string(raw), "new") {
		t.Fatalf("existing new-format state was overwritten: %s", raw)
	}
}

// A non-empty copy in the auth dir must be MERGED into the destination, not
// dropped: deleting it outright lost a whole account's counters.
func TestMergeQuotaStateFilesAbsorbsRatherThanDrops(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.json")
	src := filepath.Join(dir, "src.json")

	write := func(path string, entries []dailyQuotaFileEntry) {
		raw, err := json.Marshal(dailyQuotaFile{Version: 1, Usage: entries})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(dst, []dailyQuotaFileEntry{{
		AuthID: "acct", Model: "glm-5.2", Day: "2026-10-12",
		dailyUsage: dailyUsage{TotalTokens: 100, Requests: 1},
	}})
	write(src, []dailyQuotaFileEntry{{
		AuthID: "acct", Model: "glm-5.2", Day: "2026-10-12",
		dailyUsage: dailyUsage{TotalTokens: 250, Requests: 2},
	}, {
		AuthID: "other", Model: "deepseek-v4.1-flash", Day: "2026-10-12",
		dailyUsage: dailyUsage{TotalTokens: 7, Requests: 1},
	}})

	if !mergeQuotaStateFiles(dst, src) {
		t.Fatal("merge reported failure")
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	var merged dailyQuotaFile
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatal(err)
	}
	byKey := map[string]dailyUsage{}
	for _, e := range merged.Usage {
		byKey[e.AuthID+"/"+e.Model] = e.dailyUsage
	}
	if got := byKey["acct/glm-5.2"]; got.TotalTokens != 350 || got.Requests != 3 {
		t.Fatalf("same-key counters must sum, got %+v", got)
	}
	if got := byKey["other/deepseek-v4.1-flash"]; got.TotalTokens != 7 {
		t.Fatalf("new key must be added, got %+v", got)
	}
}

// If the new file cannot be written, the legacy copy must survive: deleting it
// anyway would silently lose the day's counters.
func TestMigrateLegacyDailyQuotaFileKeepsLegacyOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, legacyDailyQuotaFileName)
	payload := `{"version":1,"usage":[{"auth_id":"keep-me"}]}`
	if err := os.WriteFile(legacy, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	// A regular file where the target's parent directory must be makes
	// os.MkdirAll fail, so the atomic write cannot succeed.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(blocker, dailyQuotaFileName)

	migrateLegacyDailyQuotaFiles(dir, unwritable)

	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy file must be kept when the new file could not be written: %v", err)
	}
	raw, err := os.ReadFile(legacy)
	if err != nil || string(raw) != payload {
		t.Fatalf("legacy payload changed: %s (%v)", raw, err)
	}
}

// The plugin must not keep its bookkeeping inside the host auth dir: CPA scans
// that directory for *.json and synthesises a credential from every match.
func TestDailyQuotaStatePathIsOutsideAuthDir(t *testing.T) {
	authDir := t.TempDir()
	stateDir := t.TempDir()
	resetDailyQuotaState(t)
	setDailyQuotaStateRoot(stateDir)
	t.Cleanup(func() { setDailyQuotaStateRoot("") })

	setDailyQuotaPath(authDir)

	dailyQuotaMu.Lock()
	path := dailyQuotaPath
	dailyQuotaMu.Unlock()
	if path == "" {
		t.Fatal("persistence path was not set")
	}
	if strings.HasPrefix(path, authDir+string(os.PathSeparator)) {
		t.Fatalf("state file %q must not live inside the host auth dir %q", path, authDir)
	}
	entries, err := os.ReadDir(authDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("auth dir must stay empty, got %v", entries)
	}
}

// A file left in the auth dir by an older build is carried over and removed.
func TestSetDailyQuotaPathMovesStateOutOfAuthDir(t *testing.T) {
	authDir := t.TempDir()
	stateDir := t.TempDir()
	resetDailyQuotaState(t)
	setDailyQuotaStateRoot(stateDir)
	t.Cleanup(func() { setDailyQuotaStateRoot("") })

	legacy := filepath.Join(authDir, legacyDailyQuotaFileName)
	payload := `{"version":1,"usage":[{"auth_id":"moved","model":"m","day":"2026-10-11","total_tokens":7}]}`
	if err := os.WriteFile(legacy, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	setDailyQuotaPath(authDir)

	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy file must be removed from the auth dir")
	}
	entries, err := os.ReadDir(authDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("auth dir must end clean, got %v", entries)
	}
	// The legacy file is split into a per-account shard, and the legacy file
	// itself is removed so the state directory stays tidy.
	shard := filepath.Join(stateDir, "usage", quotaShardName("moved"))
	raw, err := os.ReadFile(shard)
	if err != nil {
		t.Fatalf("state not carried over into a shard: %v", err)
	}
	if !strings.Contains(string(raw), "\"total_tokens\":7") {
		t.Fatalf("payload changed during migration: %s", raw)
	}
	if _, errLegacy := os.Stat(filepath.Join(stateDir, dailyQuotaFileName)); !errors.Is(errLegacy, os.ErrNotExist) {
		t.Fatal("legacy single-file state must be removed after being split")
	}
}

// Persistence can be switched off entirely: nothing is created anywhere.
func TestDailyQuotaPersistOffCreatesNoFile(t *testing.T) {
	authDir := t.TempDir()
	stateDir := t.TempDir()
	resetDailyQuotaState(t)
	setDailyQuotaStateRoot(stateDir)
	setDailyQuotaPersist(false)
	t.Cleanup(func() {
		setDailyQuotaPersist(true)
		setDailyQuotaStateRoot("")
	})

	setDailyQuotaPath(authDir)

	dailyQuotaMu.Lock()
	path := dailyQuotaPath
	dailyQuotaMu.Unlock()
	if path != "" {
		t.Fatalf("persistence disabled but path = %q", path)
	}
	entries, _ := os.ReadDir(stateDir)
	if len(entries) != 0 {
		t.Fatalf("persistence disabled but state dir is not empty: %v", entries)
	}
}
