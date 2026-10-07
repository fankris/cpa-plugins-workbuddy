package main

import (
	"context"
	"fmt"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProgressiveAccountScale(t *testing.T) {
	for _, n := range []int{1, 10, 50, 200} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			store := newFakeAuthStore()
			for i := 0; i < n; i++ {
				store.put(fmt.Sprintf("idx-%03d", i), fmt.Sprintf("workbuddy-%03d.json", i), fmt.Sprint(i), regionCN, false)
			}
			installFakeAuthStore(t, store)
			resetAccountCache()
			srv, calls := billingStub(t, 88)
			restore := setBillingBase(srv.URL)
			defer restore()
			start := time.Now()
			_, out := managementCall(t, http.MethodGet, "/accounts", "")
			elapsed := time.Since(start)
			if len(out["accounts"].([]any)) != n || atomic.LoadInt32(calls) != 0 {
				t.Fatal("identity-first violated")
			}
			t.Logf("accounts=%d identity elapsed=%s upstream calls=0", n, elapsed)
			seen := map[string]bool{}
			offset := 0
			snapshot := ""
			for {
				r := handleCreditsQuery(pluginapi.ManagementRequest{Query: map[string][]string{"offset": {strconv.Itoa(offset)}, "snapshot": {snapshot}}})
				rows, ok := r["accounts"].([]map[string]any)
				if !ok || len(rows) > 4 {
					t.Fatalf("bad bounded page: %+v", r)
				}
				for _, row := range rows {
					id := row["auth_index"].(string)
					if seen[id] || row["error"] != nil {
						t.Fatalf("duplicate/failed %v", row)
					}
					seen[id] = true
				}
				snapshot = r["snapshot"].(string)
				if !r["has_more"].(bool) {
					break
				}
				offset = r["next_offset"].(int)
			}
			if len(seen) != n {
				t.Fatal("incomplete enumeration")
			}
			if len(store.savedRecords()) != 0 {
				t.Fatal("read wrote credentials")
			}
			r := handleCreditsQuery(pluginapi.ManagementRequest{Query: map[string][]string{"limit": {"200"}}})
			if r["code"] != "invalid_request" {
				t.Fatal(r)
			}
			if n > 1 {
				r = handleCreditsQuery(pluginapi.ManagementRequest{Query: map[string][]string{"offset": {"1"}, "snapshot": {"obsolete"}}})
				if r["code"] != "snapshot_changed" {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestCreditsReadCancellationAndSharedSlots(t *testing.T) {
	store := newFakeAuthStore()
	for i := 0; i < 20; i++ {
		store.put(fmt.Sprintf("i%02d", i), fmt.Sprintf("workbuddy-%02d.json", i), fmt.Sprint(i), regionCN, false)
	}
	installFakeAuthStore(t, store)
	resetAccountCache()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer srv.Close()
	restore := setBillingBase(srv.URL)
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := handleCreditsQueryContext(ctx, pluginapi.ManagementRequest{})
	for _, row := range r["accounts"].([]map[string]any) {
		if row["code"] != "read_canceled" {
			t.Fatal(row)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("canceled read started upstream")
	}
	// Fill all shared slots: a waiting read must honor its own deadline without I/O.
	for i := 0; i < creditsBatchLimit; i++ {
		creditReadSlots <- struct{}{}
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	handleCreditsQueryContext(waitCtx, pluginapi.ManagementRequest{})
	waitCancel()
	for i := 0; i < creditsBatchLimit; i++ {
		<-creditReadSlots
	}
	if calls.Load() != 0 {
		t.Fatal("a blocked waiter started upstream")
	}
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
			defer cancel()
			r := handleCreditsQueryContext(ctx, pluginapi.ManagementRequest{Query: map[string][]string{"auth_index": {fmt.Sprintf("i%02d", i)}}})
			row := r["accounts"].([]map[string]any)[0]
			if row["code"] != "read_timeout" {
				t.Errorf("not timeout: %v", row)
			}
		}(i)
	}
	wg.Wait()
	if time.Since(start) > 2*time.Second {
		t.Fatal("read budget not honored")
	}
	if calls.Load() > 40 {
		t.Fatalf("unexpected extra upstream attempts: %d upstream requests", calls.Load())
	}
	if len(creditReadSlots) != 0 {
		t.Fatal("slot leak")
	}
	if len(store.savedRecords()) != 0 {
		t.Fatal("read wrote credentials")
	}
}

func TestCreditsDefaultReadBudget(t *testing.T) {
	store := newFakeAuthStore()
	store.put("slow", "workbuddy-slow.json", "slow", regionCN, false)
	installFakeAuthStore(t, store)
	resetAccountCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(13 * time.Second):
		}
	}))
	defer srv.Close()
	restore := setBillingBase(srv.URL)
	defer restore()
	start := time.Now()
	result := handleCreditsQuery(pluginapi.ManagementRequest{Query: map[string][]string{"auth_index": {"slow"}}})
	elapsed := time.Since(start)
	if elapsed < 11*time.Second || elapsed > 14*time.Second {
		t.Fatalf("default budget elapsed %s", elapsed)
	}
	row := result["accounts"].([]map[string]any)[0]
	if row["code"] != "read_timeout" || row["credits"] != nil {
		t.Fatalf("timeout must not invent credits: %v", row)
	}
	if len(store.savedRecords()) != 0 {
		t.Fatal("timeout wrote credentials")
	}
	t.Logf("default slow upstream returned after %s", elapsed)
}
