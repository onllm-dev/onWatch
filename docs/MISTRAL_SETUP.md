# Mistral Setup Guide

Track your Mistral subscription allowance (Included API + Included Vibe Code) and pay-as-you-go spend in onWatch.

Mistral has no public usage API, so onWatch reads your usage the way the browser does: it imports your signed-in session cookie and reads your subscription page directly. That's different from most other providers here, which just need an API key - Mistral needs a browser session instead.

## Enable tracking

1. Sign in at https://admin.mistral.ai/subscription in a supported browser.
2. In onWatch, go to Settings > Providers > Mistral and turn it on.
3. Pick an authentication method (below) and restart onWatch.

### Automatic browser import (recommended)

onWatch reads your session cookie straight from Chrome, Firefox, or another supported browser - no copy/pasting, and it re-imports automatically as your session refreshes. Set `MISTRAL_BROWSER` to your browser if it doesn't detect one automatically.

### Manual cookie

Settings > Providers > Mistral > Manual, then paste your Mistral Cookie header. No browser or keychain access needed - useful for Docker, headless servers, or if automatic import doesn't work for you. You'll need to re-paste it when the session expires. Never commit a cookie header to source control.

## macOS permissions

Automatic import needs two one-time permissions on macOS:

1. **Browser folder access.** macOS blocks background apps from reading another app's data folder. Right-click the onWatch tray icon and choose **Grant Browser Access...**, then pick your browser in the folder panel. A locally rebuilt binary will need re-granting.
2. **Keychain access.** You'll see a prompt that `security` wants to use **Chrome Safe Storage** - this decrypts the cookie file, it's not asking for your Mistral password. Enter your Mac login password and choose **Allow** (or **Allow Once** if you'd rather be asked again next time).

Prefer to skip both prompts? Sign into Mistral in Firefox instead and select Firefox as your browser, or use manual cookie mode.

## Other platforms

- **Linux:** Chromium import needs an unlocked desktop keyring (D-Bus/GNOME/KWallet), which headless services usually don't have. Use Firefox or manual cookies instead.
- **Windows:** Chromium cookies are supported via DPAPI; app-bound encryption on newer Chrome builds can block this. Use Firefox or manual cookies if it fails.
- **Safari:** may require Full Disk Access, or point `MISTRAL_BROWSER_PROFILE` at a specific `Cookies.binarycookies` file.
- **Firefox containers:** append `::container=<id>` to `MISTRAL_BROWSER_PROFILE` to pick a specific container.
- **Docker:** use manual cookies (`MISTRAL_AUTH_COOKIE`) rather than mounting a browser profile into the container.

## Configuration reference

```dotenv
MISTRAL_ENABLED=true
MISTRAL_BROWSER=chrome
# Optional profile directory/name; Firefox containers use ::container=ID
MISTRAL_BROWSER_PROFILE=Default
# Manual mode can instead supply MISTRAL_AUTH_COOKIE privately.
# How long stored Mistral history is kept; 0 disables pruning.
MISTRAL_RETENTION=2160h
```

Settings saved in onWatch override these environment variables. The cookie field is masked in settings responses.

## What's tracked

**Included API** and **Included Vibe Code** are tracked as separate allowances, each with currency, used amount, limit, remaining amount, percentage, and reset date.

**Pay-as-you-go spend** is tracked separately from your subscription allowances. If you're not on PAYG, hide it from the dashboard: Settings > Providers > Mistral > **Show pay-as-you-go spend** > **Hide**.

## Refresh behaviour

onWatch polls every 120 seconds by default. If your session is rejected, it retries the import once immediately; after that it pauses polling until a fresh login is detected, without switching to a different account on its own. Usage history is kept for 90 days by default - change this with `MISTRAL_RETENTION` (a Go duration like `720h`, or `0` to keep everything).

## Testing

Opt-in live check against your own signed-in session (may prompt for browser/keychain access):

```sh
ONWATCH_MISTRAL_LIVE=1 GOFLAGS='-run=TestMistralLive -v' ./app.sh --test
```

The default test suite uses synthetic data only. A synthetic dashboard preview is also available with `ONWATCH_MISTRAL_PREVIEW=1 GOFLAGS='-run=TestMistralPreview -v' ./app.sh --test`.
