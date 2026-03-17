# Gemini CLI Quota Tracking

onWatch can track your Google Gemini CLI quota usage, showing per-model remaining quota, reset times, and usage trends.

## Prerequisites

1. Install Gemini CLI: https://github.com/google-gemini/gemini-cli
2. Authenticate: `gemini` (follow the OAuth login flow)
3. Verify credentials exist: `ls ~/.gemini/oauth_creds.json`

## Auto-Detection

onWatch automatically detects Gemini credentials from `~/.gemini/oauth_creds.json`. No configuration needed - just install and authenticate with the Gemini CLI.

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

## Configuration

### Opt-Out

To disable Gemini tracking:

```bash
GEMINI_ENABLED=false
```

### Custom Client Credentials

By default, onWatch uses the Gemini CLI's embedded OAuth client credentials. To override:

```bash
GEMINI_CLIENT_ID=your-client-id
GEMINI_CLIENT_SECRET=your-client-secret
```

## Troubleshooting

### "Gemini polling PAUSED due to repeated auth failures"

Re-authenticate via the Gemini CLI:

```bash
gemini
# Follow the OAuth login flow
```

onWatch will automatically detect the new credentials and resume polling.

### No Gemini Data in Dashboard

1. Check that `~/.gemini/oauth_creds.json` exists and contains a valid `access_token`
2. Check that `GEMINI_ENABLED` is not set to `false`
3. Check the onWatch logs for errors
