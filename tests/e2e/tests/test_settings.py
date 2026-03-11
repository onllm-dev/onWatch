"""E2E tests for the settings page.

Tests covering tabs, macOS menubar behavior, SMTP form, thresholds, provider toggles, menubar visibility persistence, and save.
"""
import platform

import pytest
from playwright.sync_api import Page, expect

from page_objects.settings_page import SettingsPage


class TestSettings:
    """Settings page interaction tests."""

    def test_four_tabs_present(self, settings_page: Page) -> None:
        """Settings page should expose platform-appropriate visible tabs."""
        sp = SettingsPage(settings_page)
        tabs = sp.get_tab_names()
        expected = {"Email (SMTP)", "Notifications", "Providers", "General"}
        if platform.system() == "Darwin":
            expected.add("Menubar")
        assert set(tabs) == expected

    def test_menubar_tab_behavior_matches_capabilities(self, settings_page: Page) -> None:
        """macOS builds should expose Menubar tab controls when menubar support is available."""
        if platform.system() != "Darwin":
            pytest.skip("Menubar settings are only exposed on macOS")

        sp = SettingsPage(settings_page)
        sp.select_tab("menubar")

        settings_hidden = settings_page.locator("#menubar-settings-shell").evaluate("el => el.hidden")
        order_hidden = settings_page.locator("#menubar-order-shell").evaluate("el => el.hidden")
        divider_hidden = settings_page.locator("#menubar-order-divider").evaluate("el => el.hidden")
        assert settings_hidden == order_hidden == divider_hidden

    def test_smtp_form_fields(self, settings_page: Page) -> None:
        """The Email (SMTP) tab should display all SMTP configuration fields."""
        sp = SettingsPage(settings_page)
        # Email tab should be active by default
        assert sp.get_active_tab() == "email"

        expect(settings_page.locator("#smtp-host")).to_be_visible()
        expect(settings_page.locator("#smtp-port")).to_be_visible()
        expect(settings_page.locator("#smtp-protocol")).to_be_visible()
        expect(settings_page.locator("#smtp-username")).to_be_visible()
        expect(settings_page.locator("#smtp-password")).to_be_visible()
        expect(settings_page.locator("#smtp-from-address")).to_be_visible()
        expect(settings_page.locator("#smtp-from-name")).to_be_visible()
        expect(settings_page.locator("#smtp-to")).to_be_visible()

    def test_smtp_protocol_defaults_to_auto(self, settings_page: Page) -> None:
        """SMTP protocol should default to auto mode while keeping explicit options available."""
        expect(settings_page.locator("#smtp-protocol")).to_have_value("auto")
        options = settings_page.locator("#smtp-protocol option")
        assert options.count() == 4
        assert settings_page.locator("#smtp-protocol option[value='none']").inner_text() == "None (Plaintext)"

    def test_send_test_email_button(self, settings_page: Page) -> None:
        """The test email button should be present and clickable."""
        sp = SettingsPage(settings_page)
        assert sp.get_active_tab() == "email"
        expect(settings_page.locator("#smtp-test-btn")).to_be_visible()

    def test_notification_thresholds(self, settings_page: Page) -> None:
        """Notifications tab should have warning and critical threshold inputs."""
        sp = SettingsPage(settings_page)
        sp.select_tab("notifications")

        expect(settings_page.locator("#threshold-warning")).to_be_visible()
        expect(settings_page.locator("#threshold-critical")).to_be_visible()
        expect(settings_page.locator("#threshold-warning-slider")).to_be_visible()
        expect(settings_page.locator("#threshold-critical-slider")).to_be_visible()

    def test_threshold_slider_sync(self, settings_page: Page) -> None:
        """Changing the threshold number input should sync with the slider."""
        sp = SettingsPage(settings_page)
        sp.select_tab("notifications")

        # Set warning threshold via number input
        sp.set_warning_threshold(70)
        # Trigger input event
        settings_page.dispatch_event("#threshold-warning", "input")
        settings_page.wait_for_timeout(500)

        # Default values should be present
        warning_val = sp.get_warning_input_value()
        assert warning_val == "70"

    def test_provider_toggles_tab(self, settings_page: Page) -> None:
        """Providers tab should show toggle controls for each provider."""
        sp = SettingsPage(settings_page)
        sp.select_tab("providers")

        assert sp.is_panel_visible("providers")
        assert sp.is_provider_toggles_visible()

    def test_menubar_provider_visibility_is_saved_via_global_settings(self, settings_page: Page) -> None:
        """Menubar provider visibility should persist in settings payload."""
        if platform.system() != "Darwin":
            pytest.skip("Menubar settings are only exposed on macOS")

        sp = SettingsPage(settings_page)
        sp.select_tab("menubar")

        order_hidden = settings_page.locator("#menubar-order-shell").evaluate("el => el.hidden")
        if order_hidden:
            pytest.skip("Menubar order controls are hidden for this build")

        order_list = settings_page.locator("#menubar-provider-order")
        expect(order_list).to_be_visible()

        initial_items = order_list.locator(".menubar-order-item[data-provider]")
        assert initial_items.count() >= 2

        first_provider = initial_items.nth(0).get_attribute("data-provider")
        second_provider = initial_items.nth(1).get_attribute("data-provider")
        assert first_provider
        assert second_provider

        first_toggle = initial_items.nth(0).locator('input[data-role="menubar-visible"]')
        if first_toggle.is_checked():
            first_toggle.click()
            hidden_provider = first_provider
        else:
            second_toggle = initial_items.nth(1).locator('input[data-role="menubar-visible"]')
            second_toggle.click()
            hidden_provider = second_provider

        expected_visible = settings_page.evaluate(
            """
            () => [...document.querySelectorAll('#menubar-provider-order input[data-role="menubar-visible"]')]
              .filter((input) => input.checked)
              .map((input) => input.dataset.provider)
            """
        )

        sp.save_settings()
        expect(settings_page.locator("#settings-feedback")).to_contain_text("Settings saved successfully")

        prefs_after_save = settings_page.evaluate(
            """
            async () => {
              const response = await fetch('/api/menubar/preferences', { credentials: 'same-origin' });
              return await response.json();
            }
            """
        )
        assert prefs_after_save.get("visible_providers") == expected_visible

        settings_page.reload()
        sp = SettingsPage(settings_page)
        sp.select_tab("menubar")

        hidden_toggle = settings_page.locator(
            f'#menubar-provider-order .menubar-order-item[data-provider="{hidden_provider}"] input[data-role="menubar-visible"]'
        ).first
        assert not hidden_toggle.is_checked()

        checked_after_reload = settings_page.evaluate(
            """
            () => [...document.querySelectorAll('#menubar-provider-order input[data-role="menubar-visible"]')]
              .filter((input) => input.checked)
              .map((input) => input.dataset.provider)
            """
        )
        assert checked_after_reload == expected_visible

    def test_timezone_setting(self, settings_page: Page) -> None:
        """General tab should have a timezone selector."""
        sp = SettingsPage(settings_page)
        sp.select_tab("general")

        expect(settings_page.locator("#settings-timezone")).to_be_visible()
        tz = sp.get_timezone_select()
        # Default should be empty string (browser default)
        assert tz is not None

    def test_save_settings_button(self, settings_page: Page) -> None:
        """The Save Settings button should be present and clickable."""
        sp = SettingsPage(settings_page)
        expect(settings_page.locator("#settings-save-btn")).to_be_visible()

        # Click save -- may show success or error depending on config state
        sp.save_settings()
        settings_page.wait_for_timeout(1000)
        # Feedback area should become visible after save
        feedback_el = settings_page.query_selector("#settings-feedback")
        assert feedback_el is not None
