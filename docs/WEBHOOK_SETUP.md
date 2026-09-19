# Webhook Notifications

onWatch can POST a JSON payload to any HTTP endpoint when a quota crosses a
threshold, resets, or hits an authentication problem. This is the integration
path for ntfy, Gotify, Slack- or Discord-compatible receivers, home automation,
and custom monitoring services.

Webhook delivery is a third delivery channel alongside email and push. It can be
used on its own - SMTP does not need to be configured.

## Setup

1. Open **Settings -> Notifications**.
2. Under **Delivery Channels**, enable **Webhook (HTTP)**.
3. Under **Webhook Configuration**, set the endpoint URL.
4. Choose which events to deliver.
5. Save, then use **Send Test Webhook** to verify the endpoint.

The test button uses the *saved* configuration, so save before testing.

### Fields

| Field | Notes |
|---|---|
| Endpoint URL | `http` or `https`. Loopback and private addresses are allowed so self-hosted receivers work. |
| Bearer Token | Optional. Sent as `Authorization: Bearer <token>`. Encrypted at rest with AES-256-GCM. |
| Custom Headers | Optional, one `Name: value` per line, up to 10 headers. **Stored in plaintext** - see Security notes. |
| Timeout | 1-15 seconds. This is the **total** budget across all retry attempts. |
| Retries | 0-5. Applied only to timeouts, `429`, and `5xx`. |

### Events

| Event | Fires when |
|---|---|
| `warning` | A quota crosses the warning threshold |
| `critical` | A quota crosses the critical threshold |
| `reset` | A quota cycle resets |
| `auth_error` | A provider token refresh or authentication fails |
| `starter_success` | The Codex auto quota-starter successfully started a limit window |
| `starter_failure` | The Codex auto quota-starter ping failed |
| `test` | You press **Send Test Webhook** |

## Payload

`POST` with `Content-Type: application/json`:

```json
{
  "event": "critical",
  "provider": "anthropic",
  "quota_key": "five_hour",
  "account_id": "2",
  "utilization": 96.4,
  "limit": 100,
  "threshold": 95,
  "reset_at": "2026-09-14T18:00:00Z",
  "title": "[CRITICAL] Anthropic quota five_hour at 96.4%",
  "message": "Provider: anthropic\nQuota: five_hour\n...",
  "timestamp": "2026-09-14T13:42:07Z"
}
```

Optional fields are omitted rather than sent as zero. `reset_at` appears only
when the provider reports a reset time. `quota_key`, `utilization`, `limit`, and
`threshold` are absent on auth-error and starter events.

**The payload never contains credentials** - no OAuth tokens, API keys, or the
webhook bearer token itself.

## Example: ntfy

ntfy accepts an arbitrary request body as the message text and reads the
notification title, priority, and tags from request headers.

For hosted ntfy, set the endpoint to your topic URL:

```
https://ntfy.sh/my-onwatch-topic
```

For a self-hosted instance on the same machine:

```
http://localhost:8080/my-onwatch-topic
```

Useful custom headers:

```
X-Title: onWatch quota alert
X-Priority: high
X-Tags: warning,onwatch
```

If your ntfy instance requires authentication, put the access token in the
**Bearer Token** field.

Subscribe with the ntfy app or the CLI:

```bash
ntfy subscribe my-onwatch-topic
```

## Example: Gotify

Gotify reads `title` and `message` from the body. It accepts the application
token as a query parameter:

```
http://localhost:8080/message?token=YOUR_APP_TOKEN
```

Note that the URL, including the query string, is stored in plaintext. If your
Gotify version accepts `Authorization: Bearer <app token>`, prefer putting the
token in the **Bearer Token** field instead, which is encrypted at rest.

## Delivery behaviour

Delivery is synchronous with a strict total time budget. Notifications are
deduplicated per quota cycle, so a poll issues no webhook requests at all in the
steady state and at most one per newly crossed threshold.

- **Total budget**: the timeout covers every retry attempt. Retries never extend
  how long a poll is blocked beyond the configured timeout.
- **Retry policy**: timeouts, `429`, and `5xx` are retried with backoff. Other
  `4xx` responses are treated as configuration errors and are not retried.
- **Circuit breaker**: after 3 consecutive failures, delivery pauses for 5
  minutes so an unreachable endpoint cannot slow every poll. A success clears the
  failure streak.
- **Redirects are not followed.** A `3xx` response is treated as a failure, since
  the target is user-supplied and a redirect could send the payload elsewhere.
- **Failed deliveries are retried on the next poll.** A notification is only
  marked as sent for the cycle once at least one channel succeeds.
- **Each alert fires once per quota cycle** by default. Enable **Repeat alerts**
  under Settings -> Notifications to keep being alerted while a quota stays over
  its threshold; the **Cooldown** setting is then the minimum gap between those
  repeats. Repeats apply to every channel, not just webhooks.

## Security notes

- The bearer token is encrypted at rest using a key derived from the admin
  password hash, the same scheme used for SMTP passwords, and is re-keyed
  automatically when the admin password changes.
- **Only the Bearer Token field is encrypted.** The endpoint URL and custom
  headers are stored in plaintext and returned by the settings API, so do not
  put credentials in a header (such as `X-Gotify-Key` or `Authorization:
  Basic ...`) or in the URL query string if you can avoid it. Where a service
  accepts a bearer token, use the Bearer Token field.
- Private and loopback endpoint addresses are intentionally permitted, because
  self-hosted receivers are the primary use case. onWatch will POST to whatever
  host you configure, so only point it at endpoints you control.
- Custom header names and values are rejected if they contain control characters.

## API

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/settings` | PUT | Save webhook config under the `webhook` key |
| `/api/settings` | GET | Read config back; the bearer token is masked as `bearer_token_set` |
| `/api/settings/webhook/test` | POST | Send a test payload (10 second cooldown) |
