package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWindowDashboardRendering(t *testing.T) {
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
let provider='opencode';
const elements=new Map();
const document={querySelector:()=>null,querySelectorAll:()=>[],getElementById:id=>id==='quota-grid-'+provider?{}:elements.get(id),addEventListener:()=>{},documentElement:{}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
` + string(source) + `
(async()=>{
 for(provider of ['opencode','commandcode']) {
  const at=new Date().toISOString();
  const quotas=[{name:'five_hour',value:0.93,limit:14,percent:6.64,format:'credits',resetsAt:at},{name:'weekly',value:19.01,limit:35,percent:54.31,format:'credits',resetsAt:at},{name:'monthly',value:18.99,limit:69.98,percent:27.14,format:'currency',resetsAt:at}];
  if(provider==='opencode') quotas.forEach(q=>q.format='percent');
  let fail=false;
  fetch=async()=>({ok:!fail,json:async()=>[{capturedAt:at,quotas:quotas.map(q=>({name:q.name,utilization:q.percent}))}]});
  let updates=0;
  State.chart={data:{datasets:[]},options:{scales:{x:{},y:{}}},getDatasetMeta:()=>({}),update:()=>updates++};
  for(const range of ['7d','30d']) {await fetchHistory(range);assert.equal(State.chart.data.datasets.length,3);assert.equal(updates,range==='7d'?1:2);assert.equal(State.chart.data.datasets[2].data[0].y,27.14);}
  const previous=State.chart.data.datasets;fail=true;await fetchHistory('7d');assert.equal(State.chart.data.datasets,previous,'fetch failure must keep chart');
  for(const id of ['cycles-thead','cycles-tbody','overview-thead','overview-tbody']) elements.set(id,{innerHTML:'',querySelectorAll:()=>[]});
  State.cyclesQuotaNames=quotas.map(q=>q.name);State.isLoggingHistory=true;State.cyclesBucket=1;State.cyclesPageSize=10;State.cyclesPage=1;
  State.allCyclesData=[{cycleId:42,cycleStart:at,cycleEnd:at,crossQuotas:quotas}];
  renderCyclesTable();
  const sample=elements.get('cycles-tbody').innerHTML;
  assert.equal((sample.match(/<td/g)||[]).length,5);assert(!sample.includes('--'));assert(sample.includes('Resets'));
  assert(sample.includes('27.1%'));
  if(provider==='commandcode'){assert(sample.includes('0.93 / 14'));assert(sample.includes('$18.99 / $69.98'));}
  State.overviewQuotaNames=quotas.map(q=>q.name);State.overviewGroupBy='weekly';State.overviewPage=1;State.overviewPageSize=10;
  State.allOverviewData=[{cycleId:42,cycleStart:at,cycleEnd:null,resetsAt:at,peakValue:61.25,crossQuotas:quotas}];
  renderOverviewTable();
  const header=elements.get('overview-thead').innerHTML,body=elements.get('overview-tbody').innerHTML;
  assert(!header.includes('Total Delta'));assert(!header.includes('Duration'));assert(header.includes('Reset'));assert(header.includes('Peak'));assert(header.includes('at peak'));
  assert.equal((body.match(/<td/g)||[]).length,7);assert(body.includes('61.3%'));assert(!body.includes('(+0'));
 }
 assert.equal(windowQuotaSampleHTML({value:0,limit:14,percent:0,format:'credits'}).startsWith('0 / 14'),true);
 assert.equal(windowQuotaSampleHTML({value:50.99,limit:0,percent:0,format:'currency'}),'$50.99');
})().catch(err=>{console.error(err);process.exitCode=1});
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
