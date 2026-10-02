package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMistralRecoveryUI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for browser controller tests")
	}
	code, err := staticFS.ReadFile("static/mistral-recovery.js")
	if err != nil {
		t.Fatal(err)
	}
	page, err := staticFS.ReadFile("static/menubar.html")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(page), "window.__onwatchBrowserGrantResult =")
	end := strings.Index(string(page)[start:], "if (footerVersion)") + start
	bridge := string(page)[start:end]
	test := `
const assert = require('node:assert/strict');
const timers = new Map(); let timerID = 0;
global.setTimeout = (fn, ms) => { timers.set(++timerID, {fn,ms}); return timerID; };
global.clearTimeout = id => timers.delete(id);
global.window = new EventTarget(); global.document = new EventTarget(); document.hidden = false;
` + string(code) + `
(async () => {
 const root = new EventTarget(); let refreshes = 0; let renders = 0;
 const recovery = new window.MistralRecovery({root,endpoint:'/watch/api/mistral/retry',refresh:async()=>{refreshes++;return {canRetry:true,reason:'browser_access_denied'}},render:()=>{renders++;}});
 const html = recovery.markup({canRetry:true,reason:'import_failed',message:'<script>private</script>'},'reconnect');
 assert(!html.includes('<script>')); assert(html.includes('&lt;script&gt;'));
 let resolve; let requests = 0;
 global.fetch = async (url, options) => { requests++; assert.equal(url,'/watch/api/mistral/retry'); assert.equal(options.method,'POST'); assert.equal(options.headers['X-Requested-With'],'XMLHttpRequest'); return await new Promise(r=>resolve=r); };
 const first = recovery.retry(); await recovery.retry(); assert.equal(requests,1);
 window.dispatchEvent(new Event('pagehide'));
 resolve({status:202}); await first;
 assert.equal(timers.size,0,'closing view must not resurrect polling after POST finishes');
 recovery.dispose();
 const next = new window.MistralRecovery({root,endpoint:'/retry',refresh:async()=>{refreshes++;return {canRetry:true}},render:()=>{}});
 next.markup({canRetry:true,reason:'no_session'},'reconnect');
 global.fetch=async()=>({status:429,headers:{get:()=> '30'}});
 await next.retry(); assert(next.feedback.includes('30')); await next.tick(); assert.equal(refreshes,1);
 next.dispose(); assert.equal(timers.size,0);
 let grants=0;
 const grant = new window.MistralRecovery({root,endpoint:'/retry',grant:()=>{grants++;return true},refresh:async()=>({canRetry:true}),render:()=>{}});
 let grantHTML=grant.markup({reason:'browser_access_denied',canRetry:true},'reconnect');
 assert(grantHTML.includes('Grant Browser Access'));
 assert(grantHTML.indexOf('Grant Browser Access') < grantHTML.indexOf('Retry connection'));
 grant.requestGrant(); grant.requestGrant(); assert.equal(grants,1);
 assert(grant.markup({reason:'browser_access_denied',canRetry:true}).includes('disabled'));
 grant.grantResult('cancelled'); assert(!grant.granting); assert(grant.feedback.includes('cancelled'));
 grant.granting = true; grant.grantResult('idle'); assert(!grant.granting,'idle replay must release a page that missed the completion');
 grant.grantResult('idle'); assert(grant.feedback.includes('cancelled'),'idle replay must not clear feedback');
 assert(!grant.markup({reason:'no_session',canRetry:true}).includes('Grant Browser Access'));
 grant.grantResult('retrying'); assert(!grant.granting);
 grant.hide(); grant.grantResult('retrying'); assert.equal(timers.size,0,'native completion must not resume a closed view');
 grant.grantResult('unavailable'); assert(grant.feedback.includes('still unavailable'));
 grant.grantResult('retry_failed'); assert(grant.feedback.includes('Retry connection'));
 assert.equal(grant.markup({canRetry:true},'ok'),'','healthy state must discard stale grant feedback');
 grant.dispose(); assert.equal(timers.size,0);
 const fallback = new window.MistralRecovery({root,refresh:async()=>{},render:()=>{}});
 assert(!fallback.markup({reason:'browser_access_denied'}).includes('Grant Browser Access'));
 fallback.dispose();
 assert(renders>=2);
 let loads=0, refreshDataCalls=0;
 const mistralRecovery={grantResult:()=>{},suspended:true};
 const loadSnapshot=()=>{loads++};
 const refreshData=()=>{refreshDataCalls++};
 const sendNativeAction=()=>window.__onwatchBrowserGrantResult('cancelled',false);
 ` + bridge + `
 window.__onwatchMenubarRefresh();
 assert.equal(loads,0,'status replay must not issue a duplicate snapshot fetch');
 assert.equal(refreshDataCalls,1);
 window.__onwatchBrowserGrantResult('retrying',true);
 assert.equal(loads,1,'new completion must refresh the snapshot');
})().catch(error=>{console.error(error);process.exitCode=1;});
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(test)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// The quick view must keep working for every provider if the recovery
// controller script fails to load.
func TestMenubarSurvivesMissingMistralRecovery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for browser controller tests")
	}
	page, err := staticFS.ReadFile("static/menubar.html")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(page), "const mistralRecovery =")
	end := strings.Index(string(page), "window.__onwatchBrowserGrantResult =")
	if start < 0 || end < start {
		t.Fatal("recovery bootstrap not found")
	}
	test := `
const assert = require('node:assert/strict');
global.window = {};
const body = {}; const API_BASE = ''; const sendNativeAction = () => false;
const loadSnapshot = async () => {}; const render = () => {}; const state = {};
` + string(page)[start:end] + `
assert.equal(mistralRecovery.markup({reason:'no_session',canRetry:true}, 'reconnect'), '');
mistralRecovery.grantResult('cancelled'); mistralRecovery.hide();
mistralRecovery.suspended = false;
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(test)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
