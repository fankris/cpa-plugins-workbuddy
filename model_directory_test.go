package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func directoryTest(t *testing.T) *storedAuth {
	t.Helper()
	modelTestState(t)
	accountDirectoryCache.Lock()
	accountDirectoryCache.entries = map[string]directoryEntry{}
	accountDirectoryCache.Unlock()
	t.Cleanup(func() {
		accountDirectoryCache.Lock()
		accountDirectoryCache.entries = map[string]directoryEntry{}
		accountDirectoryCache.Unlock()
	})
	return &storedAuth{Auth: storedTokens{AccessToken: "fixture-secret-directory", Region: regionGlobal, Domain: "www.workbuddy.ai"}, Account: storedAccount{UID: "fixture-directory"}}
}
func directoryBody(models string) *hostHTTPResponse {
	return &hostHTTPResponse{StatusCode: 200, Body: []byte(`{"code":0,"data":{"models":` + models + `}}`)}
}
func TestDirectoryFullMetadataAndUnknown(t *testing.T) {
	rows, err := parseDirectoryPayload(json.RawMessage(`{"models":[{"id":"gpt-example","name":"Display name","maxInputTokens":"1000000","maxOutputTokens":128000,"credits":"x0.34 credits","descriptionZh":"说明","tags":["text"],"supportsReasoning":true,"supportsToolCall":false,"supportsImages":"invalid","isDefault":true,"reasoning":{"supportedEfforts":["low","medium","high","xhigh","max","high",""],"defaultEffort":"high","summary":"auto"}},{"id":"only","credits":0}]}`), "v3_config")
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	r := rows[0]
	if r.ContextLength != 1000000 || r.MaxCompletionTokens != 128000 || r.Credits != "x0.34 credits" || len(r.Efforts) != 5 || r.DefaultEffort != "high" || r.SupportsImages != nil || r.SupportsToolCall == nil || *r.SupportsToolCall || !r.IsDefault || r.ReasoningSummary != "auto" || r.Description != "说明" || r.RoutingStatus != "directoryOnly" {
		t.Fatalf("metadata %+v", r)
	}
	if rows[1].Credits != "0" || rows[1].SupportsReasoning != nil {
		t.Fatal(rows[1])
	}
}
func TestDirectoryMergeV3PriorityConflictAndNoMutation(t *testing.T) {
	ent, _ := parseDirectoryPayload(json.RawMessage(`{"models":[{"id":"same","maxInputTokens":100,"supportsImages":true},{"id":"enterprise"}]}`), "enterprise_models")
	v3, _ := parseDirectoryPayload(json.RawMessage(`{"models":[{"id":"same","name":"V3","supportsImages":false},{"id":"v3-only"}]}`), "v3_config")
	before := string(mustJSON(v3))
	rows := mergeDirectory(ent, v3)
	if len(rows) != 3 || rows[0].Name != "V3" || rows[0].ContextLength != 0 || !rows[0].ImageInputConflict || rows[0].SupportsImages != nil || rows[1].ID != "v3-only" || rows[2].ID != "enterprise" {
		t.Fatal(rows)
	}
	if string(mustJSON(v3)) != before {
		t.Fatal("mutated source snapshot")
	}
}
func TestDirectoryNarrowEmptyAndMalformed(t *testing.T) {
	for _, raw := range []string{`[]`, `{"models":[]}`} {
		rows, e := parseDirectoryPayload(json.RawMessage(raw), "v3_config")
		if e != nil || len(rows) != 0 {
			t.Fatal(e)
		}
	}
	rows, e := parseDirectoryPayload(json.RawMessage(`["hy3","hy3","default-model","gpt-image-example","seedance-example"]`), "v3_config")
	if e != nil || len(rows) != 4 {
		t.Fatal(rows, e)
	}
	for _, raw := range []string{`{}`, `null`, `{"models":null}`, `{"models":3}`, `{"models":[123]}`} {
		if _, e := parseDirectoryPayload(json.RawMessage(raw), "v3_config"); e == nil {
			t.Fatal("accepted", raw)
		}
	}
}
func TestDirectoryPreservesDisabledAndPresetsWithoutRouting(t *testing.T) {
	sa := directoryTest(t)
	setGloballyDisabledModels([]string{"keep-disabled"})
	t.Cleanup(func() { setGloballyDisabledModels(nil) })
	hostHTTPTestOverride = func(*http.Request) (*hostHTTPResponse, error) {
		return directoryBody(`[{"id":"default-model","disabled":true,"disabledReason":"not supported"},{"id":"image-example","tags":["text-to-image"]},{"id":"v3-only"}]`), nil
	}
	r := resolveAccountDirectory(context.Background(), sa, false)
	if r.Status != "ok" || len(r.Models) != 3 || !r.Models[0].Disabled {
		t.Fatal(r)
	}
	if !reflect.DeepEqual(currentGloballyDisabledModels(), []string{"keep-disabled"}) {
		t.Fatal("changed configuration")
	}
	dynamicModelsCache.RLock()
	n := len(dynamicModelsCache.realms)
	dynamicModelsCache.RUnlock()
	if n != 0 {
		t.Fatal("directory polluted routing cache")
	}
}
func TestDirectoryConcurrentSourcesHeadersAndCallback(t *testing.T) {
	sa := directoryTest(t)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		calls.Add(1)
		arrived <- struct{}{}
		select {
		case <-release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		if req.URL.Host != "www.workbuddy.ai" || req.Header.Get("Authorization") != "Bearer "+sa.Auth.AccessToken || req.Header.Get("Accept-Language") != "en-US" || req.Header.Get("X-Machine-ID") != machineIDFor(sa.Account.UID) || req.Header.Get("User-Agent") != "WorkBuddy/5.5.4 WorkBuddy AI/5.5.4 CLI/2.137.1" || hostCallbackIDFromRequest(req) != "catalog-callback" {
			t.Errorf("incorrect request %s", req.URL)
		}
		if req.URL.Path == "/v3/config" {
			return directoryBody(`[{"id":"only-v3"}]`), nil
		}
		return directoryBody(`[{"id":"only-enterprise"}]`), nil
	}
	result := make(chan directoryResult, 1)
	go func() {
		result <- resolveAccountDirectory(withHostCallbackID(context.Background(), "catalog-callback"), sa, false)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("sources were not concurrent")
		}
	}
	close(release)
	r := <-result
	if calls.Load() != 2 || len(r.Models) != 2 || r.Models[0].ID != "only-v3" {
		t.Fatal(r, calls.Load())
	}
	if strings.Contains(string(mustJSON(r)), sa.Auth.AccessToken) {
		t.Fatal("credential leaked")
	}
}
func TestDirectoryEndpointFallbackOnlyAbsent(t *testing.T) {
	for _, status := range []int{404, 405, 401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			directoryTest(t)
			paths := []string{}
			hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
				paths = append(paths, req.URL.Path)
				if len(paths) == 1 {
					return &hostHTTPResponse{StatusCode: status, Body: []byte("secret")}, nil
				}
				return directoryBody(`[]`), nil
			}
			_, _, candidates := directoryPaths(regionGlobal)
			_, s := fetchDirectorySource(context.Background(), "secret", "uid", regionGlobal, "enterprise_models", candidates)
			if status == 404 || status == 405 {
				if len(paths) != 2 || s.Status != "ok" {
					t.Fatal(paths, s)
				}
			} else if len(paths) != 1 || s.Status != "error" {
				t.Fatal(paths, s)
			}
			if strings.Contains(s.Error, "secret") {
				t.Fatal("body leaked")
			}
		})
	}
}
func TestDirectoryCacheForceTokenAndAccountIsolation(t *testing.T) {
	sa := directoryTest(t)
	var calls atomic.Int32
	hostHTTPTestOverride = func(*http.Request) (*hostHTTPResponse, error) {
		calls.Add(1)
		return directoryBody(`[{"id":"fixture"}]`), nil
	}
	r := resolveAccountDirectory(context.Background(), sa, false)
	r.Models[0].Name = "modified"
	cached := resolveAccountDirectory(context.Background(), sa, false)
	if !cached.Cached || calls.Load() != 2 || cached.Models[0].Name == "modified" {
		t.Fatal(cached, calls.Load())
	}
	resolveAccountDirectory(context.Background(), sa, true)
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
	sa.Auth.AccessToken = "new-token"
	resolveAccountDirectory(context.Background(), sa, false)
	if calls.Load() != 6 {
		t.Fatal("token isolation")
	}
	sa.Account.UID = "another"
	resolveAccountDirectory(context.Background(), sa, false)
	if calls.Load() != 8 {
		t.Fatal("account isolation")
	}
	sa.Auth.Region = regionCN
	sa.Auth.Domain = "copilot.tencent.com"
	resolveAccountDirectory(context.Background(), sa, false)
	if calls.Load() != 10 {
		t.Fatal("service isolation")
	}
}
func TestDirectoryPartialFailureNotCachedAndForcedFailureNoStale(t *testing.T) {
	sa := directoryTest(t)
	var calls atomic.Int32
	mode := "ok"
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		calls.Add(1)
		if mode == "failed" || mode == "partial" && req.URL.Path == "/v3/config" {
			return &hostHTTPResponse{StatusCode: 503}, nil
		}
		return directoryBody(`[{"id":"fixture"}]`), nil
	}
	resolveAccountDirectory(context.Background(), sa, false)
	mode = "failed"
	r := resolveAccountDirectory(context.Background(), sa, true)
	if r.Status != "failed" || len(r.Models) != 0 || r.FetchedAt != "" {
		t.Fatal(r)
	}
	mode = "partial"
	r = resolveAccountDirectory(context.Background(), sa, false)
	if r.Status != "partial" || len(r.Models) != 1 {
		t.Fatal(r)
	}
	n := calls.Load()
	resolveAccountDirectory(context.Background(), sa, false)
	if calls.Load() != n+2 {
		t.Fatal("partial cached")
	}
}
func TestDirectoryCBForeignUsesWBAndMissingTokenMakesNoRequest(t *testing.T) {
	sa := directoryTest(t)
	var calls atomic.Int32
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		calls.Add(1)
		if r.URL.Host != "www.workbuddy.ai" {
			t.Error("not WB foreign gateway", r.URL)
		}
		return directoryBody(`[{"id":"cb-via-wb"}]`), nil
	}
	sa.Auth.Region = regionIntl
	sa.Auth.Domain = "www.codebuddy.ai"
	r := resolveAccountDirectory(context.Background(), sa, true)
	if r.Status != "ok" || len(r.Models) != 1 || calls.Load() != 2 {
		t.Fatal(r, calls.Load())
	}
	if sa.Auth.Domain != "www.codebuddy.ai" {
		t.Fatal("credential mutated")
	}
	sa.Auth.AccessToken = ""
	r = resolveAccountDirectory(context.Background(), sa, true)
	if r.Status != "failed" || calls.Load() != 2 {
		t.Fatal(r, calls.Load())
	}
}
func TestDirectoryCoalescesSameAccountReads(t *testing.T) {
	sa := directoryTest(t)
	var calls atomic.Int32
	hostHTTPTestOverride = func(*http.Request) (*hostHTTPResponse, error) {
		calls.Add(1)
		return directoryBody(`[{"id":"fixture"}]`), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := resolveAccountDirectory(context.Background(), sa, false)
			if r.Status != "ok" {
				t.Error(r)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
func TestDirectoryCancelBothSourcesAndNoCache(t *testing.T) {
	sa := directoryTest(t)
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		if calls.Add(1) == 2 {
			cancel()
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	r := resolveAccountDirectory(ctx, sa, false)
	if r.Status != "failed" || len(r.Models) != 0 {
		t.Fatal(r)
	}
	accountDirectoryCache.Lock()
	n := len(accountDirectoryCache.entries)
	accountDirectoryCache.Unlock()
	if n != 0 {
		t.Fatal("cached cancellation")
	}
}
func TestDirectoryFamilyIsExplicitlyDerived(t *testing.T) {
	for _, x := range []struct {
		id, vendor, want string
		derived          bool
	}{{"kimi-x", "", "Kimi", true}, {"hy4-x", "", "腾讯混元", true}, {"gpt-x", "", "", true}, {"kimi-x", "Official", "Official", false}} {
		got, d := directoryFamily(x.id, x.vendor)
		if got != x.want || d != x.derived {
			t.Fatal(x, got, d)
		}
	}
}
func TestDirectoryInvalidEnvelopeCannotSucceed(t *testing.T) {
	directoryTest(t)
	for _, body := range []string{`<html>login</html>`, `{"data":{"models":[]}}`, `{"code":0,"data":{}}`, `{"code":1,"msg":"secret"}`, `{"code":0,"data":{"models":[]}}`} {
		hostHTTPTestOverride = func(*http.Request) (*hostHTTPResponse, error) {
			return &hostHTTPResponse{StatusCode: 200, Body: []byte(body)}, nil
		}
		_, s := fetchDirectorySource(context.Background(), "token", "uid", regionCN, "v3_config", []string{"/v3/config"})
		want := "error"
		if body == `{"code":0,"data":{"models":[]}}` {
			want = "ok"
		}
		if s.Status != want || strings.Contains(s.Error, "secret") {
			t.Fatal(body, s)
		}
	}
}
