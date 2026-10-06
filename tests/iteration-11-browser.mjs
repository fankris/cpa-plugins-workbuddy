import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';import fs from 'node:fs';
const b=await chromium.launch({headless:true,args:['--no-sandbox']}),base='http://127.0.0.1:8080',checks=[],measurements=[];
try{
 for(const [width,height] of [[320,568],[390,844],[844,390]]){
  const c=await b.newContext({viewport:{width,height},locale:'zh-CN',isMobile:true,hasTouch:true}),p=await c.newPage();await p.goto(base+'/host-cpamc');const f=p.frames().find(x=>x!==p.mainFrame());await f.locator('tbody tr').first().waitFor();
  for(const [index,tab] of ['accounts','models','tasks','results'].entries()){
   await f.locator('.section-nav button').nth(index).click();
   const m=await f.evaluate(()=>{const nav=document.querySelector('.section-nav').getBoundingClientRect(),heading=document.querySelector('.host-workspace-heading').getBoundingClientRect();return{height:innerHeight,nav:nav.toJSON(),heading:heading.toJSON(),padding:getComputedStyle(document.querySelector('main')).paddingTop,first:document.querySelector('tbody tr')?.getBoundingClientRect().top}});
   assert.equal(m.padding,'8px');assert.ok(m.nav.bottom<=height+1&&(width<=640?m.nav.top>=height-72:m.nav.top<20),'navigation not in intended band: '+JSON.stringify({width,tab,...m}));
   if(width<=640){assert.ok(m.heading.top<16&&m.heading.height>30,'missing actual header content');
   const label=await f.locator('.host-workspace-heading span').boundingBox();
   assert.ok(await p.evaluate(({x,y})=>document.elementFromPoint(x,y)?.tagName==='IFRAME',{x:label.x+label.width/2,y:label.y+label.height/2}),'workspace title blocked');}
   if(tab==='accounts'){assert.ok(m.first<245,'account content still pushed down');await f.waitForFunction(()=>{const v=document.querySelector('.accounts-table .table-scroll');return v.scrollHeight<=v.clientHeight+1})}
   measurements.push({width,tab,...m});
  }
  checks.push(`${width}x${height}: intentional navigation band on all four workspaces, no page-padding workaround`);await c.close();
 }
 const c=await b.newContext({viewport:{width:1366,height:768},locale:'zh-CN'}),p=await c.newPage();await p.goto(base+'/host-cpamc');const f=p.frames().find(x=>x!==p.mainFrame());await f.locator('tbody tr').first().waitFor();
 const before=await f.locator('.page-heading').boundingBox();await p.locator('.floating-actions').evaluate(n=>n.style.width='500px');await f.waitForFunction(()=>parseFloat(getComputedStyle(document.querySelector('.section-nav')).marginRight)>480);const after=await f.locator('.page-heading').boundingBox();assert.equal(before.y,after.y);assert.equal(before.x,after.x);assert.equal(before.width,after.width);checks.push('desktop toolbar width changes do not shift the business work area');await c.close();
 fs.mkdirSync('validation/iteration-11',{recursive:true});fs.writeFileSync('validation/iteration-11/workspace-browser.json',JSON.stringify({checks,measurements,fixtureOnly:true},null,2));console.log(checks.join('\n'));
}finally{await b.close()}
