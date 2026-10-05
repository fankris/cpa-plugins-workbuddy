package main

import (
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"testing"
	"time"
)

func TestRefreshedExpiryUsesDurationOnlyWhenProvided(t *testing.T) {
	now := time.Unix(2000000000, 0)
	old := now.Add(3 * time.Hour).Unix()
	for _, seconds := range []int64{0, -1} {
		if got := refreshedExpiry(seconds, old, now); got != old {
			t.Fatalf("missing/invalid expiry replaced old deadline: %d", got)
		}
	}
	if got := refreshedExpiry(3600, old, now); got != now.Add(time.Hour).Unix() {
		t.Fatal("positive lifetime ignored")
	}
	if got := refreshedExpiry(0, 0, now); got != 0 {
		t.Fatal("invented unknown expiry")
	}
}
func TestRetiredSyntheticRouteFailsExplicitly(t *testing.T) {
	raw, err := handleManagement(mustJSON(pluginapi.ManagementRequest{Method: http.MethodPost, Path: loadedManagementBasePath() + "/plugins/" + providerName + "/tasks/light"}))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Result pluginapi.ManagementResponse `json:"result"`
	}
	if err = json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.StatusCode != http.StatusGone {
		t.Fatalf("retired route status = %d", env.Result.StatusCode)
	}
}
