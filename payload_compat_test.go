package main

import (
	"encoding/json"
	"testing"
)

func TestPrepareUpstreamBodyNormalizesImageAndToolSchemaCompatibility(t *testing.T) {
	body := `{"model":"client-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":"https://example.test/a.png"},{"type":"image_url","image_url":{"url":"https://example.test/b.png","detail":"high"}},{"type":"text","text":"keep"}]}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"key":{"type":"string","pattern":"^[A-Z\\_]+$"}}}}}],"functions":[{"name":"legacy","parameters":{"type":"object","properties":{"key":{"type":"string","pattern":"^[a-z\\_]+$"}}}}],"metadata":{"pattern":"^[A-Z\\_]+$"},"tool_choice":"auto"}`
	out := prepareUpstreamBody([]byte(body), nil, &storedAuth{}, "test-model")
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal prepared body: %v", err)
	}
	if got["model"] != "test-model" {
		t.Fatalf("model = %v, want test-model", got["model"])
	}
	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %v, want one user message", got["messages"])
	}
	message := messages[0].(map[string]any)
	content := message["content"].([]any)
	stringImage := content[0].(map[string]any)["image_url"].(map[string]any)
	if stringImage["url"] != "https://example.test/a.png" {
		t.Errorf("normalized image URL = %v", stringImage["url"])
	}
	canonicalImage := content[1].(map[string]any)["image_url"].(map[string]any)
	if canonicalImage["url"] != "https://example.test/b.png" || canonicalImage["detail"] != "high" {
		t.Errorf("existing image object changed: %v", canonicalImage)
	}
	if content[2].(map[string]any)["text"] != "keep" {
		t.Errorf("text part changed: %v", content[2])
	}
	patternAt := func(schema any) string {
		t.Helper()
		root, ok := schema.(map[string]any)
		if !ok {
			t.Fatalf("schema is %T, want object", schema)
		}
		properties := root["properties"].(map[string]any)
		field := properties["key"].(map[string]any)
		pattern, _ := field["pattern"].(string)
		return pattern
	}
	tool := got["tools"].([]any)[0].(map[string]any)
	toolFunction := tool["function"].(map[string]any)
	if pattern := patternAt(toolFunction["parameters"]); pattern != "^[A-Z_]+$" {
		t.Errorf("tool pattern = %q, want escaped underscore normalized", pattern)
	}
	legacyFunction := got["functions"].([]any)[0].(map[string]any)
	if pattern := patternAt(legacyFunction["parameters"]); pattern != "^[a-z_]+$" {
		t.Errorf("legacy function pattern = %q, want escaped underscore normalized", pattern)
	}
	metadata := got["metadata"].(map[string]any)
	if pattern := metadata["pattern"]; pattern != "^[A-Z\\_]+$" {
		t.Errorf("non-tool pattern changed: %v", pattern)
	}
}
