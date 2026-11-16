package web

import (
	"testing"
)

// TestZenmuxUsagePanelKeepsLastResults drives the real setupZenmuxUsage code
// with a fake DOM: a failed refresh must leave the previously rendered rows in
// place and show a stale marker instead of blanking the panel.
func TestZenmuxUsagePanelKeepsLastResults(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert = require('node:assert/strict');
const document = {getElementById:()=>null,querySelector:()=>null,querySelectorAll:()=>[],addEventListener:()=>{},documentElement:{}};
const window = {addEventListener:()=>{},location:{pathname:'/dashboard'},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage = {getItem:()=>null,setItem:()=>{}}; const sessionStorage = localStorage; const navigator = {};
` + string(source) + `
function el(id) {
  return {
    id, value:'', hidden:false, textContent:'', innerHTML:'', attrs:{}, listeners:{},
    addEventListener(type, fn) { this.listeners[type] = fn; },
    setAttribute(key, value) { this.attrs[key] = value; },
    removeAttribute(key) { delete this.attrs[key]; },
    requestSubmit() {},
    classList: { set:new Set(), add(c){ this.set.add(c); }, remove(c){ this.set.delete(c); }, contains(c){ return this.set.has(c); } },
  };
}
const ids = ['zenmux-usage-form','zenmux-usage-dimension','zenmux-usage-month','zenmux-usage-day','zenmux-usage-hour','zenmux-usage-billing','zenmux-usage-status','zenmux-usage-results','zenmux-endpoint-results','zenmux-usage-stale-dot','zenmux-usage-month-label','zenmux-usage-day-label','zenmux-usage-hour-label'];
const els = {}; ids.forEach(id => { els[id] = el(id); });
els['zenmux-usage-dimension'].value = 'BIZ_MTH';
els['zenmux-usage-month'].value = '2026-09';
document.getElementById = id => els[id] || null;
setupZenmuxUsage();
const panel = {
  submit: els['zenmux-usage-form'].listeners.submit,
  results: els['zenmux-usage-results'],
  endpoints: els['zenmux-endpoint-results'],
  status: els['zenmux-usage-status'],
  stale: els['zenmux-usage-stale-dot'],
};
assert(panel.submit, 'submit handler was not registered');
const usage = {data:{tokensByModel:[{bizTime:'20260901',modelSlug:'sample/model',requestCounts:'2',tokens:'9'}]}};
let mode = 'ok';
fetch = async () => {
  if (mode === 'fail') throw new TypeError('Failed to fetch');
  return {ok:true, status:200, json:async()=>usage};
};
(async () => {
  await panel.submit({preventDefault(){}});
  assert(panel.results.innerHTML.includes('sample/model'), 'known data did not render');
  assert.equal(panel.status.textContent, '', 'success left an error line');
  assert(!panel.stale.classList.contains('stale'), 'success left a stale marker');

  const rendered = panel.results.innerHTML;
  mode = 'fail';
  await panel.submit({preventDefault(){}});
  assert.equal(panel.results.innerHTML, rendered, 'failed refresh blanked the previous results');
  assert(panel.status.textContent.includes('unavailable'), 'failure did not report the error');
  assert(panel.stale.classList.contains('stale'), 'failure did not mark the held rows stale');
  assert(panel.stale.attrs.title, 'stale marker has no tooltip');

  mode = 'ok';
  await panel.submit({preventDefault(){}});
  assert(!panel.stale.classList.contains('stale'), 'a later success did not clear the stale marker');
  assert.equal(panel.status.textContent, '', 'a later success left the error line');
})().catch(err => { console.error(err); process.exitCode = 1; });
`
	runNodeScript(t, script)
}
