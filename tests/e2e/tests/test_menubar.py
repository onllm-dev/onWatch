"""Browser-surface tests for the menubar companion UI."""

import re

from playwright.sync_api import Page, expect

BASE_URL = "http://localhost:19211"


def open_menubar(page: Page, view: str) -> None:
    page.goto(f"{BASE_URL}/api/menubar/test?view={view}")
    page.wait_for_selector(f".menubar-view-{view}", timeout=10000)


class TestMenubarMinimalView:
    def test_renders_compact_aggregate_view(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "minimal")
        expect(authenticated_page.locator(".minimal-view")).to_be_visible()
        expect(authenticated_page.locator(".aggregate-circle")).to_be_visible()
        expect(authenticated_page.locator(".aggregate-percent")).to_have_text(re.compile(r"\d+%"))

    def test_minimal_view_keeps_footer_links(self, authenticated_page: Page) -> None:
        authenticated_page.set_viewport_size({"width": 240, "height": 180})
        open_menubar(authenticated_page, "minimal")
        expect(authenticated_page.locator("#footer")).to_be_visible()
        expect(authenticated_page.locator(".provider-card")).to_have_count(0)


class TestMenubarStandardView:
    def test_renders_provider_cards_and_quota_meters(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "standard")
        expect(authenticated_page.locator(".menubar-view-standard")).to_be_visible()
        first_card = authenticated_page.locator(".provider-card").first
        expect(first_card).to_be_visible()
        first_card.locator("summary").click()
        expect(authenticated_page.locator(".quota-meter").first).to_be_visible()

    def test_footer_links_match_expected_targets(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "standard")
        expect(authenticated_page.locator("#footer a[href='https://github.com/onllm-dev/onwatch']")).to_be_visible()
        expect(authenticated_page.locator("#footer a[href='https://github.com/onllm-dev/onwatch/issues']")).to_be_visible()
        expect(authenticated_page.locator("#footer a[href='https://onllm.dev']")).to_be_visible()


class TestMenubarDetailedView:
    def test_expands_provider_cards_with_trends(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "detailed")
        expect(authenticated_page.locator(".menubar-view-detailed")).to_be_visible()
        expect(authenticated_page.locator(".provider-card[open]").first).to_be_visible()
        expect(authenticated_page.locator(".provider-trends").first).to_be_visible()

    def test_requested_view_is_honored(self, authenticated_page: Page) -> None:
        open_menubar(authenticated_page, "detailed")
        expect(authenticated_page.locator(".minimal-view")).to_have_count(0)
