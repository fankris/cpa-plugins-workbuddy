package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Growth task support is intentionally kept inside the CPA plugin. Account
// credentials and transport still come from CPA host.auth/host.http; this file
// does not create a second account pool or HTTP server.
const (
	growthTasksPath       = "/v2/activity/growth/tasks"
	growthTasksAcceptPath = "/v2/activity/growth/tasks/accept"
	growthTasksWebOrigin  = "https://www.workbuddy.cn"
	growthTasksMaxBody    = 1 << 20
)

var growthWebBase = growthTasksWebOrigin

// growthJSONFunc is a narrow test seam for claim routing. Production keeps the
// normal CPA host HTTP bridge path through growthJSON.
var growthJSONFunc = growthJSON

type growthHTTPError struct {
	StatusCode int
	Body       string
}

func (e *growthHTTPError) Error() string {
	return fmt.Sprintf("成长任务接口 HTTP %d: %s", e.StatusCode, e.Body)
}

type growthAPIError struct {
	Code int
	Msg  string
	Data json.RawMessage
}

func (e *growthAPIError) Error() string {
	return fmt.Sprintf("成长任务接口 code=%d: %s", e.Code, truncateRedacted(e.Msg, 160))
}

var taskCodePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

// growthTask is the CPA panel's stable view of one upstream growth task.
type growthTask struct {
	TaskCode     string `json:"task_code"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	TaskDesc     string `json:"task_desc,omitempty"`
	Credit       int64  `json:"credit,omitempty"`
	Energy       int64  `json:"energy,omitempty"`
	HasReward    bool   `json:"has_reward,omitempty"`
	RewardBuddy  bool   `json:"reward_buddy,omitempty"`
	TaskType     string `json:"task_type,omitempty"`
	Tag          string `json:"tag,omitempty"`
	JumpURL      string `json:"jump_url,omitempty"`
	JumpURLText  string `json:"jump_url_text,omitempty"`
	Locked       bool   `json:"locked,omitempty"`
	Target       int64  `json:"target"`
	Current      int64  `json:"current"`
	AcceptStatus string `json:"accept_status,omitempty"`
	Status       string `json:"status,omitempty"`
	Claimable    bool   `json:"claimable,omitempty"`
	Claimed      bool   `json:"claimed,omitempty"`
}

type growthTaskResult struct {
	AuthIndex string       `json:"auth_index"`
	Nickname  string       `json:"nickname,omitempty"`
	Region    string       `json:"region,omitempty"`
	Tasks     []growthTask `json:"tasks,omitempty"`
}

// Growth tasks are currently verified only on the CN growth service. Do not
// send CN activity requests with Global or Intl credentials by accident.
func growthTasksSupported(sa *storedAuth) bool {
	return supportsBusiness(sa, "tasks")
}

func growthUnsupportedError(sa *storedAuth) error { return &businessCapabilityError{Feature: "tasks"} }

func validateGrowthTaskCode(code string) error {
	code = strings.TrimSpace(code)
	if !taskCodePattern.MatchString(code) {
		return &businessRequestError{"invalid_request", "无效任务编号"}
	}
	return nil
}

var knownGrowthTaskHosts = map[string]struct{}{
	"workbuddy.cn":     {},
	"www.workbuddy.cn": {},
	"workbuddy.ai":     {},
	"www.workbuddy.ai": {},
}

// sanitizeGrowthTaskURL returns a link only for an HTTPS URL on a known
// WorkBuddy host. Invalid or unrelated URLs are retained as display text so
// the panel can show the upstream value without making it executable.
func sanitizeGrowthTaskURL(raw string) (link, text string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Port() != "" {
		return "", raw
	}
	if _, ok := knownGrowthTaskHosts[strings.ToLower(u.Hostname())]; !ok {
		return "", raw
	}
	return u.String(), ""
}

func growthHeaders(req *http.Request, sa *storedAuth, web bool) {
	if web {
		req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", growthTasksWebOrigin)
		req.Header.Set("Referer", growthTasksWebOrigin+"/profile/growth-center")
		req.Header.Set("x-client-platform", "web")
		req.Header.Set("User-Agent", clientUA)
		// The web claim endpoint expects the full web origin here. Never inherit
		// the credential's X-Domain, which may point at the chat realm.
		req.Header.Set("X-Domain", growthTasksWebOrigin)
	} else {
		// Existing-account task requests need Bearer authentication too,
		// not just the common Origin/User-Agent headers.
		authHeadersFor(req, sa, false)
		req.Header.Set("X-Product", "SaaS")
	}
	if sa.Account.UID != "" {
		req.Header.Set("X-User-Id", sa.Account.UID)
		// Stable device identity on growth calls too: the growth center
		// correlates events per machine, and a missing machine ID makes
		// forged-behaviour events look like they came from nowhere.
		applyFingerprintHeaders(sa.Account.UID, req.Header.Set)
	}
	if sa.Account.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", sa.Account.EnterpriseID)
		req.Header.Set("X-Tenant-Id", sa.Account.EnterpriseID)
	}
	if !web && sa.Auth.Domain != "" {
		req.Header.Set("X-Domain", sa.Auth.Domain)
	}
	if !web {
		applyRealmHeaders(req, sa)
	}
}

func growthJSON(sa *storedAuth, method, base, path string, body any, web bool) (json.RawMessage, error) {
	if !growthTasksSupported(sa) {
		return nil, growthUnsupportedError(sa)
	}
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(pluginContext(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	growthHeaders(req, sa, web)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if len(resp.Body) > growthTasksMaxBody {
		return nil, fmt.Errorf("成长任务响应过大")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &growthHTTPError{StatusCode: resp.StatusCode, Body: truncateRedacted(string(resp.Body), 200)}
	}
	var env apiEnvelope
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("成长任务响应解析失败: %s", truncateRedacted(err.Error(), 120))
	}
	if env.Code != 0 {
		return nil, &growthAPIError{Code: env.Code, Msg: env.Msg, Data: env.Data}
	}
	return env.Data, nil
}

func decodeGrowthTask(raw json.RawMessage) growthTask {
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	getStr := func(keys ...string) string {
		for _, key := range keys {
			if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	getI64 := func(keys ...string) int64 {
		for _, key := range keys {
			if v := jsonI64(obj, key); v != 0 {
				return v
			}
		}
		return 0
	}
	getBool := func(keys ...string) bool {
		for _, key := range keys {
			if jsonBool(obj, key) {
				return true
			}
		}
		return false
	}
	current := getI64("current", "Current")
	target := getI64("target", "Target")
	if progress, ok := obj["progress"]; ok {
		if p, ok := progress.(map[string]any); ok {
			if v := jsonI64(p, "current", "Current"); v != 0 || current == 0 {
				current = v
			}
			if v := jsonI64(p, "target", "Target"); v != 0 || target == 0 {
				target = v
			}
		}
	}
	accept := strings.ToLower(getStr("accept_status", "acceptStatus"))
	status := strings.ToLower(getStr("status", "task_status", "taskStatus"))
	claimed := accept == "claimed" || status == "claimed"
	locked := getBool("locked", "isLocked")
	return growthTask{
		TaskCode:     getStr("task_code", "taskCode", "code"),
		Title:        getStr("title", "name"),
		Description:  getStr("description"),
		TaskDesc:     getStr("task_desc", "taskDesc"),
		Credit:       getI64("reward_credit", "rewardCredit", "credit"),
		Energy:       getI64("reward_energy", "rewardEnergy", "energy"),
		HasReward:    getBool("has_reward", "hasReward"),
		RewardBuddy:  getBool("reward_buddy", "rewardBuddy"),
		TaskType:     getStr("task_type", "taskType"),
		Tag:          getStr("tag"),
		JumpURL:      func() string { link, _ := sanitizeGrowthTaskURL(getStr("jump_url", "jumpURL")); return link }(),
		JumpURLText:  func() string { _, text := sanitizeGrowthTaskURL(getStr("jump_url", "jumpURL")); return text }(),
		Locked:       locked,
		Target:       target,
		Current:      current,
		AcceptStatus: accept,
		Status:       status,
		// A locked task is not claimable even when its progress is at target:
		// the panel already hides the claim button for locked rows, and
		// auto-lighting must not claim what the user has not unlocked.
		Claimable: !claimed && !locked && target > 0 && current >= target,
		Claimed:   claimed,
	}
}

func decodeGrowthTasks(data json.RawMessage) ([]growthTask, error) {
	var wrapper struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Tasks != nil {
		out := make([]growthTask, 0, len(wrapper.Tasks))
		for _, raw := range wrapper.Tasks {
			out = append(out, decodeGrowthTask(raw))
		}
		return out, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("任务列表格式无效")
	}
	out := make([]growthTask, 0, len(raws))
	for _, raw := range raws {
		out = append(out, decodeGrowthTask(raw))
	}
	return out, nil
}

func listGrowthTasks(sa *storedAuth) ([]growthTask, error) {
	data, err := growthJSON(sa, http.MethodGet, upstreamBaseFor(sa), growthTasksPath, nil, false)
	if err != nil {
		return nil, err
	}
	return decodeGrowthTasks(data)
}

func normalizeGrowthTaskCodes(codes []string) ([]string, error) {
	clean := make([]string, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if err := validateGrowthTaskCode(code); err != nil {
			return nil, err
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		clean = append(clean, code)
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("至少需要一个任务编号")
	}
	return clean, nil
}

func acceptGrowthTasks(sa *storedAuth, codes []string) error {
	clean, err := normalizeGrowthTaskCodes(codes)
	if err != nil {
		return err
	}
	_, err = growthJSON(sa, http.MethodPost, upstreamBaseFor(sa), growthTasksAcceptPath, map[string]any{"task_codes": clean}, false)
	return err
}

func decodeGrowthClaim(data json.RawMessage, code string) (map[string]any, error) {
	var result struct {
		AlreadyClaimed bool   `json:"already_claimed"`
		Credit         int64  `json:"credit"`
		Energy         int64  `json:"energy"`
		Message        string `json:"message"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("领奖响应解析失败")
		}
	}
	return map[string]any{
		"task_code":       code,
		"already_claimed": result.AlreadyClaimed,
		"credit":          result.Credit,
		"energy":          result.Energy,
		"message":         result.Message,
	}, nil
}

func claimAlreadyClaimedError(err error) bool {
	var apiErr *growthAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	msg := strings.ToLower(apiErr.Msg)
	return strings.Contains(msg, "already") && strings.Contains(msg, "claim") || strings.Contains(msg, "已领取") || strings.Contains(msg, "已领取过")
}

func claimGrowthTask(sa *storedAuth, code string) (map[string]any, error) {
	code = strings.TrimSpace(code)
	if err := validateGrowthTaskCode(code); err != nil {
		return nil, err
	}
	escaped := url.PathEscape(code)
	// Prefer the authenticated chat realm. The web endpoint is a compatibility
	// fallback only for an actual HTTP 400 from that realm.
	data, err := growthJSONFunc(sa, http.MethodPost, upstreamBaseFor(sa), growthTasksPath+"/"+escaped+"/claim", nil, false)
	if err != nil {
		var httpErr *growthHTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusBadRequest {
			data, err = growthJSONFunc(sa, http.MethodPost, growthWebBase, "/activity/growth/tasks/"+escaped+"/claim", nil, true)
		}
	}
	if err != nil {
		if claimAlreadyClaimedError(err) {
			return map[string]any{"task_code": code, "already_claimed": true}, nil
		}
		return nil, err
	}
	return decodeGrowthClaim(data, code)
}

var growthTaskLocks sync.Map // auth_index -> *sync.Mutex

func growthTaskLockFor(authIndex string) *sync.Mutex {
	v, _ := growthTaskLocks.LoadOrStore(authIndex, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func resolveGrowthAuth(authIndex string) (*storedAuth, error) {
	if strings.TrimSpace(authIndex) == "" {
		return nil, &businessRequestError{"invalid_request", "auth_index 是必填项"}
	}
	files, err := hostAuthList()
	if err != nil {
		return nil, fmt.Errorf("读取账号失败: %w", err)
	}
	for _, f := range files {
		if f.AuthIndex == authIndex {
			sa, err := hostAuthGet(authIndex)
			if err != nil {
				return nil, fmt.Errorf("读取账号凭证失败: %w", err)
			}
			if !growthTasksSupported(sa) {
				return nil, growthUnsupportedError(sa)
			}
			return sa, nil
		}
	}
	return nil, &businessRequestError{"not_found", "账号不存在"}
}

func acceptedGrowthTaskCodes(tasks []growthTask) []string {
	codes := make([]string, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		code := strings.TrimSpace(task.TaskCode)
		if code == "" || task.Claimed || task.Locked {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(task.AcceptStatus))
		if status == "accepted" || status == "completed" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes
}
