"""Codex usage labels, source disclosure, and quota card layout regressions."""
from pathlib import Path

import pytest
from playwright.sync_api import Page, expect

STATIC_DIR = Path(__file__).resolve().parents[3] / "internal" / "web" / "static"
SOURCE_NOTE = "Pro (More) weekly usage comes from Codex's usage API."


@pytest.fixture
def codex_page(page: Page) -> Page:
    # Load the real renderers after DOMContentLoaded, without dashboard polling.
    url = "http://onwatch.test/codex-usage-fixture"
    page.route(url, lambda route: route.fulfill(
        content_type="text/html",
        body='<html><body><main class="main-content">'
             '<div id="all-providers-container"></div>'
             '<div id="quota-grid-codex" class="quota-grid"></div>'
             '</main></body></html>',
    ))
    page.goto(url)
    page.add_style_tag(path=str(STATIC_DIR / "style.css"))
    page.add_script_tag(path=str(STATIC_DIR / "app.js"))
    return page


def _account(account_id: int = 1, plan: str = "pro", note: str = SOURCE_NOTE) -> dict:
    return {
        "accountId": account_id,
        "accountName": f"Account {account_id}",
        "planType": plan,
        "usageSourceNote": note,
        "quotas": [
            {"name": "five_hour", "utilization": 10},
            {"name": "seven_day", "utilization": 87},
            {"name": "code_review", "utilization": 5},
        ],
    }


@pytest.mark.parametrize("plan,label", [
    ("pro", "Pro (More)"), ("prolite", "Pro"), ("promax", "Pro (Max)"),
])
def test_single_codex_account_in_all_view(codex_page: Page, plan: str, label: str) -> None:
    account = _account(plan=plan, note=SOURCE_NOTE if plan == "pro" else "")
    codex_page.evaluate("""account => {
        State.allProvidersCurrent = {codexAccounts: [account]};
        renderAllProvidersView();
    }""", account)
    card = codex_page.locator('.provider-card[data-provider="codex"]')
    expect(card.locator(".provider-card-badge")).to_have_text(label)
    expect(card.locator(".codex-usage-source-note")).to_have_count(1 if plan == "pro" else 0)
    if plan == "pro":
        expect(card.locator(".codex-usage-source-note")).to_have_text(SOURCE_NOTE)
    expect(card).to_contain_text("87.0%")


def test_codex_source_note_is_escaped_in_all_view(codex_page: Page) -> None:
    note = '<script>window.noteInjected = true</script>API <usage>'
    codex_page.evaluate("""account => {
        State.allProvidersCurrent = {codex: account};
        renderAllProvidersView();
    }""", _account(note=note))
    expect(codex_page.locator(".codex-usage-source-note")).to_have_text(note)
    assert codex_page.evaluate("window.noteInjected") is None


def test_multi_account_codex_notes_stay_with_their_account(codex_page: Page) -> None:
    codex_page.evaluate("""accounts => {
        State.allProvidersCurrent = {codexAccounts: accounts};
        renderAllProvidersView();
    }""", [_account(), _account(2, "plus", "")])
    pro = codex_page.locator('.account-overview-card[data-account-id="1"]')
    plus = codex_page.locator('.account-overview-card[data-account-id="2"]')
    expect(pro.locator(".account-overview-badge")).to_have_text("Pro (More)")
    expect(pro.locator(".codex-usage-source-note")).to_have_text(SOURCE_NOTE)
    expect(plus.locator(".codex-usage-source-note")).to_have_count(0)


@pytest.mark.parametrize("width", [375, 900, 1280])
def test_codex_note_preserves_quota_card_layout(codex_page: Page, width: int) -> None:
    codex_page.set_viewport_size({"width": width, "height": 1000})
    codex_page.evaluate("""account => {
        renderCodexQuotaCards(account.quotas, 'quota-grid-codex', account.planType, account.usageSourceNote);
    }""", _account())
    grid = codex_page.locator("#quota-grid-codex")
    expect(grid.locator(".codex-usage-source-note")).to_have_text(SOURCE_NOTE)
    expect(grid.locator(".quota-card")).to_have_count(3)
    expect(grid.locator('[data-quota="seven_day"]')).to_contain_text("87.0%")
    bounds = grid.bounding_box()
    hour = grid.locator('[data-quota="five_hour"]').bounding_box()
    weekly = grid.locator('[data-quota="seven_day"]').bounding_box()
    review = grid.locator('[data-quota="code_review"]').bounding_box()
    assert bounds and hour and weekly and review
    assert abs(hour["width"] - weekly["width"]) < 1
    if width == 900:
        assert abs(review["width"] - bounds["width"]) < 1
        assert review["y"] > weekly["y"]
    else:
        assert abs(review["width"] - hour["width"]) < 1
    assert codex_page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
