import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import {officialSession,officialBase as base} from './official-session.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
fs.mkdirSync('validation/iteration-28',{recursive:true});
const browser=await chromium.launch({args:['--no-sandbox']}),checks=[],errors=[];
try{for(const width of[390,1366]){
 const {context,page,frame:f}=await officialSession(browser,{width,height:844});
 page.on('pageerror',e=>errors.push(e.message));await context.request.post(base+'/__test/reset',{data:{}});
 let writes=0;
 await context.route('**/plugins/workbuddy/checkin',async r=>{writes++;if(writes===1)return r.abort('failed');return r.fulfill({json:{success:true,auth_index:'demo-002'}})});
 await f.getByRole('checkbox',{name:'Alex · Workspace',exact:true}).check();await f.getByRole('checkbox',{name:'Design Studio',exact:true}).check();
 await f.locator('.bulk button').first().click();await f.locator('dialog .modal-footer .primary').click();
 await f.locator('.operation-strip .badge').filter({hasText:'结果未确认'}).waitFor();assert.equal(writes,2);
 await f.locator('.operation-strip button').click();assert.ok((await f.locator('dialog pre').innerText()).includes('"uncertain": true'));
 await page.screenshot({path:`validation/official-bulk-unconfirmed-${width===390?'mobile':'desktop'}.png`,fullPage:true});
 await f.locator('dialog .modal-head >button').click();checks.push(`${width}: mixed bulk network loss retains per-account uncertainty; exactly one POST per account, no replay`);
 // Known native 204 acknowledgement, after the fixture has really applied the PATCH.
 let patches=0;await context.route('**/v8/management/credentials/status',async r=>{patches++;await r.fetch();return r.fulfill({status:204,body:''})});
 await f.locator('.account-name').first().click();await f.locator('.account-operations summary').click();
 await f.locator('.account-operations button').filter({hasText:'停用账号'}).click();await f.locator('dialog').last().locator('.modal-footer .primary').click();
 await f.locator('.account-operations button').filter({hasText:'启用'}).waitFor();assert.equal(patches,1);
 assert.ok((await f.locator('.operation-strip .badge').innerText()).includes('成功'));
 await f.locator('.account-detail .modal-head >button').click();checks.push(`${width}: native credential PATCH 204 accepted; authoritative reload shows disabled, no automatic enable request`);
 // A transient status-read failure must not cause another accept_all mutation.
 await f.locator('[data-section=tasks]').click();await f.locator('.toolbar select').selectOption('demo-001');await f.locator('.task-card').first().waitFor();
 let reads=0,accepts=0,release;let thirdEnteredResolve;
 const thirdEntered=new Promise(resolve=>thirdEnteredResolve=resolve);
 await context.route('**/plugins/workbuddy/tasks/accept_all',async r=>{accepts++;return r.continue()});
 await context.route('**/plugins/workbuddy/tasks/status?**',async r=>{
  const u=new URL(r.request().url()),id=u.searchParams.get('run_id');if(!id)return r.continue();
  reads++;if(reads===2)return r.abort('failed');
  if(reads===3){thirdEnteredResolve();await new Promise(resolve=>release=resolve)}
  return r.fulfill({json:{run_id:id,auth_index:'demo-001',status:reads>=3?'succeeded':'running',accepted:reads>=3?1:0,failed:[],kind:'accept',started_at:new Date().toISOString()}});
 });
 await f.getByRole('button',{name:'接受全部可接任务',exact:true}).click();await f.locator('dialog .modal-footer .primary').click();
 await f.locator('.task-poll-notice').waitFor();await thirdEntered;
 assert.ok((await f.locator('.task-poll-notice').innerText()).includes('不会重新执行任务'));
 await page.screenshot({path:`validation/official-task-retry-${width===390?'mobile':'desktop'}.png`,fullPage:true});checks.push(`${width}: interrupted GET displays retry and last verified status while backend run remains running`);
 release();await f.locator('.run-status > .badge').filter({hasText:'成功'}).waitFor();assert.equal(accepts,1);assert.equal(reads,3);
 assert.equal(await f.locator('.task-poll-notice').count(),0);checks.push(`${width}: same run recovers to terminal state after three reads; only one accept_all POST`);
 await f.locator('[data-section=settings]').click();await f.locator('#setting-lifecycle_auto:enabled').waitFor();
 assert.ok((await f.locator('.settings-workspace').innerText()).includes('不会自动启用已暂停账号'));
 assert.equal(await f.locator('.settings-workspace').evaluate(e=>e.scrollWidth>e.clientWidth+1),false);checks.push(`${width}: explicit manual-enable-only policy visible in settings with no horizontal overflow`);
 await context.close();
}
assert.deepEqual(errors,[]);
fs.writeFileSync('validation/iteration-28/browser.json',JSON.stringify({version:fs.readFileSync('VERSION','utf8').trim(),result:'PASS',checks,errors,fixtureOnly:true,officialFrontend:'CPAMC 1.25.3',realAccountsUsed:false},null,2));console.log(checks.join('\n'));
}finally{await browser.close()}
