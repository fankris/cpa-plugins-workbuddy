// scheduler.go implements the CPA scheduler.pick capability for workbuddy.
//
// Two modes, selected by the `scheduler_mode` config key:
//
//   - "builtin" (DEFAULT) — the plugin makes no routing decision at all and
//     explicitly hands selection to CPA's built-in scheduler by returning
//     DelegateBuiltin. This is not the same as returning Handled:false: the
//     latter only means "this plugin declines", leaving whatever the host does
//     next unspecified. DelegateBuiltin names the strategy outright, so the
//     behaviour is pinned to CPA's own round-robin/fill-first implementation
//     and the plugin cannot drift from it.
//   - "credits" — the plugin picks the panel-selected active account (sticky,
//     falling back when that account becomes exhausted/disabled).
//
// Non-workbuddy candidates are always deferred so other providers keep using
// the built-in scheduler regardless of mode.
package main

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// scheduler_mode values.
//
// schedulerModeBuiltin is the default: defer to CPA's own scheduler by naming
// the strategy. schedulerModeOff is retained as an accepted alias for
// "builtin" so existing configs that spell it "off" keep working, but it is no
// longer the internal default — "off" and "builtin" now behave identically
// (both delegate), which removes the old ambiguity where "off" meant
// Handled:false and left the host to decide.
const (
	schedulerModeBuiltin = "builtin"
	schedulerModeOff     = "off"
	schedulerModeCredits = "credits"

	// builtinStrategy is the CPA built-in strategy this plugin delegates to.
	builtinStrategy = pluginapi.SchedulerBuiltinRoundRobin
)

var (
	schedulerMode   = schedulerModeBuiltin
	schedulerModeMu sync.RWMutex
)

// setSchedulerMode is a test helper that returns a restore func.
func setSchedulerMode(mode string) func() {
	schedulerModeMu.Lock()
	old := schedulerMode
	schedulerMode = mode
	schedulerModeMu.Unlock()
	return func() {
		schedulerModeMu.Lock()
		schedulerMode = old
		schedulerModeMu.Unlock()
	}
}

func loadedSchedulerMode() string {
	schedulerModeMu.RLock()
	defer schedulerModeMu.RUnlock()
	return schedulerMode
}

// handleSchedulerPick selects a workbuddy auth candidate, or delegates the
// decision to CPA's built-in scheduler.
//
// scheduler_mode:
//   - "builtin" / "off" (DEFAULT) → plugin declines and names the built-in
//     strategy via DelegateBuiltin, so CPA's own scheduler picks. The plugin
//     never influences which account serves a request in this mode.
//   - "credits" → plugin picks via panel-selected active account (sticky, with
//     fallback when that account becomes exhausted/disabled). If every WorkBuddy
//     candidate is disabled, the plugin returns CPA terminal rejection instead
//     of letting the built-in scheduler select a disabled account.
func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}

	// Default path: hand selection to CPA's built-in scheduler explicitly.
	// DelegateBuiltin (not Handled:false) is deliberate — it pins the strategy
	// to CPA's implementation instead of leaving the outcome unspecified.
	if loadedSchedulerMode() != schedulerModeCredits {
		return okEnvelope(pluginapi.SchedulerPickResponse{
			DelegateBuiltin: builtinStrategy,
			Handled:         true,
		})
	}

	// Collect WorkBuddy candidates only. Keep unrelated providers on CPA's built-in path even when WorkBuddy candidates in a mixed request are disabled.
	var wbCandidates []pluginapi.SchedulerAuthCandidate
	sawWorkBuddyCandidate := false
	sawOtherProviderCandidate := false
	for _, c := range req.Candidates {
		if c.Provider != providerName {
			sawOtherProviderCandidate = true
			continue
		}
		sawWorkBuddyCandidate = true
		if candidateDisabled(c) {
			continue
		}
		wbCandidates = append(wbCandidates, c)
	}
	if len(wbCandidates) == 0 {
		if sawWorkBuddyCandidate && !sawOtherProviderCandidate {
			return okEnvelope(pluginapi.SchedulerPickResponse{
				Handled:      true,
				Reject:       true,
				RejectCode:   "auth_unavailable",
				RejectReason: "all WorkBuddy accounts are disabled",
			})
		}
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}

	// Build thin view for active-auth picker.
	cands := make([]activeAuthCandidate, 0, len(wbCandidates))
	for _, c := range wbCandidates {
		_, exhausted := cachedCreditsScore(c.ID)
		cands = append(cands, activeAuthCandidate{
			ID:        c.ID,
			Disabled:  false, // already filtered
			Exhausted: exhausted,
		})
	}
	picked := pickActiveAuth(cands)
	if picked == "" {
		return okEnvelope(pluginapi.SchedulerPickResponse{
			Handled:      true,
			Reject:       true,
			RejectCode:   "auth_unavailable",
			RejectReason: "no selectable WorkBuddy account is available",
		})
	}
	return okEnvelope(pluginapi.SchedulerPickResponse{
		AuthID:  picked,
		Handled: true,
	})
}

// candidateDisabled reports host-disabled auth from Status/metadata.
func candidateDisabled(c pluginapi.SchedulerAuthCandidate) bool {
	st := strings.ToLower(strings.TrimSpace(c.Status))
	if st == "disabled" {
		return true
	}
	if c.Metadata != nil {
		if v, ok := c.Metadata["disabled"]; ok {
			switch t := v.(type) {
			case bool:
				return t
			case string:
				return strings.EqualFold(strings.TrimSpace(t), "true")
			}
		}
	}
	return false
}

// cachedCreditsScore returns (remain, exhausted) from accountCache.
// remain is -1 when unknown; exhausted uses isCreditsExhausted.
// Key is auth.ID (same as SchedulerAuthCandidate.ID and activeAuthID).
func cachedCreditsScore(authID string) (int64, bool) {
	v, ok := accountCache.Load(authID)
	if !ok {
		return -1, false
	}
	entry, ok := v.(*accountCacheEntry)
	if !ok || entry.credits == nil {
		return -1, false
	}
	return entry.credits.TotalRemain, isCreditsExhausted(entry.credits)
}
