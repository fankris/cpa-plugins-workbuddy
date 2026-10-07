import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import {officialSession,officialBase} from './official-session.mjs';
import fs from 'node:fs';import assert from 'node:assert/strict';
const dir='validation/iteration-29';fs.mkdirSync(dir,{recursive:true});const browser=await chromium.launch({args:['--no-sandbox']});const checks=[],measurements=[],screenshots=[];
const shot=async(page,name)=>{const path=`${dir}/${name}.png`;await page.screenshot({path,fullPage:true});screenshots.push(path)};
try{
for(const n of[1,10,50,200]){
 let reads=0,active=0,peak=0,writes=0,release;const held=new Promise(r=>release=r);const started=Date.now();
 const {context,page,frame:f}=await officialSession(browser,{width:1366,height:900},async c=>{
  c.on('request',r=>{if(r.url().includes('/plugins/workbuddy/')&&r.method()!=='GET')writes++});
  await c.route('**/plugins/workbuddy/accounts',async r=>{const data=await(await r.fetch()).json();const sample=data.accounts[0];data.accounts=Array.from({length:n},(_,i)=>({...sample,auth_index:`scale-${String(i).padStart(3,'0')}`,nickname:`Scale ${String(i).padStart(3,'0')}`,selected:i===0,credits:undefined,plan:''}));await r.fulfill({json:data})});
  await c.route('**/plugins/workbuddy/credits?**',async r=>{reads++;active++;peak=Math.max(peak,active);await held;await new Promise(ok=>setTimeout(ok,35));active--;const id=new URL(r.request().url()).searchParams.get('auth_index');await r.fulfill({json:{accounts:[{auth_index:id,plan:'Free',credits:{total_remain:88,total_size:100,total_used:12,fetched_at:new Date().toISOString(),packages:[]}}]}}).catch(()=>{})});
 });
 const identityMs=Date.now()-started;assert.ok(reads<=2);assert.ok((await f.locator('.table-footer').innerText()).includes(`/ ${n}`));await shot(page,`scale-${n}-loading`);
 await f.getByRole('button',{name:'读取全部积分',exact:true}).click();release();await f.locator('.credit-loading-bar strong').filter({hasText:`${n} / ${n}`}).waitFor({timeout:30000});
 assert.ok(peak<=2);assert.equal(writes,0);assert.ok(reads>=n&&reads<=n+2);measurements.push({accounts:n,identity_page_ms:identityMs,total_ms:Date.now()-started,peak,reads,writes});checks.push(`${n}: identities rendered before held credits, <=2 reads, full coverage, no writes`);
 if(n===200)await shot(page,'scale-200-complete');await context.close();
}
for(const width of[390,1366]){
 let reads=0;const {context,page,frame:f}=await officialSession(browser,{width,height:844},async c=>{
 await c.route('**/plugins/workbuddy/accounts',async r=>{const data=await(await r.fetch()).json();data.accounts.forEach(a=>delete a.credits);await r.fulfill({json:data})});
 await c.route('**/plugins/workbuddy/credits?**',async r=>{reads++;await new Promise(ok=>setTimeout(ok,1500));const id=new URL(r.request().url()).searchParams.get('auth_index');await r.fulfill({json:{accounts:[{auth_index:id,credits:{total_remain:999,fetched_at:new Date().toISOString()}}]}}).catch(()=>{})});
 });await f.getByRole('button',{name:'停止读取',exact:true}).click();const count=reads;await page.waitForTimeout(1800);assert.equal(reads,count);assert.equal(await f.locator('.credit-loading-bar strong').innerText(),'0 / 10');await shot(page,`stopped-${width}`);checks.push(`${width}: slow reads canceled, queue stopped, late values ignored`);
 for(const tab of['dashboard','models','tasks','settings']){await f.locator(`[data-section=${tab}]`).click();await page.waitForTimeout(250);await shot(page,`${tab}-${width}`)}
 assert.equal(await f.locator('body').evaluate(e=>e.scrollWidth>innerWidth+1),false);checks.push(`${width}: workspace body horizontally contained`);await context.close();
}
fs.writeFileSync(`${dir}/browser.json`,JSON.stringify({version:fs.readFileSync('VERSION','utf8').trim(),result:'PASS',checks,measurements,screenshots},null,2));console.log(JSON.stringify({checks,measurements},null,2));
}finally{await browser.close()}
