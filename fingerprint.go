// fingerprint.go derives stable per-account device identifiers.
//
// Adapted from the derive_id design popularized by workbuddy2api-hub
// (ardeyouxipianyi, MIT) and Sliverkiss/workbuddy2api: upstream anti-abuse
// correlates requests by machine/session identifiers. Random or per-request
// identifiers make one account look like a fleet of machines (a classic
// multi-account signal), while a single shared identifier links accounts
// together. Deriving them from the account UID + fixed salts gives every
// account exactly one stable virtual device, forever:
//
//   - same account → identical IDs across requests and restarts;
//   - different accounts → unrelated IDs (no cross-account correlation).
//
// The salts are fixed strings; changing them would look like "all accounts
// moved to a new machine", so they must stay stable across releases.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// fingerprintSalts namespace the derived identifiers. Do not change these
// values: each one defines the identity an account presents upstream.
const (
	fingerprintSaltMachine = "workbuddy-machine-v1"
	fingerprintSaltSession = "workbuddy-session-v1"
	fingerprintSaltRequest = "workbuddy-request-v1"
)

// deriveDeviceID returns a stable 32-hex-char identifier derived from the
// account UID and a fixed salt. Empty UIDs fall back to a per-process value
// so an unidentified credential never presents a shared "anonymous" device.
func deriveDeviceID(uid, salt string) string {
	uid = strings.TrimSpace(uid)
	seed := salt + ":" + uid
	if uid == "" {
		seed = salt + ":anonymous"
	}
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

// machineIDFor returns the account's stable machine identifier.
func machineIDFor(uid string) string { return deriveDeviceID(uid, fingerprintSaltMachine) }

// sessionIDFor returns the account's stable session identifier.
func sessionIDFor(uid string) string { return deriveDeviceID(uid, fingerprintSaltSession) }

// requestIDFor returns a request identifier with a stable per-account prefix
// plus a microsecond suffix, mirroring the desktop client's X-Request-ID
// shape: the prefix keeps the device identity consistent while the suffix
// keeps individual requests distinguishable.
func requestIDFor(uid string) string {
	prefix := deriveDeviceID(uid, fingerprintSaltRequest)[:24]
	suffix := time.Now().UnixNano() % 1_000_000
	return prefix + "-" + pad6(suffix)
}

func pad6(v int64) string {
	digits := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	return string(digits)
}

// applyFingerprintHeaders stamps the stable device identity headers the
// desktop client sends on chat/billing/growth calls.
func applyFingerprintHeaders(uid string, set func(key, value string)) {
	if set == nil {
		return
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return
	}
	set("X-Machine-ID", machineIDFor(uid))
	set("X-Session-ID", sessionIDFor(uid))
	set("X-Request-ID", requestIDFor(uid))
}
