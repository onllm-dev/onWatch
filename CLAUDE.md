# onWatch

Go CLI for AI quota tracking. Polls 8 providers → SQLite → Material Design 3 dashboard.

## Task

Background daemon (<50MB RAM) tracking: Anthropic, Synthetic, Z.ai, Copilot, Codex, MiniMax, Antigravity, Gemini.

## Code Map

```
main.go                     # CLI entry, daemon lifecycle
internal/
├── api/                    # HTTP clients + types per provider
│   └── {provider}_client.go, {provider}_types.go
├── store/                  # SQLite persistence per provider
│   └── store.go (schema), {provider}_store.go
├── tracker/                # Poll orchestration per provider
├── agent/                  # Background polling agents
├── web/                    # Dashboard server
│   ├── handlers.go         # API endpoints
│   ├── static/             # Embedded JS/CSS (embed.FS)
│   └── templates/          # HTML templates
├── config/                 # Config + container detection
└── notify/                 # Email + push notifications
```

## Objectives

1. **TDD-first**: Test → fail → implement → pass
2. **RAM-bounded**: 40MB limit, single SQLite conn, lean HTTP
3. **Single binary**: All assets via `embed.FS`

## Operations

**Always use `app.sh` for build and test - never run `go build` or `go test` directly.**

```bash
./app.sh --build            # Build production binary
./app.sh --test             # Run all tests with race detection and coverage
./app.sh --smoke            # Quick validation: vet + build check + short tests
go test -race ./... && go vet ./...   # Pre-commit (mandatory)
```

## Guardrails

| Rule | Reason |
|------|--------|
| Never commit `.env`, `.db`, binaries | Security |
| Never log API keys | Security |
| Parameterized SQL only | Injection prevention |
| `context.Context` always | Leak prevention |
| `-race` before commit | Data race detection |
| `subtle.ConstantTimeCompare` for creds | Timing attacks |
| Bounded queries (cycles≤200, insights≤50) | Memory caps |

## Notes

**Adding a provider:**
1. `internal/api/{provider}_client.go` + `_types.go`
2. `internal/store/{provider}_store.go`
3. `internal/tracker/{provider}_tracker.go`
4. `internal/agent/{provider}_agent.go`
5. Add to `internal/web/handlers.go` endpoints
6. Update dashboard JS in `internal/web/static/app.js`

**API Docs:** See `docs/` for provider-specific setup (COPILOT_SETUP.md, CODEX_SETUP.md, ANTIGRAVITY_SETUP.md, GEMINI_SETUP.md)

**Containers:** `IsDockerEnvironment()` in `config.go` detects Docker/K8s. Containers run foreground only.

**Release:** `./app.sh --release` → cross-compile 5 platforms → include all binaries in GitHub release.

**Anthropic Rate Limit Bypass:** Anthropic's usage API has aggressive rate limits (~5 requests per token, then 429 for ~5 min). onWatch bypasses this by refreshing the OAuth token when rate limited - each new access token gets a fresh rate limit window. Implementation details:
- `internal/agent/anthropic_agent.go`: Detects 429, calls `RefreshAnthropicToken`, saves new tokens, retries
- `internal/api/anthropic_oauth.go`: OAuth token refresh endpoint (`console.anthropic.com/v1/oauth/token`)
- `internal/api/anthropic_token_unix.go`: Writes to macOS Keychain + file for persistence
- `internal/api/anthropic_token_windows.go`: Writes to credentials file
- Refresh tokens are one-time use (OAuth rotation) - MUST save new refresh token after each refresh
- See: [issue #16](https://github.com/onllm-dev/onWatch/issues/16), [anthropics/claude-code#31021](https://github.com/anthropics/claude-code/issues/31021)

## Style

- Use `-` (hyphen) instead of `—` (em dash) in all text
