# Multiple Anthropic (Claude) Accounts

onWatch can track several Claude accounts at once. The primary account works
exactly as before (`ANTHROPIC_TOKEN`, or auto-detected Claude Code credentials).
Extra accounts are added next to it and each one is polled by its own agent.

## Option 1: environment variables (Docker friendly)

```env
# Primary account (unchanged)
ANTHROPIC_TOKEN=sk-ant-oat01-...

# Extra account "work"
ANTHROPIC_TOKEN_WORK=sk-ant-oat01-...
ANTHROPIC_REFRESH_TOKEN_WORK=sk-ant-ort01-...   # optional, enables OAuth refresh

# Extra account "personal" (access token only)
ANTHROPIC_TOKEN_PERSONAL=sk-ant-oat01-...
```

- `<NAME>` becomes the account name in lowercase. Allowed: letters, digits,
  `_` and `-` (max 32 chars, must start with a letter or digit). `default` is
  reserved for the primary account.
- On start, onWatch seeds `<data dir>/anthropic-profiles/<name>.json` from the
  env vars. With Docker the data dir is `/data`, so mount it as a volume.
- With a refresh token, onWatch refreshes the access token before it expires and
  on rate limits (429). Refresh tokens are one-time use: the rotated pair is
  written back to that account's profile file only. A container restart keeps
  the rotated tokens; changing the env value re-seeds the file.
- Without a refresh token the access token is used as-is until it expires.

## Option 2: profile files

Copy a Claude Code credentials file (`~/.claude/.credentials.json` from the
machine where that account is logged in) to:

```
<data dir>/anthropic-profiles/<name>.json
```

The file uses the Claude Code layout:

```json
{"claudeAiOauth": {"accessToken": "...", "refreshToken": "...", "expiresAt": 1767225600000}}
```

Note: once onWatch refreshes that token, the copy on the other machine is no
longer valid (refresh tokens rotate). Use a login dedicated to onWatch, or let
only one place refresh it. Do not add the account that is already the primary
account.

## Behaviour

- Accounts are read at startup; restart onWatch after adding or removing one.
  Removed accounts are soft-deleted and their history is kept.
- Each account has its own snapshots and reset cycles (`account_id` column in
  `anthropic_snapshots` / `anthropic_reset_cycles`; `0` = primary account).
- Extra accounts always use the OAuth usage API. The statusline bridge and the
  "Claude Code is running" guard apply only to the primary (local) account.
- Settings > Providers lists every account under Anthropic with its own
  Telemetry and Dashboard toggles (`anthropic:<id>` keys, `anthropic:0` is the
  primary account). The global Anthropic toggle still controls all of them.
- The dashboard shows each extra account as its own card group (Anthropic tab
  and All Providers view). `GET /api/current?provider=anthropic&account=<id>`,
  `/api/history?provider=anthropic&account=<id>` and
  `/api/cycles?provider=anthropic&account=<id>` return one account;
  `GET /api/anthropic/accounts` lists them.
- Up to 20 extra accounts are supported.

Insights, cycle overview, session tracking, logging history and the menubar
currently cover the primary account only.
