const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require.resolve('./fixtures/cpamc-patch-config.cjs'), 'utf8');
const clone = x => JSON.parse(JSON.stringify(x));
function fixture(initial, options={}) {
  let stored=clone(initial); const writes=[];
  const sandbox={module:{exports:{}},
    guardConfigConnection:()=>()=>{if(options.connectionChanged)throw Error('connection changed')},
    pluginsApi:{getConfig:async()=>{if(options.readFailure)throw Error('read failed');return clone(stored)}},
    apiClient:{put:async(path,value)=>{if(options.writeFailure)throw Error('write failed');writes.push({path,value:clone(value)});stored=clone(value);return stored}}
  };
  vm.runInNewContext(source,sandbox,{filename:'cpamc-patch-config.cjs'});
  return {patch:sandbox.module.exports, writes, state:()=>clone(stored)};
}
test('official touched-field save keeps unknown fields and modifies only one plugin object',async()=>{
 const f=fixture({enabled:true,priority:3,future:{secret:'fixture-only'},scheduler_mode:'builtin'});
 await f.patch('workbuddy',{scheduler_mode:'host'});
 assert.deepEqual(f.state(),{enabled:true,priority:3,future:{secret:'fixture-only'},scheduler_mode:'host'});
 assert.equal(f.writes[0].path,'/config/plugins/configs/workbuddy');
});
test('null clears one field; empty patch retains everything',async()=>{
 const f=fixture({login_region:'intl',future:[1,2]});
 await f.patch('workbuddy',{login_region:null});
 await f.patch('workbuddy',{});
 assert.deepEqual(f.state(),{future:[1,2]});
});
test('edited objects replace, rather than recursively merging stale keys',async()=>{
 const f=fixture({future:{old:1,keep:2},unknown:7});
 await f.patch('workbuddy',{future:{new:3}});
 assert.deepEqual(f.state(),{future:{new:3},unknown:7});
});
test('read failure never writes',async()=>{
 const f=fixture({scheduler_mode:'builtin'},{readFailure:true});
 await assert.rejects(f.patch('workbuddy',{scheduler_mode:'host'}),/read failed/);
 assert.equal(f.writes.length,0);
});
test('host connection change aborts before writing',async()=>{
 const f=fixture({scheduler_mode:'builtin'},{connectionChanged:true});
 await assert.rejects(f.patch('workbuddy',{scheduler_mode:'host'}),/connection changed/);
 assert.equal(f.writes.length,0);
});
test('write failure propagates; no optimistic success assumed',async()=>{
 const f=fixture({scheduler_mode:'builtin'},{writeFailure:true});
 await assert.rejects(f.patch('workbuddy',{scheduler_mode:'host'}),/write failed/);
 assert.equal(f.state().scheduler_mode,'builtin');
});
test('concurrent read/merge/PUT can lose edits: single-editor limit is real',async()=>{
 const f=fixture({login_region:'cn',scheduler_mode:'builtin'});
 await Promise.all([f.patch('workbuddy',{login_region:'intl'}),f.patch('workbuddy',{scheduler_mode:'host'})]);
 assert.equal(f.writes.length,2);
 assert.deepEqual(f.state(),{login_region:'cn',scheduler_mode:'host'});
});
