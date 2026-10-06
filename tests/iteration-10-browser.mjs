import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
const base='http://127.0.0.1:8080',checks=[],errors=[];
const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
try{
 for(const width of [320,390,1366])for(const host of ['standalone','cpamp','cpamc']){
  const context=await browser.newContext({viewport:{width,height:width>640?768:844},locale:'zh-CN'}),page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base+(host==='standalone'?'':'/host-'+host));const view=host==='standalone'?page:page.frames().find(f=>f!==page.mainFrame());await view.locator('tbody tr').first().waitFor();
  for(const locale of ['zh-CN','en','ru','zh-TW']){
   await view.evaluate(lang=>{localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:lang}}));window.dispatchEvent(new StorageEvent('storage',{key:'cli-proxy-language'}))},locale);
   await view.waitForFunction(lang=>document.documentElement.lang===lang,locale);
   assert.equal(await view.locator('.settings-action,a[href*="/oauth"],a[href*="#/plugins"]').count(),0);
   for(const name of ['原生登录','原生登入','Native login','Вход через CPA'])assert.equal(await view.getByRole('button',{name,exact:true}).count()+await view.getByRole('link',{name,exact:true}).count(),0);
   assert.equal(await view.locator('.more-actions').count(),0,'account import should not need More');
   const selector=width<=640?'.mobile-import':'.heading-actions .primary';
   const controls=await view.locator('.page-controls button').all();
   for(const control of controls){if(!await control.isVisible())continue;const b=await control.boundingBox();assert.ok(b.x>=0&&b.x+b.width<=width+1,'toolbar overflow '+locale);}
   await view.locator(selector).click();await view.locator('dialog textarea').fill('unsaved fixture');await view.locator('dialog .modal-head>button').click();
   for(const index of [1,2]){await view.locator('.section-nav [data-section="'+['accounts','models','tasks','dashboard'][index]+'"]').click();assert.equal(await view.locator('.more-actions,#page-actions').count(),0,'empty actions menu on models/tasks');}
   await view.locator('.section-nav [data-section="accounts"]').click();
  }
  checks.push(`${host} ${width}: four locales, no duplicated settings/login, import opens directly, no empty menus`);
  await context.close();
 }
 assert.deepEqual(errors,[]);fs.mkdirSync('validation/iteration-10',{recursive:true});fs.writeFileSync('validation/iteration-10/toolbar-browser.json',JSON.stringify({checks,errors,fixtureOnly:true},null,2));console.log(checks.join('\n'));
}finally{await browser.close()}
