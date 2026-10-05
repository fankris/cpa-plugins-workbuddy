package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestHostSchedulerModeDeclinesWithoutSideEffects(t *testing.T) {
	restore := setSchedulerMode(schedulerModeHost)
	defer restore()
	old := getActiveAuthID()
	setActiveAuthID("panel-draft")
	defer setActiveAuthID(old)
	for _, provider := range []string{providerName, "other", ""} {
		t.Run("provider="+provider, func(t *testing.T) {
			raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
				Provider: provider,
				Candidates: []pluginapi.SchedulerAuthCandidate{
					{ID: "wb", Provider: providerName, Status: "disabled"}, {ID: "other", Provider: "other"},
				},
			}))
			if err != nil {
				t.Fatal(err)
			}
			got := parsePickResponse(t, raw)
			if got.Handled || got.Reject || got.AuthID != "" || got.DelegateBuiltin != "" {
				t.Fatalf("host mode must not override CPA: %+v", got)
			}
			if getActiveAuthID() != "panel-draft" {
				t.Fatal("host mode changed panel selection")
			}
		})
	}
}

func TestNativeSchedulerFieldDocumentsLegacyCompatibility(t *testing.T) {
	fields := wbRegistration().Metadata.ConfigFields
	field := fields[2]
	if field.Name != "scheduler_mode" || field.Type != pluginapi.ConfigFieldTypeEnum {
		t.Fatalf("unexpected field: %+v", field)
	}
	for _, mode := range []string{"host", "builtin", "off", "credits"} {
		found := false
		for _, value := range field.EnumValues {
			found = found || value == mode
		}
		if !found || !strings.Contains(field.Description, mode) {
			t.Fatalf("missing mode %s", mode)
		}
	}
	if !strings.Contains(field.Description, "默认") || !strings.Contains(field.Description, "round-robin") {
		t.Fatal("legacy default not explained")
	}
}

func TestConfigureHostModeAndLegacyModes(t *testing.T) {
	defer configure(configYAMLEnvelope(""))
	for _, tc := range []struct{ input, want string }{
		{"host", schedulerModeHost}, {"HOST", schedulerModeHost}, {"builtin", schedulerModeBuiltin},
		{"off", schedulerModeBuiltin}, {"credits", schedulerModeCredits}, {"", schedulerModeHost},
	} {
		configure(configYAMLEnvelope("scheduler_mode: '" + tc.input + "'\ncheckin_auto: false\ngrowth_auto: false\ntravel_auto: false\ntoken_keepalive: false\n"))
		if got := loadedSchedulerMode(); got != tc.want {
			t.Fatalf("%q => %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLifecycleConfigValidationRejectsWithoutReset(t *testing.T) {
	restore := setSchedulerMode(schedulerModeHost)
	defer restore()
	cases := [][]byte{
		[]byte(`{"config_yaml":"not-base64"}`), []byte(`{"config_yaml":`),
		configYAMLEnvelope("[not, a, mapping]"), configYAMLEnvelope("secret: [DO-NOT-ECHO"),
		configYAMLEnvelope("login_region: DO-NOT-ECHO"), configYAMLEnvelope("scheduler_mode: missing"),
		configYAMLEnvelope("login_platform: [CLI, ide]"),
		configYAMLEnvelope("scheduler_mode: host\nscheduler_mode: credits"),
		configYAMLEnvelope("scheduler_mode: host\n---\nscheduler_mode: credits"),
	}
	for _, method := range []string{pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure} {
		for i, raw := range cases {
			result, err := handleMethod(method, raw)
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				OK    bool
				Error struct{ Code string }
			}
			if err := json.Unmarshal(result, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.OK || envelope.Error.Code != "invalid_config" {
				t.Fatalf("case %d: %s", i, result)
			}
			if strings.Contains(string(result), "DO-NOT-ECHO") {
				t.Fatal("validation leaked raw configuration")
			}
			if loadedSchedulerMode() != schedulerModeHost {
				t.Fatal("rejected config reset running scheduler")
			}
		}
	}
}

func TestLifecycleConfigValidationAcceptsLegacyAndOpaqueFields(t *testing.T) {
	for _, value := range []string{
		"", "# comment", "null", "{}",
		"login_region: 国际\nlogin_platform: ide模式\nscheduler_mode: off",
		"login_region: null\nscheduler_mode: ''",
		"future: {opaque: [1, true, secret]}\nmodels_cn: [legacy-model]\nscheduler_mode: host",
		"plugins:\n  configs:\n    workbuddy:\n      scheduler_mode: host\n      future: {nested: true}",
		"workbuddy:\n  login_region: intl\n  scheduler_mode: host",
	} {
		raw := configYAMLEnvelope(value)
		before := string(raw)
		if err := validateLifecycleConfig(raw); err != nil {
			t.Errorf("valid legacy shape %q: %v", value, err)
		}
		if string(raw) != before {
			t.Fatal("validation rewrote host configuration")
		}
	}
}
