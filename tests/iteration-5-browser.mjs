// CPA-first presentation contract. Fixture only: no official host or upstream.
import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
const base=process.env.PREVIEW_BASE||'http://127.0.0.1:8080';
const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
const checks=[],errors=[];
try{
 const ctx=await browser.newContext({locale:'zh-CN'});const page=await ctx.newPage();page.on('pageerror',e=>errors.push(e.message));let writes=0;
 await ctx.route('**/plugins/workbuddy/accounts',async route=>{
  const response=await route.fetch();const data=await response.json();
  Object.assign(data.accounts[0],{disabled:false,exhausted:false,error:'',runtime:{}});
  Object.assign(data.accounts[1],{disabled:false,exhausted:false,error:'',runtime:{status:'error'}});
  await route.fulfill({json:data});
 });
 await ctx.route('**/config/plugins/configs/workbuddy',async route=>{
  if(route.request().method()==='PUT'){writes++;return route.fulfill({json:{ok:true}})}
  return route.continue(); // Deliberately do not persist the preceding PUT.
 });
 await ctx.request.post(base+'/__test/reset',{data:{}});await page.goto(base);await page.locator('tbody tr').first().waitFor();
 await page.locator('tbody tr').filter({hasText:'Alex · Workspace'}).getByText('未知',{exact:true}).waitFor();checks.push('missing CPA runtime stays unknown in account table');
 assert.ok(await page.locator('tbody tr').getByText('需要关注',{exact:true}).count()>0);checks.push('CPA error does not render available');
 await page.locator('.section-nav').getByRole('button',{name:'模型诊断',exact:true}).click();await page.locator('tbody tr').first().getByRole('button',{name:'停用模型'}).click();await page.locator('dialog').getByRole('button',{name:'确认操作'}).click();
 await page.locator('.operation-strip .badge').filter({hasText:'结果未确认'}).waitFor();
 await page.locator('tbody tr').first().getByText('已启用',{exact:true}).waitFor();assert.equal(writes,1);checks.push('successful HTTP PUT with stale readback does not claim saved or change model badge');
 await page.locator('.section-nav').getByRole('button',{name:/诊断与操作结果/}).click();await page.locator('tbody tr').first().getByText('结果未确认',{exact:true}).waitFor();checks.push('unconfirmed outcome remains in local result history, no mutation replay');
 assert.deepEqual(errors,[]);
 fs.mkdirSync('validation/iteration-5',{recursive:true});fs.writeFileSync('validation/iteration-5/browser-cpa-first.json',JSON.stringify({result:'PASS',checks,errors,realCredentialsUsed:false,officialHost:false},null,2));console.log(checks);
}finally{await browser.close()}
