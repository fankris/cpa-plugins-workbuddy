// Runs against an isolated, empty-auth CPA v8.0.15 with official CPAMC v1.25.3.
// Never point this script at production: it changes and restores plugin config.
import {chromium,request as playwrightRequest} from '../frontend/node_modules/playwright/index.mjs';
import fs from 'node:fs';
import assert from 'node:assert/strict';
const base=process.env.CPA_TEST_BASE||'http://127.0.0.1:8317';
if(!/^http:\/\/127\.0\.0\.1:\d+$/.test(base))throw Error('test requires numeric loopback');
const key=fs.readFileSync(process.env.CPA_TEST_KEY_FILE||'/home/user/.cache/workbuddy-host-test/test-key','utf8').trim();
const api=await playwrightRequest.newContext({baseURL:base,extraHTTPHeaders:{Authorization:'Bearer '+key}});
const anonymous=await playwrightRequest.newContext({baseURL:base});
const checks=[],errors=[];let original,browser;
try{
 const res=await api.get('/v8/management/plugins');assert.equal(res.status(),200);const plugin=(await res.json()).plugins.find(p=>p.id==='workbuddy');assert.ok(plugin.registered&&plugin.effective_enabled);checks.push('official host loads and registers C ABI plugin');
 assert.deepEqual(plugin.config_fields.map(x=>x.name),['login_region','login_platform','scheduler_mode']);checks.push('native ConfigFields registered');
 assert.equal((await anonymous.get('/v0/management/plugins/workbuddy/accounts')).status(),401);checks.push('host middleware guards custom management routes');
 for(const [suffix,type] of [['panel','text/html'],['panel.js','javascript'],['panel.css','text/css']]){const r=await anonymous.get('/v0/resource/plugins/workbuddy/'+suffix);assert.equal(r.status(),200);assert.ok(r.headers()['content-type'].includes(type))}
 assert.equal((await anonymous.get('/v0/resource/plugins/workbuddy/not-found.js')).status(),404);checks.push('embedded HTML/JS/CSS served, unknown resources 404');
 assert.equal((await api.post('/v0/management/plugins/workbuddy/tasks/light',{data:{}})).status(),410);checks.push('synthetic reporting is explicitly retired');
 const empty=await (await api.get('/v0/management/plugins/workbuddy/accounts')).json();assert.deepEqual(empty.accounts,[]);checks.push('empty credential store; no fabricated account rows');
 original=await (await api.get('/v8/management/config/plugins/configs/workbuddy')).json();
 let put=await api.put('/v8/management/config/plugins/configs/workbuddy',{data:{...original,login_region:'intl',growth_auto:true}});assert.ok(put.ok());
 let stored=await (await api.get('/v8/management/config/plugins/configs/workbuddy')).json();assert.equal(stored.preserved_fixture_key,'keep-me');assert.equal(stored.login_region,'intl');
 const runtime=await (await api.get('/v0/management/plugins/workbuddy/accounts')).json();assert.equal(runtime.growth_auto,false);checks.push('native config save/readback preserves unknown keys; retired switch cannot re-enable reporting');
 await api.put('/v8/management/config/plugins/configs/workbuddy',{data:original});
 const status=await api.get('/v0/management/plugins/workbuddy/tasks/status?auth_index=empty-fixture');assert.equal(status.status(),200);assert.equal((await status.json()).status,'idle');checks.push('task status latest-by-account contract works in real host');
 browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 const ctx=await browser.newContext({locale:'en-US',viewport:{width:1440,height:1000}});const page=await ctx.newPage();page.on('pageerror',e=>errors.push(e.message));
 await page.goto(base+'/management.html');await page.getByPlaceholder('Enter the management key').fill(key);await page.getByText('Remember password',{exact:true}).click();await page.getByRole('button',{name:'Login',exact:true}).click();await page.waitForURL('**/management.html#/');
 await page.goto(base+'/management.html#/plugin-pages/workbuddy/0');let frame=page.frameLocator('iframe');await frame.getByText('Host connected',{exact:true}).waitFor();checks.push('official CPAMC embedded page inherits remembered-key session');
 await frame.getByRole('textbox',{name:'Search name, UID or account ID…'}).fill('retained-search');await frame.getByRole('button',{name:'Import credentials',exact:true}).click();await frame.locator('textarea').fill('unsaved-local-fixture');
 // CPAMC destroys/recreates the iframe during language changes. Use its actual control.
 for(const [label,title,locale] of [['中文','账号与套餐','zh-CN'],['繁體中文（台灣）','帳號與方案','zh-TW'],['Русский','Аккаунты и планы','ru'],['English','Accounts & plans','en']]){
  // The button aria label is translated after each change; use its menu position invariant.
  const button=page.locator('button[aria-label="Language"],button[aria-label="语言"],button[aria-label="語言"],button[aria-label="Язык"]');
  await button.first().click();
  let option=page.getByText(label,{exact:true});
  if(locale==='zh-TW')option=page.getByText(/繁體中文|Traditional Chinese/).first();
  if(locale==='ru')option=page.getByText(/^(Русский|俄语|俄語|俄文|Russian)$/).first();
  if(locale==='en')option=page.getByText(/^(English|英语|英語|英文|Английский)$/).first();
  const oldFrame=page.frames().find(f=>f.url().includes('/v0/resource/plugins/workbuddy/panel'));
  const detached=page.waitForEvent('framedetached',{predicate:f=>f===oldFrame,timeout:10000});
  await option.click({timeout:4000});await detached;await page.locator('iframe').waitFor();
  frame=page.frameLocator('iframe');await frame.getByRole('heading',{name:title,exact:true}).waitFor();await frame.locator('textarea').waitFor();assert.equal(await frame.locator('textarea').inputValue(),'unsaved-local-fixture');assert.equal(await frame.locator('.search input').first().inputValue(),'retained-search');
 }
 checks.push('all four official language switches restore drafts/search after iframe remount');
 const disk=await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}));assert.ok(!disk.includes('unsaved-local-fixture'));checks.push('import draft never written to browser persistent/session storage');
 await frame.locator('dialog .modal-head button').click();await page.screenshot({path:'validation/cpamc-real-host.png',fullPage:true});
 await frame.getByRole('button',{name:'Plugin settings',exact:true}).first().click();await frame.locator('dialog').waitFor();assert.equal(await frame.locator('dialog select').count(),0);assert.ok((await frame.locator('dialog a').getAttribute('href',{timeout:4000})).endsWith('#/plugins'));checks.push('settings are read-only; editing delegates to native CPAMC');
 assert.deepEqual(errors,[]);
 fs.writeFileSync('validation/real-host-integration.json',JSON.stringify({result:'PASS',host:'v8.0.15',cpamc:'v1.25.3',checks,pageErrors:errors,realCredentialsUsed:false,realInferenceTested:false,realRewardClaimsTested:false,productionChanged:false},null,2));console.log(checks);
}finally{
 if(original)await api.put('/v8/management/config/plugins/configs/workbuddy',{data:original}).catch(()=>{});
 if(browser)await browser.close();await api.dispose();await anonymous.dispose();
}
