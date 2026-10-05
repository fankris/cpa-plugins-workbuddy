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
	if !strings.Contains(string(js.body), "loadSettings") {
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
	var foundLegacyRoute, foundResourceMenu, foundJS bool
	for _, r := range reg.Routes {
		if r.Path == "/plugins/workbuddy/panel" && r.Menu == "WorkBuddy" {
			foundLegacyRoute = true
		}
	}
	for _, r := range reg.Resources {
		if r.Path == "/panel" && r.Menu == "WorkBuddy" {
			foundResourceMenu = true
		}
		if r.Path == "/panel.js" && r.Menu == "" {
			foundJS = true
		}
	}
	if !foundLegacyRoute {
		t.Error("the /plugins/workbuddy/panel menu route must be registered in Routes for older CPA host compatibility")
	}
	if !foundResourceMenu {
		t.Error("the /panel menu route must also be registered in Resources for modern CPA host compatibility")
	}
	if !foundJS {
		t.Error("panel.js must remain registered in Resources")
	}
}

// The resource route must serve the panel end to end (the host passes the full
// /v0/resource/plugins/workbuddy/... path to the plugin).
func TestManagementServesPanelAssets(t *testing.T) {
	for _, tc := range []struct{ path, wantType string }{
		{"/v0/resource/plugins/workbuddy/panel", "text/html"},
		{"/v0/resource/plugins/workbuddy/panel.js", "javascript"},
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

func TestPanelWorkspaceNavigationAndCompatibility(t *testing.T) {
	html := string(servePanel("/panel").body)
	js := string(servePanel("/panel.js").body)
	for _, want := range []string{
		`role="tablist"`, `id="workspaceAccountsTab"`, `id="workspaceModelsTab"`,
		`id="workspaceAutomationTab"`, `id="accountsView"`, `id="settingsModels"`,
		`id="settingsAutomation"`, `id="accountSearch"`, `id="staleNotice"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("panel workspace is missing %s", want)
		}
	}
	modelsAt := strings.Index(html, `id="settingsModels"`)
	catalogAt := strings.Index(html, `id="globalModelsBody"`)
	automationAt := strings.Index(html, `id="settingsAutomation"`)
	perAccountModelsAt := strings.Index(html, `id="modelsModal"`)
	if modelsAt < 0 || catalogAt < modelsAt || automationAt < catalogAt {
		t.Fatal("global model controls must live inside the model-management workspace")
	}
	if perAccountModelsAt < 0 || perAccountModelsAt < catalogAt || strings.Contains(html[perAccountModelsAt:], `id="globalModelsBody"`) {
		t.Fatal("per-account model dialog must remain separate from global model controls")
	}
	if strings.Contains(html, `id="settingsModal"`) {
		t.Fatal("workspace navigation must not add a second host-style settings menu/modal")
	}
	for _, want := range []string{"function switchWorkspace(", "function handleWorkspaceKeydown(", "function openDialog(", "configMutationRevision", "settingsWriteQueue"} {
		if !strings.Contains(js, want) {
			t.Errorf("panel script is missing %s", want)
		}
	}
	if strings.Contains(js, "materializeSettingDefaults") {
		t.Fatal("opening automation settings must not materialize default values into user config")
	}
}

func TestAccountPanelKeepsFullListAndServerEligibility(t *testing.T) {
	js := string(servePanel("/panel.js").body)
	if !strings.Contains(js, `grid.innerHTML=lastAccounts.map(card).join("");`) {
		t.Fatal("account refresh must render the canonical full account list before filtering")
	}
	if strings.Contains(js, `accountsForFilter(lastAccounts).map(card)`) {
		t.Fatal("filtered account collections must not replace the canonical card list")
	}
	if !strings.Contains(js, `const isWorkBuddy=!!a.trial_eligible||!!a.trial_claimed;`) {
		t.Fatal("trial actions must rely on service eligibility returned by the host")
	}
	if !strings.Contains(js, `a.name,a.email,a.uid`) {
		t.Fatal("account search metadata must include email as advertised")
	}
	if !strings.Contains(js, `if(!response.ok)`) || !strings.Contains(js, `function isCheckinAlreadyResult(result)`) {
		t.Fatal("panel API errors and check-in outcomes must use explicit structured handling")
	}
}
