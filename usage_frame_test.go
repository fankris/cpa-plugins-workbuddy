package main

import (
	"strings"
	"testing"
)

func TestUsageFrameWins(t *testing.T) {
	real := map[string]any{"prompt_tokens": 100.0, "completion_tokens": 50.0, "total_tokens": 150.0}
	zero := map[string]any{"prompt_tokens": 0.0, "completion_tokens": 0.0, "total_tokens": 0.0}
	bigger := map[string]any{"prompt_tokens": 120.0, "completion_tokens": 60.0, "total_tokens": 180.0}

	if !usageFrameWins(real, nil) {
		t.Error("first frame always wins")
	}
	if usageFrameWins(zero, real) {
		t.Error("a zero placeholder must never overwrite real usage")
	}
	if !usageFrameWins(real, real) {
		t.Error("an equal frame may replace the stored one (monotonic >=)")
	}
	if !usageFrameWins(bigger, real) {
		t.Error("a larger (final) frame must win")
	}
	if !usageFrameWins(real, zero) {
		t.Error("a real frame arriving after a placeholder must win")
	}
}

// aggregateCompletion must carry the REAL usage into the folded completion:
// the host's translator reads usage.prompt_tokens from this payload for token
// accounting, so a trailing zero placeholder erasing real usage would corrupt
// downstream counters, not just the client display.
func TestAggregateCompletion_ZeroUsagePlaceholderDoesNotEraseRealUsage(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"1","choices":[{"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"1","choices":[{"delta":{"content":"hello"}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`,
		``,
		`data: {"id":"1","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")

	out, err := aggregateCompletion(strings.NewReader(stream), "gpt-5.6-luna")
	if err != nil {
		t.Fatalf("aggregateCompletion: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, `"total_tokens":120`) {
		t.Errorf("real usage must survive the trailing placeholder, got: %s", body)
	}
	if strings.Contains(body, `"total_tokens":0`) {
		t.Errorf("zero placeholder leaked into the folded completion: %s", body)
	}
}
