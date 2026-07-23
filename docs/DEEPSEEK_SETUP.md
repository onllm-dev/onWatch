# DeepSeek Setup

onWatch tracks your **DeepSeek platform** API account balance. This is a **balance-based** provider (remaining credits), not a subscription quota.

## What it tracks

onWatch polls `GET https://api.deepseek.com/user/balance` and renders three balance cards:

| Card | Field | Meaning |
|------|-------|---------|
| Total Balance | `total_balance` | Total spendable balance |
| Granted | `granted_balance` | Promotional / granted credits |
| Topped Up | `topped_up_balance` | Paid top-up balance |

Trends show the balance **drop rate** over time so you can see how fast credits are being consumed.

## Setup

1. Create an API key in the [DeepSeek platform console](https://platform.deepseek.com/api_keys).
2. Provide it to onWatch via environment variable (or your `.env` file):

   ```bash
   DEEPSEEK_API_KEY=sk-...
   ```

3. Restart onWatch. The **DeepSeek** tab appears automatically once the key is set.

## Notes

- The provider is **opt-in**: it only activates when `DEEPSEEK_API_KEY` is set.
- The API key is used only as a `Bearer` token against `api.deepseek.com` and is **never logged** (redacted in debug output).
