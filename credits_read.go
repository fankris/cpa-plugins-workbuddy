package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const creditReadBudget = 12 * time.Second
const creditsBatchLimit = 4

// Shared across callers/tabs, not just a per-request limit. Never acquired by
// mutations or lifecycle maintenance. A canceled waiter cannot start upstream I/O.
var creditReadSlots = make(chan struct{}, creditsBatchLimit)

func refreshCreditsRow(f pluginapi.HostAuthFileEntry) map[string]any {
	return refreshCreditsRowContext(pluginContext(), f)
}
func refreshCreditsRowContext(parent context.Context, f pluginapi.HostAuthFileEntry) map[string]any {
	ctx, cancel := context.WithTimeout(parent, creditReadBudget)
	defer cancel()
	out := map[string]any{"auth_index": f.AuthIndex}
	fail := func(err error) map[string]any {
		code := "read_failed"
		if ctx.Err() == context.DeadlineExceeded {
			code = "read_timeout"
		} else if ctx.Err() == context.Canceled {
			code = "read_canceled"
		}
		out["error"], out["data_error"], out["read_state"], out["code"] = safeManagementError(err), safeManagementError(err), "failed", code
		return out
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	select {
	case creditReadSlots <- struct{}{}:
		defer func() { <-creditReadSlots }()
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	sa, err := hostAuthGet(f.AuthIndex)
	if err != nil {
		return fail(fmt.Errorf("credential read failed"))
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	out["service"], out["region"] = accountServiceRegion(sa), panelRegion(sa)
	seq := nextAccountSnapshot()
	// Package lookup is bounded independently and cannot consume an extra 30s
	// after credits finish. Failure keeps the previous plan; never blanks it.
	planDone := make(chan string, 1)
	go func() {
		pc, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		planDone <- fetchPaymentTypeWithClient(pc, nil, sa)
	}()
	cr, err := fetchUserResourceWithClient(ctx, nil, sa)
	if err != nil {
		result := fail(err)
		cancel()
		<-planDone
		// Cancellation isn't evidence that an earlier successful balance is invalid.
		if parent.Err() != context.Canceled {
			publishAccountSnapshot(f.ID, accountSnapshotPatch{seq: seq, creditsSet: true, errors: []string{"credits: " + safeManagementError(err)}})
		}
		return result
	}
	var plan string
	select {
	case plan = <-planDone:
	case <-ctx.Done():
		cancel()
		<-planDone
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	entry := publishAccountSnapshot(f.ID, accountSnapshotPatch{seq: seq, creditsSet: true, credits: cr, planSet: true, plan: plan})
	// Return this actual observation, not another concurrently updated identity.
	out["credits"], out["read_state"], out["data_error"] = cr, "fresh", ""
	out["capabilities"], out["trial_eligibility"] = accountCapabilities(sa, cr), accountCapabilities(sa, cr)["trial"].Eligibility
	if entry.plan != "" {
		out["plan"] = entry.plan
	}
	if isWorkBuddyService(sa) {
		out["trial_claimed"] = hasTrialPack(cr)
	}
	out["exhausted"] = isCreditsExhausted(cr)
	return out
}

func handleCreditsQuery(req pluginapi.ManagementRequest) map[string]any {
	return handleCreditsQueryContext(pluginContext(), req)
}
func handleCreditsQueryContext(parent context.Context, req pluginapi.ManagementRequest) map[string]any {
	ctx, cancel := context.WithTimeout(parent, creditReadBudget)
	defer cancel()
	id := strings.TrimSpace(queryParam(req, "auth_index"))
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": "cannot list CPA credentials"}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].AuthIndex < files[j].AuthIndex })
	total := len(files)
	identities := make([]string, 0, total)
	for _, f := range files {
		identities = append(identities, f.AuthIndex+"/"+f.ID)
	}
	sum := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	snapshot := fmt.Sprintf("%x", sum[:12])
	selected := []pluginapi.HostAuthFileEntry{}
	offset, limit := 0, creditsBatchLimit
	if id != "" {
		for _, f := range files {
			if f.AuthIndex == id {
				selected = append(selected, f)
				break
			}
		}
		if len(selected) == 0 {
			return map[string]any{"error": "account not found"}
		}
	} else {
		for key, dest := range map[string]*int{"offset": &offset, "limit": &limit} {
			if raw := queryParam(req, key); raw != "" {
				value, e := strconv.Atoi(raw)
				if e != nil {
					return map[string]any{"error": "invalid pagination", "code": "invalid_request"}
				}
				*dest = value
			}
		}
		if offset < 0 || offset > total || limit < 1 || limit > creditsBatchLimit {
			return map[string]any{"error": "invalid pagination; limit must be 1..4", "code": "invalid_request"}
		}
		if offset > 0 && queryParam(req, "snapshot") != snapshot {
			return map[string]any{"error": "account list changed or snapshot missing; restart pagination", "code": "snapshot_changed"}
		}
		end := offset + limit
		if end > total {
			end = total
		}
		selected = files[offset:end]
	}
	out := make([]map[string]any, len(selected))
	var wg sync.WaitGroup
	for i, f := range selected {
		wg.Add(1)
		go func(i int, f pluginapi.HostAuthFileEntry) { defer wg.Done(); out[i] = refreshCreditsRowContext(ctx, f) }(i, f)
	}
	wg.Wait()
	result := map[string]any{"accounts": out, "server_time_iso": time.Now().UTC().Format(time.RFC3339), "total": total, "returned": len(out), "snapshot": snapshot, "read_budget_ms": creditReadBudget.Milliseconds(), "has_more": false, "scope": "account"}
	if id == "" {
		result["scope"] = "page"
		result["offset"] = offset
		if offset+len(selected) < total {
			result["has_more"] = true
			result["next_offset"] = offset + len(selected)
		}
	}
	return result
}
