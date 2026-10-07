package main

import (
	"errors"
	"fmt"
)

// Region defines business support; login brand and gateway never define it.
// Support does not imply permission, entitlement, configuration or execution.
type businessCapability struct {
	Supported   bool   `json:"supported"`
	Eligibility string `json:"eligibility"`
	Reason      string `json:"reason,omitempty"`
}

func supportsBusiness(sa *storedAuth, feature string) bool {
	if sa == nil {
		return false
	}
	switch feature {
	case "checkin", "tasks", "travel":
		return accountRegion(sa) == regionCN
	case "trial", "activation_status":
		return accountRegion(sa) == regionIntl
	case "credits", "models", "token_refresh":
		return true
	default:
		return false
	}
}
func accountCapabilities(sa *storedAuth, cr *creditsSummary) map[string]businessCapability {
	out := map[string]businessCapability{}
	for _, key := range []string{"checkin", "tasks", "travel", "trial", "activation_status", "credits", "models", "token_refresh"} {
		c := businessCapability{Supported: supportsBusiness(sa, key), Eligibility: "unknown"}
		if !c.Supported {
			c.Eligibility = "not_applicable"
			c.Reason = "unsupported_region"
		}
		if key == "trial" && c.Supported && hasTrialPack(cr) {
			c.Eligibility = "already_claimed"
			c.Reason = "trial_pack_observed"
		}
		if key == "token_refresh" && sa != nil && sa.Auth.RefreshToken == "" {
			c.Eligibility = "missing_credential"
			c.Reason = "missing_refresh_token"
		}
		out[key] = c
	}
	return out
}

type businessCapabilityError struct{ Feature string }

func (e *businessCapabilityError) Error() string {
	return fmt.Sprintf("unsupported_region: %s is not applicable to this account region", e.Feature)
}
func requireBusiness(sa *storedAuth, feature string) error {
	if !supportsBusiness(sa, feature) {
		return &businessCapabilityError{feature}
	}
	return nil
}
func businessErrorResult(id string, err error) map[string]any {
	r := map[string]any{"ok": false, "auth_index": id, "error": safeManagementError(err)}
	var unsupported *businessCapabilityError
	if errors.As(err, &unsupported) {
		r["code"] = "unsupported_region"
		r["feature"] = unsupported.Feature
		r["status"] = "unsupported"
	}
	var invalid *businessRequestError
	if errors.As(err, &invalid) {
		r["code"] = invalid.Code
	}
	return r
}

type businessRequestError struct{ Code, Message string }

func (e *businessRequestError) Error() string { return e.Message }
