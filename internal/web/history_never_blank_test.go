package web

import (
	"testing"
)

// TestHistoryFetchNeverBlanksChart covers the shared history path used by every
// provider: HTTP errors, error bodies and network failures must leave the last
// chart in place, while a successful empty array still clears it.
func TestHistoryFetchNeverBlanksChart(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');
let provider='synthetic';
const document={querySelector:()=>null,querySelectorAll:()=>[],getElementById:id=>id==='quota-grid-'+provider?{}:null,addEventListener:()=>{},documentElement:{}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
const warnings=[]; const realWarn=console.warn; console.warn=(...args)=>warnings.push(args);
` + string(source) + `
(async()=>{
 const row={capturedAt:new Date().toISOString(),subscriptionPercent:12,searchPercent:3,toolCallsPercent:1};
 const existing={data:{datasets:[{label:'Subscription',data:[{x:new Date(),y:12}]}]},options:{scales:{x:{},y:{}}},getDatasetMeta:()=>({}),update:()=>{}};
 const fails=[
  {name:'error body',fetch:async()=>({ok:true,status:200,json:async()=>({error:'boom'})})},
  {name:'http 500',fetch:async()=>({ok:false,status:500,json:async()=>({error:'boom'})})},
  {name:'network',fetch:async()=>{throw new TypeError('Failed to fetch')}},
  {name:'non-array',fetch:async()=>({ok:true,status:200,json:async()=>({rows:[row]})})},
 ];
 for(const scenario of fails) {
  State.chart=existing; State.chart.data.datasets=existing.data.datasets;
  const before=State.chart.data.datasets;
  fetch=scenario.fetch;
  await fetchHistory('7d');
  assert.equal(State.chart.data.datasets,before,scenario.name+' replaced chart data');
  assert.equal(State.chart.data.datasets[0].data[0].y,12,scenario.name+' altered chart data');
  const warning=warnings.at(-1);
  assert(warning,'no warning for '+scenario.name);
  assert.equal(warning[1].provider,'synthetic');
  assert.equal(warning[1].range,'7d');
  assert(warning[1].reason,scenario.name+' warning missing reason');
 }
 // A genuinely empty but valid response is allowed to clear the chart.
 State.chart=existing;
 fetch=async()=>({ok:true,status:200,json:async()=>[]});
 await fetchHistory('7d');
 assert.equal(State.chart.data.datasets.length,3);
 assert(State.chart.data.datasets.every(ds=>ds.data.length===0),'successful empty response must clear chart data');
 // Both-view error bodies are rejected too, before all-provider rendering.
 provider='both';
 State.allProvidersHistory={keep:true};
 fetch=async()=>({ok:true,status:200,json:async()=>({error:'boom'})});
 await fetchHistory('7d');
 assert.deepEqual(State.allProvidersHistory,{keep:true});
 console.warn=realWarn;
})().catch(err=>{console.error(err);process.exitCode=1});
`
	runNodeScript(t, script)
}
