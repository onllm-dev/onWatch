# Kimi Code Setup

onWatch tracks **Kimi Code** (the coding agent OAuth product) quotas via:

```http
GET https://api.kimi.com/coding/v1/usages
Authorization: Bearer <access_token>
```

This is **not** the Moonshot Open Platform pay-as-you-go balance API (`api.moonshot.ai` / `api.moonshot.cn`).

## Prerequisites

1. Install and log in with either CLI (both use the same Kimi Code OAuth + `/usages` API):
   - **kimi-code** (current): [docs](https://moonshotai.github.io/kimi-code/) — `kimi login`
   - **kimi-cli** (legacy, still usable): share dir `~/.kimi` (or `$KIMI_SHARE_DIR`)
2. Credentials file (same JSON shape) is searched in order:
   - `$KIMI_CODE_CREDENTIALS` or `$KIMI_CREDENTIALS` (explicit file)
   - `$KIMI_CODE_HOME/credentials/kimi-code.json`
   - `$KIMI_SHARE_DIR/credentials/kimi-code.json` (kimi-cli override)
   - `$KIMI_HOME/credentials/kimi-code.json`
   - `~/.kimi-code/credentials/kimi-code.json` (**kimi-code**)
   - `~/.kimi/credentials/kimi-code.json` (**kimi-cli**)

**When both CLIs have credentials, onWatch uses only kimi-code** (`~/.kimi-code` / `$KIMI_CODE_*`). kimi-cli is a fallback only if no kimi-code file exists (there is a single dashboard tab, so dual-store merge is not useful).

### Token refresh policy

- If the selected store’s **access token is still valid** (`expires_at` with a 60s skew), onWatch **never** refreshes — it just reuses the token the CLI already wrote.
- Refresh runs only when access is **expired** (or missing expiry left us with a dead token path). Then onWatch may call:

```http
POST https://auth.kimi.com/api/oauth/token
grant_type=refresh_token&refresh_token=...&client_id=17e5f671-d194-4dfb-9706-5516cb48c098
```

and rewrite **that same credentials file** (mode `0600`). It does not walk the other CLI directory.

- On HTTP 401 with a still-unexpired access token, onWatch re-reads disk once (CLI may have rotated tokens) but **does not** force-refresh.

## Enable

Auto-detect is on by default when credentials exist:

```bash
# optional explicit enable
KIMI_CODE_ENABLED=true

# optional disable
KIMI_CODE_ENABLED=false
```

Docker / CI without local files:

```bash
KIMI_TOKEN=<access_token>
# or
KIMI_CODE_TOKEN=<access_token>
```

For long-running daemons, prefer mounting the kimi-code credentials file:

```bash
KIMI_CODE_CREDENTIALS=/path/to/kimi-code.json
```

## What is tracked

Dashboard quota cards (same rate-limit surface as Code CLI):

| Card | Source | Meaning |
|------|--------|---------|
| **7-day** | `usage` | 7-day utilization (`used/limit`). Product UI may show one decimal place; the API usually returns integer percents. |
| **5-hour** | `limits[]` with `duration=300` + `TIME_UNIT_MINUTE` | Rolling 5-hour window |

Insights also shows **Membership** plan name from `user.membership.level`:

| API level | Display name |
|-----------|--------------|
| `LEVEL_FREE` | Free |
| `LEVEL_BASIC` | Adagio |
| `LEVEL_STANDARD` | Moderato |
| `LEVEL_INTERMEDIATE` | Allegretto |
| `LEVEL_ADVANCED` | Allegro |
| `LEVEL_PREMIUM` | Vivace |

Other `/usages` fields (`totalQuota`, non-5h windows) are ignored. The membership site “total usage” bar (e.g. on [My Quota](https://www.kimi.com/membership/subscription?tab=quota)) comes from a separate web API (`GetSubscriptionStats`) and is **not** tracked.

### Timezones

`resetTime` values are UTC. The dashboard formats them in your configured timezone (Settings). Example: `2026-07-14T16:13:41Z` → `2026-07-15 00:13` in Asia/Shanghai.

## Verify

```bash
# slash command in legacy kimi-cli (same API)
kimi-cli  # then /usage

# or curl after refreshing a token
curl -sS -H "Authorization: Bearer $TOKEN" https://api.kimi.com/coding/v1/usages | jq .
```

Restart onWatch and open the **Kimi Code** dashboard tab.
