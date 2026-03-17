# Gemini CLI Quota Tracking

onWatch can track your Google Gemini CLI quota usage, showing per-model remaining quota, reset times, and usage trends.

## Prerequisites

1. Install Gemini CLI: https://github.com/google-gemini/gemini-cli
2. Authenticate: `gemini` (follow the OAuth login flow)
3. Verify credentials exist: `ls ~/.gemini/oauth_creds.json`

## Auto-Detection

onWatch automatically detects Gemini credentials from `~/.gemini/oauth_creds.json`. No configuration needed - just install and authenticate with the Gemini CLI.

## Docker / Headless Setup

For Docker or headless environments where Gemini CLI isn't installed, you can pass credentials via environment variables.

### Option 1: Refresh Token (recommended)

1. On any machine with Gemini CLI, authenticate and extract the refresh token:
   ```bash
   gemini
   cat ~/.gemini/oauth_creds.json | python3 -c "import json,sys; print(json.load(sys.stdin)['refresh_token'])"
   ```

2. Add to your `.env` or `docker-compose.yml`:
   ```bash
   GEMINI_REFRESH_TOKEN=1//0gXXXXXXXXXXXXX
   ```

   Or in `docker-compose.yml`:
   ```yaml
   environment:
     - GEMINI_REFRESH_TOKEN=1//0gXXXXXXXXXXXXX
   ```

   onWatch will automatically exchange the refresh token for access tokens. Google refresh tokens don't expire unless revoked.

### Option 2: File Mount

Mount the credentials file directly:
```yaml
volumes:
  - ./gemini-creds.json:/root/.gemini/oauth_creds.json:ro
environment:
  - GEMINI_ENABLED=true
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `GEMINI_ENABLED` | `true` to enable, `false` to disable (auto-detected when credentials exist) |
| `GEMINI_REFRESH_TOKEN` | OAuth refresh token (for Docker/headless - recommended) |
| `GEMINI_ACCESS_TOKEN` | OAuth access token (for Docker/headless - expires in ~1hr) |
| `GEMINI_CLIENT_ID` | Custom OAuth client ID (optional, has defaults) |
| `GEMINI_CLIENT_SECRET` | Custom OAuth client secret (optional, has defaults) |

Setting `GEMINI_REFRESH_TOKEN` or `GEMINI_ACCESS_TOKEN` automatically enables Gemini tracking (`GEMINI_ENABLED=true` is implied).

## How It Works

onWatch uses the same internal Google APIs as the Gemini CLI `/stats` command:
- `cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota` - per-model remaining quota
- `cloudcode-pa.googleapis.com/v1internal:loadCodeAssist` - tier and project detection

Token refresh is handled automatically. Google OAuth tokens expire in ~1 hour, and onWatch proactively refreshes them 15 minutes before expiry.

## Tracked Models

All models returned by the quota API are tracked, typically including:
- Gemini 2.5 Pro
- Gemini 2.5 Flash
- Gemini 2.5 Flash Lite
- Gemini 3 Pro (Preview)
- Gemini 3 Flash (Preview)
- Gemini 3.1 Flash Lite (Preview)

Each model has independent quota limits that reset on a 24-hour cycle.

## Troubleshooting

### "Gemini polling PAUSED due to repeated auth failures"

Re-authenticate via the Gemini CLI:

```bash
gemini
# Follow the OAuth login flow
```

Or update your `GEMINI_REFRESH_TOKEN` environment variable with a fresh token.

onWatch will automatically detect the new credentials and resume polling.

### No Gemini Data in Dashboard

1. Check that credentials are available (file or env vars)
2. Check that `GEMINI_ENABLED` is not set to `false`
3. Check the onWatch logs for errors:
   ```bash
   # Local
   tail -f ~/.onwatch/data/.onwatch.log | grep -i gemini

   # Docker
   docker compose logs | grep -i gemini
   ```
