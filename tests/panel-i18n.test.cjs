const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const i18n = require('../panel-i18n.js');
function storage(value) { return {getItem(key) {assert.equal(key, 'cli-proxy-language');return value;}}; }
function win(value, lang = 'en-US') { const w={localStorage:storage(value),navigator:{language:lang},location:{origin:'https://cpa.example'}};w.parent=w;return w; }
test('all UI dictionary entries have four nonempty translations', () => {
  assert.deepEqual(i18n.LANGUAGES, ['zh-CN','zh-TW','en','ru']);
  for (const [key,row] of Object.entries(i18n.messages)) {assert.equal(row.length,4,key);row.forEach(v=>assert.ok(typeof v==='string'&&v.trim(),key));}
});
test('understands CPAMC persisted and historical language shapes', () => {
 for (const locale of i18n.LANGUAGES) for (const value of [locale,JSON.stringify(locale),JSON.stringify({language:locale}),JSON.stringify({state:{language:locale},version:0})]) assert.equal(i18n.parse(value),locale);
});
test('invalid storage values fail safely', () => {for(const v of ['',null,'{bad','{}','42','{"state":{"language":"x"}}']) assert.equal(i18n.parse(v),null);});
test('locale normalization and browser fallback match CPAMC families', () => {
 assert.equal(i18n.normalize('zh-HK'),'zh-TW');assert.equal(i18n.normalize('zh-Hant-TW'),'zh-TW');assert.equal(i18n.normalize('en-GB'),'en');assert.equal(i18n.normalize('RU-ru'),'ru');assert.equal(i18n.detect(win(null,'fr-FR')).language,'en');
});
test('embedded host language wins over child/browser preference', () => {
 const w=win('en');w.parent=win('{"state":{"language":"ru"}}');assert.deepEqual(i18n.detect(w),{language:'ru',source:'host',hostSync:true});
});
test('cross-origin is reported unavailable, not falsely marked synchronized', () => {
 const w=win('zh-TW');w.parent={get location(){throw new Error('SecurityError')}};
 assert.deepEqual(i18n.detect(w),{language:'zh-TW',source:'host-unavailable',hostSync:false});
});
test('blocked local storage falls back without throwing', () => {
 const w=win(null,'ru');Object.defineProperty(w,'localStorage',{get(){throw new Error('denied')}});assert.equal(i18n.detect(w).language,'ru');
});
test('fallback and translations never substitute model identifiers', () => {
 assert.equal(i18n.t('accounts','en'),'Accounts');assert.equal(i18n.t('close','ru'),'Закрыть');assert.equal(i18n.t('custom/model-id','en'),'custom/model-id');i18n.setLanguage('ru');assert.equal(i18n.getLanguage(),'ru');
});
test('language initialization does not register network or message actions', () => {
 const script=fs.readFileSync(require.resolve('../panel-i18n.js'),'utf8');assert.doesNotMatch(script,/\bfetch\s*\(|XMLHttpRequest|postMessage\s*\(|addEventListener\(["']message/);
});
test('all static markup keys exist; original title and route names unchanged', () => {
 const html=fs.readFileSync(require.resolve('../panel.html'),'utf8');assert.match(html,/<title>WorkBuddy 面板<\/title>/);
 for(const match of html.matchAll(/data-i18n(?:-placeholder|-title|-aria-label)?="([^"]+)"/g)) assert.ok(i18n.messages[match[1]],match[1]);
 assert.ok(html.indexOf('src="panel-i18n.js"')<html.indexOf('src="panel.js"'));
 assert.doesNotMatch(html,/<option[^>]*>\s*<span/);
});
