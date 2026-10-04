// host_log.go routes plugin logging through the host's log pipeline.
//
// Before this, the plugin used the standard library logger directly, which
// meant: no log level (everything is one level), no per-plugin filtering in
// CPA's log view, and no request-id correlation for messages emitted during a
// request (the host attaches the request id when it handles host.log).
//
// Fallback behaviour matters more than the happy path here: if the host does
// not implement host.log, or the plugin is quiescing (callbacks are refused
// then), the message MUST still reach the process log. Losing diagnostics
// because a logging bridge is unavailable would be a bad trade — the bridge is
// an improvement, not a requirement.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

// Log levels accepted by the host's host.log handler. Anything else falls back
// to debug on the host side, so unknown values degrade quietly.
const (
	logLevelTrace = "trace"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
)

// hostLog emits one message through the host pipeline, falling back to the
// process logger. Never returns an error: logging must not be able to fail a
// caller.
func hostLog(level, message string, fields map[string]any) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	level = strings.ToLower(strings.TrimSpace(level))

	// Skip the bridge entirely while quiescing: hostCallbackStart would refuse
	// the call anyway, and a refused callback during shutdown is noise.
	if pluginQuiescing() {
		log.Printf("workbuddy: %s", message)
		return
	}

	payload := map[string]any{
		"level":   level,
		"message": message,
	}
	if len(fields) > 0 {
		payload["fields"] = fields
	}
	if _, err := hostCall(pluginabi.MethodHostLog, mustJSON(payload)); err != nil {
		// Host bridge unavailable or RPC failed — keep the message visible.
		log.Printf("workbuddy: %s", message)
	}
}

// hostLogf is the formatting convenience used by call sites.
func hostLogf(level, format string, args ...any) {
	hostLog(level, fmt.Sprintf(format, args...), nil)
}
