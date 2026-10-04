package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 症状 2：邮箱/昵称不显示，账号名变成 "workbuddy"
//
// 根因：插件所有持久化都走 host.auth.save，而宿主是**从文件 JSON 重建 auth
// 记录**的（internal/pluginhost/auth_callbacks.go buildAuthFromFileData）：
//
//	label := provider            // "workbuddy"
//	if email := metadata["email"]; email != "" { label = email }
//
// 插件以前从不往文件里写 email/account 名，于是第一次生命周期写入（禁用、
// 备注同步、保活刷新）之后，账号行标签就退化成 provider 名 "workbuddy"——
// 尽管凭据里的 nickname 还在。这些测试锁定修复后的契约：写入的元数据必须
// 携带账号身份，且 label 与元数据同源。
// ---------------------------------------------------------------------------

// makeJWTWithClaims builds a JWT-shaped token carrying arbitrary string claims
// so tokenEmailClaim can be exercised without a real credential.
func makeJWTWithClaims(claims map[string]string) string {
	payload, _ := json.Marshal(claims)
	return "hdr." + base64.URLEncoding.EncodeToString(payload) + ".sig"
}

// TestAccountNameForAuthPrefersNickname: the nickname the upstream returned at
// login is the account's display name and must win.
func TestAccountNameForAuthPrefersNickname(t *testing.T) {
	sa := &storedAuth{Account: storedAccount{Nickname: "张三", UID: "u1"}}
	if got := accountNameForAuth(sa); got != "张三" {
		t.Fatalf("accountNameForAuth = %q, want the nickname", got)
	}
}

// TestAccountNameForAuthFallsBackToTokenEmail: credentials imported without a
// nickname (flat CPA-manager imports, older files) still have an identity in
// the JWT — using it beats showing the provider key.
func TestAccountNameForAuthFallsBackToTokenEmail(t *testing.T) {
	sa := &storedAuth{Auth: storedTokens{AccessToken: makeJWTWithClaims(map[string]string{"email": "user@example.com"})}}
	if got := accountNameForAuth(sa); got != "user@example.com" {
		t.Fatalf("accountNameForAuth = %q, want the token email claim", got)
	}
}

// TestAccountNameForAuthNeverReturnsProviderKey: returning "workbuddy" here is
// exactly the reported symptom, so the helper must return "" instead and let
// callers apply their own fallback copy.
func TestAccountNameForAuthNeverReturnsProviderKey(t *testing.T) {
	for _, sa := range []*storedAuth{nil, {}, {Account: storedAccount{UID: "u1"}}, {Auth: storedTokens{AccessToken: "not-a-jwt"}}} {
		if got := accountNameForAuth(sa); got != "" {
			t.Errorf("accountNameForAuth(%+v) = %q, want empty (never the provider key)", sa, got)
		}
	}
}

// TestTokenEmailClaim covers the claim shapes seen in the wild and rejects
// values that are not email-shaped.
func TestTokenEmailClaim(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"email claim", makeJWTWithClaims(map[string]string{"email": "a@b.com"}), "a@b.com"},
		{"upn fallback", makeJWTWithClaims(map[string]string{"upn": "c@d.com"}), "c@d.com"},
		{"sub fallback", makeJWTWithClaims(map[string]string{"sub": "e@f.com"}), "e@f.com"},
		{"non-email sub ignored", makeJWTWithClaims(map[string]string{"sub": "1234567"}), ""},
		{"no claims", makeJWTWithClaims(map[string]string{"iss": "x"}), ""},
		{"opaque token", "opaque-token-value", ""},
		{"empty", "", ""},
		{"two segments only", "hdr.", ""},
		{"bad base64", "hdr.!!!!.sig", ""},
	}
	for _, c := range cases {
		if got := tokenEmailClaim(c.token); got != c.want {
			t.Errorf("%s: tokenEmailClaim = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestEnrichAuthMetadataCarriesIdentity is the core regression lock: the
// metadata written into the auth file must include the identity fields the host
// reads back, so a save cannot degrade the row label.
func TestEnrichAuthMetadataCarriesIdentity(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{Region: "cn"},
		Account: storedAccount{Nickname: "李四", UID: "u-42", EnterpriseID: "ent-7"},
	}
	meta := enrichAuthMetadata(sa, nil, false)
	if meta["email"] != "李四" {
		t.Errorf("email = %v, want the account name (host derives Label from it)", meta["email"])
	}
	if meta["account_name"] != "李四" {
		t.Errorf("account_name = %v, want the account name", meta["account_name"])
	}
	if meta["uid"] != "u-42" {
		t.Errorf("uid = %v, want u-42", meta["uid"])
	}
	if meta["enterprise_id"] != "ent-7" {
		t.Errorf("enterprise_id = %v, want ent-7", meta["enterprise_id"])
	}
	if meta["region"] != "cn" {
		t.Errorf("region = %v, want cn", meta["region"])
	}
	// Pre-existing fields must survive the addition.
	for _, key := range []string{"type", "provider", "logo", "note", "disabled"} {
		if _, ok := meta[key]; !ok {
			t.Errorf("metadata lost required key %q", key)
		}
	}
}

// TestEnrichAuthMetadataOmitsEmptyIdentity: with no identity available the keys
// must be ABSENT (not empty strings), so the host keeps its own fallback
// instead of labelling a row with "".
func TestEnrichAuthMetadataOmitsEmptyIdentity(t *testing.T) {
	meta := enrichAuthMetadata(&storedAuth{}, nil, false)
	for _, key := range []string{"email", "account_name", "uid", "enterprise_id"} {
		if v, ok := meta[key]; ok {
			t.Errorf("empty identity must omit %q, got %v", key, v)
		}
	}
}

// TestEnrichAuthMetadataSurvivesNilAuth must not panic on a nil credential
// (lifecycle paths call it while reconciling broken records).
func TestEnrichAuthMetadataSurvivesNilAuth(t *testing.T) {
	meta := enrichAuthMetadata(nil, nil, true)
	if meta["disabled"] != true {
		t.Errorf("disabled = %v, want true", meta["disabled"])
	}
}

// TestLabelForAuthMatchesMetadata: the host label and the saved metadata are two
// renderings of the same identity. If they can disagree, the panel and the host
// auth list show different names for one account.
func TestLabelForAuthMatchesMetadata(t *testing.T) {
	cases := []struct {
		name string
		sa   *storedAuth
		want string
	}{
		{"cn nickname", &storedAuth{Auth: storedTokens{Region: "cn"}, Account: storedAccount{Nickname: "王五"}}, "王五 [CN]"},
		{"legacy global nickname displays as Intl", &storedAuth{Auth: storedTokens{Region: "global"}, Account: storedAccount{Nickname: "Alice"}}, "Alice [Intl]"},
		{"intl nickname", &storedAuth{Auth: storedTokens{Region: "intl"}, Account: storedAccount{Nickname: "Bob"}}, "Bob [Intl]"},
		{"token email", &storedAuth{Auth: storedTokens{Region: "cn", AccessToken: makeJWTWithClaims(map[string]string{"email": "z@q.com"})}}, "z@q.com [CN]"},
		{"no identity", &storedAuth{Auth: storedTokens{Region: "cn"}}, "WorkBuddy [CN]"},
		{"nil auth", nil, "WorkBuddy [CN]"},
	}
	for _, c := range cases {
		got := labelForAuth(c.sa)
		if got != c.want {
			t.Errorf("%s: labelForAuth = %q, want %q", c.name, got, c.want)
		}
		// Whatever the label's name part is, the metadata must agree with it.
		meta := enrichAuthMetadata(c.sa, nil, false)
		name, _, _ := strings.Cut(got, " [")
		if name != "WorkBuddy" {
			if meta["email"] != name {
				t.Errorf("%s: label name %q != metadata email %v", c.name, name, meta["email"])
			}
		}
	}
}

// TestBuildAuthFileJSONPersistsIdentity is the end-to-end proof that the fix
// reaches the file the host reads back. It mirrors the host's own derivation:
// metadata["email"] becomes the row label.
func TestBuildAuthFileJSONPersistsIdentity(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "tok", Region: "cn"},
		Account: storedAccount{Nickname: "赵六", UID: "u-9", EnterpriseID: "ent-1"},
	}
	raw, err := buildAuthFileJSON(sa, false, "CN · 余100 已用0", nil)
	if err != nil {
		t.Fatalf("buildAuthFileJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("saved JSON is not an object: %v", err)
	}
	// The host derives Label from this exact key.
	if doc["email"] != "赵六" {
		t.Fatalf("saved file lost the account identity: email = %v (host would label the row %q)", doc["email"], providerName)
	}
	if doc["account_name"] != "赵六" {
		t.Errorf("account_name = %v, want 赵六", doc["account_name"])
	}
	if doc["uid"] != "u-9" {
		t.Errorf("uid = %v, want u-9", doc["uid"])
	}
	if doc["type"] != providerName || doc["provider"] != providerName {
		t.Errorf("provider identity lost: type=%v provider=%v", doc["type"], doc["provider"])
	}
	if _, ok := doc["account"].(map[string]any); !ok {
		t.Errorf("nested account block missing: %v", doc["account"])
	}
	if note, _ := doc["note"].(string); !strings.Contains(note, "余100") {
		t.Errorf("note lost: %v", doc["note"])
	}
}

// TestBuildAuthFileJSONKeepsOperatorIdentity: an operator edit made through CPA
// management must win over the plugin's own derivation.
func TestBuildAuthFileJSONKeepsOperatorIdentity(t *testing.T) {
	existing := []byte(`{"type":"workbuddy","email":"renamed@example.com","account_name":"Renamed","note":"manual"}`)
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "tok", Region: "cn"},
		Account: storedAccount{Nickname: "上游昵称", UID: "u-1"},
	}
	raw, err := buildAuthFileJSONFromExisting(existing, sa, false, "", nil)
	if err != nil {
		t.Fatalf("buildAuthFileJSONFromExisting: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("saved JSON: %v", err)
	}
	if doc["email"] != "renamed@example.com" || doc["account_name"] != "Renamed" {
		t.Errorf("operator identity was overwritten: email=%v account_name=%v", doc["email"], doc["account_name"])
	}
	// uid was absent before, so the plugin fills it in.
	if doc["uid"] != "u-1" {
		t.Errorf("uid = %v, want u-1 (filled when absent)", doc["uid"])
	}
}

// TestBuildAuthFileJSONPreservesIdentityAcrossSaves: a second save (lifecycle
// disable, keepalive) must not drop the identity written by the first one —
// that accumulation is what made the name disappear only after some time.
func TestBuildAuthFileJSONPreservesIdentityAcrossSaves(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "tok", Region: "cn"},
		Account: storedAccount{Nickname: "钱七", UID: "u-11"},
	}
	first, err := buildAuthFileJSON(sa, false, "CN", nil)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := buildAuthFileJSONFromExisting(first, sa, true, "CN · 已禁用", nil)
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(second, &doc); err != nil {
		t.Fatalf("second save JSON: %v", err)
	}
	if doc["email"] != "钱七" {
		t.Errorf("identity lost on re-save: email = %v", doc["email"])
	}
	if doc["disabled"] != true {
		t.Errorf("disabled flag not applied: %v", doc["disabled"])
	}
	if note, _ := doc["note"].(string); !strings.Contains(note, "已禁用") {
		t.Errorf("note not updated: %v", doc["note"])
	}
}

// TestParseStoredRecoversIdentityFromSavedFile is the round-trip lock for the
// whole symptom: what buildAuthFileJSON writes must be readable back by
// parseStored, otherwise the account name is lost on every re-read even though
// the file on disk still holds it.
func TestParseStoredRecoversIdentityFromSavedFile(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "tok", Region: "cn"},
		Account: storedAccount{Nickname: "孙八", UID: "u-21", EnterpriseID: "ent-3"},
	}
	saved, err := buildAuthFileJSON(sa, false, "CN", nil)
	if err != nil {
		t.Fatalf("buildAuthFileJSON: %v", err)
	}
	back, err := parseStored(saved)
	if err != nil {
		t.Fatalf("parseStored(saved): %v", err)
	}
	if back.Account.Nickname != "孙八" {
		t.Fatalf("identity lost on re-read: nickname = %q", back.Account.Nickname)
	}
	if back.Account.UID != "u-21" {
		t.Errorf("uid lost on re-read: %q", back.Account.UID)
	}
}

// TestParseStoredReadsTopLevelIdentityForNestedShape: the plugin writes
// email/account_name at the top level (where the host reads Label from), so a
// nested credential must pick them up when "account" carries no nickname.
func TestParseStoredReadsTopLevelIdentityForNestedShape(t *testing.T) {
	raw := []byte(`{"type":"workbuddy","email":"top@example.com","account_name":"Top Name","auth":{"accessToken":"tok"},"account":{"uid":"u-1"}}`)
	sa, err := parseStored(raw)
	if err != nil {
		t.Fatalf("parseStored: %v", err)
	}
	if sa.Account.Nickname != "Top Name" {
		t.Fatalf("nickname = %q, want the top-level account_name", sa.Account.Nickname)
	}
	if sa.Account.UID != "u-1" {
		t.Errorf("uid = %q, want u-1", sa.Account.UID)
	}
}

// TestParseStoredPrefersExplicitNickname: the credential's own nickname must
// win over the top-level metadata copy.
func TestParseStoredPrefersExplicitNickname(t *testing.T) {
	raw := []byte(`{"email":"top@example.com","auth":{"accessToken":"tok"},"account":{"uid":"u-1","nickname":"真实昵称"}}`)
	sa, err := parseStored(raw)
	if err != nil {
		t.Fatalf("parseStored: %v", err)
	}
	if sa.Account.Nickname != "真实昵称" {
		t.Fatalf("nickname = %q, want the credential's own nickname", sa.Account.Nickname)
	}
}

// TestParseStoredFlatEmailFallback covers CPA-manager flat imports, which carry
// no nickname but may carry an email.
func TestParseStoredFlatEmailFallback(t *testing.T) {
	sa, err := parseStored([]byte(`{"accessToken":"tok","uid":"u-2","email":"flat@example.com"}`))
	if err != nil {
		t.Fatalf("parseStored: %v", err)
	}
	if sa.Account.Nickname != "flat@example.com" {
		t.Fatalf("nickname = %q, want the flat email fallback", sa.Account.Nickname)
	}
	if label := labelForAuth(sa); !strings.Contains(label, "flat@example.com") {
		t.Errorf("label = %q, want it to carry the recovered identity", label)
	}
}
