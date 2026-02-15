# GitHub Copilot Setup Guide

Track your GitHub Copilot premium request usage with onWatch.

---

## Prerequisites

- GitHub account with an active Copilot subscription (Individual, Business, or Enterprise)
- onWatch installed ([Quick Start](../README.md#quick-start))

---

## Step 1: Create a GitHub Personal Access Token

1. Go to **GitHub Settings** → **Developer settings** → **Personal access tokens** → **Tokens (classic)**

   Direct link: https://github.com/settings/tokens

2. Click **Generate new token** → **Generate new token (classic)**

3. Configure the token:
   - **Note**: `onwatch-copilot` (or any name you prefer)
   - **Expiration**: Choose based on your preference (no expiration recommended for background tracking)
   - **Scopes**: Check only the `copilot` scope

   ![copilot scope](https://docs.github.com/assets/cb-6384/images/help/settings/token-scope-copilot.png)

4. Click **Generate token**

5. **Copy the token immediately** — you won't see it again. It starts with `ghp_`.

---

## Step 2: Configure onWatch

Add the token to your `.env` file:

```bash
cd ~/.onwatch  # or wherever your .env is located
```

Edit `.env` and add:

```
COPILOT_TOKEN=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

Or set it as an environment variable:

```bash
export COPILOT_TOKEN=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

---

## Step 3: Restart onWatch

```bash
onwatch stop
onwatch
```

Or in debug mode to verify:

```bash
onwatch --debug
```

You should see:

```
Starting Copilot agent (interval: 60s)
Copilot poll successful: 3 quotas tracked
```

---

## Step 4: View Dashboard

Open http://localhost:9211 and click the **Copilot** tab.

You'll see:
- **Premium Requests**: Your monthly limit (300 or 1500 depending on plan)
- **Chat**: Usually unlimited
- **Completions**: Usually unlimited

---

## What Gets Tracked

| Quota | Description |
|-------|-------------|
| `premium_interactions` | Monthly premium model requests (Claude, GPT-4, etc.) |
| `chat` | Standard chat completions (typically unlimited) |
| `completions` | Code completions (typically unlimited) |

The dashboard shows:
- Current usage and remaining quota
- Percentage used with color indicators
- Reset date (monthly cycle)
- Usage history and burn rate projections

---

## Troubleshooting

### "Copilot agent not starting"

Verify your token has the `copilot` scope:

```bash
curl -H "Authorization: Bearer ghp_yourtoken" \
  https://api.github.com/copilot_internal/user
```

Should return JSON with `quota_snapshots`. If you get 401/403, regenerate the token with correct scope.

### "No data showing"

- Ensure you have an active Copilot subscription
- Check that the token hasn't expired
- Look at logs: `tail -f ~/.onwatch/.onwatch.log`

### Token security

- The token is stored locally in your `.env` file
- Never commit `.env` to version control
- onWatch never sends your token anywhere except GitHub's API
- All data stays on your machine (SQLite database)

---

## API Details

onWatch uses the same internal API that VS Code, JetBrains, Zed, and other editors use:

```
GET https://api.github.com/copilot_internal/user
Authorization: Bearer <token>
```

This endpoint returns real-time quota data including entitlement, remaining count, and reset dates. While undocumented, it's stable and used by all major Copilot integrations.

---

## See Also

- [Development Guide](DEVELOPMENT.md) — Build from source
- [README](../README.md) — Quick start and configuration
