package main

import "time"

func pluginRuntimeSettings() map[string]any {
	checkinAutoMu.RLock()
	ci := checkinAuto
	checkinAutoMu.RUnlock()
	lifecycleAutoMu.RLock()
	lc := lifecycleAuto
	lifecycleAutoMu.RUnlock()
	return map[string]any{"last_travel_run": travelRunSnapshot(), "business_hours": append([]int(nil), checkinHours...), "schedule_timezone": time.Now().Location().String(), "checkin_auto": ci, "lifecycle_auto": lc, "token_keepalive": keepaliveEnabled(), "travel_auto": travelAutoEnabled(), "scheduler_mode": loadedSchedulerMode()}
}
