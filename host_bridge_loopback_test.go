package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTestTransportRejectsNonLoopback(t *testing.T) {
	restore := installHostHTTPTestDirect()
	defer restore()
	for _, url := range []string{"https://www.codebuddy.ai/probe", "http://169.254.169.254/", "https://example.com/"} {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if _, err := hostHTTPDo(req); err == nil {
			t.Fatalf("test transport accepted non-loopback URL")
		}
	}
}
func TestTestTransportRejectsExternalRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/", http.StatusFound)
	}))
	defer srv.Close()
	restore := installHostHTTPTestDirect()
	defer restore()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	if _, err := hostHTTPDo(req); err == nil {
		t.Fatal("test transport followed external redirect")
	}
}
