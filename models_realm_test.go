package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// realmTestModels builds a ModelInfo list with the given IDs for cache tests.
func realmTestModels(ids ...string) []pluginapi.ModelInfo {
	out := make([]pluginapi.ModelInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, pluginapi.ModelInfo{ID: id, Name: id, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}})
	}
	return out
}

// resetDynamicModelsCache empties the package-level realm cache — tests share
// the process, and a stale entry from one test would poison another's hints.
func resetDynamicModelsCache() {
	dynamicModelsCache.Lock()
	dynamicModelsCache.realms = map[string]realmModelsEntry{}
	dynamicModelsCache.Unlock()
}

// makeJWTWithIss builds a minimal JWT-shaped token whose payload carries the
// given iss claim — enough for isGlobalToken's base64/iss decode.
func makeJWTWithIss(iss string) string {
	payload, _ := json.Marshal(map[string]string{"iss": iss})
	return "hdr." + base64.URLEncoding.EncodeToString(payload) + ".sig"
}

// TestModelsEndpointFor pins the v0.12.18 realm→discovery-endpoint routing.
// Regression guard: the old callModelsAPI only special-cased Global and sent
// Intl (codebuddy.ai) tokens to the CN endpoint, so Intl accounts were shown
// the CN catalog and chat calls with CN-only models (deepseek-v4-flash) died
// on the Intl gateway with code 11102 "service info not found".
func TestModelsEndpointFor(t *testing.T) {
	cases := []struct {
		realm      string
		wantURL    string
		wantOrigin string
	}{
		{"cn", "https://copilot.tencent.com/console/enterprises/personal/models", "https://www.codebuddy.cn"},
		{"global", "https://www.workbuddy.ai/console/enterprises/personal/models", "https://www.workbuddy.ai"},
		{"intl", "https://www.workbuddy.ai/console/enterprises/personal/models", "https://www.workbuddy.ai"},
		{"", "https://copilot.tencent.com/console/enterprises/personal/models", "https://www.codebuddy.cn"}, // unknown → CN default
	}
	for _, c := range cases {
		url, origin := modelsEndpointFor(c.realm)
		if url != c.wantURL || origin != c.wantOrigin {
			t.Errorf("realm=%q: got (%s, %s), want (%s, %s)", c.realm, url, origin, c.wantURL, c.wantOrigin)
		}
	}
}

// TestRealmForStorage covers realm classification across the three storage
// shapes that reach model.for_auth: nested plugin OAuth files, flat CPA-UI
// imports, and legacy files with neither domain nor region (JWT iss fallback).
func TestRealmForStorage(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		token string
		want  string
	}{
		{"nested intl domain", `{"auth":{"domain":"codebuddy.ai","accessToken":"t"}}`, "t", "intl"},
		{"nested global region is public Intl", `{"auth":{"region":"global"}}`, "t", "intl"},
		{"nested global domain overrides public Intl", `{"auth":{"region":"intl","domain":"workbuddy.ai"}}`, "t", "intl"},
		{"nested cn region", `{"auth":{"region":"cn"}}`, "t", "cn"},
		{"flat intl domain", `{"domain":"www.codebuddy.ai"}`, "t", "intl"},
		{"flat global domain is public Intl", `{"domain":"workbuddy.ai"}`, "t", "intl"},
		{"flat region field", `{"region":"intl"}`, "t", "intl"},
		{"legacy iss global is public Intl", `{}`, makeJWTWithIss("https://auth.workbuddy.ai/realms/workbuddy"), "intl"},
		{"legacy iss cn", `{}`, makeJWTWithIss("https://codebuddy.cn"), "cn"},
		{"garbage json + cn token", `not-json`, "t", "cn"},
	}
	for _, c := range cases {
		if got := realmForStorage([]byte(c.raw), c.token); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	if got := serviceRealmForStorage([]byte(`{"auth":{"region":"intl","domain":"workbuddy.ai"}}`), "t"); got != regionGlobal {
		t.Errorf("legacy WorkBuddy service = %q, want global", got)
	}
	if got := serviceRealmForStorage([]byte(`{"auth":{"region":"intl","domain":"codebuddy.ai"}}`), "t"); got != regionGlobal {
		t.Errorf("CodeBuddy service = %q, want intl", got)
	}
}

// TestRealmFromRegionDomain pins the pair-mapping including the empty default
// (unknown pair → "" so the caller falls through to the next probe).
func TestRealmFromRegionDomain(t *testing.T) {
	if got := realmFromRegionDomain("", ""); got != "" {
		t.Errorf("empty pair: got %q, want empty", got)
	}
	if got := realmFromRegionDomain("INTL", ""); got != "intl" {
		t.Errorf("case-insensitive region: got %q", got)
	}
	if got := realmFromRegionDomain("", "sub.codebuddy.ai"); got != "intl" {
		t.Errorf("domain suffix: got %q", got)
	}
	if got := realmFromRegionDomain("unknown", "example.com"); got != "" {
		t.Errorf("unknown pair: got %q, want empty", got)
	}
}

// TestDynamicModelsCachePerRealm proves one realm's discovery answer never
// satisfies another realm's model.for_auth (the old single-entry cache let
// CN answers leak into Intl listings).
func TestDynamicModelsCachePerRealm(t *testing.T) {
	resetDynamicModelsCache()
	defer resetDynamicModelsCache()
	cn := wbModels()[:1]
	intl := realmTestModels("intl-only-model")
	storeDynamicModels("cn:test-cn", cn)
	storeDynamicModels("intl:test-intl", intl)

	gotCN, okCN := cachedDynamicModels("cn:test-cn")
	if !okCN || len(gotCN) != 1 || gotCN[0].ID != cn[0].ID {
		t.Fatalf("cn cache: ok=%v len=%d", okCN, len(gotCN))
	}
	gotIntl, okIntl := cachedDynamicModels("intl:test-intl")
	if !okIntl || len(gotIntl) != len(intl) {
		t.Fatalf("intl cache: ok=%v len=%d", okIntl, len(gotIntl))
	}
	if gotCN[0].ID == gotIntl[0].ID {
		t.Fatalf("cache collision: cn=%s intl=%s", gotCN[0].ID, gotIntl[0].ID)
	}
	if _, ok := cachedDynamicModels("global:test-global"); ok {
		t.Fatalf("unknown realm must miss")
	}
}

// TestStaticModelsPerRealm keeps the realm catalogs separate. The Global/Intl
// static catalog is the reference-measured desktop list for the international
// gateway (workbuddy.ai and codebuddy.ai resolve to the same address): the
// v0.12.18 contamination rule stays in force for the CN-only DeepSeek entries,
// while models both realms serve are expected on both sides.
func TestStaticModelsPerRealm(t *testing.T) {
	// The Intl/Global gateway rejects these CN entries with 11102 — they must
	// never appear in the Global/Intl static fallback again.
	cnOnly := []string{"deepseek-v4-flash", "deepseek-v4-pro", "kimi-k3-1"}
	// Models the measured desktop catalogs show on BOTH sides.
	shared := []string{
		"hy4-preview-f", "hy3", "deepseek-v4.1-flash",
		"glm-5.3", "glm-5.3-flash", "glm-5.2",
		"kimi-k2.8-preview", "kimi-k2.6",
	}
	for _, realm := range []string{"intl", "global"} {
		catalog := staticModelsForRealm(realm)
		if len(catalog) == 0 {
			t.Fatalf("%s static catalog must not be empty", realm)
		}
		for _, m := range catalog {
			for _, banned := range cnOnly {
				if strings.EqualFold(m.ID, banned) {
					t.Errorf("%s static catalog must not advertise CN-only model %s", realm, banned)
				}
			}
		}
		for _, want := range shared {
			if !realmCatalogHas(catalog, want) {
				t.Errorf("%s static catalog must carry desktop-aligned model %s", realm, want)
			}
		}
		// Legacy tail kept for credentials already routing to it.
		if !realmCatalogHas(catalog, "hy4-preview") {
			t.Errorf("%s static catalog must keep legacy hy4-preview", realm)
		}
		if !realmCatalogHas(staticModelsForRealm("global"), "gpt-6-astra") {
			t.Errorf("global static catalog must include the international-only GPT tier")
		}
	}
	// CN-only desktop entries stay on the CN side only.
	for _, want := range []string{"glm-5.1", "glm-5v-turbo", "minimax-m3", "kimi-k2.7", "kimi-k3-1"} {
		if !realmCatalogHas(staticModelsForRealm("cn"), want) {
			t.Errorf("cn static catalog must carry %s", want)
		}
	}
	if realmCatalogHas(staticModelsForRealm("global"), "glm-5.1") ||
		realmCatalogHas(staticModelsForRealm("global"), "minimax-m3") {
		t.Errorf("global static catalog must not carry CN-exclusive desktop entries")
	}
	if !realmCatalogHas(staticModelsForRealm("cn"), "deepseek-v4-flash") {
		t.Errorf("cn static catalog must keep the CN DeepSeek models")
	}
	// v0.9.8: DeepSeek V4.1 Flash launched 2026-09-10 with WorkBuddy/CodeBuddy
	// as official launch partners (deepseek.com news260910) — the CN static
	// fallback must carry it during the rollout window.
	if !realmCatalogHas(staticModelsForRealm("cn"), "deepseek-v4.1-flash") {
		t.Errorf("cn static catalog must include deepseek-v4.1-flash (official 2026-09-10 launch)")
	}
}

func realmCatalogHas(models []pluginapi.ModelInfo, id string) bool {
	for _, m := range models {
		if strings.EqualFold(m.ID, id) {
			return true
		}
	}
	return false
}

// TestParsePinnedModelList covers quoting, YAML flow lists, dedup and empties.
func TestParsePinnedModelList(t *testing.T) {
	if got := parsePinnedModelList("hy4-preview"); !pinnedSame(got, []string{"hy4-preview"}) {
		t.Errorf("single: got %#v", got)
	}
	if got := parsePinnedModelList(` "hy4-preview, claude-sonnet-5" `); !pinnedSame(got, []string{"hy4-preview", "claude-sonnet-5"}) {
		t.Errorf("quoted pair: got %#v", got)
	}
	if got := parsePinnedModelList("[A, b ,a]"); !pinnedSame(got, []string{"A", "b"}) {
		t.Errorf("flow dedup (case-insensitive): got %#v", got)
	}
	if got := parsePinnedModelList(""); got != nil {
		t.Errorf("empty: got %#v", got)
	}
	if got := parsePinnedModelList(",, ,"); got != nil {
		t.Errorf("commas only: got %#v", got)
	}
}

func pinnedSame(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// resetPinnedModels empties the pinned config between tests.
func resetPinnedModels() {
	pinnedModelsMu.Lock()
	pinnedModels = map[string][]string{}
	pinnedModelsMu.Unlock()
}

// configYAMLEnvelope wraps raw YAML in the plugin.register/reconfigure JSON
// envelope the host actually sends (config_yaml carries the YAML bytes).
func configYAMLEnvelope(yaml string) []byte {
	req := struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{ConfigYAML: []byte(yaml)}
	raw, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestConfigurePinsModelsRealm pins the config_yaml → realm accessor path,
// including metadata reuse for known IDs and generic metadata for unknowns.
func TestConfigurePinsModelsRealm(t *testing.T) {
	resetPinnedModels()
	defer resetPinnedModels()
	configure(configYAMLEnvelope("enabled: true\nmodels_intl: \"hy4-preview, claude-sonnet-5\"\n"))
	got := pinnedModelsForRealm("intl")
	if len(got) != 2 || got[0].ID != "hy4-preview" || got[1].ID != "claude-sonnet-5" {
		t.Fatalf("pinned intl: got %#v", got)
	}
	if got[0].ContextLength != 1000000 {
		t.Fatalf("known ID must reuse static metadata, got %#v", got[0])
	}
	if got[1].OwnedBy != providerName || got[1].ContextLength != 0 {
		t.Fatalf("unknown ID gets generic metadata, got %#v", got[1])
	}
	if got := pinnedModelsForRealm("cn"); got != nil {
		t.Fatalf("cn unpinned: got %#v", got)
	}
	// Reconfigure without the key resets the realm to discovery+static.
	configure(configYAMLEnvelope("enabled: true\n"))
	if got := pinnedModelsForRealm("intl"); got != nil {
		t.Fatalf("missing key must reset pins, got %#v", got)
	}
}

// TestFetchDynamicModelsPinnedWins: a pinned realm skips discovery entirely.
func TestFetchDynamicModelsPinnedWins(t *testing.T) {
	resetPinnedModels()
	resetDynamicModelsCache()
	defer func() { resetPinnedModels(); resetDynamicModelsCache() }()
	pinnedModelsMu.Lock()
	pinnedModels["intl"] = []string{"claude-sonnet-5"}
	pinnedModelsMu.Unlock()
	calls := 0
	orig := discoverModelsFn
	discoverModelsFn = func(token, realm string) ([]pluginapi.ModelInfo, error) {
		calls++
		return realmTestModels("should-not-be-used"), nil
	}
	defer func() { discoverModelsFn = orig }()
	raw := []byte(`{"auth":{"domain":"codebuddy.ai","accessToken":"tok"}}`)
	got := fetchDynamicModelsFromStorage(raw)
	if len(got) != 1 || got[0].ID != "claude-sonnet-5" {
		t.Fatalf("pinned output expected, got %#v", got)
	}
	if calls != 0 {
		t.Fatalf("discovery must be skipped when pinned, calls=%d", calls)
	}
}

// TestFetchDynamicModelsIntlStaticByDefault verifies that Intl never probes
// the retired/500 model endpoint unless the user explicitly pins models_intl.
func TestFetchDynamicModelsIntlUsesWBByDefault(t *testing.T) {
	resetPinnedModels()
	resetDynamicModelsCache()
	defer func() { resetPinnedModels(); resetDynamicModelsCache() }()
	old := discoverModelsFn
	defer func() { discoverModelsFn = old }()
	calls := 0
	discoverModelsFn = func(token, realm string) ([]pluginapi.ModelInfo, error) {
		calls++
		if realm != regionGlobal || token != "tok" {
			t.Error(token, realm)
		}
		return realmTestModels("foreign-from-wb"), nil
	}
	got := fetchDynamicModelsFromStorage([]byte(`{"auth":{"domain":"codebuddy.ai","accessToken":"tok"}}`))
	if calls != 1 || len(got) != 1 || got[0].ID != "foreign-from-wb" {
		t.Fatal(calls, got)
	}
	st := realmModelStateFor(regionGlobal)
	if st == nil || st.Source != "discovery" {
		t.Fatal(st)
	}
}

func TestFetchDynamicModelsFallbackPerRealm(t *testing.T) {
	resetPinnedModels()
	resetDynamicModelsCache()
	defer func() { resetPinnedModels(); resetDynamicModelsCache() }()
	orig := discoverModelsFn
	discoverModelsFn = func(token, realm string) ([]pluginapi.ModelInfo, error) {
		return nil, errors.New("models API status 500")
	}
	defer func() { discoverModelsFn = orig }()

	intlRaw := []byte(`{"auth":{"domain":"codebuddy.ai","accessToken":"tok"}}`)
	gotIntl := fetchDynamicModelsFromStorage(intlRaw)
	if !realmCatalogHas(gotIntl, "hy4-preview") {
		t.Fatalf("intl fallback must be the intl catalog, got %#v", gotIntl)
	}
	if realmCatalogHas(gotIntl, "deepseek-v4-flash") {
		t.Fatalf("intl fallback must never advertise deepseek-v4-flash (11102 regression)")
	}

	cnRaw := []byte(`{"auth":{"region":"cn","accessToken":"tok"}}`)
	gotCN := fetchDynamicModelsFromStorage(cnRaw)
	if !realmCatalogHas(gotCN, "deepseek-v4-flash") {
		t.Fatalf("cn fallback must keep the CN catalog, got %#v", gotCN)
	}
}

// TestModelHintForRealm checks the 11102 error hint: cached discovery wins,
// the static fallback is the REALM's own catalog (Intl accounts must never
// be shown the CN list), and the list is bounded.
func TestModelHintForRealm(t *testing.T) {
	resetDynamicModelsCache()
	defer resetDynamicModelsCache()
	defer setGloballyDisabledModels(nil)
	defer setGloballyEnabledModels(nil)
	enabled := []string{"intl-real-model"}
	for _, realm := range []string{regionCN, regionIntl} {
		for _, model := range staticModelsForRealm(realm) {
			enabled = append(enabled, model.ID)
		}
	}
	setGloballyEnabledModels(enabled)
	hint := modelHintForRealm("intl")
	if strings.Contains(hint, "deepseek-v4-flash") {
		t.Fatalf("intl static hint must not list CN-only models, got: %s", hint)
	}
	if !strings.Contains(hint, "hy4-preview") {
		t.Fatalf("intl static hint must list the intl catalog, got: %s", hint)
	}
	if !strings.Contains(hint, "static INTL catalog") || !strings.Contains(hint, "静态 INTL 目录") {
		t.Fatalf("intl static hint must be labeled bilingual, got: %s", hint)
	}
	// CN realm keeps the full CN catalog in its static hint.
	hintCN := modelHintForRealm("cn")
	if !strings.Contains(hintCN, "deepseek-v4-flash") || !strings.Contains(hintCN, "static CN catalog") {
		t.Fatalf("cn static hint must list the CN catalog, got: %s", hintCN)
	}
	// Disabled catalog entries must not reappear in error hints.
	setGloballyDisabledModels([]string{"intl-real-model"})
	storeDynamicModels("intl", realmTestModels("intl-real-model"))
	hint = modelHintForRealm("intl")
	if strings.Contains(hint, "intl-real-model") {
		t.Fatalf("globally disabled model leaked into error hint: %s", hint)
	}
	setGloballyDisabledModels(nil)
	hint = modelHintForRealm("intl")
	if strings.Contains(hint, "static INTL catalog") {
		t.Fatalf("cached catalog must not be labeled static, got: %s", hint)
	}
	if !strings.Contains(hint, "cached realm catalog") {
		t.Fatalf("cached label missing, got: %s", hint)
	}
}

// TestTranslateChatUpstreamError_11102 pins the bilingual actionable rewrite
// for the Intl 11102 rejection and the untouched passthrough for everything
// else (log parsers rely on the "upstream <status>: <payload>" shape).
func TestTranslateChatUpstreamError_11102(t *testing.T) {
	payload := `{"code":11102,"msg":"model [deepseek-v4-flash] service info not found","requestId":"6b416a6b-b9c9-499e-ae30-8d92cb4e0e04"}`
	sa := &storedAuth{}
	sa.Auth.Domain = "codebuddy.ai"

	_, err := translateChatUpstreamError(http.StatusBadRequest, payload, sa)
	msg := err.Error()
	for _, want := range []string{"11102", "workbuddy.ai", "deepseek-v4-flash"} {
		if !strings.Contains(msg, want) {
			t.Errorf("11102 error missing %q: %s", want, msg)
		}
	}
	if !strings.Contains(msg, "static INTL catalog") && !strings.Contains(msg, "cached realm catalog") {
		t.Errorf("11102 error must carry a realm model catalog hint: %s", msg)
	}

	// Global account → its gateway is named, not the Intl one.
	saG := &storedAuth{}
	saG.Auth.Domain = "workbuddy.ai"
	_, gotErr := translateChatUpstreamError(http.StatusBadRequest, payload, saG)
	if !strings.Contains(gotErr.Error(), "workbuddy.ai") {
		t.Errorf("global account error must name the global gateway: %s", gotErr.Error())
	}

	// Non-11102 failure keeps the historical passthrough shape.
	other := `{"code":1001,"message":"unauthorized"}`
	gotStatus, gotErr2 := translateChatUpstreamError(http.StatusUnauthorized, other, sa)
	if gotStatus != http.StatusUnauthorized {
		t.Errorf("passthrough status = %d, want 401", gotStatus)
	}
	if !strings.HasPrefix(gotErr2.Error(), "upstream 401: ") {
		t.Errorf("non-11102 must keep raw shape, got: %s", gotErr2.Error())
	}

	// Marker-based detection for non-JSON envelopes.
	if !isModelNotRegistered(http.StatusBadRequest, `<html>11102 service info not found</html>`) {
		t.Errorf("marker path must detect wrapped 11102 payloads")
	}
	if isModelNotRegistered(http.StatusInternalServerError, payload) {
		t.Errorf("only 400 carries the 11102 rewrite")
	}
}
