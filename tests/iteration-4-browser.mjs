// Browser-only fixture; no upstream or real credentials.
import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
const base=process.env.PREVIEW_BASE||'http://127.0.0.1:8080';
const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
const checks=[],errors=[];
try{
 const ctx=await browser.newContext({locale:'zh-CN'});const page=await ctx.newPage();page.on('pageerror',e=>errors.push(e.message));
 let status='fallback',posts=0;
 await ctx.route('**/models?auth_index=*',r=>r.fulfill({json:{auth_index:'demo-001',status,warning:status==='fallback'?'fixture discovery denied':'',source:{source:status==='fallback'?'static (discovery failed)':'pin (unverified)'},models:[{id:'fixture-model',name:'Fixture model',disabled:false}]}}));
 await ctx.route('**/models/refresh',r=>{posts++;return r.fulfill({json:{auth_index:'demo-001',status:'fallback',warning:'fixture refresh denied',source:{source:'static (discovery failed)'},models:[{id:'fixture-model',name:'Fixture model',disabled:false}]}})});
 await ctx.request.post(base+'/__test/reset',{data:{}});await page.goto(base);await page.locator('tbody tr').first().waitFor();
 await page.locator('.section-nav').getByRole('button',{name:'模型诊断',exact:true}).click();await page.getByRole('button',{name:'账号模型发现',exact:true}).click();await page.getByRole('combobox',{name:'选择账号'}).selectOption('demo-001');
 await page.getByRole('alert').filter({hasText:'发现失败'}).waitFor();await page.getByText('Fixture model',{exact:true}).waitFor();checks.push('fallback catalog stays visible with explicit unverified warning');
 await page.getByRole('button',{name:'重新发现模型',exact:true}).click();await page.locator('dialog').getByRole('button',{name:'确认操作',exact:true}).click();await page.getByRole('alert').filter({hasText:'fixture refresh denied'}).waitFor();assert.equal(posts,1);checks.push('failed refresh keeps warning without mutation replay');
 status='skipped';await page.getByRole('combobox',{name:'选择账号'}).selectOption('demo-002');await page.getByText('当前使用固定或静态目录，未执行上游发现。',{exact:true}).waitFor();assert.equal(await page.getByRole('alert').count(),0);checks.push('switching to pinned/static account clears previous fallback alert');
 assert.deepEqual(errors,[]);fs.writeFileSync('validation/iteration-4/browser-models.json',JSON.stringify({result:'PASS',checks,errors,realCredentialsUsed:false},null,2));console.log(checks);
}finally{await browser.close()}
