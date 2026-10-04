package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func resetCustomStaticModels() {
	customStaticModels.Lock()
	customStaticModels.models = nil
	customStaticModels.Unlock()
}

func TestParseCustomStaticModels(t *testing.T) {
	got := parseCustomStaticModels([]byte(`models:
  - id: cn-model
    name: CN Model
    channel: cn
    context: 131072
    max_tokens: 4096
  - id: intl-global-model
    channel: intl_global
    context: 262144
  - id: legacy-global
    realm: GLOBAL
  - id: disabled-model
    enabled: false
  - id: invalid-channel
    channel: mars
  - id: negative-context
    context: -1
  - id: cn-model
    name: Duplicate
`))
	if len(got) != 4 {
		t.Fatalf("parsed models = %#v, want four valid entries", got)
	}
	if got[0].ID != "cn-model" || got[0].Channel != "cn" || got[0].Name != "CN Model" || got[0].Context != 131072 || got[0].MaxTokens != 4096 || !got[0].Enabled {
		t.Fatalf("cn model = %#v", got[0])
	}
	if got[1].Channel != "intl_global" || got[1].Name != "intl-global-model" {
		t.Fatalf("intl_global model = %#v", got[1])
	}
	if got[2].Channel != "intl_global" {
		t.Fatalf("legacy global model = %#v", got[2])
	}
	if got[3].ID != "cn-model" {
		t.Fatalf("duplicate is retained for stable input order before realm merge: %#v", got)
	}
}

func TestParseCustomStaticModelsGrouped(t *testing.T) {
	got := parseCustomStaticModels([]byte(`models:
  intl_global:
    - deepseek-v4.1-flash
    - gpt-6-astra
    - id: custom-intl-model
      name: Custom Intl Model
      context: 2000000
      max_tokens: 16384
  cn:
    - deepseek-v4.1-flash
`))
	if len(got) != 4 {
		t.Fatalf("parsed grouped models count = %d, want 4: %#v", len(got), got)
	}
	if got[0].ID != "deepseek-v4.1-flash" || got[0].Channel != "intl_global" || got[0].Name != "DeepSeek V4.1 Flash" || got[0].Context != 1000000 || got[0].MaxTokens != 8192 {
		t.Fatalf("intl_global string item = %#v", got[0])
	}
	if got[1].ID != "gpt-6-astra" || got[1].Channel != "intl_global" || got[1].Name != "GPT-6 Astra" {
		t.Fatalf("intl_global gpt-6 item = %#v", got[1])
	}
	if got[2].ID != "custom-intl-model" || got[2].Context != 2000000 || got[2].MaxTokens != 16384 {
		t.Fatalf("intl_global mapping item = %#v", got[2])
	}
	if got[3].ID != "deepseek-v4.1-flash" || got[3].Channel != "cn" {
		t.Fatalf("cn item = %#v", got[3])
	}
}

func TestCustomStaticModelsByChannelAndMetadata(t *testing.T) {
	defer resetCustomStaticModels()
	configure(configYAMLEnvelope(`models:
  cn:
    - id: custom-cn
      name: Custom CN
      context: 100000
      max_tokens: 8192
  intl_global:
    - id: custom-intl-global
      name: Custom Intl Global
      context: 200000
      max_tokens: 4096
    - id: disabled
      enabled: false
`))

	cn := staticModelsForRealm(regionCN)
	global := staticModelsForRealm(regionGlobal)
	intl := staticModelsForRealm(regionIntl)
	if !realmCatalogHas(cn, "custom-cn") || realmCatalogHas(global, "custom-cn") || realmCatalogHas(intl, "custom-cn") {
		t.Fatal("CN-only model crossed channel boundary")
	}
	if realmCatalogHas(cn, "custom-intl-global") || !realmCatalogHas(global, "custom-intl-global") || !realmCatalogHas(intl, "custom-intl-global") {
		t.Fatal("intl_global model did not share only Intl/Global")
	}
	if realmCatalogHas(cn, "disabled") || realmCatalogHas(global, "disabled") || realmCatalogHas(intl, "disabled") {
		t.Fatal("disabled custom model must not be advertised")
	}

	var custom pluginapi.ModelInfo
	for _, model := range global {
		if model.ID == "custom-intl-global" {
			custom = model
		}
	}
	if custom.Name != "Custom Intl Global" || custom.ContextLength != 200000 || custom.MaxCompletionTokens != 4096 || !custom.UserDefined {
		t.Fatalf("custom metadata = %#v", custom)
	}
	if len(custom.SupportedGenerationMethods) != 1 || custom.SupportedGenerationMethods[0] != "chat" {
		t.Fatalf("custom methods = %#v", custom.SupportedGenerationMethods)
	}
}

func TestConfigureCustomModelsReplacesPreviousConfig(t *testing.T) {
	defer resetCustomStaticModels()
	configure(configYAMLEnvelope("models:\n  cn:\n    - first\n"))
	if !realmCatalogHas(staticModelsForRealm(regionCN), "first") {
		t.Fatal("first custom model missing")
	}
	configure(configYAMLEnvelope("models:\n  cn:\n    - second\n"))
	if realmCatalogHas(staticModelsForRealm(regionCN), "first") || !realmCatalogHas(staticModelsForRealm(regionCN), "second") {
		t.Fatal("reconfigure must replace custom models")
	}
	configure(configYAMLEnvelope("enabled: true\n"))
	if realmCatalogHas(staticModelsForRealm(regionCN), "second") {
		t.Fatal("missing models key must clear custom models")
	}
}

func TestCustomModelsMergeWithDiscoveryButPinsWin(t *testing.T) {
	defer resetCustomStaticModels()
	defer resetDynamicModelsCache()
	configure(configYAMLEnvelope("models:\n  cn:\n    - custom-cn\nmodels_cn: pinned-cn\n"))
	orig := discoverModelsFn
	defer func() { discoverModelsFn = orig }()
	called := false
	discoverModelsFn = func(token, realm string) ([]pluginapi.ModelInfo, error) {
		called = true
		return realmTestModels("discovered-cn"), nil
	}
	got := fetchDynamicModelsFromStorage([]byte(`{"accessToken":"tok","region":"cn"}`))
	if called || len(got) != 1 || got[0].ID != "pinned-cn" {
		t.Fatalf("pin must replace custom/discovery output: called=%v models=%#v", called, got)
	}

	configure(configYAMLEnvelope("models:\n  cn:\n    - custom-cn\n"))
	called = false
	got = fetchDynamicModelsFromStorage([]byte(`{"accessToken":"tok2","region":"cn"}`))
	if !called || !realmCatalogHas(got, "discovered-cn") || !realmCatalogHas(got, "custom-cn") {
		t.Fatalf("discovery must merge custom model: called=%v models=%#v", called, got)
	}
}

func TestConfigureModelsIntlGlobalDirectly(t *testing.T) {
	defer resetCustomStaticModels()
	defer resetPinnedModels()
	configure(configYAMLEnvelope("models_intl_global: \"deepseek-v4.1-flash, gpt-6-astra, gpt-5.6-sol\"\n"))
	intl := staticModelsForRealm(regionIntl)
	global := staticModelsForRealm(regionGlobal)
	cn := staticModelsForRealm(regionCN)
	for _, m := range []string{"hy4-preview", "deepseek-v4.1-flash", "gpt-6-astra", "gpt-5.6-sol"} {
		if !realmCatalogHas(intl, m) {
			t.Fatalf("intl must have %s: %#v", m, intl)
		}
		if !realmCatalogHas(global, m) {
			t.Fatalf("global must have %s: %#v", m, global)
		}
	}
	for _, m := range []string{"gpt-6-astra", "gpt-5.6-sol"} {
		if realmCatalogHas(cn, m) {
			t.Fatalf("cn must NOT have %s: %#v", m, cn)
		}
	}
}

func TestRegistrationDescribesCustomStaticModels(t *testing.T) {
	fields := wbRegistration().Metadata.ConfigFields
	if len(fields) != 2 {
		t.Fatalf("native config fields = %d, want exactly login_region and login_platform", len(fields))
	}
	if fields[0].Name != "login_region" || fields[1].Name != "login_platform" {
		t.Fatalf("native config fields = [%s, %s], want [login_region login_platform]", fields[0].Name, fields[1].Name)
	}
	for _, field := range fields {
		if field.Name == "models" || field.Name == "models_cn" || field.Name == "models_intl_global" {
			t.Fatalf("advanced model config %q must remain YAML-only", field.Name)
		}
	}
}

// Non-chat entries the upstream advertises (internal helpers, quick-preset
// aliases, IDE inline completion, image variants) must never reach the model
// list — calling them dies with 11102. Rules ported from workbuddy2api-hub's
// is_chat_model.
func TestIsChatModelID(t *testing.T) {
	blocked := []string{
		"lite", "default-model", "fast-model", "balanced-model", "primary-model", "deep-model",
		"codewise-v4", "completion-gf", "hunyuan-3b",
		"flux-image-alpha", "qwen-image-alpha-edit", "video-taco-completion",
		"Lite", "Default-Model", "CODEWISE-x",
	}
	for _, id := range blocked {
		if isChatModelID(id) {
			t.Errorf("%q must be pruned as non-chat", id)
		}
	}
	allowed := []string{
		"deepseek-v4.1-flash", "glm-5.3", "hy4-preview-f", "kimi-k3-1",
		"gpt-6-astra", "gemini-3.5-flash", "hy4-preview",
	}
	for _, id := range allowed {
		if !isChatModelID(id) {
			t.Errorf("%q must stay in the catalog", id)
		}
	}
	if isChatModelID("") || isChatModelID("  ") {
		t.Error("empty ids are not chat models")
	}
}

// Discovery output must come out pruned: a fake payload carrying the known
// non-chat entries loses them, chat entries survive.
func TestModelsFromDiscoveryPrunesNonChat(t *testing.T) {
	payload := []discoveredModel{
		{ID: "deepseek-v4.1-flash"},
		{ID: "lite"},
		{ID: "codewise-inline"},
		{ID: "default-model"},
		{ID: "glm-5.3"},
		{ID: "flux-image-alpha"},
	}
	got := modelsFromDiscovery(payload, nil)
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if !ids["deepseek-v4.1-flash"] || !ids["glm-5.3"] {
		t.Fatalf("chat models must survive pruning, got %v", ids)
	}
	for _, banned := range []string{"lite", "codewise-inline", "default-model", "flux-image-alpha"} {
		if ids[banned] {
			t.Errorf("non-chat model %s leaked through discovery", banned)
		}
	}
}
