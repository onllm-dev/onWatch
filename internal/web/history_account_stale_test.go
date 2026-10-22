package web

import (
	"testing"
)

// TestHistoryAccountSwitchFailureMarksStale covers Codex account switching: a
// successful render for account A, then a failed fetch for the newly selected
// account B, must mark A's retained chart stale. The currentness check has to
// use the account the failed request was made for, not the account the chart
// still shows; otherwise the guard rejects the failure as stale and the chart
// stays unmarked under B.
func TestHistoryAccountSwitchFailureMarksStale(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');
const registry={};
function makeEl(id){ const el={id,classList:{_set:new Set(),add(c){this._set.add(c);},remove(c){this._set.delete(c);},contains(c){return this._set.has(c);}},attrs:{},setAttribute(k,v){this.attrs[k]=v;},getAttribute(k){return this.attrs[k]||null;},removeAttribute(k){delete this.attrs[k];},style:{},children:[],innerHTML:'',textContent:'',appendChild(child){ if(child&&child.id) registry[child.id]=child; }}; if(id) registry[id]=el; return el; }
const host=makeEl('chart-controls');
const codexGrid=makeEl('quota-grid-codex');
const usageChart=makeEl('usage-chart');
const document={querySelector:sel=>sel==='.chart-section .chart-controls'?host:null,querySelectorAll:()=>[],getElementById:id=>id==='quota-grid-codex'?codexGrid:(id==='usage-chart'?usageChart:(registry[id]||null)),createElement:()=>makeEl(null),addEventListener:()=>{},documentElement:{getAttribute:()=>null}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
function Chart(){ this.options={scales:{x:{},y:{}},plugins:{legend:{labels:{}},tooltip:{}}}; this.data={labels:[],datasets:[]}; this.destroy=()=>{State.chart=null;}; this.update=()=>{}; this.getDatasetMeta=()=>({}); }
Chart.register=()=>{};
globalThis.Chart=Chart;
console.warn=()=>{};
` + string(source) + `
(async()=>{
 const T=1_700_000_000_000;
 let NOW=T;
 Date.now=()=>NOW;

 State.codexAccount=1;
 State.codexProfiles=[{id:1,name:'Work'},{id:2,name:'Personal'}];

 // Account A renders successfully.
 const row={capturedAt:new Date(T).toISOString(),five_hour:10};
 fetch=async()=>({ok:true,status:200,json:async()=>[row]});
 await fetchHistory('24h');
 const staleMark=chartStaleMark();
 assert(staleMark,'chart must expose a stale mark element');
 assert.equal(State.chart.renderKey.account,1,'rendered chart belongs to account A');
 assert.equal(staleMark.classList.contains('stale'),false);

 // The user switches to account B and B's history fetch fails. Sequence and
 // selection are both current; only the rendered account differs.
 NOW=T+12*60*1000;
 State.codexAccount=2;
 fetch=async()=>({ok:false,status:500,json:async()=>({error:'boom'})});
 await fetchHistory('24h');
 assert.equal(State.chart.data.datasets.length>0,true,'failed switch must keep account A lines');
 assert.equal(staleMark.classList.contains('stale'),true,'failed switch must mark the retained chart stale');
 assert.match(staleMark.getAttribute('title'),/^Showing Work data - Personal failed to load \(last updated 12m ago\)$/,staleMark.getAttribute('title'));

 // An obsolete failure (a newer request is already in flight) must not touch
 // the marker: clear it, then fail a request with a superseded sequence.
 staleMark.classList.remove('stale'); staleMark.removeAttribute('title');
 State.historyRequestSeq=State.historyRequestSeq+1;
 markHistoryFailureStale('codex','24h',State.historyRequestSeq-1,2);
 assert.equal(staleMark.classList.contains('stale'),false,'obsolete failure must not mark the chart');
 assert.equal(staleMark.getAttribute('title'),null,'obsolete failure must not set a tooltip');

 // A successful fetch for B clears the mark.
 State.historyRequestSeq=State.historyRequestSeq+1;
 fetch=async()=>({ok:true,status:200,json:async()=>[row]});
 await fetchHistory('24h');
 assert.equal(staleMark.classList.contains('stale'),false,'successful account load must clear the stale mark');
})().catch(err=>{console.error(err);process.exitCode=1});
`
	runNodeScript(t, script)
}
