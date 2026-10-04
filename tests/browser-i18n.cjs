/* Real browser test with local mock hosts only. No user credentials or upstream calls. */
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = path.resolve(__dirname, '..');
(async () => {
  let browser, server; let apiRequests = 0; const pageErrors=[];const denied=[];
  try {
    server = http.createServer((req,res) => {
      const pathname = new URL(req.url,'http://local').pathname;
      if(pathname === '/host') {
        res.setHeader('Content-Type','text/html; charset=utf-8');
        res.end('<!doctype html><html><body><input id="hostInput"><iframe id="plugin" src="/v0/resource/plugins/workbuddy/panel" style="width:100%;height:1000px"></iframe></body></html>');return;
      }
      if(pathname.startsWith('/v0/management/') || pathname.startsWith('/v8/management/')) {apiRequests++;res.statusCode=401;res.end('{}');return;}
      const file=({'/v0/resource/plugins/workbuddy/panel':'panel.html','/v0/resource/plugins/workbuddy/panel.js':'panel.js','/v0/resource/plugins/workbuddy/panel-i18n.js':'panel-i18n.js'})[pathname];
      if(file){res.setHeader('Content-Type',file.endsWith('.html')?'text/html; charset=utf-8':'application/javascript; charset=utf-8');res.end(fs.readFileSync(path.join(root,file)));return;}
      res.statusCode=404;res.end('not found');
    });
    await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
    const base='http://127.0.0.1:'+server.address().port;
    const cross='http://localhost:'+server.address().port;
    browser=await chromium.launch({headless:true,args:['--no-sandbox']});
    const context=await browser.newContext({locale:'en-US'});
    await context.route('**/*',route=>{if(![base,cross].includes(new URL(route.request().url()).origin)){denied.push(route.request().url());return route.abort();}return route.continue();});
    await context.addInitScript(()=>{
      if(window===window.top)localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:'en'},version:0}));
    });
    const page=await context.newPage();page.on('pageerror',e=>pageErrors.push(e.message));
    await page.goto(base+'/host');
    const frame=page.frames().find(f=>f.url().endsWith('/panel'));
    assert.ok(frame);
    await frame.waitForFunction(()=>document.documentElement.lang==='en');
    assert.equal(await frame.title(),'WorkBuddy 面板','rename was withdrawn');
    assert.match(await frame.locator('#workspaceAccountsTab').innerText(),/Accounts/);
    await frame.locator('#accountSearch').fill('do-not-clear');
    await frame.locator('#keyInput').fill('unsubmitted-local-fixture');
    await frame.evaluate(()=>{document.getElementById('importRaw').value='unsaved text';document.getElementById('settingsDisabledCount').textContent='7';});
    const initialRequests=apiRequests;
    for(const [locale,label] of [['zh-TW','帳號總覽'],['ru','Аккаунты'],['zh-CN','账号总览'],['en','Accounts']]) {
      await page.evaluate(lang=>localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:lang},version:0})),locale);
      await frame.waitForFunction(lang=>document.documentElement.lang===lang,locale);
      assert.match(await frame.locator('#workspaceAccountsTab').innerText(),new RegExp(label));
      assert.equal(await frame.locator('#accountSearch').inputValue(),'do-not-clear');
      assert.equal(await frame.locator('#keyInput').inputValue(),'unsubmitted-local-fixture');
      assert.equal(await frame.locator('#importRaw').inputValue(),'unsaved text');
      assert.equal(await frame.locator('#settingsDisabledCount').innerText(),'7');
    }
    assert.equal(apiRequests,initialRequests,'language switching must not trigger API actions');
    await frame.evaluate(()=>{const n=document.createElement('span');n.id='lateLabel';n.dataset.i18n='loading';n.textContent='加载中…';document.body.append(n);});
    await frame.waitForFunction(()=>document.getElementById('lateLabel').textContent==='Loading…');
    await page.evaluate(()=>localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:'ru'}})));
    await frame.waitForFunction(()=>document.documentElement.lang==='ru');
    await frame.evaluate(()=>location.reload());
    await frame.waitForFunction(()=>document.documentElement.lang==='ru');
    assert.match(await frame.locator('#workspaceAccountsTab').innerText(),/Аккаунты/);
    // A frame cannot opt into an invented cross-origin message bridge.
    await page.evaluate(()=>document.querySelector('iframe').contentWindow.postMessage({type:'language',language:'en'},location.origin));
    assert.equal(await frame.evaluate(()=>document.documentElement.lang),'ru');
    await frame.evaluate(()=>{
      window.dispatchEvent(new Event('pagehide'));window.dispatchEvent(new Event('pageshow'));
      const el=document.createElement('span');el.id='resumedLabel';el.dataset.i18n='close';document.body.append(el);
    });
    await frame.waitForFunction(()=>document.getElementById('resumedLabel').textContent==='Закрыть');
    assert.deepEqual(pageErrors,[]);assert.deepEqual(denied,[]);
    // Mobile layout uses the existing responsive shell; keep a screenshot for review.
    await page.setViewportSize({width:390,height:844});
    await frame.evaluate(()=>document.getElementById('keyInput').value='');
    await page.screenshot({path:path.join(root,'validation/language-browser.png'),fullPage:true});
    // Different origin, same local test server: never pretend host language is readable.
    await page.locator('#plugin').evaluate((el,url)=>{el.src=url;},cross+'/v0/resource/plugins/workbuddy/panel');
    const crossFrame=await page.locator('#plugin').contentFrame();
    await crossFrame.locator('html[data-language-source="host-unavailable"]').waitFor();
    assert.equal(await crossFrame.locator('#languageSyncNotice').isVisible(),true);
    await page.evaluate(()=>localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:'zh-TW'}})));
    assert.equal(await crossFrame.locator('html').getAttribute('lang'),'en');
    assert.deepEqual(pageErrors,[]);assert.deepEqual(denied,[]);
    const result={result:'PASS',crossOriginExplicitFallbackTested:true,resumeObserverTested:true,browser:'Chromium',sameOriginHostFixture:true,locales:['en','zh-CN','zh-TW','ru'],formAndCounterStatePreserved:true,noLanguageTriggeredApiCalls:true,reloadPreservesHostLanguage:true,unknownMessageIgnored:true,pageErrors,externalRequests:denied.length,realCPAMCIntegrationTested:false};
    fs.writeFileSync(path.join(root,'validation/browser-results.json'),JSON.stringify(result,null,2)+'\n');
    console.log(JSON.stringify(result,null,2));
  } finally {if(browser)await browser.close();if(server)await new Promise(resolve=>server.close(resolve));}
})().catch(error=>{console.error(error);process.exitCode=1;});
