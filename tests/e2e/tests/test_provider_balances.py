"""E2E tests for OpenCode Go session-cookie mode and the DeepSeek and Moonshot tabs.

A dedicated onwatch instance polls the mock server with real agents:
OpenCode Go through the console status API with a session cookie (#134),
and the DeepSeek and Moonshot balance APIs (#137).
"""
import json
import urllib.request
from typing import Generator

import pytest
from playwright.sync_api import Page, expect

from conftest import (
    MOCK_URL,
    PASSWORD,
    TMP_DIR,
    USERNAME,
    _kill_process,
    remove_instance_files,
    start_onwatch,
)

PORT = 19215
BASE = f"http://localhost:{PORT}"
DB = str(TMP_DIR / "onwatch-e2e-providers.db")
HOME = str(TMP_DIR / "onwatch-e2e-providers-home")


def _mock_counts() -> dict:
    with urllib.request.urlopen(f"{MOCK_URL}/admin/requests", timeout=5) as resp:
        return json.load(resp)


@pytest.fixture(scope="module")
def provider_server(servers) -> Generator[str, None, None]:
    """onwatch with only OpenCode Go (session cookie), DeepSeek and Moonshot."""
    proc = start_onwatch(PORT, DB, HOME, {
        "OPENCODE_GO_BASE_URL": MOCK_URL,
        "OPENCODE_GO_WORKSPACE_ID": "wrk_e2e",
        # A bare value: onWatch must send it as __Host-console_session.
        "OPENCODE_GO_AUTH_COOKIE": "oc_session_e2e",
        "DEEPSEEK_API_KEY": "sk_balance_e2e",
        "DEEPSEEK_BASE_URL": MOCK_URL,
        "MOONSHOT_API_KEY": "sk_balance_e2e",
        "MOONSHOT_BASE_URL": MOCK_URL,
    })
    yield BASE
    _kill_process(proc)
    remove_instance_files(DB, HOME)


@pytest.fixture
def logged_in(page: Page, provider_server: str) -> Page:
    page.goto(f"{provider_server}/login")
    page.fill("#username", USERNAME)
    page.fill("#password", PASSWORD)
    page.click("button.login-button")
    page.wait_for_url(f"{provider_server}/", timeout=10000)
    return page


def _open_tab(page: Page, provider: str, card_selector: str, count: int) -> None:
    # The first poll runs at startup; reload until its snapshot is stored.
    for _ in range(10):
        page.goto(f"{BASE}/?provider={provider}")
        try:
            page.wait_for_function(
                f"document.querySelectorAll('{card_selector}').length === {count}",
                timeout=3000,
            )
            return
        except Exception:
            page.wait_for_timeout(1000)
    raise AssertionError(f"{provider}: {count} cards never rendered")


def _chart_labels(page: Page) -> list:
    page.wait_for_function("State.chart && State.chart.data.datasets.length > 0", timeout=10000)
    return page.evaluate("State.chart.data.datasets.map(d => d.label)")


def _logging_first_row(page: Page) -> str:
    page.locator(".cycles-section").scroll_into_view_if_needed()
    row = page.locator("#cycles-tbody tr").first
    expect(row).not_to_contain_text("No logging data", timeout=10000)
    return row.inner_text()


class TestOpenCodeSessionCookie:
    def test_cards_show_dollar_meters(self, logged_in: Page) -> None:
        _open_tab(logged_in, "opencode", "#quota-grid-opencode .opencode-card", 3)
        expect(logged_in.locator("#fraction-opencode-weekly")).to_have_text("$3.84 / $30.00")
        expect(logged_in.locator("#percent-opencode-weekly")).to_have_text("12.8%")
        expect(logged_in.locator("#fraction-opencode-monthly")).to_have_text("$3.84 / $60.00")
        # The mock only answers with __Host-console_session + x-org-id.
        assert _mock_counts()["opencode"] >= 1


class TestBalanceTabs:
    def test_deepseek_tab_renders_balances(self, logged_in: Page) -> None:
        _open_tab(logged_in, "deepseek", "#quota-grid-deepseek .balance-card", 3)
        amounts = logged_in.locator("#quota-grid-deepseek .usage-percent").all_inner_texts()
        assert amounts == ["$4.02", "$0.32", "$3.70"]
        labels = _chart_labels(logged_in)
        assert labels == ["Total Balance", "Granted", "Topped Up"], "DeepSeek chart fell back to another provider"
        assert "$4.02" in _logging_first_row(logged_in)

    def test_moonshot_tab_renders_balances(self, logged_in: Page) -> None:
        _open_tab(logged_in, "moonshot", "#quota-grid-moonshot .balance-card", 3)
        amounts = logged_in.locator("#quota-grid-moonshot .usage-percent").all_inner_texts()
        assert amounts == ["19.47", "5.00", "14.47"]
        labels = _chart_labels(logged_in)
        assert labels == ["Available", "Voucher", "Cash"], "Moonshot chart fell back to another provider"
        assert "19.47" in _logging_first_row(logged_in)
