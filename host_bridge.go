// host_bridge.go routes every upstream HTTP call through the CPA host's
// http bridge (host.http.do / host.http.do_stream / stream_read / stream_close).
// Production traffic always uses the bridge so request-log captures outbound
// calls and host transport policy (proxy, timeout) applies. The *Direct
// variants are the test-only fallback used when the bridge is unavailable
// (unit tests, or hosts older than v7.2.x without the http bridge RPC).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// sharedHTTPClient is the fallback HTTP client used ONLY when the host HTTP
// bridge is unavailable (unit tests, or hosts older than v7.2.x without
// host.http.* RPC). All production upstream calls should route via hostHTTPDo
// / hostHTTPDoStream so request-log captures them and host transport policy
// applies. Direct use of this client in new code is a compliance bug.
func sharedHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		// No cookie jar here: auth is carried by Bearer headers, and a shared
		// jar would leak upstream set-cookie state across accounts (multi-account
		// deployments could cross-contaminate sessions). Only the short-lived
		// login clients get a jar.
		sharedClient = &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				IdleConnTimeout:     90 * time.Second,
				MaxIdleConnsPerHost: 5,
			},
		}
	})
	return sharedClient
}

// hostHTTPResponse is the plugin-side view of an HTTP response that came back
// through the host bridge. Body is fully buffered (matches the historical
// io.ReadAll(resp.Body) usage pattern in billing / models / usage callers).
type hostHTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// hostHTTPTestOverride is nil in production. Tests install a fake transport
// explicitly; production never falls back when host.http fails.
var hostHTTPTestOverride func(*http.Request) (*hostHTTPResponse, error)

// installHostHTTPTestDirect is used only by legacy unit-test helpers that
// previously relied on an implicit bridge fallback. Production code never
// calls it and leaves hostHTTPTestOverride nil.
func installHostHTTPTestDirect() func() {
	old := hostHTTPTestOverride
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		// Test-only transport must never send fixture credentials to real services.
		if !testLoopbackRequest(req) {
			return nil, fmt.Errorf("test transport forbids non-loopback requests")
		}
		client := *sharedHTTPClient()
		client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if len(via) >= 10 || !testLoopbackRequest(next) {
				return fmt.Errorf("test transport forbids redirect")
			}
			return nil
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return &hostHTTPResponse{StatusCode: resp.StatusCode, Headers: resp.Header.Clone(), Body: body}, nil
	}
	return func() { hostHTTPTestOverride = old }
}

// Used exclusively by installHostHTTPTestDirect; production uses host.http.
func testLoopbackRequest(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	ip := net.ParseIP(req.URL.Hostname())
	return ip != nil && ip.IsLoopback()
}

type hostCallbackIDContextKey struct{}

func withHostCallbackID(ctx context.Context, id string) context.Context {
	if strings.TrimSpace(id) == "" {
		return ctx
	}
	return context.WithValue(ctx, hostCallbackIDContextKey{}, strings.TrimSpace(id))
}

func hostCallbackIDFromRequest(req *http.Request) string {
	if req == nil || req.Context() == nil {
		return ""
	}
	id, _ := req.Context().Value(hostCallbackIDContextKey{}).(string)
	return id
}

// rpcHostHTTPRequestWire mirrors internal/pluginhost/host_callbacks.go's
// rpcHostHTTPRequest on the wire. The "request" sub-object is the actual HTTP
// call; the flat method/url/headers/body fields are an alternate form we don't
// use (host prefers Request when present).
type rpcHostHTTPRequestWire struct {
	HostCallbackID string            `json:"host_callback_id,omitempty"`
	Request        *rpcHostHTTPInner `json:"request,omitempty"`
}

type rpcHostHTTPInner struct {
	Method  string              `json:"method,omitempty"`
	URL     string              `json:"url,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

type rpcHostHTTPStreamResponseWire struct {
	StatusCode int                         `json:"status_code"`
	Headers    map[string][]string         `json:"headers,omitempty"`
	StreamID   string                      `json:"stream_id,omitempty"`
	Chunks     []pluginapi.HTTPStreamChunk `json:"chunks,omitempty"`
}

type rpcHostHTTPStreamReadResponseWire struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

// hostBridgeUnwrap unwraps the pluginabi.Envelope returned by host RPC and
// returns the inner Result payload. Returns an error when the envelope itself
// signals failure (ok=false) or is malformed.
func hostBridgeUnwrap(raw []byte, method string) (json.RawMessage, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%s: decode envelope: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: host error %s: %s", method, env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("%s: host returned not-ok", method)
	}
	return env.Result, nil
}

// hostBridgeAvailable reports whether host.http.* RPC is wired up. False in
// unit tests (no hostAPI) and when the host binary predates the bridge.
func hostBridgeAvailable() bool {
	hostAPIMu.RLock()
	defer hostAPIMu.RUnlock()
	return hostAPI != nil && hostAPI.call != nil
}

// hostHTTPDo performs a non-streaming upstream call via the CPA host bridge.
// Production calls fail closed when the bridge is unavailable or returns an
// unusable result; they are never retried through a second transport because
// the first RPC may already have executed a side effect.
func hostHTTPDo(req *http.Request) (*hostHTTPResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	if hostHTTPTestOverride != nil {
		return hostHTTPTestOverride(req)
	}
	var bodyBytes []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		_ = req.Body.Close()
		bodyBytes = b
	}
	if !hostBridgeAvailable() {
		return nil, fmt.Errorf("CPA host.http bridge unavailable")
	}
	wire := rpcHostHTTPRequestWire{
		HostCallbackID: hostCallbackIDFromRequest(req),
		Request: &rpcHostHTTPInner{
			Method:  req.Method,
			URL:     req.URL.String(),
			Headers: map[string][]string(req.Header),
			Body:    bodyBytes,
		},
	}
	raw, err := hostCall(pluginabi.MethodHostHTTPDo, mustJSON(wire))
	if err != nil {
		return nil, fmt.Errorf("host.http.do: %w", err)
	}
	result, err := hostBridgeUnwrap(raw, pluginabi.MethodHostHTTPDo)
	if err != nil {
		return nil, err
	}
	// Wire compat (v0.12.49): hosts marshal pluginapi.HTTPResponse WITHOUT
	// json tags, so the status arrives as PascalCase "StatusCode". Our old
	// decode only looked for "status_code" — Go's case-insensitive field
	// match rescued Headers/Body but NOT StatusCode (underscore ≠ no
	// underscore), so every bridged response decoded with status 0 and the
	// models discovery treated every upstream 200 as "models API status 0".
	// Decode both shapes; keep "status_code" for hosts that document it.
	status, headers, respBody, errDecode := decodeHostHTTPDoResult(result)
	if errDecode != nil {
		return nil, errDecode
	}
	if status <= 0 || status > 999 {
		return nil, fmt.Errorf("host.http.do returned invalid status %d", status)
	}
	return &hostHTTPResponse{
		StatusCode: status,
		Headers:    headers,
		Body:       respBody,
	}, nil
}

// decodeHostHTTPDoResult decodes the inner Result of a host.http.do envelope.
// Accepts both wire shapes seen in the wild: the documented lowercase
// {"status_code":...} and the PascalCase {"StatusCode":...} that hosts emit
// when they marshal the tag-less pluginapi.HTTPResponse struct directly.
// Headers/Body match either spelling via Go's case-insensitive field match.
func decodeHostHTTPDoResult(result json.RawMessage) (int, http.Header, []byte, error) {
	var resp struct {
		StatusCode    int                 `json:"status_code"`
		StatusCodeAlt int                 `json:"StatusCode"`
		Headers       map[string][]string `json:"headers,omitempty"`
		Body          []byte              `json:"body,omitempty"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return 0, nil, nil, fmt.Errorf("decode host.http.do response: %w", err)
	}
	status := resp.StatusCode
	if status == 0 {
		status = resp.StatusCodeAlt
	}
	return status, http.Header(resp.Headers), resp.Body, nil
}

// hostHTTPDoDirect executes the request via the plugin's own http.Client.
// Used as a fallback when the host bridge is unavailable (unit tests).
func hostHTTPDoDirect(req *http.Request, bodyBytes []byte) (*hostHTTPResponse, error) {
	// Rebuild the request since req.Body was already consumed.
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header.Clone()
	resp, err := sharedHTTPClient().Do(newReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &hostHTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       raw,
	}, nil
}

// hostHTTPStream is a handle for an in-flight host-bridged stream. Read returns
// the next chunk; Close aborts the upstream.
//
// Three modes:
//   - Bridged: streamID set, Read/Close forward to host RPC.
//   - Reader (test seam): reader set by hostStreamTestOverride.
//   - Direct (test fallback): direct holds the full buffered body, Read drains
//     it once then reports done. Close is a no-op.
type hostHTTPStream struct {
	streamID string
	direct   []byte
	directAt int
	reader   io.ReadCloser
}

// hostStreamTestOverride, when non-nil, answers hostHTTPDoStream instead of the
// real bridge. Production leaves it nil (the bridge is the only transport).
// Tests install it to drive the streaming data plane — notably the
// empty-answer detection in pumpUpstreamStream, which needs a real reader to
// observe a framing-only upstream response.
var hostStreamTestOverride func(*http.Request) (io.ReadCloser, int, error)

// hostHTTPDoStream opens a streaming call via the CPA host bridge. The host
// owns the actual response body; plugin code only pulls chunks via the bridge.
// Production failures are returned without a second transport attempt.
func hostHTTPDoStream(req *http.Request) (*hostHTTPStream, int, http.Header, error) {
	if req == nil {
		return nil, 0, nil, fmt.Errorf("nil request")
	}
	if hostStreamTestOverride != nil {
		body, status, err := hostStreamTestOverride(req)
		if err != nil {
			return nil, status, nil, err
		}
		if body == nil {
			body = io.NopCloser(strings.NewReader(""))
		}
		return &hostHTTPStream{reader: body}, status, http.Header{}, nil
	}
	var bodyBytes []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("read request body: %w", err)
		}
		_ = req.Body.Close()
		bodyBytes = b
	}
	if !hostBridgeAvailable() {
		return nil, 0, nil, fmt.Errorf("CPA host.http bridge unavailable")
	}
	wire := rpcHostHTTPRequestWire{
		HostCallbackID: hostCallbackIDFromRequest(req),
		Request: &rpcHostHTTPInner{
			Method:  req.Method,
			URL:     req.URL.String(),
			Headers: map[string][]string(req.Header),
			Body:    bodyBytes,
		},
	}
	raw, err := hostCall(pluginabi.MethodHostHTTPDoStream, mustJSON(wire))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("host.http.do_stream: %w", err)
	}
	result, err := hostBridgeUnwrap(raw, pluginabi.MethodHostHTTPDoStream)
	if err != nil {
		return nil, 0, nil, err
	}
	var resp rpcHostHTTPStreamResponseWire
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, 0, nil, fmt.Errorf("decode host.http.do_stream response: %w", err)
	}
	if resp.StatusCode <= 0 || resp.StatusCode > 999 {
		return nil, resp.StatusCode, http.Header(resp.Headers), fmt.Errorf("host.http.do_stream returned invalid status %d", resp.StatusCode)
	}
	if resp.StreamID == "" {
		return nil, resp.StatusCode, http.Header(resp.Headers), fmt.Errorf("host stream bridge unavailable")
	}
	return &hostHTTPStream{streamID: resp.StreamID}, resp.StatusCode, http.Header(resp.Headers), nil
}

// hostHTTPDoStreamDirect is the test-only fallback: it performs the request
// with the plugin's own http.Client and buffers the full body into an
// in-memory hostHTTPStream so Read/Close keep the same contract.
func hostHTTPDoStreamDirect(req *http.Request, bodyBytes []byte) (*hostHTTPStream, int, http.Header, error) {
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, nil, err
	}
	newReq.Header = req.Header.Clone()
	resp, err := sharedHTTPClient().Do(newReq)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, resp.Header.Clone(), err
	}
	return &hostHTTPStream{direct: raw}, resp.StatusCode, resp.Header.Clone(), nil
}

// Read pulls the next chunk. Returns (payload, done, err). done=true means the
// stream ended cleanly; err non-nil means upstream or bridge error.
func (s *hostHTTPStream) Read() ([]byte, bool, error) {
	if s == nil {
		return nil, true, fmt.Errorf("stream closed")
	}
	// Reader (test seam) mode: drain the installed reader.
	if s.reader != nil {
		buf := make([]byte, 32*1024)
		n, err := s.reader.Read(buf)
		if n > 0 {
			return buf[:n], false, nil
		}
		if err == io.EOF {
			return nil, true, nil
		}
		if err != nil {
			return nil, true, err
		}
		return nil, false, nil
	}
	// Direct (test fallback) mode: serve the buffered body in one shot.
	if s.direct != nil {
		if s.directAt >= len(s.direct) {
			return nil, true, nil
		}
		out := s.direct[s.directAt:]
		s.directAt = len(s.direct)
		return out, false, nil
	}
	if s.streamID == "" {
		return nil, true, fmt.Errorf("stream closed")
	}
	raw, err := hostCall(pluginabi.MethodHostHTTPStreamRead, mustJSON(map[string]any{"stream_id": s.streamID}))
	if err != nil {
		return nil, true, err
	}
	result, err := hostBridgeUnwrap(raw, pluginabi.MethodHostHTTPStreamRead)
	if err != nil {
		return nil, true, err
	}
	var resp rpcHostHTTPStreamReadResponseWire
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, true, fmt.Errorf("decode host.http.stream_read response: %w", err)
	}
	if resp.Error != "" {
		return nil, true, fmt.Errorf("%s", resp.Error)
	}
	return resp.Payload, resp.Done, nil
}

// Close aborts the upstream stream. Always safe to call (idempotent on host).
func (s *hostHTTPStream) Close() {
	if s == nil {
		return
	}
	if s.reader != nil {
		_ = s.reader.Close()
		s.reader = nil
		return
	}
	if s.direct != nil {
		s.direct = nil
		s.directAt = 0
		return
	}
	if s.streamID == "" {
		return
	}
	_, _ = hostCall(pluginabi.MethodHostHTTPStreamClose, mustJSON(map[string]any{"stream_id": s.streamID}))
	s.streamID = ""
}

// hostStreamReader adapts a hostHTTPStream to io.Reader so existing
// bufio.Scanner / io.ReadAll call sites work unchanged. The host bridge emits
// arbitrary 32KB chunks (not SSE lines), so line framing must be re-assembled
// by the consumer — Scanner handles that for us.
type hostStreamReader struct {
	s    *hostHTTPStream
	buf  []byte // leftover from previous chunk
	done bool
	err  error
}

func newHostStreamReader(s *hostHTTPStream) *hostStreamReader {
	return &hostStreamReader{s: s}
}

func (r *hostStreamReader) Read(p []byte) (int, error) {
	// Drain buffered bytes first.
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}
	if r.done {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	chunk, done, err := r.s.Read()
	if err != nil {
		r.done = true
		r.err = err
		return 0, err
	}
	if len(chunk) > 0 {
		n := copy(p, chunk)
		if n < len(chunk) {
			r.buf = append(r.buf, chunk[n:]...)
		}
		if done {
			r.done = true
		}
		return n, nil
	}
	if done {
		r.done = true
		return 0, io.EOF
	}
	// Empty chunk, not done — recurse to fetch next.
	return r.Read(p)
}

// mustJSON marshals v and panics on error — the wire structs above are always
// marshalable, so any failure here is a programming bug.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
