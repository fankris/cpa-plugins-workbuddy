package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const taskAcceptBatchSize = 20

var taskAcceptBatchGap = 1050 * time.Millisecond

const taskRunTTL = 15 * time.Minute

var taskRunSeq uint64
var taskRuns sync.Map       // run_id -> *growthTaskRun
var taskRunsByAuth sync.Map // auth_index -> *growthTaskRun
var taskRunGate sync.Mutex

type growthTaskRun struct {
	ID              string   `json:"run_id"`
	AuthIndex       string   `json:"auth_index"`
	Status          string   `json:"status"`
	Accepted        int      `json:"accepted"`
	Failed          []string `json:"failed,omitempty"`
	Error           string   `json:"error,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	CancelRequested bool     `json:"cancel_requested"`
	// Light carries the /tasks/light summary (lit/claimed/desktop-only) once
	// the lighting pass finishes; nil for accept-only runs.
	Light      *growthLightResult `json:"light,omitempty"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at,omitempty"`
	ExpiresAt  time.Time          `json:"-"`
	Mu         sync.RWMutex       `json:"-"`
}

func newTaskRun(authIndex string) *growthTaskRun {
	id := fmt.Sprintf("task-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&taskRunSeq, 1))
	r := &growthTaskRun{ID: id, AuthIndex: authIndex, Status: "running", StartedAt: time.Now().UTC()}
	taskRuns.Store(id, r)
	taskRunsByAuth.Store(authIndex, r)
	return r
}

func pruneTaskRuns(now time.Time) {
	taskRuns.Range(func(key, value any) bool {
		r, ok := value.(*growthTaskRun)
		if !ok {
			taskRuns.Delete(key)
			return true
		}
		r.Mu.RLock()
		status, expiresAt, authIndex := r.Status, r.ExpiresAt, r.AuthIndex
		r.Mu.RUnlock()
		if !expiresAt.IsZero() && status != "running" && now.After(expiresAt) {
			taskRuns.Delete(key)
			if current, ok := taskRunsByAuth.Load(authIndex); ok && current == r {
				taskRunsByAuth.Delete(authIndex)
			}
		} else if status != "running" {
			if current, ok := taskRunsByAuth.Load(authIndex); ok && current == r {
				taskRunsByAuth.Store(authIndex, r)
			}
		}
		return true
	})
}

func taskRunSnapshot(id string) map[string]any {
	pruneTaskRuns(time.Now())
	v, ok := taskRuns.Load(id)
	if !ok {
		return map[string]any{"error": "run_id 不存在或已过期", "run_id": id}
	}
	r := v.(*growthTaskRun)
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	if r.Status == "running" || r.ExpiresAt.IsZero() || time.Now().Before(r.ExpiresAt) {
		result := map[string]any{"run_id": r.ID, "auth_index": r.AuthIndex, "status": r.Status, "accepted": r.Accepted, "failed": append([]string(nil), r.Failed...), "started_at": r.StartedAt, "finished_at": r.FinishedAt, "kind": r.Kind, "cancel_requested": r.CancelRequested}
		if r.Error != "" {
			result["error"] = r.Error
		}
		return result
	}
	return map[string]any{"error": "run_id 不存在或已过期", "run_id": id}
}

func finishTaskRun(r *growthTaskRun, status, errText string) {
	r.Mu.Lock()
	r.Status = status
	r.Error = errText
	r.FinishedAt = time.Now().UTC()
	r.ExpiresAt = time.Now().Add(taskRunTTL)
	r.Mu.Unlock()
}

func tryNewTaskRun(authIndex string) (*growthTaskRun, bool) {
	authIndex = strings.TrimSpace(authIndex)
	taskRunGate.Lock()
	defer taskRunGate.Unlock()
	pruneTaskRuns(time.Now())
	if previous, ok := taskRunsByAuth.Load(authIndex); ok {
		r, valid := previous.(*growthTaskRun)
		if valid {
			r.Mu.RLock()
			running := r.Status == "running"
			r.Mu.RUnlock()
			if running {
				return r, false
			}
		}
		taskRunsByAuth.Delete(authIndex)
	}
	return newTaskRun(authIndex), true
}

func runAcceptAll(authIndex string, sa *storedAuth, r *growthTaskRun) {
	if !pluginWorkerStart() {
		finishTaskRun(r, "canceled", errPluginQuiescing.Error())
		return
	}
	defer pluginWorkerDone()
	ctx := pluginContext()
	defer func() {
		if rec := recover(); rec != nil {
			finishTaskRun(r, "failed", "任务运行异常")
		}
	}()
	lock := growthTaskLockFor(authIndex)
	lock.Lock()
	defer lock.Unlock()
	if taskRunCanceled(r) {
		finishTaskRun(r, "canceled", "")
		return
	}
	tasks, err := listGrowthTasks(sa)
	if err != nil {
		finishTaskRun(r, "failed", safeManagementError(err))
		return
	}
	codes := acceptedGrowthTaskCodes(tasks)
	failed := make([]string, 0)
	for start := 0; start < len(codes); start += taskAcceptBatchSize {
		if taskRunCanceled(r) {
			finishTaskRun(r, "canceled", "")
			return
		}
		select {
		case <-ctx.Done():
			finishTaskRun(r, "canceled", errPluginQuiescing.Error())
			return
		default:
		}
		end := start + taskAcceptBatchSize
		if end > len(codes) {
			end = len(codes)
		}
		if err := acceptGrowthTasks(sa, codes[start:end]); err != nil {
			failed = append(failed, codes[start:end]...)
			r.Mu.Lock()
			r.Failed = append([]string(nil), failed...)
			r.Mu.Unlock()
		} else {
			r.Mu.Lock()
			r.Accepted += end - start
			r.Mu.Unlock()
		}
		if end < len(codes) && taskAcceptBatchGap > 0 {
			timer := time.NewTimer(taskAcceptBatchGap)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				finishTaskRun(r, "canceled", errPluginQuiescing.Error())
				return
			case <-timer.C:
			}
		}
	}
	r.Mu.Lock()
	r.Failed = failed
	status := "succeeded"
	errText := ""
	if len(failed) > 0 {
		status = "failed"
		errText = "部分任务接受失败，可重试"
	}
	r.Mu.Unlock()
	finishTaskRun(r, status, errText)
}

func taskAuthIndex(req pluginapi.ManagementRequest) string {
	var body struct {
		AuthIndex string `json:"auth_index"`
	}
	_ = jsonUnmarshalLimit(req.Body, &body)
	if strings.TrimSpace(body.AuthIndex) != "" {
		return strings.TrimSpace(body.AuthIndex)
	}
	if values := req.Query["auth_index"]; len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	return ""
}

func handleGrowthTaskList(req pluginapi.ManagementRequest) map[string]any {
	idx := taskAuthIndex(req)
	sa, err := resolveGrowthAuth(idx)
	if err != nil {
		return businessErrorResult(idx, err)
	}
	tasks, err := listGrowthTasks(sa)
	if err != nil {
		return map[string]any{"auth_index": idx, "nickname": sa.Account.Nickname, "region": accountRegion(sa), "error": safeManagementError(err)}
	}
	return map[string]any{"auth_index": idx, "nickname": sa.Account.Nickname, "region": accountRegion(sa), "tasks": tasks}
}

func handleGrowthTaskAccept(req pluginapi.ManagementRequest) map[string]any {
	idx := taskAuthIndex(req)
	var body struct {
		TaskCodes []string `json:"task_codes"`
		TaskCode  string   `json:"task_code"`
	}
	if err := jsonUnmarshalLimit(req.Body, &body); err != nil {
		return businessErrorResult(idx, err)
	}
	if len(body.TaskCodes) == 0 && strings.TrimSpace(body.TaskCode) != "" {
		body.TaskCodes = []string{body.TaskCode}
	}
	if len(body.TaskCodes) == 0 {
		return map[string]any{"auth_index": idx, "error": "task_codes 是必填项", "code": "invalid_request"}
	}
	cleanCodes, err := normalizeGrowthTaskCodes(body.TaskCodes)
	if err != nil {
		return businessErrorResult(idx, err)
	}
	sa, err := resolveGrowthAuth(idx)
	if err != nil {
		return businessErrorResult(idx, err)
	}
	lock := growthTaskLockFor(idx)
	lock.Lock()
	defer lock.Unlock()
	if err := acceptGrowthTasks(sa, cleanCodes); err != nil {
		return businessErrorResult(idx, err)
	}
	return map[string]any{"ok": true, "auth_index": idx, "accepted": len(cleanCodes)}
}

func handleGrowthTaskAcceptAll(req pluginapi.ManagementRequest) map[string]any {
	idx := taskAuthIndex(req)
	sa, err := resolveGrowthAuth(idx)
	if err != nil {
		return businessErrorResult(idx, err)
	}
	if pluginQuiescing() {
		return map[string]any{"ok": false, "auth_index": idx, "busy": true, "error": errPluginQuiescing.Error()}
	}
	r, created := tryNewTaskRun(idx)
	if !created {
		r.Mu.RLock()
		status := r.Status
		r.Mu.RUnlock()
		return map[string]any{"ok": false, "auth_index": idx, "run_id": r.ID, "status": status, "busy": true, "error": "该账号已有任务运行中"}
	}
	r.Mu.Lock()
	r.Kind = "accept"
	r.Mu.Unlock()
	go runAcceptAll(idx, sa, r)
	return map[string]any{"ok": true, "auth_index": idx, "run_id": r.ID, "status": "running", "kind": "accept"}
}

func handleGrowthTaskStatus(req pluginapi.ManagementRequest) map[string]any {
	id := ""
	if values := req.Query["run_id"]; len(values) > 0 {
		id = strings.TrimSpace(values[0])
	}
	var body struct {
		RunID     string `json:"run_id"`
		AuthIndex string `json:"auth_index"`
	}
	if len(req.Body) > 0 {
		_ = jsonUnmarshalLimit(req.Body, &body)
	}
	if id == "" {
		id = strings.TrimSpace(body.RunID)
	}
	requestedAuth := strings.TrimSpace(body.AuthIndex)
	if values := req.Query["auth_index"]; requestedAuth == "" && len(values) > 0 {
		requestedAuth = strings.TrimSpace(values[0])
	}
	if id == "" && requestedAuth != "" {
		pruneTaskRuns(time.Now())
		if current, ok := taskRunsByAuth.Load(requestedAuth); ok {
			if run, ok := current.(*growthTaskRun); ok {
				run.Mu.RLock()
				id = run.ID
				run.Mu.RUnlock()
			}
		}
		if id == "" {
			return map[string]any{"auth_index": requestedAuth, "status": "idle"}
		}
	}
	result := taskRunSnapshot(id)
	if result["error"] != nil {
		return result
	}

	if requestedAuth != "" {
		actual, _ := result["auth_index"].(string)
		if actual != requestedAuth {
			return map[string]any{"run_id": id, "error": "auth_index 与 run_id 不匹配"}
		}
	}
	return result
}

func handleGrowthTaskClaim(req pluginapi.ManagementRequest) map[string]any {
	idx := taskAuthIndex(req)
	var body struct {
		TaskCode string `json:"task_code"`
	}
	if err := jsonUnmarshalLimit(req.Body, &body); err != nil {
		return businessErrorResult(idx, err)
	}
	code := strings.TrimSpace(body.TaskCode)
	if code == "" {
		return map[string]any{"auth_index": idx, "error": "task_code 是必填项", "code": "invalid_request"}
	}
	sa, err := resolveGrowthAuth(idx)
	if err != nil {
		return businessErrorResult(idx, err)
	}
	lock := growthTaskLockFor(idx)
	lock.Lock()
	defer lock.Unlock()
	result, err := claimGrowthTask(sa, code)
	if err != nil {
		r := businessErrorResult(idx, err)
		r["task_code"] = code
		return r
	}
	result["ok"] = true
	result["auth_index"] = idx
	result["nickname"] = sa.Account.Nickname
	return result
}

// jsonUnmarshalLimit prevents malformed management payloads from causing a
// second large allocation inside individual task handlers.
func jsonUnmarshalLimit(raw []byte, dst any) error {
	if len(raw) > managementBodyLimit {
		return fmt.Errorf("管理请求体过大")
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return &businessRequestError{"invalid_request", "invalid request JSON"}
	}
	return nil
}

func safeManagementError(err error) string {
	if err == nil {
		return ""
	}
	return truncateRedacted(err.Error(), 240)
}

func taskHTTPStatus(result map[string]any) int {
	if result["code"] == "unsupported_region" {
		return http.StatusUnprocessableEntity
	}
	if result["code"] == "invalid_request" {
		return http.StatusBadRequest
	}
	if result["code"] == "not_found" {
		return http.StatusNotFound
	}
	if result != nil {
		if _, ok := result["error"]; ok {
			if busy, _ := result["busy"].(bool); busy {
				return http.StatusConflict
			}
			if _, mismatch := result["run_id"]; mismatch && result["auth_index"] == nil {
				return http.StatusNotFound
			}
			return http.StatusBadGateway
		}
	}
	return http.StatusOK
}

// handleGrowthTaskLight runs the full accept → light → claim cycle for one
// account in the background, reusing the task-run bookkeeping so the panel can
// poll /tasks/status exactly like the accept_all flow.
func handleGrowthTaskLight(req pluginapi.ManagementRequest) map[string]any {
	idx := taskAuthIndex(req)
	sa, err := resolveGrowthAuth(idx)
	if err != nil {
		return businessErrorResult(idx, err)
	}
	if pluginQuiescing() {
		return map[string]any{"ok": false, "auth_index": idx, "busy": true, "error": errPluginQuiescing.Error()}
	}
	r, created := tryNewTaskRun(idx)
	if !created {
		r.Mu.RLock()
		status := r.Status
		r.Mu.RUnlock()
		return map[string]any{"ok": false, "auth_index": idx, "run_id": r.ID, "status": status, "busy": true, "error": "该账号已有任务运行中"}
	}
	go runLightTasks(idx, sa, r)
	return map[string]any{"ok": true, "auth_index": idx, "run_id": r.ID, "status": r.Status}
}

// runLightTasks is the background worker behind /tasks/light.
func runLightTasks(authIndex string, sa *storedAuth, r *growthTaskRun) {
	if !pluginWorkerStart() {
		finishTaskRun(r, "canceled", errPluginQuiescing.Error())
		return
	}
	defer pluginWorkerDone()
	ctx := pluginContext()
	defer func() {
		if rec := recover(); rec != nil {
			finishTaskRun(r, "failed", "任务运行异常")
		}
	}()
	lock := growthTaskLockFor(authIndex)
	lock.Lock()
	defer lock.Unlock()

	result := lightGrowthTasks(sa, r, taskAcceptBatchGap, ctx.Done())
	// Fold the lighting summary into the run record so /tasks/status and the
	// panel can show what was lit / claimed / skipped.
	r.Mu.Lock()
	r.Light = &result
	r.Mu.Unlock()
	if len(result.Failed) > 0 && len(result.Lit) == 0 && len(result.Claimed) == 0 {
		finishTaskRun(r, "failed", "任务点亮失败: "+strings.Join(result.Failed, ", "))
		return
	}
	finishTaskRun(r, "done", "")
}

// handleGrowthTravel runs one buddy-travel cycle synchronously (it is a single
// short request pair; no background run is needed).
func handleGrowthTravel(req pluginapi.ManagementRequest) map[string]any {
	idx := taskAuthIndex(req)
	sa, err := resolveGrowthAuth(idx)
	if err != nil {
		return businessErrorResult(idx, err)
	}
	lock := growthTaskLockFor(idx)
	lock.Lock()
	defer lock.Unlock()
	result := runBuddyTravel(sa)
	result["auth_index"] = idx
	result["nickname"] = sa.Account.Nickname
	return result
}

// Cancellation is cooperative, only for legitimate accept-only runs. It never
// reports that an in-flight upstream action was rolled back.
func taskRunCanceled(r *growthTaskRun) bool {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	return r.CancelRequested
}
func handleGrowthTaskCancel(req pluginapi.ManagementRequest) map[string]any {
	var body struct {
		RunID     string `json:"run_id"`
		AuthIndex string `json:"auth_index"`
	}
	if err := jsonUnmarshalLimit(req.Body, &body); err != nil {
		return map[string]any{"error": "invalid cancellation request"}
	}
	value, ok := taskRuns.Load(body.RunID)
	if !ok {
		return map[string]any{"error": "run_id not found"}
	}
	r, ok := value.(*growthTaskRun)
	if !ok {
		return map[string]any{"error": "invalid run"}
	}
	r.Mu.Lock()
	defer r.Mu.Unlock()
	if body.AuthIndex == "" || body.AuthIndex != r.AuthIndex {
		return map[string]any{"error": "auth_index mismatch"}
	}
	if r.Kind != "accept" {
		return map[string]any{"error": "cancellation unsupported for this operation"}
	}
	if r.Status == "running" {
		r.CancelRequested = true
	}
	return map[string]any{"run_id": r.ID, "auth_index": r.AuthIndex, "status": r.Status, "cancel_requested": r.CancelRequested}
}
