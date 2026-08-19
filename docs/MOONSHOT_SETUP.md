# Moonshot Setup

onWatch tracks your **Moonshot (Kimi) open-platform** API account balance. This is a **balance-based** provider (remaining credits), not a subscription quota - for the Kimi Code CLI subscription quotas, see [Kimi Setup](KIMI_SETUP.md).

## What it tracks

onWatch polls `GET https://api.moonshot.ai/v1/users/me/balance` and renders three balance cards:

| Card | Field | Meaning |
|------|-------|---------|
| Available | `available_balance` | Total spendable balance |
| Voucher | `voucher_balance` | Promotional / granted voucher credits |
| Cash | `cash_balance` | Paid cash balance |

Trends show the balance **drop rate** over time so you can see how fast credits are being consumed.

## Setup

1. Create an API key in the [Moonshot platform console](https://platform.moonshot.ai/).
2. Configure it in **Settings -> Providers -> Moonshot**, or provide it via an environment variable (or your `.env` file):

   ```bash
   MOONSHOT_API_KEY=sk-...
   ```

3. Restart onWatch. The **Moonshot** tab appears automatically once the key is set.

Keys saved from the dashboard override `MOONSHOT_API_KEY`. They are encrypted in SQLite with AES-256-GCM, and settings API responses only report whether a key is configured. The encryption key comes from `ONWATCH_CREDENTIAL_KEY` or the generated `<database>.credential-key` sidecar. Back up that sidecar together with the database; losing it makes dashboard-stored credentials unrecoverable.

## Notes

- The provider is **opt-in**: it only activates when a dashboard or environment API key is set.
- The API key is used only as a `Bearer` token against `api.moonshot.ai` and is **never logged** (redacted in debug output).
