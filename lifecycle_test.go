package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestShouldActOnCredits(t *testing.T) {
	cases := []struct {
		name string
		cr   *creditsSummary
		want bool
	}{
		{"nil unknown", nil, false},
		{"empty unknown", &creditsSummary{}, false},
		{"remain>0", &creditsSummary{TotalRemain: 1}, false},
		{"exhausted used", &creditsSummary{TotalRemain: 0, TotalUsed: 10}, true},
		{"exhausted packages", &creditsSummary{TotalRemain: 0, Packages: []packageSummary{{Name: "p"}}}, true},
	}
	for _, tc := range cases {
		if got := shouldActOnCredits(tc.cr); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsHardCreditError(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{200, "", false},
		{500, "internal", false},
		{429, "too many requests", false}, // soft unless credit semantics
		{403, "insufficient credit", true},
		{400, "积分不足", true},
		{402, "payment required", true},
		{403, "额度不足", true},
		{400, "no credit left", true},
		{400, "余额不足", true},
		{403, "credit exhausted", true},
		{429, "rate limit exceeded", false},
		{400, "invalid request", false},
	}
	for _, tc := range cases {
		if got := isHardCreditError(tc.status, tc.body); got != tc.want {
			t.Fatalf("status=%d body=%q: got %v want %v", tc.status, tc.body, got, tc.want)
		}
	}
}

func TestIsSoftRateLimit(t *testing.T) {
	if !isSoftRateLimit(429, "too many requests") {
		t.Fatal("429 should be soft")
	}
	if isSoftRateLimit(403, "insufficient credit") {
		t.Fatal("hard credit must not be soft")
	}
	if isSoftRateLimit(500, "error") {
		t.Fatal("5xx not soft rate limit")
	}
}

func TestLifecycleActionFor(t *testing.T) {
	ex := &creditsSummary{TotalRemain: 0, TotalUsed: 5}
	ok := &creditsSummary{TotalRemain: 10, TotalUsed: 1}
	cases := []struct {
		name   string
		region string
		cr     *creditsSummary
		want   lifecycleAction
	}{
		{"cn exhausted", "cn", ex, lifecycleDisable},
		{"global exhausted", "global", ex, lifecycleDelete},
		{"cn ok", "cn", ok, lifecycleNone},
		{"global ok", "global", ok, lifecycleNone},
		{"unknown", "cn", nil, lifecycleNone},
	}
	for _, tc := range cases {
		if got := lifecycleActionFor(tc.region, tc.cr); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestShouldReenableCN(t *testing.T) {
	if shouldReenableCN(false, &creditsSummary{TotalRemain: 10}) {
		t.Fatal("enabled account should not reenable")
	}
	if shouldReenableCN(true, nil) {
		t.Fatal("unknown credits must not reenable")
	}
	if shouldReenableCN(true, &creditsSummary{TotalRemain: 0, TotalUsed: 5}) {
		t.Fatal("exhausted must not reenable")
	}
	if !shouldReenableCN(true, &creditsSummary{TotalRemain: 3}) {
		t.Fatal("disabled + remain should reenable")
	}
}

func TestDisplayNote(t *testing.T) {
	cn := &storedAuth{Auth: storedTokens{Domain: "www.codebuddy.cn"}}
	gl := &storedAuth{Auth: storedTokens{Domain: "www.workbuddy.ai"}}
	note := displayNote(cn, &creditsSummary{TotalRemain: 12, TotalUsed: 8, TotalSize: 20}, false)
	if !strings.Contains(note, "CN") || !strings.Contains(note, "12") || !strings.Contains(note, "8") {
		t.Fatalf("cn note = %q", note)
	}
	note = displayNote(gl, &creditsSummary{TotalRemain: 0, TotalUsed: 250, TotalSize: 250}, false)
	if !strings.Contains(note, "Intl") || !strings.Contains(note, "耗尽") {
		t.Fatalf("global-service note = %q", note)
	}
	note = displayNote(cn, &creditsSummary{TotalRemain: 0, TotalUsed: 5}, true)
	if !strings.Contains(note, "禁用") && !strings.Contains(strings.ToLower(note), "disabled") {
		t.Fatalf("disabled note = %q", note)
	}
}

func TestBuildAuthFileJSON_ContainsDisabledAndNote(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "at", RefreshToken: "rt", Domain: "www.codebuddy.cn"},
		Account: storedAccount{UID: "u1", Nickname: "nick"},
	}
	raw, err := buildAuthFileJSON(sa, true, "CN · test", nil)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != providerName {
		t.Fatalf("type=%v", m["type"])
	}
	if m["disabled"] != true {
		t.Fatalf("disabled=%v", m["disabled"])
	}
	if m["note"] != "CN · test" {
		t.Fatalf("note=%v", m["note"])
	}
	if m["logo"] == nil || m["logo"] == "" {
		t.Fatal("logo missing")
	}
	auth, _ := m["auth"].(map[string]any)
	if auth == nil || auth["accessToken"] != "at" {
		t.Fatalf("auth tokens lost: %v", m["auth"])
	}
}

func TestAuthFileNameFor(t *testing.T) {
	cases := []struct {
		name   string
		region string
		want   string
	}{
		{name: "CN", region: regionCN, want: "workbuddy-CN-uid-1.json"},
		{name: "Global", region: regionGlobal, want: "workbuddy-Global-uid-1.json"},
		{name: "Intl", region: regionIntl, want: "workbuddy-intl-uid-1.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sa := &storedAuth{Auth: storedTokens{Region: tc.region}, Account: storedAccount{UID: "uid-1"}}
			if got := authFileNameFor(sa); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	if got := authFileNameFor(nil); got != authFileName {
		t.Fatalf("nil got %q", got)
	}
}

func TestAuthFileNameCandidatesIncludesCanonicalAndLegacyNames(t *testing.T) {
	got := authFileNameCandidates("uid-1")
	want := []string{
		"workbuddy-CN-uid-1.json",
		"workbuddy-Global-uid-1.json",
		"workbuddy-intl-uid-1.json",
		"workbuddy-uid-1.json",
		"codebuddy-cn-uid-1.json",
		"codebuddy-intl-uid-1.json",
	}
	if len(got) != len(want) {
		t.Fatalf("candidate count=%d want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d]=%q want %q", i, got[i], want[i])
		}
	}
	if got := authFileNameCandidates("bad uid/with punctuation"); len(got) == 0 || !strings.Contains(got[0], "bad_uid_with_punctuation") {
		t.Fatalf("sanitized candidates=%#v", got)
	}
}

func TestBuildAuthFileJSONFromExistingPreservesMetadata(t *testing.T) {
	existing := []byte(`{
		"type":"legacy-provider",
		"provider":"legacy-provider",
		"custom":{"keep":true},
		"auth":{"accessToken":"old"},
		"account":{"uid":"old"},
		"note":"operator note"
	}`)
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "new", RefreshToken: "rt"},
		Account: storedAccount{UID: "u1", Nickname: "nick"},
	}
	raw, err := buildAuthFileJSONFromExisting(existing, sa, true, "disabled reason", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "legacy-provider" || got["provider"] != "legacy-provider" {
		t.Fatalf("provider metadata overwritten: %#v", got)
	}
	custom, ok := got["custom"].(map[string]any)
	if !ok || custom["keep"] != true {
		t.Fatalf("unknown metadata lost: %#v", got["custom"])
	}
	if got["disabled"] != true || got["note"] != "disabled reason" {
		t.Fatalf("lifecycle metadata not applied: %#v", got)
	}
	auth, ok := got["auth"].(map[string]any)
	if !ok || auth["accessToken"] != "new" {
		t.Fatalf("auth storage not updated: %#v", got["auth"])
	}
}

func TestAppendAuthNote(t *testing.T) {
	if got := appendAuthNote("operator", "operator"); got != "operator" {
		t.Fatalf("duplicate note = %q", got)
	}
	if got := appendAuthNote("operator", "CPA 管理端可清理"); got != "operator · CPA 管理端可清理" {
		t.Fatalf("appended note = %q", got)
	}
}

func TestParseDisabledFromAuthJSON(t *testing.T) {
	if parseDisabledFromAuthJSON([]byte(`{"disabled":true}`)) != true {
		t.Fatal("want true")
	}
	if parseDisabledFromAuthJSON([]byte(`{"disabled":false}`)) != false {
		t.Fatal("want false")
	}
	if parseDisabledFromAuthJSON([]byte(`{"auth":{}}`)) != false {
		t.Fatal("missing defaults false")
	}
}

func TestLabelForAuth(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{Domain: "www.workbuddy.ai"},
		Account: storedAccount{Nickname: "Bob"},
	}
	got := labelForAuth(sa)
	if !strings.Contains(got, "Bob") || !strings.Contains(got, "Intl") {
		t.Fatalf("label=%q", got)
	}
}

func TestLifecycleActionFor_IdempotentPolicy(t *testing.T) {
	// Applying action twice with same inputs must stay disable/delete (not flip).
	ex := &creditsSummary{TotalRemain: 0, TotalUsed: 1}
	if lifecycleActionFor("cn", ex) != lifecycleDisable {
		t.Fatal("cn")
	}
	if lifecycleActionFor("global", ex) != lifecycleDelete {
		t.Fatal("global")
	}
	// Soft rate limit body never hard-credit alone.
	if isHardCreditError(429, "too many requests") {
		t.Fatal("429 body without credit markers is not hard")
	}
	if !isSoftRateLimit(429, "rate limit") {
		t.Fatal("429 soft")
	}
}

func TestParseDisabledFromAuthJSON_StringTruth(t *testing.T) {
	// Only JSON boolean true counts; string "true" is false for strict parse.
	if parseDisabledFromAuthJSON([]byte(`{"disabled":"true"}`)) {
		t.Fatal("string true should not parse as bool true with current schema")
	}
}

func TestListEntryMatchesUID(t *testing.T) {
	uid := "00e26541-1884-4916-9c26-253a325d64ac"
	want := "workbuddy-CN-" + uid + ".json"
	cases := []struct {
		name string
		f    pluginapi.HostAuthFileEntry
		want bool
	}{
		{"name exact", pluginapi.HostAuthFileEntry{Name: want}, true},
		{"id exact", pluginapi.HostAuthFileEntry{ID: want}, true},
		{"basename id", pluginapi.HostAuthFileEntry{Name: "workbuddy-CN-" + uid}, true},
		{"case", pluginapi.HostAuthFileEntry{Name: strings.ToUpper(want)}, true},
		{"other uid", pluginapi.HostAuthFileEntry{Name: "workbuddy-CN-other.json"}, false},
		{"legacy bare", pluginapi.HostAuthFileEntry{Name: "workbuddy.json"}, false},
		{"empty uid", pluginapi.HostAuthFileEntry{Name: want}, false},
	}
	for _, tc := range cases {
		u := uid
		if tc.name == "empty uid" {
			u = ""
		}
		got := listEntryMatchesUID(tc.f, u, want)
		if got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
