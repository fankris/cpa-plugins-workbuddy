package main

import (
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"strings"
	"testing"
)

func TestResourceHostPathInjection(t *testing.T) {
	oldM, oldR := loadedManagementBasePath(), loadedResourceBasePath()
	defer func() { setManagementBasePath(oldM); setResourceBasePath(oldR) }()
	setManagementBasePath("/custom/v0/management")
	setResourceBasePath("/custom/resources/workbuddy")
	page := string(servePanel("/panel").body)
	for _, want := range []string{`content="/custom/v0/management/plugins/workbuddy"`, `content="/custom/resources/workbuddy"`, `<title>WorkBuddy 面板</title>`} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing path/title context %s", want)
		}
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"/custom/resources/workbuddy/panel-i18n.js", 200}, {"/custom/resources/workbuddy/missing.js", 404}, {"/custom/resources/workbuddy/../config.yaml", 404}, {"/custom/resources/workbuddy-other/panel", 404}} {
		raw, err := handleManagement(mustJSON(pluginapi.ManagementRequest{Method: http.MethodGet, Path: tc.path}))
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Result pluginapi.ManagementResponse `json:"result"`
		}
		if err = json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if env.Result.StatusCode != tc.status {
			t.Fatalf("%s: %d, expected %d", tc.path, env.Result.StatusCode, tc.status)
		}
	}
}
func TestUnsafeHostPathRejected(t *testing.T) {
	for _, p := range []string{"https://example.com/", "//example.com/x", "/a/../x", "/x?key=secret", "/x#hash", "/x%2fy", "/x\\y", "/a\n"} {
		// Trailing whitespace is intentionally trimmed, so embedded control chars are tested separately.
		if p == "/a\n" {
			p = "/a\nb"
		}
		if cleanHostPath(p) != "" {
			t.Fatalf("unsafe path accepted: %q", p)
		}
	}
	if got := cleanHostPath("/v0/resource/plugins/workbuddy/"); got != "/v0/resource/plugins/workbuddy" {
		t.Fatal(got)
	}
}
