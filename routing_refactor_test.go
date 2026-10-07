package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeAuthDomainAndRealm(t *testing.T) {
	cases := []struct {
		name   string
		domain string
		want   string
	}{
		{"cn bare", "copilot.tencent.com", regionCN},
		{"cn url", "HTTPS://WWW.CODEBUDDY.CN/console", regionCN},
		{"global subdomain displays as Intl", "console.workbuddy.ai.", regionIntl},
		{"intl url", "https://API.CodeBuddy.AI/v2", regionIntl},
		{"cn workbuddy legacy", "www.workbuddy.cn", regionCN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accountRegion(&storedAuth{Auth: storedTokens{Domain: tc.domain}}); got != tc.want {
				t.Fatalf("accountRegion(%q) = %q, want %q", tc.domain, got, tc.want)
			}
		})
	}
}

func TestAccountRegionUsesJWTIssuerWhenDomainMissing(t *testing.T) {
	sa := &storedAuth{Auth: storedTokens{AccessToken: makeJWTWithIss("https://auth.workbuddy.ai/realms/workbuddy")}}
	if got := accountRegion(sa); got != regionIntl {
		t.Fatalf("accountRegion(domainless global token) = %q, want %q", got, regionIntl)
	}
	if got := accountServiceRegion(sa); got != regionGlobal {
		t.Fatalf("accountServiceRegion(domainless global token) = %q, want %q", got, regionGlobal)
	}
}

func TestAuthHeadersForKeepRefreshTokenScoped(t *testing.T) {
	sa := &storedAuth{
		Auth: storedTokens{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			Domain:       "console.workbuddy.ai.",
		},
		Account: storedAccount{UID: "uid-1", EnterpriseID: "ent-1"},
	}

	chatReq, _ := http.NewRequest(http.MethodPost, "https://example.test/v2/chat/completions", nil)
	authHeadersFor(chatReq, sa, false)
	if got := chatReq.Header.Get("Origin"); got != originRefererGlobal {
		t.Fatalf("chat Origin = %q, want %q", got, originRefererGlobal)
	}
	if got := chatReq.Header.Get("User-Agent"); !strings.Contains(got, "WorkBuddy") {
		t.Fatalf("chat User-Agent = %q, want WorkBuddy identity", got)
	}
	if got := chatReq.Header.Get("X-Refresh-Token"); got != "" {
		t.Fatalf("chat leaked X-Refresh-Token = %q", got)
	}
	if got := chatReq.Header.Get("X-Tenant-Id"); got != "ent-1" {
		t.Fatalf("chat X-Tenant-Id = %q, want ent-1", got)
	}

	refreshReq, _ := http.NewRequest(http.MethodPost, "https://example.test/v2/plugin/auth/token/refresh", nil)
	authHeadersFor(refreshReq, sa, true)
	if got := refreshReq.Header.Get("X-Refresh-Token"); got != "refresh-token" {
		t.Fatalf("refresh X-Refresh-Token = %q, want refresh-token", got)
	}
	if got := refreshReq.Header.Get("X-Auth-Refresh-Source"); got != "plugin" {
		t.Fatalf("refresh X-Auth-Refresh-Source = %q, want plugin", got)
	}
	if got := refreshReq.Header.Get("X-Domain"); got != "console.workbuddy.ai" {
		t.Fatalf("refresh X-Domain = %q, want normalized hostname", got)
	}
}

func TestAuthHeadersForIntl(t *testing.T) {
	sa := &storedAuth{Auth: storedTokens{AccessToken: "access-token", Domain: "https://codebuddy.ai/", Region: regionIntl}}
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/models", nil)
	authHeadersFor(req, sa, false)
	if req.Header.Get("Origin") != originRefererGlobal || req.Header.Get("Authorization") != "Bearer access-token" || req.Header.Get("X-Refresh-Token") != "" {
		t.Fatal(req.Header)
	}
	if sa.Auth.Domain != "https://codebuddy.ai/" {
		t.Fatal("credential mutated")
	}
}

func TestIntlAuthFileNameIsRegionQualified(t *testing.T) {
	sa := &storedAuth{Auth: storedTokens{Region: regionIntl}, Account: storedAccount{UID: "same-uid"}}
	if got := authFileNameFor(sa); got != "workbuddy-intl-same-uid.json" {
		t.Fatalf("Intl auth filename = %q", got)
	}
	if got := authFileNameFor(&storedAuth{Auth: storedTokens{Region: regionCN}, Account: storedAccount{UID: "same-uid"}}); got != "workbuddy-CN-same-uid.json" {
		t.Fatalf("CN auth filename = %q", got)
	}
	if got := authFileNameFor(&storedAuth{Auth: storedTokens{Region: regionGlobal}, Account: storedAccount{UID: "same-uid"}}); got != "workbuddy-Global-same-uid.json" {
		t.Fatalf("Global auth filename = %q", got)
	}
}

func TestOAuthPendingCodesAreStageSpecific(t *testing.T) {
	if !isOAuthPendingError(&oauthAPIError{Code: 11217}, 11217) {
		t.Fatal("token pending code must remain pending")
	}
	if !isOAuthPendingError(&oauthAPIError{Code: 12151}, 12151) {
		t.Fatal("account pending code must remain pending")
	}
	for _, tc := range []struct {
		err  error
		code int
	}{
		{&oauthAPIError{Code: 12153}, 11217},
		{&oauthAPIError{Code: 400}, 11217},
		{&oauthAPIError{Code: 11217}, 12151},
	} {
		if isOAuthPendingError(tc.err, tc.code) {
			t.Fatalf("error %#v incorrectly treated as pending for code %d", tc.err, tc.code)
		}
	}
}
