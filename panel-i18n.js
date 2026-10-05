/* WorkBuddy UI localization. No credentials, network requests or business actions.
 * CPAMC reference: ee79a794; language storage key cli-proxy-language.
 * Same-origin follows CPAMC. Cross-origin requires a future explicit host bridge:
 * no unverified postMessage protocol is accepted here.
 */
(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  else { root.WorkBuddyI18n = api; api.start(root); }
})(typeof window !== "undefined" ? window : globalThis, function () {
  "use strict";
  const STORAGE_KEY = "cli-proxy-language";
  const LANGUAGES = Object.freeze(["zh-CN", "zh-TW", "en", "ru"]);
  // Each row is zh-CN, zh-TW, English, Russian. Values are plain text, never HTML.
  const messages = Object.freeze({
    subtitle: ["账号、积分与自动化的一站式控制台", "帳號、積分與自動化的一站式控制台", "Accounts, credits and automation", "Аккаунты, кредиты и автоматизация"],
    import: ["导入凭证", "匯入憑證", "Import credentials", "Импорт учётных данных"],
    refresh: ["刷新数据", "重新整理資料", "Refresh data", "Обновить данные"],
    connect: ["连接", "連線", "Connect", "Подключиться"],
    managementKey: ["CPA 管理密钥", "CPA 管理金鑰", "CPA management key", "Ключ управления CPA"],
    keyPlaceholder: ["输入 CPA 管理密钥", "輸入 CPA 管理金鑰", "Enter CPA management key", "Введите ключ управления CPA"],
    authHint: ["需要 CPA 管理密钥才能读取账号数据。嵌入管理中心时会尝试自动获取；认证失败后请勿连续重试。", "需要 CPA 管理金鑰才能讀取帳號資料。嵌入管理中心時會嘗試自動取得；驗證失敗後請勿連續重試。", "A CPA management key is required. Embedded mode attempts to use the management center connection. Avoid repeated retries after authentication fails.", "Требуется ключ управления CPA. Во встроенном режиме используется подключение центра управления, если оно доступно. После ошибки входа не повторяйте запросы многократно."],
    workspace: ["WorkBuddy 工作区", "WorkBuddy 工作區", "WorkBuddy workspace", "Рабочая область WorkBuddy"],
    accounts: ["账号总览", "帳號總覽", "Accounts", "Аккаунты"],
    models: ["模型管理", "模型管理", "Models", "Модели"],
    automation: ["自动化", "自動化", "Automation", "Автоматизация"],
    accountsHint: ["查看账号积分、每日免费额度及近期状态。", "查看帳號積分、每日免費額度及近期狀態。", "View credits, daily free allowance and recent account status.", "Кредиты, дневной бесплатный лимит и состояние аккаунтов."],
    accountFilters: ["账号筛选", "帳號篩選", "Account filters", "Фильтры аккаунтов"],
    all: ["全部", "全部", "All", "Все"],
    exhausted: ["耗尽", "已耗盡", "Exhausted", "Исчерпано"],
    searchAccounts: ["搜索名称、邮箱或 UID", "搜尋名稱、電子郵件或 UID", "Search name, email or UID", "Имя, почта или UID"],
    searchAccountsLabel: ["搜索账号名称、邮箱或 UID", "搜尋帳號名稱、電子郵件或 UID", "Search accounts by name, email or UID", "Поиск аккаунта по имени, почте или UID"],
    bulk: ["批量操作", "批次操作", "Batch actions", "Групповые действия"],
    compact: ["紧凑视图", "緊湊檢視", "Compact view", "Компактный вид"],
    comfortable: ["常规视图", "一般檢視", "Comfortable view", "Обычный вид"],
    density: ["切换卡片密度", "切換卡片密度", "Toggle card density", "Изменить плотность карточек"],
    loadingAccounts: ["正在载入账号…", "正在載入帳號…", "Loading accounts…", "Загрузка аккаунтов…"],
    noAccounts: ["没有匹配的账号", "沒有符合的帳號", "No matching accounts", "Нет подходящих аккаунтов"],
    filterHint: ["试试调整区域筛选或搜索词。", "請調整區域篩選或搜尋詞。", "Try a different region filter or search term.", "Измените регион или поисковый запрос."],
    globalModels: ["全局模型管理", "全域模型管理", "Global model access", "Глобальный доступ к моделям"],
    globalModelsHint: ["统一控制所有 WorkBuddy 账号可使用的模型。", "統一控制所有 WorkBuddy 帳號可使用的模型。", "Manage model access across WorkBuddy accounts.", "Управление доступом к моделям для аккаунтов WorkBuddy."],
    refreshCatalog: ["刷新目录", "重新整理目錄", "Refresh catalog", "Обновить каталог"],
    searchModels: ["搜索模型名称或 ID", "搜尋模型名稱或 ID", "Search model name or ID", "Поиск по названию или ID модели"],
    modelFilter: ["模型状态筛选", "模型狀態篩選", "Filter model status", "Фильтр состояния моделей"],
    allModels: ["全部模型", "全部模型", "All models", "Все модели"],
    enabledOnly: ["仅启用", "僅啟用", "Enabled only", "Только включённые"],
    disabledOnly: ["仅禁用", "僅停用", "Disabled only", "Только отключённые"],
    catalogPending: ["模型目录尚未载入", "尚未載入模型目錄", "Model catalog not loaded", "Каталог моделей не загружен"],
    modelsOpenHint: ["打开模型管理后载入模型目录", "開啟模型管理後載入模型目錄", "Open Models to load the catalog", "Откройте «Модели» для загрузки каталога"],
    tasksAutomation: ["自动化与任务", "自動化與任務", "Automation and tasks", "Автоматизация и задачи"],
    reload: ["重新载入", "重新載入", "Reload", "Перезагрузить"],
    automationPending: ["打开自动化页后载入配置", "開啟自動化頁後載入設定", "Open Automation to load configuration", "Откройте «Автоматизация» для загрузки настроек"],
    close: ["关闭", "關閉", "Close", "Закрыть"],
    cancel: ["取消", "取消", "Cancel", "Отмена"],
    importJSON: ["导入凭证 JSON", "匯入憑證 JSON", "Import credential JSON", "Импорт учётных данных JSON"],
    credentialJSON: ["WorkBuddy 凭证 JSON", "WorkBuddy 憑證 JSON", "WorkBuddy credential JSON", "Учётные данные WorkBuddy JSON"],
    pasteJSON: ["粘贴 WorkBuddy / CodeBuddy 凭证 JSON", "貼上 WorkBuddy / CodeBuddy 憑證 JSON", "Paste WorkBuddy / CodeBuddy credential JSON", "Вставьте учётные данные WorkBuddy / CodeBuddy в JSON"],
    importSave: ["导入并保存", "匯入並儲存", "Import and save", "Импортировать и сохранить"],
    supportedModels: ["支持的模型", "支援的模型", "Supported models", "Поддерживаемые модели"],
    loading: ["加载中…", "載入中…", "Loading…", "Загрузка…"],
    busy: ["处理中…", "處理中…", "Working…", "Обработка…"],
    notifications: ["通知", "通知", "Notifications", "Уведомления"],
    tasks: ["成长任务", "成長任務", "Growth tasks", "Задачи развития"],
    refreshTasks: ["刷新任务", "重新整理任務", "Refresh tasks", "Обновить задачи"],
    syncUnavailable: ["无法读取管理中心语言，当前按本地偏好显示。", "無法讀取管理中心語言，目前使用本機偏好。", "Management center language is unavailable; using local preferences.", "Язык центра управления недоступен; используются локальные настройки."],
    refreshModels: ["重新获取", "重新取得", "Fetch again", "Запросить снова"]
  });
  let language = "zh-CN";
  function normalize(value) {
    const v = String(value || "").trim().toLowerCase();
    if (v === "zh" || /^zh-(cn|sg|hans)(-|$)/.test(v)) return "zh-CN";
    if (/^zh-(tw|hk|mo|hant)(-|$)/.test(v)) return "zh-TW";
    if (/^en(-|$)/.test(v)) return "en";
    if (/^ru(-|$)/.test(v)) return "ru";
    return null;
  }
  function parse(value) {
    if (typeof value !== "string" || !value) return null;
    try {
      const p = JSON.parse(value);
      return normalize(p && typeof p === "object" ? (p.state && p.state.language) || p.language : p);
    } catch (_) { return normalize(value); }
  }
  function stored(storage) { try { return parse(storage.getItem(STORAGE_KEY)); } catch (_) { return null; } }
  function browserLanguage(nav) { return normalize(nav.languages && nav.languages[0] || nav.language || "zh-CN") || "en"; }
  function detect(win) {
    let crossOrigin = false;
    let embedded = false;
    try { embedded = win.parent && win.parent !== win; } catch (_) { embedded = true; }
    if (embedded) {
      try {
        if (win.parent.location.origin !== win.location.origin) throw new Error("cross-origin");
        const value = stored(win.parent.localStorage);
        if (value) return { language: value, source: "host", hostSync: true };
        const nav = win.parent.navigator;
        return { language: browserLanguage(nav), source: "host-browser", hostSync: true };
      } catch (_) { crossOrigin = true; }
    }
    let preference = null;
    try { preference = stored(win.localStorage); } catch (_) {}
    const nav = win.navigator || {};
    return { language: preference || browserLanguage(nav), source: crossOrigin ? "host-unavailable" : preference ? "stored" : "browser", hostSync: false };
  }
  function t(key, locale) {
    const row = messages[key];
    if (!row) return key;
    const index = LANGUAGES.indexOf(normalize(locale || language) || "zh-CN");
    return row[index] || row[0];
  }
  function setLanguage(value) { language = normalize(value) || "zh-CN"; return language; }
  function translate(doc) {
    doc.querySelectorAll("[data-i18n]").forEach(el => {
      const key = el.getAttribute("data-i18n");
      if (messages[key] && el.textContent !== t(key)) el.textContent = t(key);
    });
    ["placeholder", "aria-label", "title"].forEach(attr => {
      doc.querySelectorAll("[data-i18n-" + attr + "]").forEach(el => {
        const key = el.getAttribute("data-i18n-" + attr);
        if (messages[key] && el.getAttribute(attr) !== t(key)) el.setAttribute(attr, t(key));
      });
    });
  }
  function start(win) {
    if (win.__wbI18nStarted) return;
    win.__wbI18nStarted = true;
    const doc = win.document;
    function sync() {
      const next = detect(win); setLanguage(next.language);
      doc.documentElement.lang = language;
      doc.documentElement.dataset.languageSource = next.source;
      translate(doc);
      const sel = doc.getElementById("langSelect");
      if (sel && sel.value !== language) sel.value = language;
      const notice = doc.getElementById("languageSyncNotice");
      if (notice) notice.hidden = next.source !== "host-unavailable";
    }
    function ready() {
      sync();
      // Only update explicitly labelled static text; never re-render account data.
      const observer = new win.MutationObserver(() => translate(doc));
      observer.observe(doc.body, { childList: true, subtree: true });
      win.addEventListener("storage", e => { if (e.key === STORAGE_KEY || e.key === null) sync(); });
      win.addEventListener("languagechange", sync);
      win.addEventListener("pageshow", () => { observer.observe(doc.body, { childList: true, subtree: true }); sync(); });
      doc.addEventListener("visibilitychange", () => { if (!doc.hidden) sync(); });
      win.addEventListener("pagehide", () => observer.disconnect());
    }
    if (doc.readyState === "loading") doc.addEventListener("DOMContentLoaded", ready, { once: true });
    else ready();
    return sync;
  }
  return Object.freeze({ STORAGE_KEY, LANGUAGES, messages, normalize, parse, detect, t, setLanguage, translate, start, getLanguage: () => language });
});
