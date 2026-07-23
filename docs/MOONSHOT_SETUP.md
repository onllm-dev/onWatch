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
2. Provide it to onWatch via environment variable (or your `.env` file):

   ```bash
   MOONSHOT_API_KEY=sk-...
   ```

3. Restart onWatch. The **Moonshot** tab appears automatically once the key is set.

## Notes

- The provider is **opt-in**: it only activates when `MOONSHOT_API_KEY` is set.
- The API key is used only as a `Bearer` token against `api.moonshot.ai` and is **never logged** (redacted in debug output).
