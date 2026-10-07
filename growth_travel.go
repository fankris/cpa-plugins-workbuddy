// growth_travel.go implements the CN "buddy travel" (猫猫旅行) daily welfare
// loop: query status → depart when idle → claim when arrived. The protocol
// shape (config endpoint supplies the destination id required by depart)
// follows workbuddy2api-hub's verified implementation (MIT).
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	growthTravelStatusPath = "/activity/growth/buddy/travel/status"
	growthTravelConfigPath = "/activity/growth/buddy/travel/config"
	growthTravelDepartPath = "/activity/growth/buddy/travel/depart"
	growthTravelClaimPath  = "/activity/growth/buddy/travel/claim"
)

// travelStatus is the subset of the upstream payload the panel renders.
type travelStatus struct {
	State             string         `json:"state"`
	DailyLimitReached bool           `json:"daily_limit_reached,omitempty"`
	LocationName      string         `json:"location_name,omitempty"`
	Raw               map[string]any `json:"-"`
}

func fetchTravelStatus(sa *storedAuth) (*travelStatus, error) {
	data, err := growthJSON(sa, http.MethodGet, upstreamBaseFor(sa), growthTravelStatusPath, nil, false)
	if err != nil {
		return nil, err
	}
	var payload struct {
		State             string `json:"state"`
		DailyLimitReached bool   `json:"daily_limit_reached"`
		Location          struct {
			Name string `json:"name"`
		} `json:"location"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, fmt.Errorf("旅行状态解析失败")
		}
	}
	return &travelStatus{
		State:             strings.TrimSpace(payload.State),
		DailyLimitReached: payload.DailyLimitReached,
		LocationName:      strings.TrimSpace(payload.Location.Name),
	}, nil
}

// travelConfigLocationID fetches the first available destination. The depart
// endpoint rejects an empty body with HTTP 400 "invalid request" — the
// location id is mandatory (verified upstream behaviour).
func travelConfigLocationID(sa *storedAuth) (any, error) {
	data, err := growthJSON(sa, http.MethodGet, upstreamBaseFor(sa), growthTravelConfigPath, nil, false)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Locations []struct {
			ID any `json:"id"`
		} `json:"locations"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, fmt.Errorf("旅行目的地解析失败")
		}
	}
	if len(payload.Locations) == 0 || payload.Locations[0].ID == nil {
		return nil, fmt.Errorf("上游未返回可用的旅行目的地")
	}
	return payload.Locations[0].ID, nil
}

// runBuddyTravel performs one travel cycle: claim when arrived, depart when
// idle. Returns a panel-ready result map.
func runBuddyTravel(sa *storedAuth) map[string]any {
	if err := requireBusiness(sa, "travel"); err != nil {
		return businessErrorResult("", err)
	}
	status, err := fetchTravelStatus(sa)
	if err != nil {
		return map[string]any{"ok": false, "error": safeManagementError(err)}
	}
	switch strings.ToLower(status.State) {
	case "arrived":
		data, err := growthJSON(sa, http.MethodPost, upstreamBaseFor(sa), growthTravelClaimPath, nil, false)
		if err != nil {
			return map[string]any{"ok": false, "state": status.State, "error": safeManagementError(err)}
		}
		var claim struct {
			RewardCredit int64 `json:"reward_credit"`
		}
		if len(data) > 0 {
			_ = json.Unmarshal(data, &claim)
		}
		invalidateAccountCredits("", sa.Account.UID)
		return map[string]any{
			"ok": true, "action": "claim", "state": status.State,
			"credit":  claim.RewardCredit,
			"message": fmt.Sprintf("旅行归来领奖成功，获得 %d 积分", claim.RewardCredit),
		}
	case "idle":
		if status.DailyLimitReached {
			return map[string]any{"ok": true, "action": "idle", "state": status.State, "message": "今日旅行已完成，明日 00:00 刷新"}
		}
		locationID, err := travelConfigLocationID(sa)
		if err != nil {
			// Do not invent a destination when the upstream config is unknown.
			return map[string]any{"ok": false, "error": safeManagementError(err)}
		}
		data, err := growthJSON(sa, http.MethodPost, upstreamBaseFor(sa), growthTravelDepartPath,
			map[string]any{"location_id": locationID}, false)
		if err != nil {
			return map[string]any{"ok": false, "state": status.State, "error": safeManagementError(err)}
		}
		var depart struct {
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
		}
		if len(data) > 0 {
			_ = json.Unmarshal(data, &depart)
		}
		msg := "猫猫已出发旅行，数小时后归来"
		if depart.Location.Name != "" {
			msg = fmt.Sprintf("猫猫已出发前往「%s」", depart.Location.Name)
		}
		return map[string]any{"ok": true, "action": "depart", "state": status.State, "message": msg}
	case "traveling":
		return map[string]any{"ok": true, "action": "traveling", "state": status.State, "message": "猫猫正在旅行途中"}
	default:
		return map[string]any{"ok": false, "state": status.State, "error": "unrecognized travel status"}
	}
}
