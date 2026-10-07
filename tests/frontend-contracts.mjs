import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {build} from '../frontend/node_modules/esbuild/lib/main.js';
const compile=async file=>(await build({entryPoints:[file],bundle:true,write:false,format:'cjs',platform:'browser',define:{'process.env.NODE_ENV':'"production"'}})).outputFiles[0].text;
const simple={module:{exports:{}},exports:{}};simple.exports=simple.module.exports;vm.runInNewContext(await compile('frontend/src/outcome.ts'),simple);const outcome=simple.module.exports.outcome;
for(const [label,input,want] of [
 ['discovery fallback is not success',{status:'fallback',models:[{id:'fallback'}]},'partial'],['pinned discovery is skipped',{status:'skipped'},'skipped'],['unknown response',null,'unconfirmed'],['pending is not success',{status:'running'},'pending'],['cooperative cancel pending',{status:'running',cancel_requested:true},'cancelRequested'],['confirmed cancellation',{status:'canceled'},'canceled'],['repeat reward',{already_claimed:true},'already'],['skipped token refresh',{status:'skipped'},'skipped'],['single failure',{results:[{status:'failed',detail:'expired'}]},'failed'],['mixed outcome',{results:[{ok:true},{error:'failed'}]},'partial'],['nested bulk failure',{results:[{summary:{fail:1,success:0},results:[{error:'bad'}]}]},'failed'],['partially accepted run',{status:'failed',accepted:2,error:'some failed'},'partial'],['check-in already complete',{results:[{reason:'already'}]},'already'],['normal completion',{ok:true},'success']
])test(label,()=>assert.equal(outcome(input),want));
const storage=new Map([['workbuddy-mgmt-key','fixture-key']]);
const win={location:{pathname:'/mount/v0/resource/plugins/workbuddy/panel',href:'http://local/mount/v0/resource/plugins/workbuddy/panel',host:'local'},navigator:{userAgent:'test'}};win.parent=win;win.top=win;win.self=win;
const env={module:{exports:{}},exports:{},window:win,parent:win,location:win.location,navigator:win.navigator,document:{querySelector:()=>null},localStorage:{getItem:()=>null},sessionStorage:{getItem:k=>storage.get(k),setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)},TextEncoder,TextDecoder,URL,AbortController,setTimeout,clearTimeout,history:{replaceState(){}},fetch:()=>{throw Error('fetch not configured')}};win.localStorage=env.localStorage;env.exports=env.module.exports;vm.runInNewContext(await compile('frontend/src/api.ts'),env);const api=env.module.exports;
test('v8 native path and v0 extension preserve reverse proxy prefix',()=>assert.equal(api.paths('/mount/v0/resource/plugins/workbuddy/panel').native,'/mount/v8/management'));
test('host resource path can be customized without moving extension namespace',()=>assert.equal(api.paths('/outer/resources/wb/panel','/base/v0/management/plugins/workbuddy','/resources/wb').plugin,'/outer/base/v0/management/plugins/workbuddy'));
test('unsafe paths rejected',()=>{for(const x of ['//evil/x','https://evil/x','/x/../key','/x?key=secret','/x%2fy'])assert.equal(api.safePath(x),'')});
test('credential fields recursively redacted',()=>{const r=api.redact({auth:{access_token:'secret',api_key:'secret',cookies:'secret'},status:'ok'});assert.ok(!JSON.stringify(r).includes('secret'));assert.equal(r.status,'ok')});
test('model config writes preserve opaque fields and target only plugin object',async()=>{
 const calls=[];let stored={future:{preserve:true},models_enabled:[]};
 env.fetch=async(url,opts)=>{calls.push({url,method:opts.method});if(opts.method==='PUT')stored=JSON.parse(opts.body);return{ok:true,status:200,text:async()=>JSON.stringify(stored)}};
 await api.patchConfig({models_enabled:['model-a']});
 assert.deepEqual(stored,{future:{preserve:true},models_enabled:['model-a']});
 assert.equal(calls.filter(x=>x.method==='PUT').length,1);
 assert.ok(calls.every(x=>x.url==='/mount/v8/management/config/plugins/configs/workbuddy'));
});
test('failed transport after mutation is unconfirmed, never an automatic retry',async()=>{let calls=0;env.fetch=async()=>{calls++;throw Error('connection dropped')};await assert.rejects(api.request('/tasks/claim','POST',{}),e=>e.uncertain&&e.message==='outcomeUnknown');assert.equal(calls,1)});
test('failed task terminal snapshot remains inspectable',async()=>{env.fetch=async()=>({ok:false,status:400,text:async()=>JSON.stringify({run_id:'fixture-run',status:'failed',error:'some failed',accepted:2})});const r=await api.request('/tasks/status?run_id=fixture-run');assert.equal(outcome(r),'partial')});
const dictionary={module:{exports:{}},exports:{}};dictionary.exports=dictionary.module.exports;vm.runInNewContext(await compile('frontend/src/i18n.ts'),dictionary);
test('every new UI message has four nonempty translations',()=>{for(const [key,row] of Object.entries(dictionary.module.exports.translations)){assert.equal(row.length,4,key);assert.ok(row.every(v=>typeof v==='string'&&v.trim()),key)}});
test('new bundle uses no synthetic activity route or duplicate native settings editor',()=>{const app=fs.readFileSync('frontend/src/App.tsx','utf8');assert.ok(!app.includes('/tasks/light'));assert.ok(app.includes('/credentials/refresh'));assert.ok(!app.includes("nativePage('plugins')"));assert.ok(!app.includes("nativePage('oauth')"));assert.ok(!app.includes("settingsOpen"));assert.ok(!app.includes('setEdits'))});
const ids={module:{exports:{}},exports:{}};ids.exports=ids.module.exports;vm.runInNewContext(await compile('frontend/src/operation-id.ts'),ids);
test('operation IDs work without crypto and remain unique',()=>{const values=Array.from({length:1000},()=>ids.module.exports.operationID());assert.equal(new Set(values).size,1000)});
test('invalid JSON after mutation is unconfirmed and not retried',async()=>{let calls=0;env.fetch=async()=>{calls++;return{ok:true,status:200,text:async()=>'<html>proxy response</html>'}};await assert.rejects(api.request('/import','POST',{json:{}}),e=>e.uncertain&&e.message==='outcomeUnknown');assert.equal(calls,1)});
test('native page has no nested brand header, dock or command palette',()=>{const app=fs.readFileSync('frontend/src/App.tsx','utf8');assert.ok(!app.includes('className="topbar"'));assert.ok(!app.includes('className="dock"'));assert.ok(!app.includes('setCommandOpen'));assert.ok(!app.includes('crypto.randomUUID'))});

// CPA configuration is authoritative, including after a successful PUT.
const response=value=>({ok:true,status:200,text:async()=>JSON.stringify(value)});
test('config readback mismatch is unconfirmed and never replayed',async()=>{
 let writes=0;env.fetch=async(_url,opts)=>{if(opts.method==='PUT')writes++;return response({models_disabled:[]})};
 await assert.rejects(api.patchConfig({models_disabled:['fixture-model']}),e=>e.uncertain&&e.message==='configUnconfirmed');assert.equal(writes,1);
});
test('config readback failure after PUT is not reported as a failed write',async()=>{
 let reads=0,writes=0;env.fetch=async(_url,opts)=>{if(opts.method==='PUT'){writes++;return response({})}if(++reads===2)throw Error('offline');return response({})};
 await assert.rejects(api.patchConfig({models_disabled:['fixture-model']}),e=>e.uncertain&&e.message==='configUnconfirmed');assert.equal(writes,1);
});
test('malformed config snapshot cannot overwrite the CPA plugin object',async()=>{
 for(const value of [[],42,'invalid']){let writes=0;env.fetch=async(_url,opts)=>{if(opts.method==='PUT')writes++;return response(value)};await assert.rejects(api.patchConfig({models_disabled:[]}),e=>!e.uncertain);assert.equal(writes,0)}
});
test('readback compares touched fields structurally and preserves unknown keys',async()=>{
 let stored={opaque:{untouched:1},removeMe:true};
 env.fetch=async(_url,opts)=>{if(opts.method==='PUT'){stored=JSON.parse(opts.body);stored.nested={b:2,a:1}}return response(stored)};
 const result=await api.patchConfig({nested:{a:1,b:2},removeMe:null});assert.equal(result.opaque.untouched,1);assert.equal(Object.hasOwn(result,'removeMe'),false);
});
test('null removal must be confirmed absent, not merely returned as null',async()=>{
 env.fetch=async()=>response({removeMe:null});await assert.rejects(api.patchConfig({removeMe:null}),e=>e.uncertain);
});
test('queued config edit cannot migrate to a new management identity',async()=>{
 let release,started;const held=new Promise(resolve=>release=resolve),reading=new Promise(resolve=>started=resolve);let calls=0,writes=0;
 storage.set('workbuddy-mgmt-key','queue-before');
 env.fetch=async(_url,opts)=>{calls++;if(opts.method==='PUT')writes++;if(calls===1){started();await held}return response({})};
 const first=api.patchConfig({models_disabled:['a']});await reading;const second=api.patchConfig({models_disabled:['b']});
 const results=Promise.allSettled([first,second]);storage.set('workbuddy-mgmt-key','queue-after');release();
 const settled=await results;assert.equal(writes,0);assert.equal(calls,1);for(const r of settled){assert.equal(r.status,'rejected');assert.equal(r.reason.message,'connectionChanged')}
 storage.set('workbuddy-mgmt-key','fixture-key');
});
test('identity change after PUT prevents cross-session readback and reports unknown',async()=>{
 let calls=0;storage.set('workbuddy-mgmt-key','write-before');
 env.fetch=async(_url,opts)=>{calls++;if(opts.method==='PUT')storage.set('workbuddy-mgmt-key','write-after');return response({})};
 await assert.rejects(api.patchConfig({models_disabled:[]}),e=>e.uncertain&&e.message==='configUnconfirmed');assert.equal(calls,2);storage.set('workbuddy-mgmt-key','fixture-key');
});
test('config queue recovers from rejection and reads fresh host configuration',async()=>{
 let stored={future:true};env.fetch=async(_url,opts)=>{if(opts.method==='PUT')stored=JSON.parse(opts.body);return response(stored)};
 const result=await api.patchConfig({models_disabled:['b']});assert.deepEqual(JSON.parse(JSON.stringify(result)),{future:true,models_disabled:['b']});
});
const accountModule={module:{exports:{}},exports:{}};accountModule.exports=accountModule.module.exports;vm.runInNewContext(await compile('frontend/src/account-status.ts'),accountModule);
for(const [label,account,status] of [
 ['missing runtime',{},'unknown'],['empty runtime',{runtime:{}},'unknown'],['future status',{runtime:{status:'pending'}},'unknown'],
 ['CPA error',{runtime:{status:'error'}},'needsAttention'],['CPA disabled',{runtime:{status:'disabled'}},'disabled'],
 ['explicit disable dominates cooldown',{disabled:true,runtime:{status:'active',cooldown_seconds:10}},'disabled'],
 ['CPA cooldown',{runtime:{status:'active',cooldown_seconds:10}},'cooldown'],['CPA excludes routing',{runtime:{status:'active',unavailable:true}},'needsAttention'],
 ['quota exhausted',{exhausted:true,runtime:{status:'active'}},'exhausted'],['CPA active',{runtime:{status:'active'}},'available'],
 ['old failures do not override recovered CPA state',{runtime:{status:'active',success:0,failed:5}},'available'],
 ['token date is not host runtime',{expires_at:'2099-01-01',runtime:{}},'unknown']
])test('CPA availability: '+label,()=>assert.equal(accountModule.module.exports.accountStatus(account),status));

for(const [label,account,filter,want] of [
 ['all includes unknown',{},'all',true],['attention includes cooldown',{runtime:{status:'active',cooldown_seconds:10}},'attention',true],
 ['attention includes exhausted',{exhausted:true},'attention',true],['attention includes CPA error',{runtime:{status:'error'}},'attention',true],
 ['attention excludes active',{runtime:{status:'active'}},'attention',false],['attention does not invent unknown failures',{},'attention',false],
 ['attention respects explicit disable',{disabled:true,runtime:{status:'error'}},'attention',false],['exact available filter',{runtime:{status:'active'}},'available',true]
])test('desktop quick filter: '+label,()=>assert.equal(accountModule.module.exports.accountMatchesFilter(account,filter),want));
const wf={module:{exports:{}},exports:{}};wf.exports=wf.module.exports;vm.runInNewContext(await compile('frontend/src/workspace-filters.ts'),wf);const filters=wf.module.exports;
for(const [input,status] of [[{claimed:true,locked:true,claimable:true},'claimed'],[{locked:true,claimable:true},'locked'],[{claimable:true,accept_status:'accepted'},'claimable'],[{accept_status:'accepted'},'accepted'],[{},'notAccepted']])test('task filter/header/action agree: '+status,()=>{assert.equal(filters.taskState(input),status);for(const f of ['claimed','locked','claimable','accepted','notAccepted'])assert.equal(filters.taskMatchesFilter(input,f),status===f)});
const apiCode=await compile('frontend/src/api.ts');
function isolatedAPI(fetch){const box={...env,module:{exports:{}},exports:{},fetch};box.exports=box.module.exports;vm.runInNewContext(apiCode,box);return box.module.exports}
for(const [label,status,text]of [['plain404',404,'404 page not found'],['html200',200,'<!doctype html>wrong frontend'],['empty200',200,''],['array200',200,'[]']])test('CPAMP config read negotiates '+label+' without replaying writes',async()=>{let stored={checkin_auto:true,future:{keep:1}},calls=[];const a=isolatedAPI(async(url,opts)=>{calls.push([url,opts.method]);if(url.includes('/v8/'))return{status,ok:status===200,text:async()=>text};if(opts.method==='PUT')stored=JSON.parse(opts.body);return{status:200,ok:true,text:async()=>JSON.stringify(stored)}});await a.patchConfig({checkin_auto:false});assert.equal(stored.checkin_auto,false);assert.equal(stored.future.keep,1);assert.equal(calls.filter(x=>x[0].includes('/v8/')).length,1);assert.equal(calls.filter(x=>x[1]==='PUT').length,1);assert.ok(calls.filter(x=>x[1]==='PUT').every(x=>x[0]==='/mount/v0/management/plugins/workbuddy/config'))});
test('CPAMP config permission failure never probes a second endpoint',async()=>{const calls=[];const a=isolatedAPI(async(url)=>{calls.push(url);return{status:403,ok:false,text:async()=>'{"error":"forbidden"}'}});await assert.rejects(a.patchConfig({checkin_auto:false}),/forbidden/);assert.equal(calls.length,1)});
test('CPAMP non-JSON PUT is unconfirmed and never retried',async()=>{let writes=0;const a=isolatedAPI(async(url,o)=>{if(url.includes('/v8/'))return{status:404,ok:false,text:async()=>'not found'};if(o.method==='PUT'){writes++;return{status:200,ok:true,text:async()=>'<html>bad ack</html>'}}return{status:200,ok:true,text:async()=>'{"checkin_auto":true}'}});await assert.rejects(a.patchConfig({checkin_auto:false}),e=>e.uncertain);assert.equal(writes,1)});
test('config readback stays on negotiated endpoint after a write',async()=>{let writes=0,legacyReads=0,primaryReads=0;const a=isolatedAPI(async(url,o)=>{if(url.includes('/v8/')){primaryReads++;return{status:404,ok:false,text:async()=>'not found'}}if(o.method==='PUT'){writes++;return{status:200,ok:true,text:async()=>'{}'}}legacyReads++;return legacyReads===1?{status:200,ok:true,text:async()=>'{"checkin_auto":true}'}:{status:404,ok:false,text:async()=>'not found'}});await assert.rejects(a.patchConfig({checkin_auto:false}),e=>e.uncertain&&e.message==='configUnconfirmed');assert.equal(primaryReads,1);assert.equal(writes,1)});
const ex={module:{exports:{}},exports:{}};ex.exports=ex.module.exports;vm.runInNewContext(await compile('frontend/src/expiry.ts'),ex);const expiry=ex.module.exports;
test('earliest credit amount is not all credits expiring in a window',()=>{const now=Date.parse('2026-10-06T00:00:00Z');const r=expiry.earliestCredits({packages:[{remain:5,cycle_end:'2026-10-18T02:24:00Z'},{remain:7,cycle_end:'2026-10-18 10:24:00'},{remain:100,cycle_end:'2026-11-01T00:00:00Z'}]},now);assert.equal(r.amount,12);assert.equal(expiry.expiryDays(r.days,'zh-CN'),'12.1')});
test('expiry rolls to next batch and never shows negative days',()=>{const r=expiry.earliestCredits({packages:[{remain:9,cycle_end:'2026-10-01T00:00:00Z'},{remain:3,cycle_end:'2026-10-07T00:00:00Z'}]},Date.parse('2026-10-06T00:00:00Z'));assert.equal(r.amount,3);assert.equal(r.days,1);assert.equal(expiry.expiryDays(r.days,'en'),'1.0')});
test('missing/invalid dates remain unknown and impossible CST dates are rejected',()=>{assert.ok(Number.isNaN(expiry.expiryTimestamp('2026-02-30 12:00:00')));assert.equal(expiry.earliestCredits({packages:[{remain:7,cycle_end:'bad'}]},Date.now()).at,null);assert.equal(expiry.expiryDays(0.01,'zh-CN'),'<0.1')});
const mv={module:{exports:{}},exports:{}};mv.exports=mv.module.exports;vm.runInNewContext(await compile('frontend/src/model-view.ts'),mv);const modelView=mv.module.exports;
for(const [raw,want]of [['x0.00',0],['× 0.03',0.03],['x1',1],['free',null],['x0.1 - x0.3',null],['x-1',null],['',null]])test('model multiplier parser: '+raw,()=>assert.equal(modelView.modelMultiplier(raw),want));
const modelFixture=[{id:'unknown',credits:'?',context_length:0},{id:'paid',vendor:'A',credits:'x0.03',supports_images:false,context_length:200000},{id:'zero',vendor:'B',credits:'x0.00',supports_images:true,efforts:['high'],context_length:1000000}];
test('model cost/context order puts unknown last without mutating source',()=>{for(const key of ['creditOrder','contextOrder'])assert.equal(modelView.filterModels(modelFixture,'','all','','all',key).map(x=>x.id).join(','),'zero,paid,unknown');assert.equal(modelFixture[0].id,'unknown')});
test('model declarations never infer image support from a missing field',()=>{assert.equal(modelView.filterModels(modelFixture,'','all','','imageDeclared','sourceOrder').map(x=>x.id).join(','),'zero');assert.equal(modelView.filterModels(modelFixture,'','all','','textDeclared','sourceOrder').map(x=>x.id).join(','),'paid')});
test('model search and combined family/reasoning filters share the current pool',()=>{assert.equal(modelView.filterModels(modelFixture,'high','all','B','reasoningDeclared','sourceOrder').length,1);assert.equal(modelView.filterModels(modelFixture,'high','all','A','reasoningDeclared','sourceOrder').length,0)});

for(const [raw,want]of [['x0.34 credits',0.34],['×0.00 CREDITS',0],['x1 credit',null],['x1 credits / x2 credits',null]])test('directory multiplier suffix: '+raw,()=>assert.equal(modelView.modelMultiplier(raw),want));
test('directory boolean reasoning and derived family are searchable without altering vendor',()=>{const rows=[{id:'a',family:'Kimi',supports_reasoning:true}];assert.equal(modelView.filterModels(rows,'kimi','all','Kimi','reasoningDeclared','sourceOrder').length,1);assert.equal(rows[0].vendor,undefined)});
test('directory partial and unsupported are not success',()=>{assert.equal(outcome({status:'partial'}),'partial');assert.equal(outcome({status:'unsupported'}),'skipped')});

test('directory response validation rejects empty objects, old routing responses and unknown statuses',()=>{for(const v of [{},{models:[]},{status:'ok',models:[]},{status:'surprise',models:[],sources:[]}])assert.equal(modelView.isDirectoryResponse(v),false);for(const status of ['ok','partial','failed','unsupported'])assert.equal(modelView.isDirectoryResponse({status,models:[],sources:[]}),true)});

const navigation={module:{exports:{}},exports:{}};navigation.exports=navigation.module.exports;vm.runInNewContext(await compile('frontend/src/navigation.ts'),navigation);const nav=navigation.module.exports;
for(const [old,want]of [['results','dashboard'],['dashboard','dashboard'],['accounts','accounts'],['models','models'],['tasks','tasks'],['settings','settings'],['unknown','accounts'],[null,'accounts']])test('workspace migration: '+old,()=>assert.equal(nav.workspaceTab(old),want));
test('five workspaces and no results export or table are shipped',()=>{assert.equal(nav.workspaceIDs.join(','),'dashboard,accounts,models,tasks,settings');const app=fs.readFileSync('frontend/src/App.tsx','utf8');for(const text of ['results-table','results-toolbar','exportResults','workbuddy-results.json',"tab==='results'"])assert.ok(!app.includes(text),text);assert.ok(app.includes('operationFeedback'))});
const hubBox={module:{exports:{}},exports:{}};hubBox.exports=hubBox.module.exports;vm.runInNewContext(await compile('frontend/src/model-hub.ts'),hubBox);const hub=hubBox.module.exports;
const hubRows=[{id:'shared',name:'shared',variants:[{origin:'dynamic',channel:'cn',model:{id:'shared',name:'CN',context_length:128000,credits:'x0.03',efforts:['high','low']}},{origin:'custom',channel:'intl',model:{id:'shared',name:'Custom',context_length:1000000,credits:'x0.10',efforts:['low','high']}}]},{id:'zero',variants:[{origin:'dynamic',channel:'intl',model:{id:'zero',context_length:1000000,credits:'x0.00'}}]}];
test('hub origin and channel filters match the same variant, never a false intersection',()=>{assert.equal(hub.hubView(hubRows,'','dynamic','intl','sourceOrder').map(x=>x.id).join(','),'zero');assert.equal(hub.hubView(hubRows,'','custom','intl','sourceOrder')[0].variants.length,1)});
test('hub metadata disagreement stays explicit instead of choosing largest capacity',()=>{assert.equal(hub.commonValue(hubRows[0].variants,'context_length').varied,true);assert.equal(hub.commonValue(hubRows[0].variants,'efforts').varied,false)});
test('hub cost sort puts differing/unknown per-source amounts last and zero first',()=>assert.equal(hub.hubView(hubRows,'','all','all','creditOrder').map(x=>x.id).join(','),'zero,shared'));
test('hub filtering never mutates variants and searches source-specific metadata',()=>{const before=JSON.stringify(hubRows);assert.equal(hub.hubView(hubRows,'Custom','custom','intl','sourceOrder').length,1);assert.equal(JSON.stringify(hubRows),before)});
test('hub configuration state respects native allow and deny lists without inventing availability',()=>{assert.equal(hub.hubConfiguredDisabled('Shared',{models_enabled:['shared']},true),false);assert.equal(hub.hubConfiguredDisabled('Shared',{models_enabled:['shared'],models_disabled:['SHARED']},false),true);assert.equal(hub.hubConfiguredDisabled('new',{},false),true)});
test('hub responses require source variants, legacy catalog arrays are rejected',()=>{for(const r of [{models:[]},{status:'ok',models:[{id:'old'}],sources:[]}])assert.equal(hub.isHubResponse(r),false);assert.equal(hub.isHubResponse({status:'ok',models:hubRows,sources:[]}),true)});
test('hub malformed null variants and accounts never throw during validation',()=>{for(const r of [{status:'ok',models:[null],sources:[]},{status:'ok',models:[{id:'x',variants:[null]}],sources:[]},{status:'ok',models:[],sources:[{channel:'cn',accounts:[null]}]},{status:'ok',models:[{id:'x',variants:[{origin:'dynamic',channel:'cn',model:{efforts:3}}]}],sources:[]}])assert.equal(hub.isHubResponse(r),false)});
test('hub filtered title uses visible source rather than hidden canonical metadata',()=>assert.equal(hub.hubView(hubRows,'','custom','intl','sourceOrder')[0].name,'Custom'));
test('unsupported/unconfigured channels never masquerade as failed fetches',()=>{assert.equal(hub.hubFetchFailures([{status:'unsupported'},{status:'no_account'},{status:'unavailable'},{status:'invalid_account'}]).length,0);assert.equal(hub.hubFetchFailures([{status:'ok'},{status:'partial'},{status:'failed'}]).length,2)});
test('channel error reason is specific and does not quote upstream bodies',()=>{assert.equal(hub.hubIssue({status:'failed',endpoints:[{attempts:[{http_status:401,error:'secret body'}]}]}),'auth');assert.equal(hub.hubIssue({status:'failed',endpoints:[{attempts:[{upstream_code:429}]}]}),'rate');assert.equal(hub.hubIssue({status:'partial'}),'partial');assert.equal(hub.hubIssue({status:'failed',endpoints:[]}), 'failed')});
test('parameter basis marks real differences, does not fabricate merged consensus',()=>{assert.equal(hub.hubParameterDifference(hubRows[0].variants),true);assert.equal(hub.hubParameterDifference([hubRows[0].variants[0]]),false)});
const wd={module:{exports:{}},exports:{}};wd.exports=wd.module.exports;vm.runInNewContext(await compile('frontend/src/workspace-data.ts'),wd);const data22=wd.module.exports;
test('22 channel identity never derives from nickname',()=>{assert.equal(data22.accountService({nickname:'CN',region:'intl'}),'intl');assert.equal(data22.accountService({region:'intl',service:'global'}),'intl');assert.equal(data22.accountService({service:'intl'}),'intl')});
test('22 missing runtime boolean is unknown, not disabled',()=>{assert.equal(data22.settingState({checkin_auto:false},{},'checkin_auto'),'unknown');assert.equal(data22.settingState({checkin_auto:false},{checkin_auto:'false'},'checkin_auto'),'unknown');assert.equal(data22.settingState({checkin_auto:false},{checkin_auto:false},'checkin_auto'),'matches');assert.equal(data22.settingState({checkin_auto:true},{checkin_auto:false},'checkin_auto'),'differs')});
test('22 scoped credit merge preserves all identities and routing',()=>{const a=[{auth_index:'a',disabled:true,selected:true},{auth_index:'b',credits:{total_remain:7}}];const r=data22.mergeCreditRows(a,[{auth_index:'a',credits:{total_remain:9},disabled:false,selected:false}]);assert.equal(r.length,2);assert.equal(r[0].disabled,true);assert.equal(r[0].selected,true);assert.equal(r[1],a[1]);assert.equal(a[0].credits,undefined)});
test('22 credit failures are data errors not empty successful snapshots',()=>{const r=data22.mergeCreditRows([{auth_index:'a',credits:{total_remain:9}}],[{auth_index:'a',error:'HTTP 500'}]);assert.equal(r[0].credits,undefined);assert.equal(r[0].data_error,'HTTP 500');assert.throws(()=>data22.mergeCreditRows([],{}))});

test('23 only two hub regions; old WB selection migrates without route mutation',()=>{assert.equal(hub.hubChannels.join(','),'cn,intl');assert.equal(hub.hubSourceSelection({global:'wb',cn:'a'}).intl,'wb');assert.equal(hub.hubSourceSelection({global:'wb',intl:'cb'}).intl,'cb');assert.equal(data22.accountInChannel({region:'intl',service:'global'},'intl'),true);assert.equal(data22.accountInChannel({region:'intl',service:'intl'},'intl'),true)});
test('24 explicit server capability takes precedence over region',()=>{assert.equal(data22.businessSupported({region:'cn',capabilities:{tasks:{supported:false}}},'tasks'),false);assert.equal(data22.businessSupported({region:'cn',capabilities:{tasks:{supported:'true'}}},'tasks'),false);assert.equal(data22.businessSupported({region:'cn',capabilities:{}},'tasks'),false)});
test('24 legacy support separates CN-only from foreign-only business',()=>{for(const key of ['tasks','travel','checkin']){assert.equal(data22.businessSupported({region:'cn'},key),true);assert.equal(data22.businessSupported({region:'intl'},key),false)}assert.equal(data22.businessSupported({region:'cn'},'trial'),false);assert.equal(data22.businessSupported({},'travel'),false)});
test('24 trial support is not proof of eligibility',()=>{assert.equal(data22.trialEligibility({region:'intl',trial_eligible:true}),'unknown');assert.equal(data22.trialEligibility({trial_claimed:true}),'already_claimed');assert.equal(data22.trialEligibility({capabilities:{trial:{eligibility:'unknown'}},trial_claimed:true}),'unknown')});
test('24 mixed selection counts every account including skipped ones',()=>{const r=data22.checkinSelection([{auth_index:'a',region:'cn'},{auth_index:'b',region:'intl'},{auth_index:'c',region:'cn'}],new Set(['a','b']));assert.equal(r.execute,1);assert.equal(r.skip,1);assert.equal(r.rows.length,2)});
test('24 run history requires actual typed execution evidence',()=>{assert.equal(data22.validTravelRun({travel_auto:true}),false);assert.equal(data22.validTravelRun({status:'success'}),false);assert.equal(data22.validTravelRun({status:'success',started_at:'2026-10-07T00:00:00Z',attempted:1,succeeded:1,failed:0,skipped:2}),true)});
test('24 skipped business operation is not a failure',()=>assert.equal(outcome({success:false,skipped:true,reason:'global'}),'skipped'));
test('24 confirmed already-claimed response survives API parsing',async()=>{env.fetch=async()=>({ok:true,status:200,text:async()=>JSON.stringify({success:false,already_claimed:true})});const r=await api.request('/trial','POST',{auth_index:'a'});assert.equal(outcome(r),'already')});
test('24 unsupported region is a translatable local error, not auth failure',async()=>{env.fetch=async()=>({ok:false,status:422,text:async()=>JSON.stringify({error:'not supported',code:'unsupported_region'})});await assert.rejects(api.request('/tasks?auth_index=b'),e=>e.code===422&&e.message==='unsupported_region'&&!e.uncertain)});
test('25 channel identifiers are invariant across all four UI languages',()=>{const rows=dictionary.module.exports.translations;for(const key of ['cn','hubChannel_cn','hubShortChannel_cn'])assert.ok(rows[key].every(v=>v==='CN'),key);for(const key of ['global','intl','hubChannel_intl','hubShortChannel_intl','hubChannel_global','hubShortChannel_global'])assert.ok(rows[key].every(v=>v==='Intl'),key)});
test('25 explanatory copy localizes sentences, not channel identifiers',()=>{const rows=dictionary.module.exports.translations;for(const [key,row]of Object.entries(rows))for(const v of row)assert.ok(!/国内|国外|國內|國外|中國大陸|国际|國際/.test(v),key);for(const v of rows.hubSourceShort)for(const term of ['CN','Intl','WB','CB','CPA'])assert.ok(v.includes(term),term);for(const v of rows.paste)for(const term of ['WorkBuddy','CodeBuddy','JSON'])assert.ok(v.includes(term),term)});
