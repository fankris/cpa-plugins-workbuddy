import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
const base='http://127.0.0.1:8080',checks=[],measurements=[],errors=[];
const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
async function noOverlap(page,frame,selector){
 const controls=await frame.locator(selector).all();
 for(const control of controls){if(!await control.isVisible())continue;const b=await control.boundingBox();if(!b)continue;
  for(const [x,y] of [[b.x+2,b.y+2],[b.x+b.width-2,b.y+2],[b.x+b.width/2,b.y+b.height/2]])assert.ok(await page.evaluate(({x,y})=>document.elementFromPoint(x,y)?.tagName==='IFRAME',{x,y}),'host blocks '+selector);
 }
}
try{
 for(const width of [1366,1024,390,320])for(const host of ['cpamp','cpamc']){
  const context=await browser.newContext({viewport:{width,height:width>640?768:844},locale:'zh-CN'}),page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base+'/host-'+host);const frame=page.frames().find(f=>f!==page.mainFrame());await frame.locator('tbody tr').first().waitFor();
  await frame.waitForFunction(()=>!!document.documentElement.dataset.hostLayout);
  const m=await frame.evaluate(()=>({mode:document.documentElement.dataset.hostLayout,padding:parseFloat(getComputedStyle(document.querySelector('main')).paddingTop),right:parseFloat(getComputedStyle(document.querySelector('.section-nav')).marginRight),nav:document.querySelector('.section-nav').getBoundingClientRect().toJSON(),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth}));
  assert.equal(m.overflow,false);assert.equal(m.mode,host==='cpamp'?'external-header':width>640?'overlay-band':'overlay-mobile');
  if(host==='cpamp')assert.ok(m.padding<=12,'duplicated titlebar space');
  else if(width>640){assert.equal(m.padding,12);assert.ok(m.right>100,'missing corner exclusion')}
  await noOverlap(page,frame,'.section-nav button,.page-controls button');
  await frame.locator('.accounts-table tbody tr').first().getByRole('button',{name:'查看详情',exact:true}).click();
  await noOverlap(page,frame,'dialog .modal-head>button');await frame.locator('dialog .modal-head>button').click();
  if(host==='cpamc'){
   const before=await page.locator('.floating-actions').evaluate(n=>n.outerHTML);
   await frame.locator('.section-nav').getByRole('button',{name:'模型诊断',exact:true}).click();
   assert.equal(await page.locator('.floating-actions').evaluate(n=>n.outerHTML),before,'plugin mutated host toolbar');
  }
  for(const locale of ['en','ru','zh-TW']){
   await frame.evaluate(lang=>{localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:lang}}));window.dispatchEvent(new StorageEvent('storage',{key:'cli-proxy-language'}))},locale);
   await frame.waitForFunction(lang=>document.documentElement.lang===lang,locale);
   await noOverlap(page,frame,'.section-nav button,.page-controls button');
  }
  measurements.push({host,width,...m});checks.push(`${host} ${width}: measured safe area, nav and dialog close unobstructed`);
  await context.close();
 }
 const page=await browser.newPage({viewport:{width:1366,height:768}});await page.goto(base+'/host-cpamc');const f=page.frames().find(f=>f!==page.mainFrame());await f.locator('tbody tr').first().waitFor();
 await page.locator('.floating-actions').evaluate(n=>n.style.width='500px');
 await f.waitForFunction(()=>parseFloat(getComputedStyle(document.querySelector('.section-nav')).marginRight)>480);
 checks.push('toolbar resize recalculates corner exclusion');
 await page.locator('.floating-actions').evaluate(n=>n.style.display='none');
 await f.waitForFunction(()=>document.documentElement.dataset.hostLayout==='overlay-band'&&getComputedStyle(document.querySelector('.section-nav')).marginRight==='0px');
 assert.equal(await f.locator('.section-nav').evaluate(n=>getComputedStyle(n).marginRight),'0px');checks.push('hidden toolbar releases exclusion width while preserving the navigation band');await page.close();
 const cross=await browser.newPage({viewport:{width:390,height:844}});await cross.goto(base+'/host-cross');const cf=cross.frames().find(f=>f!==cross.mainFrame());await cf.locator('tbody tr').first().waitFor();assert.equal(await cf.evaluate(()=>document.documentElement.dataset.hostLayout),'unmeasurable');await noOverlap(cross,cf,'.section-nav button');checks.push('cross-origin parent uses conservative fallback without reading parent DOM');await cross.close();
 const lifecycle=await browser.newPage();
 await lifecycle.addInitScript(()=>{if(parent!==window)return;window.__layoutListeners={resize:0,scroll:0};const add=window.addEventListener,remove=window.removeEventListener;window.addEventListener=function(type,...args){if(type in window.__layoutListeners)window.__layoutListeners[type]++;return add.call(this,type,...args)};window.removeEventListener=function(type,...args){if(type in window.__layoutListeners)window.__layoutListeners[type]--;return remove.call(this,type,...args)}});
 await lifecycle.goto(base+'/host-cpamc');const lf=lifecycle.frames().find(f=>f!==lifecycle.mainFrame());await lf.locator('tbody tr').first().waitFor();
 assert.deepEqual(await lifecycle.evaluate(()=>window.__layoutListeners),{resize:1,scroll:1});await lifecycle.locator('iframe').evaluate(n=>n.remove());
 await lifecycle.waitForFunction(()=>window.__layoutListeners.resize===0&&window.__layoutListeners.scroll===0);checks.push('iframe removal releases parent listeners instead of leaking across remounts');await lifecycle.close();
 assert.deepEqual(errors,[]);fs.mkdirSync('validation/iteration-9',{recursive:true});fs.writeFileSync('validation/iteration-9/host-layout-browser.json',JSON.stringify({checks,measurements,errors,fixtureOnly:true},null,2));console.log(checks.join('\n'));
}finally{await browser.close()}
