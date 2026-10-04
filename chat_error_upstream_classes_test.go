package main

import (
	"strings"
	"testing"
)

// The upstream's code-6004 body embeds a reset stamp; the translator must name
// it and must NOT rewrite the 429 status (CPA's per-model cooldown is the
// correct scope for a model-level throttle).
func TestTranslateChatUpstreamError_ModelThrottle6004(t *testing.T) {
	body := `{"code":6004,"msg":"usage exceeds frequency limit, your usage will reset at 2026-09-19 18:29:03 UTC+8"}`
	status, err := translateChatUpstreamError(429, body, nil)
	if status != 429 {
		t.Fatalf("status = %d, want 429 (model-level throttle keeps CPA's model-scoped cooldown)", status)
	}
	msg := err.Error()
	if !strings.Contains(msg, "模型级限流") {
		t.Errorf("message should name the model-level throttle, got: %s", msg)
	}
	if !strings.Contains(msg, "2026-09-19 18:29:03") {
		t.Errorf("message should carry the parsed reset time, got: %s", msg)
	}
	if !strings.Contains(msg, "6004") {
		t.Errorf("message should carry the upstream code, got: %s", msg)
	}
}

func TestTranslateChatUpstreamError_ModelThrottle6004_SubstringFallback(t *testing.T) {
	// Envelope variants that don't parse as plain business JSON but carry the
	// same markers must still be classified.
	body := `<error>code=6004, usage exceeds frequency limit</error>`
	status, err := translateChatUpstreamError(429, body, nil)
	if status != 429 {
		t.Fatalf("status = %d, want 429", status)
	}
	if !strings.Contains(err.Error(), "模型级限流") {
		t.Errorf("substring fallback failed, got: %s", err.Error())
	}
}

func TestParseUpstreamResetAt(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string // "2006-01-02 15:04:05" in the parsed zone's local wall clock
		wantUTC string // expected instant rendered in UTC
	}{
		{
			name:    "with UTC+8 offset",
			body:    "your usage will reset at 2026-09-19 18:29:03 UTC+8",
			want:    "2026-09-19 18:29:03",
			wantUTC: "2026-09-19 10:29:03",
		},
		{
			name:    "with T separator and half-hour offset",
			body:    "reset at 2026-09-19T18:29:03 UTC+5:30",
			want:    "2026-09-19 18:29:03",
			wantUTC: "2026-09-19 12:59:03",
		},
		{
			name:    "negative offset",
			body:    "reset at 2026-09-19 18:29:03 UTC-3",
			want:    "2026-09-19 18:29:03",
			wantUTC: "2026-09-19 21:29:03",
		},
		{
			name: "no stamp",
			body: `{"code":6004,"msg":"usage exceeds frequency limit"}`,
		},
		{
			name: "garbage stamp",
			body: "reset at 2026-13-45 99:99:99",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseUpstreamResetAt(tc.body)
			if tc.want == "" {
				if !got.IsZero() {
					t.Fatalf("parseUpstreamResetAt = %v, want zero", got)
				}
				return
			}
			if got.IsZero() {
				t.Fatalf("parseUpstreamResetAt = zero, want %s", tc.want)
			}
			if got.Format("2006-01-02 15:04:05") != tc.want {
				t.Errorf("wall clock = %s, want %s", got.Format("2006-01-02 15:04:05"), tc.want)
			}
			if got.UTC().Format("2006-01-02 15:04:05") != tc.wantUTC {
				t.Errorf("utc = %s, want %s", got.UTC().Format("2006-01-02 15:04:05"), tc.wantUTC)
			}
		})
	}
}

// A chat 403 is the content-review filter, not a credential failure. The
// translator must remap it to 400 (CPA's request-scoped class: no cooldown,
// rotation continues) instead of letting CPA cool the credential for 30
// minutes over blocked content.
func TestTranslateChatUpstreamError_Content403RemapsToRequestScoped(t *testing.T) {
	body := `{"code":11140,"msg":"content filter policy violation"}`
	status, err := translateChatUpstreamError(403, body, nil)
	if status != 400 {
		t.Fatalf("status = %d, want 400 (request-scoped, no credential cooldown)", status)
	}
	msg := err.Error()
	if !strings.Contains(msg, "内容审核") {
		t.Errorf("message should name the content review, got: %s", msg)
	}
	if !strings.Contains(msg, "not penalized") {
		t.Errorf("message should tell the user the account is untouched, got: %s", msg)
	}
}

func TestTranslateChatUpstreamError_OtherFailuresKeepRawShape(t *testing.T) {
	body := `{"code":11101,"msg":"Parse message failed"}`
	status, err := translateChatUpstreamError(400, body, nil)
	if status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
	if !strings.Contains(err.Error(), "upstream 400:") {
		t.Errorf("raw shape must be preserved, got: %s", err.Error())
	}
}

func TestTranslateChatUpstreamError_11102Unchanged(t *testing.T) {
	body := `{"code":11102,"msg":"model [x] service info not found"}`
	status, err := translateChatUpstreamError(400, body, nil)
	if status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
	if !strings.Contains(err.Error(), "11102") {
		t.Errorf("11102 handling must stay, got: %s", err.Error())
	}
}

func TestIsModelRateLimited(t *testing.T) {
	if !isModelRateLimited(429, `{"code":6004,"msg":"usage exceeds frequency limit"}`) {
		t.Error("code 6004 with 429 should classify as model throttle")
	}
	if isModelRateLimited(500, `{"code":6004,"msg":"usage exceeds frequency limit"}`) {
		t.Error("6004 outside 429 is not the throttle class")
	}
	if isModelRateLimited(429, `{"code":11101,"msg":"invalid request"}`) {
		t.Error("plain 429 without 6004 markers must not classify")
	}
}
