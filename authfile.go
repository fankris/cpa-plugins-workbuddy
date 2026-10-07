// authfile.go owns the auth-store representation used by the plugin. Physical
// files are owned by CPA: the plugin only reads them through host.auth.* and
// persists updates through host.auth.save. In particular, this file never
// removes an auth file; callers mark records disabled and leave final cleanup
// to CPA management.
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// authFileNameFor returns the canonical name for newly created/imported
// credentials. CN and Global are intentionally distinct because the same UID
// can exist in both realms; Intl keeps its historical lowercase-qualified name.
// Bare "workbuddy.json" is the legacy single-account fallback for credentials
// without a usable UID.
var unsafeUIDChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeUIDForFileName(uid string) string {
	uid = strings.TrimSpace(uid)
	uid = unsafeUIDChars.ReplaceAllString(uid, "_")
	if uid == "" || uid == "." || uid == ".." {
		return ""
	}
	if len(uid) > 64 {
		uid = uid[:64]
	}
	return uid
}

func authFileNameFor(sa *storedAuth) string {
	if sa != nil {
		if uid := sanitizeUIDForFileName(sa.Account.UID); uid != "" {
			switch credentialOriginService(sa) {
			case regionGlobal:
				return "workbuddy-Global-" + uid + ".json"
			case regionIntl:
				return "workbuddy-intl-" + uid + ".json"
			default:
				return "workbuddy-CN-" + uid + ".json"
			}
		}
	}
	return authFileName
}

// authFileNameCandidates returns all known names for one UID. New names are
// listed first, but old canonical and merged-plugin names remain supported for
// existing installations and migration. UID sanitization must match the name
// generator; otherwise a UID with punctuation can never be found again.
func authFileNameCandidates(uid string) []string {
	uid = sanitizeUIDForFileName(uid)
	if uid == "" {
		return nil
	}
	prefixes := []string{
		"workbuddy-CN-",
		"workbuddy-Global-",
		"workbuddy-intl-",
		"workbuddy-cn-",
		"workbuddy-global-",
		"workbuddy-",
		"codebuddy-cn-",
		"codebuddy-intl-",
	}
	out := make([]string, 0, len(prefixes))
	seen := make(map[string]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		name := prefix + uid + ".json"
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

// isLegacyWorkbuddyAuthName reports the historical bare single-file name that
// can collide with the realm-qualified multi-account names. The record is
// retained and marked disabled when a canonical record is written; CPA
// management owns the eventual cleanup.
func isLegacyWorkbuddyAuthName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), authFileName)
}

// authFileNameForPhysical keeps the host's current record name. Renaming a
// legacy record from inside the plugin creates a second host auth record, so
// migration and lifecycle updates must save back to the name CPA supplied.
func authFileNameForPhysical(sa *storedAuth, phys *hostAuthPhysical) string {
	if phys != nil {
		if name := strings.TrimSpace(phys.Name); name != "" {
			return name
		}
	}
	return authFileNameFor(sa)
}

// hostAuthPhysical is the host's physical/auth-store view. Path is diagnostic
// metadata only; it is deliberately never used to remove or confine files.
type hostAuthPhysical struct {
	AuthIndex string
	Name      string
	Path      string
	JSON      []byte
	Disabled  bool
}

func hostAuthGetPhysical(authIndex string) (*hostAuthPhysical, error) {
	body, _ := json.Marshal(map[string]string{"auth_index": authIndex})
	raw, err := hostCall(pluginabi.MethodHostAuthGet, body)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		return nil, fmt.Errorf("host.auth.get: bad envelope")
	}
	var resp rpcHostAuthGetResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		return nil, err
	}
	return &hostAuthPhysical{
		AuthIndex: resp.AuthIndex,
		Name:      resp.Name,
		Path:      resp.Path,
		JSON:      resp.JSON,
		Disabled:  parseDisabledFromAuthJSON(resp.JSON),
	}, nil
}

// hostAuthPersist saves via host API only. The path argument was intentionally
// removed: a plugin must not infer filesystem confinement or delete beside a
// host-owned auth record.
func hostAuthPersist(name string, raw []byte) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty auth file name")
	}
	return hostAuthSaveJSON(name, raw)
}

// existingNote returns the top-level note without interpreting unrelated
// metadata. It is used when marking a retained legacy/duplicate record.
func existingNote(raw []byte) string {
	var meta struct {
		Note string `json:"note"`
	}
	if json.Unmarshal(raw, &meta) == nil {
		return strings.TrimSpace(meta.Note)
	}
	return ""
}

// appendAuthNote keeps the existing operator-facing note and adds a lifecycle
// reason without discarding information written by CPA or another plugin.
func appendAuthNote(existing, addition string) string {
	existing = strings.TrimSpace(existing)
	addition = strings.TrimSpace(addition)
	switch {
	case existing == "":
		return addition
	case addition == "", strings.Contains(existing, addition):
		return existing
	default:
		return existing + " · " + addition
	}
}

// buildAuthFileJSON produces a new auth-store payload. When there is no
// existing document it creates the canonical nested shape and metadata.
func buildAuthFileJSON(sa *storedAuth, disabled bool, note string, extra map[string]any) ([]byte, error) {
	return buildAuthFileJSONFromExisting(nil, sa, disabled, note, extra)
}

// buildAuthFileJSONFromExisting updates only the credential fields and
// lifecycle metadata. All unrelated top-level fields, including provider,
// type, and unknown metadata, are retained verbatim in the JSON object.
// Explicit extra values are reserved for intentional migrations (for example,
// adopting a legacy record into the workbuddy provider).
func buildAuthFileJSONFromExisting(existing []byte, sa *storedAuth, disabled bool, note string, extra map[string]any) ([]byte, error) {
	out := make(map[string]any)
	if len(strings.TrimSpace(string(existing))) > 0 {
		if err := json.Unmarshal(existing, &out); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = make(map[string]any)
	}

	// New/untyped records need the plugin identity. Existing values are not
	// rewritten: a legacy type/provider is meaningful to CPA routing and is
	// retained until CPA management explicitly cleans up the old record.
	if _, ok := out["type"]; !ok {
		out["type"] = providerName
	}
	if _, ok := out["provider"]; !ok {
		out["provider"] = providerName
	}
	if _, ok := out["logo"]; !ok {
		out["logo"] = pluginLogoURL
	}

	if sa != nil {
		storage, err := json.Marshal(sa)
		if err != nil {
			return nil, err
		}
		var nested map[string]any
		if err := json.Unmarshal(storage, &nested); err != nil {
			return nil, err
		}
		if auth, ok := nested["auth"]; ok {
			out["auth"] = auth
		}
		if account, ok := nested["account"]; ok {
			out["account"] = account
		}
		// Identity metadata (v0.9.32). host.auth.save rebuilds the auth record
		// from THIS file (internal/pluginhost/auth_callbacks.go
		// buildAuthFromFileData) and derives the row label as:
		//
		//	label := provider                      // "workbuddy"
		//	if email := metadata["email"]; … { label = email }
		//
		// The plugin previously wrote no identity here, so the first lifecycle
		// save (disable / note sync / keepalive) silently relabelled the account
		// from its nickname to "workbuddy" even though the credential still held
		// the name. Writing the identity into the file keeps the label correct
		// after any save and gives CPA's auth list a real account name to render.
		//
		// Only set when absent: an operator's own edit via CPA management wins.
		if _, ok := out["email"]; !ok {
			if name := accountNameForAuth(sa); name != "" {
				out["email"] = name
			}
		}
		if _, ok := out["account_name"]; !ok {
			if name := accountNameForAuth(sa); name != "" {
				out["account_name"] = name
			}
		}
		if _, ok := out["uid"]; !ok {
			if uid := strings.TrimSpace(sa.Account.UID); uid != "" {
				out["uid"] = uid
			}
		}
		if _, ok := out["enterprise_id"]; !ok {
			if eid := strings.TrimSpace(sa.Account.EnterpriseID); eid != "" {
				out["enterprise_id"] = eid
			}
		}
	}
	out["disabled"] = disabled
	if note != "" || existingNote(existing) == "" {
		out["note"] = note
	}
	for k, v := range extra {
		out[k] = v
	}
	return json.Marshal(out)
}

// parseDisabledFromAuthJSON reads top-level disabled from physical auth JSON.
func parseDisabledFromAuthJSON(raw []byte) bool {
	var m struct {
		Disabled bool `json:"disabled"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Disabled
}

// saveAuthState writes an auth record through host.auth.save while retaining
// the host-provided name and all unrelated metadata.
func saveAuthState(name string, existing []byte, sa *storedAuth, disabled bool, note string, extra map[string]any) error {
	raw, err := buildAuthFileJSONFromExisting(existing, sa, disabled, note, extra)
	if err != nil {
		return err
	}
	return hostAuthPersist(name, raw)
}

// saveRetainedDisabledRecord marks a legacy or duplicate record disabled. It
// intentionally passes sa=nil so even nested credential fields and unknown
// storage members are left untouched.
func saveRetainedDisabledRecord(name string, phys *hostAuthPhysical, reason string) error {
	if phys == nil {
		return fmt.Errorf("nil auth record")
	}
	note := appendAuthNote(existingNote(phys.JSON), reason)
	return saveAuthState(name, phys.JSON, nil, true, note, nil)
}

// hostAuthSaveJSON persists credential JSON via host.auth.save.
func hostAuthSaveJSON(name string, raw []byte) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty auth file name")
	}
	saveReq := pluginapi.HostAuthSaveRequest{
		Name: name,
		JSON: raw,
	}
	saveBody, _ := json.Marshal(saveReq)
	rawResp, err := hostCall(pluginabi.MethodHostAuthSave, saveBody)
	if err != nil {
		return fmt.Errorf("host.auth.save: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(rawResp, &env); err != nil || !env.OK {
		msg := "host.auth.save failed"
		if env.Error != nil && env.Error.Message != "" {
			msg = truncateRedacted(env.Error.Message, 200)
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
