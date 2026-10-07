package main

import (
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"strings"
	"time"
)

// Billing's zone-less CycleEndTime is China Standard Time, not browser/server local time.
func creditExpiryTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if t, e := time.Parse(time.RFC3339, raw); e == nil {
		return t, true
	}
	t, e := time.ParseInLocation("2006-01-02 15:04:05", raw, time.FixedZone("CST", 8*3600))
	return t, e == nil
}

type creditExpiry struct {
	Within7Days   int64  `json:"within_7_days"`
	NextAt        string `json:"next_at,omitempty"`
	NextAmount    int64  `json:"next_amount"`
	UnknownRemain int64  `json:"unknown_remain"`
	Known         bool   `json:"known"`
}

func summarizeCreditExpiry(c *creditsSummary, now time.Time) creditExpiry {
	out := creditExpiry{}
	if c == nil {
		return out
	}
	out.Known = len(c.Packages) > 0
	var earliest time.Time
	positive, recognized := false, false
	for _, p := range c.Packages {
		if p.Remain <= 0 {
			continue
		}
		positive = true
		at, ok := creditExpiryTime(p.CycleEnd)
		if !ok {
			out.UnknownRemain += p.Remain
			continue
		}
		recognized = true
		if !at.After(now) {
			continue
		}
		if !at.After(now.Add(7 * 24 * time.Hour)) {
			out.Within7Days += p.Remain
		}
		if earliest.IsZero() || at.Before(earliest) {
			earliest = at
			out.NextAmount = p.Remain
		} else if at.Equal(earliest) {
			out.NextAmount += p.Remain
		}
	}
	out.Known = out.Known && (!positive || recognized)
	if !earliest.IsZero() {
		out.NextAt = earliest.UTC().Format(time.RFC3339)
	}
	return out
}
func (c creditsSummary) MarshalJSON() ([]byte, error) {
	type plain creditsSummary
	return json.Marshal(struct {
		plain
		Expiry creditExpiry `json:"expiry"`
	}{plain(c), summarizeCreditExpiry(&c, time.Now())})
}

// Host supplies model/availability/cooldown-filtered candidates. Never enumerate a parallel pool,
// fetch billing during routing, alter stored priority.
func pickExpiringCredits(candidates []pluginapi.SchedulerAuthCandidate, now time.Time) pluginapi.SchedulerPickResponse {
	bestPriority := -int(^uint(0)>>1) - 1
	for _, c := range candidates {
		if c.Provider != providerName {
			return pluginapi.SchedulerPickResponse{Handled: false}
		}
		if !candidateDisabled(c) && c.Priority > bestPriority {
			bestPriority = c.Priority
		}
	}
	picked := ""
	var earliest time.Time
	for _, c := range candidates {
		if c.Priority != bestPriority || candidateDisabled(c) {
			continue
		}
		v, ok := accountCache.Load(c.ID)
		if !ok {
			continue
		}
		entry, ok := v.(*accountCacheEntry)
		if !ok || !creditSnapshotFresh(entry, now, 5*time.Minute) || isCreditsExhausted(entry.credits) {
			continue
		}
		fetched, e := time.Parse(time.RFC3339, entry.credits.FetchedAt)
		age := now.Sub(fetched)
		if e != nil || age < 0 || age > 5*time.Minute {
			continue
		}
		summary := summarizeCreditExpiry(entry.credits, now)
		at, ok := creditExpiryTime(summary.NextAt)
		if !ok || summary.Within7Days <= 0 {
			continue
		}
		if picked == "" || at.Before(earliest) || (at.Equal(earliest) && c.ID < picked) {
			picked = c.ID
			earliest = at
		}
	}
	if picked == "" {
		return pluginapi.SchedulerPickResponse{Handled: false}
	}
	return pluginapi.SchedulerPickResponse{Handled: true, AuthID: picked}
}
