package web

import (
	"testing"
)

// TestMultiAccountChartNeverBlanks covers the multi-account (All accounts)
// overview chart: when any per-account history request fails, the chart that is
// already on screen must be kept and the failure warned, not redrawn without
// that account's lines.
func TestMultiAccountChartNeverBlanks(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');
const registry={};
function makeEl(id){ const el={id,classList:{_set:new Set(),add(c){this._set.add(c);},remove(c){this._set.delete(c);},contains(c){return this._set.has(c);}},setAttribute(){},removeAttribute(){},style:{},children:[],innerHTML:'',appendChild(child){ if(child&&child.id) registry[child.id]=child; }}; if(id) registry[id]=el; return el; }
const host=makeEl('chart-controls');
const document={querySelector:sel=>sel==='.chart-section .chart-controls'?host:null,querySelectorAll:()=>[],getElementById:id=>registry[id]||null,createElement:()=>makeEl(null),addEventListener:()=>{},documentElement:{getAttribute:()=>null}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
const warnings=[]; console.warn=(...args)=>warnings.push(args);
makeEl('quota-grid-codex');
makeEl('usage-chart');
function Chart(){ this.options={scales:{x:{},y:{}}}; this.data={datasets:[]}; }
Chart.register=()=>{};
` + string(source) + `
(async()=>{
 State.codexAccount='all';
 const quota=[{name:'five_hour',utilization:20,status:'healthy'}];
 State.accountsOverview={provider:'codex',accounts:[
  {accountId:1,accountName:'One',quotas:quota},
  {accountId:2,accountName:'Two',quotas:quota},
 ]};
 let destroyed=false;
 const existing={data:{datasets:[{label:'One',data:[{x:new Date(),y:20}]}]},options:{scales:{x:{},y:{}}},getDatasetMeta:()=>({}),update:()=>{},destroy:()=>{destroyed=true}};
 const okRows=[{capturedAt:new Date().toISOString(),five_hour:20}];
 const scenarios={
  'http error':(url)=>url.includes('account=2')?{ok:false,status:500,json:async()=>({error:'boom'})}:{ok:true,status:200,json:async()=>okRows},
  'error body':(url)=>url.includes('account=2')?{ok:true,status:200,json:async()=>({error:'boom'})}:{ok:true,status:200,json:async()=>okRows},
  'non-array':(url)=>url.includes('account=2')?{ok:true,status:200,json:async()=>({rows:okRows})}:{ok:true,status:200,json:async()=>okRows},
  'network':(url)=>{ if(url.includes('account=2')) throw new TypeError('Failed to fetch'); return {ok:true,status:200,json:async()=>okRows}; },
 };
 for(const [name,handler] of Object.entries(scenarios)) {
  destroyed=false;
  State.chart=existing;
  const before=State.chart.data.datasets;
  fetch=async(url)=>handler(String(url));
  warnings.length=0;
  await fetchHistory('7d');
  assert.equal(destroyed,false,name+' destroyed the chart');
  assert.equal(State.chart.data.datasets,before,name+' replaced chart datasets');
  assert.equal(State.chart.data.datasets[0].data[0].y,20,name+' altered chart data');
  const warning=warnings.at(-1);
  assert(warning,name+' did not warn');
  assert.equal(warning[1].provider,'codex');
  assert.equal(warning[1].range,'7d');
  assert(warning[1].reason,name+' warning missing reason');
 }
 // Every account succeeding still redraws.
 destroyed=false;
 fetch=async()=>({ok:true,status:200,json:async()=>okRows});
 await fetchHistory('7d');
 assert.equal(destroyed,true,'successful fetch must replace the chart');
})().catch(err=>{console.error(err);process.exitCode=1});
`
	runNodeScript(t, script)
}
