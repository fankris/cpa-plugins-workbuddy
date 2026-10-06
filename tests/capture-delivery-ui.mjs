// Capture the current embedded UI with visibly labelled synthetic data.
import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import fs from 'node:fs';
import {officialSession} from './official-session.mjs';
import crypto from 'node:crypto';
const base=process.env.PREVIEW_BASE||'http://127.0.0.1:8080';
if(!/^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(base))throw Error('Local fixture only');
fs.mkdirSync('validation',{recursive:true});
const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
try{
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1040}});
 await context.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():r.abort());
 await context.request.post(base+'/__test/reset',{data:{}});
 const page=await context.newPage();await page.goto(base);await page.locator('tbody tr').first().waitFor();
 const shot=name=>page.screenshot({path:'validation/'+name+'.png',fullPage:true});
 await shot('accounts-desktop');await page.locator('.accounts-table tbody tr').first().getByRole('button',{name:'查看详情',exact:true}).click();await page.locator('.account-detail').screenshot({path:'validation/detail-desktop.png'});await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click();await page.setViewportSize({width:1366,height:768});await shot('accounts-laptop');await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>{const v=document.querySelector('.accounts-table .table-scroll');return document.querySelectorAll('.accounts-table tbody tr').length===Math.floor(v.clientHeight/118)});await shot('accounts-mobile');
 await page.getByRole('button',{name:'切换明暗主题'}).click();await shot('accounts-dark-mobile');
 await page.getByRole('button',{name:'切换明暗主题'}).click();await page.setViewportSize({width:1440,height:1040});
 await page.locator('.section-nav').getByRole('button',{name:'模型诊断',exact:true}).click();await page.locator('tbody tr').first().waitFor();await shot('models-desktop');await page.setViewportSize({width:390,height:844});await shot('models-mobile');await page.setViewportSize({width:1440,height:1040});
 await page.locator('tbody tr').first().getByRole('button',{name:'停用模型'}).click();await page.locator('dialog').getByRole('button',{name:'确认操作'}).click();await page.locator('.operation-strip .badge').filter({hasText:'已保存'}).waitFor();
 await page.locator('.section-nav').getByRole('button',{name:'任务与活动',exact:true}).click();await page.getByRole('combobox',{name:'选择账号'}).selectOption('demo-001');await page.locator('.task-card').first().waitFor();
 await page.locator('.toast').waitFor({state:'hidden'});await shot('tasks-desktop');await page.setViewportSize({width:390,height:844});await shot('tasks-mobile');await page.setViewportSize({width:1440,height:1040});
 await page.locator('.section-nav').getByRole('button',{name:/诊断与操作结果/}).click();await page.locator('tbody tr').first().waitFor();await shot('results-desktop');
 await context.request.post(base+'/__test/reset',{data:{}});
 for(const [host,width,height,name] of [['cpamp',1366,768,'cpamp-desktop'],['cpamc',1366,768,'cpamc-desktop'],['cpamc',390,844,'cpamc-mobile']]){
  await page.setViewportSize({width,height});await page.goto(base+'/host-'+host);
  const frame=page.frames().find(f=>f!==page.mainFrame());await frame.locator('.accounts-table tbody tr').first().waitFor();
  await frame.waitForFunction(()=>!!document.documentElement.dataset.hostLayout);
  await shot(name);
 }
 for(const width of [1366,390]){
  const real=await officialSession(browser,{width,height:width>640?768:844});
  await real.page.screenshot({path:'validation/official-'+(width>640?'desktop':'mobile')+'.png',fullPage:true,animations:'disabled'});
  await real.page.locator('.theme-menu>.btn').click();
  if(width===390){
   await real.frame.locator('.accounts-table tbody tr .row-actions button').last().click();
   await real.page.waitForFunction(()=>{const f=document.querySelector('iframe'),b=f.contentDocument.querySelector('dialog .modal-head>button');return b&&b.getBoundingClientRect().top+f.getBoundingClientRect().top>document.querySelector('.theme-menu-popover').getBoundingClientRect().bottom});
   await real.page.screenshot({path:'validation/official-menu-dialog.png',fullPage:true,animations:'disabled'});
  }else{
   await real.page.locator('.theme-card').nth(3).click();await real.frame.waitForFunction(()=>document.documentElement.dataset.theme==='dark');
   await real.page.screenshot({path:'validation/official-dark.png',fullPage:true,animations:'disabled'});
  }
  await real.context.close();
 }
 for(const width of [1366,390]){
  const {context,page,frame}=await officialSession(browser,{width,height:width>640?768:844});await context.request.post(base+'/__test/reset',{data:{}});
  const suffix=width>640?'desktop':'mobile';
  for(const tab of ['dashboard','settings']){await frame.locator('[data-section='+tab+']').click();if(tab==='settings')await frame.locator('#setting-checkin_auto:enabled').waitFor();await page.screenshot({path:'validation/official-'+tab+'-'+suffix+'.png',fullPage:true,animations:'disabled'})}
  await frame.locator('.section-nav [data-section="models"]').click();await frame.locator('.models-table tbody tr').first().waitFor();
  await frame.locator('.toolbar .pills button').nth(1).click();await frame.locator('.notice select').selectOption('demo-001');await frame.locator('.models-table tbody tr').first().waitFor();
  await page.screenshot({path:'validation/official-models-'+suffix+'.png',fullPage:true,animations:'disabled'});
  if(width<=640)await frame.locator('.model-tools-toggle').click();await frame.locator('#model-tools select').nth(1).selectOption('imageDeclared');await page.screenshot({path:'validation/official-model-filters-'+suffix+'.png',fullPage:true,animations:'disabled'});await frame.locator('.model-footer-reset').click();if(width<=640)await frame.locator('.model-tools-toggle').click();
  await frame.locator('.toolbar .pills button').first().click();await frame.locator('.models-table tbody tr button').first().click();await frame.locator('dialog .primary').click();await frame.locator('.operation-strip .badge').filter({hasText:'已保存'}).waitFor();
  await context.route('**/config/plugins/configs/workbuddy',r=>r.request().method()==='PUT'?r.fulfill({json:{ok:true}}):r.continue());
  await frame.locator('.models-table tbody tr button').first().click();await frame.locator('dialog .primary').click();await frame.locator('.operation-strip .badge').filter({hasText:'结果未确认'}).waitFor();
  await frame.locator('.toast button').click();await frame.locator('.section-nav [data-section="results"]').click();await frame.locator('.results-toolbar select').selectOption('needsReview');
  await page.screenshot({path:'validation/official-results-'+suffix+'.png',fullPage:true,animations:'disabled'});
  await frame.locator('.section-nav [data-section="tasks"]').click();await frame.locator('.toolbar select').selectOption('demo-001');await frame.locator('.task-card').first().waitFor();
  await page.screenshot({path:'validation/official-tasks-'+suffix+'.png',fullPage:true,animations:'disabled'});await context.close();
 }
 // CPAMP routing contract fixture: v8 is not proxied, native plugin config lives at v0.
 for(const width of [1366,390]){
  const ctx=await browser.newContext({locale:'zh-CN',viewport:{width,height:width>640?768:844}});await ctx.request.post(base+'/__test/reset',{data:{}});
  await ctx.route('**/v8/management/config/plugins/configs/workbuddy',r=>r.fulfill({status:404,contentType:'text/plain',body:'404 page not found'}));
  await ctx.route('**/plugins/workbuddy/accounts',async r=>{const data=await(await r.fetch()).json(),epoch=Date.now(),end=new Date(epoch+12.1*86400000).toISOString();data.server_time_iso=new Date(epoch).toISOString();const cr=data.accounts[0].credits;cr.packages=[{name:'Expiry fixture',remain:1280,size:1280,used:0,cycle_end:end},{name:'Later fixture',remain:11560,size:13720,used:2160,cycle_end:new Date(epoch+30*86400000).toISOString()}];cr.expiry={known:true,within_7_days:0,next_at:end,next_amount:1280,unknown_remain:0};await r.fulfill({json:data})});
  const p=await ctx.newPage();await p.goto(base+'/host-cpamp');const f=p.frames().find(x=>x!==p.mainFrame());await f.locator('.accounts-table tbody tr').first().waitFor();await f.locator('.expiry-days').filter({hasText:'12.1天'}).waitFor();
  await p.screenshot({path:'validation/cpamp-expiry-'+(width>640?'desktop':'mobile')+'.png',fullPage:true});
  await f.locator('[data-section=models]').click();await f.locator('.models-table tbody tr').first().waitFor();await f.locator('.models-table tbody tr button').first().click();await f.locator('dialog .primary').click();await f.locator('.operation-strip .badge').filter({hasText:'已保存'}).waitFor();
  if(width>640)await p.screenshot({path:'validation/cpamp-config-models.png',fullPage:true});
  else{await f.locator('[data-section=settings]').click();await f.locator('#setting-checkin_auto:enabled').waitFor();await f.locator('#setting-checkin_auto').click();await f.locator('dialog .primary').click();await f.locator('.operation-strip .badge').filter({hasText:'已保存'}).waitFor();await p.screenshot({path:'validation/cpamp-config-settings.png',fullPage:true})}
  await ctx.close();
 }
 for(const width of [1366,390]){
  const {context,page,frame}=await officialSession(browser,{width,height:width>640?768:844});
  await context.request.post(base+'/__test/reset',{data:{}});
  await frame.locator('[data-section=models]').click();await frame.locator('.models-table tbody tr').first().waitFor();
  await frame.locator('.model-toolbar .pills button').nth(1).click();await frame.locator('.notice select').selectOption('demo-003');await frame.waitForFunction(()=>document.querySelectorAll('.models-table tbody tr').length===14);
  const suffix=width>640?'desktop':'mobile';
  await page.screenshot({path:'validation/official-directory-'+suffix+'.png',fullPage:true,animations:'disabled'});
  await frame.locator('.models-table tr').filter({hasText:'fixture-v3-priority'}).locator('button').click();
  await page.screenshot({path:'validation/official-directory-detail-'+suffix+'.png',fullPage:true,animations:'disabled'});await context.close();
 }
 fs.writeFileSync('validation/ui-capture.json',JSON.stringify({version:fs.readFileSync('VERSION','utf8').trim(),data:'synthetic backend; official-* images use unmodified CPAMC v1.25.3 frontend, other host shells are simulations; no real credentials',assets:Object.fromEntries(['panel.js','panel.css','panel.html'].map(f=>[f,crypto.createHash('sha256').update(fs.readFileSync(f)).digest('hex')])),screenshots:Object.fromEntries(['official-directory-desktop','official-directory-mobile','official-directory-detail-desktop','official-directory-detail-mobile','official-model-filters-desktop','official-model-filters-mobile','cpamp-expiry-desktop','cpamp-expiry-mobile','cpamp-config-models','cpamp-config-settings','official-dashboard-desktop','official-dashboard-mobile','official-settings-desktop','official-settings-mobile','official-models-desktop','official-models-mobile','official-tasks-desktop','official-tasks-mobile','official-results-desktop','official-results-mobile','official-desktop','official-mobile','official-menu-dialog','official-dark','cpamp-desktop','cpamc-desktop','cpamc-mobile','accounts-desktop','accounts-laptop','detail-desktop','accounts-mobile','accounts-dark-mobile','models-desktop','models-mobile','tasks-desktop','tasks-mobile','results-desktop'].map(n=>[n+'.png',crypto.createHash('sha256').update(fs.readFileSync('validation/'+n+'.png')).digest('hex')]))},null,2));
 console.log('Captured thirty-seven UI screenshots (twenty use verified official CPAMC frontend, mock backend); fixture data only; no external network.');
}finally{await browser.close()}
