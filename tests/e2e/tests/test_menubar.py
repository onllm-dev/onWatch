"""Browser-surface tests for the menubar companion UI."""

from playwright.sync_api import Page, expect

BASE_URL = "http://localhost:19211"


def open_menubar(page: Page, view: str | None = None) -> None:
    url = f"{BASE_URL}/api/menubar/test"
    if view:
        url = f"{url}?view={view}"
    page.goto(url)
    if view:
        page.wait_for_selector(f"#menubar-shell.menubar-view-{view}", timeout=10000)
    else:
        page.wait_for_selector("#menubar-shell", timeout=10000)


class TestMenubarMinimalView:
    def test_renders_compact_provider_rows_with_footer(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "minimal")
        expect(authenticated_page.locator(".minimal-view")).to_be_visible()
        expect(authenticated_page.locator(".minimal-provider-row").first).to_be_visible()
        expect(authenticated_page.locator(".minimal-quota-inline").first).to_be_visible()
        expect(authenticated_page.locator(".minimal-quota-reset").first).to_be_visible()
        expect(authenticated_page.locator(".menubar-footer")).to_be_visible()


class TestMenubarStandardView:
    def test_renders_provider_cards_and_per_quota_resets(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "standard")
        expect(authenticated_page.locator("#menubar-shell.menubar-view-standard")).to_be_visible()
        expect(authenticated_page.locator("#header-value")).to_be_hidden()
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


class TestMenubarDetailedView:
    def test_shows_detailed_quota_rows(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "detailed")
        expect(authenticated_page.locator("#menubar-shell.menubar-view-detailed")).to_be_visible()
        expect(authenticated_page.locator(".provider-card").first).to_be_visible()
        expect(authenticated_page.locator(".quota-detail-section").first).to_be_visible()
        expect(authenticated_page.locator(".quota-bar-track").first).to_be_visible()
        expect(authenticated_page.locator(".quota-detail-meta").first).to_be_visible()

    def test_last_view_persists_without_query_override(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page)
        expect(authenticated_page.locator("#menubar-shell.menubar-view-standard")).to_be_visible()
        authenticated_page.locator("#view-toggle").click()
        expect(authenticated_page.locator("#menubar-shell.menubar-view-detailed")).to_be_visible()
        authenticated_page.reload()
        expect(authenticated_page.locator("#menubar-shell.menubar-view-detailed")).to_be_visible()
