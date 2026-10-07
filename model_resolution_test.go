package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func modelTestState(t *testing.T) {
	t.Helper()
	resetPluginLifecycleForTest()
	resetDynamicModelsCache()
	old := discoverModelsFn
	oldHTTP := hostHTTPTestOverride
	t.Cleanup(func() {
		discoverModelsFn = old
		hostHTTPTestOverride = oldHTTP
		resetDynamicModelsCache()
		resetPluginLifecycleForTest()
	})
}
func modelStorage(uid string) []byte {
	return mustJSON(&storedAuth{Auth: storedTokens{AccessToken: "fixture-" + uid, Region: regionCN}, Account: storedAccount{UID: uid}})
}
func TestModelResolutionConcurrentSuccessSharesCache(t *testing.T) {
	modelTestState(t)
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	discoverModelsFn = func(string, string) ([]pluginapi.ModelInfo, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return realmTestModels("verified-fixture"), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := resolveCredentialModels(context.Background(), modelStorage("shared"), false)
			if r.Status != "ok" || r.Models[0].ID != "verified-fixture" {
				t.Error("wrong result")
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("upstream called %d times", calls.Load())
	}
	discoveryGates.Lock()
	remaining := len(discoveryGates.entries)
	discoveryGates.Unlock()
	if remaining != 0 {
		t.Fatal("gate leak")
	}
}
func TestModelResolutionCanceledWaiterDoesNotCancelOwner(t *testing.T) {
	modelTestState(t)
	started, release := make(chan struct{}), make(chan struct{})
	discoverModelsFn = func(string, string) ([]pluginapi.ModelInfo, error) {
		close(started)
		<-release
		return realmTestModels("owner"), nil
	}
	owner := make(chan modelResolution, 1)
	go func() { owner <- resolveCredentialModels(context.Background(), modelStorage("wait"), false) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan modelResolution, 1)
	go func() { waiter <- resolveCredentialModels(ctx, modelStorage("wait"), false) }()
	cancel()
	select {
	case r := <-waiter:
		if r.Status != "fallback" || !strings.Contains(r.Warning, "canceled") {
			t.Fatal(r.Status, r.Warning)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter cannot cancel")
	}
	close(release)
	if r := <-owner; r.Status != "ok" {
		t.Fatal("waiter canceled owner")
	}
}
func TestModelResolutionForceOnlyInvalidatesSelectedAccount(t *testing.T) {
	modelTestState(t)
	var calls int
	discoverModelsFn = func(token, realm string) ([]pluginapi.ModelInfo, error) { calls++; return realmTestModels(token), nil }
	a, b := modelStorage("a"), modelStorage("b")
	resolveCredentialModels(context.Background(), a, false)
	resolveCredentialModels(context.Background(), b, false)
	resolveCredentialModels(context.Background(), a, true)
	resolveCredentialModels(context.Background(), b, false)
	if calls != 3 {
		t.Fatalf("refresh invalidated unrelated credential: %d calls", calls)
	}
}
func TestModelResolutionFallbackKeepsLocalDiagnostics(t *testing.T) {
	modelTestState(t)
	discoverModelsFn = func(token, realm string) ([]pluginapi.ModelInfo, error) {
		if strings.HasSuffix(token, "a") {
			return nil, errors.New("discovery denied")
		}
		return realmTestModels("b"), nil
	}
	a := resolveCredentialModels(context.Background(), modelStorage("a"), true)
	b := resolveCredentialModels(context.Background(), modelStorage("b"), true)
	if a.Status != "fallback" || a.Source.LastError == "" || len(a.Models) == 0 {
		t.Fatal("fallback falsely succeeded")
	}
	if b.Status != "ok" || b.Source.LastError != "" || a.Source.Source == b.Source.Source {
		t.Fatal("cross-account diagnostic pollution")
	}
	sa, _ := parseStored(modelStorage("a"))
	r := credentialModelsResponse(context.Background(), sa, true)
	if r["status"] != "fallback" || r["warning"] == "" {
		t.Fatal("manual refresh hid discovery error")
	}
}
func TestModelResolutionStaticIsSkippedNotVerified(t *testing.T) {
	modelTestState(t)
	discoverModelsFn = func(string, string) ([]pluginapi.ModelInfo, error) { t.Error("unexpected intl probe"); return nil, nil }
	raw := mustJSON(&storedAuth{Auth: storedTokens{AccessToken: "", Region: regionIntl}, Account: storedAccount{UID: "intl"}})
	r := resolveCredentialModels(context.Background(), raw, true)
	if r.Status != "skipped" || !strings.Contains(r.Source.Source, "static") {
		t.Fatal(r.Status, r.Source)
	}
}

const healthyModelFixture = `{"code":0,"data":{"models":[{"id":"glm-5.2","name":"GLM","contextWindow":128000,"disabled":false}]}}`

func TestModelDiscoveryRetryClassification(t *testing.T) {
	for _, status := range []int{429, 503, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			modelTestState(t)
			old := modelsDiscoveryRetryDelays
			modelsDiscoveryRetryDelays = []time.Duration{time.Millisecond}
			defer func() { modelsDiscoveryRetryDelays = old }()
			calls := 0
			hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
				calls++
				if calls == 1 {
					return &hostHTTPResponse{StatusCode: status, Headers: http.Header{}, Body: []byte("rejected")}, nil
				}
				return &hostHTTPResponse{StatusCode: 200, Body: []byte(healthyModelFixture)}, nil
			}
			_, err := callModelsAPI("fixture", "cn")
			if status == 403 {
				if calls != 1 || err == nil {
					t.Fatal("business denial retried")
				}
			} else if calls != 2 || err != nil {
				t.Fatalf("transient failed: calls=%d err=%v", calls, err)
			}
		})
	}
}
func TestModelDiscoveryCancellationStopsRetryAfter(t *testing.T) {
	modelTestState(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		calls++
		cancel()
		return &hostHTTPResponse{StatusCode: 429, Headers: http.Header{"Retry-After": []string{"30"}}}, nil
	}
	start := time.Now()
	_, err := callModelsAPIContext(ctx, "fixture", "cn")
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("cancel ignored: %d %v", calls, err)
	}
}
func TestModelDiscoveryDeadlineCoversWholeRetryBudget(t *testing.T) {
	modelTestState(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	calls := 0
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		calls++
		return &hostHTTPResponse{StatusCode: 503, Headers: http.Header{"Retry-After": []string{"30"}}}, nil
	}
	_, err := callModelsAPIContext(ctx, "fixture", "cn")
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatal("deadline did not stop backoff", calls, err)
	}
}
func TestModelForAuthPreservesHostScope(t *testing.T) {
	modelTestState(t)
	discoverModelsFn = nil
	called := false
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		called = true
		if hostCallbackIDFromRequest(req) != "models-scope" {
			t.Error("lost SDK scope")
		}
		return &hostHTTPResponse{StatusCode: 200, Body: []byte(healthyModelFixture)}, nil
	}
	raw := mustJSON(struct {
		pluginapi.AuthModelRequest
		HostCallbackID string `json:"host_callback_id"`
	}{pluginapi.AuthModelRequest{StorageJSON: modelStorage("scope")}, "models-scope"})
	out, err := handleModelForAuth(raw)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	_ = json.Unmarshal(out, &env)
	if !env.OK || !called {
		t.Fatal("model provider not reached")
	}
}
func TestHostAccountListingUsesNativeOwnershipAndLegacyNames(t *testing.T) {
	old := hostRPCTestOverride
	defer func() { hostRPCTestOverride = old }()
	hostRPCTestOverride = func(string, []byte) ([]byte, error) {
		return okEnvelope(rpcHostAuthListResponse{Files: []pluginapi.HostAuthFileEntry{
			{Name: "workbuddy.json"}, {Name: "codebuddy-cn.json"}, {Name: "team-alpha.json", Type: "workbuddy"}, {Name: "team-beta.json", Provider: "workbuddy"},
			{Name: "workbuddy-foreign.json", Type: "codex"}, {Name: "unknown.json"}, {Name: "another.json", Provider: "codex"},
		}})
	}
	files, err := hostAuthList()
	if err != nil || len(files) != 4 {
		t.Fatalf("native/legacy accounts hidden: %+v %v", files, err)
	}
	if files[0].Name != "workbuddy.json" || files[2].Name != "team-alpha.json" {
		t.Fatal("filename changed")
	}
}
