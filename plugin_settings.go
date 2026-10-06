package main

func pluginRuntimeSettings() map[string]any {
	checkinAutoMu.RLock()
	ci := checkinAuto
	checkinAutoMu.RUnlock()
	lifecycleAutoMu.RLock()
	lc := lifecycleAuto
	lifecycleAutoMu.RUnlock()
	return map[string]any{"checkin_auto": ci, "lifecycle_auto": lc, "token_keepalive": keepaliveEnabled(), "travel_auto": travelAutoEnabled(), "scheduler_mode": loadedSchedulerMode()}
}
