package web

import (
	"regexp"
	"strings"
	"testing"
)

func readEmbeddedFile(t *testing.T, name string) string {
	t.Helper()

	var (
		data []byte
		err  error
	)
	if strings.HasPrefix(name, "static/") {
		data, err = staticFS.ReadFile(name)
	} else {
		data, err = templatesFS.ReadFile(name)
	}
	if err != nil {
		t.Fatalf("read embedded %s: %v", name, err)
	}
	return string(data)
}

func TestI18nRuntimeContract(t *testing.T) {
	t.Parallel()

	source := readEmbeddedFile(t, "static/i18n.js")
	required := []string{
		"const STORAGE_KEY = 'onwatch-language'",
		"Object.freeze(['en', 'zh-CN'])",
		"navigator.languages",
		"localStorage.getItem(STORAGE_KEY)",
		"localStorage.setItem(STORAGE_KEY",
		"document.documentElement.lang",
		"data-i18n-placeholder",
		"data-i18n-aria-label",
		"data-i18n-title",
		"onwatch:languagechange",
		"window.onWatchI18n",
	}
	for _, marker := range required {
		if !strings.Contains(source, marker) {
			t.Errorf("i18n runtime missing contract marker %q", marker)
		}
	}

	if !strings.Contains(source, "'zh-CN':") || !strings.Contains(source, "'settings.title': '设置'") {
		t.Fatal("Simplified Chinese dictionary is missing representative Settings translations")
	}
	if !strings.Contains(source, "en: Object.freeze({") || !strings.Contains(source, "'settings.title': 'Settings'") {
		t.Fatal("English fallback dictionary is missing representative Settings translations")
	}
}

func TestI18nLoadsBeforeDashboardRuntime(t *testing.T) {
	t.Parallel()

	layout := readEmbeddedFile(t, "templates/layout.html")
	i18nIndex := strings.Index(layout, "/static/i18n.js")
	appIndex := strings.Index(layout, "/static/app.js")
	if i18nIndex < 0 {
		t.Fatal("layout does not load static/i18n.js")
	}
	if appIndex < 0 {
		t.Fatal("layout does not load static/app.js")
	}
	if i18nIndex > appIndex {
		t.Fatal("i18n runtime must load before app.js")
	}
}

func TestLanguageSelectorsAvailableOnEveryPage(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"templates/login.html",
		"templates/dashboard.html",
		"templates/settings.html",
	} {
		template := readEmbeddedFile(t, name)
		if !strings.Contains(template, "data-language-selector") {
			t.Errorf("%s does not expose a language selector", name)
		}
		if !strings.Contains(template, `value="zh-CN"`) || !strings.Contains(template, `value="en"`) {
			t.Errorf("%s language selector must offer zh-CN and en", name)
		}
	}

	settings := readEmbeddedFile(t, "templates/settings.html")
	if !strings.Contains(settings, `id="settings-language"`) {
		t.Fatal("Settings -> General does not contain the full language preference field")
	}
}

func TestTemplatesUseStableTranslationKeys(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"templates/layout.html": {
			`data-i18n="a11y.skip_dashboard"`,
		},
		"templates/login.html": {
			`data-i18n="login.subtitle"`,
			`data-i18n-placeholder="login.username_placeholder"`,
			`data-i18n="login.sign_in"`,
		},
		"templates/dashboard.html": {
			`data-i18n="dashboard.title"`,
			`data-i18n="notifications.title"`,
			`data-i18n="table.sessions"`,
		},
		"templates/settings.html": {
			`data-i18n="settings.title"`,
			`data-i18n="settings.language"`,
			`data-i18n="settings.save"`,
		},
	}

	for name, markers := range cases {
		template := readEmbeddedFile(t, name)
		for _, marker := range markers {
			if !strings.Contains(template, marker) {
				t.Errorf("%s missing stable translation marker %s", name, marker)
			}
		}
	}
}

func TestAppJSUsesI18nForDynamicAndLocaleSensitiveText(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	required := []string{
		"function tr(key, params)",
		"function getActiveLocale()",
		"onwatch:languagechange",
		"tr('status.active')",
		"tr('account.all_accounts')",
		"tr('time.updated'",
		"toLocaleString(getActiveLocale()",
		"new Intl.NumberFormat(getActiveLocale()",
	}
	for _, marker := range required {
		if !strings.Contains(appJS, marker) {
			t.Errorf("app.js missing i18n integration marker %q", marker)
		}
	}
}

func TestAppJSLocalizesPrimaryDynamicSettingsAndDashboardText(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	required := []string{
		"tr('table.showing'",
		"tr('notifications.empty')",
		"tr('common.show')",
		"tr('codex.saved_profiles')",
		"tr('minimax.add_account')",
		"tr('settings.send_test_email')",
		"tr('settings.push_not_subscribed')",
		"tr('settings.password_updated')",
	}
	for _, marker := range required {
		if !strings.Contains(appJS, marker) {
			t.Errorf("app.js missing primary dynamic i18n marker %q", marker)
		}
	}

	forbidden := []string{
		"infoEl.textContent = `Showing ${",
		`notification-empty">No notifications`,
		`>+ Add Account</button>`,
		`>Saved Profiles</h4>`,
	}
	for _, marker := range forbidden {
		if strings.Contains(appJS, marker) {
			t.Errorf("app.js still contains untranslated primary UI text %q", marker)
		}
	}
}

func TestProviderSettingsCoverCredentialAndDiscoveryProviders(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	for _, marker := range []string{
		"moonshot: {",
		"deepseek: {",
		"cursor: {",
		"grok: {",
		"kimi: {",
		"desc: tr('provider_settings.moonshot_desc')",
		"desc: tr('provider_settings.deepseek_desc')",
		"desc: tr('provider_settings.cursor_credentials_desc')",
		"desc: tr('provider_settings.grok_credentials_desc')",
		"desc: tr('provider_settings.kimi_credentials_desc')",
	} {
		if !strings.Contains(appJS, marker) {
			t.Errorf("provider settings UI missing %q", marker)
		}
	}

	for _, provider := range []string{"moonshot", "deepseek"} {
		pattern := regexp.MustCompile(provider + `:\s*\{[\s\S]*?fields:\s*\[[\s\S]*?id:\s*'api_key'[\s\S]*?type:\s*'password'[\s\S]*?sensitive:\s*true`)
		if !pattern.MatchString(appJS) {
			t.Errorf("%s provider settings must use a sensitive API key password field", provider)
		}
	}
}

func TestI18nDictionariesCoverRuntimeAndTemplateKeys(t *testing.T) {
	t.Parallel()

	source := readEmbeddedFile(t, "static/i18n.js")
	separator := "    'zh-CN': Object.freeze({"
	parts := strings.SplitN(source, separator, 2)
	if len(parts) != 2 {
		t.Fatal("i18n runtime is missing the zh-CN dictionary boundary")
	}

	keyPattern := regexp.MustCompile(`(?m)^\s*'([^']+)':`)
	keySet := func(dictionary string) map[string]struct{} {
		keys := make(map[string]struct{})
		for _, match := range keyPattern.FindAllStringSubmatch(dictionary, -1) {
			if _, exists := keys[match[1]]; exists {
				t.Errorf("duplicate translation key %q", match[1])
			}
			keys[match[1]] = struct{}{}
		}
		return keys
	}

	english := keySet(parts[0])
	chinese := keySet(parts[1])
	for key := range english {
		if _, ok := chinese[key]; !ok {
			t.Errorf("zh-CN dictionary missing English key %q", key)
		}
	}
	for key := range chinese {
		if _, ok := english[key]; !ok {
			t.Errorf("English dictionary missing zh-CN key %q", key)
		}
	}

	assertKnown := func(sourceName, key string) {
		t.Helper()
		if _, ok := english[key]; !ok {
			t.Errorf("%s references unknown translation key %q", sourceName, key)
		}
	}

	appJS := readStaticAppJS(t)
	callPattern := regexp.MustCompile(`\btr\('([^']+)'`)
	for _, match := range callPattern.FindAllStringSubmatch(appJS, -1) {
		assertKnown("static/app.js", match[1])
	}

	attributePattern := regexp.MustCompile(`data-i18n(?:-placeholder|-aria-label|-title)?="([a-zA-Z0-9_.-]+)"`)
	for _, name := range []string{"templates/layout.html", "templates/login.html", "templates/dashboard.html", "templates/settings.html"} {
		for _, match := range attributePattern.FindAllStringSubmatch(readEmbeddedFile(t, name), -1) {
			assertKnown(name, match[1])
		}
	}
}

func TestSimplifiedChineseCoversLegacyVisibleEnglish(t *testing.T) {
	t.Parallel()

	templates := readEmbeddedFile(t, "templates/dashboard.html") + readEmbeddedFile(t, "templates/settings.html")
	appJS := readStaticAppJS(t)

	templateForbidden := []string{
		">Tokens per Request<",
		">Source File<",
		">How Custom API Integrations Work<",
		`aria-label="Dashboard provider tab order and names"`,
		`aria-label="API integrations chart metric"`,
		`data-range="1h">1h</button>`,
	}
	for _, marker := range templateForbidden {
		if strings.Contains(templates, marker) {
			t.Errorf("templates still contain unlocalized visible text %q", marker)
		}
	}
	templateRequired := []string{
		`data-i18n="time.updated_placeholder"`,
		`data-i18n-placeholder="settings.from_name_placeholder"`,
		`data-i18n-duration="1h"`,
	}
	for _, marker := range templateRequired {
		if !strings.Contains(templates, marker) {
			t.Errorf("templates missing complete localization marker %q", marker)
		}
	}

	appForbidden := []string{
		"desc: 'Configure how onWatch collects Anthropic usage data.",
		"description: 'Claude Code usage tracking'",
		"Reload Providers From .env",
		"<option value=\"\">Select quota...</option>",
		"title=\"Send warning notifications\"",
		">Provider <span class=\"sort-arrow\"",
		`<div class="settings-toggle-label">API Integrations`,
		`aria-label="Open ${escapeHTML(accountName)} details"`,
		`quota.source === 'statusline' ? 'Live' : 'API'`,
		`data.source === 'statusline' ? '\u{1F7E2} Live' : '\u{1F310} API'`,
		`fractionText = '∞ Unlimited'`,
		`fractionEl.textContent = '∞ Unlimited'`,
		`const label = isAnthropicPeakHours(promo) ? 'Peak hours' : 'Off-peak hours'`,
		`aria-label="${collapsed ? 'Expand' : 'Collapse'}`,
		"pwdInput.placeholder = '********** (saved)'",
		`opt.textContent = q.label`,
		`title="Auto-start is on`,
		`aria-label="View ${displayName} details"`,
		`% remaining`,
		`>Total Delta`,
		"'Key is configured - leave blank to keep'",
	}
	for _, marker := range appForbidden {
		if strings.Contains(appJS, marker) {
			t.Errorf("app.js still contains unlocalized visible text %q", marker)
		}
	}

	required := []string{
		"localizedProviderDescription(",
		"createProviderSettingsConfig()",
		"localizedCardLabel(",
		"localizedDatasetLabel(",
		"localizedRangeLabel(",
		"localizedMiniMaxLabel(",
		"if (key === 'api-integrations') return tr('dashboard.api_integrations_label')",
		"tr('settings.reload_providers')",
		"tr('settings.select_quota')",
		"tr('table.provider')",
		"tr('common.unlimited')",
		"tr('promo.until_transition'",
		"tr(collapsed ? 'a11y.expand_provider' : 'a11y.collapse_provider'",
		"tr('settings.saved_secret_placeholder')",
		"setAttribute('data-i18n-placeholder', 'settings.saved_secret_placeholder')",
	}
	for _, marker := range required {
		if !strings.Contains(appJS, marker) {
			t.Errorf("app.js missing complete localization marker %q", marker)
		}
	}
}

func TestOpenCodeAccountFormExplainsCredentialSources(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	if strings.Contains(appJS, `placeholder="wrk_..."`) {
		t.Fatal("OpenCode workspace placeholder must show a complete example instead of an ellipsis")
	}

	required := []string{
		"tr('opencode.workspace_placeholder')",
		"tr('opencode.workspace_hint')",
		"tr('opencode.cookie_source_hint')",
	}
	for _, marker := range required {
		if !strings.Contains(appJS, marker) {
			t.Errorf("OpenCode account form missing credential guidance marker %q", marker)
		}
	}
}
