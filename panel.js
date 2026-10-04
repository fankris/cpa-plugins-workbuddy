/* panel.js — WorkBuddy management panel logic.
 *
 * Split out of panel.html in 0.9.31: the page was a single 1232-line file with
 * ~1000 lines of inline JS, which made review and diffing impractical. The HTML
 * keeps markup + styles + the tiny early-theme bootstrap (it must run before
 * first paint, so it stays inline).
 *
 * Everything here is a plain classic script (no modules) because the host
 * serves it as a plugin resource and the panel is also embedded in an iframe
 * by CPAMC/CPAMP.
 */
const MANAGEMENT_SUFFIX = "/v0/management/plugins/workbuddy";
function managementAPIBase(pathname){
  const path=String(pathname||"").replace(/\/+$/,"");
  const suffixes=["/v0/resource/plugins/workbuddy/panel","/v0/management/plugins/workbuddy/panel"];
  for(const suffix of suffixes){if(path.endsWith(suffix))return path.slice(0,-suffix.length)+MANAGEMENT_SUFFIX}
  return MANAGEMENT_SUFFIX;
}
const API = managementAPIBase(window.location.pathname);

/* ---------- management key acquisition (3 fallbacks, zero config) ----------
   1) Embedding UI localStorage, same-origin iframe:
      - CPA main panel (CPAMC): store "cli-proxy-auth", object with
        state.managementKey, enc::v1:: obfuscation.
      - CPA Manager Plus (CPAMP): store "managementKey", a JSON.stringify'd
        plain string; v1.13+ obfuscates enc::v2::, older builds enc::v1:: or
        plaintext. A leftover enc:: remnant means decryption failed and must
        NOT be sent as a key (guaranteed 401).
   2) ?key= URL param (cleaned from history after read, kept in sessionStorage).
   3) Manual input (sessionStorage only, never persisted).
*/
const ENC_PREFIX = "enc::v1::";
const ENC_PREFIX_V2 = "enc::v2::";
const SECRET_SALT = "cli-proxy-api-webui::secure-storage";
const PANEL_STORE = "cli-proxy-auth";
const SS_KEY = "workbuddy-mgmt-key";
let urlManagementKey="";
let urlKeyConsumed=false;
let memoryManagementKey="";
let rejectedManagementKey="";

function _enc(t){return new TextEncoder().encode(t)}
function _dec(b){return new TextDecoder().decode(b)}
function _keyBytes(v2){
  const list = [];
  if(v2){
    try{if(window.parent&&window.parent.location&&window.parent.location.host) list.push(_enc(SECRET_SALT+"|v2|"+window.parent.location.host))}catch(e){}
    try{list.push(_enc(SECRET_SALT+"|v2|"+window.location.host))}catch(e){}
    list.push(_enc(SECRET_SALT+"|v2"));
    return list;
  }
  try{if(window.parent&&window.parent.location&&window.parent.location.host) list.push(_enc(SECRET_SALT+"|"+window.parent.location.host+"|"+navigator.userAgent))}catch(e){}
  try{list.push(_enc(SECRET_SALT+"|"+window.location.host+"|"+navigator.userAgent))}catch(e){}
  list.push(_enc(SECRET_SALT));
  return list;
}
function _xor(d,k){const r=new Uint8Array(d.length);for(let i=0;i<d.length;i++)r[i]=d[i]^k[i%k.length];return r}
function _b64e(b){let s="";for(let i=0;i<b.length;i++)s+=String.fromCharCode(b[i]);return btoa(s)}
function _b64d(s){const bin=atob(s);const b=new Uint8Array(bin.length);for(let i=0;i<bin.length;i++)b[i]=bin.charCodeAt(i);return b}
function deobfuscate(p){
  if(!p)return p;
  const v2=p.startsWith(ENC_PREFIX_V2);
  if(!v2&&!p.startsWith(ENC_PREFIX))return p;
  let payload;
  try{payload=_b64d(p.slice((v2?ENC_PREFIX_V2:ENC_PREFIX).length))}catch(e){return p}
  for(const k of _keyBytes(v2)){
    try{
      const dec = _dec(_xor(payload, k));
      // Valid plaintexts: JSON object (CPAMC store), JSON array, or a JSON
      // quoted string (CPAMP stores keys as JSON.stringify(key)).
      if(dec && (dec.startsWith("{") || dec.startsWith("[") || dec.startsWith("\""))) return dec;
    }catch(e){}
  }
  return p;
}
function _unquote(s){
  if(s&&s.length>=2&&s.startsWith("\"")&&s.endsWith("\"")){
    try{const p=JSON.parse(s);if(typeof p==="string"&&p)return p}catch(e){}
  }
  return s;
}
function isEmbedded(){try{return window.self!==window.top}catch(e){return false}}

function safeSessionGet(){try{return sessionStorage.getItem(SS_KEY)||""}catch(e){return ""}}
function safeSessionSet(value){memoryManagementKey=String(value||"");try{sessionStorage.setItem(SS_KEY,memoryManagementKey)}catch(e){}}
function clearManagementKey(value){
  const rejected=String(value||"");
  if(rejected)rejectedManagementKey=rejected;
  try{if(!rejected||sessionStorage.getItem(SS_KEY)===rejected)sessionStorage.removeItem(SS_KEY)}catch(e){}
  if(!rejected||memoryManagementKey===rejected)memoryManagementKey="";
  if(urlManagementKey===rejected)urlManagementKey="";
}
function readPanelKey(){
  for(const store of [PANEL_STORE,"managementKey"]){
    let raw=null;
    try{if(window.parent&&window.parent.localStorage)raw=window.parent.localStorage.getItem(store)}catch(e){}
    if(!raw){try{raw=localStorage.getItem(store)}catch(e){}}
    if(!raw)continue;
    if(store==="managementKey"){
      const decoded=_unquote(deobfuscate(raw).trim());
      if(decoded&&!decoded.startsWith("{")&&!decoded.startsWith("[")&&!decoded.startsWith("enc::")&&!decoded.startsWith('"'))return decoded;
      continue;
    }
    try{
      const parsed=JSON.parse(deobfuscate(raw));
      const state=(parsed&&parsed.state)||parsed||{};
      const key=typeof state.managementKey==="string"?state.managementKey.trim():"";
      if(key)return key;
    }catch(e){}
  }
  return null;
}
function readUrlKey(){
  if(urlKeyConsumed)return urlManagementKey||null;
  urlKeyConsumed=true;
  try{
    const url=new URL(window.location.href);
    urlManagementKey=(url.searchParams.get("key")||"").trim();
    if(urlManagementKey){
      url.searchParams.delete("key");
      history.replaceState(history.state||null,"",url.pathname+url.search+url.hash);
    }
  }catch(e){}
  return urlManagementKey||null;
}
readUrlKey();
function getKey(){
  const session=safeSessionGet();
  if(session&&session!==rejectedManagementKey)return session;
  const panelKey=readPanelKey();
  if(panelKey&&panelKey!==rejectedManagementKey)return panelKey;
  if(urlManagementKey&&urlManagementKey!==rejectedManagementKey){safeSessionSet(urlManagementKey);return urlManagementKey}
  return memoryManagementKey&&memoryManagementKey!==rejectedManagementKey?memoryManagementKey:null;
}
function saveKey(){
  const value=document.getElementById("keyInput").value.trim();
  if(!value)return;
  rejectedManagementKey="";
  safeSessionSet(value);
  document.getElementById("authBox").style.display="none";
  restoreDensity();
  load(false);
}
function showAuth(){const box=document.getElementById("authBox");if(box)box.style.display="block";const input=document.getElementById("keyInput");input&&input.focus()}
function authHeaders(key){return key?{"Authorization":"Bearer "+key}:{}}
/* --------------------------------------------------------------------------- */

function toast(title, kind="ok", detail=""){
  const box=document.getElementById("toasts");
  const el=document.createElement("div");
  el.className="toast "+kind;
  el.setAttribute("role",kind==="err"?"alert":"status");
  el.setAttribute("aria-live",kind==="err"?"assertive":"polite");
  el.innerHTML='<div class="t"></div>'+(detail?'<div class="d"></div>':'');
  el.querySelector(".t").textContent=title;
  if(detail)el.querySelector(".d").textContent=detail;
  box.appendChild(el);
  requestAnimationFrame(()=>requestAnimationFrame(()=>el.classList.add("show")));
  const kill=()=>{el.classList.remove("show");setTimeout(()=>el.remove(),300)};
  el.onclick=kill;
  setTimeout(kill, kind==="err"?6000:4000);
  while(box.children.length>5)box.firstChild.remove();
}
const busyStates=new WeakMap();
function busy(btn,on){
  if(!btn)return;
  let state=busyStates.get(btn);
  if(on){
    if(state)state.count++;
    else{state={count:1,disabled:btn.disabled,html:btn.innerHTML};busyStates.set(btn,state)}
    btn.disabled=true;
    btn.innerHTML='<span class="spin" aria-hidden="true"></span>处理中…';
    btn.setAttribute('aria-busy','true');
    return;
  }
  if(!state)return;
  state.count--;
  if(state.count>0)return;
  btn.disabled=state.disabled;
  btn.innerHTML=state.html;
  btn.removeAttribute('aria-busy');
  busyStates.delete(btn);
}
// Immediate UI: mark a check-in button as done so user doesn't see "签到" flash
// while load() re-fetches (which can take seconds).
function markCheckinDone(btn){
  if(!btn)return;
  busy(btn,false);
  btn.disabled=true;
  btn.className="done";
  btn.innerHTML="已签到";
  btn.dataset.orig="已签到";
}
function markTrialDone(btn){
  if(!btn)return;
  busy(btn,false);
  btn.disabled=true;
  btn.className="done";
  btn.innerHTML="已领取";
  btn.dataset.orig="已领取";
}
function fmtRemain(c){if(!c)return "-";const t=(c.total_remain||0)+(c.total_used||0);return c.total_remain+" 余 / "+t+" 总"}
function taskStatusText(t){
  if(t.claimed)return '<span class="task-pill ok">已领取</span>';
  if(t.claimable)return '<span class="task-pill ok">可领取</span>';
  if(t.locked)return '<span class="task-pill warn">已锁定</span>';
  if(String(t.accept_status||'').toLowerCase()==='accepted')return '<span class="task-pill">已接受</span>';
  return '<span class="task-pill">未接受</span>';
}
function taskCardHTML(t){
  const title=esc(t.title||t.task_desc||t.description||t.task_code||'未命名任务');
  const desc=esc(t.description||t.task_desc||'');
  const current=Number(t.current)||0, target=Number(t.target)||0;
  const reward=[];
  if(Number(t.credit))reward.push('积分 '+Number(t.credit));
  if(Number(t.energy))reward.push('能量 '+Number(t.energy));
  if(t.reward_buddy)reward.push('Buddy');
  const claim=t.claimable&&!t.claimed&&!t.locked;
  const code=esc(String(t.task_code||''));
  const claimBtn=claim?`<button class="sec" data-action="task-claim" data-task-code="${code}">领取奖励</button>`:'';
  const jump=t.jump_url?`<a href="${esc(t.jump_url)}" target="_blank" rel="noopener noreferrer" class="task-pill">打开任务</a>`:(t.jump_url_text?`<span class="task-pill warn">${esc(t.jump_url_text)}</span>`:'');
  return `<div class="task-item"><div class="task-head"><div><div class="task-title">${title}</div>${desc?`<div class="task-meta">${desc}</div>`:''}</div><div>${taskStatusText(t)}</div></div><div class="task-meta">进度 ${current}${target?` / ${target}`:''}${reward.length?' · 奖励 '+esc(reward.join('、')):''}${t.task_code?` · 编号 ${esc(t.task_code)}`:''}</div><div class="task-actions">${claimBtn}${jump}</div></div>`;
}
function renderTasks(data){
  const body=document.getElementById('tasksBody');
  if(!body)return;
  if(data&&data.error){body.innerHTML=`<div class="task-meta v err">${esc(data.error)}</div>`;return}
  const tasks=Array.isArray(data&&data.tasks)?data.tasks:[];
  if(!tasks.length){body.innerHTML='<div class="task-meta">暂无成长任务，或上游尚未返回任务。</div>';return}
  body.innerHTML='<div class="task-list">'+tasks.map(taskCardHTML).join('')+'</div>';
  bindCardActions(body);
}
/* ---------- automation settings (writes CPA plugin config live) ----------
   The toggles are persisted through CPA's own management API
   (PATCH /v0/management/plugins/workbuddy/config), which saves config.yaml
   and asynchronously re-applies it — the plugin receives plugin.reconfigure
   and the new values take effect without a restart.
   Only touched keys are sent, so advanced YAML-only options (models,
   scheduler_mode, ...) are never overwritten by the panel. */
const CONFIG_API = API + "/config";

const SETTINGS_FIELDS = [
  {key:"checkin_auto",   label:"自动签到",     hint:"CN 账号每天 09:00 / 21:00 自动签到领积分，并自动恢复被停用的账号"},
  {key:"lifecycle_auto", label:"积分生命周期", hint:"积分耗尽的账号自动停用、不再参与调度；签到回血后自动恢复"},
  {key:"growth_auto",    label:"成长任务自动点亮", hint:"签到后自动接取任务、上报行为事件点亮并领奖（专家/团队自动换 id，夜猫子仅在 23:00–08:00）"},
  {key:"travel_auto",    label:"猫猫旅行",     hint:"自动检查旅行状态：在家自动派出、归来自动领奖"},
  {key:"token_keepalive",label:"令牌保活",     hint:"每天 22:00 自动刷新访问令牌，避免会话过期"},
];
let settingsRequestSeq=0;
let settingsActiveTab="automation";
let activeWorkspace="accounts";
let settingsWriteQueue=Promise.resolve();
let configMutationRevision=0;
let settingsValues=Object.create(null);
let settingsFieldSeq=Object.create(null);
async function waitForConfigWrites(){
  await Promise.all([settingsWriteQueue.catch(()=>{}),globalModelSaveQueue.catch(()=>{})]);
}
function switchWorkspace(workspace){
  const next=["accounts","models","automation"].includes(workspace)?workspace:"accounts";
  activeWorkspace=next;
  settingsActiveTab=next==="models"?"models":"automation";
  document.querySelectorAll(".workspace-tab").forEach(tab=>{
    const active=tab.dataset.workspace===next;
    tab.classList.toggle("active",active);
    tab.setAttribute("aria-selected",active?"true":"false");
    tab.tabIndex=active?0:-1;
  });
  document.querySelectorAll("[data-workspace-panel]").forEach(panel=>{
    panel.hidden=panel.dataset.workspacePanel!==next;
  });
  if(next==="models"&&!globalModelCatalogLoaded)loadGlobalModelCatalog();
  if(next==="automation")loadSettings();
}
function openSettingsModal(){switchWorkspace("automation")}
function closeSettingsModal(){settingsRequestSeq++;switchWorkspace("accounts")}
function onSettingsMaskClick(){}
function switchSettingsTab(tab){switchWorkspace(tab==="models"?"models":"automation")}
function reloadSettings(){
  loadSettings();
  if(settingsActiveTab==="models")loadGlobalModelCatalog(document.getElementById("modelRefreshBtn"),true);
}

async function loadSettings(){
  const requestSeq=++settingsRequestSeq;
  const box=document.getElementById('settingsBody');
  const status=document.getElementById('settingsStatus');
  box.innerHTML='<div class="loading">加载中…</div>';
  status.textContent='';
  let cfg={};
  try{
    await waitForConfigWrites();
    if(requestSeq!==settingsRequestSeq)return;
    const revision=configMutationRevision;
    cfg=await apiConfig('GET');
    if(cfg&&cfg.error)throw new Error(cfg.error);
    if(requestSeq!==settingsRequestSeq)return;
    if(revision!==configMutationRevision){loadSettings();return;}
    disabledModelIDs=Array.isArray(cfg.models_disabled)?cfg.models_disabled:[];
    enabledModelIDs=Array.isArray(cfg.models_enabled)?cfg.models_enabled:[];
    if(globalModelCatalogLoaded)syncDisabledModelRows();else updateGlobalModelBadge();
  }catch(e){
    if(requestSeq!==settingsRequestSeq)return;
    box.innerHTML='<div class="task-meta">读取配置失败：'+esc(e.message||String(e))+'</div>';
    return;
  }
  // Last keepalive run (best-effort): surfaces the /keepalive/status endpoint
  // so operators can see whether the 22:00 refresh actually happened.
  let keepaliveLine='';
  try{
    const ks=await api('/keepalive/status');
    const last=ks&&ks.last_run;
    if(last&&Array.isArray(last.results)&&last.results.length){
      const okN=last.results.filter(r=>r.status==="refreshed").length;
      const badN=last.results.filter(r=>r.status&&r.status!=="refreshed"&&r.status!=="skipped").length;
      keepaliveLine='上次保活：'+okN+' 成功'+(badN?' · '+badN+' 异常':'')+'（'+(last.when||'').replace('T',' ').slice(0,19)+'）';
    }else{
      keepaliveLine='上次保活：暂无记录（每天 22:00 自动执行）';
    }
  }catch(_){/* best-effort: the panel works without this line */}
  if(requestSeq!==settingsRequestSeq)return;
  const rows=SETTINGS_FIELDS.map(f=>{
    const raw=cfg[f.key];
    // Absent key = plugin default (on). Explicit false = off.
    const on=raw===undefined||raw===null?true:(raw===true||raw==='true'||raw===1||raw==='1');
    settingsValues[f.key]=on;
    return `<label class="settings-option">
      <input type="checkbox" data-key="${f.key}" ${on?'checked':''} onchange="saveSetting('${f.key}',this.checked,this)">
      <span><b>${esc(f.label)}</b><span class="hint-text">${esc(f.hint)}</span></span>
    </label>`;
  }).join('');
  box.innerHTML=rows||'<div class="task-meta">无可用选项</div>';
  status.textContent='配置来自 CPA（plugins.configs.workbuddy） · 缺省自动化项按插件默认开启展示，不会自动写回配置'+(keepaliveLine?' · '+keepaliveLine:'');
}

async function saveSetting(key,value,el){
  const next=!!value;
  const previous=Object.prototype.hasOwnProperty.call(settingsValues,key)?!!settingsValues[key]:!next;
  const seq=(settingsFieldSeq[key]||0)+1;
  settingsFieldSeq[key]=seq;
  settingsValues[key]=next;
  configMutationRevision++;
  const status=document.getElementById('settingsStatus');
  if(status)status.textContent='保存中…';
  const body={};body[key]=next;
  const request=settingsWriteQueue.catch(()=>{}).then(()=>apiConfig('PATCH',body));
  settingsWriteQueue=request;
  try{
    await request;
    if(settingsFieldSeq[key]===seq)settingsValues[key]=next;
    if(status)status.textContent='已保存，CPA 正在异步重新应用（'+key+' = '+(next?'开':'关')+'）';
    toast('设置已保存','ok',key+(next?' 已开启':' 已关闭'));
    load(false);
  }catch(e){
    if(settingsFieldSeq[key]===seq){settingsValues[key]=previous;if(el)el.checked=previous;loadSettings();}
    if(status)status.textContent='保存失败：'+(e.message||String(e));
    toast('保存失败','err',e.message||String(e));
  }finally{
    if(settingsWriteQueue===request)settingsWriteQueue=Promise.resolve();
  }
}

async function requestJSON(url,opts={}){
  const key=getKey();
  if(!key){showAuth();throw new Error('需要管理密钥')}
  if(Date.now()<authFailUntil)throw new Error('认证请求已暂缓，请等待冷却后重试（防止主机 IP 封禁）');
  const controller=new AbortController();
  const timeout=setTimeout(()=>controller.abort(),45000);
  let response;
  try{
    response=await fetch(url,{...opts,credentials:'omit',headers:{...(opts.headers||{}),...authHeaders(key)},signal:controller.signal});
  }catch(e){
    if(controller.signal.aborted)throw new Error('请求超时，请检查 CPA 主机状态');
    throw e;
  }finally{clearTimeout(timeout)}
  const text=await response.text().catch(()=>"");
  let data={};
  if(text){try{data=JSON.parse(text)}catch(_){data={message:text}}}
  const detail=String(data&& (data.error||data.message)||text||('HTTP '+response.status));
  if(response.status===401){
    authFailCount++;
    clearManagementKey(key);
    showAuth();
    if(authFailCount>=3){authFailUntil=Date.now()+60000;authFailCount=0;throw new Error('管理密钥连续失败，请检查密钥并等待 60 秒后再试')}
    throw new Error(detail||'management key 无效或缺失');
  }
  if(response.status===403){
    if(/IP banned|too many failed|封禁/i.test(detail)){
      let waitMs=60000;
      const match=detail.match(/(\d+)\s*m(?:in(?:ute)?s?)?\s*(\d+)\s*s/i)||detail.match(/(\d+)\s*m/i);
      if(match){waitMs=Math.min(Math.max(((parseInt(match[1],10)||0)*60+(parseInt(match[2],10)||0))*1000,30000),30*60*1000)}
      authFailUntil=Date.now()+waitMs;
    }
    throw new Error(detail||'禁止访问 (403)');
  }
  if(response.status===429){
    const seconds=Number(response.headers&&response.headers.get('Retry-After'));
    if(Number.isFinite(seconds)&&seconds>0)authFailUntil=Date.now()+Math.min(seconds*1000,30*60*1000);
    throw new Error(detail||'请求过于频繁，请稍后再试');
  }
  if(!response.ok){const error=new Error(detail||('HTTP '+response.status));error.status=response.status;throw error}
  authFailCount=0;
  return data;
}
async function api(path,opts={}){return requestJSON(API+path,opts)}

async function apiConfig(method,body){
  return requestJSON(CONFIG_API,{method,headers:body?{'Content-Type':'application/json'}:{},...(body?{body:JSON.stringify(body)}:{})});
}
const dialogFocusReturn=new WeakMap();
let previousBodyOverflow="";
function openDialog(id,focusSelector){
  const mask=document.getElementById(id);
  if(!mask)return;
  if(!mask.classList.contains("show"))dialogFocusReturn.set(mask,document.activeElement);
  mask.classList.add("show");
  const app=document.getElementById("appShell");
  if(app)app.inert=true;
  if(!document.body.classList.contains("dialog-open"))previousBodyOverflow=document.body.style.overflow||"";
  document.body.classList.add("dialog-open");
  document.body.style.overflow="hidden";
  const dialog=mask.querySelector('[role="dialog"]');
  requestAnimationFrame(()=>{
    const target=(focusSelector&&dialog&&dialog.querySelector(focusSelector))||(dialog&&dialog.querySelector('button:not([disabled]),input:not([disabled]),textarea:not([disabled]),select:not([disabled]),[href],[tabindex]:not([tabindex="-1"])'))||dialog;
    if(target)target.focus();
  });
}
function closeDialog(id){
  const mask=document.getElementById(id);
  if(!mask)return;
  mask.classList.remove("show");
  const stillOpen=Array.from(document.querySelectorAll(".modal-mask.show")).length>0;
  const app=document.getElementById("appShell");
  if(!stillOpen){
    if(app)app.inert=false;
    document.body.classList.remove("dialog-open");
    document.body.style.overflow=previousBodyOverflow;
    const opener=dialogFocusReturn.get(mask);
    dialogFocusReturn.delete(mask);
    requestAnimationFrame(()=>{
      if(opener&&opener.isConnected)opener.focus();
      else document.getElementById("workspaceAccountsTab")?.focus();
    });
  }
}
function activeDialog(){return Array.from(document.querySelectorAll(".modal-mask.show")).pop()||null}
function handleWorkspaceKeydown(event){
  const tab=event.target.closest&&event.target.closest(".workspace-tab");
  if(!tab)return;
  const tabs=Array.from(document.querySelectorAll(".workspace-tab"));
  const at=tabs.indexOf(tab);
  let next=-1;
  if(event.key==="ArrowRight")next=(at+1)%tabs.length;
  else if(event.key==="ArrowLeft")next=(at-1+tabs.length)%tabs.length;
  else if(event.key==="Home")next=0;
  else if(event.key==="End")next=tabs.length-1;
  if(next<0)return;
  event.preventDefault();
  tabs[next].focus();
  switchWorkspace(tabs[next].dataset.workspace);
}
let tasksRequestSeq=0;
let tasksModalGeneration=0;
let currentModelsAuth="";
let modelsRequestSeq=0;
function openModelsModal(idx,authIndex){
  currentModelsAuth=authIndex||idx||"";
  const requestSeq=++modelsRequestSeq;
  const account=(lastAccounts||[]).find(x=>x.auth_index===currentModelsAuth)||{};
  document.getElementById("modelsModalTitle").textContent="支持的模型 · "+(account.nickname||account.label||currentModelsAuth);
  document.getElementById("modelsBody").innerHTML='<div class="loading">加载中…</div>';
  openDialog("modelsModal");
  loadModels(currentModelsAuth,false,requestSeq);
}
function closeModelsModal(){modelsRequestSeq++;currentModelsAuth="";closeDialog("modelsModal")}
function onModelsMaskClick(event){if(event.target===document.getElementById("modelsModal"))closeModelsModal()}
async function loadModels(idx,notify,requestSeq){
  if(!idx)return;
  requestSeq=requestSeq||modelsRequestSeq;
  try{
    const d=await api('/models?auth_index='+encodeURIComponent(idx));
    if(requestSeq!==modelsRequestSeq||idx!==currentModelsAuth)return;
    renderModels(d);
    if(notify)toast('模型列表已刷新','ok','共 '+(d.count||0)+' 个');
  }catch(e){
    if(requestSeq!==modelsRequestSeq||idx!==currentModelsAuth)return;
    renderModels({error:e.message||String(e)});
    if(notify)toast('模型列表刷新失败','err',e.message||String(e));
  }
}
async function refreshModels(idx,btn){
  if(!idx)return; busy(btn,true);
  const requestSeq=++modelsRequestSeq;
  try{
    const d=await api('/models/refresh?auth_index='+encodeURIComponent(idx),{method:'POST'});
    if(requestSeq!==modelsRequestSeq||idx!==currentModelsAuth)return;
    renderModels(d);
    if(d.error)toast('重新获取失败','err',d.error);
    else toast('模型列表已重新获取','ok','共 '+(d.count||0)+' 个');
  }catch(e){toast('重新获取失败','err',e.message||String(e))}
  finally{busy(btn,false)}
}
function renderModels(d){
  const box=document.getElementById('modelsBody');
  if(!box)return;
  if(!d||d.error){box.innerHTML='<div class="md-empty">加载失败：'+esc((d&&d.error)||'未知错误')+'</div>';return}
  const list=Array.isArray(d.models)?d.models:[];
  const src=d.source||{};
  const srcLine=src.source?'<div class="models-source-line"><span class="models-source-label">数据来源</span><strong>'+esc(src.source)+'</strong>'+(src.count?' <span>· 目录 '+esc(src.count)+' 项</span>':'')+'</div>':'';
  if(!list.length){box.innerHTML=srcLine+'<div class="md-empty">该凭据当前没有可用模型。</div>';return}
  const rows=list.map(m=>{
    const id=String(m&&m.id||'').trim();
    if(!id)return '';
    const name=String(m.name||'').trim();
    const title=name&&name!==id?name:id;
    const disabled=!!m.disabled;
    const bits=[];
    if(m.context_length)bits.push('上下文 '+fmtTokens(m.context_length));
    if(m.max_completion_tokens)bits.push('输出上限 '+fmtTokens(m.max_completion_tokens));
    const meta=bits.length?'<div class="md-meta">'+bits.map(esc).join(' · ')+'</div>':'';
    const idLine=title!==id?'<code class="account-model-id">'+esc(id)+'</code>':'';
    const search=(id+' '+title+' '+bits.join(' ')).toLowerCase();
    const stateClass=disabled?'disabled':'enabled';
    return '<div class="md-item account-model-item" data-search="'+esc(search)+'" data-disabled="'+(disabled?'true':'false')+'"><div class="account-model-row"><div class="account-model-main"><strong class="account-model-title">'+esc(title)+'</strong>'+idLine+meta+'</div><span class="model-state account-model-state '+stateClass+'">'+(disabled?'全局禁用':'已启用')+'</span></div></div>';
  }).filter(Boolean).join('');
  box.innerHTML=srcLine+'<div class="models-browser-toolbar"><input id="accountModelsSearch" class="field-input models-browser-search" type="search" placeholder="搜索模型名称或 ID" aria-label="搜索该账号支持的模型" oninput="filterAccountModels()"><select id="accountModelsFilter" class="field-input models-browser-filter" aria-label="按全局状态筛选" onchange="filterAccountModels()"><option value="all">全部状态</option><option value="enabled">仅已启用</option><option value="disabled">仅已禁用</option></select></div><div id="accountModelsSummary" class="models-browser-summary" aria-live="polite"></div><div class="md-list account-model-list">'+(rows||'')+'<div id="accountModelsEmpty" class="md-empty" hidden></div></div>';
  filterAccountModels();
}
function filterAccountModels(){
  const query=(document.getElementById('accountModelsSearch')?.value||'').trim().toLowerCase();
  const filter=document.getElementById('accountModelsFilter')?.value||'all';
  const rows=Array.from(document.querySelectorAll('#modelsBody .account-model-item'));
  let visible=0,enabled=0,disabled=0;
  rows.forEach(row=>{
    const isDisabled=row.dataset.disabled==='true';
    if(isDisabled)disabled++;else enabled++;
    const matchesQuery=!query||(row.dataset.search||'').includes(query);
    const matchesFilter=filter==='all'||(filter==='disabled'?isDisabled:!isDisabled);
    const show=matchesQuery&&matchesFilter;
    row.hidden=!show;
    if(show)visible++;
  });
  const summary=document.getElementById('accountModelsSummary');
  if(summary)summary.textContent='显示 '+visible+' / '+rows.length+' 个模型 · 已启用 '+enabled+' · 全局禁用 '+disabled;
  const empty=document.getElementById('accountModelsEmpty');
  if(empty){empty.hidden=visible>0;empty.textContent=rows.length?'没有符合筛选条件的模型。':'';}
}

let globalModelSaveQueue=Promise.resolve();
let disabledModelIDs=[];
let enabledModelIDs=[];
let globalModelItems=[];
let pendingModelChanges=new Map();
let globalModelCatalogLoaded=false;
let globalModelRequestSeq=0;
function modelIDKey(id){return String(id||'').trim().toLowerCase()}
function isGloballyDisabledModelCached(id){
  const key=modelIDKey(id);
  if(!key)return true;
  const pending=pendingModelChanges.get(key);
  if(pending)return !pending.enabled;
  return disabledModelIDs.some(x=>modelIDKey(x)===key)||!enabledModelIDs.some(x=>modelIDKey(x)===key);
}
function updateGlobalModelBadge(){
  const badge=document.getElementById('settingsDisabledCount');
  if(badge)badge.textContent=String(currentDisabledModelCount());
  filterGlobalModels();
}
function filterGlobalModels(){
  const query=(document.getElementById('modelSettingsSearch')?.value||'').trim().toLowerCase();
  const filter=document.getElementById('modelSettingsFilter')?.value||'all';
  const rows=Array.from(document.querySelectorAll('#globalModelsBody .global-model-item'));
  let visible=0;
  let disabledCount=0;
  rows.forEach(row=>{
    const disabled=row.dataset.disabled==='true';
    if(disabled)disabledCount++;
    const matchesQuery=!query||(row.dataset.search||'').includes(query);
    const matchesFilter=filter==='all'||(filter==='disabled'?disabled:!disabled);
    const show=matchesQuery&&matchesFilter;
    row.hidden=!show;
    if(show)visible++;
  });
  const empty=document.getElementById('globalModelFilterEmpty');
  if(empty){empty.hidden=visible>0;empty.textContent=rows.length?'没有符合筛选条件的模型。':'';}
  const summary=document.getElementById('modelSettingsSummary');
  if(summary){
    if(rows.length)summary.textContent='显示 '+visible+' / '+rows.length+' 个模型 · 已启用 '+(rows.length-disabledCount)+' · 全局禁用 '+disabledCount;
    else summary.textContent='当前 '+currentDisabledModelCount()+' 个禁用项';
  }
}
function currentDisabledModelCount(){
  const disabled=new Set(disabledModelIDs.map(modelIDKey).filter(Boolean));
  globalModelItems.forEach(model=>{
    const key=modelIDKey(model&&model.id);
    if(!key)return;
    if(isGloballyDisabledModelCached(key))disabled.add(key);else disabled.delete(key);
  });
  pendingModelChanges.forEach((change,key)=>{if(change.enabled)disabled.delete(key);else disabled.add(key)});
  return disabled.size;
}
function estimateAccountModelCredits(list,model,credits){
  if(!model||model.has_limit||!credits||credits.total_used==null)return null;
  const accountCredits=Number(credits.total_used);
  const used=Math.max(0,Number(model.used)||0);
  if(!Number.isFinite(accountCredits)||accountCredits<0||used<=0)return null;
  const billableTokens=(Array.isArray(list)?list:[]).reduce((sum,item)=>{
    if(!item||item.has_limit)return sum;
    return sum+Math.max(0,Number(item.used)||0);
  },0);
  if(billableTokens<=0)return null;
  const allocated=accountCredits*used/billableTokens;
  return {accountCredits,allocated,perMillion:allocated*1000000/used};
}
function modelUsageStatsByID(accounts){
  const result=new Map();
  (accounts||lastAccounts||[]).forEach(account=>{
    const list=Array.isArray(account&&account.daily_free)?account.daily_free:[];
    list.forEach(model=>{
      const id=String(model&&model.model||'').trim();
      const key=modelIDKey(id);
      if(!key)return;
      const used=Math.max(0,Number(model.used)||0);
      const stats=result.get(key)||{tokens:0,requests:0,estimatedCredits:0,pricedTokens:0,dailyLimitTokens:0};
      stats.tokens+=used;
      stats.requests+=Math.max(0,Number(model.requests)||0);
      if(model.has_limit){
        stats.dailyLimitTokens+=used;
      }else{
        const estimate=estimateAccountModelCredits(list,model,account&&account.credits);
        if(estimate){stats.estimatedCredits+=estimate.allocated;stats.pricedTokens+=used;}
      }
      result.set(key,stats);
    });
  });
  return result;
}
function modelUsageLabel(stats){
  if(!stats||stats.tokens<=0)return '今日暂无本地用量';
  const parts=['今日 '+fmtTokens(stats.tokens)+' tokens'];
  if(stats.requests>0)parts.push(fmtNum(stats.requests)+' 次请求');
  if(stats.pricedTokens>0){
    parts.push('估算 '+fmtCredits(stats.estimatedCredits)+' 积分');
    parts.push(fmtCredits(stats.estimatedCredits*1000000/stats.pricedTokens)+' 积分/百万 tokens');
  }else if(stats.dailyLimitTokens>0){
    parts.push('每日额度模型暂不估算积分');
  }else{
    parts.push('积分数据待刷新');
  }
  return parts.join(' · ');
}
function syncGlobalModelUsageRows(){
  const statsByID=modelUsageStatsByID();
  document.querySelectorAll('#globalModelsBody .global-model-item').forEach(row=>{
    const usage=row.querySelector('.global-model-usage');
    if(usage)usage.textContent=modelUsageLabel(statsByID.get(modelIDKey(row.dataset.modelId)));
  });
}
async function loadGlobalModelCatalog(btn,force){
  const box=document.getElementById('globalModelsBody');
  if(!box)return;
  if(globalModelCatalogLoaded&&!force){filterGlobalModels();return;}
  const requestSeq=++globalModelRequestSeq;
  const status=document.getElementById('modelSettingsStatus');
  box.innerHTML='<div class="loading">正在载入模型目录…</div>';
  if(status)status.textContent='正在读取账号模型与静态目录…';
  if(btn)busy(btn,true);
  try{
    await waitForConfigWrites();
    if(requestSeq!==globalModelRequestSeq)return;
    const revision=configMutationRevision;
    const results=await Promise.all([apiConfig('GET'),api('/models/catalog')]);
    if(requestSeq!==globalModelRequestSeq)return;
    if(revision!==configMutationRevision){queueMicrotask(()=>{if(requestSeq===globalModelRequestSeq)loadGlobalModelCatalog(null,true)});return;}
    const cfg=results[0]||{};
    const data=results[1]||{};
    if(cfg.error)throw new Error(cfg.error);
    if(data.error)throw new Error(data.error);
    disabledModelIDs=Array.isArray(cfg.models_disabled)?cfg.models_disabled:[];
    enabledModelIDs=Array.isArray(cfg.models_enabled)?cfg.models_enabled:[];
    const list=Array.isArray(data.models)?data.models:[];
    const usageByModel=modelUsageStatsByID();
    globalModelItems=list;
    updateGlobalModelBadge();
    if(!list.length){
      box.innerHTML='<div class="md-empty">当前没有可管理的模型。已保存的启用或禁用项仍会显示在目录中。</div>';
      globalModelCatalogLoaded=true;
      if(status)status.textContent='目录已载入。模型状态全局共享；更改会保存并由 CPA 异步重新应用。';
      updateGlobalModelBadge();
      return;
    }
    const rows=list.map(m=>{
      const id=String(m&&m.id||'').trim();
      if(!id)return '';
      const name=String(m.name||'');
      const disabled=!!m.disabled||isGloballyDisabledModelCached(id);
      const details=[];
      if(m.context_length)details.push('上下文 '+fmtTokens(m.context_length));
      if(m.max_completion_tokens)details.push('输出上限 '+fmtTokens(m.max_completion_tokens));
      const detail=details.join(' · ');
      const title=name&&name!==id?'<span class="global-model-title">'+esc(name)+'</span>':'';
      const usage=modelUsageLabel(usageByModel.get(modelIDKey(id)));
      const search=(id+' '+name+' '+detail).toLowerCase();
      return '<div class="md-item global-model-item" data-model-id="'+esc(id)+'" data-search="'+esc(search)+'" data-disabled="'+(disabled?'true':'false')+'"><label class="global-model-label"><span class="global-model-toggle-wrap"><input type="checkbox" class="global-model-toggle" data-model-id="'+esc(id)+'"'+(!disabled?' checked':'')+' aria-label="启用 '+esc(id)+'"><span>启用</span></span><span class="global-model-name">'+title+'<code class="global-model-id">'+esc(id)+'</code>'+(detail?'<span class="global-model-meta">'+esc(detail)+'</span>':'')+'<span class="global-model-usage">'+esc(usage)+'</span></span><span class="model-state">'+(disabled?'已禁用':'已启用')+'</span></label></div>';
    }).filter(Boolean).join('');
    box.innerHTML='<div class="md-list">'+(rows||'<div class="md-empty">没有可显示的模型。</div>')+'<div id="globalModelFilterEmpty" class="md-empty" hidden></div></div>';
    box.querySelectorAll('.global-model-toggle').forEach(input=>input.addEventListener('change',()=>setGlobalModelEnabled(input.dataset.modelId,input.checked,input)));
    globalModelCatalogLoaded=true;
    syncDisabledModelRows();
    updateGlobalModelBadge();
    if(status)status.textContent='目录已载入。未勾选模型默认禁用；勾选即保存，由 CPA 异步重新应用于所有 WorkBuddy 账号。';
  }catch(e){
    if(requestSeq!==globalModelRequestSeq)return;
    globalModelCatalogLoaded=false;
    box.innerHTML='<div class="md-empty">加载失败：'+esc(e.message||String(e))+'</div>';
    if(status)status.textContent='模型目录载入失败，请检查管理密钥或刷新重试。';
    toast('模型目录加载失败','err',e.message||String(e));
  }finally{
    if(btn&&requestSeq===globalModelRequestSeq)busy(btn,false);
  }
}
function syncDisabledModelRows(){
  document.querySelectorAll('#globalModelsBody .global-model-item[data-model-id]').forEach(row=>{
    const disabled=isGloballyDisabledModelCached(row.dataset.modelId);
    row.dataset.disabled=disabled?'true':'false';
    const input=row.querySelector('.global-model-toggle');
    const label=row.querySelector('.model-state');
    if(input){input.checked=!disabled;input.setAttribute('aria-label','启用 '+row.dataset.modelId)}
    if(label)label.textContent=disabled?'已禁用':'已启用';
  });
  filterGlobalModels();
}
function setGlobalModelEnabled(id,enabled,el){
  id=String(id||'').trim();
  const modelKey=modelIDKey(id);
  if(!modelKey)return;
  const pending={id,enabled:!!enabled};
  configMutationRevision++;
  pendingModelChanges.set(modelKey,pending);
  if(el)el.disabled=true;
  syncDisabledModelRows();
  updateGlobalModelBadge();
  const status=document.getElementById('modelSettingsStatus');
  if(status)status.textContent='正在保存模型启用设置…';
  globalModelSaveQueue=globalModelSaveQueue.catch(()=>{}).then(async()=>{
    await settingsWriteQueue.catch(()=>{});
    const cfg=await apiConfig('GET');
    if(cfg&&cfg.error)throw new Error(cfg.error);
    const currentEnabled=Array.isArray(cfg.models_enabled)?cfg.models_enabled:[];
    const currentDisabled=Array.isArray(cfg.models_disabled)?cfg.models_disabled:[];
    const nextEnabled=currentEnabled.filter(x=>modelIDKey(x)!==modelKey);
    const nextDisabled=currentDisabled.filter(x=>modelIDKey(x)!==modelKey);
    if(enabled)nextEnabled.push(id);else nextDisabled.push(id);
    await apiConfig('PATCH',{models_enabled:nextEnabled,models_disabled:nextDisabled});
    enabledModelIDs=nextEnabled;
    disabledModelIDs=nextDisabled;
    if(pendingModelChanges.get(modelKey)===pending)pendingModelChanges.delete(modelKey);
    syncDisabledModelRows();
    updateGlobalModelBadge();
    if(status)status.textContent='已保存，CPA 正在异步重新应用。';
    toast(enabled?'模型已全局启用':'模型已全局禁用','ok',id);
  }).catch(e=>{
    if(pendingModelChanges.get(modelKey)===pending)pendingModelChanges.delete(modelKey);
    syncDisabledModelRows();
    updateGlobalModelBadge();
    if(status)status.textContent='保存失败：'+(e.message||String(e));
    toast('保存失败','err',e.message||String(e));
  }).finally(()=>{if(el)el.disabled=false;});
}
function openTasksModal(idx){
  currentTaskAuth=idx||'';
  const requestSeq=++tasksModalGeneration;
  const a=(lastAccounts||[]).find(x=>x.auth_index===currentTaskAuth)||{};
  currentTaskName=a.nickname||currentTaskAuth;
  openDialog("tasksModal");
  document.getElementById('tasksModalTitle').textContent='成长任务 · '+currentTaskName;
  const unsupported=(a.region||'cn')!=='cn';
  document.getElementById('tasksAcceptAllBtn').style.display=unsupported?'none':'';
  document.getElementById('tasksLightBtn').style.display=unsupported?'none':'';
  document.getElementById('tasksTravelBtn').style.display=unsupported?'none':'';
  document.getElementById('tasksRefreshBtn').style.display=unsupported?'none':'';
  document.getElementById('tasksBody').innerHTML=unsupported?'<div class="task-meta">成长任务当前仅支持 CN 账号，Intl 账号不发送 CN 活动请求。</div>':'<div class="loading">加载中…</div>';
  if(!unsupported)loadTasks(currentTaskAuth,false,requestSeq);
}
function closeTasksModal(){tasksModalGeneration++;tasksRequestSeq++;currentTaskAuth='';currentTaskName='';closeDialog('tasksModal')}
function onTasksMaskClick(e){if(e.target===document.getElementById('tasksModal'))closeTasksModal()}
async function loadTasks(idx,notify,modalSeq){
  if(!idx)return;
  const requestSeq=++tasksRequestSeq;
  const generation=modalSeq==null?tasksModalGeneration:modalSeq;
  const isCurrent=()=>requestSeq===tasksRequestSeq&&generation===tasksModalGeneration&&idx===currentTaskAuth;
  try{
    const data=await api('/tasks?auth_index='+encodeURIComponent(idx));
    if(!isCurrent())return;
    renderTasks(data);
    if(notify)toast('任务已刷新','ok',currentTaskName);
  }catch(e){
    if(!isCurrent())return;
    renderTasks({error:e.message||String(e)});
    if(notify)toast('任务刷新失败','err',e.message||String(e));
  }
}

async function acceptAllTasks(idx,btn){
  if(!idx)return; busy(btn,true);
  try{
    const d=await api('/tasks/accept_all',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({auth_index:idx})});
    if(d.error){toast(d.busy?'任务正在运行':'接受任务失败',d.busy?'warn':'err',d.error);return}
    if(d.run_id){
      toast('任务接受已开始','ok','后台运行中');
      await pollTaskRun(d.run_id,idx);
    }else{
      toast('任务接受完成','ok','已接受 '+(d.accepted||0)+' 个');
      await loadTasks(idx,false);
    }
  }catch(e){toast('接受任务失败','err',e.message||String(e))}finally{busy(btn,false)}
}
async function lightAllTasks(idx,btn){
  if(!idx)return; busy(btn,true);
  try{
    const d=await api('/tasks/light',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({auth_index:idx})});
    if(d.error){toast(d.busy?'任务正在运行':'点亮失败',d.busy?'warn':'err',d.error);return}
    if(d.run_id){
      toast('一键点亮已开始','ok','自动接取+上报+领奖，请稍候');
      await pollTaskRun(d.run_id,idx);
    }
  }catch(e){toast('点亮失败','err',e.message||String(e))}finally{busy(btn,false)}
}
async function runTravel(idx,btn){
  if(!idx)return; busy(btn,true);
  try{
    const d=await api('/tasks/travel',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({auth_index:idx})});
    if(d.error){toast('猫猫旅行失败','err',d.error);return}
    toast('猫猫旅行','ok',d.message||d.action||'已完成');
    await loadTasks(idx,false);
  }catch(e){toast('猫猫旅行失败','err',e.message||String(e))}finally{busy(btn,false)}
}
async function pollTaskRun(runId,idx){
  const deadline=Date.now()+120000;
  while(Date.now()<deadline){
    await new Promise(r=>setTimeout(r,900));
    let d;
    try{d=await api('/tasks/status?run_id='+encodeURIComponent(runId)+'&auth_index='+encodeURIComponent(idx));}
    catch(e){toast('任务状态查询失败','err',e.message||String(e));return}
    if(d.error){toast('任务状态查询失败','err',d.error);return}
    if(d.status==='succeeded'){toast('任务接受完成','ok','已接受 '+(d.accepted||0)+' 个');await loadTasks(idx,false);return}
    if(d.status==='failed'){toast('任务接受部分失败','warn',d.error||'可重试');await loadTasks(idx,false);return}
  }
  toast('任务仍在后台运行','warn','可稍后刷新任务状态');
}
async function claimGrowthTask(code,btn){
  if(!currentTaskAuth||!code)return; busy(btn,true);
  const idx=currentTaskAuth;
  try{const d=await api('/tasks/claim',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({auth_index:idx,task_code:code})});if(d.error)toast('领取失败','err',d.error);else if(d.already_claimed)toast('任务已领取','warn',code);else{toast('任务奖励已领取','ok',code);await loadTasks(idx,false)}}catch(e){toast('领取失败','err',e.message||String(e))}finally{busy(btn,false)}
}

function esc(s){return String(s??"").replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]))}
function progressHTML(credits,pending){
  const state=creditOf({credits});
  if(!state.known)return '<div class="pb"><div class="pb-label"><span>可用积分</span><span>'+(pending?"正在获取…":"额度未加载")+'</span></div><div class="pb-track"><div class="pb-bar" style="width:0%"></div></div></div>';
  const remain=state.remain,used=state.used;
  const total=state.size>0?state.size:remain+used;
  const pct=total>0?Math.min(100,Math.max(0,Math.round(used/total*100))):0;
  let color='var(--ok)';if(pct>60)color='var(--warn)';if(pct>85)color='var(--err)';
  const when=credits&&credits.fetched_at?' · 快照 '+esc(String(credits.fetched_at).replace('T',' ').replace('Z',' UTC')):'';
  const packCount=credits&&Number(credits.pack_count)>0?' · '+Number(credits.pack_count)+' 个包':'';
  return '<div class="pb"><div class="pb-label"><span>可用 '+remain+'</span><span>已用 '+used+' · '+pct+'%</span></div><div class="pb-track"><div class="pb-bar" style="width:'+pct+'%;background:'+color+'"></div></div><div class="pb-meta">剩余 '+remain+' · 已用 '+used+' · 额度池 '+total+packCount+when+'</div></div>';
}
function bindCardActions(root){
  (root||document).querySelectorAll("[data-action]").forEach(btn=>{
    if(btn._bound) return;
    btn._bound=true;
    btn.addEventListener("click", ev=>{
      const act=ev.currentTarget.dataset.action;
      const idx=ev.currentTarget.dataset.authIndex;
      if(act==="checkin") checkin(idx, ev.currentTarget);
      else if(act==="claim") claimTrial(idx, ev.currentTarget);
      else if(act==="select") selectAuth(idx, ev.currentTarget);
      else if(act==="refresh") refreshCredits(idx, ev.currentTarget);
      else if(act==="tasks") openTasksModal(idx);
      else if(act==="models") openModelsModal(idx, ev.currentTarget.dataset.authIndex);
      else if(act==="task-claim") claimGrowthTask(ev.currentTarget.dataset.taskCode, ev.currentTarget);
    });
  });
}
// Model-source line (v0.9.9): tells the user WHERE this realm's model list
// came from — dynamic discovery, config pin, or the static fallback — and, on
// fallback, the recorded discovery failure reason (hover for full text).
function card(a){
  const plan=(a.plan||"unknown").toLowerCase();
  const ci=a.checkin||{}, cr=a.credits;
  const exhausted=isAccountExhausted(a);
  const region=a.region||"cn";
  const isIntl=region==="intl"||region==="global";
  const isWorkBuddy=!!a.trial_eligible||!!a.trial_claimed;
  const disabled=!!a.disabled;
  const selected=!!a.selected;
  const rt=a.runtime;
  const cooling=!!(rt&&rt.unavailable&&rt.cooldown_seconds>0);
  // 卡片状态色：冷却 > 禁用 > 耗尽 > 使用中 > 正常
  const tone = cooling?"cool":(disabled?"off":(exhausted?"warn":(selected?"on":"")));
  const regionLabel=isIntl?"Intl":"CN";
  const regionBadge=`<span class="badge ${isIntl?"global":region}">${regionLabel}</span>`;

  let actionHTML;
  if(isWorkBuddy){
    actionHTML=a.trial_claimed
      ? '<button class="done" disabled="disabled">已领取</button>'
      : `<button class="sec compact-claim" data-action="claim" data-auth-index="${esc(a.auth_index)}" title="领取专家加油包"><span class="action-label-full">领取专家加油包</span><span class="action-label-short">领取</span></button>`;
  }else if(isIntl){
    actionHTML='<span class="action-note">Intl 服务免签到</span>';
  }else{
    const checked=ci.today_checked_in;
    actionHTML=`<button class="${checked?"done":"sec"}" data-action="checkin" data-auth-index="${esc(a.auth_index)}"${checked?" disabled=\"disabled\"":""}>${checked?"已签到":"签到"}</button>`;
  }
  const selectBtn=selected
    ? `<button class="done" disabled="disabled" title="插件偏好账号；实际路由仍由 CPA 调度模式决定">已选用</button>`
    : (disabled?``:`<button class="sec" data-action="select" data-auth-index="${esc(a.auth_index)}">选用</button>`);

  const badges=(selected?`<span class="badge on" title="插件偏好账号；默认轮询模式下 CPA 主机仍决定实际路由">已选用</span>`:"")
    +(cooling?`<span class="badge cooling" title="解除时间 ${esc(fmtClock(rt.cooldown_until))}${rt.status_message?` · ${esc(rt.status_message)}`:""}">冷却 ${fmtDuration(rt.cooldown_seconds)}</span>`:"")
    +(disabled?`<span class="badge disabled">已禁用</span>`:"")
    +(exhausted?`<span class="badge exhausted">耗尽</span>`:"")
    +regionBadge
    +`<span class="badge ${plan}">${esc(a.plan||"-")}</span>`;

  const searchText=[a.nickname,a.label,a.name,a.email,a.uid,a.auth_index,region,a.plan].filter(Boolean).join(" ").toLowerCase();
  const cardError=a.error||((a.credit_error)?"积分读取失败："+a.credit_error:"");
  return `<div class="card tone-${tone}${selected?" selected":""}" data-region="${esc(region)}" data-auth="${esc(a.auth_index)}" data-search="${esc(searchText)}" data-exhausted="${exhausted?"true":"false"}">
  <div class="card-hd">
    <div class="card-title">
      <span class="card-name" title="${esc(a.nickname||a.label||a.name)}">${esc(a.nickname||a.label||a.name)}</span>
      <div class="card-badges">${badges}</div>
    </div>
    <div class="card-sub">${esc(a.uid||"")}${a.uid&&a.name?" · ":""}${esc(a.name||"")}</div>
  </div>
  ${cardError?`<div class="card-err">${esc(cardError)}</div>`:""}
  <div class="card-body">
    ${progressHTML(cr,!a.disabled&&!a.error&&!a.credit_error)}
    ${healthBarHTML(rt&&rt.health)}
    ${dailyFreeHTML(a.daily_free,a.credits)}
  </div>
  <div class="actions">
    <div class="actions-main">
      <button class="sec btn-models" data-action="models" data-auth-index="${esc(a.auth_index)}" title="查看该账号支持的模型"><span class="btn-ico">≡</span><span class="btn-text">模型</span></button>
      <div class="actions-util">
        <button class="icon-btn" data-action="refresh" data-auth-index="${esc(a.auth_index)}" title="刷新积分" aria-label="刷新积分">↻</button>
        ${isWorkBuddy?"":`<button class="icon-btn" data-action="tasks" data-auth-index="${esc(a.auth_index)}" title="成长任务" aria-label="成长任务">✦</button>`}
      </div>
      ${actionHTML}
    </div>
    <div class="toggle-wrap">${selectBtn}</div>
  </div>
  </div>`;
}
// 密度切换：偏好存 localStorage，刷新后保持。
// 账号多时紧凑模式能显著减少滚动 —— 这是原生的 gridCompact 做的同一件事。
function toggleDensity(){
  const g=document.getElementById("grid");
  const btn=document.getElementById("densityBtn");
  if(!g) return;
  const compact=g.classList.toggle("compact");
  try{ localStorage.setItem("wb_density", compact?"compact":"normal"); }catch(e){}
  if(btn){btn.textContent=compact?"常规视图":"紧凑视图";btn.setAttribute("aria-pressed",compact?"true":"false");}
}
function restoreDensity(){
  let compact=false;
  try{ compact=localStorage.getItem("wb_density")==="compact"; }catch(e){}
  if(!compact) return;
  const g=document.getElementById("grid");
  const btn=document.getElementById("densityBtn");
  if(g) g.classList.add("compact");
  if(btn){btn.textContent="常规视图";btn.setAttribute("aria-pressed","true");}
}
let currentFilter="all";
let currentSearch="";
let dashboardRequestSeq=0;
let selectionRefreshGeneration=0;
const creditRefreshSeqByAuth=new Map();
let lastAccounts=[];
let lastRequests=null;
let activeAuthId="";
let currentTaskAuth="";
let currentTaskName="";
let authFailCount=0;
let authFailUntil=0;
// Lazy-load: fetch credits for all cards that lack them, concurrently.
// Each card fetches independently; failed ones retry once after 2s.
const CREDIT_CONCURRENCY=4;
const creditJobQueue=[];
let activeCreditJobs=0;
function enqueueCreditJob(work){
  return new Promise((resolve,reject)=>{
    creditJobQueue.push({work,resolve,reject});
    drainCreditJobQueue();
  });
}
function drainCreditJobQueue(){
  while(activeCreditJobs<CREDIT_CONCURRENCY&&creditJobQueue.length){
    const job=creditJobQueue.shift();
    activeCreditJobs++;
    Promise.resolve().then(job.work).then(job.resolve,job.reject).finally(()=>{activeCreditJobs--;drainCreditJobQueue()});
  }
}
async function lazyLoadCredits(accounts,generation){
  const requestGeneration=generation==null?dashboardRequestSeq:generation;
  const need=(accounts||[]).filter(a=>!creditOf(a).known&&!a.error&&!a.disabled&&!a.credit_error);
  if(!need.length)return;
  await Promise.all(need.map(account=>enqueueCreditJob(async()=>{
    if(requestGeneration!==dashboardRequestSeq)return;
    const idx=String(account.auth_index||"");
    try{
      const data=await api('/credits?auth_index='+encodeURIComponent(idx));
      if(requestGeneration!==dashboardRequestSeq)return;
      const current=lastAccounts.find(item=>String(item.auth_index||"")===idx);
      if(!current)return;
      const result=(data&&Array.isArray(data.accounts)?data.accounts[0]:null)||data||{};
      if(data&&data.error)current.credit_error=data.error;
      else if(result.error)current.credit_error=result.error;
      else if(result.credits&&creditOf({credits:result.credits}).known){
        current.credits=result.credits;
        current.credit_error="";
        if(result.exhausted!=null)current.exhausted=!!result.exhausted;
        if(result.trial_claimed!=null)current.trial_claimed=!!result.trial_claimed;
        if(result.plan)current.plan=result.plan;
      }else current.credit_error="积分响应中未包含有效额度字段";
      updateOneCard(current);
    }catch(error){
      if(requestGeneration!==dashboardRequestSeq)return;
      const current=lastAccounts.find(item=>String(item.auth_index||"")===idx);
      if(current){current.credit_error=error.message||"积分查询失败";updateOneCard(current)}
    }
  })));
  await refreshDashboardSelection(requestGeneration);
}
async function refreshDashboardSelection(generation){
  if(generation!==dashboardRequestSeq||selectionRefreshGeneration===generation)return;
  const all=lastAccounts||[];
  if(!all.length||!all.every(account=>creditOf(account).known||account.error||account.credit_error||account.disabled))return;
  selectionRefreshGeneration=generation;
  try{
    const data=await api("/accounts");
    if(generation!==dashboardRequestSeq||!data||data.error)return;
    activeAuthId=data.active_auth||activeAuthId;
    const byIndex=new Map((data.accounts||[]).map(account=>[String(account.auth_index||""),account]));
    all.forEach(account=>{
      const fresh=byIndex.get(String(account.auth_index||""));
      if(!fresh)return;
      if(fresh.selected!=null)account.selected=!!fresh.selected;
      if(fresh.exhausted!=null)account.exhausted=!!fresh.exhausted;
    });
    renderAccountGrid(all);
  }catch(_){/* the current dashboard remains usable without selection reconciliation */}
}
function renderAccountGrid(accounts){
  const grid=document.getElementById("grid");
  if(!grid)return;
  lastAccounts=Array.isArray(accounts)?accounts:[];
  if(!lastAccounts.length){
    grid.innerHTML='<div class="loading">暂无 WorkBuddy 账号。导入凭证后会显示在这里。</div>';
    updateFilterCounts(lastAccounts);
    applyAccountFilters();
    return;
  }
  grid.innerHTML=lastAccounts.map(card).join("");
  bindCardActions(grid);
  updateFilterCounts(lastAccounts);
  applyAccountFilters();
  syncGlobalModelUsageRows();
}
function updateOneCard(account){
  const idx=String(account&&account.auth_index||"");
  const current=lastAccounts.find(item=>String(item.auth_index||"")===idx);
  if(!current)return;
  const el=Array.from(document.querySelectorAll("#grid .card")).find(node=>String(node.dataset.auth||"")===idx);
  if(!el){renderAccountGrid(lastAccounts);return;}
  const temp=document.createElement("div");
  temp.innerHTML=card(current);
  if(temp.firstElementChild){
    const next=temp.firstElementChild;
    el.replaceWith(next);
    bindCardActions(next);
  }
  applyAccountFilters();
  syncGlobalModelUsageRows();
}

function filterRegion(region,btn){
  currentFilter=region||"all";
  document.querySelectorAll(".ftag").forEach(button=>{
    const active=button.dataset.region===currentFilter;
    button.classList.toggle("active",active);
    button.setAttribute("aria-pressed",active?"true":"false");
  });
  applyAccountFilters();
}
function filterAccounts(){
  currentSearch=(document.getElementById("accountSearch")?.value||"").trim().toLowerCase();
  applyAccountFilters();
}
function accountMatchesSearch(account,query){
  if(!query)return true;
  return [account&&account.nickname,account&&account.label,account&&account.name,account&&account.uid,account&&account.auth_index,account&&account.email]
    .filter(Boolean).join(" ").toLowerCase().includes(query);
}
function applyAccountFilters(){
  const query=(document.getElementById("accountSearch")?.value||currentSearch||"").trim().toLowerCase();
  currentSearch=query;
  let visible=0;
  document.querySelectorAll("#grid .card").forEach(cardElement=>{
    const region=String(cardElement.dataset.region||"cn").toLowerCase();
    const matchesRegion=currentFilter==="all"||(currentFilter==="exhausted"?cardElement.dataset.exhausted==="true":currentFilter==="intl"?(region==="intl"||region==="global"):region===currentFilter);
    const matchesText=!query||(cardElement.dataset.search||"").includes(query);
    const show=matchesRegion&&matchesText;
    cardElement.hidden=!show;
    if(show)visible++;
  });
  const empty=document.getElementById("accountEmptyState");
  if(empty){
    const noMatch=(lastAccounts||[]).length>0&&visible===0;
    empty.hidden=!noMatch;
    const title=empty.querySelector("strong");
    const detail=empty.querySelector("span");
    if(title)title.textContent=query?"没有匹配的账号":"此筛选下没有账号";
    if(detail)detail.textContent=query?"尝试更短的名称、邮箱或 UID。":"切换筛选条件，或检查账号状态。";
  }
  renderSummary(lastAccounts);
}
function updateFilterCounts(accounts){
  let cn=0,intl=0,exhausted=0;
  (accounts||[]).forEach(account=>{
    const region=String(account.region||"cn").toLowerCase();
    if(region==="intl"||region==="global")intl++;else cn++;
    if(isAccountExhausted(account))exhausted++;
  });
  const all=document.getElementById("cntAll");
  const cnNode=document.getElementById("cntCn");
  const intlNode=document.getElementById("cntIntl");
  const exhaustedNode=document.getElementById("cntExhausted");
  if(all)all.textContent=String((accounts||[]).length);
  if(cnNode)cnNode.textContent=String(cn);
  if(intlNode)intlNode.textContent=String(intl);
  if(exhaustedNode)exhaustedNode.textContent=String(exhausted);
}
function accountsForFilter(accounts){
  const query=(document.getElementById("accountSearch")?.value||currentSearch||"").trim().toLowerCase();
  return (accounts||[]).filter(account=>{
    const region=String(account.region||"cn").toLowerCase();
    const matchesRegion=currentFilter==="all"||(currentFilter==="exhausted"?isAccountExhausted(account):currentFilter==="intl"?(region==="intl"||region==="global"):region===currentFilter);
    return matchesRegion&&accountMatchesSearch(account,query);
  });
}
function creditOf(account){
  const credits=account&&account.credits;
  if(!credits||typeof credits!=="object")return {remain:0,used:0,size:0,known:false};
  const owns=key=>Object.prototype.hasOwnProperty.call(credits,key);
  const amount=key=>{const value=Number(credits[key]);return Number.isFinite(value)?Math.max(0,value):0};
  const remain=amount("total_remain"),used=amount("total_used"),size=amount("total_size");
  const known=["total_remain","total_used","total_size","fetched_at","packages"].some(owns);
  return {remain,used,size,known};
}
function isAccountExhausted(account){
  if(account&&account.exhausted===true)return true;
  const credits=creditOf(account);
  if(!credits.known||credits.remain>0)return false;
  const packages=account&&account.credits&&account.credits.packages;
  return credits.used>0||credits.size>0||(Array.isArray(packages)&&packages.length>0);
}

// 每账号健康条。
//
// 数据来自**宿主**：CPA 自己按凭据维护 20 × 10 分钟的 success/failed 分桶
// （recent_requests），把每个路由到该凭据的请求记进去，经
// host.auth.get_runtime 下发。插件只做展示 —— 自己统计只能看到到达本插件
// 执行器的请求，会漏掉从未到达的，也无法归属到账号。
//
// 关键语义（照抄原生）：**空闲格与全失败格必须区分**。灰色=无请求，
// 红色=全失败；混用会让闲置账号看起来像故障。
function fmtDuration(sec){
  sec=Number(sec)||0;
  if(sec<=0) return "";
  if(sec<60) return sec+" 秒";
  if(sec<3600) return Math.round(sec/60)+" 分钟";
  if(sec<86400) return (sec/3600).toFixed(1)+" 小时";
  return (sec/86400).toFixed(1)+" 天";
}
function fmtClock(value){
  if(value==null||String(value).trim()==="")return "";
  const raw=String(value).trim();
  let date;
  if(typeof value==="number"||/^\d+(?:\.\d+)?$/.test(raw)){
    const numeric=Number(raw);
    date=new Date(numeric<1e12?numeric*1000:numeric);
  }else{
    date=new Date(raw);
  }
  if(!Number.isFinite(date.getTime()))return raw;
  return date.toLocaleString("zh-CN",{year:"numeric",month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit"});
}
// Compact token count: 1234567 -> "123.5万", 200000000 -> "2亿".
// 千分位数字（积分等精确值用这个，不用"万/亿"缩写 —— 积分粒度小，
// 缩写会让"剩余 1万"看不出是 10000 还是 19999）。
function fmtNum(n){
  n=Number(n);
  if(!isFinite(n)) return "-";
  return n.toLocaleString("en-US");
}
function fmtCredits(n){
  n=Number(n);
  if(!isFinite(n))return "-";
  return n.toLocaleString("en-US",{maximumFractionDigits:1});
}
function fmtTokens(n){
  n = Number(n)||0;
  if(n >= 100000000) return (n/100000000).toFixed(n%100000000===0?0:2)+"亿";
  if(n >= 10000) return (n/10000).toFixed(n%10000===0?0:1)+"万";
  return String(n);
}
// Daily FREE allowance per model (v0.9.34).
//
// Deliberately a SEPARATE block from the credits bar above it: credits are
// PURCHASED quota (account-wide, resets on the package cycle) while this is the
// FREE tier (per model, resets daily). One bar for both would imply a single
// budget when they are two independent ones.
//
// Token counts are local. Account-cycle credits are allocated by current-day
// model token share for an explicitly labeled estimate; they are not model bills.
function dailyFreeHTML(list,credits){
  if(!list||!list.length) return "";
  const rows = list.map(m=>{
    if(!m.has_limit){
      const estimate=estimateAccountModelCredits(list,m,credits);
      const detail=estimate
        ? `账号本周期已用 ${fmtNum(estimate.accountCredits)} 积分 · 本模型估算 ${fmtCredits(estimate.allocated)} 积分 · ${fmtCredits(estimate.perMillion)} 积分/百万 tokens`
        : (credits&&credits.total_used!=null
          ? `账号本周期已用 ${fmtNum(credits.total_used)} 积分 · 模型单价待本地用量可匹配`
          : "账号积分消耗待刷新");
      const note="估算值：将账号本周期积分按今日无每日限额模型 token 占比分摊；上游没有单模型积分账单。";
      return `<div class="dq-row">
        <div class="dq-head"><span class="dq-model">${esc(m.model)}</span><span class="dq-num">${fmtTokens(m.used)} tokens</span></div>
        <div class="dq-meta dq-credit-estimate" title="${esc(note)}">${esc(detail)}</div>
      </div>`;
    }
    const pct = Math.max(0,Math.min(100,m.used_percent||0));
    const color = pct>=90?"var(--danger,#e5484d)":(pct>=70?"#e8a33d":"var(--acc)");
    return `<div class="dq-row">
      <div class="dq-head">
        <span class="dq-model">${esc(m.model)}</span>
        <span class="dq-num">剩余 ${fmtTokens(m.remaining)} / ${fmtTokens(m.limit)}</span>
      </div>
      <div class="dq-track"><div class="dq-bar" style="width:${pct}%;background:${color}"></div></div>
      <div class="dq-meta">已用 ${fmtTokens(m.used)} (${pct}%) · 今日 ${m.requests||0} 次${m.failed_requests?` · 失败 ${m.failed_requests}`:""}${m.limit_source==="config"?" · 限额来自配置":""}</div>
    </div>`;
  }).join("");
  return `<div class="dq"><div class="dq-title">模型用量 <span class="dq-hint" title="token 为本机统计；模型积分是账号本周期总积分按今日模型 token 占比估算，不是上游模型级账单。">本机统计/积分估算</span></div>${rows}</div>`;
}

function healthBarHTML(health){
  const h=health||[];
  if(!h.length) return "";
  const total=h.reduce((a,b)=>a+(b.success||0)+(b.failed||0),0);
  if(!total) return "";  // 该账号完全无流量时不占版面
  const blocks=h.map(b=>{
    const ok=b.success||0, bad=b.failed||0, n=ok+bad;
    const label=b.label||"";
    if(n===0) return `<div class="hb-block hb-idle" title="${esc(label)} · 无请求"></div>`;
    const rate=Math.round(ok*100/n);
    const cls=bad===0?"hb-ok":(ok===0?"hb-bad":"hb-mixed");
    const style=`background:color-mix(in srgb, var(--err) ${Math.round((1-ok/n)*100)}%, var(--ok))`;
    return `<div class="hb-block ${cls}" style="${style}" title="${esc(label)} · 成功 ${ok} · 失败 ${bad} · ${rate}%"></div>`;
  }).join("");
  const ok=h.reduce((a,b)=>a+(b.success||0),0);
  const rate=Math.round(ok*100/total);
  const rateCls=rate>=90?"hb-rate-hi":(rate>=50?"hb-rate-mid":"hb-rate-lo");
  return `<div class="hb"><div class="hb-blocks">${blocks}</div><span class="hb-rate ${rateCls}">${rate}%</span></div>`;
}
// 请求终态告警（v0.9.37）。
//
// 这是唯一能看到"请求到了但没被服务"的地方：UsagePlugin 只覆盖到达执行器的
// 请求，全部凭据不可用时不会产生 usage 记录。宿主通过 request.complete 上报
// 终态，这里只做展示，不改任何调度状态。
function requestsHTML(){
  const r=lastRequests;
  if(!r) return "";
  const c=r.counts||{};
  const total=(c.succeeded||0)+(c.failed||0)+(c.rejected||0)+(c.canceled||0);
  if(!total) return "";
  const parts=[];
  if(c.succeeded) parts.push(`成功 ${c.succeeded}`);
  if(c.failed)    parts.push(`失败 ${c.failed}`);
  if(c.rejected)  parts.push(`被拒 ${c.rejected}`);
  if(c.canceled)  parts.push(`取消 ${c.canceled}`);
  const alarm=r.all_failing?`<div class="req-alarm">连续 ${r.fail_streak} 个请求未成功 —— 可能所有 workbuddy 账号都无法服务，请检查额度/凭据</div>`:"";
  const last=(r.events||[]).slice(0,3).map(e=>{
    const t=(e.error||"").replace(/\s+/g," ").slice(0,90);
    return `<div class="req-line"><span class="req-out req-${esc(e.outcome||"")}">${esc(e.outcome||"?")}</span> <span class="req-model">${esc(e.model||"-")}</span>${e.status?` <span class="req-status">${e.status}</span>`:""}${t?` <span class="req-err" title="${esc(e.error||"")}">${esc(t)}</span>`:""}</div>`;
  }).join("");
  return `<div class="pb-meta" style="margin-top:10px">近期请求（1 小时）：${parts.join(" · ")}</div>${alarm}${last}`;
}
function renderSummary(accounts){
  const box=document.getElementById("summaryBox");
  if(!box) return;
  const all=accounts||[];
  if(!all.length){
    box.style.display="none";
    box.innerHTML="";
    return;
  }
  const scoped=accountsForFilter(all);
  let remain=0, used=0, size=0, knownN=0, disabledN=0, exhaustedN=0;
  scoped.forEach(a=>{
    const c=creditOf(a);
    if(c.known){ remain+=c.remain; used+=c.used; size+=c.size; knownN++; }
    if(a.disabled) disabledN++;
    if(isAccountExhausted(a)) exhaustedN++;
  });
  let cnRemain=0, cnUsed=0, intlRemain=0, intlUsed=0;
  let allRemain=0, allUsed=0, allSize=0, allKnownN=0;
  all.forEach(a=>{
    const c=creditOf(a);
    if(!c.known) return;
    allRemain+=c.remain; allUsed+=c.used; allSize+=c.size; allKnownN++;
    if((a.region||"cn")==="intl"||(a.region||"")==="global"){ intlRemain+=c.remain; intlUsed+=c.used; }
    else { cnRemain+=c.remain; cnUsed+=c.used; }
  });
  const total=size>0?size:(remain+used);
  const pct=total>0?Math.min(100,Math.max(0,Math.round(used/total*100))):0;
  const totalLabel=knownN?String(total):"-";
  let barColor="var(--ok)"; if(pct>60)barColor="var(--warn)"; if(pct>85)barColor="var(--err)";
  const scopeLabel=currentFilter==="all"?"全部账号":(currentFilter==="intl"?"Intl 账号":(currentFilter==="exhausted"?"耗尽账号":"CN 账号"));
  const allTotal=allSize>0?allSize:(allRemain+allUsed);
  const allTotalLabel=allKnownN?String(allTotal):"-";
  box.style.display="block";
  box.innerHTML=`
    <div class="summary-hd">
      <div class="summary-title">用量汇总 · ${esc(scopeLabel)}</div>
      <div class="summary-meta">${scoped.length} 个账号 · 有数据 ${knownN} · 禁用 ${disabledN} · 耗尽 ${exhaustedN}</div>
    </div>
    <div class="summary-grid">
      <div class="summary-item"><div class="k">剩余(可用)</div><div class="v ok">${remain}</div></div>
      <div class="summary-item"><div class="k">已用(消耗)</div><div class="v ${pct>85?"err":(pct>60?"warn":"")}">${used}</div></div>
      <div class="summary-item"><div class="k">额度池</div><div class="v">${totalLabel}</div></div>
      <div class="summary-item"><div class="k">消耗占比</div><div class="v ${pct>85?"err":(pct>60?"warn":"ok")}">${knownN?pct+"%":"-"}</div></div>
    </div>
    <div class="pb">
      <div class="pb-label"><span>消耗进度（已用/额度池）</span><span>${knownN?pct+"%":"-"}</span></div>
      <div class="pb-track"><div class="pb-bar" style="width:${total?pct:0}%;background:${barColor}"></div></div>
      <div class="pb-meta">可用 ${remain} · 已用 ${used} · 池 ${total||0}（签到加包会抬高池和可用，不等于没消耗）</div>
    </div>
    ${currentFilter==="all"?`
    <div class="pb-meta" style="margin-top:10px">分区域：CN 可用 ${cnRemain} / 已用 ${cnUsed} · Intl 可用 ${intlRemain} / 已用 ${intlUsed}</div>
    `:`
    <div class="pb-meta" style="margin-top:10px">全部账号池：可用 ${allRemain} · 已用 ${allUsed} · 池 ${allTotalLabel}</div>
    `}
    ${requestsHTML()}
  `;
}

async function load(force,btn){
  const grid=document.getElementById("grid");
  const generation=++dashboardRequestSeq;
  if(!getKey()){
    showAuth();
    if(!grid.querySelector(".card"))grid.innerHTML='<div class="loading">请先连接 CPA 管理密钥。</div>';
    return;
  }
  if(force){if(!btn)btn=document.getElementById("refreshBtn");busy(btn,true)}
  try{
    const data=force?await api("/refresh",{method:"POST"}):await api("/accounts");
    if(generation!==dashboardRequestSeq)return;
    if(!data||data.error)throw new Error(data&&data.error||"账号数据暂不可用");
    lastRequests=data.requests||null;
    let statusLine="服务器时间 "+(data.server_time||"");
    const autoBits=[];
    if(data.checkin_auto)autoBits.push("签到");
    if(data.lifecycle_auto)autoBits.push("生命周期");
    if(data.growth_auto)autoBits.push("成长任务");
    if(data.travel_auto)autoBits.push("猫猫旅行");
    if(data.keepalive_auto)autoBits.push("保活");
    statusLine+=" · 自动: "+(autoBits.length?autoBits.join("/"):"全部关闭");
    const clock=document.getElementById("svtime");
    if(clock)clock.textContent=statusLine;
    activeAuthId=data.active_auth||"";
    const accounts=Array.isArray(data.accounts)?data.accounts:[];
    renderAccountGrid(accounts);
    const stale=document.getElementById("staleNotice");
    if(stale){stale.hidden=true;stale.textContent="";}
    if(force){
      const errCount=accounts.filter(account=>account.error).length;
      const lifecycleCount=Array.isArray(data.lifecycle)?data.lifecycle.length:0;
      let detail=accounts.length+" 个账号";
      if(errCount)detail+=" · "+errCount+" 个有错误";
      if(lifecycleCount)detail+=" · 生命周期调整 "+lifecycleCount+" 项（账号文件会保留，仅可能停用/恢复）";
      toast("账号同步完成",errCount?"warn":"ok",detail);
    }
    void lazyLoadCredits(accounts,generation);
  }catch(error){
    if(generation!==dashboardRequestSeq)return;
    const stale=document.getElementById("staleNotice");
    const message=error.message||String(error);
    if(lastAccounts.length){
      if(stale){stale.textContent="刷新失败，保留上次成功的数据："+message;stale.hidden=false;}
    }else{
      grid.innerHTML='<div class="loading">'+esc(message)+"</div>";
      renderSummary([]);
    }
    if(force)toast("刷新失败","err",message);
  }finally{
    if(force)busy(btn,false);
  }
}

function isCheckinAlreadyResult(result){
  if(!result||result.error||result.reason==="global")return false;
  if(result.reason==="already"||result.already_checked_in)return true;
  if(result.success)return false;
  return /already|已签过|已签到|今日已签到|今天已签到/i.test(String(result.message||""));
}
function isCheckinDoneResult(result){return !!(result&&result.success)||isCheckinAlreadyResult(result)}
function checkinResultToast(result,who){
  const name=result&&result.nickname||who||"账号";
  if(result&&result.error){toast(name+"：签到失败","err",result.error);return}
  const message=String(result&&result.message||"");
  if(result&&(result.reason==="global"||(result.skipped&&/国际版|不支持/.test(message)))){
    toast(name+"：国际版无需签到","warn",message||"成长/试用资格取决于服务端标记");return;
  }
  if(isCheckinAlreadyResult(result)){toast(name+"：今日已签到","ok",message);return}
  if(result&&result.success){toast(name+"：签到成功","ok","+"+(result.credit??result.daily_credit??"")+" 积分 · 连续 "+(result.streak_days??"?")+" 天");return}
  toast(name+"："+(message||"签到未完成"),"warn");
}

async function checkin(idx, btn){
  busy(btn,true);
  try{
    const d=await api("/checkin",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({auth_index:idx})});
    if(d&&d.error)throw new Error(d.error);
    const res=(d.results||[])[0]||{};
    checkinResultToast(res);
    if(isCheckinDoneResult(res)){
      // Instant UI — do NOT busy(false) which restores "签到" before load finishes
      markCheckinDone(btn);
      // Optimistic local state so partial re-renders stay correct
      const a=(lastAccounts||[]).find(x=>x.auth_index===idx);
      if(a){
        a.checkin=Object.assign({}, a.checkin||{}, {today_checked_in:true});
      }
    }else{
      busy(btn,false);
    }
    // Await reload so card matches server; cache was cleared on backend for this account
    await load(false);
  }catch(e){
    toast("签到请求失败","err",e.message);
    busy(btn,false);
  }
}
async function selectAuth(idx,btn){
  busy(btn,true);
  try{
    const data=await api("/select",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({auth_index:idx})});
    if(data&&data.error)throw new Error(data.error);
    activeAuthId=data.active_auth||idx;
    const name=data.nickname||idx;
    const region=data.region==="intl"||data.region==="global"?"Intl":"CN";
    toast("插件偏好已更新","ok",name+" · "+region+" · CPA 路由仍依调度模式决定");
    lastAccounts.forEach(account=>{
      const authId=String(account.auth_id||"");
      const authIndex=String(account.auth_index||"");
      account.selected=authIndex===String(idx)||authId===String(activeAuthId)||authIndex===String(activeAuthId);
    });
    renderAccountGrid(lastAccounts);
  }catch(error){toast("选用失败","err",error.message||String(error))}
  finally{busy(btn,false)}
}

async function refreshCredits(idx,btn){
  const authIndex=String(idx||"");
  const generation=dashboardRequestSeq;
  const seq=(creditRefreshSeqByAuth.get(authIndex)||0)+1;
  creditRefreshSeqByAuth.set(authIndex,seq);
  busy(btn,true);
  try{
    const data=await api('/credits?auth_index='+encodeURIComponent(authIndex));
    if(generation!==dashboardRequestSeq||creditRefreshSeqByAuth.get(authIndex)!==seq)return;
    const result=(data&&Array.isArray(data.accounts)?data.accounts[0]:null)||data||{};
    const creditState=creditOf({credits:result.credits});
    const account=lastAccounts.find(item=>String(item.auth_index||"")===authIndex);
    if((data&&data.error)||(result&&result.error)||!creditState.known){
      const message=data&&data.error||result&&result.error||"积分响应中未包含有效额度字段";
      if(account){account.credit_error=message;updateOneCard(account)}
      toast("积分刷新失败","err",message);
      return;
    }
    if(account){
      account.credits=result.credits;
      account.credit_error="";
      if(result.exhausted!=null)account.exhausted=!!result.exhausted;
      if(result.trial_claimed!=null)account.trial_claimed=!!result.trial_claimed;
      if(result.plan)account.plan=result.plan;
      updateOneCard(account);
    }
    toast("积分已刷新","ok",(result.nickname||authIndex)+" · 剩余 "+creditState.remain);
    selectionRefreshGeneration=0;
    await refreshDashboardSelection(generation);
  }catch(error){
    if(generation===dashboardRequestSeq&&creditRefreshSeqByAuth.get(authIndex)===seq)toast("积分刷新失败","err",error.message||String(error));
  }finally{busy(btn,false)}
}

async function checkinAll(btn){
  busy(btn,true);
  try{
    const data=await api("/checkin",{method:"POST",headers:{"Content-Type":"application/json"},body:"{}"});
    if(data&&data.error)throw new Error(data.error);
    const results=Array.isArray(data&&data.results)?data.results:[];
    const summary=data&&data.summary||{};
    const count=(value,fallback)=>value==null?fallback:(Number.isFinite(Number(value))?Math.max(0,Number(value)):fallback);
    const successful=results.filter(item=>item&&item.success&&item.reason!=="already"&&!item.skipped).length;
    const alreadyCount=results.filter(isCheckinAlreadyResult).length;
    const failures=results.filter(item=>!isCheckinDoneResult(item)&&!item.skipped&&item.reason!=="global").length;
    const ok=count(summary.success,successful);
    const already=count(summary.already,alreadyCount);
    const fail=count(summary.fail,failures);
    const skipped=count(summary.skipped_global,results.filter(item=>item&&(item.reason==="global"||item.skipped)).length);
    const eligible=count(summary.eligible,ok+fail);
    const details=[];
    if(skipped)details.push("跳过不支持签到的 Intl "+skipped);
    if(already)details.push("今日已签 "+already);
    if(eligible)details.push("待签 "+eligible);
    const detail=details.join(" · ");
    if(fail){
      const errors=results.filter(item=>item&&item.error).map(item=>(item.nickname||item.auth_index||"账号")+": "+item.error).join("；");
      toast("批量签到："+ok+" 成功 / "+fail+" 失败"+(already?" / "+already+" 已签":""),"err",errors||detail);
    }else if(ok){toast("批量签到："+ok+" 成功"+(already?" / "+already+" 今日已签":""),"ok",detail)}
    else if(already||skipped)toast("无需签到","ok",detail||"已完成今日签到或账号类型不支持");
    else toast("没有需要签到的账号","warn",detail||"没有可执行的签到任务");
    const doneIndices=new Set(results.filter(isCheckinDoneResult).map(item=>String(item.auth_index||"")).filter(Boolean));
    if(doneIndices.size){
      lastAccounts.forEach(account=>{
        if(!doneIndices.has(String(account.auth_index||"")))return;
        account.checkin={...(account.checkin||{}),today_checked_in:true};
        updateOneCard(account);
      });
    }
    await load(false);
  }catch(error){toast("批量签到失败","err",error.message||String(error))}
  finally{busy(btn,false)}
}

async function claimTrial(idx,btn){
  busy(btn,true);
  try{
    const data=await api("/trial",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({auth_index:idx})});
    if(data&&data.error)throw new Error(data.error);
    if(data&&data.already_claimed){toast((data.nickname||"账号")+"：已领取过","warn",data.message||"");markTrialDone(btn)}
    else if(data&&data.success){toast((data.nickname||"账号")+"：领取成功","ok","+250 积分 · 14 天有效");markTrialDone(btn)}
    else{toast((data&&data.nickname||"账号")+"：领取未完成","warn",data&&data.message||"服务端未确认领取成功");busy(btn,false)}
    await load(false);
  }catch(error){toast("领取请求失败","err",error.message||String(error))}
  finally{busy(btn,false)}
}
async function claimTrialAll(btn){
  busy(btn,true);
  try{
    const data=await api("/accounts");
    if(data&&data.error)throw new Error(data.error);
    const accounts=(data.accounts||[]).filter(account=>account.trial_eligible&&!account.trial_claimed);
    if(!accounts.length){toast("没有可领取的账号","warn","资格以服务端标记为准");return}
    let success=0,already=0,failed=0;
    const errors=[];
    for(const account of accounts){
      try{
        const result=await api("/trial",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({auth_index:account.auth_index})});
        if(result&&result.error){failed++;errors.push((account.nickname||account.auth_index)+": "+result.error)}
        else if(result&&result.already_claimed)already++;
        else if(result&&result.success)success++;
        else{failed++;errors.push((account.nickname||account.auth_index)+": "+(result&&result.message||"未确认领取成功"))}
      }catch(error){failed++;errors.push((account.nickname||account.auth_index)+": "+(error.message||String(error)))}
    }
    if(failed)toast("批量领取："+success+" 成功 / "+already+" 已领 / "+failed+" 失败","err",errors.slice(0,3).join("；"));
    else if(success)toast("批量领取："+success+" 成功"+(already?" / "+already+" 已领":""),"ok");
    else if(already)toast("符合条件的账号已领取","ok",already+" 个已领取");
    else toast("没有确认成功的领取","warn");
    await load(false);
  }catch(error){toast("批量领取失败","err",error.message||String(error))}
  finally{busy(btn,false)}
}

// Automation switches live in the workspace and PATCH only touched CPA config fields;
// CPA applies the resulting plugin configuration asynchronously.
function openImportModal(){openDialog("importModal","#importRaw")}
function closeImportModal(){closeDialog("importModal")}
function onImportMaskClick(e){
  if(e.target===document.getElementById("importModal")) closeImportModal();
}
async function importAuth(btn){
  const raw=(document.getElementById("importRaw").value||"").trim();
  if(!raw){toast("请粘贴凭证 JSON","warn");return}
  busy(btn,true);
  try{
    let payload={raw};
    try{payload={json:JSON.parse(raw)}}catch(_){}
    const d=await api("/import",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload)});
    if(d.success){
      toast("导入成功","ok",(d.nickname||d.uid||"")+ " → "+(d.file||d.name||""));
      document.getElementById("importRaw").value="";
      closeImportModal();
      load(true);
    }else toast("导入失败","err",d.error||"unknown");
  }catch(e){toast("导入请求失败","err",e.message)}
  busy(btn,false);
}
document.getElementById("keyInput").addEventListener("keydown",e=>{if(e.key==="Enter")saveKey()});
document.addEventListener("keydown",event=>{
  handleWorkspaceKeydown(event);
  const mask=activeDialog();
  if(!mask)return;
  const dialog=mask.querySelector('[role="dialog"]');
  if(event.key==="Escape"){
    event.preventDefault();
    if(mask.id==="modelsModal")closeModelsModal();
    else if(mask.id==="tasksModal")closeTasksModal();
    else if(mask.id==="importModal")closeImportModal();
    return;
  }
  if(event.key!=="Tab"||!dialog)return;
  const focusable=Array.from(dialog.querySelectorAll('button:not([disabled]),input:not([disabled]),textarea:not([disabled]),select:not([disabled]),a[href],[tabindex]:not([tabindex="-1"])'))
    .filter(element=>{
      if(element.hidden||element.closest("[hidden]")||element.getAttribute("aria-hidden")==="true")return false;
      const style=window.getComputedStyle?window.getComputedStyle(element):null;
      return !style||(style.display!=="none"&&style.visibility!=="hidden");
    });
  if(!focusable.length){event.preventDefault();dialog.focus();return}
  const first=focusable[0],last=focusable[focusable.length-1];
  if(event.shiftKey&&(document.activeElement===first||!dialog.contains(document.activeElement))){event.preventDefault();last.focus()}
  else if(!event.shiftKey&&(document.activeElement===last||!dialog.contains(document.activeElement))){event.preventDefault();first.focus()}
});
/* Start theme sync watcher (parent MutationObserver + standalone prefers-color-scheme listener). */
restoreDensity();
if(window.__wbThemeSync)window.__wbThemeSync();
if(getKey()){
  load(false);
}else{
  showAuth();
}
