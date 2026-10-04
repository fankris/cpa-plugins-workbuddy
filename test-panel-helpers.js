"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const panel = fs.readFileSync(path.join(__dirname, "panel.js"), "utf8");

function section(startMarker, endMarker) {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker, start);
  assert.notEqual(start, -1, "Missing panel source: " + startMarker);
  assert.notEqual(end, -1, "Missing panel source end: " + endMarker);
  return panel.slice(start, end);
}

const esc = panel.split(/\r?\n/).find((line) => line.startsWith("function esc(s){"));
assert.ok(esc, "esc helper exists");
const formatters = section("function fmtNum(n){", "// Daily FREE allowance");
const modelHelpers = section("function estimateAccountModelCredits(", "async function loadGlobalModelCatalog(");
const globalModelPolicy = section("function isGloballyDisabledModelCached(id){", "function updateGlobalModelBadge(");
const quotaRenderer = section("function dailyFreeHTML(", "function healthBarHTML(");
const accountQuota = section("function creditOf(account){", "// 每账号健康条。");
const creditProgress = section("function progressHTML(credits,pending){", "function bindCardActions(root){");
const checkinHelpers = section("function isCheckinAlreadyResult(result){", "function checkinResultToast(");
const context = vm.createContext({
  lastAccounts: [],
  modelIDKey: (id) => String(id || "").trim().toLowerCase(),
  disabledModelIDs: [],
  enabledModelIDs: [],
  pendingModelChanges: new Map(),
  document: { querySelectorAll: () => [] },
});
vm.runInContext([esc, formatters, modelHelpers, globalModelPolicy, quotaRenderer, accountQuota, creditProgress, checkinHelpers].join("\n"), context);

assert.equal(context.esc(42), "42", "numeric catalog metadata is safely escaped");
assert.equal(context.esc(0), "0", "zero values remain visible");
assert.equal(context.esc("<model>"), "&lt;model&gt;", "HTML text remains escaped");

const usage = [
  { model: "glm-5.3-flash", used: 1512000, has_limit: false, requests: 4 },
  { model: "glm-5.3", used: 500000, has_limit: false, requests: 2 },
  { model: "deepseek-v4.1-flash", used: 400000, has_limit: true, requests: 1, limit: 1000000, remaining: 600000, used_percent: 40 },
];
const credits = { total_used: 2012 };
const html = context.dailyFreeHTML(usage, credits);
assert.match(html, /账号本周期已用 2,012 积分/);
assert.match(html, /本模型估算 1,512 积分/);
assert.match(html, /1,000 积分\/百万 tokens/);
assert.doesNotMatch(html, /未配置每日限额/);

const stats = context.modelUsageStatsByID([{ daily_free: usage, credits }]);
const flash = stats.get("glm-5.3-flash");
assert.equal(flash.tokens, 1512000);
assert.equal(flash.estimatedCredits, 1512);
assert.equal(flash.pricedTokens, 1512000);
assert.match(context.modelUsageLabel(flash), /1,000 积分\/百万 tokens/);
assert.equal(stats.get("deepseek-v4.1-flash").pricedTokens, 0, "daily-quota models are excluded from estimated paid cost");

context.enabledModelIDs = ["modelA"];
context.disabledModelIDs = ["modelB"];
assert.equal(context.isGloballyDisabledModelCached("MODELA"), false, "the explicit allowlist enables a model case-insensitively");
assert.equal(context.isGloballyDisabledModelCached("modelB"), true, "an explicit disabled ID remains disabled");
assert.equal(context.isGloballyDisabledModelCached("modelC"), true, "models remain opt-in by default");
context.pendingModelChanges.set("modelb", { enabled: true });
assert.equal(context.isGloballyDisabledModelCached("modelB"), false, "a pending enable is reflected optimistically");
context.pendingModelChanges.set("modelb", { enabled: false });
assert.equal(context.isGloballyDisabledModelCached("modelB"), true, "a pending disable is reflected optimistically");

const knownZero = { total_remain: 0, total_used: 0, total_size: 0, fetched_at: "2026-04-20T10:00:00Z" };
assert.equal(context.creditOf({ credits: knownZero }).known, true, "a fetched zero balance is known");
assert.match(context.progressHTML(knownZero, false), /可用 0/);
assert.doesNotMatch(context.progressHTML(knownZero, false), /额度未加载|正在获取/);
assert.equal(context.creditOf({ credits: {} }).known, false, "an empty placeholder is not a fetched balance");
assert.match(context.progressHTML({}, true), /正在获取/);
assert.match(context.progressHTML({}, false), /额度未加载/);
assert.equal(context.isAccountExhausted({ credits: { total_remain: 0, total_used: 0, total_size: 500 } }), true);
assert.equal(context.isAccountExhausted({ credits: knownZero }), false, "a real zero-size snapshot is not presumed exhausted");
assert.equal(context.isAccountExhausted({}), false, "missing credit data is not presumed exhausted");
assert.equal(context.isCheckinAlreadyResult({ message: "今日签到失败" }), false, "a failure mentioning today is not already checked in");
assert.equal(context.isCheckinAlreadyResult({ reason: "already" }), true);
assert.equal(context.isCheckinAlreadyResult({ success: true, reason: "already" }), true, "structured already result wins over a generic success flag");
assert.equal(context.isCheckinDoneResult({ success: true }), true);
assert.equal(context.isCheckinDoneResult({ reason: "global", message: "Intl unsupported" }), false);

console.log("panel helper tests passed: escaping, credit allocation, known/unknown quota states, and check-in classification");
