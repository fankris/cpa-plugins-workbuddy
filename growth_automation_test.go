package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Growth automation is the most side-effecting part of the plugin: it posts
// behaviour events upstream and claims rewards. These tests pin the rules that
// keep it from misbehaving — desktop-only tasks are never faked, night tasks
// respect their window, and a claim is attempted only after the target is met.

// growthStub answers the growth endpoints. It records every reported event so
// tests can assert the exact event batch shape. Stateful on purpose: a
// successful accept flips accept_status to "accepted" and a successful report
// advances the task's current by one, mirroring the upstream closely enough
// that the accept-retry and progress-poll logic under test behaves as it does
// against the real service.
type growthStub struct {
	tasks        []map[string]any
	events       [][]map[string]any
	claims       []string
	accepts      []string
	reportStatus int
	claimBody    string
	// acceptFailures makes the first N accept calls fail transiently (HTTP
	// 500), exercising the accept-retry pass.
	acceptFailures int
	// acceptBroken makes accept calls succeed without changing state — the
	// upstream refused the task but answered 200.
	acceptBroken bool
	// frozen stops reports from advancing progress: the upstream accepted the
	// task but silently drops events, so the claim gate must defer.
	frozen bool
}

// findTask returns the stub task with the given code.
func (s *growthStub) findTask(code string) map[string]any {
	for _, t := range s.tasks {
		if t["task_code"] == code {
			return t
		}
	}
	return nil
}

// stubNum reads a JSON-ish number field that may hold any Go numeric literal
// (int literals in test fixtures, float64 after a round-trip).
func stubNum(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func (s *growthStub) handler(req *http.Request) (*hostHTTPResponse, error) {
	body, _ := io.ReadAll(req.Body)
	ok := func(data string) (*hostHTTPResponse, error) {
		return &hostHTTPResponse{
			StatusCode: 200,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"code":0,"msg":"OK","data":` + data + `}`),
		}, nil
	}
	switch {
	case strings.HasSuffix(req.URL.Path, growthTasksPath):
		raw, _ := json.Marshal(map[string]any{"tasks": s.tasks})
		return ok(string(raw))

	case strings.HasSuffix(req.URL.Path, growthReportPath):
		var batch []map[string]any
		_ = json.Unmarshal(body, &batch)
		s.events = append(s.events, batch)
		if s.reportStatus != 0 {
			return &hostHTTPResponse{StatusCode: s.reportStatus, Body: []byte("report failed")}, nil
		}
		// One accepted event advances exactly one unit of progress. Events
		// carry no task_code, so the stub advances the first actionable task
		// (accepted, not claimed/locked, below target) — every test here has
		// at most one lit task, which keeps the simulation faithful.
		if !s.frozen {
			for range batch {
				for _, t := range s.tasks {
					status, _ := t["accept_status"].(string)
					claimed, _ := t["claimed"].(bool)
					locked, _ := t["locked"].(bool)
					if claimed || locked || (status != "" && status != "accepted" && status != "completed") {
						continue
					}
					cur := stubNum(t["current"])
					tgt := stubNum(t["target"])
					if cur < tgt {
						t["current"] = cur + 1
						break
					}
				}
			}
		}
		return ok(`{}`)

	case strings.HasSuffix(req.URL.Path, "/claim"):
		// /v2/activity/growth/tasks/<code>/claim
		trimmed := strings.TrimSuffix(req.URL.Path, "/claim")
		code := trimmed[strings.LastIndex(trimmed, "/")+1:]
		s.claims = append(s.claims, code)
		if s.claimBody != "" {
			return ok(s.claimBody)
		}
		return ok(`{"credit":10,"energy":5}`)

	case strings.HasSuffix(req.URL.Path, growthTasksAcceptPath):
		var payload struct {
			TaskCodes []string `json:"task_codes"`
		}
		_ = json.Unmarshal(body, &payload)
		s.accepts = append(s.accepts, payload.TaskCodes...)
		if s.acceptFailures > 0 {
			s.acceptFailures--
			return &hostHTTPResponse{StatusCode: 500, Body: []byte("transient accept failure")}, nil
		}
		if !s.acceptBroken {
			for _, code := range payload.TaskCodes {
				if t := s.findTask(code); t != nil {
					if status, _ := t["accept_status"].(string); status != "accepted" {
						t["accept_status"] = "accepted"
					}
				}
			}
		}
		return ok(`{}`)
	}
	return &hostHTTPResponse{StatusCode: 404, Body: []byte("unexpected path " + req.URL.Path)}, nil
}

func installGrowthStub(t *testing.T, stub *growthStub) {
	t.Helper()
	old := hostHTTPTestOverride
	hostHTTPTestOverride = stub.handler
	t.Cleanup(func() { hostHTTPTestOverride = old })
}

func cnGrowthAuth() *storedAuth {
	sa := &storedAuth{}
	sa.Auth.AccessToken = "token"
	sa.Auth.Region = regionCN
	sa.Account.UID = "u-growth"
	sa.Account.EnterpriseID = "ent-1"
	return sa
}

// A claimable task must be claimed, and the reward folded into the result.
func TestGrowthLightClaimsTaskAlreadyAtTarget(t *testing.T) {
	stub := &growthStub{tasks: []map[string]any{{
		"task_code": "create_canvas", "target": 1, "current": 1,
		"claimable": true, "accept_status": "accepted",
	}}}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.claims) != 1 || stub.claims[0] != "create_canvas" {
		t.Fatalf("expected one claim for create_canvas, got %v", stub.claims)
	}
	if res.CreditEarned != 10 {
		t.Fatalf("claim reward not recorded, got credit=%d (%+v)", res.CreditEarned, res)
	}
}

// A task below target must be lit by reporting behaviour events until the
// target is met, then claimed.
func TestGrowthLightReportsEventsThenClaims(t *testing.T) {
	stub := &growthStub{tasks: []map[string]any{{
		"task_code": "create_canvas", "target": 2, "current": 0,
		"accept_status": "accepted",
	}}}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.events) != 2 {
		t.Fatalf("target=2 from current=0 needs 2 event reports, got %d", len(stub.events))
	}
	for i, batch := range stub.events {
		if len(batch) != 1 {
			t.Fatalf("batch %d should carry exactly one event, got %d", i, len(batch))
		}
		if batch[0]["eventCode"] == nil {
			t.Fatalf("event %d is missing eventCode: %+v", i, batch[0])
		}
	}
	if len(stub.claims) != 1 {
		t.Fatalf("a lit task must be claimed, got %v", stub.claims)
	}
	if len(res.Lit) != 1 || res.Lit[0] != "create_canvas" {
		t.Fatalf("lit task not recorded: %+v", res)
	}
}

// Desktop-only tasks must be reported honestly, never faked.
func TestGrowthLightSkipsDesktopOnlyTasks(t *testing.T) {
	stub := &growthStub{tasks: []map[string]any{{
		"task_code": "Buddy_App", "target": 1, "current": 0, "accept_status": "accepted",
	}}}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.events) != 0 {
		t.Fatalf("desktop-only tasks must not be lit, saw %d event batches", len(stub.events))
	}
	if len(res.DesktopOnly) != 1 || res.DesktopOnly[0] != "Buddy_App" {
		t.Fatalf("desktop-only task should be reported as such: %+v", res)
	}
}

// Unknown task codes must be skipped rather than reported with a guessed event.
func TestGrowthLightSkipsUnknownTaskCodes(t *testing.T) {
	stub := &growthStub{tasks: []map[string]any{{
		"task_code": "totally_unknown_task", "target": 1, "current": 0, "accept_status": "accepted",
	}}}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.events) != 0 {
		t.Fatal("an unknown task code must not produce events")
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("unknown code should be skipped: %+v", res)
	}
}

// The night task (black_cat) must only be lit inside its 23:00–08:00 window.
func TestGrowthLightRespectsNightWindow(t *testing.T) {
	now := time.Now()
	night := inNightWindow(now)
	stub := &growthStub{tasks: []map[string]any{{
		"task_code": "black_cat", "target": 1, "current": 0, "accept_status": "accepted",
	}}}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if night {
		if len(stub.events) == 0 {
			t.Fatal("inside the night window the cat task must be lit")
		}
	} else {
		if len(stub.events) != 0 {
			t.Fatal("outside the night window the cat task must not be lit")
		}
		if len(res.Skipped) == 0 {
			t.Fatalf("outside the window the task should be reported as skipped: %+v", res)
		}
	}
}

// Already-claimed and locked tasks must never be touched again.
func TestGrowthLightIgnoresClaimedAndLockedTasks(t *testing.T) {
	// "claimed" is carried by accept_status/status upstream, and a locked task
	// must not be actionable even when its progress already reached target.
	stub := &growthStub{tasks: []map[string]any{
		{"task_code": "create_canvas", "target": 1, "current": 1, "accept_status": "claimed"},
		{"task_code": "template_5", "target": 1, "current": 1, "locked": true, "accept_status": "accepted"},
	}}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.events) != 0 {
		t.Fatalf("claimed/locked tasks must not be lit, saw %d batches", len(stub.events))
	}
	if len(stub.claims) != 0 {
		t.Fatalf("claimed/locked tasks must not be claimed, saw %v", stub.claims)
	}
}

// A pending (not yet accepted) task must be accepted first.
func TestGrowthLightAcceptsPendingTasks(t *testing.T) {
	stub := &growthStub{tasks: []map[string]any{{
		"task_code": "create_canvas", "target": 1, "current": 1,
		"claimable": true, "accept_status": "pending",
	}}}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.accepts) != 1 || stub.accepts[0] != "create_canvas" {
		t.Fatalf("pending task must be accepted first, got %v", stub.accepts)
	}
}

// An upstream report failure must be reported as a failure, never as success.
func TestGrowthLightReportsFailures(t *testing.T) {
	stub := &growthStub{
		reportStatus: 500,
		tasks: []map[string]any{{
			"task_code": "create_canvas", "target": 1, "current": 0, "accept_status": "accepted",
		}},
	}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(res.Failed) != 1 || res.Failed[0] != "create_canvas" {
		t.Fatalf("a failed report must surface as a failure, got %+v", res)
	}
	if len(stub.claims) != 0 {
		t.Fatal("a task that failed to light must not be claimed")
	}
}

// Cancellation must stop the loop promptly (the panel can close mid-run).
func TestGrowthLightStopsOnCancel(t *testing.T) {
	stub := &growthStub{tasks: []map[string]any{{
		"task_code": "create_canvas", "target": 100, "current": 0, "accept_status": "accepted",
	}}}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	close(done) // already cancelled

	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)
	if len(stub.events) > 0 {
		t.Fatalf("a cancelled run must not report events, saw %d", len(stub.events))
	}
	_ = res
}

// Non-CN accounts must be refused before any upstream call: growth tasks are
// a CN-only activity.
func TestGrowthAutomationUnsupportedForNonCNAccounts(t *testing.T) {
	stub := &growthStub{}
	installGrowthStub(t, stub)

	for _, region := range []string{regionGlobal, regionIntl} {
		sa := &storedAuth{}
		sa.Auth.Region = region
		sa.Account.UID = "u-" + region
		if growthTasksSupported(sa) {
			t.Errorf("region %s must not be growth-capable", region)
		}
		runGrowthAutomation("idx", sa)
	}
	if len(stub.events) != 0 || len(stub.claims) != 0 {
		t.Fatal("non-CN accounts must not reach growth endpoints")
	}
}

// acceptedGrowthTaskCodes must pick exactly the actionable codes.
func TestAcceptedGrowthTaskCodesSelection(t *testing.T) {
	tasks := []growthTask{
		{TaskCode: "a", AcceptStatus: "pending"},
		{TaskCode: "b", AcceptStatus: "accepted"},
		{TaskCode: "c", AcceptStatus: "completed"},
		{TaskCode: "d", AcceptStatus: "pending", Claimed: true},
		{TaskCode: "e", AcceptStatus: "pending", Locked: true},
		{TaskCode: "f", AcceptStatus: "pending"},
		{TaskCode: "f", AcceptStatus: "pending"}, // duplicate
		{TaskCode: "", AcceptStatus: "pending"},
	}
	got := acceptedGrowthTaskCodes(tasks)
	want := []string{"a", "f"}
	if len(got) != len(want) {
		t.Fatalf("acceptedGrowthTaskCodes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("acceptedGrowthTaskCodes = %v, want %v", got, want)
		}
	}
}

// Expert/team events must rotate ids so the upstream does not dedup them.
func TestGrowthEventIDRotationForExpertAndTeam(t *testing.T) {
	sa := cnGrowthAuth()
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		event := buildGrowthEvent("expert", sa.Account.UID, i, sa)
		id, _ := event["id"].(string)
		if id == "" {
			t.Fatalf("expert event %d has no id: %+v", i, event)
		}
		if seen[id] {
			t.Fatalf("expert event id %q repeated — upstream would dedup it", id)
		}
		seen[id] = true
	}
}

// Night window boundaries must match the documented 23:00–08:00 rule.
func TestNightWindowBoundaries(t *testing.T) {
	cases := []struct {
		hour int
		want bool
	}{
		{22, false}, {23, true}, {0, true}, {3, true}, {7, true}, {8, false}, {12, false},
	}
	for _, tc := range cases {
		at := time.Date(2026, 9, 19, tc.hour, 30, 0, 0, time.Local)
		if got := inNightWindow(at); got != tc.want {
			t.Errorf("hour %02d: inNightWindow = %v, want %v", tc.hour, got, tc.want)
		}
	}
}

// A transiently rejected accept batch must be retried after a re-list; without
// the retry every later report lands on an unaccepted task and progress stays
// at zero (the "accepted a pile, lit nothing" failure mode).
func TestGrowthLightRetriesRejectedAccept(t *testing.T) {
	stub := &growthStub{
		acceptFailures: 1,
		tasks: []map[string]any{{
			"task_code": "create_canvas", "target": 1, "current": 0,
			"accept_status": "pending",
		}},
	}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.accepts) != 2 {
		t.Fatalf("a transient accept rejection must be retried once, got %d accepts: %v", len(stub.accepts), stub.accepts)
	}
	if len(stub.events) != 1 {
		t.Fatalf("the retried task must still be lit, saw %d reports", len(stub.events))
	}
	if len(stub.claims) != 1 {
		t.Fatalf("the lit task must be claimed, got %v", stub.claims)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("retry recovered the accept, but failures recorded: %v", res.Failed)
	}
}

// When accept never takes effect (upstream answers 200 but the task stays
// pending), reporting events is wasted work: the run must skip the task
// instead of burning its reports.
func TestGrowthLightSkipsUnacceptedTasks(t *testing.T) {
	stub := &growthStub{
		acceptBroken: true,
		tasks: []map[string]any{{
			"task_code": "create_canvas", "target": 2, "current": 0,
			"accept_status": "pending",
		}},
	}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.events) != 0 {
		t.Fatalf("events must not be reported for a task that never accepted, saw %d", len(stub.events))
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("unaccepted task should be skipped: %+v", res)
	}
	joined := strings.Join(res.Notes, " ")
	if !strings.Contains(joined, "未接取") {
		t.Fatalf("skip note should explain the unaccepted status, got %v", res.Notes)
	}
}

// An upstream that silently drops reports must NOT be claimed: the claim gate
// polls progress and defers the reward to the next run instead of failing the
// claim with "task not completed".
func TestGrowthLightDefersClaimWhenProgressStalls(t *testing.T) {
	stub := &growthStub{
		frozen: true,
		tasks: []map[string]any{{
			"task_code": "create_canvas", "target": 2, "current": 0,
			"accept_status": "accepted",
		}},
	}
	installGrowthStub(t, stub)

	done := make(chan struct{})
	res := lightGrowthTasks(cnGrowthAuth(), nil, time.Millisecond, done)

	if len(stub.events) != 2 {
		t.Fatalf("reports still go out, saw %d", len(stub.events))
	}
	if len(stub.claims) != 0 {
		t.Fatalf("a stalled task must not be claimed blind, got %v", stub.claims)
	}
	if len(res.Lit) != 0 {
		t.Fatalf("a deferred task is not lit: %+v", res)
	}
	joined := strings.Join(res.Notes, " ")
	if !strings.Contains(joined, "顺延") {
		t.Fatalf("deferral must be noted, got %v", res.Notes)
	}
}

// The night-owl pass touches ONLY black_cat: claim when reached, otherwise one
// event report and the shared progress poll.
func TestNightGrowthRunsBlackCatOnly(t *testing.T) {
	// Reached target → straight claim, no report.
	stub := &growthStub{tasks: []map[string]any{
		{"task_code": "black_cat", "target": 3, "current": 3, "accept_status": "accepted"},
		{"task_code": "create_canvas", "target": 1, "current": 0, "accept_status": "accepted"},
	}}
	installGrowthStub(t, stub)
	runNightGrowth("idx-1", cnGrowthAuth())
	if len(stub.claims) != 1 || stub.claims[0] != "black_cat" {
		t.Fatalf("reached black_cat must be claimed, got %v", stub.claims)
	}
	if len(stub.events) != 0 {
		t.Fatalf("reached black_cat must not report events, saw %d", len(stub.events))
	}

	// Below target (one night = one event; target 2, current 1) → exactly one
	// night event, then claim through the poll.
	stub = &growthStub{tasks: []map[string]any{
		{"task_code": "black_cat", "target": 2, "current": 1, "accept_status": "accepted"},
		{"task_code": "create_canvas", "target": 1, "current": 0, "accept_status": "accepted"},
	}}
	installGrowthStub(t, stub)
	runNightGrowth("idx-2", cnGrowthAuth())
	if len(stub.events) != 1 {
		t.Fatalf("night pass reports exactly one cat event, saw %d", len(stub.events))
	}
	if len(stub.claims) != 1 || stub.claims[0] != "black_cat" {
		t.Fatalf("black_cat must be claimed once progress settles, got %v", stub.claims)
	}
	// create_canvas must be untouched by the night pass.
	if stubNum(stub.findTask("create_canvas")["current"]) != 0 {
		t.Fatal("the night pass must not touch daily tasks")
	}

	// Still below target after the night event (target 3 over 3 days) → no
	// claim; the reward waits for a later night.
	stub = &growthStub{tasks: []map[string]any{
		{"task_code": "black_cat", "target": 3, "current": 1, "accept_status": "accepted"},
	}}
	installGrowthStub(t, stub)
	runNightGrowth("idx-3", cnGrowthAuth())
	if len(stub.events) != 1 {
		t.Fatalf("night pass reports one cat event, saw %d", len(stub.events))
	}
	if len(stub.claims) != 0 {
		t.Fatalf("black_cat below target must not be claimed, got %v", stub.claims)
	}
}

// The night-owl scheduler tick exists and sits between the evening ticks.
func TestNightGrowthHoursTick(t *testing.T) {
	if !shouldRunNightGrowthNow(time.Date(2026, 9, 25, 1, 30, 0, 0, time.Local)) {
		t.Fatal("01:30 must fall inside the night-owl tick window")
	}
	if shouldRunNightGrowthNow(time.Date(2026, 9, 25, 2, 30, 0, 0, time.Local)) {
		t.Fatal("02:30 is outside the night-owl tick window")
	}
}
