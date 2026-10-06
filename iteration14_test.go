package main

import (
	"context"
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"testing"
	"time"
)

func TestDualMenuDescriptionFirstWins(t *testing.T) {
	r := managementRegistration()
	merged := map[string]string{}
	for _, v := range r.Routes {
		if v.Menu != "" {
			merged[v.Menu] = v.Description
		}
	}
	for _, v := range r.Resources {
		if _, ok := merged[v.Menu]; !ok && v.Menu != "" {
			merged[v.Menu] = v.Description
		}
		if v.Menu == "WorkBuddy" && v.Description != panelMenuDescription {
			t.Fatal(v)
		}
	}
	if merged["WorkBuddy"] != panelMenuDescription || merged["WorkBuddy"] == "" {
		t.Fatal(merged)
	}
}
func TestCreditExpiryUTCAndCST(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	a, ok := creditExpiryTime("2026-10-07 08:00:00")
	b, _ := creditExpiryTime("2026-10-07T00:00:00Z")
	if !ok || !a.Equal(b) {
		t.Fatal(a, b)
	}
	c := &creditsSummary{Packages: []packageSummary{{Remain: 12, CycleEnd: "2026-10-07 08:00:00"}, {Remain: 8, CycleEnd: "2026-10-07T00:00:00Z"}, {Remain: 7, CycleEnd: "invalid"}, {Remain: 9, CycleEnd: "2026-10-05T00:00:00Z"}, {Remain: 30, CycleEnd: "2026-11-01T00:00:00Z"}}}
	e := summarizeCreditExpiry(c, now)
	if e.Within7Days != 20 || e.NextAmount != 20 || e.UnknownRemain != 7 {
		t.Fatal(e)
	}
	if summarizeCreditExpiry(nil, now).Known {
		t.Fatal("unknown must not be known zero")
	}
}
func TestExpirySchedulerEligibilityAndFreshness(t *testing.T) {
	now := time.Now().UTC()
	ids := []string{"expiry-a", "expiry-b", "expiry-c"}
	for i, id := range ids {
		accountCache.Store(id, &accountCacheEntry{credits: &creditsSummary{TotalRemain: 50, FetchedAt: now.Format(time.RFC3339), Packages: []packageSummary{{Remain: 50, CycleEnd: now.Add(time.Duration(i+1) * 24 * time.Hour).Format(time.RFC3339)}}}})
		defer accountCache.Delete(id)
	}
	cs := []pluginapi.SchedulerAuthCandidate{{ID: ids[2], Provider: providerName}, {ID: ids[1], Provider: providerName}, {ID: ids[0], Provider: providerName}}
	if p := pickExpiringCredits(cs, now); p.AuthID != ids[0] {
		t.Fatal(p)
	}
	cs[2].Status = "disabled"
	if p := pickExpiringCredits(cs, now); p.AuthID != ids[1] {
		t.Fatal(p)
	}
	cs[0].Priority = 10
	if p := pickExpiringCredits(cs, now); p.AuthID != ids[2] {
		t.Fatal(p)
	}
	if p := pickExpiringCredits(cs, now.Add(6*time.Minute)); p.Handled {
		t.Fatal("stale must defer", p)
	}
	cs = append(cs, pluginapi.SchedulerAuthCandidate{ID: "other", Provider: "other"})
	if p := pickExpiringCredits(cs, now); p.Handled {
		t.Fatal("mixed provider must defer")
	}
	if p := pickExpiringCredits([]pluginapi.SchedulerAuthCandidate{{ID: "missing", Provider: providerName}}, now); p.Handled {
		t.Fatal(p)
	}
}
func TestModelRichMetadataObservedFields(t *testing.T) {
	var m discoveredModel
	if err := json.Unmarshal([]byte(`{"id":"deepseek-v4.1-flash","credits":"x0.00","vendor":"DeepSeek","maxInputTokens":1000000,"maxOutputTokens":128000,"supportsImages":true,"onlyReasoning":true,"reasoning":{"supportedEfforts":["low","high"],"defaultEffort":"high"}}`), &m); err != nil {
		t.Fatal(err)
	}
	infos := modelsFromDiscovery([]discoveredModel{m}, nil)
	if discoveryContextDegraded([]discoveredModel{m}) || infos[0].ContextLength != 1000000 || infos[0].MaxCompletionTokens != 128000 || infos[0].Thinking == nil || len(infos[0].Thinking.Levels) != 2 {
		t.Fatal(infos)
	}
	d := detailsFromDiscovery([]discoveredModel{m})[m.ID]
	if d.Credits != "x0.00" || d.SupportsImages == nil || !*d.SupportsImages || d.Vendor != "DeepSeek" {
		t.Fatal(d)
	}
	if d := detailsFromDiscovery([]discoveredModel{{ID: "unknown"}})["unknown"]; d.SupportsImages != nil || d.Credits != "" {
		t.Fatal("unknown must remain unknown", d)
	}
}
func TestFallbackDoesNotBorrowRichMetadata(t *testing.T) {
	r := resolveCredentialModels(context.Background(), []byte(`{}`), false)
	if len(r.Details) > 0 {
		t.Fatal(r.Details)
	}
}
func TestExpiryModeConfiguration(t *testing.T) {
	if err := validateLifecycleConfig(mustMarshal(t, map[string]any{"config_yaml": []byte("scheduler_mode: credits_expiry\n")})); err != nil {
		t.Fatal(err)
	}
}
func TestExpiryAllDatesUnknown(t *testing.T) {
	e := summarizeCreditExpiry(&creditsSummary{Packages: []packageSummary{{Remain: 19, CycleEnd: "bad"}}}, time.Now())
	if e.Known || e.UnknownRemain != 19 {
		t.Fatal(e)
	}
}
func TestExpiryPolicyUsesActualHandler(t *testing.T) {
	restore := setSchedulerMode(schedulerModeExpiry)
	defer restore()
	now := time.Now()
	accountCache.Store("expiry-handler", &accountCacheEntry{credits: &creditsSummary{TotalRemain: 12, FetchedAt: now.UTC().Format(time.RFC3339), Packages: []packageSummary{{Remain: 12, CycleEnd: now.Add(time.Hour).UTC().Format(time.RFC3339)}}}})
	defer accountCache.Delete("expiry-handler")
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "expiry-handler", Provider: providerName}}}))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsePickResponse(t, raw); !got.Handled || got.AuthID != "expiry-handler" {
		t.Fatal(got)
	}
}
func TestRichModelCacheIsCredentialScoped(t *testing.T) {
	storageA := []byte(`{"auth":{"accessToken":"meta-token-a"},"account":{"uid":"meta-account-a"}}`)
	storageB := []byte(`{"auth":{"accessToken":"meta-token-b"},"account":{"uid":"meta-account-b"}}`)
	for i, storage := range [][]byte{storageA, storageB} {
		token, _ := extractAccessToken(storage)
		service := serviceRealmForStorage(storage, token)
		key := modelCacheKey(service, storage, token)
		dynamicModelsCache.Lock()
		oldRealm, hadRealm := dynamicModelsCache.realms[service]
		dynamicModelsCache.Unlock()
		defer func() {
			dynamicModelsCache.Lock()
			delete(dynamicModelsCache.realms, key)
			if hadRealm {
				dynamicModelsCache.realms[service] = oldRealm
			} else {
				delete(dynamicModelsCache.realms, service)
			}
			dynamicModelsCache.Unlock()
		}()
		price := []string{"x0.00", "x0.03"}[i]
		storeDynamicModels(key, []pluginapi.ModelInfo{{ID: "same-id", ContextLength: 1000}}, map[string]modelDetails{"same-id": {Credits: price, Source: "upstream"}})
	}
	for i, storage := range [][]byte{storageA, storageB} {
		result := resolveCredentialModels(context.Background(), storage, false)
		if result.Details["same-id"].Credits != []string{"x0.00", "x0.03"}[i] {
			t.Fatalf("credential metadata leaked: %+v", result)
		}
	}
}
