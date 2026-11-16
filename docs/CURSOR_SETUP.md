# Cursor Provider Setup

onWatch can track Cursor AI usage quotas automatically by reading auth tokens from the local system.

## How It Works

onWatch detects your Cursor authentication from two sources:

1. **Cursor Desktop SQLite** (preferred) - reads `cursorAuth/accessToken` and `cursorAuth/refreshToken` from `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb`
2. **macOS Keychain** (fallback) - reads `cursor-access-token` and `cursor-refresh-token` services

If both sources exist and the SQLite account is a free-tier plan while the keychain has a different account, onWatch prefers the keychain token (matching openusage behavior).

If SQLite stores opaque `cursor-safe-storage:` tokens while Keychain still has JWTs, onWatch prefers Keychain so cookie and Connect RPC calls can authenticate.

## Auto-Detection

No manual configuration needed. If Cursor Desktop is installed and you're logged in, onWatch will auto-detect your credentials on startup.

## Manual Configuration

If auto-detection fails, set the `CURSOR_TOKEN` environment variable:

```bash
export CURSOR_TOKEN="your_cursor_access_token"
```

Or add it to your `.env` file:

```
CURSOR_TOKEN=your_cursor_access_token
```

## Token Refresh

Cursor access tokens are short-lived JWTs. onWatch automatically refreshes them:

1. Before each poll, onWatch checks if the token expires within 5 minutes
2. If expiring, it calls the Cursor OAuth refresh endpoint (`https://api2.cursor.sh/oauth/token`)
3. The refreshed tokens are written back to the store they were read from (Cursor's SQLite database or the macOS Keychain). If the other store still holds the exact refresh token that was just consumed, the same session is duplicated there (older onWatch versions wrote every refresh to both stores), so it is updated too. A store holding a different token is left alone, and a missing store is never created. If Cursor's database holds an encrypted `cursor-safe-storage:` value, onWatch never overwrites it.

If the SQLite database holds an encrypted `cursor-safe-storage:` value and onWatch falls back to the Keychain token, that token is treated as read-only and never refreshed. It may be an old copy of Cursor Desktop's session, so refreshing it would consume the refresh token Desktop holds and log Desktop out. If it has expired, onWatch reports the normal auth-failure state; re-authenticate in Cursor Desktop to recover.

If a refresh succeeds but the new tokens cannot be saved, onWatch keeps the new access token in memory for this process and stops further automatic refreshes, so it never rotates a token it cannot persist.

If the refresh token is invalid (e.g., revoked), onWatch pauses polling and logs an error. You'll need to re-authenticate via the Cursor app.

## Supported Metrics

### Individual Accounts (Pro, Ultra, Free)
- **Total Usage** - percentage of plan limit used
- **Auto Mode** - auto-mode usage percentage
- **API Usage** - API/manual usage percentage
- **Credits** - combined credit grants + Stripe prepaid balance (dollars)
- **On-Demand** - spend limit usage (dollars, if configured)

### Team Accounts
- **Total Usage** - dollar-based spend tracking
- **Auto Mode** - auto-mode usage percentage
- **API Usage** - API manual usage percentage
- **Credits** - combined credit grants + Stripe balance (dollars)
- **On-Demand** - individual or pooled spend limit (dollars)

### Enterprise Accounts
- **Requests** - per-model request counts and limits (when `/api/usage` exposes finite limits)
- **Total Usage** - included pool usage in dollars (from `/api/usage-summary` when legacy usage has null limits)

## Account Type Detection

onWatch automatically detects your account type:
- **Individual**: `planName` is "pro", "ultra", or "free", or `spendLimitUsage.limitType` is "user"
- **Team**: `planName` is "team"/"business", or `limitType` is "team"
- **Enterprise**: `planName` is "enterprise" (uses request-based `/api/usage` instead of Connect RPC)

`spendLimitUsage.pooledLimit` is no longer used to infer a Team account. Free plans also send this field, so only the plan name or `limitType` decides Team status.

Numeric usage fields are decoded tolerantly: Cursor may send an amount as a JSON number (`50000`) or a quoted string (`"50000"`). Fields that still cannot be parsed are treated as zero and logged once, instead of failing the whole poll.

## API Endpoints Used

| Endpoint | Purpose |
|----------|---------|
| `POST /aiserver.v1.DashboardService/GetCurrentPeriodUsage` | Current usage metrics (Connect RPC) |
| `POST /aiserver.v1.DashboardService/GetPlanInfo` | Plan name and pricing (Connect RPC) |
| `POST /aiserver.v1.DashboardService/GetCreditGrantsBalance` | Credit grants balance (Connect RPC) |
| `GET https://cursor.com/api/auth/stripe` | Stripe prepaid balance (cookie auth) |
| `GET https://cursor.com/api/usage` | Request-based usage for enterprise (cookie auth) |
| `GET https://cursor.com/api/usage-summary` | Billing pool usage for enterprise/team when `/api/usage` has no limits |
| `POST https://api2.cursor.sh/oauth/token` | OAuth token refresh |

## Troubleshooting

### "unauthorized - invalid or expired token"
onWatch will attempt to refresh your token automatically. If refresh fails, try:
1. Open Cursor Desktop and make sure you're logged in
2. Restart onWatch to trigger fresh token detection

### "session expired - re-authentication required"
Your refresh token has been revoked. You need to:
1. Re-authenticate in the Cursor app
2. Restart onWatch

### Free-tier account detected incorrectly
If onWatch detects your free-tier SQLite account instead of your paid keychain account, try:
1. Logging out of the free-tier account in Cursor Desktop
2. Restarting onWatch