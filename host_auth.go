// host_auth.go wraps the host's auth-store RPC (host.auth.list / get /
// get_bundle). These are the only paths the plugin uses to read auth files;
// writes go through hostAuthPersist / hostAuthPersistMigrate in lifecycle.go.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// rpcHostAuthListResponse mirrors the host's host.auth.list envelope result.
type rpcHostAuthListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

type rpcHostAuthGetResponse struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name"`
	Path      string          `json:"path"`
	JSON      json.RawMessage `json:"json"`
}

// hostAuthList returns all workbuddy credentials known to the host.
func hostAuthList() ([]pluginapi.HostAuthFileEntry, error) {
	raw, err := hostCall(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		return nil, fmt.Errorf("host.auth.list: bad envelope")
	}
	var resp rpcHostAuthListResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		return nil, err
	}
	// Fresh slice — resp.Files[:0] would alias the RPC response's backing
	// array (P1-3: fragile pattern, safe today but could break if resp is
	// ever cached/reused).
	//
	// CPA has already classified these entries. Respect explicit provider/type,
	// including user-named files, while retaining our legacy untyped filenames.
	// Do not rename files or adopt another provider just because of a prefix.
	out := make([]pluginapi.HostAuthFileEntry, 0, len(resp.Files))
	for _, f := range resp.Files {
		// Content guard: a file can carry our filename prefix while its body
		// belongs to another plugin (e.g. a qoder auth saved under a
		// workbuddy- name by a third-party tool). Running it through this
		// plugin's endpoints 401s against the wrong upstream with a cryptic
		// APISIX HTML page. Entries WITHOUT a type stay eligible — the
		// filename prefix remains their only discriminator.
		if foreign, owner := foreignAuthOwner(f.Type, f.Provider); foreign {
			log.Printf("workbuddy: auth %s skipped — credential type %q belongs to the %s plugin, not workbuddy", f.Name, owner, owner)
			continue
		}
		if !isOurDeclaredType(f.Type) && !isOurDeclaredType(f.Provider) && !isOurFamilyFileName(f.Name) {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// foreignAuthOwner reports whether the host-resolved type/provider of an
// auth entry names a plugin outside this one's family. Returns (false, "")
// when no type is declared (legacy files — the filename prefix is then the
// only discriminator) or when the value belongs to the workbuddy/codebuddy
// merged family.
func foreignAuthOwner(entryType, entryProvider string) (bool, string) {
	t := strings.ToLower(strings.TrimSpace(entryType))
	if t == "" {
		t = strings.ToLower(strings.TrimSpace(entryProvider))
	}
	switch t {
	case "", "workbuddy", "workbuddy-cn", "workbuddy-global", "workbuddy-intl",
		"codebuddy", "codebuddy-cn", "codebuddy-intl":
		return false, ""
	default:
		return true, t
	}
}

// hostAuthGet fetches the credential JSON for one auth index.
func hostAuthGet(authIndex string) (*storedAuth, error) {
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return nil, err
	}
	return parseStored(phys.JSON)
}

// hostAuthGetBundle is one host.auth.get for both storage and physical metadata
// (avoids the previous double-RPC in dashboard: get + getPhysical).
func hostAuthGetBundle(authIndex string) (*storedAuth, *hostAuthPhysical, error) {
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return nil, nil, err
	}
	sa, err := parseStored(phys.JSON)
	if err != nil {
		return nil, phys, err
	}
	return sa, phys, nil
}

// ---------------------------------------------------------------------------
// host.auth.get_runtime — the host's OWN view of a credential's runtime state
// ---------------------------------------------------------------------------

// hostAuthRuntime is the subset of the host's runtime credential view this
// plugin consumes.
//
// Why this exists: host.auth.get returns only {auth_index, name, path, json},
// so the plugin had to maintain its OWN opinion of "exhausted / disabled /
// cooling down" (cachedCreditsScore + lifecycleState). Those two views can
// disagree — the plugin's cache is a guess about state the host owns — and
// every disagreement shows up as a panel that contradicts actual routing.
// get_runtime returns the authoritative view instead.
//
// Fields are pointers/zero-valued where the host may omit them, so "absent"
// stays distinguishable from "false".
type hostAuthRuntime struct {
	AuthIndex     string `json:"auth_index,omitempty"`
	ID            string `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
	Status        string `json:"status,omitempty"`
	StatusMessage string `json:"status_message,omitempty"`
	Disabled      bool   `json:"disabled,omitempty"`
	Unavailable   bool   `json:"unavailable,omitempty"`
	RuntimeOnly   bool   `json:"runtime_only,omitempty"`
	// NextRetryAfter is when the host considers the credential usable again
	// (cooldown expiry). Zero means "no cooldown".
	NextRetryAfter time.Time `json:"next_retry_after,omitempty"`
	Priority       int       `json:"priority,omitempty"`
	Success        int64     `json:"success,omitempty"`
	Failed         int64     `json:"failed,omitempty"`
	LastRefresh    time.Time `json:"last_refresh,omitempty"`
	Email          string    `json:"email,omitempty"`
	// RecentRequests is the host's OWN per-credential time series: 20 buckets
	// of 10 minutes, attributed to this exact credential. See
	// hostAuthRuntimeFor for why the plugin consumes this instead of
	// maintaining its own counters.
	RecentRequests []hostRecentRequestBucket `json:"recent_requests,omitempty"`
}

// hostRecentRequestBucket is one time slice of the host's recent-request ring.
type hostRecentRequestBucket struct {
	// Time is the host-formatted local label, e.g. "19:20-19:30".
	Time    string `json:"time"`
	Success int64  `json:"success"`
	Failed  int64  `json:"failed"`
}

// hostAuthGetRuntime fetches the host's runtime view of one credential.
//
// Returns (nil, nil) — not an error — when the host does not support the RPC or
// the credential is unknown: this is a read-only enrichment for the panel, and
// a host that predates the RPC (or a credential deleted mid-flight) must not
// break the dashboard. Callers treat nil as "runtime view unavailable".
func hostAuthGetRuntime(authIndex string) (*hostAuthRuntime, error) {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return nil, nil
	}
	raw, err := hostCall(pluginabi.MethodHostAuthGetRuntime, mustJSON(map[string]string{"auth_index": authIndex}))
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		return nil, nil
	}
	var resp struct {
		Auth hostAuthRuntime `json:"auth"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		return nil, nil
	}
	if resp.Auth.AuthIndex == "" {
		resp.Auth.AuthIndex = authIndex
	}
	return &resp.Auth, nil
}
