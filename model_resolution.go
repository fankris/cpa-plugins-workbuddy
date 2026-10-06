package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// This is only coordination for the existing model cache, not an account pool.
// Waiters retain their own callback context; one canceled caller cannot cancel
// another caller's upstream request or borrow its host callback ID.
type discoveryGate struct {
	token chan struct{}
	refs  int
}

var discoveryGates = struct {
	sync.Mutex
	entries map[string]*discoveryGate
}{entries: map[string]*discoveryGate{}}

func lockDiscovery(ctx context.Context, key string) (func(), error) {
	discoveryGates.Lock()
	g := discoveryGates.entries[key]
	if g == nil {
		g = &discoveryGate{token: make(chan struct{}, 1)}
		discoveryGates.entries[key] = g
	}
	g.refs++
	discoveryGates.Unlock()
	releaseRef := func() {
		discoveryGates.Lock()
		g.refs--
		if g.refs == 0 {
			delete(discoveryGates.entries, key)
		}
		discoveryGates.Unlock()
	}
	select {
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	case g.token <- struct{}{}:
		return func() { <-g.token; releaseRef() }, nil
	}
}
func modelContext(contexts ...context.Context) context.Context {
	if len(contexts) > 0 && contexts[0] != nil {
		return contexts[0]
	}
	return pluginContext()
}

type modelResolution struct {
	Details map[string]modelDetails
	Models  []pluginapi.ModelInfo
	Source  *realmModelsState
	Status  string // ok, skipped (pin/static), fallback (discovery failed)
	Warning string
}

func modelResult(models []pluginapi.ModelInfo, source, status string) modelResolution {
	return modelResolution{Models: models, Source: &realmModelsState{Source: source, Count: len(models)}, Status: status}
}
func resolveCredentialModels(parent context.Context, storage []byte, force bool) modelResolution {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	token, _ := extractAccessToken(storage)
	service := serviceRealmForStorage(storage, token)
	realm := displayRegionForService(service)
	key := modelCacheKey(service, storage, token)
	fallback := func(err error) modelResolution {
		msg := truncateRedacted(err.Error(), 300)
		if ctx.Err() == nil {
			noteRealmError(service, msg)
		}
		out := modelResult(staticModelsForRealm(realm), "static (discovery failed)", "fallback")
		out.Warning = msg
		out.Source.LastError = msg
		out.Source.LastErrorA = time.Now().UTC().Format(time.RFC3339)
		return out
	}
	if err := ctx.Err(); err != nil {
		return fallback(err)
	}
	if pinned := pinnedModelsForRealm(realm); len(pinned) > 0 {
		noteRealmSource(service, "pin (unverified)", len(pinned))
		return modelResult(pinned, "pin (unverified)", "skipped")
	}
	if (realm == regionIntl && service != regionGlobal) || token == "" {
		base := builtinStaticModelsForRealm(realm)
		models := appendCustomModels(realm, base)
		source := "static (no token in storage)"
		if realm == regionIntl && service != regionGlobal {
			source = "static (intl dynamic discovery unavailable)"
		}
		source = modelSourceWithCustom(realm, source, base)
		noteRealmSource(service, source, len(models))
		return modelResult(models, source, "skipped")
	}
	release, err := lockDiscovery(ctx, key)
	if err != nil {
		return fallback(err)
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return fallback(err)
	}
	if force {
		dynamicModelsCache.Lock()
		delete(dynamicModelsCache.realms, key)
		dynamicModelsCache.Unlock()
	}
	if models, ok := cachedDynamicModels(key); ok {
		models = appendCustomModels(realm, models)
		out := modelResult(models, "discovery (cached)", "ok")
		out.Source = realmModelStateFor(key)
		out.Source.Count = len(models)
		dynamicModelsCache.Lock()
		out.Details = dynamicModelsCache.realms[key].details
		dynamicModelsCache.Unlock()
		return out
	}
	collector := &modelDetailsCollector{}
	ctx = context.WithValue(ctx, modelDetailsKey{}, collector)
	var models []pluginapi.ModelInfo
	if discoverModelsFn != nil {
		models, err = discoverModelsFn(token, service)
	} else {
		models, err = callModelsAPIContext(ctx, token, service)
	}
	if ctx.Err() != nil {
		return fallback(ctx.Err())
	}
	if err != nil {
		return fallback(err)
	}
	if len(models) == 0 {
		return fallback(fmt.Errorf("discovery payload had no user-facing models"))
	}
	storeDynamicModels(key, models, collector.Rows)
	source := realmModelStateFor(key)
	models = appendCustomModels(realm, models)
	source.Count = len(models)
	return modelResolution{Models: models, Details: collector.Rows, Source: source, Status: "ok"}
}
