// Real Chromium layout/interaction checks, synthetic fixture only.
import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';import fs from 'node:fs';
const base='http://127.0.0.1:8080',checks=[],measurements=[],errors=[];
const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
async function layout(page,embedded=false){
 const result=await page.evaluate(()=>{
  const viewport=document.querySelector('.accounts-table .table-scroll'),rows=[...document.querySelectorAll('.accounts-table tbody tr')],footer=document.querySelector('.accounts-table .table-footer'),main=document.querySelector('main');
  return {width:innerWidth,height:innerHeight,pageWidth:document.documentElement.scrollWidth,pageHeight:document.documentElement.scrollHeight,mainHeight:main.clientHeight,mainScroll:main.scrollHeight,first:rows[0]?.getBoundingClientRect().top,rows:rows.length,footer:footer?.getBoundingClientRect().bottom,viewportHeight:viewport?.clientHeight,viewportScroll:viewport?.scrollHeight,actions:rows.flatMap(r=>[...r.querySelectorAll('.row-actions button')].map(b=>({x:b.getBoundingClientRect().x,right:b.getBoundingClientRect().right,bottom:b.getBoundingClientRect().bottom,height:b.getBoundingClientRect().height}))),nav:[...document.querySelectorAll('.section-nav button')].map(b=>b.getBoundingClientRect().right)};
 });
 assert.ok(result.pageWidth<=result.width);assert.ok(result.pageHeight<=result.height+1);assert.ok(result.mainScroll<=result.mainHeight+1);
 assert.ok(result.first<=(embedded?325:250),'list starts too low: '+JSON.stringify(result));assert.ok(result.footer<=result.height);
 assert.ok(result.viewportScroll<=result.viewportHeight+1,'account page requires internal scrolling');
 assert.ok(result.actions.every(a=>a.x>=0&&a.right<=result.width&&a.bottom<result.footer&&a.height>=40));assert.ok(result.nav.every(x=>x<=result.width));
 measurements.push({...result,embedded});
}
try{
 for(const [width,height] of [[320,568],[390,844],[430,932],[844,390]]){
  const context=await browser.newContext({locale:'zh-CN',viewport:{width,height},isMobile:true,hasTouch:true});const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  await page.request.post(base+'/__test/reset',{data:{}});await page.goto(base);await page.locator('.accounts-table tbody tr').first().waitFor();
  await page.waitForFunction(()=>{const v=document.querySelector('.accounts-table .table-scroll');return document.querySelectorAll('.accounts-table tbody tr').length===Math.max(1,Math.min(8,Math.floor(v.clientHeight/118)))});
  await layout(page);checks.push(width+'x'+height+': bounded page, adaptive rows, all row actions and pagination visible');
  await page.getByRole('button',{name:'下一页',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.table-footer').textContent.includes('2 /'));await layout(page);checks.push(width+'px: pagination stays visible and next page renders within viewport');
  await context.close();
 }
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:390,height:844},isMobile:true,hasTouch:true});const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));await page.request.post(base+'/__test/reset',{data:{}});await page.goto(base);await page.locator('.accounts-table tbody tr').first().waitFor();
 await page.getByRole('button',{name:'筛选',exact:true}).click();await page.getByRole('combobox',{name:'状态',exact:true}).selectOption('disabled');await page.locator('tbody tr').filter({hasText:'Archive'}).waitFor();assert.equal(await page.locator('tbody tr').count(),1);await page.getByRole('button',{name:'筛选',exact:true}).click();assert.ok(await page.locator('.filter-dot').isVisible());checks.push('filters expand on demand; active filter remains indicated');
 await page.getByRole('button',{name:'筛选',exact:true}).click();await page.getByRole('combobox',{name:'状态',exact:true}).selectOption('all');await page.getByRole('button',{name:'筛选',exact:true}).click();
 await page.getByRole('button',{name:'更多',exact:true}).click();await page.getByRole('button',{name:'导入凭据',exact:true}).click();await page.locator('dialog textarea').fill('unsaved fixture');await page.setViewportSize({width:390,height:420});
 const bounds=await page.locator('dialog .modal-footer .primary').boundingBox();assert.ok(bounds.y>=0&&bounds.y+bounds.height<=420);await page.locator('dialog').getByRole('button',{name:'取消',exact:true}).click();await page.setViewportSize({width:390,height:844});checks.push('more actions accessible; import confirmation stays inside reduced-height viewport');
 const first=page.locator('.accounts-table tbody tr').filter({hasText:'Alex · Workspace'});await first.getByRole('button',{name:'停用账号',exact:true}).click();
 const confirm=page.locator('dialog .modal-footer .primary');assert.ok((await confirm.boundingBox()).height>=44);await confirm.click();await first.getByRole('button',{name:'启用账号',exact:true}).waitFor();
 const traffic=await (await page.request.get(base+'/__test/state')).json();assert.equal(traffic.requests.filter(r=>r.path==='/v8/management/credentials/status'&&r.method==='PATCH').length,1);checks.push('mobile row mutation uses one CPA native request and updates status');
 await first.getByRole('button',{name:'查看详情',exact:true}).click();await page.locator('dialog .detail-actions').getByRole('button',{name:'刷新令牌',exact:true}).waitFor();assert.ok((await page.locator('dialog .detail-actions').boundingBox()).y<400);await page.locator('dialog .modal-head>button').click();checks.push('account detail actions appear before long quota/runtime content');
 for(const locale of ['zh-CN','zh-TW','en','ru']){
  await page.evaluate(lang=>{localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:lang}}));window.dispatchEvent(new StorageEvent('storage',{key:'cli-proxy-language'}))},locale);
  for(let tab=0;tab<4;tab++){
   await page.locator('.section-nav button').nth(tab).click();
   if(tab===1)await page.locator('.models-table tbody tr').first().waitFor();
   if(tab===2){await page.locator('main>.toolbar select').selectOption('demo-001');await page.locator('.task-card').first().waitFor()}
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth&&document.documentElement.scrollHeight<=innerHeight+1&&document.querySelector('main').scrollHeight<=document.querySelector('main').clientHeight+1),'overflow '+locale+' '+tab);
   if(tab===1)assert.ok(await page.locator('.model-limits').first().isVisible());
   if(tab===2){const action=await page.locator('.task-card .task-actions button').first().boundingBox();assert.ok(action.y+action.height<844)}
  }
  checks.push(locale+': four sections fit mobile width; model limits and task action remain visible');
 }
 await context.close();
 const embedded=await browser.newContext({locale:'zh-CN',viewport:{width:390,height:844}});const host=await embedded.newPage();await host.goto(base+'/host');const frame=host.frames().find(f=>f!==host.mainFrame());await frame.locator('.accounts-table tbody tr').first().waitFor();await frame.waitForFunction(()=>{const v=document.querySelector('.accounts-table .table-scroll');return document.querySelectorAll('.accounts-table tbody tr').length===Math.floor(v.clientHeight/118)});await layout(frame,true);checks.push('same-origin iframe retains host toolbar clearance without pushing account actions offscreen');await embedded.close();
 assert.deepEqual(errors,[]);fs.writeFileSync('validation/iteration-6/mobile-browser.json',JSON.stringify({result:'PASS',checks,measurements,pageErrors:errors,fixtureOnly:true,hardwareKeyboardTested:false},null,2));console.log(checks);
}catch(error){for(const c of browser.contexts())for(const p of c.pages()){console.log((await p.locator('body').innerText()).slice(0,5000));await p.screenshot({path:'/home/user/mobile6-failure.png'})}throw error}finally{await browser.close()}
