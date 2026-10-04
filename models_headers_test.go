package main

import (
	"net/http"
	"testing"
)

func TestApplyModelsDiscoveryHeadersIntl(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://www.codebuddy.ai/console/enterprises/personal/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	applyModelsDiscoveryHeaders(req, "intl")

	checks := map[string]string{
		"X-Domain":          "www.codebuddy.ai",
		"X-Product":         "cloud",
		"X-IDE-Type":        "IDE",
		"X-IDE-Name":        "CodeBuddy",
		"X-IDE-Version":     "1.100.0",
		"X-Product-Version": "1.100.0",
	}
	for name, want := range checks {
		if got := req.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := req.Header.Get("X-Requested-With"); got != "XMLHttpRequest" {
		t.Fatalf("discovery helper must not alter generic X-Requested-With policy, got %q", got)
	}
}

func TestApplyModelsDiscoveryHeadersNonIntlUnchanged(t *testing.T) {
	for _, realm := range []string{"cn", "global", ""} {
		req, err := http.NewRequest(http.MethodGet, "https://example.com/models", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Product", "SaaS")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		applyModelsDiscoveryHeaders(req, realm)
		for _, name := range []string{"X-Domain", "X-IDE-Type", "X-IDE-Name", "X-IDE-Version", "X-Product-Version"} {
			if got := req.Header.Get(name); got != "" {
				t.Errorf("realm=%q: %s = %q, want empty", realm, name, got)
			}
		}
		if got := req.Header.Get("X-Product"); got != "SaaS" {
			t.Errorf("realm=%q: X-Product = %q, want SaaS", realm, got)
		}
		if got := req.Header.Get("X-Requested-With"); got != "XMLHttpRequest" {
			t.Errorf("realm=%q: X-Requested-With = %q, want unchanged", realm, got)
		}
	}
}
