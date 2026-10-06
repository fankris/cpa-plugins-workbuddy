// Desktop geometry and real browser interaction; synthetic fixture only.
import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';import fs from 'node:fs';
const base='http://127.0.0.1:8080',checks=[],measurements=[],errors=[];
const b=await chromium.launch({headless:true,args:['--no-sandbox']});
async function geometry(page,embedded=false){
 const m=await page.evaluate(()=>{
  const rect=x=>x.getBoundingClientRect().toJSON(),view=document.querySelector('.accounts-table .table-scroll');
  return {width:innerWidth,height:innerHeight,body:[document.documentElement.scrollWidth,document.documentElement.scrollHeight],main:[document.querySelector('main').scrollHeight,document.querySelector('main').clientHeight],first:rect(document.querySelector('.accounts-table tbody tr')),last:rect([...document.querySelectorAll('.accounts-table tbody tr')].at(-1)),footer:rect(document.querySelector('.accounts-table .table-footer')),list:[view.scrollWidth,view.clientWidth,view.scrollHeight,view.clientHeight],actions:[...document.querySelectorAll('.accounts-table .row-actions button')].map(rect)};
 });
 assert.ok(m.body[0]<=m.width&&m.body[1]<=m.height+1,JSON.stringify(m));assert.ok(m.main[0]<=m.main[1]+1);
 assert.ok(m.list[0]<=m.list[1]+1,'horizontal scroll '+JSON.stringify(m));
 assert.ok(m.first.y<=(embedded?350:290),'top area too large');
 assert.ok(m.actions.every(r=>r.x>=0&&r.right<=m.width));
 assert.ok(m.footer.bottom<=m.height);
 // Default eight rows should fit common desktop viewport without list scrolling.
 assert.ok(m.list[2]<=m.list[3]+1,'default rows require scroll '+JSON.stringify(m));
 assert.ok(m.last.bottom<=m.footer.top+1);
 measurements.push({...m,embedded});
}
try{
 for(const [width,height] of [[1024,768],[1366,768],[1440,900],[1920,1080]]){
  const c=await b.newContext({locale:'zh-CN',viewport:{width,height}}),p=await c.newPage();p.on('pageerror',e=>errors.push(e.message));await p.request.post(base+'/__test/reset',{data:{}});await p.goto(base);await p.locator('tbody tr').first().waitFor();
  await geometry(p);checks.push(width+'x'+height+': eight rows, actions and pagination fit with no body/list scrolling');
  for(const locale of ['en','ru','zh-TW']){
   await p.evaluate(lang=>{localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:lang}}));window.dispatchEvent(new StorageEvent('storage',{key:'cli-proxy-language'}))},locale);await p.waitForFunction(lang=>document.documentElement.lang===lang,locale);
   await geometry(p);
  }
  checks.push(width+'px: English, Russian and Traditional Chinese maintain column/action bounds');await c.close();
 }
 const c=await b.newContext({locale:'zh-CN',viewport:{width:1366,height:768}}),p=await c.newPage();p.on('pageerror',e=>errors.push(e.message));await p.request.post(base+'/__test/reset',{data:{}});await p.goto(base);await p.locator('tbody tr').first().waitFor();
 await p.getByRole('button',{name:'需要关注',exact:true}).click();await p.waitForFunction(()=>document.querySelectorAll('.accounts-table tbody tr').length===2);assert.equal(await p.getByRole('combobox',{name:'状态',exact:true}).inputValue(),'attention');checks.push('attention summary selects the same cooldown/exhausted/error group it counts');
 await p.getByRole('button',{name:'账号总数',exact:true}).click();await p.getByRole('combobox',{name:'每页',exact:true}).selectOption('12');await p.waitForFunction(()=>document.querySelectorAll('.accounts-table tbody tr').length===10);await p.getByRole('combobox',{name:'每页',exact:true}).selectOption('8');await p.waitForFunction(()=>document.querySelectorAll('.accounts-table tbody tr').length===8);checks.push('desktop page size changes and returns to default without losing records');
 await p.locator('tbody tr').first().getByRole('button',{name:'查看详情',exact:true}).click();
 const detail=await p.locator('.account-detail').evaluate(d=>{const body=d.querySelector('.modal-body'),left=d.querySelector('.detail-overview').getBoundingClientRect(),right=d.querySelector('.detail-diagnostics').getBoundingClientRect();return{top:d.getBoundingClientRect().top,bottom:d.getBoundingClientRect().bottom,height:innerHeight,left:left.right,right:right.left,scroll:body.scrollHeight,client:body.clientHeight}});
 assert.ok(detail.top>=0&&detail.bottom<=detail.height&&detail.left<detail.right);assert.ok(detail.scroll<=detail.client+1,'detail requires scroll');
 await p.getByRole('button',{name:'下一个账号',exact:true}).click();await p.getByRole('dialog',{name:'Archive',exact:true}).waitFor();await p.getByRole('button',{name:'上一个账号',exact:true}).click();await p.getByRole('dialog',{name:'Alex · Workspace',exact:true}).waitFor();await p.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click();checks.push('two-column inspector shows quota and diagnostics together; account switching needs no close/reopen');
 const row=p.locator('tbody tr').filter({hasText:'Alex · Workspace'});await row.getByRole('checkbox').check();assert.equal(await row.getAttribute('aria-selected'),'true');await p.locator('.bulk').getByRole('button',{name:'取消',exact:true}).click();checks.push('selected rows have explicit state; selection clears from batch bar');
 for(let repeat=0;repeat<2;repeat++){await row.getByRole('button',{name:'任务与活动',exact:true}).click();await p.locator('.task-card').first().waitFor();assert.equal(await p.locator('.task-card').count(),3);await p.locator('.section-nav').getByRole('button',{name:'账号与套餐',exact:true}).click();await row.waitFor()};checks.push('revisiting the same account task shortcut reloads instead of clearing tasks');
 for(const index of [1,2,3]){await p.locator('.section-nav [data-section="'+['accounts','models','tasks','dashboard'][index]+'"]').click();if(index===1)await p.locator('.models-table tbody tr').first().waitFor();if(index===2)await p.locator('.task-card').first().waitFor();assert.ok(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth&&document.documentElement.scrollHeight<=innerHeight+1&&document.querySelector('main').scrollHeight<=document.querySelector('main').clientHeight+1))};checks.push('models, tasks and dashboard remain bounded desktop workspaces');
 await c.close();
 const ec=await b.newContext({locale:'zh-CN',viewport:{width:1366,height:768}}),host=await ec.newPage();await host.goto(base+'/host');const f=host.frames().find(f=>f!==host.mainFrame());await f.locator('tbody tr').first().waitFor();await geometry(f,true);checks.push('embedded desktop preserves toolbar clearance and all eight default rows');await ec.close();
 assert.deepEqual(errors,[]);fs.writeFileSync('validation/iteration-7/desktop-browser.json',JSON.stringify({result:'PASS',checks,measurements,pageErrors:errors,fixtureOnly:true},null,2));console.log(checks);
}catch(error){for(const c of b.contexts())for(const p of c.pages()){console.log((await p.locator('body').innerText()).slice(0,2000));await p.screenshot({path:'/home/user/pc7-failure.png'})}throw error}finally{await b.close()}
