package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestStoreQuotaSnapshotIndexesAuthIndexAndID(t *testing.T) {
	accountCache = sync.Map{}
	credits := &creditsSummary{TotalRemain: 3, TotalSize: 5}
	storeQuotaSnapshot("idx-1", "id-1", "Pro", credits)
	for _, key := range []string{"idx-1", "id-1"} {
		value, ok := accountCache.Load(key)
		if !ok {
			t.Fatalf("missing cache key %q", key)
		}
		entry, ok := value.(*accountCacheEntry)
		if !ok || entry.credits != credits || entry.plan != "Pro" {
			t.Fatalf("cache[%q]=%#v", key, value)
		}
	}
}

func TestQuotaResponseMapsAggregateAndPackages(t *testing.T) {
	resp := quotaResponse("Pro", &creditsSummary{
		TotalRemain: 75,
		TotalUsed:   25,
		TotalSize:   100,
		Packages: []packageSummary{
			{Name: "monthly", Remain: 50, Used: 10, Size: 60, CycleEnd: "2026-10-01 00:00:00"},
			{Name: "bonus", Remain: 25, Used: 15, Size: 40},
		},
	})
	if resp.Subscription == nil || resp.Subscription.Plan != "Pro" {
		t.Fatalf("subscription=%#v", resp.Subscription)
	}
	if len(resp.Groups) != 3 {
		t.Fatalf("groups=%d want aggregate + 2 packages", len(resp.Groups))
	}
	if got := resp.Groups[0].Buckets[0].RemainingFraction; got != 0.75 {
		t.Fatalf("aggregate fraction=%v", got)
	}
	if got := resp.Groups[1].Buckets[0].ResetTime; got != "2026-10-01 00:00:00" {
		t.Fatalf("reset=%q", got)
	}
	if got := resp.Groups[1].Buckets[0].RemainingFraction; got != 50.0/60.0 {
		t.Fatalf("package fraction=%v", got)
	}
}

func TestQuotaResetIsExplicitlyUnsupported(t *testing.T) {
	raw, err := handleQuotaReset([]byte(`{"auth_index":"idx"}`))
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.QuotaResetResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Fatal("reset must not claim success")
	}
	if resp.Message == "" {
		t.Fatal("reset should explain unsupported behavior")
	}
}

func TestQuotaDescribe(t *testing.T) {
	raw, err := handleQuotaDescribe(nil)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.QuotaDescribeResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.DisplayName != "WorkBuddy" || resp.SupportsReset {
		t.Fatalf("describe=%#v", resp)
	}
	found := false
	for _, provider := range resp.SupportedProviders {
		if provider == providerName {
			found = true
		}
	}
	if !found {
		t.Fatalf("supported providers=%v", resp.SupportedProviders)
	}
}

func TestNativeQuotaUsesInjectedHostHTTPClient(t *testing.T) {
	client := &quotaTestHTTPClient{}
	sa := &storedAuth{Auth: storedTokens{AccessToken: "at", Domain: "codebuddy.cn"}, Account: storedAccount{UID: "u1"}}
	resp, err := fetchNativeQuota(testContext(), pluginapi.QuotaFetchRequest{
		AuthIndex:   "idx",
		Provider:    providerName,
		StorageJSON: mustQuotaJSON(sa),
		HTTPClient:  client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Subscription == nil || len(resp.Groups) == 0 {
		t.Fatalf("quota response=%#v", resp)
	}
	if client.calls != 2 {
		t.Fatalf("HTTP calls=%d want resource + payment type", client.calls)
	}
}

func TestQuotaResponseSummaryMetrics(t *testing.T) {
	resp := quotaResponse("Pro", &creditsSummary{
		TotalRemain: 75,
		TotalUsed:   25,
		TotalSize:   100,
	})
	want := map[string]float64{"credits_remaining": 75, "credits_used": 25, "credits_total": 100}
	if len(resp.Summary) != len(want) {
		t.Fatalf("summary=%#v", resp.Summary)
	}
	for _, m := range resp.Summary {
		if m.Format != "number" || m.Label == "" || want[m.Key] != m.Value {
			t.Fatalf("metric=%#v", m)
		}
		delete(want, m.Key)
	}
	if len(want) != 0 {
		t.Fatalf("missing summary metrics %v", want)
	}
}

func TestQuotaDescribeCoversLegacyCodebuddy(t *testing.T) {
	raw, err := handleQuotaDescribe(nil)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.QuotaDescribeResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, provider := range resp.SupportedProviders {
		if provider == "codebuddy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("supported providers=%v", resp.SupportedProviders)
	}
}

func TestNativeQuotaAcceptsLegacyCodebuddyAndReportsClockOffset(t *testing.T) {
	client := &quotaTestHTTPClient{dateHeader: time.Now().Add(-10 * time.Minute).UTC().Format(http.TimeFormat)}
	resp, err := fetchNativeQuota(testContext(), pluginapi.QuotaFetchRequest{
		AuthIndex:   "idx",
		Provider:    "codebuddy",
		StorageJSON: mustQuotaJSON(&storedAuth{Auth: storedTokens{AccessToken: "at", Domain: "codebuddy.cn"}, Account: storedAccount{UID: "u2"}}),
		HTTPClient:  client,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMs := (-10 * time.Minute).Milliseconds()
	diff := resp.ServerTimeOffsetMs - wantMs
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Minute.Milliseconds() {
		t.Fatalf("serverTimeOffsetMs=%d want ~%d", resp.ServerTimeOffsetMs, wantMs)
	}
}

func mustQuotaJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

// Kept local to avoid coupling quota tests to lifecycle cancellation state.
func testContext() context.Context { return context.Background() }

type quotaTestHTTPClient struct {
	calls      int
	dateHeader string
}

func (c *quotaTestHTTPClient) Do(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	c.calls++
	if req.Method != http.MethodPost {
		return pluginapi.HTTPResponse{StatusCode: 405}, nil
	}
	resp := pluginapi.HTTPResponse{StatusCode: 200}
	if c.dateHeader != "" {
		resp.Headers = http.Header{"Date": []string{c.dateHeader}}
	}
	if strings.HasSuffix(req.URL, "/get-payment-type") {
		resp.Body = []byte(`{"code":0,"data":{"paymentType":"Pro"}}`)
		return resp, nil
	}
	resp.Body = []byte(`{"code":0,"data":{"TotalDosage":100,"Accounts":[{"PackageName":"monthly","CycleCapacityRemain":75,"CycleCapacitySize":100}]}}`)
	return resp, nil
}

func (c *quotaTestHTTPClient) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return pluginapi.HTTPStreamResponse{}, nil
}
