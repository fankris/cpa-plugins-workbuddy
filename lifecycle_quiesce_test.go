package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The upstream rejects several body shapes (Tencent-specific quirks). These
// rewrites run on every request, so their exact semantics matter: an
// over-eager rewrite silently changes what the model sees.

// reasoning_effort: real tiers are mirrored to reasoning_summary and an absent
// field is left alone (forcing it triggers the upstream content filter).
// Unusable values ("none"/"off"/unknown/blank) are never forwarded.
func TestMirrorReasoningEffort(t *testing.T) {
	none := map[string]any{"reasoning_effort": "none"}
	if !mirrorReasoningEffort(none) {
		t.Error(`"none" should report a change`)
	}
	if _, still := none["reasoning_effort"]; still {
		t.Error(`"none" must not be forwarded (upstream has no "none")`)
	}

	off := map[string]any{"reasoning_effort": "OFF"}
	if !mirrorReasoningEffort(off) {
		t.Error(`"off" should report a change`)
	}
	if _, still := off["reasoning_effort"]; still {
		t.Error(`"off" must not be forwarded`)
	}

	high := map[string]any{"reasoning_effort": "high"}
	if !mirrorReasoningEffort(high) {
		t.Error("a real effort should report a change")
	}
	if high["reasoning_effort"] != "high" {
		t.Error("a real effort must be preserved")
	}
	if high["reasoning_summary"] != "auto" {
		t.Errorf("a real effort must mirror reasoning_summary=auto, got %v", high["reasoning_summary"])
	}

	absent := map[string]any{}
	if mirrorReasoningEffort(absent) {
		t.Error("an absent effort must be a no-op (forcing it trips the content filter)")
	}
	if _, added := absent["reasoning_summary"]; added {
		t.Error("an absent effort must not add reasoning_summary")
	}

	empty := map[string]any{"reasoning_effort": "  "}
	if !mirrorReasoningEffort(empty) {
		t.Error("a blank effort is unusable and must be dropped")
	}
	if _, still := empty["reasoning_effort"]; still {
		t.Error("a blank effort must not be forwarded")
	}
}

// Only system messages are rewritten; user/assistant content must be untouched.
func TestRewriteSystemInPlaceOnlyTouchesSystemMessages(t *testing.T) {
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
			map[string]any{"role": "assistant", "content": "hi"},
		},
	}
	if rewriteSystemInPlace(obj) {
		t.Error("a body without system messages should report no change")
	}
	msgs := obj["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "hello" {
		t.Error("user content must not be rewritten")
	}

	// A blocked template inside a system message IS rewritten (that is the
	// point of the pass): the Anthropic CLI wording trips Tencent's filter.
	blocked := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": []any{
				map[string]any{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
			}},
		},
	}
	if !rewriteSystemInPlace(blocked) {
		t.Error("a blocked system template must be rewritten")
	}
	sys := blocked["messages"].([]any)[0].(map[string]any)
	parts := sys["content"].([]any)
	text := parts[0].(map[string]any)["text"].(string)
	// An agent-identity line trips the pattern, so the whole prompt is replaced
	// by the neutral one (not a word-level substitution).
	if strings.Contains(text, "Claude Code") {
		t.Errorf("blocked agent identity survived the rewrite: %q", text)
	}
	if text != neutralPrompt {
		t.Errorf("an agent-identity prompt should be replaced with the neutral prompt, got %q", text)
	}
	// Content structure is preserved (parts stay parts).
	if _, isSlice := sys["content"].([]any); !isSlice {
		t.Error("rewriting must preserve the content-parts structure")
	}
}

// A body with nothing to rewrite must report no change so the caller can skip
// a needless re-serialisation.
func TestRewriteSystemInPlaceReportsNoChangeForCleanBody(t *testing.T) {
	obj := map[string]any{
		"model": "glm-5.1",
		"messages": []any{
			map[string]any{"role": "system", "content": "plain string"},
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	if rewriteSystemInPlace(obj) {
		t.Error("a clean body should report no change")
	}
}

// Plugin lifecycle: quiescing must stop new work, cancel in-flight streams and
// wait for workers, and the test reset must restore a usable state.
func TestPluginQuiesceStopsAndResets(t *testing.T) {
	resetPluginLifecycleForTest()
	t.Cleanup(resetPluginLifecycleForTest)

	if pluginQuiescing() {
		t.Fatal("a fresh plugin must not be quiescing")
	}
	// A registered stream must be cancelled by quiesce. registerAsyncStream
	// accounts for one worker slot, released by stream.finish() — exactly like
	// the real pump goroutine, which returns once its context is cancelled.
	// quiesce waits for that drain, so the test must model it: a stream that
	// never finishes would (correctly) block quiesce forever.
	var cancelled bool
	var mu sync.Mutex
	var stream *pluginAsyncStream
	stream, ok := registerAsyncStream("s-1", func() {
		mu.Lock()
		cancelled = true
		mu.Unlock()
		go func() {
			// The pump notices cancellation and exits, releasing its slot.
			time.Sleep(10 * time.Millisecond)
			stream.finish()
		}()
	})
	if !ok {
		t.Fatal("stream registration should succeed while running")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		quiescePlugin()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("quiesce must not hang waiting for its own worker")
	}

	if !pluginQuiescing() {
		t.Fatal("quiesce must set the quiescing flag")
	}
	mu.Lock()
	wasCancelled := cancelled
	mu.Unlock()
	if !wasCancelled {
		t.Fatal("quiesce must cancel in-flight streams")
	}
	if pluginWorkerStart() {
		t.Fatal("quiesce must refuse new workers")
	}

	// After a reset the plugin is usable again (reconfigure path).
	resetPluginLifecycleForTest()
	if pluginQuiescing() {
		t.Fatal("reset must clear the quiescing flag")
	}
	if !pluginWorkerStart() {
		t.Fatal("reset must allow workers again")
	}
	pluginWorkerDone()
}

// Quiescing must refuse host callbacks (the host API may be going away).
func TestHostCallbacksRefusedWhileQuiescing(t *testing.T) {
	resetPluginLifecycleForTest()
	t.Cleanup(resetPluginLifecycleForTest)

	if !hostCallbackStart(false) {
		t.Fatal("callbacks should be allowed while running")
	}
	hostCallbackDone()

	pluginLifecycleMu.Lock()
	pluginQuiescingState = true
	pluginLifecycleMu.Unlock()

	if hostCallbackStart(false) {
		t.Fatal("a normal callback must be refused while quiescing")
	}
	// Cleanup callbacks (stream close) are still allowed so the host can drain.
	if !hostCallbackStart(true) {
		t.Fatal("cleanup callbacks must remain allowed while quiescing")
	}
	hostCallbackDone()
}

// Worker bookkeeping must be safe under concurrent start/finish.
func TestPluginWorkerAccounting(t *testing.T) {
	resetPluginLifecycleForTest()
	t.Cleanup(resetPluginLifecycleForTest)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pluginWorkerStart() {
				defer pluginWorkerDone()
			}
		}()
	}
	wg.Wait()
	// All workers finished: quiesce must return promptly (nothing outstanding).
	done := make(chan struct{})
	go func() {
		defer close(done)
		quiescePlugin()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("quiesce hung: worker accounting leaked")
	}
}

// setManagementBasePath must normalise the host-provided base path and ignore
// an empty value (the cache keeps the previous value).
func TestSetManagementBasePath(t *testing.T) {
	old := loadedManagementBasePath()
	t.Cleanup(func() { setManagementBasePath(old) })

	setManagementBasePath("/v0/management/")
	if got := loadedManagementBasePath(); got != "/v0/management" {
		t.Fatalf("trailing slash not trimmed: %q", got)
	}
	setManagementBasePath("")
	if got := loadedManagementBasePath(); got != "/v0/management" {
		t.Fatalf("an empty path must be ignored, got %q", got)
	}
}
