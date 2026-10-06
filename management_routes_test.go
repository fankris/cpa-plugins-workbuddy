package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// The management endpoints are what the panel and CPAMP/CPAMC call. These
// tests drive them through handleManagement (the real RPC entry point) so the
// routing, the envelope shape and the credential guards are all covered.

// managementCallQuery issues a GET with query parameters (the host passes them
// in ManagementRequest.Query, not in the URL).
func managementCallQuery(t *testing.T, subPath string, query map[string][]string) (int, map[string]any) {
	t.Helper()
	req := mustManagementRequest(http.MethodGet, subPath, "", nil)
	// mustManagementRequest returns marshalled JSON; re-decode to inject Query.
	var parsed pluginapi.ManagementRequest
	if err := json.Unmarshal(req, &parsed); err != nil {
		t.Fatalf("decode management request: %v", err)
	}
	parsed.Query = query
	raw, err := handleManagement(mustJSON(parsed))
	if err != nil {
		t.Fatalf("handleManagement(%s): %v", subPath, err)
	}
	return decodeManagementResponse(t, raw)
}

// managementCall issues one management RPC and decodes the inner JSON body.
// The envelope wraps a pluginapi.ManagementResponse, whose fields carry no
// JSON tags (Go names) and whose Body is []byte (base64 on the wire).
func managementCall(t *testing.T, method, subPath, body string) (int, map[string]any) {
	t.Helper()
	raw, err := handleManagement(mustManagementRequest(method, subPath, body, nil))
	if err != nil {
		t.Fatalf("handleManagement(%s %s): %v", method, subPath, err)
	}
	return decodeManagementResponse(t, raw)
}

// decodeManagementResponse unwraps the management envelope.
func decodeManagementResponse(t *testing.T, raw []byte) (int, map[string]any) {
	t.Helper()
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			StatusCode int    `json:"StatusCode"`
			Body       []byte `json:"Body"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope: %v (%s)", err, raw)
	}
	if !env.OK {
		t.Fatalf("management call failed: %s", raw)
	}
	var payload map[string]any
	if len(env.Result.Body) > 0 {
		_ = json.Unmarshal(env.Result.Body, &payload)
	}
	return env.Result.StatusCode, payload
}

// Import must accept this plugin's own credentials and reject another
// plugin's — a foreign credential would save fine but 401 on every request.
func TestManagementImportGuardsForeignCredentials(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)
	resetCheckinState()

	own := `{"json":{"auth":{"accessToken":"wb-token","refreshToken":"wb-refresh"},"account":{"uid":"own1","nickname":"Own"},"type":"workbuddy"}}`
	status, out := managementCall(t, http.MethodPost, "/import", own)
	if status != http.StatusOK {
		t.Fatalf("own credential import status = %d, want 200", status)
	}
	if out["success"] != true {
		t.Fatalf("own credential must import, got %+v", out)
	}
	if len(store.savedRecords()) == 0 {
		t.Fatal("import must persist through host.auth.save")
	}

	foreign := `{"json":{"auth":{"accessToken":"q-token"},"account":{"uid":"q1"},"type":"qoder"}}`
	status, out = managementCall(t, http.MethodPost, "/import", foreign)
	if status != http.StatusOK {
		t.Fatalf("foreign credential should return a 200 with an error body, got %d", status)
	}
	if out["success"] != false {
		t.Fatalf("a foreign credential must be rejected, got %+v", out)
	}
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "qoder") {
		t.Fatalf("the rejection should name the owning plugin, got %q", msg)
	}
}

// An empty import payload must be a clean error, not a panic.
func TestManagementImportRejectsEmptyPayload(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)
	_, out := managementCall(t, http.MethodPost, "/import", `{}`)
	if out["success"] != false {
		t.Fatalf("empty payload must fail cleanly, got %+v", out)
	}
}

// checkin/config is the legacy toggle endpoint; it must still read and write
// the same switch the settings modal uses.
func TestManagementCheckinConfigRoundTrip(t *testing.T) {
	old := checkinAuto
	checkinAutoMu.Lock()
	checkinAuto = true
	checkinAutoMu.Unlock()
	t.Cleanup(func() {
		checkinAutoMu.Lock()
		checkinAuto = old
		checkinAutoMu.Unlock()
	})

	_, out := managementCall(t, http.MethodPost, "/checkin/config", `{"enabled":false}`)
	if got, _ := out["checkin_auto"].(bool); got {
		t.Fatalf("disabling must report checkin_auto=false, got %+v", out)
	}
	checkinAutoMu.RLock()
	current := checkinAuto
	checkinAutoMu.RUnlock()
	if current {
		t.Fatal("the toggle must update the runtime switch")
	}

	_, out = managementCall(t, http.MethodPost, "/checkin/config", `{"enabled":true}`)
	if got, _ := out["checkin_auto"].(bool); !got {
		t.Fatalf("re-enabling must report checkin_auto=true, got %+v", out)
	}
}

// select must validate its auth_index and report the resulting selection.
func TestManagementSelectRejectsUnknownAccount(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)
	resetCheckinState()

	_, out := managementCall(t, http.MethodPost, "/select", `{"auth_index":"does-not-exist"}`)
	if out["error"] == nil {
		t.Fatalf("selecting an unknown account must report an error, got %+v", out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "not found") {
		t.Fatalf("error should explain the unknown account, got %q", msg)
	}
}

// credits?auth_index=... must return that account's snapshot; an unknown
// index must be a clean error rather than an empty success.
func TestManagementCreditsQuery(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-cr", "workbuddy-CN-cr.json", "cr", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	srv, _ := billingStub(t, 88)
	restore := setBillingBase(srv.URL)
	defer restore()

	_, out := managementCallQuery(t, "/credits", map[string][]string{"auth_index": {"idx-cr"}})
	accounts, _ := out["accounts"].([]any)
	if len(accounts) == 0 {
		t.Fatalf("credits query should return the requested account, got %+v", out)
	}
	first, _ := accounts[0].(map[string]any)
	if first["auth_index"] != "idx-cr" {
		t.Fatalf("wrong account returned: %+v", first)
	}

	credits, _ := first["credits"].(map[string]any)
	if credits["total_remain"] != float64(88) {
		t.Fatalf("plugin must return actual parsed credits: %+v", first)
	}

	_, out = managementCallQuery(t, "/credits", map[string][]string{"auth_index": {"nope"}})
	if out["error"] == nil {
		accounts, _ := out["accounts"].([]any)
		if len(accounts) == 0 {
			t.Fatal("an unknown auth_index must surface an error")
		}
	}
}

// Unknown paths must 404 inside the plugin's envelope (not leak a panic).
func TestManagementUnknownPathReturns404(t *testing.T) {
	status, out := managementCall(t, http.MethodGet, "/definitely-not-a-route", "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", status)
	}
	if out["error"] == nil {
		t.Fatal("404 body should carry an error message")
	}
}

// The dashboard payload must expose every automation switch the panel renders,
// so a missing field cannot silently read as "off" in the UI.
func TestManagementDashboardExposesAutomationSwitches(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-dash", "workbuddy-CN-dash.json", "dash", regionCN, false)
	installFakeAuthStore(t, store)
	resetCheckinState()

	resetAccountCache()
	srv, calls := billingStub(t, 88)
	restore := setBillingBase(srv.URL)
	defer restore()
	status, out := managementCall(t, http.MethodGet, "/accounts", "")
	if status != http.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", status)
	}
	accounts := out["accounts"].([]any)
	account := accounts[0].(map[string]any)
	credits, _ := account["credits"].(map[string]any)
	if credits["total_remain"] != float64(88) || account["plan"] != "Free" {
		t.Fatalf("cold accounts load must fetch plugin billing details: %+v", account)
	}
	before := atomic.LoadInt32(calls)
	managementCall(t, http.MethodGet, "/accounts", "")
	if atomic.LoadInt32(calls) != before {
		t.Fatal("warm dashboard must use billing cache")
	}
	if len(store.savedRecords()) != 0 {
		t.Fatal("initial dashboard read must not write account credentials")
	}

	for _, key := range []string{
		"accounts", "active_auth", "checkin_auto", "lifecycle_auto",
		"keepalive_auto", "growth_auto", "travel_auto", "schedule", "server_time", "server_time_iso", "summary",
	} {
		if _, ok := out[key]; !ok {
			t.Errorf("dashboard payload is missing %q", key)
		}
	}
	if _, err := time.Parse(time.RFC3339, out["server_time_iso"].(string)); err != nil {
		t.Fatal("dashboard clock must include timezone", err)
	}
	if _, ok := out["usage_report"]; ok {
		t.Error("usage_report was removed in 0.9.30 and must not reappear in the dashboard")
	}
}

// A non-GET/POST method must not reach a handler.
func TestManagementMethodNotAllowed(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)
	status, _ := managementCall(t, http.MethodDelete, "/accounts", "")
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected method should not dispatch, got status %d", status)
	}
}

// ---------------------------------------------------------------------------
// 每日免费额度接口（v0.9.34）
//
// 面板从这里读「每模型今日已用/剩余」。接口必须：按 auth_index 过滤、未知
// 账号报错而不是静默返回空、reset 明确说明只清本地计数。
// ---------------------------------------------------------------------------

func TestManagementDailyQuotaQueryAllAccounts(t *testing.T) {
	resetDailyQuotaState(t)
	store := newFakeAuthStore()
	store.put("idx-dq-1", "workbuddy-Global-acct-dq-1.json", "acct-dq-1", "global", false)
	installFakeAuthStore(t, store)

	recordDailyUsage(usageRecord("workbuddy-Global-acct-dq-1.json", "deepseek-v4.1-flash", 1234))

	status, out := managementCall(t, http.MethodGet, "/daily-quota", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if out["day"] == nil || out["day"] == "" {
		t.Error("payload must name the day it reports")
	}
	accounts, ok := out["accounts"].([]any)
	if !ok || len(accounts) != 1 {
		t.Fatalf("accounts = %#v, want one row", out["accounts"])
	}
	row, _ := accounts[0].(map[string]any)
	models, _ := row["models"].([]any)
	var found bool
	for _, m := range models {
		entry, _ := m.(map[string]any)
		if entry["model"] == "deepseek-v4.1-flash" {
			found = true
			if entry["used"] != float64(1234) {
				t.Errorf("used = %v, want 1234", entry["used"])
			}
		}
	}
	if !found {
		t.Error("recorded model missing from daily-quota payload")
	}
}

func TestManagementDailyQuotaQuerySingleAccount(t *testing.T) {
	resetDailyQuotaState(t)
	store := newFakeAuthStore()
	store.put("idx-dq-2", "workbuddy-Global-acct-dq-2.json", "acct-dq-2", "global", false)
	installFakeAuthStore(t, store)

	status, out := managementCallQuery(t, "/daily-quota", map[string][]string{"auth_index": {"idx-dq-2"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if out["auth_index"] != "idx-dq-2" {
		t.Errorf("auth_index = %v", out["auth_index"])
	}
	if _, ok := out["models"]; !ok {
		t.Error("single-account payload must carry a models list")
	}
}

func TestManagementDailyQuotaUnknownAccountErrors(t *testing.T) {
	resetDailyQuotaState(t)
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)

	status, out := managementCallQuery(t, "/daily-quota", map[string][]string{"auth_index": {"nope"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d (errors ride the body, like other panel endpoints)", status)
	}
	if out["error"] == nil {
		t.Fatal("unknown auth_index must report an error, not silently return empty data")
	}
}

// TestManagementDailyQuotaResetIsScopedAndHonest: reset clears local counters
// only, and must SAY so — an operator reading "reset ok" could otherwise assume
// real upstream allowance was restored.
func TestManagementDailyQuotaResetIsScopedAndHonest(t *testing.T) {
	resetDailyQuotaState(t)
	store := newFakeAuthStore()
	store.put("idx-dq-3", "workbuddy-Global-acct-dq-3.json", "acct-dq-3", "global", false)
	installFakeAuthStore(t, store)
	recordDailyUsage(usageRecord("workbuddy-Global-acct-dq-3.json", "deepseek-v4.1-flash", 4321))

	status, out := managementCallQueryMethod(t, http.MethodPost, "/daily-quota/reset", map[string][]string{"auth_index": {"idx-dq-3"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if out["status"] != "ok" {
		t.Fatalf("status field = %v", out["status"])
	}
	msg, _ := out["message"].(string)
	if !strings.Contains(msg, "不会恢复") {
		t.Errorf("reset response must state that upstream allowance is NOT restored, got %q", msg)
	}

	for _, row := range dailyQuotaSnapshotForAuth("workbuddy-Global-acct-dq-3.json") {
		if row.Model == "deepseek-v4.1-flash" && row.Used != 0 {
			t.Fatalf("reset did not clear counters: %d", row.Used)
		}
	}
}

// managementCallQueryMethod is managementCallQuery with an explicit method, for
// POST endpoints that also read query parameters.
func managementCallQueryMethod(t *testing.T, method, subPath string, query map[string][]string) (int, map[string]any) {
	t.Helper()
	var parsed pluginapi.ManagementRequest
	if err := json.Unmarshal(mustManagementRequest(method, subPath, "", nil), &parsed); err != nil {
		t.Fatalf("decode management request: %v", err)
	}
	parsed.Query = query
	raw, err := handleManagement(mustJSON(parsed))
	if err != nil {
		t.Fatalf("handleManagement(%s %s): %v", method, subPath, err)
	}
	return decodeManagementResponse(t, raw)
}

// ---------------------------------------------------------------------------
// 模型列表接口（0.9.42）
//
// 卡片原本只显示"已支持 N 个模型"—— 纯数字回答不了任何实际问题：模型缺失时
// 运维需要知道**具体是哪些**，才能判断是区域路由、excluded 过滤、配置 pin
// 还是上游目录的问题。
// ---------------------------------------------------------------------------

func TestManagementModelsQueryAllAccounts(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-m1", "workbuddy-CN-u-m1.json", "u-m1", "cn", false)
	installFakeAuthStore(t, store)

	status, out := managementCall(t, http.MethodGet, "/models", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	accounts, ok := out["accounts"].([]any)
	if !ok || len(accounts) != 1 {
		t.Fatalf("accounts = %#v, want one row", out["accounts"])
	}
	row, _ := accounts[0].(map[string]any)
	// A credential with no reachable upstream still yields a usable list (the
	// realm static fallback) rather than an error — the panel must render.
	if _, hasErr := row["error"]; hasErr {
		t.Fatalf("unexpected error for a valid credential: %v", row["error"])
	}
	if _, hasCount := row["count"]; !hasCount {
		t.Error("row must report a model count")
	}
	if _, hasModels := row["models"]; !hasModels {
		t.Error("row must carry the model list, not just a count")
	}
}

func TestManagementModelsQuerySingleAccount(t *testing.T) {
	store := newFakeAuthStore()
	store.put("idx-m2", "workbuddy-Global-u-m2.json", "u-m2", "global", false)
	installFakeAuthStore(t, store)

	status, out := managementCallQuery(t, "/models", map[string][]string{"auth_index": {"idx-m2"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if out["auth_index"] != "idx-m2" {
		t.Errorf("auth_index = %v", out["auth_index"])
	}
	if out["region"] != "intl" {
		t.Errorf("region = %v, want intl", out["region"])
	}
}

func TestManagementModelsUnknownAccountErrors(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)

	status, out := managementCallQuery(t, "/models", map[string][]string{"auth_index": {"nope"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d (errors ride the body)", status)
	}
	if out["error"] == nil {
		t.Fatal("unknown auth_index must report an error, not an empty list")
	}
}

// TestManagementModelsRefreshRequiresAuthIndex: a refresh without a target is
// ambiguous (refresh which credential?), so it must be rejected rather than
// silently refreshing everything.
func TestManagementModelsRefreshRequiresAuthIndex(t *testing.T) {
	store := newFakeAuthStore()
	installFakeAuthStore(t, store)

	status, out := managementCall(t, http.MethodPost, "/models/refresh", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if out["error"] == nil {
		t.Fatal("refresh without auth_index must be rejected")
	}
}

// TestModelsForCredentialSortsByID: an unstable order makes the catalog look
// like it changed between refreshes when it did not.
func TestModelsForCredentialSortsByID(t *testing.T) {
	models := []panelModel{{ID: "zeta"}, {ID: "alpha"}, {ID: "mid"}}
	sortPanelModels(models)
	for i := 1; i < len(models); i++ {
		if models[i-1].ID > models[i].ID {
			t.Fatalf("not sorted: %v", models)
		}
	}
}
