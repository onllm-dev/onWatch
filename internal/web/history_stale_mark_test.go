package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHistoryFailureMarksMismatchedChartStale covers the shared history path:
// a failed fetch may keep the lines already drawn, but only when they belong to
// the provider, account and range now selected. A mismatch keeps the lines and
// marks the chart stale; the next success clears the mark.
func TestHistoryFailureMarksMismatchedChartStale(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');
const registry={};
function makeEl(id){ const el={id,classList:{_set:new Set(),add(c){this._set.add(c);},remove(c){this._set.delete(c);},contains(c){return this._set.has(c);}},attrs:{},title:'',setAttribute(k,v){this.attrs[k]=v;},getAttribute(k){return this.attrs[k]||null;},removeAttribute(k){delete this.attrs[k];},style:{},children:[],innerHTML:'',textContent:'',appendChild(child){ if(child&&child.id) registry[child.id]=child; }}; if(id) registry[id]=el; return el; }
const host=makeEl('chart-controls');
const document={querySelector:sel=>sel==='.chart-section .chart-controls'?host:null,querySelectorAll:()=>[],getElementById:id=>registry[id]||null,createElement:()=>makeEl(null),addEventListener:()=>{},documentElement:{getAttribute:()=>null}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
console.warn=()=>{};
` + string(source) + `
(async()=>{
 const row={capturedAt:new Date().toISOString(),subscriptionPercent:12,searchPercent:3,toolCallsPercent:1};
 const existing={data:{datasets:[{label:'Subscription',data:[{x:new Date(),y:12}]}]},options:{scales:{x:{},y:{}}},getDatasetMeta:()=>({}),update:()=>{}};
 State.chart=existing;
 fetch=async()=>({ok:true,status:200,json:async()=>[row]});
 await fetchHistory('24h');
 const staleMark=chartStaleMark();
 assert(staleMark,'chart must expose a stale mark element');
 assert.equal(staleMark.classList.contains('stale'),false,'a success must clear the stale mark');

 // Same provider, different range: keep the drawn lines but mark them stale.
 fetch=async()=>({ok:false,status:500,json:async()=>({error:'boom'})});
 await fetchHistory('7d');
 assert.equal(State.chart.data.datasets[0].data[0].y,12,'failed switch must keep the chart lines');
 assert.equal(staleMark.classList.contains('stale'),true,'mismatched failure must mark the chart stale');
 const staleTitle=staleMark.getAttribute('title');
 assert(staleTitle.startsWith('Showing 24h data - 7d failed to load'),'mismatch context missing: '+staleTitle);
 assert(/last updated (just now|\d+[mhd] ago)/.test(staleTitle),'stale tooltip must give the data age: '+staleTitle);

 // Next success clears the mark.
 fetch=async()=>({ok:true,status:200,json:async()=>[row]});
 await fetchHistory('7d');
 assert.equal(staleMark.classList.contains('stale'),false,'successful load must clear the stale mark');
 process.exit(0);
})().catch(err=>{console.error(err);process.exitCode=1});
`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
