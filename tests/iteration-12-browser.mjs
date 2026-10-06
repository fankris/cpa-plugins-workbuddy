import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import {officialSession} from './official-session.mjs';
import assert from 'node:assert/strict';import fs from 'node:fs';import crypto from 'node:crypto';
const hash='866bae020785b59126a209e89389b77f673fd791beb2208ea650dcc07b3a108d';
assert.equal(crypto.createHash('sha256').update(fs.readFileSync('/home/user/.cache/cpamc-reference/management.html')).digest('hex'),hash);
const b=await chromium.launch({headless:true,args:['--no-sandbox']}),checks=[],measurements=[];
try{
 for(const width of [1366,390,320]){
  const {context,page,frame}=await officialSession(b,{width,height:width>640?768:844});
  assert.equal(await frame.locator('html').getAttribute('data-host-ui'),'cpamc');assert.equal(await frame.locator('.data-refresh').isVisible(),false);
  assert.equal(await page.locator('.floating-actions>button,.floating-actions>.language-menu>.btn,.floating-actions>.theme-menu>.btn').count(),4);
  const band=await page.locator('.floating-actions').boundingBox();measurements.push({width,band});checks.push(`${width}: verified official four-icon header; no duplicate plugin data refresh`);
  // Real official theme actions, including removing data-theme for light.
  for(const [index,want] of [[3,'dark'],[1,'light'],[3,'dark'],[2,'light']]){
   await page.locator('.theme-menu>.btn').click();await page.locator('.theme-card').nth(index).click();await frame.waitForFunction(t=>document.documentElement.dataset.theme===t,want);
  }
  checks.push(`${width}: official dark → white and dark → paper theme transitions propagate`);
  const before=await frame.locator('.page-heading').boundingBox();await page.locator('.theme-menu>.btn').click();await page.locator('.theme-menu-popover').waitFor();
  // Open the actual plugin dialog while a host-owned popup remains open.
  await frame.locator('.accounts-table tbody tr .row-actions button').last().click();
  const menu=await page.locator('.theme-menu-popover').boundingBox();
  await frame.waitForFunction(()=>document.querySelector('dialog')?.open);
  await page.waitForFunction(()=>{const f=document.querySelector('iframe'),d=f.contentDocument.querySelector('dialog .modal-head>button');return f.getBoundingClientRect().top+d.getBoundingClientRect().top>document.querySelector('.theme-menu-popover').getBoundingClientRect().bottom});
  const close=await frame.locator('dialog .modal-head>button').boundingBox();assert.ok(close.y>menu.y+menu.height);
  await frame.locator('dialog .modal-head>button').click();assert.equal((await frame.locator('.page-heading').boundingBox()).y,before.y);
  await page.locator('.theme-menu>.btn').click();checks.push(`${width}: expanded official theme panel leaves modal close usable without moving workspace`);
  // Host header refresh really remounts the iframe, preserving selection state.
  await frame.locator('.metrics .metric-filter').nth(1).click();
  await Promise.all([page.waitForEvent('framedetached',{predicate:f=>f===frame}),page.locator('.floating-actions>button').first().click()]);
  await page.waitForFunction(()=>document.querySelector('iframe')?.contentDocument?.querySelector('.accounts-table tbody tr'));
  const fresh=page.frames().find(f=>f!==page.mainFrame());await fresh.locator('.accounts-table tbody tr').first().waitFor();
  assert.equal(await fresh.locator('.filter-options select').first().inputValue(),'available');
  checks.push(`${width}: official refresh remounts plugin and preserves the account filter`);
  await page.locator('section[aria-label="通知"] button[aria-label="关闭"]').last().click();
  await page.locator('.language-menu>.btn').click();await page.locator('.language-menu-popover').waitFor();
  const heading=await fresh.locator('.page-heading').boundingBox();
  assert.equal(heading.y,before.y);
  await Promise.all([page.waitForEvent('framedetached',{predicate:f=>f===fresh}),page.locator('.language-menu-option').nth(2).click()]);
  await page.waitForFunction(()=>document.querySelector('iframe')?.contentDocument?.documentElement?.lang==='en');
  const english=page.frames().find(f=>f!==page.mainFrame());await english.locator('.accounts-table tbody tr').first().waitFor();assert.equal(await english.locator('.filter-options select').first().inputValue(),'available');
  checks.push(`${width}: official language menu switches iframe language without moving the workspace or losing filter`);
  await context.close();
 }
 fs.mkdirSync('validation/iteration-12',{recursive:true});fs.writeFileSync('validation/iteration-12/official-browser.json',JSON.stringify({checks,measurements,officialFrontend:'CPAMC v1.25.3 unmodified release',sha256:hash,backend:'local fixture, not official CPA or real accounts'},null,2));console.log(checks.join('\n'));
}finally{await b.close()}
