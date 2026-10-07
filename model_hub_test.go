package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestHubSourceSelection(t *testing.T) {
	rows := []hubAccount{{ID: "off", Selected: true}, {ID: "first", Available: true}, {ID: "active", Available: true, Selected: true}}
	for _, c := range []struct{ request, id, basis string }{{"", "active", "selected"}, {"first", "first", "manual"}, {"missing", "", "invalid_account"}, {"off", "", "invalid_account"}} {
		a, b := chooseHubAccount(rows, c.request)
		if a.ID != c.id || b != c.basis {
			t.Fatal(c, a, b)
		}
	}
	a, b := chooseHubAccount(rows[:2], "")
	if a.ID != "first" || b != "automatic" {
		t.Fatal(a, b)
	}
	_, b = chooseHubAccount(rows[:1], "")
	if b != "no_account" {
		t.Fatal(b)
	}
}
func TestHubMergePreservesOriginChannelsAndDifferences(t *testing.T) {
	input := []hubVariant{{Origin: "dynamic", Channel: "cn", Account: "a", Model: directoryModel{panelModel: panelModel{ID: "same", Name: "CN", ContextLength: 128000}}}, {Origin: "dynamic", Channel: "global", Account: "b", Model: directoryModel{panelModel: panelModel{ID: "same", Name: "Intl", ContextLength: 1000000}}}, {Origin: "custom", Channel: "global", ConfigKey: "models", Model: directoryModel{panelModel: panelModel{ID: "same", ContextLength: 500000}}}, {Origin: "dynamic", Channel: "global", Model: directoryModel{panelModel: panelModel{ID: "same-sg", Name: "CN"}}}}
	before := string(mustJSON(input))
	r := mergeHubModels(input)
	if len(r) != 2 || len(r[0].Variants) != 3 || len(r[0].Origins) != 2 || len(r[0].DynamicChannels) != 2 || len(r[0].CustomChannels) != 1 {
		t.Fatal(r)
	}
	if r[0].Variants[0].Model.ContextLength != 128000 || r[0].Variants[1].Model.ContextLength != 1000000 {
		t.Fatal("metadata overwritten")
	}
	if string(mustJSON(input)) != before {
		t.Fatal("input mutated")
	}
}
func TestHubCustomDisabledPinsAndChannels(t *testing.T) {
	customStaticModels.Lock()
	old := customStaticModels.models
	customStaticModels.models = []customStaticModel{{ID: "all", Channel: "all", Enabled: false}, {ID: "international", Channel: "intl_global", Enabled: true}}
	customStaticModels.Unlock()
	pinnedModelsMu.Lock()
	pins := pinnedModels
	pinnedModels = map[string][]string{regionCN: {"pinned-cn"}, regionIntl: {"pinned-intl"}}
	pinnedModelsMu.Unlock()
	defer func() {
		customStaticModels.Lock()
		customStaticModels.models = old
		customStaticModels.Unlock()
		pinnedModelsMu.Lock()
		pinnedModels = pins
		pinnedModelsMu.Unlock()
	}()
	variants := customHubVariants()
	r := mergeHubModels(variants)
	if len(r) != 4 {
		t.Fatal(r)
	}
	for _, m := range r {
		if len(m.DynamicChannels) != 0 {
			t.Fatal("configuration masquerades as dynamic")
		}
		if m.ID == "all" && (len(m.Variants) != 3 || !m.Variants[0].Model.Disabled) {
			t.Fatal(m)
		}
		if m.ID == "international" && len(m.CustomChannels) != 2 {
			t.Fatal(m)
		}
	}
}
func hubTestHost(t *testing.T) {
	t.Helper()
	directoryTest(t)
	oldRPC := hostRPCTestOverride
	oldActive := getActiveAuthID()
	setActiveAuthID("cn-b-file")
	t.Cleanup(func() { hostRPCTestOverride = oldRPC; setActiveAuthID(oldActive) })
	hostRPCTestOverride = func(method string, body []byte) ([]byte, error) {
		if method == "host.auth.list" {
			return okEnvelope(rpcHostAuthListResponse{Files: []pluginapi.HostAuthFileEntry{{AuthIndex: "cn-a", ID: "cn-a-file", Name: "a.json", Type: "workbuddy"}, {AuthIndex: "cn-b", ID: "cn-b-file", Name: "b.json", Type: "workbuddy"}, {AuthIndex: "global", ID: "global-file", Name: "g.json", Type: "workbuddy"}, {AuthIndex: "intl", ID: "intl-file", Name: "i.json", Type: "workbuddy"}}})
		}
		if method == "host.auth.get" {
			var p map[string]string
			_ = json.Unmarshal(body, &p)
			id := p["auth_index"]
			realm := regionCN
			domain := "copilot.tencent.com"
			if id == "global" {
				realm = regionGlobal
				domain = "www.workbuddy.ai"
			}
			if id == "intl" {
				realm = regionIntl
				domain = "www.codebuddy.ai"
			}
			raw := mustJSON(storedAuth{Auth: storedTokens{Region: realm, Domain: domain, AccessToken: "secret-" + id}, Account: storedAccount{UID: id, Nickname: id}})
			return okEnvelope(rpcHostAuthGetResponse{AuthIndex: id, Name: id + ".json", JSON: raw})
		}
		return nil, fmt.Errorf("unexpected host mutation %s", method)
	}
}
func TestHubHandlerOnePerChannelScopeCacheAndNoRoutingMutation(t *testing.T) {
	hubTestHost(t)
	var cn, global atomic.Int32
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		if hostCallbackIDFromRequest(req) != "hub" {
			t.Error("scope lost")
		}
		token := req.Header.Get("Authorization")
		if strings.Contains(token, "cn-a") {
			t.Error("queried every account instead of chosen")
		}
		if req.URL.Host == "www.codebuddy.ai" {
			t.Error("cross service unverified fetch")
		}
		if req.URL.Host == "copilot.tencent.com" {
			cn.Add(1)
		} else {
			global.Add(1)
		}
		return directoryBody(`[{"id":"shared","maxInputTokens":1000000}]`), nil
	}
	ctx := withHostCallbackID(context.Background(), "hub")
	r := handleModelHub(pluginapi.ManagementRequest{}, ctx, false)
	sources := r["sources"].([]hubSource)
	if r["status"] != "ok" || sources[0].Account != "cn-b" || sources[0].Basis != "selected" || sources[1].Account != "global" || sources[2].Status != "unsupported" || cn.Load() != 2 || global.Load() != 2 {
		t.Fatal(r, cn.Load(), global.Load())
	}
	if getActiveAuthID() != "cn-b-file" {
		t.Fatal("mutated active routing account")
	}
	if strings.Contains(string(mustJSON(r)), "secret-") {
		t.Fatal("token leaked")
	}
	handleModelHub(pluginapi.ManagementRequest{}, ctx, false)
	if cn.Load() != 2 || global.Load() != 2 {
		t.Fatal("cache ignored")
	}
	handleModelHub(pluginapi.ManagementRequest{}, ctx, true)
	if cn.Load() != 4 || global.Load() != 4 {
		t.Fatal("force ignored")
	}
	dynamicModelsCache.RLock()
	n := len(dynamicModelsCache.realms)
	dynamicModelsCache.RUnlock()
	if n != 0 {
		t.Fatal("polluted routing registry")
	}
}
func TestHubRejectsCrossChannelManualSelectionWithoutFallback(t *testing.T) {
	hubTestHost(t)
	var cn atomic.Int32
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		if req.URL.Host == "copilot.tencent.com" {
			cn.Add(1)
		}
		return directoryBody(`[]`), nil
	}
	r := handleModelHub(pluginapi.ManagementRequest{Body: mustJSON(map[string]any{"sources": map[string]string{"cn": "global"}})}, context.Background(), false)
	if r["sources"].([]hubSource)[0].Status != "invalid_account" || cn.Load() != 0 || getActiveAuthID() != "cn-b-file" {
		t.Fatal(r)
	}
}
func TestHubPartialChannelKeepsOtherAndNoStaticInjection(t *testing.T) {
	hubTestHost(t)
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		if req.URL.Host == "copilot.tencent.com" {
			return &hostHTTPResponse{StatusCode: 503}, nil
		}
		return directoryBody(`[{"id":"intl-only"}]`), nil
	}
	r := handleModelHub(pluginapi.ManagementRequest{}, context.Background(), false)
	rows := r["models"].([]hubModel)
	var dynamic []string
	for _, row := range rows {
		if len(row.DynamicChannels) > 0 {
			dynamic = append(dynamic, row.ID)
		}
	}
	if len(dynamic) != 1 || dynamic[0] != "intl-only" {
		t.Fatal(rows)
	}
	if r["sources"].([]hubSource)[0].Status != "failed" {
		t.Fatal(r)
	}
}
func TestHubManualChangeUsesRequestedAccountAndNeverSelectsItForRouting(t *testing.T) {
	hubTestHost(t)
	var a atomic.Int32
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		if strings.Contains(req.Header.Get("Authorization"), "cn-a") {
			a.Add(1)
		}
		return directoryBody(`[]`), nil
	}
	r := handleModelHub(pluginapi.ManagementRequest{Body: mustJSON(map[string]any{"sources": map[string]string{"cn": "cn-a"}})}, context.Background(), true)
	if r["sources"].([]hubSource)[0].Basis != "manual" || a.Load() != 2 || getActiveAuthID() != "cn-b-file" {
		t.Fatal(r)
	}
}
