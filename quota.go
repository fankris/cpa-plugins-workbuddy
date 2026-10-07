package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// quotaFetchRPCRequest mirrors the host-side quota RPC wrapper. The callback ID
// is carried by the host for the bridge lifecycle; WorkBuddy's existing HTTP
// bridge uses the process host API and does not need to expose that field here.
type quotaFetchRPCRequest struct {
	pluginapi.QuotaFetchRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type quotaResetRPCRequest struct {
	pluginapi.QuotaResetRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleQuotaDescribe(raw []byte) ([]byte, error) {
	var req pluginapi.QuotaDescribeRequest
	if len(raw) > 0 {
		if err := jsonUnmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	return okEnvelope(pluginapi.QuotaDescribeResponse{
		SupportedProviders: []string{
			providerName,
			// Bare "codebuddy" keeps legacy accounts from the pre-merge
			// codebuddy plugin quota-capable until adoption rewrites them.
			"codebuddy",
			"workbuddy-cn",
			"workbuddy-global",
			"workbuddy-intl",
			"codebuddy-cn",
			"codebuddy-intl",
		},
		DisplayName:   "WorkBuddy",
		SupportsReset: false,
	})
}

func handleQuotaFetch(raw []byte) ([]byte, error) {
	var rpc quotaFetchRPCRequest
	if err := jsonUnmarshal(raw, &rpc); err != nil {
		return nil, err
	}
	ctx := withHostCallbackID(pluginContext(), rpc.HostCallbackID)
	resp, err := fetchNativeQuota(ctx, rpc.QuotaFetchRequest)
	if err != nil {
		return nil, err
	}
	return okEnvelope(resp)
}

func handleQuotaReset(raw []byte) ([]byte, error) {
	var rpc quotaResetRPCRequest
	if len(raw) > 0 {
		if err := jsonUnmarshal(raw, &rpc); err != nil {
			return nil, err
		}
	}
	return okEnvelope(pluginapi.QuotaResetResponse{
		Success: false,
		Message: "WorkBuddy 不支持原生额度重置；请使用上游套餐/签到功能。",
	})
}

// jsonUnmarshal is kept as a small seam for quota RPC handlers and tests.
func jsonUnmarshal(raw []byte, dst any) error {
	return json.Unmarshal(raw, dst)
}

func fetchNativeQuota(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(req.Provider) != "" && !isWorkBuddyQuotaProvider(req.Provider) {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("unsupported quota provider %q", req.Provider)
	}

	sa, err := quotaStoredAuth(req)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	credits, err := fetchUserResourceWithClient(ctx, req.HTTPClient, sa)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	plan := fetchPaymentTypeWithClient(ctx, req.HTTPClient, sa)
	storeQuotaSnapshot(req.AuthIndex, req.AuthID, plan, credits)
	resp := quotaResponse(plan, credits)
	resp.ServerTimeOffsetMs = upstreamClockOffsetMs.Load()
	return resp, nil
}

func isWorkBuddyQuotaProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", providerName, "codebuddy", "workbuddy-cn", "workbuddy-global", "workbuddy-intl", "codebuddy-cn", "codebuddy-intl":
		return true
	default:
		return false
	}
}

func quotaStoredAuth(req pluginapi.QuotaFetchRequest) (*storedAuth, error) {
	if len(req.StorageJSON) > 0 {
		return parseStored(req.StorageJSON)
	}
	for _, key := range []string{strings.TrimSpace(req.AuthIndex), strings.TrimSpace(req.AuthID)} {
		if key == "" {
			continue
		}
		if sa, err := hostAuthGet(key); err == nil {
			return sa, nil
		}
	}
	return nil, fmt.Errorf("quota credential storage is unavailable")
}

func storeQuotaSnapshot(authIndex, authID, plan string, credits *creditsSummary) {
	keys := []string{strings.TrimSpace(authIndex), strings.TrimSpace(authID)}
	seen := make(map[string]struct{}, len(keys))
	var previous *accountCacheEntry
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if v, ok := accountCache.Load(key); ok {
			if entry, ok := v.(*accountCacheEntry); ok && entry != nil && previous == nil {
				previous = entry
			}
		}
	}
	if len(seen) == 0 || credits == nil {
		return
	}
	entry := &accountCacheEntry{
		credits: credits,
		plan:    strings.TrimSpace(plan),
		fetched: nowUTC(),
	}
	if previous != nil {
		entry.checkin = previous.checkin
	}
	// The panel indexes accountCache by auth.ID, while native quota requests
	// arrive with the stable auth_index. Keep both aliases pointed at the same
	// snapshot so a native refresh is immediately visible in either path.
	for key := range seen {
		accountCache.Store(key, entry)
	}
}

func quotaResponse(plan string, credits *creditsSummary) pluginapi.QuotaFetchResponse {
	resp := pluginapi.QuotaFetchResponse{
		Subscription: &pluginapi.QuotaSubscription{
			Plan:     strings.TrimSpace(plan),
			TierName: strings.TrimSpace(plan),
		},
	}
	if credits != nil {
		// Summary metrics are what management UIs render first (bounded
		// key/label/value triples; filterUsableQuotaSummary-shape).
		resp.Summary = []pluginapi.QuotaMetric{
			{Key: "credits_remaining", Label: "剩余积分", Value: float64(credits.TotalRemain), Format: "number"},
			{Key: "credits_used", Label: "已用积分", Value: float64(credits.TotalUsed), Format: "number"},
			{Key: "credits_total", Label: "积分总额", Value: float64(credits.TotalSize), Format: "number"},
		}
	}
	resp.Groups = append(resp.Groups, pluginapi.QuotaGroup{
		DisplayName: "WorkBuddy credits",
		Buckets: []pluginapi.QuotaBucket{{
			Window:            "aggregate",
			RemainingFraction: quotaFraction(credits.TotalRemain, credits.TotalSize),
			Description:       fmt.Sprintf("remaining=%d used=%d total=%d", credits.TotalRemain, credits.TotalUsed, credits.TotalSize),
		}},
	})
	for _, p := range credits.Packages {
		bucket := pluginapi.QuotaBucket{
			Window:      "package",
			ResetTime:   strings.TrimSpace(p.CycleEnd),
			Description: strings.TrimSpace(p.Name),
		}
		bucket.RemainingFraction = quotaFraction(p.Remain, p.Size)
		resp.Groups = append(resp.Groups, pluginapi.QuotaGroup{
			DisplayName: strings.TrimSpace(p.Name),
			Buckets:     []pluginapi.QuotaBucket{bucket},
		})
	}
	return resp
}

func quotaFraction(remain, size int64) float64 {
	if size <= 0 {
		return 0
	}
	fraction := float64(remain) / float64(size)
	if fraction < 0 {
		return 0
	}
	if fraction > 1 {
		return 1
	}
	return fraction
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
