package main

import (
	"context"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"strings"
	"testing"
)

func Test23WBAndCBSameRegionDefaultEndpoints(t *testing.T) {
	for _, domain := range []string{"www.workbuddy.ai", "www.codebuddy.ai"} {
		sa := &storedAuth{Auth: storedTokens{Domain: domain, AccessToken: "test-only", Region: regionIntl}, Account: storedAccount{UID: "u"}}
		before := string(mustJSON(sa))
		if accountRegion(sa) != regionIntl || accountServiceRegion(sa) != regionGlobal || !strings.HasPrefix(endpointChatFor(sa), upstreamBaseGlobal+"/") || !strings.HasPrefix(endpointTokenRefreshFor(sa), upstreamBaseGlobal+"/") || billingBaseFor(sa) != billingBaseGlobal {
			t.Fatal(domain)
		}
		raw, _ := storedAuthJSON(sa)
		if serviceRealmForStorage(raw, sa.Auth.AccessToken) != regionGlobal {
			t.Fatal(string(raw))
		}
		if before != string(mustJSON(sa)) {
			t.Fatal("routing mutated stored credential")
		}
	}
	for _, domain := range []string{"www.workbuddy.cn", "www.codebuddy.cn", "copilot.tencent.com"} {
		sa := &storedAuth{Auth: storedTokens{Domain: domain}}
		if accountRegion(sa) != regionCN || upstreamBaseForAuth(sa) != upstreamBaseCN {
			t.Fatal(domain)
		}
	}
}
func Test23ForeignIssuerAliasesAndSuffixSafety(t *testing.T) {
	for _, issuer := range []string{"https://auth.workbuddy.ai/realms/a", "https://auth.codebuddy.ai/realms/a"} {
		if !isGlobalToken(makeJWTWithIss(issuer)) {
			t.Fatal(issuer)
		}
	}
	if isGlobalToken(makeJWTWithIss("https://workbuddy.ai.attacker.test")) {
		t.Fatal("accepted unrelated domain")
	}
}
func Test23CBSourceAndLegacyWBKeyBothResolveSameForeignChannel(t *testing.T) {
	hubTestHost(t)
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		if strings.Contains(r.Header.Get("Authorization"), "intl") && r.URL.Host != "www.workbuddy.ai" {
			t.Error("CB token must default to WB foreign gateway", r.URL)
		}
		return directoryBody(`[{"id":"observed"}]`), nil
	}
	for _, key := range []string{"intl", "global"} {
		r := handleModelHub(pluginapi.ManagementRequest{Body: mustJSON(map[string]any{"sources": map[string]string{key: "intl"}})}, context.Background(), true)
		ss := r["sources"].([]hubSource)
		if len(ss) != 2 || ss[1].Channel != regionIntl || len(ss[1].Accounts) != 2 || ss[1].Account != "intl" || ss[1].Status != "ok" {
			t.Fatal(r)
		}
		if getActiveAuthID() != "cn-b-file" {
			t.Fatal("changed routing")
		}
	}
}
func Test23ForeignAuthFailureDoesNotProbeDomesticGateway(t *testing.T) {
	sa := directoryTest(t)
	sa.Auth.Domain = "www.codebuddy.ai"
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		if r.URL.Host != "www.workbuddy.ai" {
			t.Error("cross-region request", r.URL)
		}
		return &hostHTTPResponse{StatusCode: 401}, nil
	}
	r := resolveAccountDirectory(context.Background(), sa, true)
	if r.Status != "failed" || len(r.Models) != 0 {
		t.Fatal(r)
	}
}
