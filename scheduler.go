// scheduler.go implements CPA scheduler.pick for WorkBuddy.
// "host" is the rebuild default; "builtin" and "off" explicitly request round-robin.
// "host" declines selection so CPA keeps its configured selector, including
// fill-first/affinity behavior. "credits" retains the plugin's active-account
// behavior. Explicit historical mode values remain supported; an omitted mode now follows CPA.
package main

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// scheduler_mode values.
//
// Legacy values keep their prior meaning; host is explicitly opt-in.
const (
	schedulerModeHost    = "host"
	schedulerModeBuiltin = "builtin"
	schedulerModeOff     = "off"
	schedulerModeCredits = "credits"

	// builtinStrategy is the CPA built-in strategy this plugin delegates to.
	builtinStrategy = pluginapi.SchedulerBuiltinRoundRobin
)

var (
	schedulerMode   = schedulerModeHost
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

// handleSchedulerPick keeps the configured host policy in host mode and
// preserves existing builtin/off and credits behavior for compatibility.
func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}

	mode := loadedSchedulerMode()
	if mode == schedulerModeHost {
		// CPA v8.0.13 pickViaPluginScheduler returns unhandled here; its
		// caller then invokes the configured selector for single/mixed routes.
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}
	// Preserve the historical explicitly named round-robin strategy.
	if mode != schedulerModeCredits {
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
