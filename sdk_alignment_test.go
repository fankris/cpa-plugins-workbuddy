package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func sdkRPC(t *testing.T, f func(string, []byte) ([]byte, error)) {
	t.Helper()
	resetPluginLifecycleForTest()
	old := hostRPCTestOverride
	hostRPCTestOverride = f
	t.Cleanup(func() { quiescePlugin(); hostRPCTestOverride = old; resetPluginLifecycleForTest() })
}
func waitSDK(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("SDK callback did not drain")
	}
}

func TestSDKHTTPParentCancelInterruptsHeaders(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			started, cancelled := make(chan struct{}), make(chan struct{})
			var count atomic.Int32
			sdkRPC(t, func(method string, raw []byte) ([]byte, error) {
				var wire struct {
					OperationID    string `json:"operation_id"`
					HostCallbackID string `json:"host_callback_id"`
				}
				_ = json.Unmarshal(raw, &wire)
				switch method {
				case pluginabi.MethodHostHTTPOperationOpen:
					if wire.HostCallbackID != "caller" {
						t.Error("lost callback scope")
					}
					return okEnvelope(map[string]string{"operation_id": "op"})
				case pluginabi.MethodHostHTTPDo, pluginabi.MethodHostHTTPDoStream:
					if wire.OperationID != "op" || wire.HostCallbackID != "caller" {
						t.Error("lost operation ownership")
					}
					close(started)
					<-cancelled
					return nil, context.Canceled
				case pluginabi.MethodHostHTTPCancel:
					if count.Add(1) == 1 {
						close(cancelled)
					}
					return okEnvelope(map[string]any{})
				default:
					return nil, fmt.Errorf("unexpected method %s", method)
				}
			})
			ctx, cancel := context.WithCancel(withHostCallbackID(context.Background(), "caller"))
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid", nil)
			done := make(chan struct{})
			go func() {
				defer close(done)
				if stream {
					_, _, _, err := hostHTTPDoStream(req)
					if err == nil {
						t.Error("canceled stream opened")
					}
				} else {
					_, err := hostHTTPDo(req)
					if err == nil {
						t.Error("canceled request succeeded")
					}
				}
			}()
			waitSDK(t, started)
			cancel()
			waitSDK(t, done)
			if count.Load() != 1 {
				t.Fatalf("cancel calls = %d", count.Load())
			}
		})
	}
}
func TestSDKHTTPQuiesceCancelsBackgroundOperation(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var once sync.Once
	sdkRPC(t, func(method string, raw []byte) ([]byte, error) {
		switch method {
		case pluginabi.MethodHostHTTPOperationOpen:
			return okEnvelope(map[string]string{"operation_id": "background"})
		case pluginabi.MethodHostHTTPDo:
			close(started)
			<-cancelled
			return nil, context.Canceled
		case pluginabi.MethodHostHTTPCancel:
			once.Do(func() { close(cancelled) })
			return okEnvelope(map[string]any{})
		default:
			return nil, fmt.Errorf("unexpected %s", method)
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, _ := http.NewRequest("GET", "https://example.invalid", nil)
		_, _ = hostHTTPDo(req)
	}()
	waitSDK(t, started)
	drained := make(chan struct{})
	go func() { quiescePlugin(); close(drained) }()
	waitSDK(t, drained)
	waitSDK(t, done)
}
func TestSDKHTTPOpenFailureDoesNotIssueRequest(t *testing.T) {
	var calls atomic.Int32
	sdkRPC(t, func(method string, raw []byte) ([]byte, error) {
		calls.Add(1)
		if method != pluginabi.MethodHostHTTPOperationOpen {
			t.Error("unexpected fallback or request")
		}
		return errorEnvelope("unavailable", "closed"), nil
	})
	req, _ := http.NewRequest("POST", "https://example.invalid", nil)
	if _, err := hostHTTPDo(req); err == nil {
		t.Fatal("missing operation accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("request replayed")
	}
}
func TestSDKHTTPAlreadyCancelledNeverCallsHost(t *testing.T) {
	sdkRPC(t, func(string, []byte) ([]byte, error) { t.Error("host called for canceled request"); return nil, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.invalid", nil)
	if _, err := hostHTTPDo(req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, _, err := hostHTTPDoStream(req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestSDKHTTPStreamCloseRacesReadAndClosesOnce(t *testing.T) {
	reading, closed := make(chan struct{}), make(chan struct{})
	var closes, cancels atomic.Int32
	sdkRPC(t, func(method string, raw []byte) ([]byte, error) {
		switch method {
		case pluginabi.MethodHostHTTPOperationOpen:
			return okEnvelope(map[string]string{"operation_id": "op"})
		case pluginabi.MethodHostHTTPDoStream:
			return okEnvelope(map[string]any{"status_code": 200, "stream_id": "s"})
		case pluginabi.MethodHostHTTPStreamRead:
			close(reading)
			<-closed
			return okEnvelope(map[string]any{"done": true})
		case pluginabi.MethodHostHTTPStreamClose:
			if closes.Add(1) == 1 {
				close(closed)
			}
			return okEnvelope(map[string]any{})
		case pluginabi.MethodHostHTTPCancel:
			cancels.Add(1)
			return okEnvelope(map[string]any{})
		default:
			return nil, fmt.Errorf("unexpected %s", method)
		}
	})
	req, _ := http.NewRequest("GET", "https://example.invalid", nil)
	stream, _, _, err := hostHTTPDoStream(req)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _, _ = stream.Read() }()
	waitSDK(t, reading)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); stream.Close() }()
	}
	wg.Wait()
	waitSDK(t, done)
	if closes.Load() != 1 || cancels.Load() != 1 {
		t.Fatalf("close=%d cancel=%d", closes.Load(), cancels.Load())
	}
}
func TestSDKLateCloserAndConcurrentCloseExactlyOnce(t *testing.T) {
	for i := 0; i < 500; i++ {
		var count atomic.Int32
		s := &pluginAsyncStream{}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); s.setCloser(func() { count.Add(1) }) }()
		go func() { defer wg.Done(); s.closeUpstream() }()
		wg.Wait()
		s.closeUpstream()
		if count.Load() != 1 {
			t.Fatalf("closer ran %d times", count.Load())
		}
	}
}

type sdkTerminalReader struct {
	sent bool
	err  error
}

func (r *sdkTerminalReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	return copy(p, "last payload"), r.err
}
func (r *sdkTerminalReader) Close() error { return nil }
func TestSDKStreamPreservesFinalPayloadAndError(t *testing.T) {
	for _, terminal := range []error{io.EOF, io.ErrUnexpectedEOF} {
		t.Run(terminal.Error(), func(t *testing.T) {
			s := &hostHTTPStream{reader: &sdkTerminalReader{err: terminal}}
			defer s.Close()
			r := newHostStreamReader(s)
			if n, err := r.Read(nil); n != 0 || err != nil {
				t.Fatal("zero-size read consumed data")
			}
			got, err := io.ReadAll(r)
			if string(got) != "last payload" {
				t.Fatalf("payload lost: %q", got)
			}
			if terminal == io.EOF {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, terminal) {
				t.Fatalf("terminal error lost: %v", err)
			}
		})
	}
}
func TestSDKStreamKeepsEmptyChunksIterative(t *testing.T) {
	var calls int
	sdkRPC(t, func(method string, raw []byte) ([]byte, error) {
		if method == pluginabi.MethodHostHTTPStreamClose {
			return okEnvelope(map[string]any{})
		}
		calls++
		if calls < 10000 {
			return okEnvelope(map[string]any{"done": false})
		}
		return okEnvelope(map[string]any{"payload": []byte("end"), "done": true})
	})
	s := &hostHTTPStream{streamID: "empty-chunks"}
	defer s.Close()
	got, err := io.ReadAll(newHostStreamReader(s))
	if err != nil || string(got) != "end" {
		t.Fatalf("%q %v", got, err)
	}
}
func TestSDKExecutorPreservesCallbackScope(t *testing.T) {
	resetPluginLifecycleForTest()
	defer resetPluginLifecycleForTest()
	old := hostStreamTestOverride
	defer func() { hostStreamTestOverride = old }()
	hostStreamTestOverride = func(req *http.Request) (io.ReadCloser, int, error) {
		if hostCallbackIDFromRequest(req) != "executor-scope" {
			t.Error("executor lost host_callback_id")
		}
		return io.NopCloser(strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")), 200, nil
	}
	raw := mustJSON(executorStreamRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "test", StorageJSON: mustJSON(&storedAuth{Auth: storedTokens{AccessToken: "fixture"}}), Payload: []byte(`{"messages":[{"role":"user","content":"test"}]}`)}, HostCallbackID: "executor-scope"})
	for _, handler := range []func([]byte) ([]byte, error){handleExecExecute, handleExecStream} {
		result, err := handler(raw)
		if err != nil {
			t.Fatal(err)
		}
		var env envelope
		_ = json.Unmarshal(result, &env)
		if !env.OK {
			t.Fatalf("executor failed: %s", result)
		}
	}
}
func TestSDKRefreshScopeAndHostOwnedPersistence(t *testing.T) {
	resetPluginLifecycleForTest()
	defer resetPluginLifecycleForTest()
	old := hostHTTPTestOverride
	defer func() { hostHTTPTestOverride = old }()
	hostHTTPTestOverride = func(req *http.Request) (*hostHTTPResponse, error) {
		if hostCallbackIDFromRequest(req) != "refresh-scope" {
			t.Error("refresh lost scope")
		}
		return &hostHTTPResponse{StatusCode: 200, Body: []byte(`{"code":0,"data":{"accessToken":"new-fixture"}}`)}, nil
	}
	raw := mustJSON(struct {
		pluginapi.AuthRefreshRequest
		HostCallbackID string `json:"host_callback_id"`
	}{pluginapi.AuthRefreshRequest{StorageJSON: mustJSON(&storedAuth{Auth: storedTokens{AccessToken: "old-fixture", ExpiresAt: 2000000000}})}, "refresh-scope"})
	result, err := handleRefreshAuth(raw)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Result pluginapi.AuthRefreshResponse `json:"result"`
	}
	_ = json.Unmarshal(result, &env)
	if env.Result.Auth.FileName != "" || env.Result.Auth.ID != "" {
		t.Fatal("refresh tried to rename host-owned auth")
	}
}
