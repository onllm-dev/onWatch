# Security Policy

onWatch is a self-hosted daemon that holds live credentials for up to 16 AI
providers and serves a web dashboard. That makes it a high-value target on the
machine it runs on. This document says how to report a vulnerability, what the
current security posture actually is, where the known gaps are, and what an
operator should do about them.

onWatch is GPL-3.0 and volunteer-maintained. There is no security team, no paid
support tier and no SLA. Everything below is written so you can judge the risk
yourself rather than trust an assurance.

## Supported versions

| Version | Supported |
|---------|-----------|
| Latest release on `main` | Yes - fixes land here |
| Anything older | No - upgrade first |

Only the most recent release is supported. There are no backport branches and no
long-term-support line. A fix ships as a new patch release; if you are behind,
the answer to a security report will be "upgrade".

Releases come from exactly one place: the GitHub Actions `release.yml` workflow
in this repository, which builds six binaries (linux/amd64, linux/arm64,
windows/amd64, windows/arm64, darwin/amd64, darwin/arm64) and publishes them
with a `SHA256SUMS` file to the repository's Releases page. Anything served from
anywhere else is not a release of this project.

Verify what you install:

```bash
# Download the binary and SHA256SUMS from the release page, then:
sha256sum -c SHA256SUMS --ignore-missing
```

Do this manually. `install.sh` and `install.ps1` download the binary over HTTPS
but **do not verify the checksum or any signature** - see
[Known hardening gaps](#known-hardening-gaps). You can also build from source
(`./app.sh --build`) or reproducibly with Nix (`nix build .#onwatch`), which
avoids the download entirely.

## Reporting a vulnerability

**Do not open a public GitHub issue for a vulnerability.** A public issue is
readable by everyone the moment you file it, including on installs that have not
yet been patched. Use one of the private channels below instead.

Preferred: **GitHub private vulnerability reporting** for this repository -
<https://github.com/onllm-dev/onWatch/security/advisories/new>. This keeps the
report private, gives you a thread with the maintainers, and produces a CVE and
an advisory when the fix ships.

Alternative: email `<SECURITY-CONTACT-PLACEHOLDER>`.

> **Maintainers:** replace `<SECURITY-CONTACT-PLACEHOLDER>` with a real,
> monitored address before relying on this file. An unreplaced placeholder means
> the email channel does not exist, and this file should then say so plainly
> rather than imply an address that bounces.

Please include:

- The version (`onwatch --version`) and platform.
- What an attacker gains, and what access they need to start.
- Reproduction steps, or a proof of concept.
- Any suggested fix.

**Do not** include real credentials, a copy of your `onwatch.db`, your `.env`,
or the contents of `~/.onwatch/data/.onwatch.log` in a report. All four contain
live secrets. Redact, or describe the shape of the data instead.

### What to expect

| Stage | Target |
|-------|--------|
| Acknowledgement | 5 working days |
| Initial assessment | 14 days |
| Fix or a documented mitigation | Best effort, severity-dependent |

These are targets, not commitments. This is a volunteer project; a report filed
while the maintainers are unavailable will wait. If you have had no reply after
14 days, it is reasonable to say in your private thread that you intend to
disclose publicly on a date you choose, and to go ahead on that date. We would
rather you disclose than sit on an unfixed bug indefinitely.

Credit is given in the advisory unless you ask otherwise.

## Scope

### In scope

- The `onwatch` daemon and all provider polling code (`internal/api/`,
  `internal/agent/`, `internal/tracker/`).
- The dashboard: authentication, session handling, authorisation, XSS, CSRF,
  SSRF, path traversal, template injection (`internal/web/`).
- Credential handling - detection, reading, rewriting and storage of provider
  credentials, including the `.bak` files onWatch writes and the OS keychain
  and keyring entries it touches.
- SQL injection, and any place the codebase departs from parameterised SQL.
- Data at rest: what is stored unencrypted, and anything that leaks a secret
  into a log, an HTTP response or a Prometheus metric.
- `install.sh`, `install.ps1`, `install.bat`, the Dockerfile, the systemd and
  launchd unit generation, and the release workflow itself.
- The VS Code extension and the GNOME extension in this repository.
- Denial of service against the daemon that is cheap and remote, including
  unbounded memory or disk growth reachable from a request.

### Out of scope

- **The provider vendors' own APIs.** onWatch is a read-only client of
  Anthropic, OpenAI, Google, GitHub, Cursor, xAI, Moonshot and the rest. A flaw
  in their API, their rate limiting, their OAuth implementation or their data
  handling is theirs to fix and we cannot patch it. Report it to them. What *is*
  in scope is onWatch mishandling a vendor's response or credential.
- **Exposing the dashboard to the public internet.** onWatch is designed for
  loopback or a trusted LAN. Single-password authentication with no MFA is not
  an internet-facing authentication story, and we do not treat "the dashboard is
  reachable from the internet and someone guessed the password" or
  "an unauthenticated attacker can reach `/login`" as vulnerabilities - that is
  the deployment's choice, not a defect. An authentication *bypass*, a way past
  the rate limiter, or a pre-auth memory-corruption or resource-exhaustion bug
  is in scope regardless of where you bound it.
- Findings from an automated scanner with no demonstrated impact, missing
  headers that are already covered below, and anything that requires the
  attacker to already have root or the operator's shell on the host - at that
  point they have the database and the `.env` directly.
- Third-party Go dependencies: report upstream, then tell us so we can bump.
- Social engineering of the maintainers, and physical attacks.

## Current security posture

Verified against the code in this tree, with file and line references so you can
check rather than take it on faith.

| Control | Implementation |
|---------|----------------|
| Dashboard password hashing | bcrypt at `DefaultCost` (10) - `internal/web/middleware.go:32` (`HashPassword`), verified at `:42` (`CheckPasswordHash`) |
| Legacy hash handling | Pre-bcrypt 64-hex SHA-256 hashes are detected (`internal/web/middleware.go:49`) and upgraded on next successful login |
| Credential comparison | `subtle.ConstantTimeCompare` for every username, password-hash and token comparison - `internal/web/middleware.go:101, 115, 286, 299, 346-347`, and for the metrics bearer token at `internal/web/server.go:321` |
| SMTP password at rest | AES-256-GCM, key derived by HKDF-SHA256 from the admin password hash with a random 16-byte per-install salt and domain-separation info string - `internal/web/crypto.go:42-73`, `internal/notify/crypto.go:30-66`. Re-encrypted on password change (`ReEncryptAllData`) |
| SQL | Parameterised throughout; no string-built queries. Project rule in `CLAUDE.md` |
| Session tokens | 32 random bytes from `crypto/rand`, 7-day expiry (`internal/web/middleware.go:70`), pruned on a ticker (`main.go:1944`), and **all** tokens invalidated on password change (`DeleteAllAuthTokens`) |
| Login rate limiting | Per-IP: 5 failures inside a 5-minute window blocks that IP for 5 minutes, `Retry-After: 300` returned - `internal/web/middleware.go:23-27, 436-500`, enforced at `internal/web/handlers.go:7337-7377`. Bounded at 1000 tracked IPs with oldest-evicted, so it cannot be used to exhaust memory |
| Data directory | Created `0700` - `main.go:911`, `internal/config/config.go:995` |
| Debug log | Created and `chmod`-ed to `0600` - `internal/config/config.go:1019-1029`. It records client IP addresses and provider account names, hence the mode |
| `.env` | Written `0600` by the setup wizard - `setup.go:516, 691` |
| Codex profile files | Written `0600` in a `0700` directory - `internal/web/handlers.go:369, 408, 543` |
| Security headers | `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` (except the deliberately frameable quick-view path), `Referrer-Policy: strict-origin-when-cross-origin` - `internal/web/server.go:277-292` |
| Content Security Policy | `default-src 'self'` with `script-src 'self'`, `font-src 'self'`, `connect-src 'self'`, `worker-src 'self'` and no third-party host whitelisted anywhere - `internal/web/server.go:294-301` |
| No third-party subresources | Chart.js, its date adapter and the Ubuntu / JetBrains Mono webfonts are vendored into the binary under `internal/web/static/vendor/` and served from the dashboard's own origin. A dashboard page load - including the unauthenticated `/login` page - discloses the viewer's IP address to no CDN or font host. A test (`internal/web/no_external_assets_test.go`) fails the build if a template regresses to an external reference |
| Version check | Off-switchable. Settings -> General -> "Privacy & Outbound Connections" -> "Update check", or `ONWATCH_UPDATE_CHECK=false` to pin it off for the whole deployment (`internal/config/config.go:511-515`, `internal/web/handlers.go:7494-7500`). When off, onWatch makes **no** request to `api.github.com` at all. When on, the `User-Agent` discloses the exact installed version |
| Trusted-proxy auth | `ONWATCH_AUTH_MODE=trusted_proxy` reads the peer from `r.RemoteAddr` and checks it against `ONWATCH_TRUSTED_PROXY_CIDRS` - it does **not** trust `X-Forwarded-For` for the trust decision (`internal/web/trusted_proxy.go:37-50`). It also requires exactly one instance of the identity header, so an append-mode proxy cannot let a client smuggle a second identity (`:54-58`). With no CIDRs or no header configured the constructor returns `nil` and auth fails closed to local password auth (`:28-33`) |
| Secure cookies | `ONWATCH_SECURE_COOKIES=true` sets the `Secure` flag; it is also set automatically when the bind host is neither `0.0.0.0` nor `127.0.0.1` (`internal/web/handlers.go:7392`) |
| Metrics token | Bearer token via `ONWATCH_METRICS_TOKEN`, compared in constant time - `internal/web/server.go:313-325` |
| No telemetry | There is no analytics, no crash reporting and no phone-home. The onWatch project receives nothing. Greps for `sentry`, `posthog`, `mixpanel`, `gtag` and `plausible` return nothing |

## Known hardening gaps

These are real and unpatched as of this writing. They are listed here rather
than left for you to find. Each row has the mitigation to apply today.

### 1. The default bind address is all interfaces - FIXED

`ONWATCH_HOST` now defaults to `127.0.0.1`, so a fresh install is reachable
only from the machine it runs on (`internal/config/secure_defaults.go`,
`EffectiveHost`). A container still defaults to `0.0.0.0`, because there the
network namespace is the boundary and a published port does not work
otherwise. An explicit `ONWATCH_HOST` always wins.

**If you are on an older version:** set `ONWATCH_HOST=127.0.0.1` unless you
specifically need network access, and firewall the port either way.

### 2. The default dashboard password is `changeme` - MITIGATED

`DefaultAdminPass` is still the literal string `changeme`
(`internal/config/config.go`), but onWatch now **refuses to start** when that
password would be served on a non-loopback address
(`ValidateNetworkExposure`). The error names both remedies. Set
`ONWATCH_ALLOW_DEFAULT_PASSWORD=true` to override on a trusted private network.

A loopback-only install still runs on the default password, so it remains
worth changing: anyone with an account on that machine can reach it.

**Mitigation:** set `ONWATCH_ADMIN_PASS` in `.env` before first start, or
change it immediately in the dashboard under Settings -> General -> Password.
Changing it also rotates the SMTP encryption key, re-keys the stored provider
credentials, and invalidates every existing session token.

### 3. `/metrics` is unauthenticated unless you set a token - FIXED

`/metrics` is no longer registered at all unless `ONWATCH_METRICS_TOKEN` is
set, or `ONWATCH_METRICS_PUBLIC=true` is passed to serve it open deliberately
(`internal/web/server.go`). It previously served with no authentication
whenever no token was set, which on the old `0.0.0.0` default was an
unauthenticated read of who uses which provider and how much: the endpoint
exposes per-account quota data, utilisation and reset timings, and the
`accountInfo` join metric maps `account_id` to a human-readable
`account_name` (`internal/metrics/metrics.go:51-53`).

**If you scrape it:** set `ONWATCH_METRICS_TOKEN` to a long random value and
send it as a bearer token. `ONWATCH_METRICS_PUBLIC=true` restores the old
behaviour if your scraper cannot send one, but then block `/metrics` at the
reverse proxy.

### 4. `ANTIGRAVITY_BASE_URL` is an unvalidated destination for a bearer token

`ANTIGRAVITY_BASE_URL` is read straight from the environment
(`internal/config/config.go:381`) and used as the client's base URL
(`main.go:1055-1063`). It is not validated, not restricted to loopback, and not
checked against a scheme allowlist. The Antigravity CSRF token is then sent to
whatever host it names, over plain HTTP if that is what the URL says. Anyone who
can write the environment of the daemon - a compromised `.env`, a bad
`docker-compose.yml`, a malicious `EnvironmentFile` - can exfiltrate that
credential. The same setting is writable from the dashboard settings API
(`internal/web/handlers.go:1548`), so it is reachable by anyone who has the
dashboard password.

**Mitigation:** leave `ANTIGRAVITY_BASE_URL` unset unless you are running in
Docker and need it. When you do set it, point it at a loopback or in-cluster
address you control, and treat write access to the daemon's environment and to
the dashboard as equivalent to holding the token.

### 5. SMTP protocol `none` sends mail credentials in plaintext

Setting the SMTP protocol to `none` disables TLS. If authentication is also
enabled, the username and password go over the wire in the clear. onWatch warns
at mailer construction (`internal/notify/smtp.go:35-38`) and sends anyway.

**Mitigation:** use `tls` or `starttls`. Only use `none` for an authentication-free
relay on `127.0.0.1`. Never use `none` with credentials against a remote host.

### 6. The installers do not verify what they download

`install.sh` and `install.ps1` fetch the release binary over HTTPS with no
checksum and no signature check - there is no `sha256sum`, `shasum` or
`Get-FileHash` anywhere in either script, even though the release workflow
publishes a `SHA256SUMS` file. TLS is the only integrity guarantee.

**Mitigation:** download the binary and `SHA256SUMS` from the release page and
verify manually, or build from source, or use `nix build .#onwatch`.

### 7. The `.bak` credential copies are never cleaned up

When onWatch refreshes a provider's OAuth token it rewrites that provider's
credential file and leaves a `.bak` copy beside it -
`~/.claude/.credentials.json.bak`, `~/.codex/auth.json.bak` and the OpenCode
equivalent. Nothing ever deletes them, so a stale copy of a refresh token can
outlive the credential it backed up.

**Mitigation:** treat `~/.claude`, `~/.codex`, `~/.gemini`, `~/.grok` and the
OpenCode and Kimi credential directories as secret material, keep them `0700`,
and delete `.bak` files yourself when you rotate a provider credential.

### 8. `getClientIP` trusts client-supplied forwarding headers

`getClientIP` (`internal/web/security.go:107-129`) returns
`X-Forwarded-For` (first element) or `X-Real-Ip` before falling back to
`RemoteAddr`. Nothing checks that the request came from a trusted proxy first,
so when onWatch is reached directly the client chooses the IP that gets
rate-limited and written to the log. An attacker can vary the header to sidestep
the per-IP login block and to poison the audit trail. Note this is a *different*
code path from trusted-proxy authentication, which correctly uses `RemoteAddr`
(`internal/web/trusted_proxy.go:37`).

**Mitigation:** put onWatch behind a reverse proxy that strips inbound
`X-Forwarded-For` and `X-Real-Ip` and sets them itself, and do not rely on the
login rate limiter as your only brute-force defence on an exposed install. Add
`fail2ban` or an equivalent on the proxy's own access log.

### 9. The IP allowlist is not wired up

`IPWhitelistMiddleware` exists (`internal/web/security.go:137-186`) but **no
configuration option reaches it and nothing constructs it** - there is no caller
of `NewIPWhitelistMiddleware` outside tests, and no `ONWATCH_*` variable for it.
Do not plan around it. Earlier documentation describing an optional IP allowlist
is describing code that is not reachable today. It also depends on `getClientIP`,
so it would inherit gap 8.

**Mitigation:** do the allowlisting in your firewall, or in the reverse proxy
(`allow`/`deny` in nginx, `IPAllowList` in Traefik).

### 10. Secrets stored unencrypted in the database

The SQLite database file itself is not encrypted, so what protects it is the
`0700` data directory and whatever the filesystem gives you.

Encrypted at rest, with AES-256-GCM under a key derived from the dashboard
password: the SMTP password, the `gemini_tokens` setting (OAuth access **and**
refresh token) and `provider_accounts.metadata` (the provider API key). All
three are re-keyed when the dashboard password changes. Values written by an
earlier version are read as cleartext and encrypted the next time they are
written.

Still cleartext: the `provider_settings` setting (provider API keys, the
Copilot token, the Antigravity CSRF token, the OpenCode auth cookie) and
`vapid_keys`. Encrypting those means changing every field-level read site and
has not been done. Until it is, treat `onwatch.db` as a file that still
contains live provider credentials.

**Mitigation:** keep the data directory on an encrypted volume (see the
checklist), keep it `0700`, and never copy the database anywhere you would not
copy a password file. Anyone who reads `onwatch.db` can use your provider
accounts.

### 11. `system_alerts` grows forever

`ClearOldSystemAlerts` had no callers, so `system_alerts` grew forever. It is
now driven by the retention agent (`internal/agent/retention_agent.go`), along
with `api_integration_ingest_state`, which was never pruned at all.

Retention is still **off by default** - both periods are 0, meaning keep
everything, so that an upgrade never deletes an existing install's history.
Choosing a period is the operator's decision.

**Mitigation:** set a retention period in Settings -> General -> Data
Retention, or with `ONWATCH_RETENTION_SCRUB_DAYS` /
`ONWATCH_RETENTION_DELETE_DAYS`.
### Backups

- Back up `~/.onwatch/data/onwatch.db` with `sqlite3 onwatch.db ".backup out.db"`
  or `VACUUM INTO`, not `cp` - a plain copy of a live database can catch a
  torn write, and misses the `-wal` file.
- **A backup of `onwatch.db` is a backup of your provider credentials.** Encrypt
  it at rest (`age`, `gpg`, or an encrypted destination) and control who can
  read it exactly as you would a password vault export.
- Back up `.env` separately, with the same care.
- Every backup is outside any in-product deletion. When you erase data in
  onWatch, your backups still have it until they age out. Keep backup retention
  short and write it down.

### Destroying the database properly

Deleting the row is not deleting the data. SQLite leaves freed pages in the file
and `DROP COLUMN` does not reclaim them - a pre-upgrade database still contains
whole raw event lines in pages belonging to the dropped
`api_integration_usage_events.raw_line` column.

To remove data for real:

```bash
# 1. Stop the daemon.
onwatch --stop

# 2. Reclaim freed pages so deleted rows are not recoverable from slack space.
sqlite3 ~/.onwatch/data/onwatch.db "VACUUM;"

# 3. Or destroy the whole thing - note the -wal and -shm files.
rm -f ~/.onwatch/data/onwatch.db ~/.onwatch/data/onwatch.db-wal ~/.onwatch/data/onwatch.db-shm
```

Then deal with everything outside the database, which no `VACUUM` touches:

| Also remove | Why |
|-------------|-----|
| `~/.onwatch/data/.onwatch.log` and its `.1`/`.2`/`.3` rotations | IP addresses, provider account names, proxy-asserted usernames |
| `~/.onwatch/api-integrations/*.jsonl` | The original telemetry input files. onWatch only tails them and **never** deletes or truncates them, so they outlive any database-side erasure |
| `~/.onwatch/data/codex-profiles/*.json` | Full Codex OAuth tokens with `account_id` and `chatgpt_user_id` |
| `~/.onwatch/quickview-profile/` | A full browser profile containing the `onwatch_session` cookie. Never cleaned automatically |
| `~/.onwatch/.env` | Provider API keys and the dashboard password |
| `.bak` credential copies in `~/.claude`, `~/.codex` and the OpenCode directory | Stale refresh tokens (gap 7) |
| Your backups | Nothing in-product reaches them |

On an SSD, `rm` does not guarantee the bytes are gone. If that matters, the
answer is full-disk encryption applied *before* the data was written, and
destroying the key - not a file shredder.

## Breach detection and response

### Signals available today

onWatch is not an intrusion-detection system. These are the signals it actually
produces:

| Signal | Where | What it may mean |
|--------|-------|------------------|
| Repeated login failures, then `Retry-After: 300` on `/login` | Access log / proxy log; the limiter itself does not write a log line per block (`internal/web/middleware.go:436-500`) | Brute-force attempt against the dashboard |
| `USING DEFAULT PASSWORD` | Daemon log at startup (`main.go:963-965`) | The install is open to anyone who reaches the port |
| `Dashboard is reachable from your network` | Daemon log at startup (`main.go:358-368`) | Non-loopback bind |
| `IP not in whitelist` | Only if the allowlist middleware is ever wired up (`internal/web/security.go:161`) - **not emitted today**, see gap 9 | Blocked source |
| `Auth rejected` / `Unauthenticated request, redirecting to login` with `remote` | Daemon log at **debug** level (`internal/web/middleware.go:311, 323`) - set `ONWATCH_LOG_LEVEL=debug` to see them | Unauthenticated probing, with the source address |
| `auth_error`, `token_refresh_failed`, `polling_paused` system alerts | Dashboard alerts, `system_alerts` table (`internal/store/store.go:2447`) | A provider rejected a credential. Could be an expired token - or a credential that was stolen and revoked, or that someone else is now using |
| `metrics endpoint is unauthenticated` | Daemon log at startup (`internal/web/server.go:118`) | `/metrics` is open |

### What an operator should watch

- **`auth_error` bursts across several providers at once.** One provider failing
  is routine. All of them failing together is consistent with credential
  compromise and mass revocation, and deserves a look at the provider consoles.
- **Provider-side session and activity logs.** These are the strongest signal
  you have, and they are not in onWatch. If a provider shows API activity from
  an IP that is not your daemon, the credentials in `onwatch.db` are compromised.
- **Unexpected quota consumption** in the dashboard - someone else using your
  key shows up as usage you cannot account for.
- **Modification times on `onwatch.db` and `.env`** when the daemon was not
  running, and on the third-party credential files onWatch rewrites.
- **Failed logins from addresses you do not recognise** - remembering gap 8, the
  logged IP is attacker-controllable when onWatch is reached directly.
- **`onwatch.db` size growth** - `system_alerts` and the provider snapshot
  tables are unbounded today (gap 11).

There is no alerting on any of this out of the box. If you need it, ship the
daemon log to whatever you already use for log alerting.

### If you are breached

1. Stop the daemon (`onwatch --stop`) and take the host off the network.
2. **Rotate every provider credential.** Assume all of them leaked - most are in
   `onwatch.db` in cleartext (gap 10), and the OAuth refresh tokens in the
   third-party credential files onWatch touches let an attacker mint fresh
   access tokens indefinitely. Revoke at the provider, not just locally.
3. Change the dashboard password. This also invalidates every session token
   (`DeleteAllAuthTokens`) and re-keys the encrypted SMTP password.
4. Rotate the SMTP credentials and the VAPID keys.
5. Preserve `~/.onwatch/data/.onwatch.log` and its rotations, and your proxy
   access logs, before they roll over. The log is size-capped at 4 x 50 MB with
   no time limit, so it will not age out on a schedule - but it will roll.
6. Work out what personal data was in scope. `docs/PRIVACY.md` has the
   field-level inventory; `docs/COMPLIANCE.md` has the notification workflow.

### Notification duties

**These deadlines bind whoever deployed onWatch, not the onWatch project.** The
project has no access to your database, receives no telemetry, and cannot detect
or notify anyone on your behalf. If you run onWatch for an organisation and it
holds data about identifiable people - your employees' provider accounts, their
email addresses, their usage history - the duty is yours.

| Regime | Duty | Deadline |
|--------|------|----------|
| GDPR Art. 33 | Notify your supervisory authority | **72 hours** from becoming aware, unless the breach is unlikely to result in a risk to rights and freedoms. Late notification must be accompanied by reasons for the delay |
| GDPR Art. 33(5) | Document every breach internally, notified or not | Immediately, and retain |
| GDPR Art. 34 | Notify the affected data subjects | **Without undue delay**, where the breach is likely to result in a **high risk** to their rights and freedoms |
| DPDP Act 2023, s.8(6) | Notify the **Data Protection Board of India** and **each affected Data Principal** | As prescribed by the DPDP Rules. India's rule does not carry GDPR's risk threshold - plan on notifying |

A leaked `onwatch.db` is not a minor breach. It contains working credentials for
the affected people's AI provider accounts, which is unauthorised access to
their accounts at third parties, plus their email addresses and a detailed
history of their usage. Assume the high-risk threshold in Art. 34 is met and
that individual notification is required.

Work out which authority you answer to, and who signs the notification, *before*
you need it. `docs/COMPLIANCE.md` covers the roles and the paperwork.

## Contributing security fixes

- Follow the TDD rule in `CLAUDE.md`: test first, watch it fail, then fix.
- `./app.sh --test` and `go vet ./...` must pass. `-race` is mandatory before
  commit.
- Never add a credential, an IP address or a hostname to a log line.
- Parameterised SQL only.
- Do not add a new outbound destination without saying so in the PR - the
  outbound-call surface is documented and reviewed.
- Do not weaken the CSP or add a third-party subresource.
  `internal/web/no_external_assets_test.go` will stop you, and that test exists
  on purpose.
