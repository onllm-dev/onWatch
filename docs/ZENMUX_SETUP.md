# Zenmux setup

onWatch reads the [Zenmux subscription detail API](https://zenmux.ai/docs/api/platform/subscription-detail.html) and the [personal account usage API](https://zenmux.ai/docs/api/platform/account-usage.html). Both use read-only GET requests. onWatch does not send model inference requests.

1. Create a **Management API Key** in the Zenmux Console. For account usage, it must be a directly created personal account management key. Standard inference keys, organization management keys and OAuth tokens do not work for the account usage endpoint.
2. Configure `ZENMUX_MANAGEMENT_API_KEY` in your onWatch environment, or save the key in Settings > Providers > Zenmux. Settings changes take effect immediately. Set `ZENMUX_ENABLED=false` to disable polling even if a key is present.
3. Enable Zenmux telemetry in Settings > Providers if it is paused. Polling uses the normal onWatch polling interval.

Protect the key like a password. The environment file should be readable only by its owner. The Settings mechanism stores provider keys in the onWatch SQLite database, as it does for other providers; secure the database and its backups accordingly. Settings responses do not return saved key values. onWatch does not auto-detect Zenmux keys or forward them to redirects.

The 5-hour and 7-day cards show values returned by Zenmux: Flow usage, limits, utilization and natural reset when available. The display preference selects used or available percentage; status colors always reflect used percentage. The 5-hour card also shows remaining early-reset attempts if reported. Unknown values are shown as unavailable, not zero. When sufficient continuous, recent samples share the same known reset and limit, cards show an approximate local usage rate and, if it precedes reset, approximate time to exhaustion. These estimates can change with usage. History charts and Prometheus utilization metrics include only reported rolling-window usage.

Zenmux reports **monthly limits only**. The monthly card does not show usage, remaining quota, utilization or a reset. The plan expiration date is displayed separately and is not a monthly quota reset. A missing reset means Zenmux did not provide one. HTTP 422 indicates the documented management API rate limit; onWatch waits until the next scheduled poll.

## Model usage history

The Zenmux dashboard shows personal account usage across all API keys, grouped by model by default, with an optional interval view. Choose a calendar month (daily buckets), day (hourly buckets) or UTC hour (minute buckets). Billing defaults to subscription; select all billing types, PAYG, or subscription fallback to PAYG when needed. Zenmux does not specify a retention limit or guaranteed freshness for this endpoint. A successful empty result does not prove that historical data exists for the chosen period. `Unknown` means Zenmux did not return a statistic, while `0` is a reported zero. Bucket labels are Zenmux business-time identifiers, not individual request timestamps; the hour query is explicitly UTC.

Select a model and use **Show endpoints** for Zenmux's separate endpoint statistics over the whole selected period. Available statistics include the main and fastest endpoints, request share and selected-model success rate. These endpoint totals are not a per-bucket or per-request attribution, and Zenmux does not supply a model-to-key cross-tab in the displayed usage groups. Subscription usage is distinct from PAYG and fallback. Combined results include all three billing types and must not be interpreted as a cash charge or as a model allocation of Flow quota windows.

These are aggregates, not request logs. Zenmux's documented [get-generation API](https://zenmux.ai/docs/api/platform/get-generation.html) requires an existing generation ID and an inference API key; it does not list requests. onWatch does not access browser sessions or invent individual records.

Account analytics is fetched when the Zenmux page opens or when the user changes the period, billing filter or selected model and requests a refresh. It is not persisted, and an analytics error does not stop quota polling. Zenmux documents a shared Usage/Cost limit of 60 requests per minute per personal account, plus management-wide limits. onWatch serializes these UI requests and allows at most one every 2 seconds per process; other applications share the provider limit. If limited, wait and retry. Results over 1 MiB or 2,000 model/endpoint rows are rejected rather than silently truncated. The account usage API does not document pagination.

Usage threshold alerts apply only to reported rolling-window percentages. An alert is re-armed when consecutive polls report a later natural reset time together with a passed previous reset or a usage drop. If those signals are missing, onWatch cannot confirm a rollover and does not re-arm the alert automatically. Reset-cycle analytics are not currently provided for Zenmux.
