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
    # Kept for failure messages: a render that never happens is otherwise
    # impossible to diagnose from CI output.
    page.console_errors = []
    page.on("pageerror", lambda e: page.console_errors.append(f"pageerror: {e}"))
    page.on("console", lambda m: m.type == "error" and page.console_errors.append(f"console: {m.text}"))
    page.goto(f"{provider_server}/login")
    page.fill("#username", USERNAME)
    page.fill("#password", PASSWORD)
    page.click("button.login-button")
    page.wait_for_url(f"{provider_server}/", timeout=10000)
    return page


def _current(page: Page, provider: str) -> str:
    """The provider's /api/current body, for failure messages."""
    return page.evaluate(
        "async (p) => JSON.stringify(await (await fetch(`/api/current?provider=${p}`)).json())",
        provider,
    )


def _open_tab(page: Page, provider: str, card_selector: str, count: int) -> None:
    # The first poll runs at startup and then every 10s; reload until the
    # stored snapshot renders the cards.
    last_error = None
    for _ in range(12):
        page.goto(f"{BASE}/?provider={provider}")
        try:
            # Functions, not bare expressions: the dashboard's CSP forbids
            # unsafe-eval, which Playwright needs for an expression string.
            page.wait_for_function(
                "([sel, n]) => document.querySelectorAll(sel).length === n",
                arg=[card_selector, count],
                timeout=4000,
            )
            return
        except Exception as e:  # timeout or a navigation mid-wait; retry
            last_error = e
            page.wait_for_timeout(1000)
    grid = page.evaluate(
        "(id) => { const g = document.getElementById(id); return g ? g.outerHTML.slice(0, 400) : 'missing'; }",
        f"quota-grid-{provider}",
    )
    raise AssertionError(
        f"{provider}: {count} cards never rendered ({str(last_error)[:300]}); url={page.url}; grid={grid}; "
        f"errors={getattr(page, 'console_errors', [])}; mock requests={_mock_counts()}; "
        f"/api/current={_current(page, provider)}"
    )


def _chart_labels(page: Page) -> list:
    page.wait_for_function("() => State.chart && State.chart.data.datasets.length > 0", timeout=10000)
    return page.evaluate("() => State.chart.data.datasets.map(d => d.label)")


def _expect_logging_row(page: Page, text: str) -> None:
    # The table loads when scrolled into view; until then it shows the
    # template's placeholder row, so wait for the value itself.
    page.locator(".cycles-section").scroll_into_view_if_needed()
    expect(page.locator("#cycles-tbody tr").first).to_contain_text(text, timeout=15000)


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
        _expect_logging_row(logged_in, "$4.02")

    def test_moonshot_tab_renders_balances(self, logged_in: Page) -> None:
        _open_tab(logged_in, "moonshot", "#quota-grid-moonshot .balance-card", 3)
        amounts = logged_in.locator("#quota-grid-moonshot .usage-percent").all_inner_texts()
        assert amounts == ["19.47", "5.00", "14.47"]
        labels = _chart_labels(logged_in)
        assert labels == ["Available", "Voucher", "Cash"], "Moonshot chart fell back to another provider"
        _expect_logging_row(logged_in, "19.47")
