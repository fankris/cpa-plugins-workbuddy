// Adapted from the original WorkBuddy panel; same-origin CPAMC auth only.
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

export { getKey, clearManagementKey };
export function setKey(value){rejectedManagementKey="";safeSessionSet(value)}
