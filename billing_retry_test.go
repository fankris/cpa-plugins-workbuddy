package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBillingCall_RetriesOn5xx verifies that a transient upstream 500 is
// retried and ultimately succeeds when the next attempt returns 200.
func TestBillingCall_RetriesOn5xx(t *testing.T) {
	orig := billingRetryDelays
	billingRetryDelays = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { billingRetryDelays = orig }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 { // first two attempts → 500
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"k":"v"}}`))
	}))
	defer srv.Close()

	// Temporarily override billingBase so the test server is used.
	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{}
	data, err := billingCall(sa, "/test", nil)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if string(data) != `{"k":"v"}` {
		t.Fatalf("unexpected data: %s", string(data))
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls (2 retry), got %d", calls)
	}
}

// TestBillingCall_NoRetryOn4xx verifies that business-level errors (4xx,
// non-zero code) are not retried.
func TestBillingCall_NoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"msg":"bad request"}`))
	}))
	defer srv.Close()

	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{}
	_, err := billingCall(sa, "/test", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should not retry on 4xx — exactly 1 call.
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry on 4xx), got %d", calls)
	}
}

// TestIsTransientBillingErr covers classification boundaries. Classification is
// now typed (errors.As on *upstreamError) instead of string matching, so a
// wrapped error keeps its meaning and 429 counts as transient.
func TestIsTransientBillingErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"500", &upstreamError{StatusCode: 500, Path: "/v2/billing"}, true},
		{"503", &upstreamError{StatusCode: 503, Path: "/v2/billing"}, true},
		{"429 rate limit", &upstreamError{StatusCode: 429, Path: "/v2/billing"}, true},
		{"transport failure", &upstreamError{Path: "/v2/billing", Err: errors.New("connection reset")}, true},
		{"400 business", &upstreamError{StatusCode: 400, Path: "/v2/billing"}, false},
		{"401 auth", &upstreamError{StatusCode: 401, Path: "/v2/billing"}, false},
		{"wrapped 500 still transient", fmt.Errorf("refresh credits: %w", &upstreamError{StatusCode: 500}), true},
		{"wrapped 400 still not transient", fmt.Errorf("refresh credits: %w", &upstreamError{StatusCode: 400}), false},
		{"business code string", errors.New("code=10000 msg=API request failed"), false},
		{"parse failure string", errors.New("parse failed: unexpected EOF"), false},
	}
	for _, tt := range tests {
		if got := isTransientBillingErr(tt.err); got != tt.want {
			t.Errorf("%s: isTransientBillingErr(%v) = %v, want %v", tt.name, tt.err, got, tt.want)
		}
	}
}

// A 429 must be retried (previously it was not retried at all), and the
// upstream's Retry-After must be honoured over the configured backoff.
func TestBillingCall_RetriesOn429AndHonoursRetryAfter(t *testing.T) {
	orig := billingRetryDelays
	billingRetryDelays = []time.Duration{50 * time.Millisecond}
	defer func() { billingRetryDelays = orig }()

	var calls int32
	var secondAttemptAt time.Time
	start := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1") // 1s, longer than the 50ms backoff
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		secondAttemptAt = time.Now()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"k":"v"}}`))
	}))
	defer srv.Close()

	restore := setBillingBase(srv.URL)
	defer restore()

	data, err := billingCall(&storedAuth{}, "/test", nil)
	if err != nil {
		t.Fatalf("429 should be retried and succeed: %v", err)
	}
	if string(data) != `{"k":"v"}` {
		t.Fatalf("unexpected data: %s", string(data))
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls (1 retry after 429), got %d", calls)
	}
	if waited := secondAttemptAt.Sub(start); waited < 900*time.Millisecond {
		t.Fatalf("Retry-After: 1 must be honoured (waited %v, expected ~1s)", waited)
	}
}

// A hostile Retry-After must be capped so a dashboard refresh cannot stall.
func TestRetryDelayIsCapped(t *testing.T) {
	err := &upstreamError{StatusCode: 429, RetryAfter: 10 * time.Minute}
	if got := retryDelayFor(err, time.Second); got != maxRetryAfter {
		t.Fatalf("retryDelayFor(10m) = %v, want cap %v", got, maxRetryAfter)
	}
	if got := retryDelayFor(&upstreamError{StatusCode: 500}, 300*time.Millisecond); got != 300*time.Millisecond {
		t.Fatalf("no Retry-After must fall back to the configured backoff, got %v", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("2"); got != 2*time.Second {
		t.Errorf(`parseRetryAfter("2") = %v, want 2s`, got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf(`parseRetryAfter("") = %v, want 0`, got)
	}
	if got := parseRetryAfter("not-a-date"); got != 0 {
		t.Errorf(`parseRetryAfter("not-a-date") = %v, want 0`, got)
	}
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 4*time.Second {
		t.Errorf("HTTP-date form = %v, want ~3s", got)
	}
}

// The error message must stay diagnostic (status + path + snippet) and must
// never leak credentials from the body.
func TestUpstreamErrorMessageIsRedacted(t *testing.T) {
	err := &upstreamError{
		StatusCode: 500,
		Path:       "/v2/billing/meter",
		Snippet:    redactedSnippet([]byte(`{"error":"Bearer sk-live-abcdefghijklmnop"}`), 120),
	}
	msg := err.Error()
	if !strings.Contains(msg, "500") || !strings.Contains(msg, "/v2/billing/meter") {
		t.Fatalf("message lost diagnostics: %s", msg)
	}
	if strings.Contains(msg, "sk-live-abcdefghijklmnop") {
		t.Fatalf("message leaked a credential: %s", msg)
	}
}
