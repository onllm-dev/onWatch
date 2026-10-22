package web

import (
	"testing"
)

// TestHistoryBothMergesFailedProviders covers the All Providers mini-charts:
// when the backend reports that one provider's read failed, its previously
// drawn rows must be kept and marked stale, while successful empty reads still
// clear a chart. The next successful read must drop the stale mark.
func TestHistoryBothMergesFailedProviders(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');
const listeners={};
const makeEl=(id)=>({id,children:[],innerHTML:'',textContent:'',dataset:{},attrs:{},classList:{_s:new Set(),add(c){this._s.add(c);},remove(c){this._s.delete(c);},contains(c){return this._s.has(c);},toggle(){}},setAttribute(k,v){this.attrs[k]=v;},getAttribute(k){return this.attrs[k]||null;},removeAttribute(k){delete this.attrs[k];},addEventListener(){},querySelector(){return null;},querySelectorAll(){return [];}});
const container=makeEl('all-providers-container');
const document={getElementById:id=>id==='all-providers-container'?container:null,querySelector:()=>null,querySelectorAll:()=>[],createElement:()=>makeEl(null),addEventListener:()=>{},documentElement:{getAttribute:()=>null}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}}),OnWatchPeak:null};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
function Chart(){ this.options={scales:{x:{},y:{}}}; this.data={datasets:[]}; this.destroy=()=>{}; this.update=()=>{}; }
console.warn=()=>{};
` + string(source) + `
(async()=>{
 const row=(minutesAgo)=>({capturedAt:new Date(Date.now()-minutesAgo*60000).toISOString(),subscriptionPercent:10,searchPercent:2,toolCallsPercent:1});
 State.allProvidersCurrent={ synthetic:{quotas:[]}, zai:{quotas:[]} };
 State.allProvidersInsights={};
 State.allProvidersHistory=null;

 // First load: both providers succeed.
 fetch=async()=>({ok:true,status:200,json:async()=>({synthetic:[row(10)],zai:[row(9)]})});
 document.getElementById=id=>id==='all-providers-container'?container:(id==='both-view'?{}:null);
 await fetchHistory('24h');
 assert.deepEqual(State.allProvidersHistory.errors,{},'successful response must carry no error entries');
 const synRows=State.allProvidersHistory.synthetic;
 const zaiRows=State.allProvidersHistory.zai;
 assert.equal(synRows.length,1);
 assert.equal(zaiRows.length,1);

 // The zai read fails; the backend keeps synthetic and lists zai in errors.
 fetch=async()=>({ok:true,status:200,json:async()=>({synthetic:[row(1)],errors:{zai:'history read failed'}})});
 await fetchHistory('24h');
 assert.equal(State.allProvidersHistory.synthetic[0],State.allProvidersHistory.synthetic[0]);
 assert.equal(State.allProvidersHistory.synthetic.length,1,'synthetic must be replaced by the new successful read');
 assert.equal(State.allProvidersHistory.zai,zaiRows,'failed provider must keep its previous rows');
 assert.match(State.allProvidersHistory.zai[0].capturedAt,/./);

 // The rendered card carries an amber stale dot with the data age.
 const html=container.innerHTML;
 const stale=/data-provider="zai"[\s\S]*?provider-chart-stale-dot[\s\S]*?title="([^"]*)"/.exec(html);
 assert(stale,'zai card must show a stale marker');
 assert(/Last updated (\d+[mhd] ago|just now) - refresh failed/.test(stale[1]),'stale tooltip: '+stale[1]);
 const syntheticCard=/data-provider="synthetic"[\s\S]*?<\/section>/.exec(html);
 assert(syntheticCard,'synthetic card missing');
 assert(!syntheticCard[0].includes('provider-chart-stale-dot'),'healthy provider must not be marked stale');

 // A later success clears the mark.
 fetch=async()=>({ok:true,status:200,json:async()=>({synthetic:[row(1)],zai:[row(1)]})});
 await fetchHistory('24h');
 assert.deepEqual(State.allProvidersHistory.errors,{},'recovered response must clear the error map');
 assert.equal(State.allProvidersHistory.zai.length,1);
 assert(!container.innerHTML.includes('provider-chart-stale-dot'),'successful load must clear the stale mark');

 // A successful empty result may still clear a chart.
 fetch=async()=>({ok:true,status:200,json:async()=>({synthetic:[],zai:[row(1)]})});
 await fetchHistory('24h');
 assert.equal(State.allProvidersHistory.synthetic.length,0,'successful empty read must clear the chart');
})().catch(err=>{console.error(err);process.exitCode=1});
`
	runNodeScript(t, script)
}

// TestHistoryRefreshFailureKeepsStaleMark covers a chart whose refresh keeps
// failing with the same selection: the retained lines must stay marked stale,
// with the age of the data in the tooltip, and an obsolete failure must not
// touch the marker.
func TestHistoryRefreshFailureKeepsStaleMark(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');
const registry={};
function makeEl(id){ const el={id,classList:{_set:new Set(),add(c){this._set.add(c);},remove(c){this._set.delete(c);},contains(c){return this._set.has(c);}},attrs:{},setAttribute(k,v){this.attrs[k]=v;},getAttribute(k){return this.attrs[k]||null;},removeAttribute(k){delete this.attrs[k];},style:{},children:[],innerHTML:'',textContent:'',appendChild(child){ if(child&&child.id) registry[child.id]=child; }}; if(id) registry[id]=el; return el; }
const host=makeEl('chart-controls');
const grid={};
const document={querySelector:sel=>sel==='.chart-section .chart-controls'?host:null,querySelectorAll:()=>[],getElementById:id=>id==='quota-grid-synthetic'?grid:(registry[id]||null),createElement:()=>makeEl(null),addEventListener:()=>{},documentElement:{getAttribute:()=>null}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
console.warn=()=>{};
` + string(source) + `
(async()=>{
 // A controllable wall clock: the fetch succeeds at T, then 12 minutes pass
 // before the refresh fails, so the retained data is 12 minutes old.
 const T=1_700_000_000_000;
 let NOW=T;
 Date.now=()=>NOW;
 const row={capturedAt:new Date(T).toISOString(),subscriptionPercent:12,searchPercent:3,toolCallsPercent:1};
 const existing={data:{datasets:[{label:'Subscription',data:[{x:new Date(),y:12}]}]},options:{scales:{x:{},y:{}}},getDatasetMeta:()=>({}),update:()=>{}};
 State.chart=existing;
 fetch=async()=>({ok:true,status:200,json:async()=>[row]});
 await fetchHistory('24h');
 const staleMark=chartStaleMark();
 assert(staleMark,'chart must expose a stale mark element');
 assert.equal(staleMark.classList.contains('stale'),false,'a success must clear the stale mark');

 NOW=T+12*60*1000;
 // Same selection fails: keep the lines, mark them stale with the data age.
 fetch=async()=>({ok:false,status:500,json:async()=>({error:'boom'})});
 await fetchHistory('24h');
 assert.equal(State.chart.data.datasets[0].data[0].y,12,'failure must keep the drawn lines');
 assert.equal(staleMark.classList.contains('stale'),true,'same-selection failure must mark the chart stale');
 const firstTitle=staleMark.getAttribute('title');
 assert.match(firstTitle,/^Last updated 12m ago - refresh failed$/,firstTitle);

 // A repeated failure keeps the mark, and the age keeps growing.
 fetch=async()=>({ok:false,status:503,json:async()=>({error:'boom'})});
 await fetchHistory('24h');
 assert.equal(staleMark.classList.contains('stale'),true,'repeated failure must keep the stale mark');
 assert.match(staleMark.getAttribute('title'),/^Last updated 12m ago - refresh failed$/);

 // An obsolete failure must not touch the marker: mark it clear, then fire a
 // request that a newer one has already superseded.
 staleMark.classList.remove('stale'); staleMark.removeAttribute('title');
 const staleSeq=State.historyRequestSeq;
 State.historyRequestSeq=staleSeq+1;
 markHistoryFailureStale('synthetic','24h',staleSeq);
 assert.equal(staleMark.classList.contains('stale'),false,'obsolete failure must not mark the chart');
 assert.equal(staleMark.getAttribute('title'),null,'obsolete failure must not set a tooltip');
 State.historyRequestSeq=staleSeq;

 // An in-flight newer request also disqualifies the failure.
 staleMark.classList.remove('stale'); staleMark.removeAttribute('title');
 State.historyRequestSeq=staleSeq+1;
 markHistoryFailureStale('synthetic','24h',staleSeq);
 assert.equal(staleMark.classList.contains('stale'),false,'superseded failure must not mark the chart');
 State.historyRequestSeq=staleSeq;

 // Mismatched range keeps the mismatch context plus the age.
 fetch=async()=>({ok:false,status:500,json:async()=>({error:'boom'})});
 await fetchHistory('7d');
 assert.match(staleMark.getAttribute('title'),/^Showing 24h data - 7d failed to load \(last updated 12m ago\)$/,staleMark.getAttribute('title'));

 // A success on the current selection clears it.
 fetch=async()=>({ok:true,status:200,json:async()=>[row]});
 await fetchHistory('7d');
 assert.equal(staleMark.classList.contains('stale'),false,'successful load must clear the stale mark');
})().catch(err=>{console.error(err);process.exitCode=1});
`
	runNodeScript(t, script)
}
