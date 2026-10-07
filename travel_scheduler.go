package main

import (
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"sync"
	"time"
)

type travelRunSummary struct {
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Attempted  int    `json:"attempted"`
	Succeeded  int    `json:"succeeded"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	Error      string `json:"error,omitempty"`
}

var travelRunGate sync.Mutex
var travelRunState struct {
	sync.Mutex
	Last *travelRunSummary
}

func travelRunSnapshot() *travelRunSummary {
	travelRunState.Lock()
	defer travelRunState.Unlock()
	if travelRunState.Last == nil {
		return nil
	}
	copy := *travelRunState.Last
	return &copy
}
func publishTravelRun(r travelRunSummary) {
	travelRunState.Lock()
	defer travelRunState.Unlock()
	travelRunState.Last = &r
}
func dailyBusinessTick(now time.Time) bool {
	for _, h := range checkinHours {
		if now.Hour() == h {
			return true
		}
	}
	return false
}
func runScheduledBusinessTick(now time.Time) {
	if dailyBusinessTick(now) {
		runAutoCheckin()
		runAutoTravel()
	}
	if shouldRunKeepaliveNow(now) {
		runTokenKeepalive()
	}
}

// Independent of checkin_auto and retired growth_auto. Never reports activity.
func runAutoTravel() {
	if !travelAutoEnabled() || pluginQuiescing() || !travelRunGate.TryLock() {
		return
	}
	defer travelRunGate.Unlock()
	if !pluginWorkerStart() {
		return
	}
	defer pluginWorkerDone()
	summary := travelRunSummary{Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	publishTravelRun(summary)
	defer func() { summary.FinishedAt = time.Now().UTC().Format(time.RFC3339); publishTravelRun(summary) }()
	files, err := hostAuthList()
	if err != nil {
		summary.Status = "failed"
		summary.Error = "credential list unavailable"
		return
	}
	var wg sync.WaitGroup
	var count sync.Mutex
	sem := make(chan struct{}, 4)
	for _, f := range files {
		sem <- struct{}{}
		wg.Add(1)
		go func(f pluginapi.HostAuthFileEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			status := runAutoTravelAccount(f)
			count.Lock()
			defer count.Unlock()
			switch status {
			case "skipped":
				summary.Skipped++
			case "success":
				summary.Attempted++
				summary.Succeeded++
			default:
				summary.Attempted++
				summary.Failed++
			}
		}(f)
	}
	wg.Wait()
	summary.Status = "success"
	if summary.Failed > 0 {
		summary.Status = "failed"
		if summary.Succeeded > 0 {
			summary.Status = "partial"
		}
	}
	if summary.Attempted == 0 {
		summary.Status = "skipped"
	}
	if pluginQuiescing() {
		summary.Status = "canceled"
	}
}
func runAutoTravelAccount(f pluginapi.HostAuthFileEntry) string {
	if f.Disabled || !travelAutoEnabled() || pluginQuiescing() {
		return "skipped"
	}
	sa, err := hostAuthGet(f.AuthIndex)
	if err != nil {
		return "failed"
	}
	if !supportsBusiness(sa, "travel") {
		return "skipped"
	}
	lock := growthTaskLockFor(f.AuthIndex)
	lock.Lock()
	defer lock.Unlock()
	if !travelAutoEnabled() || pluginQuiescing() {
		return "skipped"
	}
	r := runBuddyTravel(sa)
	if r["ok"] == true {
		return "success"
	}
	return "failed"
}
