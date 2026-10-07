// Runs against an isolated, empty-auth CPA v8.0.15 with official CPAMC v1.25.3.
// Never point this script at production: it changes and restores plugin config.
import {chromium,request as playwrightRequest} from '../frontend/node_modules/playwright/index.mjs';
import fs from 'node:fs';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
const base=process.env.CPA_TEST_BASE||'http://127.0.0.1:8317';
if(!/^http:\/\/127\.0\.0\.1:\d+$/.test(base))throw Error('test requires numeric loopback');
const version=fs.readFileSync('VERSION','utf8').trim();
const iteration=version.split('.').at(-1);
fs.mkdirSync('validation/iteration-'+iteration,{recursive:true});
const key=fs.readFileSync(process.env.CPA_TEST_KEY_FILE||'/home/user/.cache/workbuddy-host-test/test-key','utf8').trim();
const api=await playwrightRequest.newContext({baseURL:base,extraHTTPHeaders:{Authorization:'Bearer '+key}});
const anonymous=await playwrightRequest.newContext({baseURL:base});
const checks=[],errors=[];let original,browser;
try{
 const res=await api.get('/v8/management/plugins');assert.equal(res.status(),200);const plugin=(await res.json()).plugins.find(p=>p.id==='workbuddy');assert.ok(plugin.registered&&plugin.effective_enabled);checks.push('official host loads and registers C ABI plugin');
 assert.deepEqual(plugin.config_fields.map(x=>x.name),['login_region','login_platform','scheduler_mode']);checks.push('native ConfigFields registered');
 assert.equal((await anonymous.get('/v0/management/plugins/workbuddy/accounts')).status(),401);
 // CPA maps legacy GET+Menu declarations into the native public resource namespace.
 assert.ok(plugin.menus.some(m=>m.path==='/v0/resource/plugins/workbuddy/panel'&&m.description));checks.push('host middleware guards custom management routes');
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
 // Dismiss the actual native notification (its accessible text includes a sr-only prefix).
 await page.locator('[class*=NotificationContainer-module__notification] button').first().click();
 await page.goto(base+'/management.html#/plugin-pages/workbuddy/0');let frame=page.frameLocator('iframe');await frame.locator('[data-section=dashboard]').click();await frame.getByText('Host connected',{exact:true}).waitFor();await frame.locator('[data-section=accounts]').click();checks.push('official CPAMC embedded page inherits remembered-key session');
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
 // Native CPAMC's temporary login toast can overlap the iframe navigation.
 await page.locator('[class*=NotificationContainer-module__notification]').waitFor({state:'hidden',timeout:15000});
 await frame.locator('[data-section=settings]').click();await frame.locator('#setting-checkin_auto:enabled').waitFor();
 const oldCheckin=await frame.locator('#setting-checkin_auto').isChecked();
 await frame.locator('#setting-checkin_auto').click();await frame.locator('dialog .modal-footer .primary').click();
 await frame.locator('#setting-checkin_auto:enabled').waitFor();assert.equal(await frame.locator('#setting-checkin_auto').isChecked(),!oldCheckin);
 stored=await (await api.get('/v8/management/config/plugins/configs/workbuddy')).json();assert.equal(stored.checkin_auto,!oldCheckin);assert.equal(stored.preserved_fixture_key,'keep-me');
 const settings=await(await api.get('/v0/management/plugins/workbuddy/settings')).json();assert.equal(settings.checkin_auto,!oldCheckin);
 assert.ok((await frame.locator('.maintenance-action').innerText()).includes('never automatically enabled'));
 checks.push('current plugin settings confirm native config write, preserve opaque key and match runtime; manual-enable-only policy visible');
 await page.screenshot({path:'validation/iteration-'+iteration+'/official-real-host-settings.png',fullPage:true});
 assert.deepEqual(errors,[]);
 fs.writeFileSync('validation/iteration-'+iteration+'/real-host-integration.json',JSON.stringify({version,binary_sha256:crypto.createHash('sha256').update(fs.readFileSync('artifacts/workbuddy.so')).digest('hex'),result:'PASS',host:'v8.0.15',cpamc:'v1.25.3',checks,pageErrors:errors,officialHost:true,emptyCredentialStore:true,realCredentialsUsed:false,realInferenceTested:false,realRewardClaimsTested:false,productionChanged:false},null,2));console.log(checks);
}catch(error){
 if(browser){const page=browser.contexts()[0]?.pages()[0];if(page){await page.screenshot({path:'validation/iteration-'+iteration+'/host-failure.png',fullPage:true});const f=page.frames().find(f=>f.url().includes('/resource/plugins/workbuddy/'));console.error('Failure diagnostics',errors,await f?.locator('body').innerText())}}
 throw error;
}finally{
 if(original)await api.put('/v8/management/config/plugins/configs/workbuddy',{data:original}).catch(()=>{});
 if(browser)await browser.close();await api.dispose();await anonymous.dispose();
}
