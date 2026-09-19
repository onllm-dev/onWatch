# onWatch Privacy Notice

**Last reviewed: 2026-09-16.** This notice describes onWatch as shipped in the release
it is bundled with. The copy of this file inside your installed version is the
authority for that version; a newer copy on GitHub may describe a version you are
not running.

onWatch is a self-hosted daemon. You install it on your own machine, it polls AI
provider quota APIs with your own credentials, it writes usage history to a SQLite
file on that same machine, and it serves a dashboard on that machine. The onWatch
project receives nothing. There is no analytics, no telemetry, no crash reporting,
no phone-home and no onWatch-operated backend of any kind. Searching the source for
the usual suspects (Sentry, PostHog, Mixpanel, Plausible, tag managers) returns
nothing, and you can repeat that search yourself.

That does not make this document unnecessary. onWatch handles account emails, OAuth
refresh tokens, per-request usage logs and other people's credential files, and it
contacts up to twenty-five third-party endpoints. This notice itemises all of it.

---

## 0. How to read this, and who it is for

This document serves two different readers. Most sections apply to both; where they
do not, the section says so.

| If you are | Then | Read especially |
|---|---|---|
| An individual running onWatch on your own machine, for your own accounts, with the dashboard reachable only by you | Most of the GDPR does not apply to you at all (Art. 2(2)(c), purely personal or household activity), and the DPDP Act does not apply to you either (s.3(c)(ii), personal or domestic purpose). This notice is then documentation, not a compliance obligation. | Sections 1, 3, 4, 5, 6 |
| An organisation deploying onWatch, or anyone whose dashboard shows another person's account | You are the Data Controller under the GDPR and the Data Fiduciary under the DPDP Act. This notice is the raw material for your own notice, your Art. 30 record of processing, and your DPDP s.5 itemised notice. It is not itself your notice - you must publish your own, with your own identity and contact details in it. | All sections, and Sections 1, 6, 7, 8, 9, 11 in particular |

Nothing in this document is legal advice, and the onWatch project cannot give you
any. It is an accurate description of what the software does, so that you can write
whatever your own situation requires.

---

## 1. Who is who

**The onWatch project (onllm-dev) is not a controller, not a processor, not a Data
Fiduciary and not a Data Processor for anything you do with onWatch.** The reason is
simple and verifiable: no data ever reaches the project. onWatch is GPL-3.0 source
and a binary you run. The project has no server that receives your usage data, your
credentials, your IP address or your configuration.

**The person or organisation that runs the binary is the controller / Data
Fiduciary.** That is whoever decided to install onWatch and chose which provider
accounts to watch. They determine the purposes and means, they hold the database,
and they carry every obligation that follows.

### 1.1 The household exemption, and exactly where it stops

If you run onWatch on your own machine, watching your own AI accounts, and nobody
else can reach the dashboard, you are within the personal/household exemption -
GDPR Art. 2(2)(c) and DPDP Act s.3(c)(ii). You are still processing your own data,
but data protection law does not regulate you for it.

The exemption stops the moment any of the following is true. This is not an
exhaustive list, but these are the three cases that actually occur with onWatch:

| Situation | Why the exemption stops |
|---|---|
| The dashboard is reachable by other people - bound to `0.0.0.0`, exposed through a reverse proxy, on a shared LAN, or behind SSO via `ONWATCH_AUTH_MODE=trusted_proxy` | You are making personal data (account emails, usage history) available to others. Processing is no longer purely personal. |
| A company, team or client deploys it | The purpose is commercial or organisational, not domestic. Full controller / Data Fiduciary duties apply. |
| It watches an account belonging to someone other than you - an employee's, a contractor's, a team member's | You are processing another identifiable person's data, including their AI usage patterns over time. This is the case with the clearest obligations: notice, lawful basis, and in an employment context usually a works-council or employee-consultation duty as well. There is nothing in onWatch that tells that person they are being watched. You have to tell them. |

### 1.2 If you deploy onWatch for an organisation

You should also be aware that:

- onWatch **writes to credential files owned by other tools and other users** (see
  Section 5). On a multi-user machine, or with `sudo`, that touches files that are
  not yours.
- onWatch's history is a **behavioural record**. Even the tables that contain no
  name or email are a minute-by-minute record of when one identifiable person used
  which AI model, how much, and what it cost. Treat all of it as personal data, not
  just the columns with an email in them.
- The provider APIs are the vendors' own processing, under the vendors' own terms.
  onWatch reads from them; it does not change what they collect. Your agreements
  with Anthropic, OpenAI, Google and the rest are unaffected by onWatch and are not
  described here.

---

## 2. Purposes, and what onWatch does not do

Applies to organisational deployments; useful context for everyone.

| Purpose | Data used |
|---|---|
| Show current quota and usage per provider | Provider quota responses, account identifiers |
| Show usage history, reset cycles and trends over time | Snapshot tables, one row per poll per provider |
| Send threshold and reset alerts | Notification settings, SMTP config, push subscriptions |
| Authenticate the dashboard | `users`, `auth_tokens`, session tokens, login rate-limiting by IP |
| Keep polling working across token expiry | Provider credential files, OAuth refresh endpoints |
| Ingest optional third-party usage telemetry you point it at | `~/.onwatch/api-integrations/*.jsonl` |

**onWatch performs no automated decision-making and no profiling** in the sense of
GDPR Art. 22. Alerts are fixed numeric-threshold comparisons ("utilisation crossed
80%"). Nothing is scored, ranked, inferred or predicted about a person, and no
decision with a legal or similarly significant effect is produced. There is no
advertising, no ad tech, no behavioural tracking, no cookies beyond a single
first-party session cookie (`onwatch_session`), and no third-party cookies at all.

The lawful basis under the GDPR is yours to determine. For most organisational
deployments watching corporate accounts it will be legitimate interests (Art.
6(1)(f)) with a documented balancing test, or contract. Under the DPDP Act, note
that consent is the default basis (s.6) and that the "certain legitimate uses" in
s.7 are narrow; if you are watching an employee's account you must work out which
applies before you deploy, not after.

---

## 3. What is stored, itemised

Everything in this section lives in exactly one of four places on the machine
running onWatch:

1. The SQLite database, by default `~/.onwatch/data/onwatch.db` (plus `-wal` and
   `-shm`). In Docker, `/data/onwatch.db`.
2. Other files under `~/.onwatch/` (`%LOCALAPPDATA%\onwatch` on Windows).
3. Credential files belonging to other tools, elsewhere on the disk (Section 5).
4. Process memory, which goes away when the daemon stops.

The database has 56 tables. **17 hold personal data. 39 hold only numeric quota
telemetry** - no email, no name, no account identifier, no endpoint, no credential,
no IP address. Section 3.5 lists those 39 by name so you can see the shape of it,
but read the caveat there before you treat them as anonymous.

The database file is **not encrypted as a whole**. Individual values that are
encrypted are named in Section 5.3.

### 3.1 Identity and credentials in the database

| Table and column | What it is | Where it comes from |
|---|---|---|
| `users.username`, `users.password_hash` | The dashboard login. bcrypt hash (legacy installs may hold a 64-hex SHA-256). | You, at setup |
| `auth_tokens.token` | Dashboard session bearer token, 32 random bytes hex | Generated on login |
| `push_subscriptions.endpoint`, `.p256dh`, `.auth` | A Web Push subscription. The endpoint is a URL at the browser vendor's push service and is a stable identifier for that specific browser install; `p256dh` and `auth` are that browser's encryption keys. | The browser, when you enable push |
| `provider_accounts.name` | Free-text account label. For Codex it is the profile name you typed - people routinely use an email address or a person's name here. | You |
| `provider_accounts.external_id` | Hard third-party identity. For Codex this is `<chatgpt_account_id>:<chatgpt_user_id>`, where the user id is decoded from the `id_token` JWT. This directly links the install to a named OpenAI account. | Provider credential |
| `provider_accounts.metadata` | Free-form JSON per account. For MiniMax it holds the **API key**. | Setup / provider |
| `antigravity_snapshots.email` | Google/Antigravity account email | Provider response |
| `grok_snapshots.email`, `.team_id`, `.login_method` | xAI account email, team/org id, login method | `~/.grok/auth.json` |
| `ollama_snapshots.account_email`, `.account_name` | Ollama Cloud account email and real name | `POST ollama.com/api/me` |
| `kimi_snapshots.user_id`, `.region`, `.membership` | Kimi/Moonshot account user id, region, membership level | Provider response |
| `gemini_snapshots.project_id` | The Google Cloud / Code Assist project id (`cloudaicompanionProject`) tied to the install | Provider response |
| `codex_snapshots.account_id`, and the `account_id` column on `codex_reset_cycles`, `minimax_snapshots`, `minimax_reset_cycles`, `grok_snapshots`, `grok_reset_cycles`, `kimi_snapshots`, `kimi_reset_cycles` | An internal integer foreign key to `provider_accounts.id`. Not an identifier by itself, but it is the join that attaches every quota row to an identity row. | Internal |
| `openrouter_snapshots.label` | The OpenRouter API key's own label, returned by the vendor. User-chosen free text - commonly a person's name, an email or a machine name. | Provider response |
| `system_alerts.title`, `.message`, `.metadata` | Local alert records. The telemetry-ingest error path writes `{"source_path":"<absolute path>","line":"<first 180 bytes of the offending line, verbatim>"}` into `metadata`, so this column can embed a filesystem path containing the OS username and a raw fragment of third-party telemetry. | Internal |

**The identity columns above are re-written on every single poll.** They are
columns on the snapshot tables, not on a separate identity table, so with the
default 120-second poll interval an account email is written into a new row roughly
720 times a day, indefinitely.

### 3.2 Settings keys

`settings` is a key/value table. These are the keys that matter for privacy.

| Key | Contents | Encrypted? |
|---|---|---|
| `smtp` | `host`, `port`, `protocol`, `username`, `password`, `from_address`, `from_name`, `to`. The `to` list is the alert recipients. | Password only. The **username, from address and recipient addresses are plaintext**. |
| `gemini_tokens` | Google OAuth `access_token`, `refresh_token`, `expires_at` | Yes (see Section 5.3) |
| `provider_settings` | Provider secrets set through the dashboard, overriding `.env`: `api_key` for Synthetic, Z.ai, MiniMax, OpenRouter, Moonshot, DeepSeek and Ollama; `token` for Copilot; `csrf_token` for Antigravity; `auth_cookie` and `workspace_id` for OpenCode. These are stripped from dashboard API responses, which is a display measure, not storage protection. | No - plaintext at rest |
| `vapid_keys` | The Web Push VAPID keypair, **including the private key** | No - plaintext at rest |
| `encryption_salt` | 16-byte hex salt used to derive the at-rest encryption key. Not personal data, but it is key material: reset it independently and the encrypted values become undecryptable. | n/a |
| `dashboard_provider_labels` | Free-text per-provider tab labels you chose. Unvalidated - in practice people put account emails, personal names or machine names here. | No |
| `timezone`, `notifications`, `provider_visibility`, `api_integrations_visibility`, `hidden_insights`, `menubar`, `auto_refresh_tokens`, `update_check`, `dashboard_providers_order`, migration flags | Display and behaviour preferences. No identifiers. `timezone` is a weak location signal. | No |

### 3.3 The `raw_json` columns, and what is actually inside them

Every provider snapshot table has a `raw_json` column holding the vendor's response
verbatim. The typed columns next to it are a subset. **Three of these columns hold
identifiers that appear nowhere else in the database**, so an export or an erasure
that only walks the typed columns will miss them.

| Column | What is inside | Identifier only found here |
|---|---|---|
| `copilot_snapshots.raw_json` | The whole Copilot user response: `login`, `access_type_sku`, `quota_reset_date`, the quota map | **`login` - your GitHub username.** The typed columns keep only the plan and reset date. |
| `antigravity_snapshots.raw_json` | IDE path: the whole user-status response - `userStatus.name`, `userStatus.email`, plan info, every model config. CLI path (`source` = `cli`): only the quota-summary buckets, no identifiers. | **`userStatus.name` - your real display name.** |
| `kimi_snapshots.raw_json` | The whole usages response: `user.userId`, `user.region`, `user.businessId`, `user.membership.level`, `authentication.method`, `authentication.scope`, usage and limit numbers | **`user.businessId` and the `authentication` block.** |
| `grok_snapshots.raw_json` | Billing cycle start/end, monthly limit, on-demand cap, usage in cents | No identifier, but it is the account's financial spend history |
| `cursor_snapshots.raw_json` | Billing cycle, plan usage, total/included/bonus spend, spend limits, display message. The Cursor OAuth tokens and Stripe structs are deliberately **not** persisted. | No identifier; financial spend history |
| `ollama_snapshots.raw_json` | The `/usage` body only: cost, period, per-model name/request count/cost, monthly usage. The `/me` body holding the email and name goes to typed columns instead. | No identifier; per-model activity |
| `anthropic_snapshots.raw_json` | A quota map: quota name to `{utilization, resets_at, is_enabled, monthly_limit, used_credits}`. Keys can be model-scoped, e.g. `seven_day_opus`. The statusline path stores `{"_source":"statusline","rate_limits":{...}}`. | None |
| `codex_snapshots.raw_json` | `plan_type`, primary/secondary rate-limit windows, code-review rate limit, credit balance | None |
| `gemini_snapshots.raw_json` | Quota buckets only: `remainingFraction`, `resetTime`, `modelId`. The project id goes to a typed column. | None |
| `minimax_snapshots.raw_json` | `base_resp` plus per-model interval and weekly counts | None |
| `opencode_snapshots.raw_json` | **Always empty.** The OpenCode client never populates it. Documented so it is not mistaken for stored vendor data. | None |
| `zai_snapshots.time_usage_details` | JSON array of per-model usage, e.g. `[{"modelCode":"search-prime","usage":16}]` | None; per-model activity breakdown |

### 3.4 The optional telemetry ingest

Off unless you create `~/.onwatch/api-integrations/` and put JSONL files in it. This
is the only part of onWatch that ingests data you supply rather than data a vendor
returns, and it is the part whose contents onWatch cannot enumerate.

| Table and column | What it is |
|---|---|
| `api_integration_usage_events.account_name` | Free-text account label taken verbatim from each line, default `default`. In practice integrations put an account email or user id here. |
| `api_integration_usage_events.request_id` | The upstream provider's request id, verbatim. A correlation handle into that provider's own logs for that specific call. |
| `api_integration_usage_events.metadata_json` | **Free-form JSON that you control, with no schema, no key allowlist and no redaction** - only whitespace-compacted and capped at 4096 bytes. It can therefore contain anything the emitting integration writes: end-user ids, session or conversation ids, prompt or document excerpts, IP addresses, email addresses. This notice cannot tell you what is in it. If you enable this feature, you are responsible for knowing what your own integrations emit. |
| `api_integration_usage_events.source_path`, `.fingerprint` | The absolute path of the source file (normally containing the OS username) and a SHA-256 over the event fields used for deduplication. The fingerprint is a stable pseudonymous hash of the event - pseudonymised data, not anonymous data. |
| `api_integration_usage_events.integration_name`, `.model`, token counts, `.cost_usd`, `.latency_ms`, `.captured_at` | Per-call telemetry: which model, when, how many prompt and completion tokens, what it cost, how long it took. Every field is numeric, and at per-request granularity this is the most revealing material in the database. |
| `api_integration_ingest_state.source_path` | One row per tailed file, keyed on the absolute path. Persists after the file itself is gone. |
| `api_integration_ingest_state.partial_line` | A verbatim trailing fragment of a telemetry file, **up to 512 KB**, kept so tailing can resume across restarts. It contains raw event text, including whatever was in `metadata`. |

**`api_integration_usage_events.raw_line` (historical).** Databases created by the
original `feat/api-integrations` build stored the entire verbatim JSONL line. A
migration drops the column, but SQLite's `DROP COLUMN` does not reclaim the page
content, so a pre-upgrade database file or any backup of one still contains whole
raw events. Removing this requires `VACUUM` and a decision about your backups.

**The source `.jsonl` files themselves are never deleted or truncated by onWatch.**
It only tails them. Deleting rows from the database leaves the original events intact
on disk.

### 3.5 The 39 tables with no personal data

Numeric quota telemetry only. Listed so you can see how much of the database is not
about identity:

`schema_version`, `quota_snapshots`, `reset_cycles`, `sessions`, `zai_snapshots`,
`zai_hourly_usage`, `zai_reset_cycles`, `anthropic_snapshots`,
`anthropic_quota_values`, `anthropic_reset_cycles`, `notification_log`,
`copilot_quota_values`, `copilot_reset_cycles`, `codex_quota_values`,
`codex_reset_cycles`, `antigravity_model_values`, `antigravity_reset_cycles`,
`moonshot_snapshots`, `moonshot_reset_cycles`, `deepseek_snapshots`,
`deepseek_reset_cycles`, `minimax_model_values`, `minimax_reset_cycles`,
`gemini_quota_values`, `gemini_reset_cycles`, `openrouter_reset_cycles`,
`cursor_snapshots`, `cursor_quota_values`, `cursor_reset_cycles`,
`grok_quota_values`, `grok_reset_cycles`, `kimi_quota_values`, `kimi_reset_cycles`,
`opencode_snapshots`, `opencode_quota_values`, `opencode_reset_cycles`,
`ollama_quota_values`, `ollama_model_usage`, `ollama_reset_cycles`.

Note `zai_snapshots`, `cursor_snapshots`, `anthropic_snapshots`, `moonshot_snapshots`,
`deepseek_snapshots` and `opencode_snapshots` appear here: their `raw_json` and
detail columns were checked and hold no identifiers.

> **Caveat, and it matters.** "No personal data" above means no email, name, account
> identifier, endpoint, credential or IP address. It does **not** mean anonymous.
> A typical onWatch install watches one person's accounts, so every utilisation
> figure, reset cycle and per-model request count in these 39 tables is behavioural
> data about one identifiable person, and is personal data under the GDPR. An export
> or erasure feature should cover all 56 tables, and should cascade on `account_id`
> and snapshot ids rather than deleting identity rows and leaving the history behind.

### 3.6 Files on disk

| Path | Contents |
|---|---|
| `~/.onwatch/data/onwatch.db` (`-wal`, `-shm`) | Everything in Sections 3.1 to 3.5. Mode 0600, in a 0700 directory. Not encrypted as a file. |
| `~/.onwatch/.env` (fallback `./.env`) | Every provider secret in cleartext plus the dashboard login: `SYNTHETIC_API_KEY`, `ZAI_API_KEY`, `ANTHROPIC_TOKEN`, `CODEX_TOKEN`, `OPENCODE_GO_AUTH_COOKIE`, `OPENCODE_GO_WORKSPACE_ID`, `COPILOT_TOKEN`, `MINIMAX_API_KEY`, `OPENROUTER_API_KEY`, `OLLAMA_API_KEY`, `ONWATCH_ADMIN_USER`, `ONWATCH_ADMIN_PASS`. Written mode 0600. |
| `~/.onwatch/data/.onwatch.log` (`.1`, `.2`, `.3`) | The debug log, mode 0600. Records **client IP addresses** (every rejected or unauthenticated request), **proxy-asserted SSO usernames** when `ONWATCH_AUTH_MODE=trusted_proxy`, and **provider account names**. Size-capped at 4 x 50 MB with no time limit. In Docker this content goes to stdout instead, i.e. into your container log, under whatever retention your log stack applies. |
| `~/.onwatch/data/codex-profiles/<profile>.json` | Written by onWatch. Per saved Codex profile: `name`, `account_id`, `user_id` (the ChatGPT user id from the `id_token`), `saved_at`, `tokens.access_token`, `tokens.refresh_token`, `tokens.id_token`, `api_key`. Mode 0600. |
| `~/.onwatch/api-integrations/*.jsonl` | Your telemetry input files. Read only, never deleted or truncated by onWatch. |
| `~/.onwatch/quickview-profile/` | A full browser profile directory, created when the tray quick-view launches Chrome/Edge/Brave/Vivaldi in app mode. It holds the `onwatch_session` cookie, local storage and the dashboard's cache. **Deleting the database does not clear this.** onWatch never cleans it up. |
| `~/.onwatch/onwatch.pid`, `port`, `onwatch-menubar.pid`, `onwatch-menubar.refresh`, `menubar.log` | Process ids, the listening port, a refresh timestamp. No personal data in the contents, though the directory path contains the OS username and `menubar.log` can contain the same request-level detail as the main log. |
| `~/.onwatch/.autostart-declined` | A marker file containing a fixed string. No personal data. Listed for completeness. |

Credential files belonging to **other tools** are in Section 5.

### 3.7 In memory only, never written to disk by onWatch

| What | Lifetime |
|---|---|
| Live dashboard session tokens, plus the admin username and password hash | Process lifetime; tokens also mirrored to `auth_tokens` and expire at 7 days |
| Login rate-limiter entries keyed by **client IP address**, with failure counts | Transient: blocks expire after 5 minutes, entries clear on successful login, stale entries are evicted every tick, and the map is hard-capped at 1000 IPs. Never written to the database. Note that the IP is taken from `X-Forwarded-For` or `X-Real-Ip` before `RemoteAddr`, so behind a proxy you are storing whatever the proxy asserts. |
| Every provider API key, token, auth cookie and CSRF token, held in the config struct and the HTTP clients | Process lifetime |
| The Grok account holder's **first and last name**, read out of `~/.grok/auth.json` | Read into memory; onWatch does not persist it |
| The Antigravity CSRF token, lifted from the running IDE's process command line | Process lifetime, unless you instead type it into the dashboard, in which case it is stored in `settings.provider_settings` |
| The proxy-asserted SSO username in `trusted_proxy` auth mode | Per request, but the value reaches the debug log |

---

## 4. Where it goes

Nothing in this section goes to the onWatch project. Every destination below is
either a vendor whose API you asked onWatch to poll, infrastructure you configured
yourself, or GitHub.

### 4.1 Automatically, because a credential file exists on disk - READ THIS ONE

**Seven providers begin polling their vendors' APIs with no configuration from you
at all, purely because another tool's credential file is present on the disk.**

You install onWatch to watch one provider. If Claude Code, the Codex CLI, OpenCode,
the Gemini CLI, Cursor, the Grok CLI or Kimi Code is installed on the same machine,
onWatch finds those credentials and starts contacting those vendors on its normal
poll timer. Nobody consented to it, because nobody was asked.

| Provider | Starts because onWatch found | Then polls | Can you stop it before the first poll? |
|---|---|---|---|
| Anthropic | `~/.claude/.credentials.json`, the macOS Keychain item, or the GNOME keyring | `api.anthropic.com` | **No.** There is no `ANTHROPIC_ENABLED=false` equivalent. Only the dashboard polling toggle, after the fact, or removing the credentials. |
| Codex | `~/.codex/auth.json`, the OpenCode `auth.json`, or a saved Codex profile | `chatgpt.com` | **No.** No `CODEX_ENABLED=false` exists. |
| OpenCode | Codex credentials whose source is OpenCode - this flips OpenCode on and overrides the env default | `opencode.ai` | Partly. You must set `OPENCODE_ENABLED` to something other than `true` **and** remove the OpenCode-sourced credentials. |
| Cursor | The session token inside Cursor's `state.vscdb` | `api2.cursor.sh`, `cursor.com` | **No.** No `CURSOR_ENABLED=false` exists. |
| Gemini | `~/.gemini/oauth_creds.json` | `cloudcode-pa.googleapis.com` | Yes - `GEMINI_ENABLED=false` |
| Grok | `~/.grok/auth.json` | `grok.com` | Yes - `GROK_ENABLED=false` |
| Kimi | `kimi-code.json` under your credentials directory | `api.kimi.com` | Yes - `KIMI_ENABLED=false` or `KIMI_CODE_ENABLED=false` |

For Anthropic, Codex, Cursor and OpenCode there is no pre-start switch, so **the
first poll happens before you have a chance to object.** After that you can turn any
provider's polling off in the dashboard, which is honoured on the next cycle.

What each poll discloses is in Section 4.2. If you are deploying for an
organisation, treat this as a disclosure you must make in your own notice, and
check the machine for credential files before you start the daemon.

### 4.2 Only because you configured this provider

Each provider polls on the agent timer, default every 120 seconds
(`ONWATCH_POLL_INTERVAL`). Every one of them can be turned off with the dashboard's
per-provider polling toggle. Every request discloses the machine's **egress IP
address** and TLS fingerprint in addition to what is listed.

| Destination | What it is for | What leaves the machine |
|---|---|---|
| `api.synthetic.new` | Synthetic quota | `Authorization: Bearer <SYNTHETIC_API_KEY>`, `User-Agent: onwatch/1.0` |
| `api.z.ai` | Z.ai quota | `Authorization: <ZAI_API_KEY>` (raw, no `Bearer`), `User-Agent: onwatch/1.0` |
| `open.bigmodel.cn` | Z.ai quota, China region only (`ZAI_REGION=cn`) | Same as above |
| `api.anthropic.com` | Claude Code quota | The Claude Code OAuth access token, `anthropic-beta: oauth-2025-04-20`, and `User-Agent: claude-code/2.1.69` - **onWatch identifies itself to Anthropic as the Claude Code client, not as onWatch** |
| `console.anthropic.com` | OAuth token refresh, used deliberately as a rate-limit bypass: on HTTP 429 onWatch mints a new access token to get a fresh rate-limit window | The refresh token and OAuth client id. The response then **rewrites your on-disk Claude Code credentials**, the macOS Keychain item and the GNOME keyring. Fires reactively from inside a poll. |
| `chatgpt.com` | Codex usage | The Codex/ChatGPT access token, `X-Account-Id` and `ChatClaude-Account-Id` carrying the ChatGPT account id |
| `chatgpt.com` (auto quota-starter, Beta, **default off**) | Deliberately spends a little of your ChatGPT quota to start a fresh limit window after a reset | **The only call in onWatch that submits generative content.** A full model request: the model, an instructions string, and the user turn `Reply with exactly the string "Quota Resumed" and nothing else.`, sent with `originator: codex_cli_rs`. It bills your account. Opt in only via `CODEX_AUTO_START_5H` / `CODEX_AUTO_START_7D` or the dashboard equivalents. |
| `auth.openai.com` | Codex OAuth refresh | Refresh token and client id. The response **rewrites your Codex `auth.json`**. |
| `api.github.com` | GitHub Copilot quota | `Authorization: Bearer <COPILOT_TOKEN>`. Requires an explicit token - no auto-detection. |
| `api.minimax.io` | MiniMax coding-plan quota | `Authorization: Bearer <MINIMAX_API_KEY>` |
| `www.minimaxi.com` | MiniMax quota, China region only | Same as above |
| `cloudcode-pa.googleapis.com` | Gemini Code Assist quota and tier | Google OAuth access token, project and tier identifiers in the request body |
| `oauth2.googleapis.com` | Gemini OAuth refresh | Google refresh token, OAuth client id and client secret |
| `api2.cursor.sh` | Cursor usage over Connect RPC, and OAuth refresh | Cursor access token, then the refresh token on expiry |
| `cursor.com` | Cursor plan info and per-user usage | `Cookie: WorkosCursorSessionToken=<token>` - **a full web session cookie, not a scoped API key** - and your Cursor user id in the query string |
| `api.kimi.com` | Kimi Code usage | Kimi access token, `User-Agent: onwatch/kimi-code` |
| `auth.kimi.com` | Kimi OAuth refresh | Kimi refresh token |
| `grok.com` | Grok credits and quota over gRPC-web | Grok access token, plus `Origin: https://grok.com` and `Referer: https://grok.com/?_s=usage` - **onWatch forges browser-origin headers so the request looks like the Grok web app** |
| `api.moonshot.ai` | Moonshot balance | `Authorization: Bearer <MOONSHOT_API_KEY>` |
| `api.deepseek.com` | DeepSeek balance | `Authorization: Bearer <DEEPSEEK_API_KEY>` |
| `openrouter.ai` | OpenRouter key limits and usage | `Authorization: Bearer <OPENROUTER_API_KEY>` |
| `ollama.com` | Ollama Cloud usage and account identity | `Authorization: Bearer <OLLAMA_API_KEY>`. The `/api/me` response is where `account_email` and `account_name` come from. |
| `127.0.0.1:42100` (Antigravity) | Antigravity quota from the locally running IDE | **Nothing leaves the machine.** Caveat: `ANTIGRAVITY_BASE_URL` is an unvalidated base URL, and the CSRF token is sent to whatever host it names. Do not point it off-box. |

Three things in that table are worth restating plainly, because they change what a
vendor sees: onWatch **impersonates the Claude Code client** to Anthropic, **forges
the Grok web app's Origin and Referer** to xAI, and sends **full web session
cookies** rather than scoped API keys to Cursor and OpenCode. If your organisation
has rules about how its accounts may be accessed, these are the facts to check them
against.

### 4.3 Notifications - two destinations onWatch does not control

Both are off unless you configure them.

**Your SMTP server.** When a threshold is crossed, onWatch connects to the mail
relay you configured and sends the alert. What reaches that relay: the SMTP username
and password, the From and To addresses, and an alert body naming the provider, the
quota and the utilisation percentage. So the relay learns your recipients and your
quota posture. **If you set the protocol to `none`, the mail server credentials are
transmitted in plaintext.** onWatch warns about this; choose `none` only on a
network and server you trust completely. Where that relay is, and what it logs, is
between you and its operator.

**The browser's push service.** If you enable push, the browser registers a
subscription with its own vendor's service - Google FCM for Chrome, Mozilla for
Firefox, Apple for Safari, Microsoft for Edge. onWatch does not choose this and
cannot change it; the destination is whatever endpoint the browser hands over. The
alert payload is AES-128-GCM encrypted under RFC 8291, **so the push service cannot
read your alerts**. It does still learn:

- the daemon's egress IP address,
- the timing and frequency of every alert, which is a usable signal about when you
  hit your limits,
- the subscription endpoint, which identifies that specific browser install,
- onWatch's VAPID public key.

Registration itself also discloses the viewer's browser IP and User-Agent to that
vendor and establishes a persistent link between that browser and this onWatch
instance. To stop all of it, disable push and delete the stored subscriptions.

### 4.4 The version check, and how to switch it off

When enabled, onWatch asks `api.github.com` whether a newer release exists - on
dashboard load and then hourly, cached for an hour server-side. What GitHub sees:
the machine's egress IP address and `User-Agent: onwatch/<version>`, which
**discloses the exact installed version**, and therefore whether you are running a
build with a known vulnerability.

**Turn it off in Settings -> General -> "Privacy & Outbound Connections" -> Update
check, or set `ONWATCH_UPDATE_CHECK=false` to pin it off for the whole deployment.
When it is off, onWatch makes no version request at all.** You can still update by
re-running the installer.

Related GitHub traffic, for completeness: the update check has two fallbacks
(`api.github.com/repos/.../releases` and a `github.com` redirect) which follow the
same switch; downloading a replacement binary only happens when you click Update or
run `onwatch update`; and the installer scripts fetch from `github.com` and
`raw.githubusercontent.com` when you run them. The setup script may also offer to
star the repository using your own `gh` credentials - that is a public, attributable
action on your GitHub account, and `ONWATCH_STAR=no` declines it.

### 4.5 Only when you click

The dashboard now serves **every** asset from inside the binary. Chart.js and the
Ubuntu and JetBrains Mono webfonts are vendored under
`internal/web/static/vendor/`, the Content-Security-Policy is `'self'` only, and a
test fails the build if any template or static file references a third-party origin.
**No viewer IP address reaches Google or any CDN, on any page, including the
unauthenticated login page.** The dashboard loads no third-party fonts, no
third-party scripts and no analytics of any kind.

What remains are plain links in the footer and one contextual badge:
`onwatch.onllm.dev`, `onllm.dev`, `github.com`, `buymeacoffee.com` and
`www.reddit.com`. They are inert `<a target="_blank" rel="noopener">` elements -
nothing is fetched until a person clicks. On click, that destination sees the
clicker's IP address, User-Agent and an origin-only Referer.

The menubar and tray companion is fully local: its page has a `'self'`-only policy
and all its IPC is over `127.0.0.1`.

### 4.6 Only at build time

These apply if you build the container image yourself. The shipped container makes
none of them at runtime, and neither does the release binary.

| Destination | Why |
|---|---|
| `registry-1.docker.io` | Pulls the Go builder and Alpine base images |
| `dl-cdn.alpinelinux.org` | `apk add` for git, ca-certificates, tzdata |
| `proxy.golang.org`, `sum.golang.org` | `go mod download`. This **discloses the full dependency graph** to Google-operated services. |
| `gcr.io` | Pulls the distroless runtime base image |

All four are avoidable: use an internal registry mirror and APK mirror, a vendored
module tree or `GOPROXY=off`, or build with `nix build .#onwatch`, which pins a
`vendorHash`.

### 4.7 There is no offline mode flag

onWatch has no single `ONWATCH_OFFLINE` switch. To run it fully air-gapped today you
turn the version check off (Section 4.4), turn off or do not configure every
provider, and leave notifications unconfigured. At that point onWatch makes no
outbound request at all - the dashboard and all of its assets are served from the
binary.

---

## 5. Credentials: what onWatch reads, and what it rewrites

This is the part of onWatch with the widest blast radius outside its own data
directory. onWatch does not have provider credentials of its own; it uses yours, and
for OAuth providers it keeps them alive by refreshing them, which means writing to
files it does not own.

The behaviour is controllable: **Settings -> General -> Credentials -> "Auto refresh
tokens"**. Turn it off and onWatch stops writing refreshed tokens back into harness
credential files.

### 5.1 Read only

| Path | What is in it |
|---|---|
| `~/.gemini/oauth_creds.json` | `access_token`, `refresh_token`, `id_token`, `scope`, `token_type`, `expiry_date`. Note the refreshed copy is persisted **into the database** under the `gemini_tokens` setting, so erasing the database does not touch this file, and deleting this file does not erase the database copy. |
| `~/.grok/auth.json` (or `$GROK_HOME`) | The richest identity file onWatch touches: `key`, `refresh_token`, `auth_mode`, `email`, `team_id`, `user_id`, **`first_name`, `last_name`**, `expires_at`. The email, team id and login method end up in `grok_snapshots`. The names stay in memory. |
| Cursor's `state.vscdb` | onWatch opens Cursor's own SQLite state database and reads the session token (and optionally the Stripe membership type) out of `ItemTable`. Read only, but a live Cursor session credential passes through onWatch's memory, and the path is logged on failure. |
| The running Antigravity IDE's process command line | There is no credential file for Antigravity. onWatch enumerates local processes and lifts `--csrf_token` off the command line. |

### 5.2 Read and rewritten

| Path | What onWatch writes | Leftover copies |
|---|---|---|
| `~/.claude/.credentials.json` | The refreshed Claude Code OAuth block: `accessToken`, `refreshToken`, `expiresAt`, `scopes`, `subscriptionType`, `rateLimitTier` | **`.credentials.json.bak`**, plus the atomic temp file. Mode 0600. |
| macOS Keychain item `Claude Code-credentials` | The same refreshed tokens, into the store Claude Code actually reads, keyed by your OS username | Managed by the OS keychain |
| GNOME Keyring / libsecret, via `secret-tool` | The same refreshed tokens, so the refresh survives a restart, keyed by your OS username | Managed by the keyring |
| `~/.codex/auth.json` (or `$CODEX_HOME`) | `OPENAI_API_KEY` and `tokens` (`access_token`, `refresh_token`, `id_token`, `account_id`) | **`.bak`** plus temp file |
| OpenCode `auth.json` (`$OPENCODE_HOME`, `$XDG_DATA_HOME/opencode/`, or `~/.local/share/opencode/`) | `openai` block: `type`, `refresh`, `access`, `expires`, `accountId` | **`.bak`** plus temp file |
| `kimi-code.json` (`$XDG_DATA_HOME/credentials/` or `~/.kimi-code/credentials/`) | `access_token`, `refresh_token`, `token_type`, `scope`, `expires_at`, `expires_in`. Mode 0600. | none |
| `~/.onwatch/data/codex-profiles/<profile>.json` | Written by onWatch, for onWatch: the full Codex identity and tokens per saved profile (Section 3.6) | none |

**The `.bak` files are never cleaned up.** Each one is a second, unmanaged copy of a
live OAuth refresh token sitting next to the original. If you are hardening a
deployment, or decommissioning a machine, find and remove them. Anthropic refresh
tokens are additionally one-time-use with rotation, so a stale `.bak` may hold a
token that is already spent - or one that still works.

### 5.3 What is encrypted at rest, and what is not

The database file as a whole is **not** encrypted. Individual values are, as
follows. The key is derived with HKDF from the dashboard password hash plus the
`encryption_salt` setting, and is re-derived and the data re-encrypted when the
dashboard password changes.

| Value | State |
|---|---|
| `settings.smtp` -> `password` | **Encrypted**, AES-256-GCM |
| `settings.gemini_tokens` (Google access and refresh token) | **Encrypted**, AES-256-GCM |
| `provider_accounts.metadata` (includes the MiniMax API key) | **Encrypted**, AES-256-GCM |
| `settings.provider_settings` (every provider API key, the Copilot token, the Antigravity CSRF token, the OpenCode auth cookie) | **Encrypted**, AES-256-GCM. Also redacted from dashboard API responses. |
| `settings.vapid_keys`, including the Web Push **private key** | **Encrypted**, AES-256-GCM. |
| `settings.smtp` -> `username`, `from_address`, `to` | **Not encrypted.** Plaintext email addresses at rest. |
| `users.password_hash` | bcrypt hash (legacy installs: 64-hex SHA-256). A hash, not encryption. |
| `auth_tokens.token` | Plaintext, but expires at 7 days and is wiped on password change |
| `~/.onwatch/.env` | **Not encrypted.** Every provider secret and the dashboard login in cleartext, mode 0600. |
| Credential files belonging to other tools (Section 5.1, 5.2) | Whatever protection that tool applies. onWatch writes them mode 0600 where it writes them at all. |

Because the encryption key is derived from the dashboard password, a weak dashboard
password weakens the encrypted values too, and losing the `encryption_salt` makes
them undecryptable. Full-disk encryption remains the right control for the rest.

---

## 6. Retention, export and erasure

### 6.1 The retention policy, and its default

Retention is a policy **you set**. There are two settings, both a whole number of
days, both configurable in the dashboard (Settings, under General) and stored in the
database, and both also settable per deployment with the environment variables
`ONWATCH_RETENTION_SCRUB_DAYS` and `ONWATCH_RETENTION_DELETE_DAYS`:

| Setting | Effect |
|---|---|
| `retention_scrub_days` | Once a row is older than this, its **identifier and verbatim-payload columns are cleared in place** - the emails, account names, labels, `external_id`, `project_id`, `user_id`, `raw_json` and the telemetry `account_name`, `request_id`, `metadata_json` and `source_path`. The numeric quota columns beside them are deliberately left alone, so your long-term charts survive scrubbing. |
| `retention_delete_days` | Once a row is older than this, it is **deleted**, with its child rows removed first so no orphan is left behind. |

**The default for both is zero, which means keep everything forever.** That is the
default for every existing install and every new one. An unset, empty or
unparseable value is also read as "keep everything" rather than guessed at, because
a retention pass is destructive and an unreadable setting must never be taken as
permission to delete. If you need storage limitation - GDPR Art. 5(1)(e) is not
optional for an organisational deployment - **you have to set these yourself.**

The policy is enforced by an hourly pass that re-reads the settings each time, so a
change made in the dashboard takes effect within the hour without a restart, and an
environment value is the default for whichever of the two periods you have not set
in the dashboard. Scrubbing runs before deletion, so a row due for both ends up
deleted rather than scrubbed twice. `retention_delete_days` must be at least
`retention_scrub_days`
(otherwise rows would be deleted before they were ever scrubbed), and the accepted
maximum is 36500 days.

The tables the policy covers come from one declarative registry, and a test fails
the build if a table in the schema is left unclassified. That is the mechanism that
stops a newly added provider from quietly creating a table that escapes retention,
export and erasure.

### 6.2 What is kept, and for how long

| Data | Retention |
|---|---|
| Provider snapshot history, quota values, model values and reset cycles (all ~14 provider table families) | Per the policy above. **Default: kept indefinitely.** |
| `system_alerts` | Per the policy above, by `created_at`. Dismissing an alert in the dashboard only marks it dismissed; retention or erasure is what removes it. |
| `api_integration_usage_events` | 60 days by default, from `ONWATCH_API_INTEGRATIONS_RETENTION` (for example `720h`; `0` disables pruning). This is the one automatic prune that predates the retention policy. |
| `api_integration_ingest_state` | Per the policy above, by `updated_at`. Note the 60-day event prune does not touch this table, so without a retention policy the 512 KB `partial_line` fragments persist. |
| `auth_tokens` and in-memory sessions | 7 days maximum, evicted on a ticker, and all tokens are deleted when the dashboard password changes |
| Login rate-limiter entries (IP addresses) | Minutes, in memory only, capped at 1000 entries |
| `users`, `provider_accounts`, `push_subscriptions`, `settings` | Until you delete them. These are configuration, not history. |
| `~/.onwatch/data/.onwatch.log` | Size-capped at 4 x 50 MB. **There is no time limit.** Under Docker this goes to your container log instead, with whatever retention your log stack applies - that part is yours to configure. |
| `~/.onwatch/api-integrations/*.jsonl` | **Never touched by onWatch.** Your files, your retention. |
| `~/.onwatch/quickview-profile/` | **Never cleaned by onWatch.** Remove the directory to clear the cached dashboard and its session cookie. |
| Credential `.bak` files written by onWatch | **Never cleaned by onWatch.** Remove them yourself. |

### 6.3 Export

`GET /api/privacy/export`, reachable from Settings, returns a single JSON file named
`onwatch-export-<timestamp>.json`. It walks every table in the registry, not only
the 17 with obvious identifiers. It is streamed straight out of SQLite rather than
assembled in memory, so a long history does not blow the 40 MB ceiling, and it is
capped at 20000 rows per table with any truncation recorded in the output rather
than silently dropped.

**Credential values are replaced with `[redacted-by-onwatch-export]`. Personal data
is not redacted** - the point of the file is to show what is held. Treat the
download as sensitive: it is a complete copy of the account's usage history.

### 6.4 Erasure

`POST /api/privacy/erase`, reachable from Settings, in two scopes:

- **Scope `all`** clears every provider's history plus the device and account
  records.
- **Scope `provider`** clears one named provider, which is the proportionate answer
  when only one account is in question.

Erasure requires a typed confirmation, because it cannot be undone.

Two limits to be precise about. First, **erasure does not shrink the database
file**: SQLite leaves the freed pages in place, so you should run `VACUUM` after a
large erasure. This matters most for a database that was ever written by the
original api-integrations build, which still holds whole raw telemetry lines in the
dropped `raw_line` column until it is vacuumed (Section 3.4). Second, it removes
rows, not files.

### 6.5 What erasure and export cannot reach, and what the deployer must still decide

An erasure inside the database does **not** remove:

1. `~/.onwatch/api-integrations/*.jsonl` - the original telemetry events, with their
   full `metadata`, survive on disk. Only you can delete those.
2. `~/.onwatch/data/.onwatch.log` and its rotations, which contain IP addresses,
   proxy-asserted usernames and provider account names. In Docker, your container
   log.
3. `~/.onwatch/.env`.
4. Credential files belonging to other tools, and the `.bak` copies onWatch left
   next to them.
5. `~/.onwatch/quickview-profile/`.
6. **Your backups.** Anything you erase from the live database still exists in every
   snapshot, volume backup and database copy you have taken. If you cannot
   re-process backups, say so in your own notice and say how long they last.
7. The provider's own records. onWatch reads a vendor's API; it cannot delete
   anything on the vendor's side. An erasure request that covers the underlying AI
   usage has to go to Anthropic, OpenAI, Google, xAI, Moonshot and so on directly.

Decisions that are yours, not onWatch's: the two retention windows, which default to
keeping everything; who can reach the dashboard; whether to enable the telemetry
ingest at all and what your integrations are allowed to put in `metadata`; your
backup retention; your container log retention; disk encryption; when to run
`VACUUM`; and whether you tell the people whose accounts you are watching.

---

## 7. Rights, and the actual mechanism for each

Applies where the GDPR or the DPDP Act applies to you - so, in practice, to
organisational deployments and shared dashboards, not to an individual watching
their own accounts at home.

onWatch is software, not a service, so it cannot answer a request on anyone's
behalf. What it provides is the mechanism; the deployer has to receive the request,
verify who is asking, act within the statutory deadline and reply.

| Right | Mechanism in onWatch |
|---|---|
| Access (GDPR Art. 15), portability (Art. 20), and the DPDP s.11 right to information about processing | **The export** - `GET /api/privacy/export`, from Settings (Section 6.3). Machine-readable JSON, every table in the registry. Add to it the disclosures in Sections 3 and 4 of this notice, which give the purposes, the recipients and the retention that an Art. 15 response also requires. |
| Erasure (GDPR Art. 17), and DPDP s.12(3) erasure plus s.8(7) erasure on consent withdrawal | **The erasure** - `POST /api/privacy/erase`, from Settings, scoped to one provider or to everything (Section 6.4), followed by `VACUUM`. Read Section 6.5 before you certify that erasure is complete - seven categories of data sit outside the database. |
| Rectification (GDPR Art. 16), DPDP s.12(1) correction and completion | Honestly: **it depends which field.** Provider data (emails, account names, plan, project id, usage figures) is fetched read-only from the vendor. onWatch cannot correct it, and if it did, the next poll would overwrite the correction. The correction has to be made **at the provider**, and it then flows into onWatch on the next poll - within about two minutes on the default interval. Data you entered locally is directly editable in the dashboard: provider account names, dashboard provider labels, notification settings, recipients, timezone. If a correction cannot wait for the next poll, delete the affected rows. |
| Restriction (GDPR Art. 18) and objection (Art. 21) | Turn the provider's polling off in the dashboard, which stops new rows immediately, and erase or scrub the existing ones. There is no flag that marks data as restricted while keeping it visible. |
| Withdrawal of consent (GDPR Art. 7(3), DPDP s.6(4)-(6)) | Turn off the provider, remove its credentials so auto-enablement (Section 4.1) does not restart it, then erase. Under DPDP s.8(7) erasure on withdrawal is the Data Fiduciary's duty unless retention is legally required. |
| Grievance redressal (DPDP s.13) and complaints | Section 8. **The deployer must fill this in.** |
| Nomination (DPDP s.14) | Section 9. |
| Complaint to a supervisory authority (GDPR Art. 77) or to the Data Protection Board of India (DPDP s.13(3)) | Always available, and in both regimes a person may complain to the regulator. Under DPDP s.13(3) they must ordinarily exhaust the Data Fiduciary's own grievance mechanism first, which is why Section 8 has to be real. |

Note on the DPDP Act's own vocabulary: the data principal's duties under s.15
include not making a false or frivolous complaint. It is not the deployer's job to
police that, but it is worth knowing when you design your process.

---

## 8. Grievance contact

> **DEPLOYER: REPLACE THIS ENTIRE BLOCK BEFORE SHOWING THIS NOTICE TO ANYONE.**
>
> - **Data Controller / Data Fiduciary:** `[legal entity name, registered address]`
> - **Grievance Officer / contact for data protection queries:** `[name or role]`
> - **Email:** `[email address that is monitored]`
> - **Postal address:** `[address]`
> - **Response time we commit to:** `[for example, within 30 days]`
> - **Data Protection Officer, if you have appointed one (GDPR Art. 37):**
>   `[name and contact]`
> - **EU or UK representative, if you are established outside and need one (GDPR
>   Art. 27):** `[name and contact]`

**A Data Fiduciary must publish this.** DPDP Act s.8(9) requires you to publish the
business contact information of a Data Protection Officer or of a person able to
answer a data principal's questions about processing, and s.8(10) requires you to
have a grievance redressal mechanism. s.13 gives the data principal the right to use
it, and s.13(2) requires you to respond within the period the rules prescribe. The
GDPR separately requires controller identity and contact details in any Art. 13/14
notice.

**If you are an individual running onWatch for yourself, this section does not apply
to you.** There is nobody to appoint and nobody to publish to.

**The onWatch project is not the contact for any of this.** It holds none of your
data and cannot act on a request about it. Bugs and security issues in the software
are a separate matter and do go to the project - through the repository's
`SECURITY.md` process.

---

## 9. Nomination (DPDP Act s.14)

Section 14 gives a data principal the right to nominate another person to exercise
their rights on their behalf, in the event of death or incapacity.

onWatch has no nomination feature - no field for a nominee, no mechanism to verify
one, and no way to hand a nominee an account. **If the DPDP Act applies to your
deployment, offering nomination is your duty as Data Fiduciary and it has to be
handled outside onWatch:** accept a nomination through your grievance channel
(Section 8), record it where you keep your other data protection records, verify the
nominee when they come forward, and then use onWatch's export and erasure (Sections
6.3 and 6.4) to give effect to whatever they ask for.

If you deploy onWatch for a team, decide now, in writing, what happens to a member's
usage history when they leave, die or become incapacitated. The answer is a
retention and erasure decision you already have to make (Section 6.5).

---

## 10. Children's data (DPDP Act s.9)

The honest position, plainly:

- onWatch is a developer tool for tracking paid AI provider quotas. It is not
  directed at children, it has no consumer sign-up, no public instance and no way
  for anyone to create an account in it - the deployer sets a single dashboard
  password.
- onWatch processes no data it knows or has reason to believe belongs to a child.
  The only people whose data it holds are the holders of the provider accounts being
  watched, and those are paid AI accounts held by the deployer.
- onWatch **cannot** verify age, and does not try. There is no age gate and there is
  no field anywhere that records an age. If a deployer configures onWatch to watch a
  child's account, onWatch will have no idea, and the DPDP s.9 obligations -
  verifiable parental consent under s.9(1) - fall entirely on that deployer.

**DPDP s.9(3) is not engaged.** That subsection prohibits tracking, behavioural
monitoring and targeted advertising directed at children. onWatch does none of those
things for anyone, of any age: no advertising of any kind, no ad tech, no
third-party trackers, no cross-site identifiers, no profiling, no automated
decision-making (Section 2), and, since the dashboard's assets are all served from
the binary under a `'self'`-only policy, no third-party request on page load at all
(Section 4.5). There is nothing here to exempt, because there is nothing here to
prohibit.

The same reasoning applies to persons with disabilities who have a lawful guardian
under s.9(2): onWatch has no mechanism to identify them and no tracking or
advertising to restrict.

---

## 11. Cross-border transfers

Relevant under GDPR Art. 44-49 (transfers to third countries) and DPDP Act s.16
(the Central Government may restrict transfer to notified countries).

onWatch itself transfers nothing anywhere - it has no infrastructure. Every transfer
below is a consequence of a destination **you** chose. If you are an EU or UK
controller you need a transfer mechanism for each one you actually use: an adequacy
decision, Standard Contractual Clauses with a transfer impact assessment, or another
Chapter V basis. That is between you and the vendor, under the vendor's own terms,
and onWatch has no visibility into it.

| Transfer | Where it goes, and what you need to check |
|---|---|
| Provider API polls (Section 4.2) | Wherever that vendor processes. Most of these are US companies. Two cases are explicitly **region-selectable by you**, so the choice of jurisdiction is yours to make and to document: Z.ai via `api.z.ai` versus `open.bigmodel.cn` (China), and MiniMax via `api.minimax.io` versus `www.minimaxi.com` (China). Setting `ZAI_REGION=cn`, or a MiniMax account region of `cn`, sends your API key and quota requests to a Chinese endpoint. Note also that Moonshot, Kimi and Z.ai are Chinese-headquartered vendors regardless of endpoint. |
| OAuth token refresh endpoints | `console.anthropic.com`, `auth.openai.com`, `oauth2.googleapis.com`, `api2.cursor.sh`, `auth.kimi.com`. Same jurisdictions as the vendors. |
| Your SMTP relay (Section 4.3) | Wherever you pointed it. A hosted mail provider may be in another country and may retain message content and metadata under its own policy. Your choice, your transfer, your assessment. |
| The browser's push service (Section 4.3) | Google, Mozilla, Apple or Microsoft, **chosen by the viewer's browser, not by you and not by onWatch**. Payloads are encrypted end to end, but the daemon's IP address and the timing of every alert are disclosed to that vendor. This is genuinely awkward to assess, because the destination changes with whatever browser a viewer happens to use. If that is unacceptable for your deployment, do not enable push - use email. |
| `api.github.com` version check (Section 4.4) | GitHub, a US company. **Switch it off** and this transfer does not happen: Settings -> General -> "Privacy & Outbound Connections", or `ONWATCH_UPDATE_CHECK=false`. |
| Build-time fetches (Section 4.6) | Docker Hub, Alpine's CDN, Google-operated Go module and checksum services, and Google's container registry. Builder infrastructure only, and all four are mirrorable. |

---

## 12. Security safeguards

Not duplicated here, because it would go stale. See **`SECURITY.md`** in the
repository for the security model, the hardening guidance, the supported versions
and how to report a vulnerability.

The three facts from this notice that most affect your security posture, so that you
do not have to cross-reference to find them:

1. The database is **not encrypted as a file**, and `settings.provider_settings`,
   `settings.vapid_keys` and the SMTP username, from and recipient addresses are
   plaintext inside it (Section 5.3). Use full-disk encryption.
2. onWatch **writes to other tools' credential files** and leaves uncleaned `.bak`
   copies of live OAuth refresh tokens next to them (Section 5.2). `Settings ->
   General -> Credentials -> Auto refresh tokens` turns the writing off.
3. The dashboard has **one shared password**, no per-user accounts and no audit
   trail of who looked at what. If more than one person can reach it, that is the
   control you are missing, and the debug log's IP addresses are not a substitute.

---

## 13. Changes to this notice

This notice is versioned with the source. Any change to what onWatch stores, where
it sends it, or how long it keeps it is reflected here in the same release that
makes the change, and shows up in the repository's commit history for this file -
which is the authoritative changelog for it. There is no remote copy that can be
changed underneath you: the notice your dashboard serves at `/privacy` is the one
compiled into the binary you are running.

onWatch cannot notify anyone of a change, because it has no way to contact anyone.
If you are a deployer, re-read this file when you upgrade, and update your own
notice if anything here moved.

**Last reviewed: 2026-09-16.**
