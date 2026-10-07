package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestManagerRealmCredentialCompatibility(t *testing.T) {
	for _, raw := range []string{
		`{"auth":{"accessToken":"opaque","realm":"global"}}`,
		`{"accessToken":"opaque","realm":"global"}`,
		`{"auth":{"accessToken":"opaque"},"realm":"global"}`,
		`{"auth":{"accessToken":"opaque"},"domain":"www.workbuddy.ai"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			sa, e := parseStored([]byte(raw))
			if e != nil {
				t.Fatal(e)
			}
			wire, _ := storedAuthJSON(sa)
			if serviceRealmForStorage(wire, sa.Auth.AccessToken) != regionGlobal || serviceRealmForStorage([]byte(raw), "opaque") != regionGlobal {
				t.Fatal("lost Manager realm", string(wire))
			}
		})
	}
	sa, e := parseStored([]byte(`{"auth":{"accessToken":"opaque","region":"intl","realm":"global","domain":"www.codebuddy.ai"}}`))
	if e != nil || sa.Auth.Region != "intl" {
		t.Fatal(sa, e)
	}
	wire, _ := storedAuthJSON(sa)
	if serviceRealmForStorage(wire, "opaque") != regionGlobal {
		t.Fatal("WB default entrypoint missing")
	}
}
func TestHubKnownExpirySelection(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		disabled bool
		token    string
		expiry   int64
		reason   string
	}{{false, "token", 0, ""}, {false, "token", now.Unix() + 60, ""}, {false, "token", now.Unix() - 1, "expired"}, {false, "token", (now.Unix() - 1) * 1000, "expired"}, {false, "token", (now.Unix() + 60) * 1000, ""}, {false, " ", 0, "no_token"}, {true, "token", 0, "disabled"}} {
		if got := hubAccountReason(c.disabled, c.token, c.expiry, now); got != c.reason {
			t.Fatal(c, got)
		}
	}
	a, b := chooseHubAccount([]hubAccount{{ID: "expired", Selected: true, Available: false, Reason: "expired"}, {ID: "usable", Available: true}}, "")
	if a.ID != "usable" || b != "automatic" {
		t.Fatal(a, b)
	}
	_, b = chooseHubAccount([]hubAccount{{ID: "expired", Available: false, Reason: "expired"}}, "expired")
	if b != "invalid_account" {
		t.Fatal(b)
	}
}
func TestDirectoryManagerEnvelopeAndCandidateNegotiation(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		calls  int
	}{{200, `{"code":"0","data":{"models":[{"id":"model"}]}}`, 1}, {200, `{"code":404,"msg":"secret"}`, 2}, {200, `{"code":"400","data":{}}`, 2}, {200, `{"code":401}`, 1}, {200, `{"code":403}`, 1}, {200, `{"code":429}`, 1}, {200, `{"code":false}`, 2}, {400, `secret`, 2}, {501, `secret`, 2}, {503, `secret`, 1}} {
		t.Run(fmt.Sprintf("%d-%s", c.status, c.body), func(t *testing.T) {
			directoryTest(t)
			calls := 0
			hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
				calls++
				if r.URL.Host != "www.workbuddy.ai" {
					t.Fatal("wrong service")
				}
				if calls == 1 {
					return &hostHTTPResponse{StatusCode: c.status, Body: []byte(c.body)}, nil
				}
				return directoryBody(`[{"id":"fallback"}]`), nil
			}
			_, _, paths := directoryPaths(regionGlobal)
			_, s := fetchDirectorySource(context.Background(), "secret", "uid", regionGlobal, "enterprise_models", paths)
			if calls != c.calls || len(s.Attempts) != c.calls {
				t.Fatal(c, s, calls)
			}
			if strings.Contains(string(mustJSON(s)), "secret") {
				t.Fatal("credential/body leaked")
			}
			if c.calls == 2 && s.Status != "ok" {
				t.Fatal(s)
			}
		})
	}
}
func TestManagerRealmThroughHubAndHostHTTP(t *testing.T) {
	hubTestHost(t)
	previous := hostRPCTestOverride
	hostRPCTestOverride = func(method string, body []byte) ([]byte, error) {
		if method == "host.auth.get" {
			var p map[string]string
			_ = json.Unmarshal(body, &p)
			if p["auth_index"] == "cn-b" {
				return okEnvelope(rpcHostAuthGetResponse{AuthIndex: "cn-b", Name: "manager.json", JSON: json.RawMessage(`{"auth":{"accessToken":"opaque-manager-token","realm":"global"},"account":{"uid":"manager-uid"}}`)})
			}
		}
		return previous(method, body)
	}
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		if strings.Contains(r.Header.Get("Authorization"), "opaque-manager-token") && r.URL.Host != "www.workbuddy.ai" {
			t.Error("Manager global credential sent to wrong realm")
		}
		if hostCallbackIDFromRequest(r) != "manager-hub" {
			t.Error("scope lost")
		}
		return &hostHTTPResponse{StatusCode: 200, Body: []byte(`{"code":"0","data":{"models":[{"id":"manager-model","name":"Manager wire fixture"}]}}`)}, nil
	}
	r := handleModelHub(pluginapi.ManagementRequest{}, withHostCallbackID(context.Background(), "manager-hub"), true)
	sources := r["sources"].([]hubSource)
	if sources[1].Account != "cn-b" || sources[1].Basis != "selected" || sources[1].Status != "ok" {
		t.Fatal(r)
	}
	if getActiveAuthID() != "cn-b-file" {
		t.Fatal("routing changed")
	}
	if strings.Contains(string(mustJSON(r)), "opaque-manager-token") {
		t.Fatal("token leaked")
	}
	if path := os.Getenv("WB_HUB_WIRE_FIXTURE"); path != "" {
		if e := os.WriteFile(path, mustJSON(r), 0600); e != nil {
			t.Fatal(e)
		}
	}
}

func TestHubAllExpiredIsNotUnconfiguredAndDoesNotFetch(t *testing.T) {
	hubTestHost(t)
	previous := hostRPCTestOverride
	hostRPCTestOverride = func(method string, body []byte) ([]byte, error) {
		if method == "host.auth.get" {
			var p map[string]string
			_ = json.Unmarshal(body, &p)
			return okEnvelope(rpcHostAuthGetResponse{AuthIndex: p["auth_index"], Name: "expired.json", JSON: mustJSON(storedAuth{Auth: storedTokens{AccessToken: "expired-secret", Region: regionCN, ExpiresAt: time.Now().Unix() - 10}})})
		}
		return previous(method, body)
	}
	hostHTTPTestOverride = func(*http.Request) (*hostHTTPResponse, error) {
		t.Error("expired credential sent upstream")
		return nil, fmt.Errorf("must not run")
	}
	r := handleModelHub(pluginapi.ManagementRequest{}, context.Background(), true)
	s := r["sources"].([]hubSource)[0]
	if r["status"] != "partial" || s.Status != "unavailable" || len(s.Accounts) != 4 || s.Accounts[0].Reason != "expired" {
		t.Fatal(r)
	}
}
