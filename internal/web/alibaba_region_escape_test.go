package web

import (
	"strings"
	"testing"
)

// The Alibaba region is user-controlled (env or dashboard). Both places that
// interpolate it into HTML must escape it; the MiniMax region render is out of
// scope and stays as-is.
func TestAppJS_AlibabaRegionIsEscaped(t *testing.T) {
	appJS := readStaticAppJS(t)

	if strings.Contains(appJS, "`Alibaba account: ${escapeHtml(account.name)}${account.region ? ' (' + account.region + ')' : ''}`") {
		t.Error("the Alibaba provider section still interpolates account.region unescaped")
	}
	if !strings.Contains(appJS, "`Alibaba account: ${escapeHtml(account.name)}${account.region ? ' (' + escapeHTML(account.region) + ')' : ''}`") {
		t.Error("the Alibaba provider section must escape account.region")
	}
	if strings.Contains(appJS, "${isDeleted ? 'Deleted' : regionLabel + ' - ' + (account.hasCookies ? 'Cookies configured' : 'No cookies set')}") {
		t.Error("the Alibaba account list still interpolates regionLabel unescaped")
	}
	if !strings.Contains(appJS, "${isDeleted ? 'Deleted' : escapeHTML(regionLabel) + ' - ' + (account.hasCookies ? 'Cookies configured' : 'No cookies set')}") {
		t.Error("the Alibaba account list must escape regionLabel")
	}

	// MiniMax keeps its existing rendering (item 4/8 scope).
	if !strings.Contains(appJS, "`MiniMax account: ${escapeHtml(account.name)}${account.region ? ' (' + account.region + ')' : ''}`") {
		t.Error("the MiniMax region render should be left unchanged")
	}
	if !strings.Contains(appJS, "${isDeleted ? 'Deleted' : regionLabel + ' - ' + (account.hasKey ? 'Key configured' : 'No key set')}") {
		t.Error("the MiniMax region label render should be left unchanged")
	}
}
