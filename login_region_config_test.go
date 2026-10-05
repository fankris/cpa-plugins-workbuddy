package main

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestRegistrationExposesAuthorizationRegions(t *testing.T) {
	fields := wbRegistration().Metadata.ConfigFields
	if len(fields) != 3 {
		t.Fatalf("ConfigFields = %d, want exactly 3", len(fields))
	}
	if fields[0].Name != "login_region" || fields[1].Name != "login_platform" {
		t.Fatalf("ConfigFields = [%s, %s], want [login_region login_platform]", fields[0].Name, fields[1].Name)
	}
	field := &fields[0]
	if field.Type != pluginapi.ConfigFieldTypeEnum {
		t.Fatalf("login_region type = %q, want enum", field.Type)
	}
	if len(field.EnumValues) != 2 || field.EnumValues[0] != "cn" || field.EnumValues[1] != "intl" {
		t.Fatalf("login_region enum values = %#v, want [cn intl]", field.EnumValues)
	}
	for _, want := range []string{"国内", "海外", "cn", "intl"} {
		if !strings.Contains(field.Description, want) {
			t.Errorf("login_region description %q does not contain %q", field.Description, want)
		}
	}
	platform := fields[1]
	if platform.Type != pluginapi.ConfigFieldTypeEnum || len(platform.EnumValues) != 2 || platform.EnumValues[0] != "CLI" || platform.EnumValues[1] != "ide" {
		t.Fatalf("login_platform field = %+v, want CLI/ide enum", platform)
	}
}

func TestConfigureLoginRegionControlsNewLoginTarget(t *testing.T) {
	configure(configYAMLEnvelope("enabled: true\nlogin_region: intl\n"))
	if got := loadedLoginRegion(); got != regionIntl {
		t.Fatalf("intl login_region = %q, want %q", got, regionIntl)
	}

	configure(configYAMLEnvelope("enabled: true\nlogin_region: cn\n"))
	if got := loadedLoginRegion(); got != regionCN {
		t.Fatalf("cn login_region = %q, want %q", got, regionCN)
	}
}

func TestIntlLoginRegionForcesIDEPlatform(t *testing.T) {
	configure(configYAMLEnvelope("enabled: true\nlogin_platform: CLI\nlogin_region: intl\n"))
	if got := loadedLoginRegion(); got != regionIntl {
		t.Fatalf("login_region = %q, want %q", got, regionIntl)
	}
	if got := currentLoginPlatform(); got != "CLI" {
		t.Fatalf("configured platform = %q, want CLI before startLoginWithRegion applies Intl override", got)
	}
}
