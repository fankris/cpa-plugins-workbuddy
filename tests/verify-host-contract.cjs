// Static evidence check against the exact SDK source, NOT a live CPA test.
const fs = require('node:fs');
const path = require('node:path');
const cp = require('node:child_process');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const moduleInfo = JSON.parse(cp.execFileSync('go', ['list', '-m', '-json', 'github.com/router-for-me/CLIProxyAPI/v8'], {encoding:'utf8'}));
assert.equal(moduleInfo.Version, 'v8.0.15');
const relative = 'sdk/cliproxy/auth/conductor_selection.go';
const text = fs.readFileSync(path.join(moduleInfo.Dir, relative), 'utf8');
const start = text.indexOf('func (m *Manager) pickViaPluginScheduler(');
const end = text.indexOf('\nfunc ', start + 1);
const pick = text.slice(start, end);
assert.match(pick, /if !handled \|\| !resp.Handled \{\s*return nil, false, nil\s*\}/);
assert.match(pick, /builtinSchedulerStrategy\(resp.DelegateBuiltin\)/);
assert.match(text, /if !handled \{\s*selectorCtx := selectorContextForAvailableAuths\(ctx, selector, model\)\s*selected, errPick = selector.Pick\(selectorCtx, provider,/);
assert.match(text, /if !handled \{\s*selectorCtx := selectorContextForAvailableAuths\(ctx, selector, model\)\s*selected, errPick = selector.Pick\(selectorCtx, "mixed",/);
console.log(JSON.stringify({
 result:'PASS', type:'static pinned-source contract check', version:moduleInfo.Version,
 file:relative, sha256:crypto.createHash('sha256').update(text).digest('hex'),
 checks:['Handled=false returns unhandled','explicit delegation names a strategy','single route calls configured selector','mixed route calls configured selector'],
 realCPALoadingTested:false, realRoutingTested:false
}, null, 2));
