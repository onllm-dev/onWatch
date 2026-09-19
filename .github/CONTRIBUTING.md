# Contributing to onWatch

Thanks for your interest in contributing!

## Quick Start

1. Fork and clone the repo
2. Copy `.env.example` to `.env` and add at least one API key
3. Run `./app.sh --build` to build
4. Run `./app.sh --test` to verify tests pass

## Development

- Go 1.25+ required
- All tests must pass with `-race` flag
- Run `go vet ./...` before submitting

## What We Need Help With

- New provider integrations (see below)
- Dashboard UI improvements
- Documentation and tutorials
- Bug reports with reproduction steps

## Adding a New Provider

Each provider needs 4 files + 2 updates:

1. `internal/api/{provider}_client.go` + `_types.go`
2. `internal/store/{provider}_store.go`
3. `internal/tracker/{provider}_tracker.go`
4. `internal/agent/{provider}_agent.go`
5. Add endpoints in `internal/web/handlers.go`
6. Add dashboard tab in `internal/web/static/app.js`

Look at any existing provider for the pattern.

## Pull Requests

- Keep PRs focused on a single change
- Include tests for new functionality
- Update docs if adding user-facing features
