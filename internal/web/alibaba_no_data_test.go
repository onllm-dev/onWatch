package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Before the first poll Alibaba has no data. The card must say so (like the
// Gemini, OpenCode, Ollama and Mistral cards) instead of drawing a fabricated
// 0.0% healthy state.
func TestAlibabaCardBeforeFirstPollShowsNoData(t *testing.T) {
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
const containers={};
const document={
  querySelector:()=>null, querySelectorAll:()=>[],
  getElementById:(id)=>containers[id]||null,
  createElement:()=>({dataset:{},style:{},classList:{add:()=>{},toggle:()=>{}},appendChild:()=>{},setAttribute:()=>{}}),
  addEventListener:()=>{}, documentElement:{}
};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
` + string(source) + `
// No fabricated placeholder list.
assert.deepEqual(alibabaEffectiveQuotas([]),[],'no placeholder quota list');
assert.deepEqual(alibabaEffectiveQuotas(null),[],'no placeholder quota list for null');

// Rendering the empty payload shows the empty state, not a healthy 0%.
const grid={innerHTML:'',children:[],querySelector:()=>null,querySelectorAll:()=>[]};
containers['quota-grid-alibaba']=grid;
assert.equal(alibabaQuotaSetsMatch(grid,[]),false,'a bare grid does not yet match an empty payload');
renderAlibabaQuotaCards([], 'quota-grid-alibaba');
assert(grid.innerHTML.includes('empty-state'),'empty payload must render the empty state: '+grid.innerHTML);
assert(!grid.innerHTML.includes('0.0%'),'empty payload must not render a fabricated percentage: '+grid.innerHTML);
assert(grid.innerHTML.includes('No Alibaba data yet'),'empty state must name the provider');
// Once rendered, the empty state counts as a match so it is not redrawn.
grid.querySelector=(sel)=> sel === '.empty-state' ? {} : null;
assert.equal(alibabaQuotaSetsMatch(grid,[]),true,'the rendered empty state matches an empty payload');

// A real payload still renders cards.
const real={innerHTML:'',children:[],querySelector:()=>null};
containers['quota-grid-real']=real;
real.appendChild=function(card){ real.innerHTML += card.innerHTML; };
renderAlibabaQuotaCards([{name:'five_hour',utilization:12.5,status:'healthy'}], 'quota-grid-real');
assert(!real.innerHTML.includes('empty-state'),'real data must not render the empty state');
assert(real.innerHTML.includes('12.5%'),'real data renders its percentage: '+real.innerHTML);
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// A failed storage read (readOK false) must not replace already-rendered cards
// with the empty state; a successful read with no rows still shows the empty
// state. The decision helper is the one fetchCurrent uses. On the very first
// load there is nothing to keep, so the panel must explain the failed read, and
// a later successful load must replace that message with the real cards.
func TestAlibabaFailedReadKeepsRenderedCards(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');
const containers={};
function el(tag){return {tagName:tag,innerHTML:'',children:[],style:{},dataset:{},classList:{add:()=>{},remove:()=>{},toggle:()=>{}},appendChild(c){this.children.push(c);},setAttribute:()=>{},addEventListener:()=>{},querySelector:()=>null,querySelectorAll:()=>[]};}
const document={
  querySelector:()=>null, querySelectorAll:()=>[],
  getElementById:(id)=>containers[id]||null,
  createElement:el,
  addEventListener:()=>{}, documentElement:{}, body:{classList:{toggle:()=>{}}}
};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
` + string(source) + `
// A failed read carries no rows and must be ignored, so the grid is untouched.
assert.equal(alibabaCanRenderPayload({quotas:[],readOK:false}),false,'failed read must not render');
assert.equal(alibabaCanRenderPayload({quotas:null,readOK:false}),false,'failed read with null quotas must not render');
// A successful empty read renders the empty state once.
assert.equal(alibabaCanRenderPayload({quotas:[],readOK:true}),true,'empty successful read renders');
assert.equal(alibabaCanRenderPayload({quotas:[{name:'five_hour',utilization:1}]}),true,'legacy payload without readOK renders');

// First load + failed read: nothing is rendered yet, so the panel explains the
// failed read in the existing empty-state style instead of staying blank.
const first={innerHTML:'',children:[],querySelector:()=>null,querySelectorAll:()=>[]};
first.appendChild=function(card){ first.innerHTML += card.innerHTML; };
containers['quota-grid-alibaba']=first;
alibabaShowReadError('quota-grid-alibaba');
assert(first.innerHTML.includes('empty-state'),'first-load read error must use the empty-state element: '+first.innerHTML);
assert(first.innerHTML.includes('Could not load Alibaba data'),'first-load read error must explain the blank panel: '+first.innerHTML);

// A later successful load replaces that message with real cards, even though
// it is also an empty-state element. The read-error marker keeps it from
// counting as the real empty state.
first.querySelector=(sel)=> sel.indexOf('empty-state') === 0 ? {} : null;
first.querySelectorAll=()=>[];
assert.equal(alibabaQuotaSetsMatch(first,[]),false,'the read-error message must not match a successful empty payload');
// fetchCurrent only redraws when the grid does not already match, so this is
// the path a successful load takes over the message.
if (!alibabaQuotaSetsMatch(first, [{name:'five_hour',utilization:12.5,status:'healthy'}])) {
  renderAlibabaQuotaCards([{name:'five_hour',utilization:12.5,status:'healthy'}], 'quota-grid-alibaba');
}
assert(!first.innerHTML.includes('Could not load Alibaba data'),'a successful load clears the read-error message: '+first.innerHTML);
assert(!first.innerHTML.includes('empty-state'),'a successful load must not keep the read-error empty state: '+first.innerHTML);
assert(first.innerHTML.includes('12.5%'),'a successful load renders the real cards: '+first.innerHTML);

// Populated grid + failed read: cards stay and no message replaces them.
const grid={innerHTML:'cards',children:[{dataset:{quota:'five_hour'}}],querySelector:(sel)=> sel === '.alibaba-card' ? {} : null,querySelectorAll:()=>[]};
containers['quota-grid-alibaba']=grid;
const before=grid.innerHTML;
if (alibabaCanRenderPayload({quotas:[],readOK:false})) {
  renderAlibabaQuotaCards([], 'quota-grid-alibaba');
}
alibabaShowReadError('quota-grid-alibaba');
assert.equal(grid.children.length,1,'existing cards must stay after a failed read');
assert.equal(grid.innerHTML,before,'a failed read must not rewrite the grid');
assert(!grid.innerHTML.includes('Could not load Alibaba data'),'a failed read must not add the message over existing cards');
`
	runNodeScript(t, script)
}
