import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import {officialSession,officialBase as base} from './official-session.mjs';
import assert from 'node:assert/strict';import fs from 'node:fs';
const b=await chromium.launch({headless:true,args:['--no-sandbox']}),checks=[],errors=[];
const bounds=async f=>{const r=await f.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,buttons:[...document.querySelectorAll('.task-card-top,.results-toolbar')].map(x=>{const r=x.getBoundingClientRect();return [r.left,r.right,x.scrollWidth,x.clientWidth]})}));assert.ok(r.scroll<=r.width+1,JSON.stringify(r));assert.ok(r.buttons.every(x=>x[0]>=0&&x[1]<=r.width+1&&x[2]<=x[3]+1),JSON.stringify(r))};
try{
 for(const width of [1366,390,320]){
  console.log('width',width);
  const {context,page,frame:f}=await officialSession(b,{width,height:width>640?768:844});page.on('pageerror',e=>errors.push(e.message));await context.request.post(base+'/__test/reset',{data:{}});
  await f.locator('[data-section=models]').click();await f.locator('.models-table tbody tr button').first().click();await f.locator('.hub-config-control button:enabled').click();await f.locator('dialog .primary').click();await f.locator('.operation-strip .badge').filter({hasText:'已保存'}).waitFor();
  await context.route('**/config/plugins/configs/workbuddy',r=>r.request().method()==='PUT'?r.fulfill({json:{ok:true}}):r.continue());
  await f.locator('.models-table tbody tr button').first().click();await f.locator('.hub-config-control button:enabled').click();await f.locator('dialog .primary').click();await f.locator('.operation-strip .badge').filter({hasText:'结果未确认'}).waitFor();
  await context.route('**/plugins/workbuddy/tasks?*',r=>r.fulfill({json:{tasks:[{task_code:'locked',title:'锁定任务不得显示可领取',locked:true,claimable:true,current:1,target:1,credit:5},{task_code:'reward',title:'正常可领取任务',claimable:true,current:1,target:1,credit:5},{task_code:'accept',title:'待接受的合法任务',accept_status:'',current:0,target:1,credit:5}]}}));
  await f.locator('.section-nav [data-section="tasks"]').click();await f.locator('.toolbar select').selectOption('demo-001');await f.locator('.task-card').first().waitFor();assert.equal(await f.locator('.task-card-top h3').count(),3);assert.equal(await f.locator('.task-icon').count(),0);assert.equal(await f.locator('.task-card').first().locator('button').count(),0);
  await f.locator('.task-filters button').nth(1).click();assert.equal(await f.locator('.task-card').count(),1);await f.locator('.task-filters button').nth(2).click();assert.equal(await f.locator('.task-card').count(),1);await f.locator('.task-filters button').first().click();await bounds(f);checks.push(`${width}: compact task header, locked priority and consistent actionable filters`);
  await f.locator('[data-section=models]').click();await f.locator('.operation-strip .badge').filter({hasText:'结果未确认'}).waitFor();await f.locator('.operation-strip button').click();await f.locator('dialog .badge').filter({hasText:'结果未确认'}).waitFor();await f.locator('dialog .modal-head>button').click();checks.push(`${width}: latest unconfirmed model feedback remains inspectable without a results page`);
  await f.locator('[data-section=dashboard]').click();await f.locator('.connection-panel').waitFor();
  await Promise.all([page.waitForEvent('framedetached',{predicate:x=>x===f}),page.locator('.floating-actions>button').first().click()]);await page.waitForFunction(()=>document.querySelector('iframe')?.contentDocument?.querySelector('.connection-panel'));
  let current=page.frames().find(x=>x!==page.mainFrame());assert.equal(await current.locator('.app-shell').getAttribute('data-tab'),'dashboard');await page.locator('section[aria-label="通知"] button[aria-label="关闭"]').last().click();
  for(const [index,lang] of [[1,'zh-TW'],[2,'en'],[3,'ru'],[0,'zh-CN']]){
   await page.locator('.language-menu>.btn').click();await Promise.all([page.waitForEvent('framedetached',{predicate:x=>x===current}),page.locator('.language-menu-option').nth(index).click()]);await page.waitForFunction(lang=>document.querySelector('iframe')?.contentDocument?.documentElement?.lang===lang,lang);current=page.frames().find(x=>x!==page.mainFrame());await current.locator('.connection-panel').waitFor();await bounds(current);
   await current.locator('.section-nav [data-section="tasks"]').click();await current.locator('.task-card').first().waitFor();await bounds(current);await current.locator('.section-nav [data-section="models"]').click();await current.locator('.models-table tbody tr').first().waitFor();await bounds(current);await current.locator('.section-nav [data-section="dashboard"]').click();
  }
  checks.push(`${width}: official refresh and four-language remount preserve dashboard; tasks/models/dashboard stay bounded`);await context.close();
 }
 assert.deepEqual(errors,[]);fs.writeFileSync('validation/iteration-13/browser.json',JSON.stringify({result:'PASS',checks,errors,officialFrontend:'unmodified CPAMC 1.25.3',backend:'local fixture only'},null,2));console.log(checks.join('\n'));
}finally{await b.close()}
