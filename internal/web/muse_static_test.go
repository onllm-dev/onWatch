package web

import (
	"strings"
	"testing"
)

func TestAppJS_MuseQuotaCardsRerenderWhenQuotaSetChanges(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	if !strings.Contains(appJS, "function museQuotaSetsMatch(container, quotas)") {
		t.Fatal("Muse cards must compare rendered and incoming quota-name sets")
	}

	updateIdx := strings.Index(appJS, "} else if (provider === 'muse') {")
	if updateIdx < 0 {
		t.Fatal("Muse current-usage update branch not found")
	}
	updateBody := appJS[updateIdx:]
	setMatchIdx := strings.Index(updateBody, "!museQuotaSetsMatch(container, data.quotas)")
	updateCardsIdx := strings.Index(updateBody, "data.quotas.forEach(q => updateMuseCard(q));")
	if setMatchIdx < 0 {
		t.Fatal("Muse cards must re-render when quota-name sets differ")
	}
	if updateCardsIdx < 0 || setMatchIdx > updateCardsIdx {
		t.Fatal("Muse quota set comparison must happen before in-place card updates")
	}
}

func TestAppJS_MuseWindowsWired(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	for _, want := range []string{
		"function renderMuseQuotaCards(quotas, containerId)",
		"function updateMuseCard(quota)",
		"function museCardLabel(quota)",
		"quota-grid-muse",
		"window_5h",
	} {
		if !strings.Contains(appJS, want) {
			t.Fatalf("app.js must contain %q for Muse support", want)
		}
	}
}
