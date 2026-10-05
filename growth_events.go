// growth_events.go implements growth-task event lighting ("点亮"): the desktop
// client advances task progress by reporting structured behaviour events to
// POST /v2/report, not by calling any task endpoint. Accept/claim alone leaves
// every task at 0/target forever.
//
// Protocol knowledge adapted from workbuddy2api-hub (ardeyouxipianyi, MIT) and
// Sliverkiss/workbuddy2api, re-verified against the endpoints this plugin
// already talks to. Key upstream behaviours the engine honours:
//
//   - Expert/team events are deduplicated by (eventCode, id): repeating one id
//     never advances progress, so each report must carry a different id.
//   - Some tasks only accept genuine desktop actions (deep links workbuddy://)
//     and silently ignore forged events; those are reported as "desktop only"
//     instead of being faked.
//   - black_cat (夜猫子) only counts between 23:00 and 08:00 local time.
//   - Reporting is rate-sensitive: every event is spaced by at least 1s.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const growthReportPath = "/v2/report"

// Progress-poll budget for the post-report claim gate. Mirrors the cadence the
// reference measured: two quick checks, then keep polling while the counter
// moves (reports settle in 1-3s; a stuck counter is abandoned quickly so one
// dead task cannot stall a whole run).
const (
	progressPollAttempts     = 6
	progressPollFastAttempts = 2
	progressPollGap          = 1500 * time.Millisecond
	progressPollMovingGap    = 2500 * time.Millisecond
)

// growthEventKinds maps a task code to the event family that lights it.
// Codes come from the upstream task list; unknown codes are skipped rather
// than reported with a guessed event.
var growthEventKinds = map[string]string{
	"create_canvas":       "canvas",
	"template_5":          "template",
	"expert_5":            "expert",
	"Expert_team_use_3":   "team",
	"skill_1":             "skill",
	"automation_1":        "automation",
	"playbook_prompt":     "playbook",
	"Expert_lighthouse":   "lighthouse",
	"Hp_Appearance":       "skin",
	"chat_5":              "chat",
	"Model_chat_GLM5.2":   "glmchat",
	"black_cat":           "cat",
	"Buddy_App":           "desktop_only",
	"Buddy_App_QQ":        "desktop_only",
	"RichMeow_Chat":       "desktop_only",
	"Library_read":        "desktop_only",
	"Expert_Philanthropy": "desktop_only",
	"first_buddy":         "skip",
}

// desktopOnlyTasks explains why a task cannot be lit programmatically; the
// panel surfaces the deep link so the user can finish it in the client.
var desktopOnlyTasks = map[string]string{
	"Buddy_App":           "在桌面端「发现应用」进入任意一个 Buddy 应用",
	"Buddy_App_QQ":        "在桌面端「发现应用」进入「企鹅教师助手」",
	"RichMeow_Chat":       "在桌面端发起 1 次对话",
	"Library_read":        "在桌面端打开「资料库」并读完介绍文档",
	"Expert_Philanthropy": "需要真实捐款动作，无法自动完成",
}

// expertIDPool / teamIDPool supply distinct ids for expert/team events. The
// upstream dedupes by (eventCode, id), so rotating through the pool is what
// actually advances progress across multiple reports.
var expertIDPool = []struct{ ID, Name string }{
	{"ex_PZw8Gu81HfN4", "运维工程师"},
	{"ex_ROsDtJbzADFV", "产品经理"},
	{"ex_SMUnl0nJbPix", "UI设计师"},
	{"ex_ZTR062oVBOCW", "数据分析师"},
	{"ex_a3sSSFBy8qaC", "后端架构师"},
	{"ex_aG1kvKbq8lPx", "文案策划"},
	{"ex_al1vxtUOYQ10", "测试专家"},
	{"ex_cZfiyuET9UQP", "安全顾问"},
}

var teamIDPool = []struct{ ID, Name string }{
	{"CloudOpsTeam", "运维专家团队"},
	{"CloudContentTeam", "内容专家团队"},
	{"CloudDevTeam", "研发专家团队"},
	{"ProductStrategyTeam", "产品战略团队"},
	{"MarketingCampaignTeam", "营销活动团队"},
}

// nightTaskCodes only count during the 23:00–08:00 window.
var nightTaskCodes = map[string]struct{}{blackCatTaskCode: {}}

// blackCatTaskCode is the 夜猫子 task: one night chat event per day, three days
// to target. The 01:00 scheduler tick exists solely for it.
const blackCatTaskCode = "black_cat"

func inNightWindow(t time.Time) bool {
	h := t.Hour()
	return h >= 23 || h < 8
}

// growthEventKindFor resolves the event family for a task code.
func growthEventKindFor(code string) string {
	if kind, ok := growthEventKinds[strings.TrimSpace(code)]; ok {
		return kind
	}
	return ""
}

// buildGrowthEvent constructs one behaviour event payload for the given task
// kind. idx rotates ids so repeated reports advance progress.
func buildGrowthEvent(kind string, uid string, idx int, sa *storedAuth) map[string]any {
	now := time.Now().UnixMilli()
	cid := fmt.Sprintf("wb-task-%d-%d", now, idx)
	rid := cid + "-req"
	event := map[string]any{
		"timestamp":      now,
		"reportDelay":    0,
		"conversationId": cid,
		"requestId":      rid,
		"userId":         uid,
	}

	switch kind {
	case "canvas":
		event["eventCode"] = "wbx_design_canvas_task_create"
		event["source"] = "summon_keyword"
		event["isCustomModel"] = false
		event["name"] = ""
		event["inputLength"] = 12
		event["id"] = fmt.Sprintf("wbx-canvas-%d", now)
		event["cost"] = 0
		event["isSuccessful"] = true
	case "template":
		event["eventCode"] = "agent_task_created_with_template"
		event["isCustomModel"] = true
		event["id"] = fmt.Sprintf("%d", idx)
		event["name"] = "幻灯片"
	case "expert", "team", "lighthouse":
		event["eventCode"] = "expert_actual_use"
		event["mode"] = "CLOUD"
		var id, name, expertType string
		switch kind {
		case "team":
			pick := teamIDPool[idx%len(teamIDPool)]
			id, name, expertType = pick.ID, pick.Name, "team"
		case "lighthouse":
			id, name, expertType = "ex_2cvvUZQhDyeJ", "腾讯轻量云专家", "agent"
		default:
			pick := expertIDPool[idx%len(expertIDPool)]
			id, name, expertType = pick.ID, pick.Name, "agent"
		}
		event["id"] = id
		event["name"] = name
		event["expertTitle"] = name
		event["type"] = "02-Engineering"
		event["expertType"] = expertType
		event["source"] = "builtin"
		event["version"] = "1.0.2"
		event["cost"] = 0
		event["characterCount"] = 12
		event["messageId"] = rid
		event["requestModelId"] = "deepseek-v4-flash"
		event["requestModelName"] = "DeepSeek V4 Flash"
	case "skill":
		event["eventCode"] = "skill_info"
		event["skillId"] = "skill_2096525080079265792"
		event["name"] = "pptx"
	case "automation":
		event["eventCode"] = "automated_task_create_suc"
		event["name"] = "每周工作整理"
		event["type"] = "cron"
		event["source"] = "manually"
		event["modelId"] = "deepseek-v4-flash"
		event["modelIsThinking"] = false
		event["schedule"] = map[string]any{
			"type":  "recurring",
			"rrule": "FREQ=WEEKLY;BYDAY=FR;BYHOUR=9;BYMINUTE=0",
		}
		event["prompt"] = "每周五自动整理本周工作"
	case "playbook":
		event["eventCode"] = "playbook_prompt_send"
		event["id"] = "worker-ledger-freedom-dashboard"
		event["name"] = "打工人小账本"
		event["type"] = "other"
		event["promptLength"] = 10
		event["isOfficial"] = 1
		event["source"] = "discover"
	case "skin":
		event["eventCode"] = "appearance_skin_apply"
		event["action"] = "apply"
		event["source"] = "settings_close"
		event["id"] = "theme-tkmw7j"
		event["vipLevel"] = "free"
		event["series"] = "craft"
		event["type"] = "unknown"
		event["name"] = "和平精英激战金秋"
	case "chat", "glmchat", "cat":
		event["eventCode"] = "chat_request_send"
		modelID, modelName := "deepseek-v4-flash", "DeepSeek V4 Flash"
		if kind == "glmchat" || kind == "cat" {
			modelID, modelName = "glm-5.2", "GLM-5.2"
		}
		event["mode"] = "craft"
		if kind == "cat" {
			event["mode"] = "night"
		}
		event["inputLength"] = 12
		event["requestModelId"] = modelID
		event["requestModelName"] = modelName
		event["isPlan"] = false
		event["agentName"] = "default"
		event["agentType"] = "conversation"
	default:
		event["eventCode"] = "heartbeat"
	}
	return event
}

// reportGrowthEvents posts an event batch to the chat realm's /v2/report.
func reportGrowthEvents(sa *storedAuth, events []map[string]any) error {
	if len(events) == 0 {
		return nil
	}
	body, err := json.Marshal(events)
	if err != nil {
		return err
	}
	_, err = growthJSON(sa, http.MethodPost, upstreamBaseFor(sa), growthReportPath, json.RawMessage(body), false)
	return err
}

// growthLightResult summarises one account's lighting pass for the panel.
type growthLightResult struct {
	Lit          []string `json:"lit,omitempty"`
	Claimed      []string `json:"claimed,omitempty"`
	DesktopOnly  []string `json:"desktop_only,omitempty"`
	Skipped      []string `json:"skipped,omitempty"`
	Failed       []string `json:"failed,omitempty"`
	CreditEarned int64    `json:"credit_earned"`
	EnergyEarned int64    `json:"energy_earned"`
	Notes        []string `json:"notes,omitempty"`
}

// lightGrowthTasks accepts pending tasks, reports the events that light them,
// then claims whatever became claimable. gap spaces every upstream call
// (upstream rate-limits bursts; 1s mirrors the desktop client's cadence).
func lightGrowthTasks(sa *storedAuth, r *growthTaskRun, gap time.Duration, ctxDone <-chan struct{}) growthLightResult {
	result := growthLightResult{}
	if sa == nil {
		result.Failed = append(result.Failed, "账号凭证不可用")
		return result
	}
	if gap <= 0 {
		gap = time.Second
	}
	cancelled := func() bool {
		select {
		case <-ctxDone:
			return true
		default:
			return false
		}
	}

	tasks, err := listGrowthTasks(sa)
	if err != nil {
		result.Failed = append(result.Failed, "任务列表获取失败: "+safeManagementError(err))
		return result
	}

	// 1. Accept everything not yet accepted. Failures are not recorded here:
	// the re-check below owns the verdict (a transient rejection recovers on
	// the retry, and recording it twice would misreport the run).
	pending := acceptedGrowthTaskCodes(tasks)
	for start := 0; start < len(pending); start += taskAcceptBatchSize {
		if cancelled() {
			return result
		}
		end := start + taskAcceptBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		if err := acceptGrowthTasks(sa, pending[start:end]); err == nil {
			if r != nil {
				r.Mu.Lock()
				r.Accepted += end - start
				r.Mu.Unlock()
			}
		}
		sleepCtx(ctxDone, gap)
	}

	if len(pending) > 0 {
		// Re-check and re-accept: the upstream occasionally rejects a whole
		// batch transiently (workbuddy2api-hub PR #27), and events reported
		// for an unaccepted task never count toward progress — the run would
		// spend its reports lighting tasks that are stuck at 0/target.
		acceptFailed := map[string]struct{}{}
		for _, code := range pending {
			acceptFailed[code] = struct{}{}
		}
		recordAcceptFailures := func() {
			for code := range acceptFailed {
				result.Failed = append(result.Failed, code)
			}
		}
		if tasks, err = listGrowthTasks(sa); err == nil {
			if still := acceptedGrowthTaskCodes(tasks); len(still) > 0 {
				for start := 0; start < len(still); start += taskAcceptBatchSize {
					if cancelled() {
						recordAcceptFailures()
						return result
					}
					end := start + taskAcceptBatchSize
					if end > len(still) {
						end = len(still)
					}
					if err := acceptGrowthTasks(sa, still[start:end]); err != nil {
						// Still failing: keep the codes marked.
					} else {
						for _, code := range still[start:end] {
							delete(acceptFailed, code)
						}
						if r != nil {
							r.Mu.Lock()
							r.Accepted += end - start
							r.Mu.Unlock()
						}
					}
					sleepCtx(ctxDone, gap)
				}
				if tasks, err = listGrowthTasks(sa); err != nil {
					recordAcceptFailures()
					result.Failed = append(result.Failed, "任务清单刷新失败: "+safeManagementError(err))
					return result
				}
				// The re-list is the truth: anything that reports accepted or
				// completed now (whatever the accept call claimed) recovered.
				for _, t := range tasks {
					status := strings.ToLower(strings.TrimSpace(t.AcceptStatus))
					if status == "accepted" || status == "completed" {
						delete(acceptFailed, strings.TrimSpace(t.TaskCode))
					}
				}
			} else {
				// The re-list already shows everything accepted.
				for _, t := range tasks {
					status := strings.ToLower(strings.TrimSpace(t.AcceptStatus))
					if status == "accepted" || status == "completed" {
						delete(acceptFailed, strings.TrimSpace(t.TaskCode))
					}
				}
			}
			recordAcceptFailures()
		} else {
			recordAcceptFailures()
			result.Failed = append(result.Failed, "任务清单刷新失败: "+safeManagementError(err))
			return result
		}
	}

	uid := sa.Account.UID
	for _, task := range tasks {
		if cancelled() {
			return result
		}
		code := strings.TrimSpace(task.TaskCode)
		// Claimed and locked tasks are not actionable. acceptedGrowthTaskCodes
		// and the panel both already treat `locked` that way; this loop used to
		// check only `claimed`, so it reported behaviour events for tasks the
		// user had not unlocked yet (found by the 0.9.31 audit).
		if code == "" || task.Claimed || task.Locked {
			continue
		}
		kind := growthEventKindFor(code)
		if kind == "" {
			result.Skipped = append(result.Skipped, code)
			continue
		}
		if kind == "desktop_only" || kind == "skip" {
			result.DesktopOnly = append(result.DesktopOnly, code)
			continue
		}
		if _, isNight := nightTaskCodes[code]; isNight && !inNightWindow(time.Now()) {
			result.Skipped = append(result.Skipped, code)
			result.Notes = append(result.Notes, code+" 仅 23:00–08:00 上报计数")
			continue
		}
		// Events reported for a task the upstream never accepted are wasted:
		// progress stays 0 and the claim dies with "task not completed".
		// AcceptStatus is the upstream's own accept receipt; an absent field
		// stays reportable (the progress poll below catches a dead claim).
		if status := strings.ToLower(strings.TrimSpace(task.AcceptStatus)); status != "" &&
			status != "accepted" && status != "completed" {
			result.Skipped = append(result.Skipped, code)
			result.Notes = append(result.Notes, code+" 未接取（accept_status="+status+"），上报不计进度，已跳过")
			continue
		}

		// Already at target → straight to claim.
		if task.Claimable || (task.Target > 0 && task.Current >= task.Target) {
			claimGrowthAndRecord(sa, code, &result)
			sleepCtx(ctxDone, gap)
			continue
		}

		need := task.Target - task.Current
		if need < 1 {
			need = 1
		}
		lit := true
		for i := int64(0); i < need; i++ {
			if cancelled() {
				return result
			}
			event := buildGrowthEvent(kind, uid, int(task.Current)+int(i), sa)
			if err := reportGrowthEvents(sa, []map[string]any{event}); err != nil {
				lit = false
				break
			}
			sleepCtx(ctxDone, gap)
		}
		if !lit {
			result.Failed = append(result.Failed, code)
			continue
		}
		// Verify progress before claiming. The upstream settles reports
		// asynchronously and can silently drop events (id dedup, unaccepted
		// task); claiming blind produced a "task not completed" failure and
		// the reward was left unclaimed even though another report round on a
		// later run could fix it. Fast checks first, a little longer when the
		// counter is visibly moving (workbuddy2api-hub PR #21/#27).
		reached, prog := pollGrowthProgress(sa, code, task.Target, task.Current, ctxDone)
		if !reached {
			result.Notes = append(result.Notes, fmt.Sprintf("%s 已上报但进度 %d/%d 未达成，领奖顺延到下次运行", code, prog, task.Target))
			sleepCtx(ctxDone, gap)
			continue
		}
		result.Lit = append(result.Lit, code)
		claimGrowthAndRecord(sa, code, &result)
		sleepCtx(ctxDone, gap)
	}
	return result
}

// pollGrowthProgress re-fetches the task list until code reaches its target
// (or the attempt budget runs out). Returns the final known progress. Reports
// settle in 1–3 seconds, so the first checks are quick; when the counter is
// moving the wait stretches before giving up.
func pollGrowthProgress(sa *storedAuth, code string, target, current int64, ctxDone <-chan struct{}) (bool, int64) {
	prog := current
	for attempt := 0; attempt < progressPollAttempts; attempt++ {
		tasks, err := listGrowthTasks(sa)
		if err == nil {
			for _, t := range tasks {
				if strings.TrimSpace(t.TaskCode) != code {
					continue
				}
				prog = t.Current
				if prog >= target || t.Claimable || strings.EqualFold(strings.TrimSpace(t.Status), "completed") {
					return true, prog
				}
			}
			if prog > current {
				// Progressing: worth waiting a little longer.
				sleepCtx(ctxDone, progressPollMovingGap)
				continue
			}
		}
		if attempt < progressPollFastAttempts {
			sleepCtx(ctxDone, progressPollGap)
			continue
		}
		break
	}
	return prog >= target, prog
}

// claimGrowthAndRecord claims one task and folds the outcome into result.
func claimGrowthAndRecord(sa *storedAuth, code string, result *growthLightResult) {
	res, err := claimGrowthTask(sa, code)
	if err != nil {
		if claimAlreadyClaimedError(err) {
			return
		}
		result.Failed = append(result.Failed, code)
		return
	}
	if already, _ := res["already_claimed"].(bool); already {
		return
	}
	if credit, ok := res["credit"].(int64); ok {
		result.CreditEarned += credit
	}
	if energy, ok := res["energy"].(int64); ok {
		result.EnergyEarned += energy
	}
	result.Claimed = append(result.Claimed, code)
}

// sleepCtx sleeps for d unless ctxDone closes first.
func sleepCtx(ctxDone <-chan struct{}, d time.Duration) {
	if d <= 0 || ctxDone == nil {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctxDone:
	case <-timer.C:
	}
}

// growthAutoEnabled reports whether the scheduler should run growth automation
// (light + travel) on its daily ticks. Default on; the config key
// growth_auto: false disables it.
func growthAutoEnabled() bool {
	growthAutoMu.RLock()
	defer growthAutoMu.RUnlock()
	return growthAuto
}

// runGrowthAutomation runs the full CN growth loop for one account: buddy
// travel plus task lighting. Best-effort by design — the caller is the daily
// check-in tick and must not fail because a growth endpoint misbehaves.
func runGrowthAutomation(authIndex string, sa *storedAuth) {
	if sa == nil || growthUnsupported(sa) {
		return
	}
	if !pluginWorkerStart() {
		return
	}
	defer pluginWorkerDone()
	lock := growthTaskLockFor(authIndex)
	lock.Lock()
	defer lock.Unlock()
	ctx := pluginContext()

	if travelAutoEnabled() {
		travel := runBuddyTravel(sa)
		if ok, _ := travel["ok"].(bool); ok {
			if action, _ := travel["action"].(string); action != "" {
				log.Printf("workbuddy: growth travel [%s] %s", growthAccountTag(sa), action)
			}
		}
	}

	result := lightGrowthTasks(sa, nil, taskAcceptBatchGap, ctx.Done())
	log.Printf("workbuddy: growth light [%s] lit=%d claimed=%d desktop_only=%d failed=%d credit=+%d",
		growthAccountTag(sa), len(result.Lit), len(result.Claimed), len(result.DesktopOnly), len(result.Failed), result.CreditEarned)
}

// growthUnsupported reports whether growth automation applies to this account
// (CN realm only; Global/Intl have no growth center).
func growthUnsupported(sa *storedAuth) bool {
	return !growthTasksSupported(sa)
}

// runNightGrowthAll fans the night-owl pass out over every CN account, one
// worker per account (same concurrency shape as runAutoCheckin). Called by the
// 01:00 scheduler tick.
func runNightGrowthAll() {
	if pluginQuiescing() {
		return
	}
	if !growthAutoEnabled() {
		return
	}
	files, err := hostAuthList()
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, f := range files {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pluginQuiescing() {
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			sa, err := hostAuthGet(f.AuthIndex)
			if err != nil || growthUnsupported(sa) {
				return
			}
			runNightGrowth(f.AuthIndex, sa)
		}()
	}
	wg.Wait()
}

// runNightGrowth lights ONLY the night-window tasks (black_cat, target 累计
// 3 天): claim when the counter already reached target, otherwise report one
// night chat event and claim through the shared progress poll. Everything else
// stays on the daily pass — a full light run at 01:00 would only burn upstream
// calls re-checking tasks that were settled at 21:00.
func runNightGrowth(authIndex string, sa *storedAuth) {
	if sa == nil || growthUnsupported(sa) {
		return
	}
	lock := growthTaskLockFor(authIndex)
	lock.Lock()
	defer lock.Unlock()
	ctx := pluginContext()
	ctxDone := ctx.Done()

	tasks, err := listGrowthTasks(sa)
	if err != nil {
		log.Printf("workbuddy: night growth [%s] task list failed: %v", growthAccountTag(sa), safeManagementError(err))
		return
	}
	for _, task := range tasks {
		if strings.TrimSpace(task.TaskCode) != blackCatTaskCode {
			continue
		}
		if task.Claimed || task.Locked {
			return
		}
		if status := strings.ToLower(strings.TrimSpace(task.AcceptStatus)); status != "" &&
			status != "accepted" && status != "completed" {
			log.Printf("workbuddy: night growth [%s] %s not accepted (%s), skipped", growthAccountTag(sa), blackCatTaskCode, status)
			return
		}
		uid := sa.Account.UID
		if task.Claimable || (task.Target > 0 && task.Current >= task.Target) {
			result := growthLightResult{}
			claimGrowthAndRecord(sa, blackCatTaskCode, &result)
			log.Printf("workbuddy: night growth [%s] %s claim reached=%d failed=%d credit=+%d",
				growthAccountTag(sa), blackCatTaskCode, len(result.Claimed), len(result.Failed), result.CreditEarned)
			return
		}
		event := buildGrowthEvent("cat", uid, int(task.Current), sa)
		if err := reportGrowthEvents(sa, []map[string]any{event}); err != nil {
			log.Printf("workbuddy: night growth [%s] %s report failed: %v", growthAccountTag(sa), blackCatTaskCode, err)
			return
		}
		reached, prog := pollGrowthProgress(sa, blackCatTaskCode, task.Target, task.Current, ctxDone)
		if !reached {
			log.Printf("workbuddy: night growth [%s] %s reported, progress %d/%d not yet reached", growthAccountTag(sa), blackCatTaskCode, prog, task.Target)
			return
		}
		result := growthLightResult{}
		claimGrowthAndRecord(sa, blackCatTaskCode, &result)
		log.Printf("workbuddy: night growth [%s] %s lit+claim reached=%d failed=%d credit=+%d",
			growthAccountTag(sa), blackCatTaskCode, len(result.Claimed), len(result.Failed), result.CreditEarned)
		return
	}
	log.Printf("workbuddy: night growth [%s] no %s task in list", growthAccountTag(sa), blackCatTaskCode)
}

func growthAccountTag(sa *storedAuth) string {
	if sa == nil {
		return "?"
	}
	if sa.Account.Nickname != "" {
		return sa.Account.Nickname
	}
	if uid := strings.TrimSpace(sa.Account.UID); uid != "" {
		if len(uid) > 8 {
			return uid[:8]
		}
		return uid
	}
	return "?"
}

// travelAuto toggles the buddy-travel half of growth automation independently
// of task lighting (some users only want the tasks, or vice versa).
var (
	growthAuto   = false
	growthAutoMu sync.RWMutex

	travelAuto   = false
	travelAutoMu sync.RWMutex
)

// travelAutoEnabled reports whether the scheduler should run buddy travel.
func travelAutoEnabled() bool {
	travelAutoMu.RLock()
	defer travelAutoMu.RUnlock()
	return travelAuto
}
