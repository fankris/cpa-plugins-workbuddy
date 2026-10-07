package main

import (
	"context"
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Deliberately read-only. No submit-region, login, trial or credential writes.
func fetchActivationStatus(ctx context.Context, sa *storedAuth) map[string]any {
	out := map[string]any{"status": "unknown", "registration": "unknown", "trial_eligibility": "unknown", "checked_at": time.Now().UTC().Format(time.RFC3339), "official_url": "https://www.workbuddy.ai/"}
	if err := requireBusiness(sa, "activation_status"); err != nil {
		return businessErrorResult("", err)
	}
	if strings.TrimSpace(sa.Account.UID) == "" || strings.TrimSpace(sa.Auth.AccessToken) == "" {
		out["reason"] = "missing_credential"
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, billingBaseFor(sa)+"/auth/realms/copilot/overseas/user/register?userId="+url.QueryEscape(sa.Account.UID), nil)
	if err != nil {
		out["reason"] = "request_failed"
		return out
	}
	billingHeaders(req, sa)
	resp, err := hostHTTPDo(req)
	if err != nil {
		out["reason"] = "request_failed"
		return out
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out["reason"] = "upstream_error"
		out["http_status"] = resp.StatusCode
		return out
	}
	var envelope struct {
		Code json.RawMessage `json:"code"`
	}
	if json.Unmarshal(resp.Body, &envelope) != nil || len(envelope.Code) == 0 {
		out["reason"] = "invalid_response"
		return out
	}
	code := strings.Trim(string(envelope.Code), "\" ")
	switch code {
	case "0", "200":
		out["status"] = "ok"
		out["registration"] = "registered"
	case "500":
		out["status"] = "action_required"
		out["registration"] = "required"
	default:
		out["reason"] = "upstream_error"
	}
	return out
}
func handleActivationStatus(req pluginapi.ManagementRequest, ctx context.Context) map[string]any {
	id := strings.TrimSpace(queryParam(req, "auth_index"))
	if id == "" {
		return map[string]any{"error": "auth_index is required", "code": "invalid_request"}
	}
	files, err := hostAuthList()
	if err != nil {
		return businessErrorResult(id, err)
	}
	for _, f := range files {
		if f.AuthIndex == id {
			sa, err := hostAuthGet(id)
			if err != nil {
				return businessErrorResult(id, err)
			}
			out := fetchActivationStatus(ctx, sa)
			out["auth_index"] = id
			return out
		}
	}
	return map[string]any{"error": "account not found", "code": "not_found"}
}
