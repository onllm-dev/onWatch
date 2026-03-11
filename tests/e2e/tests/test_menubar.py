"""Browser-surface tests for the menubar companion UI."""

from playwright.sync_api import Page, expect

BASE_URL = "http://localhost:19211"


def open_menubar(page: Page, view: str | None = None, expected_view: str | None = None) -> None:
    url = f"{BASE_URL}/api/menubar/test"
    if view:
        url = f"{url}?view={view}"
    page.goto(url)
    resolved_view = expected_view or view
    if resolved_view:
        page.wait_for_selector(f"#menubar-shell.menubar-view-{resolved_view}", timeout=10000)
    else:
        page.wait_for_selector("#menubar-shell", timeout=10000)


class TestMenubarStandardView:
    def test_minimal_query_preserves_minimal_view(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "minimal")
        expect(authenticated_page.locator("#menubar-shell.menubar-view-minimal")).to_be_visible()
        expect(authenticated_page.locator(".minimal-view")).to_have_count(1)
        expect(authenticated_page.locator(".menubar-footer")).to_be_visible()

    def test_renders_provider_cards_and_per_quota_resets(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "standard")
        expect(authenticated_page.locator("#menubar-shell.menubar-view-standard")).to_be_visible()
        expect(authenticated_page.locator("#header-value")).to_be_visible()
        first_card = authenticated_page.locator(".provider-card").first
        expect(first_card).to_be_visible()
        expect(first_card.locator(".provider-icon")).to_be_visible()
        expect(authenticated_page.locator(".quota-meter").first).to_be_visible()
        expect(authenticated_page.locator(".quota-reset-line").first).to_be_visible()

    def test_footer_refresh_and_links_are_visible(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "standard")
        expect(authenticated_page.locator("#refresh-button")).to_be_visible()
        expect(authenticated_page.locator("#footer-github")).to_have_attribute("href", "https://github.com/onllm-dev/onwatch")
        expect(authenticated_page.locator("#footer-support")).to_have_attribute("href", "https://github.com/onllm-dev/onwatch/issues")
        expect(authenticated_page.locator("#footer-onllm")).to_have_attribute("href", "https://onllm.dev")

    def test_settings_panel_only_shows_supported_status_modes(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "standard")
        authenticated_page.locator("#settings-toggle").click()
        expect(authenticated_page.locator('input[name="status-display"]')).to_have_count(3)
        expect(authenticated_page.get_by_text("Multi-provider", exact=True)).to_be_visible()
        expect(authenticated_page.get_by_text("Critical count", exact=True)).to_be_visible()
        expect(authenticated_page.get_by_text("Icon only", exact=True)).to_be_visible()
        expect(authenticated_page.locator("text=Highest %")).to_have_count(0)
        expect(authenticated_page.locator("text=Aggregate")).to_have_count(0)
        assert authenticated_page.locator('input[name="status-selection"]').count() > 0
        expect(authenticated_page.locator("#status-provider")).to_have_count(0)
        expect(authenticated_page.locator("#status-quota")).to_have_count(0)
        expect(authenticated_page.locator("text=Preview")).to_be_visible()

    def test_provider_order_arrow_controls_reorder_rows(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "standard")
        authenticated_page.locator("#settings-toggle").click()

        rows = authenticated_page.locator("#provider-order-list .provider-order-item")
        assert rows.count() >= 2

        first_before = rows.nth(0).get_attribute("data-provider-id")
        second_before = rows.nth(1).get_attribute("data-provider-id")
        assert first_before
        assert second_before

        move_down = rows.nth(0).locator('button[data-provider-move="down"]')
        expect(move_down).to_be_visible()
        move_down.click()

        rows_after = authenticated_page.locator("#provider-order-list .provider-order-item")
        first_after = rows_after.nth(0).get_attribute("data-provider-id")
        second_after = rows_after.nth(1).get_attribute("data-provider-id")
        assert first_after == second_before
        assert second_after == first_before

    def test_light_theme_switch_applies_root_theme_and_save(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "standard")
        authenticated_page.locator("#settings-toggle").click()

        theme_select = authenticated_page.locator('select[name="theme-mode"]')
        expect(theme_select).to_be_visible()
        theme_select.select_option("light")

        html_root = authenticated_page.locator("html")
        expect(html_root).to_have_attribute("data-theme", "light")

        authenticated_page.locator("#settings-save").click()
        expect(authenticated_page.locator("#settings-panel")).to_be_hidden()


class TestMenubarDetailedView:
    def test_shows_detailed_quota_rows(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "detailed")
        expect(authenticated_page.locator("#menubar-shell.menubar-view-detailed")).to_be_visible()
        expect(authenticated_page.locator(".provider-card").first).to_be_visible()
        expect(authenticated_page.locator(".quota-detail-section").first).to_be_visible()
        expect(authenticated_page.locator(".quota-bar-track").first).to_be_visible()
        expect(authenticated_page.locator(".quota-detail-meta").first).to_be_visible()

    def test_unsaved_view_toggle_does_not_override_default_view(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page)
        expect(authenticated_page.locator("#menubar-shell.menubar-view-standard")).to_be_visible()
        authenticated_page.locator("#view-toggle").click()
        expect(authenticated_page.locator("#menubar-shell.menubar-view-detailed")).to_be_visible()
        authenticated_page.reload()
        expect(authenticated_page.locator("#menubar-shell.menubar-view-standard")).to_be_visible()
