package main

import (
	"testing"
)

// Foolproof config: boolean spellings users actually type must all work, and
// an empty config must behave as "everything on with defaults" so a fresh
// install needs zero configuration.
func TestConfigureAcceptsTypedBooleanSpellings(t *testing.T) {
	cases := []struct {
		yaml string
		want bool
	}{
		{"checkin_auto: true", true},
		{"checkin_auto: True", true},
		{"checkin_auto: TRUE", true},
		{"checkin_auto: YES", true},
		{"checkin_auto: On", true},
		{"checkin_auto: 'true'", true},
		{"checkin_auto: \"1\"", true},
		{"checkin_auto: 开启", true},
		{"checkin_auto: false", false},
		{"checkin_auto: 0", false},
		{"checkin_auto: no", false},
		{"checkin_auto: off", false},
	}
	for _, tc := range cases {
		configure(configYAMLEnvelope(tc.yaml))
		checkinAutoMu.RLock()
		got := checkinAuto
		checkinAutoMu.RUnlock()
		if got != tc.want {
			t.Errorf("%q: checkin_auto=%t want %t", tc.yaml, got, tc.want)
		}
	}
}

func TestConfigureAcceptsChineseRegionSpelling(t *testing.T) {
	configure(configYAMLEnvelope("login_region: 海外"))
	if got := loadedLoginRegion(); got != regionIntl {
		t.Fatalf("login_region 海外 = %q want intl", got)
	}
	configure(configYAMLEnvelope("login_region: 国际"))
	if got := loadedLoginRegion(); got != regionIntl {
		t.Fatalf("login_region 国际 = %q want intl", got)
	}
}

func TestZeroConfigDefaultsEverythingOn(t *testing.T) {
	// Empty config = fresh install: defaults must be usable with no edits.
	configure(configYAMLEnvelope(""))
	checkinAutoMu.RLock()
	auto := checkinAuto
	checkinAutoMu.RUnlock()
	if !auto {
		t.Fatal("checkin_auto default should be true")
	}
	if !lifecycleEnabled() {
		t.Fatal("lifecycle_auto default should be true")
	}
	if keepaliveEnabled() {
		t.Fatal("plugin keepalive must be opt-in; CPA owns credential refresh")
	}
	if got := loadedLoginRegion(); got != regionCN {
		t.Fatalf("login_region default = %q want cn", got)
	}
	if got := currentLoginPlatform(); got != "CLI" {
		t.Fatalf("login_platform default = %q want CLI", got)
	}
}

// The visual editor hides advanced keys; setting them in YAML must keep
// working exactly as before (no regression from the foolproof surface).
func TestHiddenAdvancedKeysStillTakeEffect(t *testing.T) {
	configure(configYAMLEnvelope("scheduler_mode: credits\nmodels_cn: hunyuan-pro\n"))
	if got := loadedSchedulerMode(); got != schedulerModeCredits {
		t.Fatalf("hidden scheduler_mode ignored: %q", got)
	}
	if ids := pinnedModelIDsForRealm("cn"); len(ids) != 1 || ids[0] != "hunyuan-pro" {
		t.Fatalf("hidden models_cn ignored: %v", ids)
	}
}

// Leftover usage_report_* keys from the removed plugin-side push (0.9.30)
// must be ignored: configure() succeeds and no secret can surface anywhere.
func TestRemovedUsageReportKeysAreIgnored(t *testing.T) {
	configure(configYAMLEnvelope("usage_report_url: http://127.0.0.1:18317/v0/management/usage/import\nusage_report_key: super-secret-key\n"))
	checkinAutoMu.RLock()
	auto := checkinAuto
	checkinAutoMu.RUnlock()
	if !auto {
		t.Fatal("defaults should still apply with leftover usage_report_* keys present")
	}
}
