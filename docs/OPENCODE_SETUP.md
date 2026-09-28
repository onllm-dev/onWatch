# OpenCode Go Setup Guide

Track OpenCode Go subscription quotas in onWatch.

OpenCode Go has no documented quota API. onWatch reads the subscription's own meters from the console endpoint `GET /console/api/go/status`, and it can authenticate in either of two ways:

- **Service-account key (recommended).** A console key with usage read access. No browser cookie, and it does not expire when you log out.
- **Browser session.** Your workspace ID plus the `__Host-console_session` cookie from opencode.ai. Used only when no key is set.

This is separate from `OPENCODE_ENABLED`, which only feeds ChatGPT credentials from OpenCode into the **Codex** provider.

> **Upgrading from the old scrape mode?** The `/workspace/{id}/go` page and the `auth` cookie no longer work ([issue #134](https://github.com/onllm-dev/onWatch/issues/134)). Keep your workspace ID, but replace `OPENCODE_GO_AUTH_COOKIE` with the value of the `__Host-console_session` cookie, or switch to a service-account key.

---

## What you see

`go/status` returns three meters, each with a used and a limit amount. They are the figures OpenCode enforces and shows on the Go page. onWatch shows them as the **5-Hour**, **Weekly** and **Monthly** cards, with the percentage and the dollar amount (for example `$3.84 / $30.00`). Nothing is estimated, and there is no price table to keep in sync.

- **5-Hour** is a session window. While a session is open it carries its reset time. With no open session it shows 0% and no reset time.
- **Weekly** resets at the time the API reports.
- **Monthly** renews with the subscription period. Its reset is the period end the API reports, so no reset day needs to be configured.

Amounts are stored in USD. Snapshots taken before this change were stored as percentages (limit 100), so the logging history shows a short window of mixed units after upgrading. Charts and cycle history use the percentage and are unaffected. If you set an **absolute** notification threshold for an OpenCode quota, it is now compared in dollars; percentage thresholds are unchanged.

onWatch reuses a status response for 60 seconds, so a poll interval shorter than that does not add requests.

---

## Option 1: Service-account key (recommended)

1. In the OpenCode console, open **Keys -> Add Service Account** and create a key with **usage read** access. Keys look like `oc_sk_...`.
2. Set it (or paste it into **Settings -> Providers -> OpenCode Go -> Usage API Key**):

   ```bash
   OPENCODE_GO_API_KEY=oc_sk_...
   ```

The meters belong to the Go subscription the key resolves to. To track a different subscription, use a key from that subscription's workspace.

---

## Option 2: Browser session

### 1. Copy the session cookie

1. Sign in at https://opencode.ai and open the console
2. Open browser Developer Tools -> **Application** / **Storage** -> **Cookies** -> `https://opencode.ai`
3. Find the `__Host-console_session` cookie and copy its **value**

You can paste just the value, `__Host-console_session=<value>`, or a full `Cookie:` header copied from DevTools. Treat it like a password: logging out, rotating sessions, or clearing cookies invalidates it.

### 2. Find your workspace ID

With Developer Tools open on the **Network** tab, open your Go usage page in the console. Select the `status` request to `/console/api/go/status` and copy the value of its `x-org-id` request header. It looks like `wrk_...`.

### 3. Configure onWatch

Add both values to `~/.onwatch/.env` (or your project `.env`):

```bash
OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxx
OPENCODE_GO_AUTH_COOKIE=your_console_session_value
```

Both are required. Without them (and without `OPENCODE_GO_API_KEY`) the OpenCode Go provider stays disabled.

You can also set them in **Settings -> Providers -> OpenCode Go** (**Workspace ID** and **Session Cookie**).

---

## Reload / Restart

Settings changes take effect after a daemon restart:

```bash
onwatch stop
onwatch
```

Or verify in the foreground with `onwatch --debug`. You should see `OpenCode poll complete` with `quota_count=3`.

Then open http://localhost:9211, switch to the **OpenCode** tab, and confirm the 5-Hour, Weekly and Monthly cards populate. Charts, cycle overview and insights fill in after a few polls.

---

## Security Notes

- Never commit `.env` or paste the key or cookie into issue reports / logs
- onWatch redacts `api_key` and `auth_cookie` from `/api/settings` responses
- `go/status` responses are never written to logs or echoed in errors (they contain account IDs)
- The session cookie is only sent to `opencode.ai`, and redirects are never followed
- All processing stays local on your machine

---

## Limitations & Notes

- `go/status` is undocumented. If OpenCode changes its shape, onWatch reports a parse failure rather than showing wrong numbers.
- Session cookie lifetime is controlled by OpenCode. Expect to refresh it after logout or session rotation; a service-account key avoids this.
- The workspace ID is required in session mode; onWatch does not auto-discover workspaces.
- The console usage export (`/console/api/v1/usage/export`) is not used: its Go rows carry no cost, and since 25 September 2026 it answers 403 to service-account keys while OpenCode migrates its usage records. See [anomalyco/opencode#50912](https://github.com/anomalyco/opencode/issues/50912), which also describes `go/status`.

---

## Troubleshooting

### No OpenCode tab

- Confirm `OPENCODE_GO_API_KEY` is set, or both `OPENCODE_GO_WORKSPACE_ID` and `OPENCODE_GO_AUTH_COOKIE`
- Restart onWatch and check `--debug` logs for missing-config messages

### Unauthorized / forbidden

- **Key:** check the service-account key still exists and has usage read access. A 403 on a key that used to work usually means OpenCode changed what service-account keys may read.
- **Session:** the log says `paste the __Host-console_session cookie`. Re-copy a fresh `__Host-console_session` value while signed in; the old `auth` cookie is rejected. A `400` / invalid response usually means the workspace ID is wrong.
- A 429 means the API is rate limiting; onWatch skips that poll and tries again on the next one.

### Parse failed / response format changed

OpenCode likely changed the `go/status` response. File an issue with:

- Approximate time of failure
- Whether the browser console still shows 5h / weekly / monthly usage
- **Do not** attach keys, cookies or raw `go/status` responses (they contain account IDs)

### Docker / headless

Pass the env vars into the container. There is no local credential auto-detection for OpenCode Go.

```bash
OPENCODE_GO_API_KEY=oc_sk_...
# or, browser session:
OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxx
OPENCODE_GO_AUTH_COOKIE=your_console_session_value
```

---

## Related

- Main README environment variable reference
- Codex + OpenCode ChatGPT auth (`OPENCODE_ENABLED`) is documented in [CODEX_SETUP.md](CODEX_SETUP.md)
