package web

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestPeakChipOncePerCard ensures every provider card renders exactly one
// DeepSeek peak chip. The individual views put it on the first quota (or the
// primary balance) card; the All Providers view must not add a second one in
// the card header, and must still show one for balance-only providers.
func TestPeakChipOncePerCard(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	peakHelperPath := filepath.Join(filepath.Dir(file), "static", "peak-hours.js")
	script := `
const assert=require('node:assert/strict');
const registry={};
function makeEl(id){
  return {
    id, dataset:{}, className:'', innerHTML:'', textContent:'', style:{}, children:[],
    classList:{_set:new Set(),add(c){this._set.add(c);},remove(c){this._set.delete(c);},contains(c){return this._set.has(c);}},
    setAttribute(){}, removeAttribute(){}, getAttribute(){return null;}, appendChild(child){this.children.push(child); this.innerHTML += child.innerHTML || '';}, append(){},
    querySelector(){return null;}, querySelectorAll(){return [];},
  };
}
const containers={};
const providerTabs=['commandcode','opencode','deepseek'].map(p=>({dataset:{provider:p}}));
const document={
  querySelector(){return null;},
  querySelectorAll(sel){ return sel==='#provider-tabs .provider-tab[data-provider]' ? providerTabs : []; },
  getElementById(id){ if(!containers[id]) containers[id]=makeEl(id); return containers[id]; },
  createElement(){return makeEl('el');},
  addEventListener(){},
  documentElement:{getAttribute:()=>null},
};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
const getComputedStyle=()=>({getPropertyValue:()=>''});
console.warn=()=>{};
const peakHelper=require(` + jsStringLiteral(t, peakHelperPath) + `);
window.OnWatchPeak=peakHelper;
` + string(source) + `
const chipCount = (html) => (String(html).match(/peak-chip-wrap/g) || []).length;

// Individual provider views: one chip per grid.
State.currentRange='6h';
renderCommandCodeQuotaCards([{name:'five_hour',utilization:20,status:'healthy'},{name:'weekly',utilization:30,status:'healthy'}], 'quota-grid-commandcode');
assert.equal(chipCount(containers['quota-grid-commandcode'].innerHTML),1,'Command Code individual view must show one chip');

renderOpenCodeQuotaCards([{name:'five_hour',utilization:20,status:'healthy'},{name:'weekly',utilization:30,status:'healthy'}], 'quota-grid-opencode');
assert.equal(chipCount(containers['quota-grid-opencode'].innerHTML),1,'OpenCode individual view must show one chip');

renderBalanceCards('deepseek', {status:'healthy',currency:'USD',total:4.2,granted:1,toppedUp:3.2}, 'quota-grid-deepseek');
assert.equal(chipCount(containers['quota-grid-deepseek'].innerHTML),1,'DeepSeek individual view must show one chip');

// All Providers view: one chip per provider card.
State.allProvidersCurrent={
  commandcode:{quotas:[{name:'five_hour',utilization:20,status:'healthy'},{name:'weekly',utilization:30,status:'healthy'}],planType:''},
  opencode:{quotas:[{name:'five_hour',utilization:20,status:'healthy'}],planName:''},
  deepseek:{capturedAt:new Date().toISOString(),balance:{status:'healthy',currency:'USD',total:4.2}},
};
State.allProvidersInsights={};
State.allProvidersHistory={};
const all=document.getElementById('all-providers-container');
renderAllProvidersView();
const cards=String(all.innerHTML).split('<section class="provider-card').slice(1);
assert.equal(cards.length,3,'expected three provider cards');
for(const card of cards) {
  const provider=(card.match(/data-provider="([^"]+)"/)||[])[1]||'unknown';
  assert.equal(chipCount(card),1,provider+' All Providers card must show exactly one chip');
}
`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
