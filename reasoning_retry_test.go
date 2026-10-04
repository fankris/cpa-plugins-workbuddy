package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestIsInvalidReasoningEffort(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   bool
	}{
		{http.StatusBadRequest, `{"code":11150}`, true},
		{http.StatusBadRequest, `{"extError":{"code":"invalid_reasoning_effort"}}`, true},
		{http.StatusInternalServerError, `{"code":11150}`, false},
		{http.StatusBadRequest, `{"msg":"invalid_reasoning_effort"}`, false},
		{http.StatusBadRequest, `<error>code 11150</error>`, false},
	} {
		if got := isInvalidReasoningEffort(tc.status, tc.body); got != tc.want {
			t.Errorf("isInvalidReasoningEffort(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
		}
	}
}

func TestOpenChatStreamWithReasoningRetryRemovesOnlyEffortAndRetriesOnce(t *testing.T) {
	old := hostStreamTestOverride
	t.Cleanup(func() { hostStreamTestOverride = old })

	ctx := withHostCallbackID(context.Background(), "retry-test-callback")
	body := `{"model":"deepseek-v4.1-flash","reasoning_effort":"high","reasoningEffort":"high","reasoning_summary":"auto","thinking":{"type":"enabled"},"messages":[{"role":"assistant","reasoning_content":"trace","content":"answer"}]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test/v2/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token")

	var requestBodies []string
	var callbackIDs []string
	hostStreamTestOverride = func(r *http.Request) (io.ReadCloser, int, error) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, 0, err
		}
		requestBodies = append(requestBodies, string(payload))
		callbackIDs = append(callbackIDs, hostCallbackIDFromRequest(r))
		if len(requestBodies) == 1 {
			return io.NopCloser(strings.NewReader(`{"code":11150,"extError":{"code":"invalid_reasoning_effort"}}`)), http.StatusBadRequest, nil
		}
		return io.NopCloser(strings.NewReader("data: retry-ok\n\n")), http.StatusOK, nil
	}

	stream, status, _, err := openChatStreamWithReasoningRetry(req, []byte(body))
	if err != nil {
		t.Fatalf("openChatStreamWithReasoningRetry: %v", err)
	}
	defer stream.Close()
	if status != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", status)
	}
	if len(requestBodies) != 2 {
		t.Fatalf("host stream calls = %d, want exactly 2", len(requestBodies))
	}
	if callbackIDs[0] != "retry-test-callback" || callbackIDs[1] != "retry-test-callback" {
		t.Fatalf("callback IDs = %#v, want context preserved on both attempts", callbackIDs)
	}
	if requestBodies[0] != body {
		t.Fatalf("first request body changed: %s", requestBodies[0])
	}
	var retried map[string]any
	if err := json.Unmarshal([]byte(requestBodies[1]), &retried); err != nil {
		t.Fatalf("decode retry body: %v", err)
	}
	for _, key := range []string{"reasoning_effort", "reasoningEffort", "reasoning_summary"} {
		if _, ok := retried[key]; ok {
			t.Errorf("retry body still contains %q: %v", key, retried[key])
		}
	}
	if retried["thinking"] == nil {
		t.Error("retry must preserve the thinking options")
	}
	messages, _ := retried["messages"].([]any)
	assistant, _ := messages[0].(map[string]any)
	if assistant["reasoning_content"] != "trace" {
		t.Errorf("retry must preserve message reasoning trace, got %v", assistant["reasoning_content"])
	}
	if got := req.Header.Get("Authorization"); got != "Bearer token" {
		t.Errorf("original request header changed: %q", got)
	}
	response, err := io.ReadAll(newHostStreamReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "data: retry-ok\n\n" {
		t.Fatalf("retry response = %q", response)
	}
}

func TestOpenChatStreamWithReasoningRetryDoesNotRetryOtherErrors(t *testing.T) {
	old := hostStreamTestOverride
	t.Cleanup(func() { hostStreamTestOverride = old })

	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"ordinary bad request", http.StatusBadRequest, `{"code":11101,"msg":"bad request"}`},
		{"wrong status", http.StatusInternalServerError, `{"code":11150}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			hostStreamTestOverride = func(*http.Request) (io.ReadCloser, int, error) {
				calls++
				return io.NopCloser(strings.NewReader(tc.body)), tc.status, nil
			}
			req, _ := http.NewRequest(http.MethodPost, "https://example.test/chat", strings.NewReader(`{"reasoning_effort":"high"}`))
			stream, status, _, err := openChatStreamWithReasoningRetry(req, []byte(`{"reasoning_effort":"high"}`))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if calls != 1 || status != tc.status {
				t.Fatalf("calls/status = %d/%d, want 1/%d", calls, status, tc.status)
			}
			got, err := io.ReadAll(newHostStreamReader(stream))
			if err != nil || string(got) != tc.body {
				t.Fatalf("response = %q, err=%v", got, err)
			}
		})
	}
}

func TestOpenChatStreamWithReasoningRetryStopsAfterSecondFailure(t *testing.T) {
	old := hostStreamTestOverride
	t.Cleanup(func() { hostStreamTestOverride = old })
	calls := 0
	hostStreamTestOverride = func(*http.Request) (io.ReadCloser, int, error) {
		calls++
		return io.NopCloser(strings.NewReader(`{"code":11150}`)), http.StatusBadRequest, nil
	}
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/chat", strings.NewReader(`{"reasoning_effort":"high"}`))
	stream, status, _, err := openChatStreamWithReasoningRetry(req, []byte(`{"reasoning_effort":"high"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if calls != 2 || status != http.StatusBadRequest {
		t.Fatalf("calls/status = %d/%d, want 2/400", calls, status)
	}
	got, err := io.ReadAll(newHostStreamReader(stream))
	if err != nil || string(got) != `{"code":11150}` {
		t.Fatalf("final response = %q, err=%v", got, err)
	}
}

func TestOpenChatStreamWithReasoningRetryReturnsReadError(t *testing.T) {
	old := hostStreamTestOverride
	t.Cleanup(func() { hostStreamTestOverride = old })
	calls := 0
	hostStreamTestOverride = func(*http.Request) (io.ReadCloser, int, error) {
		calls++
		return errorReadCloser{}, http.StatusBadRequest, nil
	}
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/chat", strings.NewReader(`{"reasoning_effort":"high"}`))
	_, _, _, err := openChatStreamWithReasoningRetry(req, []byte(`{"reasoning_effort":"high"}`))
	if err == nil || !strings.Contains(err.Error(), errTestRead.Error()) {
		t.Fatalf("error = %v, want %v", err, errTestRead)
	}
	if calls != 1 {
		t.Fatalf("host calls = %d, want 1", calls)
	}
}

type errorReadCloser struct{}

var errTestRead = errors.New("test stream read failure")

func (errorReadCloser) Read([]byte) (int, error) { return 0, errTestRead }
func (errorReadCloser) Close() error             { return nil }
