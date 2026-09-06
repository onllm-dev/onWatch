package web

import (
	"strings"
	"testing"
)

func TestAppJS_OllamaQuotaCardsRerenderWhenQuotaSetChanges(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	if !strings.Contains(appJS, "function ollamaQuotaSetsMatch(container, quotas)") {
		t.Fatal("Ollama cards must compare rendered and incoming quota-name sets")
	}

	updateIdx := strings.Index(appJS, "} else if (provider === 'ollama') {")
	if updateIdx < 0 {
		t.Fatal("Ollama current-usage update branch not found")
	}
	updateBody := appJS[updateIdx:]
	setMatchIdx := strings.Index(updateBody, "!ollamaQuotaSetsMatch(container, data.quotas)")
	updateCardsIdx := strings.Index(updateBody, "data.quotas.forEach(q => updateOllamaCard(q));")
	if setMatchIdx < 0 {
		t.Fatal("Ollama cards must re-render when quota-name sets differ")
	}
	if updateCardsIdx < 0 || setMatchIdx > updateCardsIdx {
		t.Fatal("Ollama quota set comparison must happen before in-place card updates")
	}
}

func TestAppJS_OllamaLimitUnknownRendering(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	// The card must special-case an unknown cap: dollars-used label and a "--" percent.
	if !strings.Contains(appJS, "function ollamaCardLabel(quota)") {
		t.Fatal("Ollama cards must have a currency label helper")
	}
	if !strings.Contains(appJS, "' used'") && !strings.Contains(appJS, "used'") {
		t.Fatal("Ollama unknown-cap label must render dollars used")
	}
	if !strings.Contains(appJS, "renderOllamaModelBreakdown") {
		t.Fatal("Ollama detail view must render a per-model breakdown")
	}
}
