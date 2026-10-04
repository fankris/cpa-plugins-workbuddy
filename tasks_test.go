package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestDecodeGrowthTaskProgressAndClaimable(t *testing.T) {
	raw := json.RawMessage(`{"task_code":"chat:daily","title":"每日对话","progress":{"current":2,"target":5},"reward_credit":20,"accept_status":"accepted"}`)
	task := decodeGrowthTask(raw)
	if task.TaskCode != "chat:daily" || task.Current != 2 || task.Target != 5 {
		t.Fatalf("decoded task = %+v", task)
	}
	if task.Claimable || task.Claimed {
		t.Fatalf("incomplete task incorrectly claimable: %+v", task)
	}

	raw = json.RawMessage(`{"task_code":"chat_daily","current":5,"target":5,"status":"completed"}`)
	task = decodeGrowthTask(raw)
	if task.Claimed || !task.Claimable {
		t.Fatalf("completed task status wrong: %+v", task)
	}
}

func TestDecodeGrowthTasksWrapperAndArray(t *testing.T) {
	for _, raw := range []string{
		`{"tasks":[{"task_code":"a"}]}`,
		`[{"task_code":"b"}]`,
	} {
		tasks, err := decodeGrowthTasks(json.RawMessage(raw))
		if err != nil || len(tasks) != 1 {
			t.Fatalf("decode %s: tasks=%+v err=%v", raw, tasks, err)
		}
	}
}

func TestGrowthTaskCodeValidation(t *testing.T) {
	for _, code := range []string{"chat:daily", "task_code-1", "a.b"} {
		if err := validateGrowthTaskCode(code); err != nil {
			t.Errorf("valid code %q rejected: %v", code, err)
		}
	}
	for _, code := range []string{"", "a/b", "a b", strings.Repeat("x", 129)} {
		if err := validateGrowthTaskCode(code); err == nil {
			t.Errorf("invalid code %q accepted", code)
		}
	}
}

func TestGrowthTasksRegionGuard(t *testing.T) {
	if !growthTasksSupported(&storedAuth{Auth: storedTokens{Region: regionCN}}) {
		t.Fatal("CN account should support growth tasks")
	}
	for _, region := range []string{regionGlobal, regionIntl} {
		sa := &storedAuth{Auth: storedTokens{Region: region}}
		if growthTasksSupported(sa) {
			t.Fatalf("%s account should not support CN growth tasks", region)
		}
	}
}

func TestAcceptedGrowthTaskCodesSkipsCompletedLockedAndDeduplicates(t *testing.T) {
	got := acceptedGrowthTaskCodes([]growthTask{
		{TaskCode: "new"},
		{TaskCode: " new "},
		{TaskCode: "accepted", AcceptStatus: "accepted"},
		{TaskCode: "completed", AcceptStatus: "completed"},
		{TaskCode: "locked", Locked: true},
		{TaskCode: "claimed", Claimed: true},
	})
	if len(got) != 1 || got[0] != "new" {
		t.Fatalf("codes = %v", got)
	}
}

func TestNormalizeGrowthTaskCodesReportsUniqueAcceptedCount(t *testing.T) {
	got, err := normalizeGrowthTaskCodes([]string{"a", " a ", "b"})
	if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("codes=%v err=%v", got, err)
	}
}

func TestGrowthHeadersWebUsesExplicitDomain(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, growthWebBase+"/claim", nil)
	if err != nil {
		t.Fatal(err)
	}
	sa := &storedAuth{Auth: storedTokens{AccessToken: "token", Domain: "chat.example"}}
	growthHeaders(req, sa, true)
	if got := req.Header.Get("X-Domain"); got != growthTasksWebOrigin {
		t.Fatalf("X-Domain=%q", got)
	}
}

func TestSanitizeGrowthTaskURL(t *testing.T) {
	if link, text := sanitizeGrowthTaskURL("https://www.workbuddy.cn/activity/a"); link == "" || text != "" {
		t.Fatalf("known URL link=%q text=%q", link, text)
	}
	for _, raw := range []string{"http://www.workbuddy.cn/a", "javascript:alert(1)", "https://evil.example/a", "https://www.workbuddy.cn:443/a"} {
		if link, text := sanitizeGrowthTaskURL(raw); link != "" || text != raw {
			t.Fatalf("unsafe URL %q link=%q text=%q", raw, link, text)
		}
	}
}

func TestClaimGrowthTaskOnlyFallsBackOnHTTP400(t *testing.T) {
	old := growthJSONFunc
	defer func() { growthJSONFunc = old }()
	var calls []struct {
		base, path string
		web        bool
	}
	growthJSONFunc = func(_ *storedAuth, _ string, base, path string, _ any, web bool) (json.RawMessage, error) {
		calls = append(calls, struct {
			base, path string
			web        bool
		}{base, path, web})
		if !web {
			return nil, &growthHTTPError{StatusCode: http.StatusBadRequest}
		}
		return json.RawMessage(`{"credit":1}`), nil
	}
	result, err := claimGrowthTask(&storedAuth{Auth: storedTokens{Region: regionCN}}, "a")
	if err != nil || result["credit"] != int64(1) || len(calls) != 2 || !calls[1].web {
		t.Fatalf("result=%v err=%v calls=%v", result, err, calls)
	}
}

func TestClaimGrowthTaskDoesNotFallbackOnNon400(t *testing.T) {
	old := growthJSONFunc
	defer func() { growthJSONFunc = old }()
	calls := 0
	growthJSONFunc = func(_ *storedAuth, _ string, _ string, _ string, _ any, _ bool) (json.RawMessage, error) {
		calls++
		return nil, &growthHTTPError{StatusCode: http.StatusUnauthorized}
	}
	if _, err := claimGrowthTask(&storedAuth{Auth: storedTokens{Region: regionCN}}, "a"); err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestTaskRunRunningDoesNotExpireAndStatusChecksAuth(t *testing.T) {
	r := newTaskRun("auth-a")
	r.Mu.Lock()
	r.StartedAt = time.Now().Add(-2 * taskRunTTL)
	r.Mu.Unlock()
	got := taskRunSnapshot(r.ID)
	if got["error"] != nil || got["status"] != "running" {
		t.Fatalf("running snapshot=%v", got)
	}
	mismatch := handleGrowthTaskStatus(pluginapi.ManagementRequest{
		Query: url.Values{"run_id": []string{r.ID}, "auth_index": []string{"auth-b"}},
	})
	if mismatch["error"] == nil {
		t.Fatalf("auth mismatch should fail: %v", mismatch)
	}
	taskRuns.Delete(r.ID)
	taskRunsByAuth.Delete(r.AuthIndex)
}

func TestClaimAlreadyClaimedError(t *testing.T) {
	if !claimAlreadyClaimedError(&growthAPIError{Msg: "already claimed"}) {
		t.Fatal("expected already claimed")
	}
	if claimAlreadyClaimedError(errors.New("other")) {
		t.Fatal("unexpected already claimed")
	}
}

func TestTaskRunSnapshotMissing(t *testing.T) {
	got := taskRunSnapshot("missing-run")
	if got["error"] == nil {
		t.Fatalf("missing run should report error: %+v", got)
	}
}
