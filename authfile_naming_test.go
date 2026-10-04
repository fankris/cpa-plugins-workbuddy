package main

import (
	"strings"
	"testing"
)

// Auth file naming and identity are load-bearing: the plugin looks accounts up
// by name, and a mismatch silently orphans a credential (the account shows as
// missing while its file still exists). These tests pin the canonical names,
// the legacy candidates, and the identity key used for adoption dedupe.

func TestAuthFileNameForRealm(t *testing.T) {
	cases := []struct {
		region string
		uid    string
		want   string
	}{
		{regionCN, "u123", "workbuddy-CN-u123.json"},
		{regionGlobal, "u123", "workbuddy-Global-u123.json"},
		{regionIntl, "u123", "workbuddy-intl-u123.json"},
		{"", "u123", "workbuddy-CN-u123.json"}, // default realm
	}
	for _, tc := range cases {
		sa := &storedAuth{Account: storedAccount{UID: tc.uid}}
		sa.Auth.Region = tc.region
		if got := authFileNameFor(sa); got != tc.want {
			t.Errorf("region %q: authFileNameFor = %q, want %q", tc.region, got, tc.want)
		}
	}
	// No UID → the historical bare name (never a realm-qualified one).
	if got := authFileNameFor(&storedAuth{}); got != authFileName {
		t.Errorf("authFileNameFor(no uid) = %q, want %q", got, authFileName)
	}
	if got := authFileNameFor(nil); got != authFileName {
		t.Errorf("authFileNameFor(nil) = %q, want %q", got, authFileName)
	}
}

// sanitizeUIDForFileName must neutralise path traversal and separators, since
// the UID arrives from an imported credential file.
func TestSanitizeUIDForFileName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"u123", "u123"},
		{"  u123  ", "u123"},
		// Every unsafe character becomes "_", so traversal cannot survive.
		{"../../etc/passwd", "_etc_passwd"},
		{"a/b\\c", "a_b_c"},
		{"a b", "a_b"},
		{"", ""},
		// "." and ".." are replaced (never returned as path segments) and the
		// result is then non-empty, so it is safe as a filename component.
		{".", "_"},
		{"..", "_"},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := sanitizeUIDForFileName(tc.in); got != tc.want {
			t.Errorf("sanitizeUIDForFileName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Length is capped so a hostile UID cannot produce an over-long filename.
	long := strings.Repeat("x", 200)
	if got := sanitizeUIDForFileName(long); len(got) > 64 {
		t.Errorf("sanitized UID length = %d, want <= 64", len(got))
	}
}

// Every historical name must remain discoverable, and sanitization must match
// the generator (otherwise a punctuated UID could never be found again).
func TestAuthFileNameCandidatesCoverLegacyNames(t *testing.T) {
	got := authFileNameCandidates("u-1")
	// Case-variant duplicates (workbuddy-cn- vs workbuddy-CN-) are collapsed:
	// the candidate list is matched case-insensitively, so listing both spellings
	// would be redundant.
	want := []string{
		"workbuddy-CN-u-1.json",
		"workbuddy-Global-u-1.json",
		"workbuddy-intl-u-1.json",
		"workbuddy-u-1.json",
		"codebuddy-cn-u-1.json",
		"codebuddy-intl-u-1.json",
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	// Case-insensitive matching must still resolve the lower-case spellings.
	lower := map[string]bool{}
	for _, name := range got {
		lower[strings.ToLower(name)] = true
	}
	if !lower["workbuddy-cn-u-1.json"] || !lower["workbuddy-global-u-1.json"] {
		t.Fatalf("lower-case legacy spellings must remain covered: %v", got)
	}
	if c := authFileNameCandidates(""); c != nil {
		t.Fatalf("an empty UID must yield no candidates, got %v", c)
	}
	// A UID that sanitizes differently must still round-trip.
	if c := authFileNameCandidates("a b"); len(c) == 0 || !strings.Contains(c[0], "a_b") {
		t.Fatalf("candidates must use the sanitized UID, got %v", c)
	}
}

// A UID containing a name that would collide with a legacy candidate must not
// produce duplicate entries.
func TestAuthFileNameCandidatesDeduplicate(t *testing.T) {
	got := authFileNameCandidates("u1")
	seen := map[string]bool{}
	for _, name := range got {
		lower := strings.ToLower(name)
		if seen[lower] {
			t.Fatalf("duplicate candidate %q in %v", name, got)
		}
		seen[lower] = true
	}
}

// The physical record's name always wins: renaming a host-owned record would
// create a second auth entry.
func TestAuthFileNameForPhysicalPrefersHostName(t *testing.T) {
	sa := &storedAuth{Account: storedAccount{UID: "u9"}}
	sa.Auth.Region = regionCN

	phys := &hostAuthPhysical{Name: "legacy-name.json"}
	if got := authFileNameForPhysical(sa, phys); got != "legacy-name.json" {
		t.Fatalf("physical name must win, got %q", got)
	}
	// No physical name → fall back to the canonical name.
	if got := authFileNameForPhysical(sa, &hostAuthPhysical{}); got != "workbuddy-CN-u9.json" {
		t.Fatalf("empty physical name should fall back to canonical, got %q", got)
	}
	if got := authFileNameForPhysical(sa, nil); got != "workbuddy-CN-u9.json" {
		t.Fatalf("nil physical should fall back to canonical, got %q", got)
	}
}

// Legacy-name predicates drive adoption; they must be prefix-based and
// case-insensitive, and must not swallow canonical names.
func TestLegacyNamePredicates(t *testing.T) {
	if !isLegacyCodebuddyAuthName("codebuddy-cn-u1.json") {
		t.Error("codebuddy-cn- prefix must be recognised")
	}
	if !isLegacyCodebuddyAuthName("CodeBuddy-CN-u1.json") {
		t.Error("legacy detection must be case-insensitive")
	}
	if isLegacyCodebuddyAuthName("workbuddy-CN-u1.json") {
		t.Error("canonical names must not be treated as legacy")
	}
	if !isLegacyCodebuddyIntlAuthName("codebuddy-intl-u1.json") {
		t.Error("codebuddy-intl- prefix must be recognised")
	}
	if isLegacyCodebuddyIntlAuthName("codebuddy-cn-u1.json") {
		t.Error("the CN and Intl legacy families must not overlap")
	}
	if !isLegacyWorkbuddyAuthName("workbuddy.json") {
		t.Error("the bare workbuddy.json name is the legacy single-file name")
	}
	if isLegacyWorkbuddyAuthName("workbuddy-CN-u1.json") {
		t.Error("canonical realm-qualified names must not be treated as legacy")
	}
}

// The identity key must separate realms for the same UID, otherwise adoption
// would dedupe a CN account against an Intl one and drop a credential.
func TestAuthIdentityKeySeparatesRealms(t *testing.T) {
	cn := &storedAuth{Account: storedAccount{UID: "same-uid"}}
	cn.Auth.Region = regionCN
	intl := &storedAuth{Account: storedAccount{UID: "same-uid"}}
	intl.Auth.Region = regionIntl
	global := &storedAuth{Account: storedAccount{UID: "same-uid"}}
	global.Auth.Region = regionGlobal

	keys := map[string]string{
		"cn":     authIdentityKey(cn),
		"intl":   authIdentityKey(intl),
		"global": authIdentityKey(global),
	}
	seen := map[string]string{}
	for realm, key := range keys {
		if key == "" {
			t.Fatalf("%s identity key must not be empty", realm)
		}
		if other, dup := seen[key]; dup {
			t.Fatalf("identity key collision between %s and %s (%q)", realm, other, key)
		}
		seen[key] = realm
	}
	if authIdentityKey(nil) != "" {
		t.Error("nil auth must have an empty identity key")
	}
	if authIdentityKey(&storedAuth{}) != "" {
		t.Error("an auth without a UID must have an empty identity key")
	}
}

// foreignAuthOwner decides whether a host entry belongs to another plugin.
func TestForeignAuthOwner(t *testing.T) {
	own := []string{"", "workbuddy", "workbuddy-cn", "workbuddy-global", "workbuddy-intl",
		"codebuddy", "codebuddy-cn", "codebuddy-intl", "WORKBUDDY"}
	for _, t2 := range own {
		if foreign, owner := foreignAuthOwner(t2, ""); foreign {
			t.Errorf("type %q must be accepted as ours (reported owner %q)", t2, owner)
		}
	}
	for _, foreign := range []string{"qoder", "qoderwork", "trae", "claude"} {
		isForeign, owner := foreignAuthOwner(foreign, "")
		if !isForeign {
			t.Errorf("type %q must be rejected as foreign", foreign)
		}
		if owner != foreign {
			t.Errorf("owner for %q = %q, want %q", foreign, owner, foreign)
		}
	}
	// Provider is consulted when Type is empty.
	if foreign, _ := foreignAuthOwner("", "trae"); !foreign {
		t.Error("an empty type must fall back to the provider field")
	}
	if foreign, _ := foreignAuthOwner("", ""); foreign {
		t.Error("a type-less legacy entry must not be treated as foreign")
	}
}
