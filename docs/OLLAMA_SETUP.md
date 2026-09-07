# Ollama Cloud Setup Guide

Track your **Ollama Cloud** included monthly usage in onWatch.

> **Beta provider.** The ollama.com usage API is undocumented, and two details are still being confirmed with users on [issue #86](https://github.com/onllm-dev/onWatch/issues/86): the Free plan's included allowance (the page shows only a percent) and the dollar unit on paid plans. If your card shows "$X used" without a cap, set `OLLAMA_MONTHLY_LIMIT`. Reports of what your ollama.com settings page shows next to onWatch's value are very welcome there.

Ollama Cloud bills against an included monthly usage allowance measured in US dollars. onWatch reads that usage from the authenticated ollama.com API with an API key you create at https://ollama.com/settings/keys.

---

## Prerequisites

- An [Ollama Cloud](https://ollama.com) account (Free, Pro, Max, or Team)
- An API key from https://ollama.com/settings/keys
- onWatch installed ([Quick Start](../README.md#quick-start))

---

## How It Works

onWatch polls two authenticated JSON endpoints on ollama.com using your API key as a `Bearer` token:

```text
GET  https://ollama.com/api/usage
POST https://ollama.com/api/me
```

- `GET /api/usage` returns the included monthly usage in USD (`limits.monthly.usage`), per-model request counts for the current month, and `activity.cost` - the extra pay-as-you-go spend over the last four weeks that is charged on top of the included allowance.
- `POST /api/me` returns your plan tier, name, email, and account creation time.

Neither endpoint returns the monthly dollar cap or the reset time, so onWatch derives both:

- **Cap** - taken from your plan tier (see the table below), or from `OLLAMA_MONTHLY_LIMIT` if you set it. On the Free plan the cap is unknown (its "starter usage" is not published), so the card shows dollars used without a percentage unless you set `OLLAMA_MONTHLY_LIMIT`.
- **Reset** - anchored to your account creation day of month at first. Ollama Cloud resets the Free allowance monthly from signup; paid plans reset on the subscription start day. onWatch learns the real day on its own: the first time included usage drops between two polls, that moment is recorded as the reset anchor and used from then on (it survives restarts). Set `OLLAMA_RESET_DAY` only if you want to pin the day explicitly; it disables learning.

Both web search and cloud model calls draw from the same included usage. Snapshots are stored locally in SQLite like every other provider.

---

## 1. Create an API Key

1. Sign in at https://ollama.com and open https://ollama.com/settings/keys
2. Create a new API key
3. Copy the key value

API keys do not expire, but they can be revoked from the same page. Treat the key like a password.

---

## 2. Configure onWatch

Use any one of the following.

### Option A - `.env`

Add the key to `~/.onwatch/.env` (or your project `.env`):

```bash
OLLAMA_API_KEY=your_ollama_cloud_api_key
```

### Option B - `onwatch setup`

The wizard checks the key against ollama.com before saving it and shows your plan tier. A rejected key is re-prompted; a network failure keeps the key.

Run the interactive wizard and choose **Ollama Cloud only** (or add it under **Multiple** / **All available**):

```bash
onwatch setup
```

### Option C - Dashboard

1. Open **Settings → Providers → Ollama Cloud**
2. Paste your **API Key**
3. Save

The provider is opt-in: it only activates once `OLLAMA_API_KEY` is set. Restart onWatch after adding the key:

```bash
onwatch stop
onwatch
```

Or verify in the foreground:

```bash
onwatch --debug
```

You should see `Ollama API client configured` and the Ollama agent start.

---

## 3. Optional Overrides

These are only needed when the derived values are wrong for your account.

```bash
# Included usage cap in USD (0 = derive from plan)
OLLAMA_MONTHLY_LIMIT=60

# Reset day of month, 1-31 (0 = account anniversary)
OLLAMA_RESET_DAY=15
```

Plan-derived caps (from https://ollama.com/pricing):

| Plan | Included monthly usage (USD) |
|------|------------------------------|
| Free | unknown (unpublished "starter usage") |
| Pro  | 60 |
| Max  | 300 |
| Team | 1,000 |

- Set `OLLAMA_MONTHLY_LIMIT` on the Free plan to get a percentage on the card, or to override the plan default.
- Set `OLLAMA_RESET_DAY` only to pin the reset day explicitly. Without it onWatch starts from your account anniversary and corrects itself after the first observed reset.

---

## Dashboard

The Ollama Cloud tab shows:

- **Monthly Included Usage** card - dollars used against the resolved cap, with utilization percent and reset countdown (percent is hidden when the cap is unknown)
- **Per-model requests** - request counts for each model used this month
- **Extra usage** - pay-as-you-go spend (`activity.cost`) charged beyond the included allowance over the last four weeks
- Historical chart, billing-cycle history, and burn-rate insights

---

## Security Notes

- The API key is used only as a `Bearer` token against `ollama.com` and is **never logged** (redacted in debug output)
- Never commit `.env` or paste the key into issue reports / logs
- All processing stays local on your machine

---

## Troubleshooting

### 401 Unauthorized

The key is invalid or was revoked. Create a fresh key at https://ollama.com/settings/keys and update `OLLAMA_API_KEY`, then restart onWatch.

### Free plan shows no percentage

The Free plan cap is not published, so onWatch shows dollars used without a percentage. Set `OLLAMA_MONTHLY_LIMIT` to your known allowance to get a percentage and projections.

### Reset day looks wrong

Until the first real reset has been observed, the date is a guess anchored to your account creation day of month. It corrects itself automatically once included usage drops at the true reset. To fix it immediately, set `OLLAMA_RESET_DAY` to the correct day (1-31) and restart onWatch.

---

## Related

- Main README environment variable reference
