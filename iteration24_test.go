package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func envelope24(data string) *hostHTTPResponse {
	return &hostHTTPResponse{StatusCode: 200, Body: []byte(`{"code":0,"data":` + data + `}`)}
}

const resource24 = `{"TotalCount":1,"Accounts":[{"PackageName":"Pro","CapacityRemain":50,"CapacityUsed":10,"CapacitySize":60}]}`

func Test24CapabilityMatrixAndUnknownEligibility(t *testing.T) {
	for _, region := range []string{regionCN, regionGlobal, regionIntl} {
		a := &storedAuth{Auth: storedTokens{Region: region}}
		c := accountCapabilities(a, nil)
		domestic := region == regionCN
		for _, f := range []string{"checkin", "tasks", "travel"} {
			if c[f].Supported != domestic {
				t.Fatal(region, f, c)
			}
		}
		if c["trial"].Supported == domestic || (!domestic && c["trial"].Eligibility != "unknown") || c["token_refresh"].Eligibility != "missing_credential" {
			t.Fatal(c)
		}
	}
	if supportsBusiness(nil, "tasks") || supportsBusiness(nil, "trial") {
		t.Fatal("nil capability")
	}
	a := &storedAuth{Auth: storedTokens{Region: regionIntl}}
	cr := &creditsSummary{Packages: []packageSummary{{Name: "CodeBuddy One-time Free 2-Week Pro Plan Trial"}}}
	if accountCapabilities(a, cr)["trial"].Eligibility != "already_claimed" {
		t.Fatal("trial evidence lost")
	}
}
func Test24ForeignCacheAndLowLevelCheckinNeverProbe(t *testing.T) {
	a := directoryTest(t)
	resetAccountCache()
	t.Cleanup(resetAccountCache)
	var calls atomic.Int32
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		calls.Add(1)
		if strings.Contains(r.URL.Path, "checkin") {
			t.Error("foreign check-in probe", r.URL)
		}
		if strings.HasSuffix(r.URL.Path, "get-user-resource") {
			return envelope24(resource24), nil
		}
		return envelope24(`{"paymentType":"Pro"}`), nil
	}
	for _, region := range []string{regionGlobal, regionIntl} {
		a.Auth.Region = region
		a.Auth.Domain = ""
		for _, force := range []bool{false, true} {
			_, ci, cr, errs := cachedAccountDetails("foreign-"+region, a, force)
			if ci != nil || cr == nil || len(errs) > 0 {
				t.Fatal(ci, cr, errs)
			}
		}
	}
	n := calls.Load()
	if _, e := fetchCheckinStatus(a); e == nil {
		t.Fatal("foreign status accepted")
	}
	if _, e := performCheckinCall(a); e == nil {
		t.Fatal("foreign mutation accepted")
	}
	if calls.Load() != n {
		t.Fatal("unsupported low-level request reached HTTP")
	}
}
func Test24ForeignCreditReadFallbackIsNarrow(t *testing.T) {
	for _, status := range []int{404, 405, 401, 403, 429, 500, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			a := directoryTest(t)
			old := billingRetryDelays
			billingRetryDelays = nil
			t.Cleanup(func() { billingRetryDelays = old })
			paths := []string{}
			hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
				paths = append(paths, r.URL.Path)
				if r.URL.Host != "www.workbuddy.ai" {
					t.Error("cross-region", r.URL)
				}
				if len(paths) == 1 {
					return &hostHTTPResponse{StatusCode: status, Body: []byte("not-json")}, nil
				}
				return envelope24(resource24), nil
			}
			cr, err := fetchUserResource(a)
			if status == 404 || status == 405 {
				if err != nil || cr.TotalRemain != 50 || len(paths) != 2 || paths[0] != "/billing/meter/get-user-resource" || paths[1] != "/v2/billing/meter/get-user-resource" {
					t.Fatal(cr, err, paths)
				}
			} else if err == nil || len(paths) != 1 {
				t.Fatal(err, paths)
			}
		})
	}
}
func Test24BusinessCode404AndCNResourcePath(t *testing.T) {
	a := directoryTest(t)
	calls := 0
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		calls++
		if calls == 1 {
			return &hostHTTPResponse{StatusCode: 200, Body: []byte(`{"code":404}`)}, nil
		}
		return envelope24(resource24), nil
	}
	if cr, e := fetchUserResource(a); e != nil || cr.TotalRemain != 50 || calls != 2 {
		t.Fatal(cr, e, calls)
	}
	a.Auth.Region = regionCN
	a.Auth.Domain = "codebuddy.cn"
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		if r.URL.Path != "/v2/billing/meter/get-user-resource" {
			t.Error(r.URL)
		}
		return envelope24(resource24), nil
	}
	if _, e := fetchUserResource(a); e != nil {
		t.Fatal(e)
	}
}
func Test24AutomaticTravelIndependentSchedulerMatrix(t *testing.T) {
	modelTestState(t)
	store := newFakeAuthStore()
	store.put("cn", "cn.json", "cn", regionCN, false)
	store.put("foreign", "foreign.json", "foreign", regionIntl, false)
	store.put("disabled", "disabled.json", "disabled", regionCN, true)
	installFakeAuthStore(t, store)
	oldCI, oldLC, oldTravel := checkinAuto, lifecycleAuto, travelAuto
	defer func() {
		checkinAuto = oldCI
		lifecycleAuto = oldLC
		travelAuto = oldTravel
		travelRunState.Lock()
		travelRunState.Last = nil
		travelRunState.Unlock()
		resetAccountCache()
	}()
	lifecycleAuto = false
	var trips atomic.Int32
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		if r.URL.Path == growthTravelStatusPath {
			trips.Add(1)
			if r.Header.Get("Authorization") != "Bearer access-cn" {
				t.Error("foreign/disabled travel", r.Header.Get("Authorization"))
			}
			return envelope24(`{"state":"traveling"}`), nil
		}
		if strings.Contains(r.URL.Path, "checkin") {
			return envelope24(`{"active":false}`), nil
		}
		t.Error("unexpected automatic action", r.URL)
		return nil, fmt.Errorf("unexpected")
	}
	for _, ci := range []bool{false, true} {
		for _, tr := range []bool{false, true} {
			checkinAuto = ci
			travelAuto = tr
			before := trips.Load()
			runScheduledBusinessTick(time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC))
			want := int32(0)
			if tr {
				want = 1
			}
			if trips.Load()-before != want {
				t.Fatal(ci, tr, trips.Load()-before)
			}
			if tr {
				r := travelRunSnapshot()
				if r.Status != "success" || r.Succeeded != 1 || r.Skipped != 2 || r.FinishedAt == "" {
					t.Fatal(r)
				}
			}
		}
	}
	before := trips.Load()
	runScheduledBusinessTick(time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if trips.Load() != before {
		t.Fatal("outside schedule")
	}
	if len(store.savedRecords()) != 0 {
		t.Fatal("travel wrote auth")
	}
}
func Test24UnsupportedTaskAndTrialManagementAre422(t *testing.T) {
	modelTestState(t)
	store := newFakeAuthStore()
	store.put("foreign", "f.json", "foreign", regionIntl, false)
	store.put("cn", "c.json", "cn", regionCN, false)
	installFakeAuthStore(t, store)
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		t.Error("unsupported request", r.URL)
		return nil, fmt.Errorf("unexpected")
	}
	for _, path := range []string{"/tasks/accept", "/tasks/accept_all", "/tasks/claim", "/tasks/travel"} {
		status, r := managementCall(t, "POST", path, `{"auth_index":"foreign","task_code":"a"}`)
		if status != 422 || r["code"] != "unsupported_region" {
			t.Fatal(path, status, r)
		}
	}
	status, r := managementCallQuery(t, "/tasks", map[string][]string{"auth_index": {"foreign"}})
	if status != 422 || r["code"] != "unsupported_region" {
		t.Fatal(status, r)
	}
	status, r = managementCall(t, "POST", "/trial", `{"auth_index":"cn"}`)
	if status != 422 || r["code"] != "unsupported_region" {
		t.Fatal(status, r)
	}
	if _, err := performTrialCall(&storedAuth{}); err == nil {
		t.Fatal("low-level CN trial")
	}
}
func Test24ActivationReadOnlyAndUnknownCases(t *testing.T) {
	a := directoryTest(t)
	for _, c := range []struct {
		code        int
		body, state string
	}{{200, `{"code":0}`, "registered"}, {200, `{"code":"200"}`, "registered"}, {200, `{"code":500}`, "required"}, {500, `{"code":500}`, "unknown"}, {401, `{"code":0}`, "unknown"}, {200, `{}`, "unknown"}, {200, `{"code":14017}`, "unknown"}} {
		hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
			if r.Method != "GET" || r.URL.Host != "www.workbuddy.ai" || r.URL.Path != "/auth/realms/copilot/overseas/user/register" || r.URL.Query().Get("userId") != a.Account.UID {
				t.Error("not read-only scoped request", r.URL, r.Method)
			}
			return &hostHTTPResponse{StatusCode: c.code, Body: []byte(c.body)}, nil
		}
		r := fetchActivationStatus(context.Background(), a)
		if r["registration"] != c.state || r["trial_eligibility"] != "unknown" {
			t.Fatal(c, r)
		}
	}
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		t.Error("domestic activation request")
		return nil, fmt.Errorf("unexpected")
	}
	r := fetchActivationStatus(context.Background(), &storedAuth{})
	if r["code"] != "unsupported_region" {
		t.Fatal(r)
	}
}
func Test24TrialAlreadyClaimedDoesNotRunMaintenance(t *testing.T) {
	modelTestState(t)
	store := newFakeAuthStore()
	store.put("foreign", "f.json", "foreign", regionIntl, false)
	installFakeAuthStore(t, store)
	old := lifecycleAuto
	lifecycleAuto = true
	defer func() { lifecycleAuto = old }()
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		if r.URL.Path != "/billing/ide/trial" {
			t.Error("unexpected trial side effect", r.URL)
		}
		return &hostHTTPResponse{StatusCode: 200, Body: []byte(`{"code":14051,"msg":"already claimed"}`)}, nil
	}
	r := handleClaimTrial(pluginapi.ManagementRequest{Body: json.RawMessage(`{"auth_index":"foreign"}`)})
	if r["already_claimed"] != true || len(store.savedRecords()) != 0 {
		t.Fatal(r, store.savedRecords())
	}
}
func Test24TaskRequestsCarryCredentialNotRefreshToken(t *testing.T) {
	modelTestState(t)
	a := &storedAuth{Auth: storedTokens{Region: regionCN, AccessToken: "account-token", RefreshToken: "refresh-private"}, Account: storedAccount{UID: "task-user"}}
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		if r.Header.Get("Authorization") != "Bearer account-token" || r.Header.Get("X-User-Id") != "task-user" || r.Header.Get("X-Refresh-Token") != "" {
			t.Error("bad scoped task auth", r.Header)
		}
		return envelope24(`{"tasks":[]}`), nil
	}
	if _, err := listGrowthTasks(a); err != nil {
		t.Fatal(err)
	}
}
func Test24UnknownTravelStateCannotReportSuccess(t *testing.T) {
	stub := &travelStub{state: "future-state"}
	installTravelStub(t, stub)
	if r := runBuddyTravel(cnTravelAuth()); r["ok"] != false {
		t.Fatal(r)
	}
	if stub.posts[growthTravelDepartPath] != 0 || stub.posts[growthTravelClaimPath] != 0 {
		t.Fatal(stub.posts)
	}
}
func Test24TravelWorkersBoundedAndQuiesceCancelsRequests(t *testing.T) {
	modelTestState(t)
	store := newFakeAuthStore()
	for i := 0; i < 9; i++ {
		id := fmt.Sprint(i)
		store.put(id, id+".json", id, regionCN, false)
	}
	installFakeAuthStore(t, store)
	old := travelAuto
	travelAuto = true
	defer func() { travelAuto = old; travelRunState.Lock(); travelRunState.Last = nil; travelRunState.Unlock() }()
	started := make(chan struct{}, 9)
	var total atomic.Int32
	hostHTTPTestOverride = func(r *http.Request) (*hostHTTPResponse, error) {
		total.Add(1)
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("unbounded travel HTTP")
		}
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	done := make(chan struct{})
	go func() { runAutoTravel(); close(done) }()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	if total.Load() != 4 {
		t.Fatal("worker bound exceeded", total.Load())
	}
	quiesced := make(chan struct{})
	go func() { quiescePlugin(); close(quiesced) }()
	select {
	case <-quiesced:
	case <-time.After(3 * time.Second):
		t.Fatal("quiesce failed to drain travel")
	}
	<-done
	if total.Load() != 4 || travelRunSnapshot().Status != "canceled" {
		t.Fatal(total.Load(), travelRunSnapshot())
	}
}
