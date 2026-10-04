package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestConfigureModelsDisabledAndReconfigure(t *testing.T) {
	defer setGloballyDisabledModels(nil)
	defer setGloballyEnabledModels(nil)
	configure(configYAMLEnvelope("models_disabled: [DeepSeek-V4.1-Flash, custom-model, deepseek-v4.1-flash] # inline\nmodels_enabled: [DeepSeek-V4.1-Flash, enabled-model]\n"))
	if got := currentGloballyDisabledModels(); len(got) != 2 || got[0] != "DeepSeek-V4.1-Flash" || got[1] != "custom-model" {
		t.Fatalf("models_disabled = %#v, want deduplicated IDs", got)
	}
	if got := currentGloballyEnabledModels(); len(got) != 2 || got[0] != "DeepSeek-V4.1-Flash" || got[1] != "enabled-model" {
		t.Fatalf("models_enabled = %#v, want deduplicated IDs", got)
	}
	if !isGloballyDisabledModel("DeepSeek-V4.1-Flash") {
		t.Fatal("models_disabled must override models_enabled")
	}
	configure(configYAMLEnvelope("models_disabled: [other-model]\n"))
	if got := currentGloballyDisabledModels(); len(got) != 1 || got[0] != "other-model" {
		t.Fatalf("reconfigured models_disabled = %#v, want replacement list", got)
	}
	if got := currentGloballyEnabledModels(); len(got) != 0 {
		t.Fatalf("omitted models_enabled should reset allowlist, got %#v", got)
	}
	configure(configYAMLEnvelope("enabled: true\n"))
	if got := currentGloballyDisabledModels(); len(got) != 0 {
		t.Fatalf("removing models_disabled must clear runtime state, got %#v", got)
	}
	if !isGloballyDisabledModel("unconfigured-model") {
		t.Fatal("models must remain disabled when no model is explicitly enabled")
	}
}

func TestModelsAreDisabledByDefaultUntilExplicitlyEnabled(t *testing.T) {
	defer setGloballyDisabledModels(nil)
	defer setGloballyEnabledModels(nil)
	setGloballyDisabledModels(nil)
	setGloballyEnabledModels(nil)

	models := realmTestModels("model-one", "model-two")
	if got := filterGloballyDisabledModels(models); len(got) != 0 {
		t.Fatalf("models must default to disabled, got %v", discoveryIDs(got))
	}
	setGloballyEnabledModels([]string{"model-one"})
	if got := filterGloballyDisabledModels(models); len(got) != 1 || got[0].ID != "model-one" {
		t.Fatalf("allowlist should register only checked model, got %v", discoveryIDs(got))
	}
}

func TestFilterGloballyDisabledModelsExactCaseInsensitiveAndNonMutating(t *testing.T) {
	defer setGloballyDisabledModels(nil)
	defer setGloballyEnabledModels(nil)
	setGloballyEnabledModels([]string{"deepseek-v4.1-flash", "deepseek-v4.1-flash-extended", "glm-5.3"})
	setGloballyDisabledModels([]string{"DEEPSEEK-V4.1-FLASH"})
	models := realmTestModels("deepseek-v4.1-flash", "deepseek-v4.1-flash-extended", "glm-5.3")
	got := filterGloballyDisabledModels(models)
	if ids := discoveryIDs(got); len(ids) != 2 || ids[0] != "deepseek-v4.1-flash-extended" || ids[1] != "glm-5.3" {
		t.Fatalf("filtered IDs = %#v", ids)
	}
	if len(models) != 3 || models[0].ID != "deepseek-v4.1-flash" {
		t.Fatalf("filter mutated input slice: %#v", models)
	}
}

func TestModelRegistrationFiltersGlobalAndHostExcludedModels(t *testing.T) {
	defer setGloballyDisabledModels(nil)
	defer setGloballyEnabledModels(nil)
	resetPinnedModels()
	defer resetPinnedModels()
	defer resetDynamicModelsCache()
	setGloballyEnabledModels([]string{"hy4-preview-f", "hy3", "discovered-model", "host-excluded-model", "available-model"})
	setGloballyDisabledModels([]string{"Hy4-Preview-F", "custom-intl-model", "discovered-model"})
	customStaticModels.Lock()
	oldCustom := customStaticModels.models
	customStaticModels.models = []customStaticModel{{ID: "custom-intl-model", Name: "Custom Intl", Channel: "intl_global", Enabled: true}}
	customStaticModels.Unlock()
	t.Cleanup(func() {
		customStaticModels.Lock()
		customStaticModels.models = oldCustom
		customStaticModels.Unlock()
	})

	staticReq, _ := json.Marshal(pluginapi.StaticModelRequest{Host: pluginapi.HostConfigSummary{
		ExcludedModels: map[string][]string{providerName: {"hy3"}},
	}})
	staticRaw, err := handleModelStatic(staticReq)
	if err != nil {
		t.Fatal(err)
	}
	staticResp := decodeModelResponse(t, staticRaw)
	if realmCatalogHas(staticResp.Models, "hy4-preview-f") || realmCatalogHas(staticResp.Models, "hy3") {
		t.Fatalf("static response retained globally/host excluded model: %v", discoveryIDs(staticResp.Models))
	}

	resetDynamicModelsCache()
	origDiscover := discoverModelsFn
	discoverModelsFn = func(string, string) ([]pluginapi.ModelInfo, error) {
		return realmTestModels("discovered-model", "host-excluded-model", "available-model"), nil
	}
	t.Cleanup(func() { discoverModelsFn = origDiscover })
	request := pluginapi.AuthModelRequest{
		AuthProvider: providerName,
		StorageJSON:  []byte(`{"auth":{"domain":"workbuddy.ai","accessToken":"token"}}`),
		Host:         pluginapi.HostConfigSummary{ExcludedModels: map[string][]string{providerName: {"HOST-EXCLUDED-MODEL"}}},
	}
	authReq, _ := json.Marshal(request)
	authRaw, err := handleModelForAuth(authReq)
	if err != nil {
		t.Fatal(err)
	}
	authResp := decodeModelResponse(t, authRaw)
	if realmCatalogHas(authResp.Models, "discovered-model") || realmCatalogHas(authResp.Models, "host-excluded-model") {
		t.Fatalf("auth response retained globally/host excluded model: %v", discoveryIDs(authResp.Models))
	}
	if !realmCatalogHas(authResp.Models, "available-model") {
		t.Fatalf("auth response lost allowed model: %v", discoveryIDs(authResp.Models))
	}
}

func TestGlobalModelCatalogMarksDisabledAndIncludesAvailableSources(t *testing.T) {
	defer setGloballyDisabledModels(nil)
	defer setGloballyEnabledModels(nil)
	defer resetPinnedModels()
	defer resetDynamicModelsCache()
	setGloballyEnabledModels([]string{"catalog-custom-model"})
	setGloballyDisabledModels([]string{"DISCOVERED-MODEL", "hy4-preview-f", "retired-model"})
	customStaticModels.Lock()
	oldCustom := customStaticModels.models
	customStaticModels.models = []customStaticModel{{ID: "catalog-custom-model", Name: "Catalog Custom", Channel: "intl_global", Enabled: true}}
	customStaticModels.Unlock()
	t.Cleanup(func() {
		customStaticModels.Lock()
		customStaticModels.models = oldCustom
		customStaticModels.Unlock()
	})

	store := newFakeAuthStore()
	store.put("catalog-index", "workbuddy-Global-catalog.json", "catalog", regionGlobal, false)
	installFakeAuthStore(t, store)
	origDiscover := discoverModelsFn
	discoverModelsFn = func(string, string) ([]pluginapi.ModelInfo, error) {
		return realmTestModels("discovered-model"), nil
	}
	t.Cleanup(func() { discoverModelsFn = origDiscover })

	catalog := handleGlobalModelCatalog()
	found := map[string]panelModel{}
	for _, model := range catalog {
		found[strings.ToLower(model.ID)] = model
	}
	if model, ok := found["discovered-model"]; !ok || !model.Disabled {
		t.Errorf("discovered model should be present and disabled: %+v, found=%v", model, ok)
	}
	if model, ok := found["hy4-preview-f"]; !ok || !model.Disabled {
		t.Errorf("static model should be present and disabled: %+v, found=%v", model, ok)
	}
	if model, ok := found["catalog-custom-model"]; !ok || model.Name != "Catalog Custom" || model.Disabled {
		t.Errorf("custom model should be present: %+v, found=%v", model, ok)
	}
	if model, ok := found["retired-model"]; !ok || !model.Disabled || model.Name != "当前目录未返回" {
		t.Errorf("saved disabled model missing from current catalog should remain manageable: %+v, found=%v", model, ok)
	}
}

func TestModelsDisabledYAMLListParsesAsInts(t *testing.T) {
	cfg := pluginConfigScalars([]byte("models_disabled:\n  - one\n  - two\n"))
	if got := cfg.listValue("models_disabled"); len(got) != 2 || strings.Join(got, ",") != "one,two" {
		t.Fatalf("models_disabled list = %#v", got)
	}
}

func decodeModelResponse(t *testing.T, raw []byte) pluginapi.ModelResponse {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("model handler returned error: %s", raw)
	}
	var response pluginapi.ModelResponse
	if err := json.Unmarshal(env.Result, &response); err != nil {
		t.Fatalf("decode model response: %v", err)
	}
	return response
}
