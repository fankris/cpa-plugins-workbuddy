package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The host HTTP bridge is the single transport for every upstream call. Its
// envelope handling and the direct (test) fallback must agree, otherwise a
// plugin that works in tests fails in production or vice versa.

// hostBridgeUnwrap must decode a well-formed envelope and reject a bad one
// with a message naming the method (so logs identify the failing call).
func TestHostBridgeUnwrap(t *testing.T) {
	good, _ := okEnvelope(map[string]any{"status_code": 200})
	result, err := hostBridgeUnwrap(good, "host.http.do")
	if err != nil {
		t.Fatalf("a valid envelope must unwrap: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("unwrapped result is empty")
	}

	// A non-OK envelope must surface the plugin-side error message.
	bad := errorEnvelope("boom", "upstream exploded")
	if _, err := hostBridgeUnwrap(bad, "host.http.do"); err == nil {
		t.Fatal("an error envelope must not unwrap successfully")
	} else if !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("the unwrap error should carry the host message, got %v", err)
	}

	// Garbage must not panic.
	if _, err := hostBridgeUnwrap([]byte("not json"), "host.http.do"); err == nil {
		t.Fatal("malformed JSON must be an error")
	}
}

// The direct stream fallback must buffer the body and satisfy the same
// Read/Close contract the bridged path exposes.
func TestHostHTTPDoStreamDirectBuffersBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "yes")
		_, _ = w.Write([]byte("hello stream"))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	stream, status, headers, err := hostHTTPDoStreamDirect(req, []byte("body"))
	if err != nil {
		t.Fatalf("direct stream: %v", err)
	}
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if headers.Get("X-Test") != "yes" {
		t.Errorf("response headers lost: %v", headers)
	}
	defer stream.Close()

	payload, done, err := stream.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(payload) != "hello stream" {
		t.Fatalf("payload = %q", payload)
	}
	if done {
		t.Fatal("the chunk-carrying read must report done=false")
	}
	// The following read reports the clean end (that is how the pump stops).
	if _, done2, err2 := stream.Read(); err2 != nil || !done2 {
		t.Fatalf("exhausted stream should end cleanly, got done=%v err=%v", done2, err2)
	}
}

// The reader adapter must pass through chunks unchanged.
func TestHostStreamReaderPassesThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload-chunk"))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	stream, _, _, err := hostHTTPDoStreamDirect(req, []byte("body"))
	if err != nil {
		t.Fatalf("direct stream: %v", err)
	}
	defer stream.Close()
	got, err := io.ReadAll(newHostStreamReader(stream))
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if string(got) != "payload-chunk" {
		t.Fatalf("reader returned %q", got)
	}
}

func mustReq(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://example.invalid/x", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// hostHTTPDo must surface the host's status/body verbatim, and must not fall
// back to a second transport when the bridge reports a failure.
func TestHostHTTPDoUsesTestOverrideVerbatim(t *testing.T) {
	old := hostHTTPTestOverride
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		return &hostHTTPResponse{StatusCode: 418, Body: []byte("teapot")}, nil
	}
	defer func() { hostHTTPTestOverride = old }()

	resp, err := hostHTTPDo(mustReq(t, ""))
	if err != nil {
		t.Fatalf("hostHTTPDo: %v", err)
	}
	if resp.StatusCode != 418 || string(resp.Body) != "teapot" {
		t.Fatalf("response not passed through: %+v", resp)
	}
}

// A nil request must be rejected rather than dereferenced.
func TestHostHTTPDoRejectsNilRequest(t *testing.T) {
	if _, err := hostHTTPDo(nil); err == nil {
		t.Fatal("a nil request must be an error")
	}
}

// The stream entry point must reject a nil request too.
func TestHostHTTPDoStreamRejectsNilRequest(t *testing.T) {
	if _, _, _, err := hostHTTPDoStream(nil); err == nil {
		t.Fatal("a nil request must be an error")
	}
}

// withHostCallbackID must attach the callback id to the request context so the
// host can correlate stream callbacks.
func TestWithHostCallbackIDRoundTrip(t *testing.T) {
	req := mustReq(t, "")
	if got := hostCallbackIDFromRequest(req); got != "" {
		t.Fatalf("a fresh request has no callback id, got %q", got)
	}
	ctx := withHostCallbackID(req.Context(), "cb-123")
	req = req.WithContext(ctx)
	if got := hostCallbackIDFromRequest(req); got != "cb-123" {
		t.Fatalf("callback id not preserved: %q", got)
	}
}

// A bridged stream response with a missing stream id must be an explicit error
// (not a silent empty stream).
// In a unit-test process the cgo host table is never populated, so the bridged
// stream path must fail closed with an explicit message rather than silently
// returning an empty stream. (The direct fallback is exercised separately.)
func TestHostHTTPDoStreamFailsClosedWithoutHost(t *testing.T) {
	_, _, _, err := hostHTTPDoStream(mustReq(t, ""))
	if err == nil {
		t.Fatal("without a host bridge the stream call must fail, not return an empty stream")
	}
	if !strings.Contains(err.Error(), "bridge unavailable") {
		t.Fatalf("error should name the missing bridge, got %v", err)
	}
}

// The non-streaming entry point must also fail closed without a host bridge:
// the first RPC may already have executed a side effect, so a silent retry on
// another transport would be wrong.
func TestHostHTTPDoFailsClosedWithoutHost(t *testing.T) {
	if _, err := hostHTTPDo(mustReq(t, "")); err == nil {
		t.Fatal("without a host bridge the call must fail rather than fall back")
	}
}
