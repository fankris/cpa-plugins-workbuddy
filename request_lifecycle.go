// request_lifecycle.go consumes the host's request.complete event.
//
// # WHY THIS EXISTS
//
// UsagePlugin only fires for requests that reached THIS plugin's executor. When
// every workbuddy credential is disabled or cooling down, CPA rejects the
// request before dispatch — so no usage record is produced and the plugin sees
// nothing. The single most important signal for a quota-exhausted provider
// ("traffic arrived and we could not serve any of it") was therefore invisible.
//
// RequestLifecyclePlugin closes that gap: the host sends one terminal event per
// request that reached request interception, with an Outcome of
// succeeded / failed / rejected / canceled. A run of `rejected` events is
// exactly "requests arrived but no credential could serve them".
//
// Scope discipline: this is OBSERVABILITY, not control. It never disables an
// account, never touches credits, and never fails the host call — the request
// it describes is already over, and an error here would be logged as a plugin
// fault for something the plugin cannot influence.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// requestCompleteRPCRequest mirrors the host's request.complete wrapper.
type requestCompleteRPCRequest struct {
	pluginapi.RequestCompletion
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// The health bar is NOT built here. The host already rings 20 x 10-minute
// success/failure buckets PER CREDENTIAL (sdk/cliproxy/auth
// RecentRequestsSnapshot), records every routed request into them, and exposes
// them through host.auth.get_runtime — measured, not assumed. A plugin-side
// reimplementation could only ever see requests that reached this plugin's
// executor, so it would under-count and could not attribute per account.
// See panel.go hostAuthRuntimeFor.

// lifecycleMaxEvents bounds the retained event ring. The ring exists so the
// panel can show WHY requests are failing (the host's error strings), not to
// keep a complete history.
const lifecycleMaxEvents = 50

// outcomeCounters is a sliding-window tally of terminal outcomes.
type outcomeCounters struct {
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Rejected  int64 `json:"rejected"`
	Canceled  int64 `json:"canceled"`
}

// lifecycleEvent is one retained terminal event for the panel.
type lifecycleEvent struct {
	At time.Time `json:"at"`
	// AuthID is the credential CPA routed this request to, when the host
	// exposes it (completion.Metadata["selected_auth_id"]). Empty when absent,
	// which is why the health bar falls back to provider-wide.
	AuthID   string `json:"auth_id,omitempty"`
	Outcome  string `json:"outcome"`
	Model    string `json:"model,omitempty"`
	Stream   bool   `json:"stream,omitempty"`
	Status   int    `json:"status,omitempty"`
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"duration_ms,omitempty"`
}

var (
	requestLifecycleMu     sync.Mutex
	requestLifecycleCounts = map[string]int64{} // outcome -> count (windowed by reset)
	requestLifecycleEvents []lifecycleEvent     // newest first, capped
	// requestLifecycleFailStreak counts CONSECUTIVE requests that did not
	// succeed. A single failure is noise; a run with no success between is the
	// actionable "nothing is being served" signal, so it is tracked separately
	// rather than derived from the totals.
	//
	// Canceled is excluded: a client disconnect is normal traffic, not a
	// provider problem.
	requestLifecycleFailStreak int64
)

// handleRequestComplete is the request.complete RPC entry point.
//
// Always acknowledges. The described request is already finished, so returning
// an error could only mark the plugin faulty for something it cannot change.
func handleRequestComplete(raw []byte) ([]byte, error) {
	var req requestCompleteRPCRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return okEnvelope(map[string]any{})
		}
	}
	recordRequestCompletion(req.RequestCompletion)
	return okEnvelope(map[string]any{})
}

// recordRequestCompletion folds one terminal event into the windowed counters.
func recordRequestCompletion(c pluginapi.RequestCompletion) {
	outcome := strings.TrimSpace(string(c.Outcome))
	if outcome == "" {
		outcome = "unknown"
	}

	// Duration is derived here rather than taken from the host: StartedAt and
	// CompletedAt are both provided, and computing it keeps the event
	// self-describing even if the host later stops sending one of them.
	var durationMs int64
	if !c.StartedAt.IsZero() && !c.CompletedAt.IsZero() {
		if d := c.CompletedAt.Sub(c.StartedAt); d > 0 {
			durationMs = d.Milliseconds()
		}
	}
	at := c.CompletedAt
	if at.IsZero() {
		at = time.Now()
	}

	requestLifecycleMu.Lock()
	defer requestLifecycleMu.Unlock()

	requestLifecycleCounts[outcome]++

	switch outcome {
	case string(pluginapi.RequestCompletionSucceeded):
		requestLifecycleFailStreak = 0
	case string(pluginapi.RequestCompletionCanceled):
		// Neither a success nor a provider failure: leave the streak alone
		// rather than resetting it (a cancel proves nothing about the provider)
		// or extending it (it is not a failure).
	default:
		requestLifecycleFailStreak++
	}

	// Retain failures and rejects only: a ring full of successes would push the
	// interesting events out within seconds on a busy instance.
	if outcome != string(pluginapi.RequestCompletionSucceeded) {
		requestLifecycleEvents = append([]lifecycleEvent{{
			At:       at,
			Outcome:  outcome,
			Model:    strings.TrimSpace(c.Model),
			Stream:   c.Stream,
			Status:   c.StatusCode,
			Error:    truncateForPanel(c.Error, 300),
			Duration: durationMs,
		}}, requestLifecycleEvents...)
		if len(requestLifecycleEvents) > lifecycleMaxEvents {
			requestLifecycleEvents = requestLifecycleEvents[:lifecycleMaxEvents]
		}
	}

	// Log a sustained failure streak once, at the moment it becomes actionable.
	// Logging every failure would flood the host log during an outage.
	//
	// Field names matter: the host's log formatter prints only a fixed
	// allowlist (logFieldOrder in internal/logging/global_logger.go), so a
	// custom key like "fail_streak" is silently dropped. `reason` is on the
	// list, so the actionable detail goes there and is visible in CPA's log.
	if requestLifecycleFailStreak == 5 {
		hostLog(logLevelWarn, fmt.Sprintf("%d consecutive requests did not succeed — no workbuddy credential may be able to serve traffic", requestLifecycleFailStreak), map[string]any{
			"provider": providerName,
			"reason":   "no_usable_credential",
		})
	}
}

// truncateForPanel bounds a host-supplied error string for display. Rune-safe so
// a multi-byte truncation cannot produce invalid UTF-8 in the JSON payload.
func truncateForPanel(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// requestLifecycleSnapshot returns the windowed counters, the failure streak
// and the retained events for the panel.
func requestLifecycleSnapshot() map[string]any {
	requestLifecycleMu.Lock()
	defer requestLifecycleMu.Unlock()

	counts := make(map[string]int64, len(requestLifecycleCounts))
	for k, v := range requestLifecycleCounts {
		counts[k] = v
	}
	events := make([]lifecycleEvent, len(requestLifecycleEvents))
	copy(events, requestLifecycleEvents)

	out := map[string]any{
		"counts":      counts,
		"fail_streak": requestLifecycleFailStreak,
		"events":      events,
	}
	// Surface the actionable condition directly so the panel does not have to
	// re-derive it (and cannot derive it differently).
	out["all_failing"] = requestLifecycleFailStreak >= 5
	return out
}

// resetRequestLifecycleForTest clears the windowed state.
func resetRequestLifecycleForTest() {
	requestLifecycleMu.Lock()
	requestLifecycleCounts = map[string]int64{}
	requestLifecycleEvents = nil
	requestLifecycleFailStreak = 0
	requestLifecycleMu.Unlock()
}
