package main

import (
	"path"
	"strings"
	"sync"
)

var resourcePathMu sync.RWMutex
var resourceBasePathCache = "/v0/resource/plugins/" + providerName

// Only same-origin absolute path prefixes may be injected into browser assets.
// In particular, //host, traversal, query strings and encoded separators are rejected.
func cleanHostPath(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), "/")
	if p == "" || !strings.HasPrefix(p, "/") || strings.Contains(p, "//") || strings.ContainsAny(p, "?#\\%\r\n") {
		return ""
	}
	if path.Clean(p) != p {
		return ""
	}
	return p
}
func loadedResourceBasePath() string {
	resourcePathMu.RLock()
	defer resourcePathMu.RUnlock()
	return resourceBasePathCache
}
func setResourceBasePath(p string) {
	p = cleanHostPath(p)
	if p == "" {
		return
	}
	resourcePathMu.Lock()
	resourceBasePathCache = p
	resourcePathMu.Unlock()
}
