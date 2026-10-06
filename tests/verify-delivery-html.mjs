import {chromium} from '../frontend/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import crypto from 'node:crypto';
const out=new URL('../../deliverables/',import.meta.url);
const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
try{
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],external=[];
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',r=>{if(!r.request().url().startsWith('file:')&&!r.request().url().startsWith('data:')){external.push(r.request().url());return r.abort()}return r.continue()});
 await page.goto(new URL('WorkBuddy-交付说明.html',out).href);
 await page.locator('details').evaluateAll(nodes=>nodes.forEach(n=>n.open=true));
 const imageCount=Object.keys(JSON.parse(fs.readFileSync(new URL('DELIVERY-META.json',out),'utf8')).screenshots.screenshots).length;assert.equal(await page.locator('figure img').count(),imageCount);
 // Validate each screenshot in view, rather than requesting simultaneous
 // offscreen decodes (Chromium may reject those under its decode budget).
 for(const image of await page.locator('figure img').all()){await image.scrollIntoViewIfNeeded();await image.evaluate(async img=>{await img.decode();if(!img.naturalWidth||!img.naturalHeight)throw Error('empty screenshot')})}
 await page.locator('.shot').first().click();assert.equal(await page.locator('dialog').evaluate(d=>d.open),true);await page.keyboard.press('Escape');assert.equal(await page.locator('dialog').evaluate(d=>d.open),false);
 for(const width of [390,768,1440]){await page.setViewportSize({width,height:900});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'overflow at '+width)}
 assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
 fs.writeFileSync(new URL('HTML-CHECK.json',out),JSON.stringify({result:'PASS',html_sha256:crypto.createHash('sha256').update(fs.readFileSync(new URL('WorkBuddy-交付说明.html',out))).digest('hex'),embeddedImages:imageCount,imageDecode:'PASS',zoomAndEscape:'PASS',widthsChecked:[390,768,1440],externalRequests:external,pageErrors:errors},null,2));
 console.log('HTML images, zoom/Escape, 390/768/1440px widths, offline network and scripts: PASS');
}finally{await browser.close()}
