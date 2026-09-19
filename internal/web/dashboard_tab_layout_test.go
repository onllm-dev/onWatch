package web

import (
	"regexp"
	"strings"
	"testing"
)

func readStaticStyleCSS(t *testing.T) string {
	t.Helper()
	data, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		t.Fatalf("read static/style.css: %v", err)
	}
	return string(data)
}

// cssRule returns the declaration block for the first rule whose selector list
// contains the given selector, so assertions do not accidentally match a
// neighbouring rule that happens to share a property.
func cssRule(t *testing.T, css, selector string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^[^@{}]*\Q` + selector + `\E\s*(,[^{}]*)?\{([^}]*)\}`)
	m := re.FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("no CSS rule found for %s", selector)
	}
	return m[2]
}

// The tab order list is one row per provider and onWatch ships 16 providers, so
// a row that stacks its label and input vertically turns this section into
// roughly 1,900px of scrolling. Each row must lay its columns out horizontally.
func TestCSS_DashboardTabOrderRowIsHorizontal(t *testing.T) {
	t.Parallel()

	css := readStaticStyleCSS(t)
	rule := cssRule(t, css, ".dashboard-tab-order-item")

	if !strings.Contains(rule, "display: grid") {
		t.Error(".dashboard-tab-order-item must use a grid so name, input and controls share one row")
	}
	if !strings.Contains(rule, "grid-template-columns") {
		t.Error(".dashboard-tab-order-item must declare grid-template-columns to align rows into columns")
	}

	fields := cssRule(t, css, ".dashboard-tab-order-fields")
	if strings.Contains(fields, "flex-direction: column") {
		t.Error(".dashboard-tab-order-fields must not stack its children vertically")
	}
}

// Stacking the two reorder arrows vertically forces the row taller than its
// text content needs.
func TestCSS_DashboardTabOrderArrowsAreSideBySide(t *testing.T) {
	t.Parallel()

	css := readStaticStyleCSS(t)
	rule := cssRule(t, css, ".dashboard-tab-order-move")

	if strings.Contains(rule, "flex-direction: column") {
		t.Error(".dashboard-tab-order-move must lay the arrows out side by side, not stacked")
	}
}

// The grid only makes sense while there is room for it; narrow viewports must
// fall back to the stacked layout rather than crushing the rename input.
func TestCSS_DashboardTabOrderStacksOnNarrowViewports(t *testing.T) {
	t.Parallel()

	css := readStaticStyleCSS(t)

	idx := strings.Index(css, ".dashboard-tab-order-item")
	if idx < 0 {
		t.Fatal(".dashboard-tab-order-item not found")
	}
	if !strings.Contains(css, "@media (max-width: 720px)") {
		t.Error("expected a narrow-viewport breakpoint that restacks the tab order rows")
	}
}

// The per-row "Tab name" caption repeated the section description 16 times and
// cost a whole line of height in every row. The input keeps its accessible name
// through aria-label instead.
func TestAppJS_DashboardTabRowHasNoRedundantCaption(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	if strings.Contains(appJS, "dashboard-tab-rename-label") {
		t.Error("the per-row 'Tab name' caption should be gone; the aria-label carries the accessible name")
	}
	if !strings.Contains(appJS, `aria-label="Rename tab for`) {
		t.Error("the rename input must keep an aria-label so it stays reachable without a visible caption")
	}
	if !strings.Contains(appJS, `placeholder="${escapeHTML(placeholder)}"`) {
		t.Error("the rename input must keep showing the default name as its placeholder")
	}
}

// Reordering is the primary action in this list, so the drag handle and both
// arrow buttons must survive the layout change.
func TestAppJS_DashboardTabRowKeepsReorderControls(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	for _, needle := range []string{
		`class="menubar-order-handle"`,
		`data-dashboard-move="up"`,
		`data-dashboard-move="down"`,
		`draggable="true"`,
	} {
		if !strings.Contains(appJS, needle) {
			t.Errorf("dashboard tab row lost %s", needle)
		}
	}
}

// The Telemetry and Dashboard hints were identical on all 17 provider cards, so
// they cost a line of height per card to say the same two things over and over.
// The meaning moves to the toggle's tooltip instead.
func TestAppJS_ProviderToggleHintsAreNotRepeatedPerCard(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	if strings.Contains(appJS, "settings-toggle-item-hint") {
		t.Error("per-card Telemetry/Dashboard hint text should be gone; use the toggle tooltip")
	}
	if !strings.Contains(appJS, "Track usage data in background") {
		t.Error("the Telemetry explanation must survive as a tooltip")
	}
	if !strings.Contains(appJS, "Show as individual tab") {
		t.Error("the Dashboard explanation must survive as a tooltip")
	}
}

// Density is the point of the change, so pin the paddings that drive row height.
func TestCSS_SettingsSectionsAreCompact(t *testing.T) {
	t.Parallel()

	css := readStaticStyleCSS(t)

	section := cssRule(t, css, ".settings-section")
	if strings.Contains(section, "padding: 24px") {
		t.Error(".settings-section still uses the original 24px padding")
	}

	desc := cssRule(t, css, ".settings-section-desc")
	if strings.Contains(desc, "margin: 0 0 20px") {
		t.Error(".settings-section-desc still reserves a 20px gap under every description")
	}

	toggleRow := cssRule(t, css, ".settings-toggle-row")
	if strings.Contains(toggleRow, "padding: 14px 16px") {
		t.Error(".settings-toggle-row still uses the original 14px padding")
	}
}

// The webhook configuration block is the tallest thing on the Notifications
// tab, and it was rendering at full height even with the channel switched off.
// It is now disclosed by the channel toggle.
func TestSettings_WebhookConfigSectionIsCollapsible(t *testing.T) {
	t.Parallel()

	html := readSettingsTemplate(t)
	if !strings.Contains(html, `id="webhook-config-section"`) {
		t.Error("the Webhook Configuration section needs an id so the channel toggle can disclose it")
	}

	appJS := readStaticAppJS(t)
	if !strings.Contains(appJS, "syncWebhookConfigVisibility") {
		t.Error("app.js must sync the webhook config section with the channel toggle")
	}
	if !strings.Contains(appJS, "syncWebhookConfigVisibility();") {
		t.Error("syncWebhookConfigVisibility is never called")
	}
}

// Short checkbox labels waste half the row when forced full width.
func TestCSS_ShortCheckboxListsUseTwoColumns(t *testing.T) {
	t.Parallel()

	css := readStaticStyleCSS(t)
	if !strings.Contains(css, ".settings-checkbox-row-compact") {
		t.Error("expected a compact checkbox variant that pairs short options into two columns")
	}

	html := readSettingsTemplate(t)
	if !strings.Contains(html, "settings-checkbox-row settings-checkbox-row-compact") {
		t.Error("the short webhook event and notification type options should use the compact variant")
	}
}

// Cards without a gear button let the toggle group slide right, so the
// Telemetry and Dashboard columns landed at a different x on almost every card.
// Fixed-width toggle slots plus a reserved gear slot keep the columns straight.
func TestProviderCards_ToggleColumnsAreAligned(t *testing.T) {
	t.Parallel()

	css := readStaticStyleCSS(t)
	item := cssRule(t, css, ".settings-toggle-item")
	if !strings.Contains(item, "width:") {
		t.Error(".settings-toggle-item needs a fixed width so the columns align across cards")
	}

	if !strings.Contains(css, ".settings-toggle-gear-slot") {
		t.Error("expected a reserved gear slot so gear-less cards keep the same trailing width")
	}

	appJS := readStaticAppJS(t)
	if !strings.Contains(appJS, "settings-toggle-gear-slot") {
		t.Error("provider cards must render the reserved gear slot")
	}
}

// The settings column was pinned to 760px while the dashboard already scales to
// var(--layout-max-width), so on a wide window more than half the page was empty
// margin. Settings now follows the same width (and the same density preference).
func TestCSS_SettingsMainUsesFullPageWidth(t *testing.T) {
	t.Parallel()

	css := readStaticStyleCSS(t)
	rule := cssRule(t, css, ".settings-main")

	if strings.Contains(rule, "max-width: 760px") {
		t.Error(".settings-main still pins the settings column to 760px")
	}
	if !strings.Contains(rule, "var(--layout-max-width") {
		t.Error(".settings-main should follow --layout-max-width like the dashboard does")
	}
}

// With the full page width available, the Providers tab puts the tab-order list
// and the provider controls side by side instead of stacking them into one very
// long column.
func TestCSS_ProvidersPanelIsTwoColumns(t *testing.T) {
	t.Parallel()

	css := readStaticStyleCSS(t)
	rule := cssRule(t, css, "#panel-providers.settings-panel.active")

	if !strings.Contains(rule, "display: grid") {
		t.Error("the Providers panel should lay its two sections out side by side")
	}
	if !strings.Contains(rule, "grid-template-columns") {
		t.Error("the Providers panel needs a column template")
	}
	if !strings.Contains(css, "@media (max-width: 1100px)") {
		t.Error("the side-by-side layout must collapse back to one column on narrow windows")
	}
}

// Providers that are not set up dominated the list without being actionable, so
// they collapse into a disclosure that keeps them reachable.
func TestAppJS_UnconfiguredProvidersAreCollapsed(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	if !strings.Contains(appJS, "provider-unconfigured") {
		t.Error("unconfigured providers should render into their own collapsed group")
	}
	if !strings.Contains(appJS, "Not configured (") {
		t.Error("the disclosure should say how many providers are hidden")
	}
	// They must stay reachable: a <details> keeps them in the DOM and expandable.
	if !strings.Contains(appJS, `document.createElement('details')`) {
		t.Error("use a <details> so the hidden providers can still be expanded and enabled")
	}
	if !strings.Contains(appJS, "unconfiguredGroup.className = 'provider-unconfigured'") {
		t.Error("the disclosure needs the provider-unconfigured class for styling")
	}
}
