package main

import (
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

	migrateLegacyDailyQuotaFile(dir, next)

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

	migrateLegacyDailyQuotaFile(dir, next)

	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale legacy file must be removed")
	}
	raw, _ := os.ReadFile(next)
	if !strings.Contains(string(raw), "new") {
		t.Fatalf("existing new-format state was overwritten: %s", raw)
	}
}
