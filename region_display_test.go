package main

import "testing"

// v0.12.15: Intl (codebuddy.ai) credentials previously surfaced as "CN" in
// the credential manager — accountRegion only knew workbuddy.ai Global and
// defaulted everything else to cn. The fix adds an explicit auth.region
// (written at login/import) plus a codebuddy.ai domain-sniff fallback, and
// the display helpers gain the INTL state.

func TestAccountRegionV01215(t *testing.T) {
	cases := []struct {
		name   string
		sa     *storedAuth
		region string
	}{
		// Known domains determine display grouping; otherwise stored region is used.
		{"CN domain overrides stale intl region", &storedAuth{Auth: storedTokens{Region: "intl", Domain: "copilot.tencent.com"}}, "cn"},
		{"global region maps to intl", &storedAuth{Auth: storedTokens{Region: "global"}}, "intl"},
		{"Intl domain overrides stale cn region", &storedAuth{Auth: storedTokens{Region: "cn", Domain: "codebuddy.ai"}}, "intl"},
		{"explicit case/space tolerant", &storedAuth{Auth: storedTokens{Region: " INTL "}}, "intl"},
		{"intl domain bare", &storedAuth{Auth: storedTokens{Domain: "codebuddy.ai"}}, "intl"},
		{"intl domain subdomain", &storedAuth{Auth: storedTokens{Domain: "api.codebuddy.ai"}}, "intl"},
		{"intl domain uppercase", &storedAuth{Auth: storedTokens{Domain: "CodeBuddy.AI"}}, "intl"},
		{"global service displays as intl", &storedAuth{Auth: storedTokens{Domain: "www.workbuddy.ai"}}, "intl"},
		{"cn domain still cn", &storedAuth{Auth: storedTokens{Domain: "www.codebuddy.cn"}}, "cn"},
		{"empty still cn", &storedAuth{}, "cn"},
		{"nil still cn", nil, "cn"},
	}
	for _, tc := range cases {
		if got := accountRegion(tc.sa); got != tc.region {
			t.Errorf("%s: accountRegion() = %q, want %q", tc.name, got, tc.region)
		}
	}
}

func TestDisplayNoteThreeWayV01215(t *testing.T) {
	cases := []struct {
		name string
		sa   *storedAuth
		want string
	}{
		{"intl note", &storedAuth{Auth: storedTokens{Region: "intl"}}, "Intl · 积分未知"},
		{"global service note", &storedAuth{Auth: storedTokens{Domain: "workbuddy.ai"}}, "Intl · 积分未知"},
		{"cn note", &storedAuth{Auth: storedTokens{Domain: "www.codebuddy.cn"}}, "CN · 积分未知"},
	}
	for _, tc := range cases {
		if got := displayNote(tc.sa, nil, false); got != tc.want {
			t.Errorf("%s: displayNote() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLabelForAuthThreeWayV01215(t *testing.T) {
	cases := []struct {
		name string
		sa   *storedAuth
		want string
	}{
		{"intl label", &storedAuth{Auth: storedTokens{Region: "intl"}}, "WorkBuddy [Intl]"},
		{"global service displays as Intl", &storedAuth{Auth: storedTokens{Domain: "workbuddy.ai"}}, "WorkBuddy [Intl]"},
		{"cn label", &storedAuth{}, "WorkBuddy [CN]"},
		{"nickname kept", &storedAuth{Account: storedAccount{Nickname: "Alice"}, Auth: storedTokens{Region: "intl"}}, "Alice [Intl]"},
	}
	for _, tc := range cases {
		if got := labelForAuth(tc.sa); got != tc.want {
			t.Errorf("%s: labelForAuth() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
