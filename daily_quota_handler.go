// daily_quota_handler.go exposes the local daily free-allowance counters over
// the Management API.
//
// Read-only plus an explicit reset. The counters are measured by this plugin
// from host usage records (see daily_quota.go) because upstream publishes no
// per-model quota endpoint; the panel is the only consumer.
package main

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// handleDailyQuotaQuery serves GET /daily-quota[?auth_index=...].
//
// With auth_index: one account's per-model rows. Without: every account, keyed
// by auth_index, so the panel can render all cards from one request.
func handleDailyQuotaQuery(req pluginapi.ManagementRequest) map[string]any {
	authIndex := queryParam(req, "auth_index")
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	day := dailyQuotaNow().Local().Format("2006-01-02")
	if authIndex != "" {
		for _, f := range files {
			if f.AuthIndex != authIndex {
				continue
			}
			return map[string]any{
				"auth_index": f.AuthIndex,
				"auth_id":    f.ID,
				"name":       f.Name,
				"label":      f.Label,
				"day":        day,
				"models":     dailyQuotaSnapshotForAuth(f.ID),
			}
		}
		return map[string]any{"error": "auth_index not found: " + authIndex}
	}

	out := make(map[string]any, len(files))
	rows := make([]map[string]any, 0, len(files))
	for _, f := range files {
		row := map[string]any{
			"auth_index": f.AuthIndex,
			"auth_id":    f.ID,
			"name":       f.Name,
			"label":      f.Label,
			"day":        day,
			"models":     dailyQuotaSnapshotForAuth(f.ID),
		}
		rows = append(rows, row)
		out[f.AuthIndex] = row
	}
	return map[string]any{"day": day, "accounts": rows}
}

// handleDailyQuotaReset serves POST /daily-quota/reset[?auth_index=...].
//
// This clears LOCAL counters only. It exists so an operator can recover from a
// counter that drifted (e.g. a day's traffic was attributed to the wrong
// credential), and it deliberately does not touch upstream state: resetting the
// local number does not restore any real allowance, and the response says so.
func handleDailyQuotaReset(req pluginapi.ManagementRequest) map[string]any {
	authIndex := queryParam(req, "auth_index")
	if authIndex == "" {
		resetDailyQuota("")
		return map[string]any{
			"status":  "ok",
			"scope":   "all",
			"message": "已清空本插件记录的今日用量。这不会恢复上游的真实免费额度，仅重置本地显示。",
		}
	}
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	for _, f := range files {
		if f.AuthIndex != authIndex {
			continue
		}
		resetDailyQuota(f.ID)
		return map[string]any{
			"status":     "ok",
			"scope":      authIndex,
			"auth_index": authIndex,
			"message":    "已清空该账号本插件记录的今日用量。这不会恢复上游的真实免费额度，仅重置本地显示。",
		}
	}
	return map[string]any{"error": "auth_index not found: " + authIndex}
}

// queryParam reads the first value of a query key.
func queryParam(req pluginapi.ManagementRequest, key string) string {
	if vals := req.Query[key]; len(vals) > 0 {
		return strings.TrimSpace(vals[0])
	}
	return ""
}
