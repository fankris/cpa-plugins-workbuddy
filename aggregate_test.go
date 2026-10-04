package main

import (
	"strings"
	"testing"
)

func TestAggregateCompletion_BasicSSE(t *testing.T) {
	sse := "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"1\",\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":10,\"total_tokens\":15}}\n\ndata: [DONE]\n\n"
	out, err := aggregateCompletion(strings.NewReader(sse), "test-model")
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "Hello world") {
		t.Fatalf("content not merged: %s", s)
	}
	if !strings.Contains(s, "stop") {
		t.Fatalf("finish_reason missing: %s", s)
	}
}

func TestAggregateCompletion_Empty(t *testing.T) {
	_, err := aggregateCompletion(strings.NewReader(""), "test")
	if err == nil {
		t.Fatal("empty stream should be rejected")
	}
}

func TestAggregateCompletion_NoDone(t *testing.T) {
	sse := "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"
	out, _ := aggregateCompletion(strings.NewReader(sse), "m")
	if !strings.Contains(string(out), "hi") {
		t.Fatal("content missing")
	}
}

func TestAggregateCompletion_MultilineDataAndDoneStopsTrailing(t *testing.T) {
	sse := "data: {\"id\":\"1\",\n" +
		"data: \"choices\":[{\"index\":0,\"delta\":{\"content\":\"multi\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n" +
		"data: {not-forwarded}\n\n"
	out, err := aggregateCompletion(strings.NewReader(sse), "m")
	if err != nil || !strings.Contains(string(out), "multi") || strings.Contains(string(out), "not-forwarded") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}
