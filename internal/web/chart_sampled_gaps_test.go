package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Long ranges arrive sampled in SQL (about 200 evenly spread points), so their
// normal spacing exceeds the poll-based gap threshold. The chart must draw
// them as a continuous line, still flag a real outage as a gap, plot no
// metadata fields, and label per-model Anthropic weekly buckets by name.
func TestChartSampledRangesAreContinuous(t *testing.T) {
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
const document={querySelector:()=>null,querySelectorAll:()=>[],getElementById:()=>null,addEventListener:()=>{},documentElement:{}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
` + string(source) + `
const start=Date.UTC(2026,9,15);
const step=7*24*3600*1000/199; // 7 days sampled to 200 points: ~50 minutes apart
const sampled=Array.from({length:200},(_,i)=>({x:new Date(start+i*step),y:i%100}));
for (const range of ['7d','30d']) {
  const r=processDataWithGaps(sampled,range);
  // The last index has no following segment; only real segments matter.
  const segmentGaps=[...r.gapSegments].filter(i=>i<sampled.length-1);
  assert.equal(segmentGaps.length,0,range+': evenly sampled points must not be gaps');
  assert(r.pointRadii.slice(1,-1).every(p=>p===0),range+': no dots inside a continuous line');
}
// A real outage (a full day missing) is still a gap.
const withOutage=sampled.filter((_,i)=>i<80||i>110);
const g=processDataWithGaps(withOutage,'7d');
assert.equal([...g.gapSegments].filter(i=>i<withOutage.length-1).length,1,'a day-long outage must still be one gap');
assert(g.gapSegments.has(79),'the gap starts at the last point before the outage');
// Metadata fields are not series; normal keys are.
assert.equal(isChartSeriesKey('_source'),false);
assert.equal(isChartSeriesKey('capturedAt'),false);
assert.equal(isChartSeriesKey('five_hour'),true);
// Per-model weekly buckets get a readable label.
assert.equal(anthropicQuotaLabel('seven_day_scoped_fable'),'Weekly Fable');
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
