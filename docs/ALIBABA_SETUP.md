# Alibaba Token Plan Setup Guide

This guide configures usage tracking for the Alibaba Cloud Model Studio **Token Plan (Personal)** in onWatch. It tracks the 5-hour, weekly and monthly usage windows, their reset times, and the plan's status and remaining days.

## How it works

Alibaba has no public API-key based quota endpoint for the Token Plan. onWatch reads the same read-only console endpoints that the Model Studio web page uses, authenticated with your **browser session cookies**:

1. `GET` the console page for your region to read the `SEC_TOKEN` CSRF token
2. Two read-only gateway calls: usage (per-window fractions plus reset times) and subscription (plan, status, remaining days)
3. One optional billing call to resolve the plan instance id

Polling spends no quota. Cookies and the `SEC_TOKEN` are never logged, and the dashboard never returns saved cookies (it only reports whether they are set).

## Prerequisites

- An active Token Plan (Personal) subscription on the Alibaba Cloud Model Studio international console
- Access to the console in a browser where you are logged in
- onWatch with the Alibaba provider (this branch or later)

## 1. Export your cookies

Log in at https://modelstudio.console.alibabacloud.com, open the Token Plan page, then export the session cookies using either method:

**Cookie header (quickest)**

1. Open the browser developer tools -> **Network** tab
2. Reload the Token Plan page and click the request for the page itself
3. Copy the `Cookie` request header value (`name=value; name2=value2; ...`)

**Netscape `cookies.txt`**

1. Use a cookie-export browser extension (any "cookies.txt" exporter) on the console domain
2. Save the export as a file, for example `~/.onwatch/alibaba-cookies.txt`
3. `#HttpOnly_` lines are supported, so HttpOnly session cookies are included

Both formats are accepted everywhere onWatch takes cookies. A leading `Cookie:` prefix and line breaks are stripped.

Prefer the Netscape export when you can: onWatch sends only the cookies that match each request host. A single Cookie header pasted from the console page works for that page, but the usage gateway lives on another host (`bailian-singapore-cs.alibabacloud.com`), and a jar that still carries host-only console cookies can be rejected by Alibaba's WAF.

Prefer the Netscape export when you can: onWatch sends only the cookies that match each request host. A single Cookie header pasted from the console page works for that page, but the usage gateway lives on another host (`bailian-singapore-cs.alibabacloud.com`), and a jar that still carries host-only console cookies can be rejected by Alibaba's WAF.

## 2. Configure onWatch

Add one of these to your environment file (`~/.onwatch/.env` for local installs):

```env
# Option A: inline (Cookie header or Netscape text)
ALIBABA_COOKIES=login_aliyunid_ticket=...; ...

# Option B: a file, read at startup
ALIBABA_COOKIES_FILE=/home/you/.onwatch/alibaba-cookies.txt
```

Optional settings:

```env
ALIBABA_REGION=ap-southeast-1   # console region (default)
ALIBABA_ENABLED=true            # enable with no env cookies (add accounts in the dashboard)
# ALIBABA_ENABLED=false         # disable even when cookies are set
```

| Variable | Description |
|----------|-------------|
| `ALIBABA_COOKIES` | Cookie header string or Netscape `cookies.txt` text |
| `ALIBABA_COOKIES_FILE` | Path to a cookie export, read at startup |
| `ALIBABA_REGION` | Console region used in the page URL (default `ap-southeast-1`) |
| `ALIBABA_ENABLED` | `true` enables the provider without cookies; `false` disables it |

The cookies seed the account named `default`, but only when that account has no cookies yet. Cookies you saved from the dashboard are never overwritten by the environment.

Then restart onWatch:

```bash
onwatch stop
onwatch
```

## 3. Multiple accounts

Every Alibaba account is polled independently and has its own quota history, cycles and insights.

1. Open **Settings -> Providers -> Alibaba Token Plan**
2. Add an account: a name (letters, numbers, `-`, `_`), the cookies, and optionally a region
3. onWatch starts polling the new account immediately (no restart)

The same operations are available over the API:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/alibaba/accounts` | GET | List accounts (cookies are never returned, only `hasCookies`) |
| `/api/alibaba/accounts` | POST | Create: `{"name","cookies","region"}` |
| `/api/alibaba/accounts?id=N` | PUT | Update `name`, `cookies`, `region`, or `restore` a deleted account |
| `/api/alibaba/accounts?id=N` | DELETE | Soft-delete an account |
| `/api/alibaba/accounts/usage` | GET | Current usage for every active account |
| `/api/alibaba/current?account=N` | GET | Current quotas for one account |
| `/api/alibaba/history?account=N&range=7d` | GET | Utilization history |
| `/api/alibaba/cycles?account=N&type=five_hour` | GET | Reset cycles for a window |
| `/api/alibaba/insights?account=N` | GET | Burn-rate insights |

`account` defaults to the first active account that has cookies.

Accounts that exist only in the database also start polling at startup, even with no `ALIBABA_*` variables set. A fresh install with no cookies anywhere should set `ALIBABA_ENABLED=true` once so the provider is available to add accounts to.

## 4. Verify

- Open the dashboard and switch to the **Alibaba** tab
- You should see **5-Hour**, **Weekly** and **Monthly** utilization cards with reset countdowns
- In **Settings -> Providers**, confirm Alibaba shows as configured and polling

## Session expiry

The console session is a normal browser login, so it expires like one (the lifetime is set by Alibaba, and logging out ends it). When it does:

- The poll fails with `unauthorized - session expired or SEC_TOKEN missing`, and the card stops updating (it is marked stale after 30 minutes)
- Re-export the cookies and update them by editing the account in the dashboard

Tips:

- Export cookies right after a fresh login, from the page that loads the Token Plan
- Do not log out of the console in that browser afterwards - logging out ends the session

Note that `ALIBABA_COOKIES` / `ALIBABA_COOKIES_FILE` only fill an account that has no cookies. To replace expired cookies on the `default` account, update them from the dashboard.

## Notes

- Quota windows: `five_hour`, `weekly`, `monthly` (shown as 5-Hour, Weekly, Monthly). The usage endpoint reports a 0-1 fraction, stored as a 0-100 percent.
- Data is stored locally in SQLite (`alibaba_snapshots`, `alibaba_quota_values`, `alibaba_reset_cycles`), keyed by account.
- Notifications use the standard quota thresholds and fire a reset notice when a window rolls over.
- Region only affects the console page URL and gateway parameters. Leave it at the default unless your plan lives in another region.
