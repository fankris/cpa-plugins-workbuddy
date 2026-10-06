import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
const base=process.env.PREVIEW_BASE||'http://127.0.0.1:8080';
const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
const errors=[],external=[],checks=[];
try{
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1040}});
 await context.route('**/*',r=>{if(![base,base.replace('127.0.0.1','localhost')].includes(new URL(r.request().url()).origin)){external.push(r.request().url());return r.abort()}return r.continue()});
 await context.request.post(base+'/__test/reset',{data:{}});
 const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
 await page.goto(base);await page.locator('tbody tr').first().waitFor();
 assert.equal(await page.title(),'WorkBuddy 面板');assert.equal(await page.locator('tbody tr').count(),8);
 await page.getByRole('button',{name:'下一页',exact:true}).click();assert.equal(await page.locator('tbody tr').count(),2);checks.push('pagination');
 await page.getByRole('textbox',{name:'搜索名称、UID 或账号标识…'}).fill('Alex');assert.equal(await page.locator('tbody tr').count(),1);
 await page.getByRole('button',{name:'导入凭据',exact:true}).click();await page.locator('textarea').fill('unsaved fixture');
 // Locale changes update React text without reconstructing form state or fetching data.
 const count=(await (await page.request.get(base+'/__test/state')).json()).requests.length;
 for(const [locale,title] of [['en','Accounts & plans'],['ru','Аккаунты и планы'],['zh-TW','帳號與方案'],['zh-CN','账号与套餐']]){
  await page.evaluate(lang=>{localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:lang}}));window.dispatchEvent(new StorageEvent('storage',{key:'cli-proxy-language'}))},locale);
  await page.getByRole('heading',{name:title,exact:true}).waitFor();assert.equal(await page.locator('textarea').inputValue(),'unsaved fixture');
 }
 assert.equal((await (await page.request.get(base+'/__test/state')).json()).requests.length,count);checks.push('four locales; drafts and search survive; zero locale-triggered API calls');
 await page.locator('dialog').getByRole('button',{name:'取消',exact:true}).click();await page.getByRole('button',{name:/Alex · Workspace/}).click();await page.locator('dialog').getByRole('button',{name:'刷新令牌',exact:true}).click();await page.locator('dialog').last().getByRole('button',{name:'确认操作',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('dialog .confirm-target'));
 await page.locator('dialog').getByRole('button',{name:'关闭',exact:true}).click();
 const traffic=(await (await page.request.get(base+'/__test/state')).json()).requests;
 assert.ok(traffic.some(x=>x.path==='/v8/management/credentials/refresh'&&x.method==='POST'));assert.ok(!traffic.some(x=>x.path.endsWith('/keepalive')));checks.push('token refresh uses native CPA endpoint');
 await page.locator('.section-nav').getByRole('button',{name:'模型诊断',exact:true}).click();await page.locator('tbody tr').first().waitFor();
 await page.locator('.models-table tbody tr button').first().click();await page.locator('.hub-config-control button:enabled').click();await page.locator('dialog').getByRole('button',{name:'确认操作'}).click();await page.locator('.operation-strip .badge').filter({hasText:'已保存'}).waitFor();
 const state=await (await page.request.get(base+'/__test/state')).json();assert.equal(state.config.future_opaque.preserve,true);checks.push('model toggle preserves unknown plugin settings');
 await page.screenshot({path:'validation/models-desktop.png',fullPage:true});
 await page.locator('.section-nav').getByRole('button',{name:'任务与活动',exact:true}).click();await page.getByRole('combobox',{name:'选择账号'}).selectOption('demo-001');await page.locator('.task-card').first().waitFor();assert.equal(await page.locator('.task-card').count(),3);
 await page.getByRole('button',{name:'领取已完成奖励',exact:true}).click();await page.locator('dialog').getByRole('button',{name:'取消',exact:true}).click();assert.ok(!(await (await page.request.get(base+'/__test/state')).json()).requests.some(x=>x.path.endsWith('/tasks/claim')));checks.push('canceling confirmation causes no mutation');
 await page.getByRole('button',{name:'领取已完成奖励',exact:true}).click();await page.locator('dialog').getByRole('button',{name:'确认操作',exact:true}).click();await page.locator('.task-card').first().getByText('已领取',{exact:true}).first().waitFor();checks.push('claim action refreshes upstream state; no local fake completion');
 await page.getByRole('combobox',{name:'选择账号'}).selectOption('demo-003');await page.getByText('当前区域不支持此任务接口',{exact:true}).waitFor();assert.equal(await page.locator('.task-card').count(),0);checks.push('region support boundary');
 await page.getByRole('combobox',{name:'选择账号'}).selectOption('demo-001');await page.locator('.task-card').first().waitFor();await page.screenshot({path:'validation/tasks-desktop.png',fullPage:true});
 await page.getByRole('button',{name:'接受全部可接任务',exact:true}).click();await page.locator('dialog').getByRole('button',{name:'确认操作',exact:true}).click();await page.locator('.run-status').waitFor();await page.waitForFunction(()=>document.querySelector('.run-status .badge')?.textContent?.includes('成功'));checks.push('accept-only run polling reaches confirmed result');
 await page.locator('.operation-strip button').click();await page.locator('dialog').waitFor();assert.ok(!(await page.locator('body').innerText()).includes('fixture-secret-must-not-be-logged'));checks.push('credential response not retained in inline feedback');await page.locator('dialog .modal-head>button').click();await page.locator('[data-section=dashboard]').click();await page.locator('.connection-panel').waitFor();
 await page.screenshot({path:'validation/dashboard-standalone.png',fullPage:true});
 await page.locator('.section-nav').getByRole('button',{name:'账号与套餐',exact:true}).click();await page.getByRole('textbox',{name:'搜索名称、UID 或账号标识…'}).fill('');await page.screenshot({path:'validation/accounts-desktop.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:'validation/accounts-mobile.png',fullPage:true});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));checks.push('390px viewport has no page overflow');
 await page.getByRole('button',{name:'切换明暗主题'}).click();await page.screenshot({path:'validation/accounts-dark-mobile.png',fullPage:true});
 // Same-origin host remount: transient parent memory, never persistent credential drafts.
 const host=await context.newPage();await host.goto(base+'/host');let frame=host.frameLocator('iframe');await frame.locator('tbody tr').first().waitFor();await frame.getByRole('button',{name:'导入凭据',exact:true}).click();await frame.locator('textarea').fill('transient-private-draft');await host.evaluate(()=>document.querySelector('iframe').remove());await host.evaluate(()=>{const f=document.createElement('iframe');f.src='/v0/resource/plugins/workbuddy/panel';document.body.append(f)});frame=host.frameLocator('iframe');await frame.locator('textarea').waitFor();assert.equal(await frame.locator('textarea').inputValue(),'transient-private-draft');assert.ok(!(await host.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}))).includes('transient-private-draft'));checks.push('parent-memory restoration across iframe remount, no persistent import draft');
 await host.locator('iframe').evaluate((node,src)=>node.src=src,base.replace('127.0.0.1','localhost')+'/v0/resource/plugins/workbuddy/panel');
 await host.frameLocator('iframe').getByText('无法读取管理中心语言，当前使用本地偏好。',{exact:true}).waitFor();checks.push('cross-origin host language is explicitly unavailable');
 assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
 fs.writeFileSync('validation/rebuild-browser.json',JSON.stringify({result:'PASS',checks,pageErrors:errors,externalRequests:external.length,fixtureOnly:true},null,2));console.log(checks);
}finally{await browser.close()}
