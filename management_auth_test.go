package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Since 0.9.25 the plugin no longer enforces its own management key: the host
// management middleware is the sole guard (official plugin spec), and a second
// gate 403'd mutating calls whenever the configured key differed from the
// credential the embedding UI forwards (CPAMP substitutes its saved CPA key).
// 0.9.30 removed the management_key config entirely — it only ever existed as
// the CPAMP usage-key fallback for the deleted direct push.
func TestMutatingEndpointsIgnoreCallerCredentials(t *testing.T) {
	headers := []http.Header{
		{"Authorization": []string{"Bearer wrong-key"}},
		{"X-Management-Key": []string{"also-wrong"}},
		nil,
	}
	for i, h := range headers {
		raw, err := handleManagement(mustManagementRequest(http.MethodPost, "/checkin/config", `{"enabled":false}`, h))
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !env.OK {
			t.Fatalf("case %d: mutating call rejected with misaligned key: %s", i, string(env.Result))
		}
	}
}

// The per-IP token bucket still guards mutating endpoints; it must absorb a
// bulk "check in all" run (capacity 30) before tripping.
func TestMutatingRateLimitAbsorbsBulkThenTrips(t *testing.T) {
	const ip = "203.0.113.7"
	allowed := 0
	for i := 0; i < mgmtRateLimitCapacity; i++ {
		if allowManagementRequest(ip) {
			allowed++
		}
	}
	if allowed != mgmtRateLimitCapacity {
		t.Fatalf("bulk burst: allowed %d want %d", allowed, mgmtRateLimitCapacity)
	}
	if allowManagementRequest(ip) {
		t.Fatal("bucket should be exhausted after burst")
	}
}

func mustManagementRequest(method, subPath, body string, headers http.Header) []byte {
	base := loadedManagementBasePath() + "/plugins/" + providerName
	if !strings.HasPrefix(subPath, "/") {
		subPath = "/" + subPath
	}
	req := pluginapi.ManagementRequest{
		Method:  method,
		Path:    base + subPath,
		Headers: headers,
	}
	if body != "" {
		req.Body = []byte(body)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return raw
}
