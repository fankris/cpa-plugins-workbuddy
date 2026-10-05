package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// The panel is served as two assets: the HTML shell and panel.js (split out in
// 0.9.31). The host matches resource routes by exact path, so panel.js must be
// declared in managementRegistration — otherwise the browser gets a 404 and the
// page renders but never runs.

// Both assets must be reachable with the correct content type.
func TestServePanelAssets(t *testing.T) {
	html := servePanel("/panel")
	if !strings.Contains(html.contentType, "text/html") {
		t.Errorf("panel contentType = %q, want text/html", html.contentType)
	}
	if len(html.body) == 0 {
		t.Error("panel HTML is empty")
	}
	if !strings.Contains(string(html.body), "panel.js") {
		t.Error("the panel shell must reference panel.js")
	}

	js := servePanel("/panel.js")
	if !strings.Contains(js.contentType, "javascript") {
		t.Errorf("panel.js contentType = %q, want application/javascript", js.contentType)
	}
	if len(js.body) == 0 {
		t.Error("panel.js is empty")
	}
	if !strings.Contains(string(js.body), "WorkBuddyPanel") {
		t.Error("panel.js does not look like the panel script")
	}
}

// The bare and root spellings must all resolve to the HTML shell.
func TestServePanelPathVariants(t *testing.T) {
	for _, sub := range []string{"", "/", "/panel", "/panel.html"} {
		asset := servePanel(sub)
		if len(asset.body) == 0 || !strings.Contains(asset.contentType, "text/html") {
			t.Errorf("servePanel(%q) did not return the HTML shell", sub)
		}
	}
}

// Unknown sub-paths must 404 rather than exposing arbitrary files.
func TestServePanelUnknownPathIs404(t *testing.T) {
	asset := servePanel("/../../etc/passwd")
	if !strings.Contains(string(asset.body), "404") {
		t.Errorf("unknown path must 404, got %q", string(asset.body))
	}
	if strings.Contains(string(asset.body), "root:") {
		t.Fatal("path traversal must not read files")
	}
}

// Declare the dashboard menu on the current resource route and remove the old
// legacy GET menu route, so hosts expose only the new panel entry.
func TestPanelMenuAndJSAreRegistered(t *testing.T) {
	raw, err := json.Marshal(managementRegistration())
	if err != nil {
		t.Fatalf("marshal management registration: %v", err)
	}
	var reg pluginapi.ManagementRegistrationResponse
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("decode management registration wire response: %v", err)
	}
	var foundPanelMenu, foundJS, foundLegacyPanelRoute bool
	menuCount := 0
	for _, r := range reg.Routes {
		if r.Menu != "" {
			menuCount++
		}
		if r.Path == "/plugins/workbuddy/panel" {
			foundLegacyPanelRoute = true
		}
	}
	for _, r := range reg.Resources {
		if r.Menu != "" {
			menuCount++
		}
		if r.Path == "/panel" && r.Menu == "WorkBuddy" {
			foundPanelMenu = true
		}
		if r.Path == "/panel.js" && r.Menu == "" {
			foundJS = true
		}
	}
	if menuCount != 1 {
		t.Errorf("host menu count = %d, want exactly one", menuCount)
	}
	if foundLegacyPanelRoute {
		t.Error("the old /plugins/workbuddy/panel menu route must be removed")
	}
	if len(reg.Resources) != 4 {
		t.Errorf("panel resources = %#v, want /panel menu, /panel.js /panel-i18n.js compatibility asset and /panel.css", reg.Resources)
	}
	if !foundPanelMenu {
		t.Error("WorkBuddy menu must point to the new /panel resource route")
	}
	if !foundJS {
		t.Error("panel.js must remain a menu-less resource route")
	}
}

// The resource route must serve the panel end to end (the host passes the full
// /v0/resource/plugins/workbuddy/... path to the plugin).
func TestManagementServesPanelAssets(t *testing.T) {
	for _, tc := range []struct{ path, wantType string }{
		{"/v0/resource/plugins/workbuddy/panel", "text/html"},
		{"/v0/resource/plugins/workbuddy/panel.js", "javascript"},
		{"/v0/resource/plugins/workbuddy/panel.css", "text/css"},
		{"/v0/resource/plugins/workbuddy/panel-i18n.js", "javascript"},
	} {
		req := pluginapi.ManagementRequest{Method: http.MethodGet, Path: tc.path}
		raw, err := handleManagement(mustJSON(req))
		if err != nil {
			t.Fatalf("handleManagement(%s): %v", tc.path, err)
		}
		var env struct {
			OK     bool `json:"ok"`
			Result struct {
				StatusCode int                 `json:"StatusCode"`
				Headers    map[string][]string `json:"Headers"`
				Body       []byte              `json:"Body"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("envelope(%s): %v", tc.path, err)
		}
		if !env.OK {
			t.Fatalf("%s: not ok: %s", tc.path, raw)
		}
		if env.Result.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", tc.path, env.Result.StatusCode)
		}
		if len(env.Result.Body) == 0 {
			t.Fatalf("%s: empty body", tc.path)
		}
		gotType := strings.Join(env.Result.Headers["Content-Type"], ",")
		if !strings.Contains(gotType, tc.wantType) {
			t.Errorf("%s: content type = %q, want it to contain %q", tc.path, gotType, tc.wantType)
		}
	}
}

// The rebuilt panel mounts a modular React app; browser tests cover navigation,
// filtering, pagination, state preservation and server eligibility instead of
// pinning legacy DOM IDs and innerHTML implementation details.
func TestPanelWorkspaceNavigationAndCompatibility(t *testing.T) {
	page := string(servePanel("/panel").body)
	for _, marker := range []string{`<title>WorkBuddy 面板</title>`, `id="root"`, `data-panel-version="2"`, `panel.css`, `panel.js`} {
		if !strings.Contains(page, marker) {
			t.Fatalf("missing rebuilt shell marker %s", marker)
		}
	}
	if strings.Contains(page, "onclick=") {
		t.Fatal("shell must not contain inline business handlers")
	}
	if !strings.Contains(string(servePanel("/panel.js").body), "WorkBuddyPanel") {
		t.Fatal("rebuilt bundle missing")
	}
	if !strings.Contains(servePanel("/panel.css").contentType, "text/css") {
		t.Fatal("stylesheet is not served")
	}
}

func TestAccountPanelKeepsFullListAndServerEligibility(t *testing.T) {
	// This is only a build-level guard. Functional assertions live in rebuild-browser.mjs.
	js := string(servePanel("/panel.js").body)
	for _, marker := range []string{"trial_eligible", "trial_claimed", "auth_index", "models_enabled", "/credentials/status", "outcomeUnknown"} {
		if !strings.Contains(js, marker) {
			t.Fatalf("missing account/API contract %s", marker)
		}
	}
	if strings.Contains(js, "/tasks/light") {
		t.Fatal("synthetic activity reporting must not be exposed by the new UI")
	}
}
