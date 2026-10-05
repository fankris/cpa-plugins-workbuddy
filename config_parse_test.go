package main

import (
	"strings"
	"testing"
)

// The hand-rolled line parser silently misread inline comments: the value
// string kept the comment, so `checkin_auto: true # 开启` parsed as false and
// `login_region: "intl" # 海外` fell back to cn. Regression tests for all
// three failure modes found during the 0.9.31 audit.
func TestConfigInlineCommentsAreIgnored(t *testing.T) {
	configure(configYAMLEnvelope(strings.Join([]string{
		`checkin_auto: true # 开启`,
		`lifecycle_auto: false # 关闭`,
		`login_region: "intl" # 海外`,
		`login_platform: ide # IDE 模式`,
		`token_keepalive: on # 保活`,
		`models_cn: hunyuan-pro, deepseek-v4-pro # 我的模型`,
	}, "\n")))

	checkinAutoMu.RLock()
	checkin := checkinAuto
	checkinAutoMu.RUnlock()
	if !checkin {
		t.Error(`checkin_auto: true # 开启 must parse as true (was false before 0.9.31)`)
	}

	lifecycleAutoMu.RLock()
	lifecycle := lifecycleAuto
	lifecycleAutoMu.RUnlock()
	if lifecycle {
		t.Error(`lifecycle_auto: false # 关闭 must parse as false`)
	}

	if got := loadedLoginRegion(); got != regionIntl {
		t.Errorf(`login_region: "intl" # 海外 parsed as %q, want intl`, got)
	}
	if got := currentLoginPlatform(); got != "ide" {
		t.Errorf(`login_platform: ide # IDE 模式 parsed as %q, want ide`, got)
	}

	keepaliveAutoMu.RLock()
	keepalive := keepaliveAuto
	keepaliveAutoMu.RUnlock()
	if !keepalive {
		t.Error(`token_keepalive: on # 保活 must parse as true`)
	}

	ids := pinnedModelIDsForRealm("cn")
	want := []string{"hunyuan-pro", "deepseek-v4-pro"}
	if len(ids) != len(want) {
		t.Fatalf("models_cn with comment parsed as %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("models_cn[%d] = %q, want %q (comment must not become part of the ID)", i, ids[i], want[i])
		}
	}
}

// Nested keys with the same name as a top-level setting must not leak upward.
func TestConfigNestedKeysDoNotLeak(t *testing.T) {
	configure(configYAMLEnvelope(strings.Join([]string{
		`models:`,
		`  cn:`,
		`    - deepseek-v4-pro`,
		`  checkin_auto: false`,
		`login_region: cn`,
	}, "\n")))

	checkinAutoMu.RLock()
	checkin := checkinAuto
	checkinAutoMu.RUnlock()
	if !checkin {
		t.Error("nested checkin_auto must not override the top-level default (true)")
	}
	if got := loadedLoginRegion(); got != regionCN {
		t.Errorf("login_region = %q, want cn", got)
	}
}

// Sequences must work for the models_* keys, not just comma-separated scalars.
func TestConfigModelListAcceptsSequence(t *testing.T) {
	configure(configYAMLEnvelope(strings.Join([]string{
		`models_intl:`,
		`  - gpt-6-astra`,
		`  - deepseek-v4.1-flash`,
	}, "\n")))
	ids := pinnedModelIDsForRealm("intl")
	if len(ids) != 2 || ids[0] != "gpt-6-astra" || ids[1] != "deepseek-v4.1-flash" {
		t.Fatalf("models_intl sequence parsed as %v", ids)
	}
}

// Absent keys must keep their defaults rather than flipping to false.
func TestConfigAbsentKeysKeepDefaults(t *testing.T) {
	configure(configYAMLEnvelope("login_region: cn\n"))

	for name, got := range map[string]bool{
		"checkin_auto":   checkinAutoValue(),
		"lifecycle_auto": lifecycleEnabled(),
	} {
		if !got {
			t.Errorf("%s default must stay true when the key is absent", name)
		}
	}
	if growthAutoEnabled() || travelAutoEnabled() || keepaliveEnabled() {
		t.Fatal("rebuild must not start retired/optional automation by default")
	}
	if loadedSchedulerMode() != schedulerModeHost {
		t.Fatal("default must follow the host selector")
	}

}

func checkinAutoValue() bool {
	checkinAutoMu.RLock()
	defer checkinAutoMu.RUnlock()
	return checkinAuto
}
