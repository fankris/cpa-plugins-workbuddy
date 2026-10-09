package main

import (
	"strings"
	"testing"
)

// A model that streams its reasoning under a field name outside the original
// hardcoded list must still count as producing output. Treating it as "no
// output" made the plugin raise an empty-answer error mid-stream, and the
// client reported "stream closed before response.completed".
func TestChunkHasModelOutputAcceptsReasoningFieldNames(t *testing.T) {
	cases := []struct {
		name  string
		chunk string
		want  bool
	}{
		{"content", `{"choices":[{"delta":{"content":"hi"}}]}`, true},
		{"reasoning_content", `{"choices":[{"delta":{"reasoning_content":"hmm"}}]}`, true},
		{"reasoning", `{"choices":[{"delta":{"reasoning":"hmm"}}]}`, true},
		{"thinking", `{"choices":[{"delta":{"thinking":"hmm"}}]}`, true},
		{"reasoning_summary", `{"choices":[{"delta":{"reasoning_summary":"hmm"}}]}`, true},
		{"unknown future field", `{"choices":[{"delta":{"some_new_reasoning_field":"hmm"}}]}`, true},
		{"tool call", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a"}]}}]}`, true},
		{"framed thinking", `data: {"choices":[{"delta":{"thinking":"hmm"}}]}`, true},
		{"role only", `{"choices":[{"delta":{"role":"assistant"}}]}`, false},
		{"empty content", `{"choices":[{"delta":{"content":""}}]}`, false},
		{"empty delta", `{"choices":[{"delta":{}}]}`, false},
		{"framing only", `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, false},
	}
	for _, tc := range cases {
		if got := chunkHasModelOutput(tc.chunk); got != tc.want {
			t.Errorf("%s: chunkHasModelOutput(%s) = %v, want %v", tc.name, tc.chunk, got, tc.want)
		}
	}
}

// The folding path must accumulate reasoning regardless of which name the
// upstream used, otherwise a reasoning-only answer folds into an empty reply.
func TestAggregateCompletionFoldsAllReasoningNames(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
		`data: {"choices":[{"delta":{"thinking":"why "}}]}`,
		`data: {"choices":[{"delta":{"reasoning_summary":"so"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out, err := aggregateCompletion(strings.NewReader(stream), "gpt-6.1-sol")
	if err != nil {
		t.Fatalf("aggregateCompletion: %v", err)
	}
	if !strings.Contains(string(out), "why ") || !strings.Contains(string(out), "so") {
		t.Fatalf("folded completion lost reasoning: %s", out)
	}
}
