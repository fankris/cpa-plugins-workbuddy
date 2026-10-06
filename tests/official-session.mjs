export const officialBase='http://127.0.0.1:8080';
export async function officialSession(browser,viewport){
 const context=await browser.newContext({viewport,locale:'zh-CN'});
 await context.route('**/*',r=>new URL(r.request().url()).origin===officialBase?r.continue():r.abort());
 await context.addInitScript(()=>{if(window===parent&&!sessionStorage.getItem('official-fixture-initialized')){localStorage.setItem('apiBase',JSON.stringify(location.origin));localStorage.setItem('managementKey',JSON.stringify('official-fixture-only'));localStorage.setItem('isLoggedIn','true');localStorage.setItem('cli-proxy-language',JSON.stringify({state:{language:'zh-CN'},version:0}));sessionStorage.setItem('official-fixture-initialized','true')}});
 const page=await context.newPage();await page.goto(officialBase+'/official/management.html#/plugin-pages/workbuddy/0');
 await page.locator('iframe').waitFor({timeout:15000});
 const frame=page.frames().find(f=>f!==page.mainFrame());await frame.locator('.accounts-table tbody tr').first().waitFor();
 return {context,page,frame};
}
