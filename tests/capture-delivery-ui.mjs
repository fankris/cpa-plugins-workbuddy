// Capture the current embedded UI with visibly labelled synthetic data.
import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import fs from 'node:fs';
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
 fs.writeFileSync('validation/ui-capture.json',JSON.stringify({version:fs.readFileSync('VERSION','utf8').trim(),data:'synthetic fixture, no real credentials',assets:Object.fromEntries(['panel.js','panel.css','panel.html'].map(f=>[f,crypto.createHash('sha256').update(fs.readFileSync(f)).digest('hex')])),screenshots:Object.fromEntries(['accounts-desktop','accounts-laptop','detail-desktop','accounts-mobile','accounts-dark-mobile','models-desktop','models-mobile','tasks-desktop','tasks-mobile','results-desktop'].map(n=>[n+'.png',crypto.createHash('sha256').update(fs.readFileSync('validation/'+n+'.png')).digest('hex')]))},null,2));
 console.log('Captured ten actual UI screenshots; fixture data only; no external network.');
}finally{await browser.close()}
