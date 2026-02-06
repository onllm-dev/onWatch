# Z.ai API Integration Reference

> **Purpose:** This document captures all tested Z.ai monitoring API endpoints, their request/response shapes, and a roadmap for future SynTrack integration. No implementation yet — reference only.
>
> **Tested on:** 2026-02-06

---

## Authentication

- **Header:** `Authorization: <ZAI_API_KEY>`
- **Key source:** Environment variable `ZAI_API_KEY` (stored in `.env`, never committed)
- **Error on bad auth:** HTTP 200 with JSON body `{"code":401,"msg":"token expired or incorrect","success":false}`
  - Note: The API returns HTTP 200 even for auth failures — error detection must check `code` field, NOT HTTP status.

---

## Base URLs

Both return **identical data** — either can be used:

| Platform | Base URL |
|----------|----------|
| Z.ai (primary) | `https://api.z.ai/api` |
| ZHIPU (mirror) | `https://open.bigmodel.cn/api` |

All endpoints below are relative to these base URLs.

---

## Endpoint 1: Model Usage (Time-Series)

### Request

```
GET /monitor/usage/model-usage?startTime={start}&endTime={end}
```

| Parameter | Format | Required | Example |
|-----------|--------|----------|---------|
| `startTime` | `YYYY-MM-DD HH:mm:ss` | Yes | `2026-02-05 00:00:00` |
| `endTime` | `YYYY-MM-DD HH:mm:ss` | Yes | `2026-02-06 23:59:59` |

**Without time params:** Returns empty/invalid response. Always provide both.

### Example Request

```bash
curl -s "https://api.z.ai/api/monitor/usage/model-usage?startTime=2026-02-05%2000:00:00&endTime=2026-02-06%2023:59:59" \
  -H "Authorization: $ZAI_API_KEY" \
  -H "Accept-Language: en-US,en" \
  -H "Content-Type: application/json"
```

### Response Shape

```json
{
  "code": 200,
  "msg": "Operation successful",
  "success": true,
  "data": {
    "x_time": [
      "2026-02-05 00:00",
      "2026-02-05 01:00",
      "..."
    ],
    "modelCallCount": [null, null, 20, 55, null, "..."],
    "tokensUsage": [null, null, 144154, 233640, null, "..."],
    "totalUsage": {
      "totalModelCallCount": 10296,
      "totalTokensUsage": 360784945
    }
  }
}
```

### Field Reference

| Field | Type | Description |
|-------|------|-------------|
| `x_time` | `string[]` | Hourly time buckets (`YYYY-MM-DD HH:mm`) |
| `modelCallCount` | `(number\|null)[]` | API calls per hour. `null` = no activity |
| `tokensUsage` | `(number\|null)[]` | Tokens consumed per hour. `null` = no activity |
| `totalUsage.totalModelCallCount` | `number` | Sum of all calls in the time range |
| `totalUsage.totalTokensUsage` | `number` | Sum of all tokens in the time range |

### Observations

- **Granularity:** Always hourly, regardless of range size
- **Max tested range:** 30 days (744 data points) — works fine
- **Null handling:** Inactive hours return `null`, not `0`
- **Arrays are parallel:** `x_time[i]` corresponds to `modelCallCount[i]` and `tokensUsage[i]`

### Sample Output (2-day range, 2026-02-05 to 2026-02-06)

```
Active hours: 12 / 48
  2026-02-05 06:00:    20 calls,      144,154 tokens
  2026-02-05 07:00:    55 calls,      233,640 tokens
  2026-02-06 04:00:    76 calls,    2,129,738 tokens
  2026-02-06 07:00:    19 calls,      473,581 tokens
  2026-02-06 08:00:   433 calls,   13,378,138 tokens
  2026-02-06 09:00:   674 calls,   19,519,783 tokens
  2026-02-06 15:00: 1,996 calls,   70,733,616 tokens
  2026-02-06 19:00:   315 calls,    8,609,852 tokens
  2026-02-06 20:00: 2,754 calls,   92,603,829 tokens
  2026-02-06 21:00: 3,221 calls,  116,462,553 tokens
  2026-02-06 22:00:   733 calls,   36,496,061 tokens
Totals: 10,296 calls / 360,784,945 tokens
```

### Sample Output (30-day range, 2026-01-07 to 2026-02-06)

```
Time points: 744
Total model calls: 46,701
Total tokens: 1,545,177,489
```

---

## Endpoint 2: Tool Usage (Time-Series)

### Request

```
GET /monitor/usage/tool-usage?startTime={start}&endTime={end}
```

Same time parameters as model-usage.

### Example Request

```bash
curl -s "https://api.z.ai/api/monitor/usage/tool-usage?startTime=2026-02-05%2000:00:00&endTime=2026-02-06%2023:59:59" \
  -H "Authorization: $ZAI_API_KEY" \
  -H "Accept-Language: en-US,en" \
  -H "Content-Type: application/json"
```

### Response Shape

```json
{
  "code": 200,
  "msg": "Operation successful",
  "success": true,
  "data": {
    "x_time": ["2026-02-05 00:00", "2026-02-05 01:00", "..."],
    "networkSearchCount": [null, null, 3, 13, null, "..."],
    "webReadMcpCount": [null, null, 1, 0, null, "..."],
    "zreadMcpCount": [null, null, 0, 0, null, "..."],
    "totalUsage": {
      "totalNetworkSearchCount": 16,
      "totalWebReadMcpCount": 1,
      "totalZreadMcpCount": 0,
      "totalSearchMcpCount": 17,
      "toolDetails": [
        { "modelName": "search-prime", "totalUsageCount": 16 },
        { "modelName": "web-reader", "totalUsageCount": 1 }
      ]
    }
  }
}
```

### Field Reference

| Field | Type | Description |
|-------|------|-------------|
| `networkSearchCount` | `(number\|null)[]` | Web searches per hour |
| `webReadMcpCount` | `(number\|null)[]` | Web-reader MCP calls per hour |
| `zreadMcpCount` | `(number\|null)[]` | Z-read MCP calls per hour |
| `totalUsage.totalNetworkSearchCount` | `number` | Total web searches |
| `totalUsage.totalWebReadMcpCount` | `number` | Total web reads |
| `totalUsage.totalZreadMcpCount` | `number` | Total z-reads |
| `totalUsage.totalSearchMcpCount` | `number` | Grand total of all search/tool calls |
| `totalUsage.toolDetails` | `object[]` | Per-model breakdown: `modelName` + `totalUsageCount` |

### Known Tool Models

| modelName | Description |
|-----------|-------------|
| `search-prime` | Web search tool |
| `web-reader` | Web page reader (MCP) |
| `zread` | Z-read document reader (MCP) |

### Sample Output (7-day range)

```
Network searches: 16
Web reads (MCP):   2
Z-reads (MCP):     0
Total search MCP: 18
Tool details:
  search-prime: 16
  web-reader:    2
```

---

## Endpoint 3: Quota / Limits (Real-Time Snapshot)

### Request

```
GET /monitor/usage/quota/limit
```

**No time parameters.** Returns current quota state.

### Example Request

```bash
curl -s "https://api.z.ai/api/monitor/usage/quota/limit" \
  -H "Authorization: $ZAI_API_KEY" \
  -H "Accept-Language: en-US,en" \
  -H "Content-Type: application/json"
```

### Response Shape

```json
{
  "code": 200,
  "msg": "Operation successful",
  "success": true,
  "data": {
    "limits": [
      {
        "type": "TIME_LIMIT",
        "unit": 5,
        "number": 1,
        "usage": 1000,
        "currentValue": 19,
        "remaining": 981,
        "percentage": 1,
        "usageDetails": [
          { "modelCode": "search-prime", "usage": 16 },
          { "modelCode": "web-reader", "usage": 39 },
          { "modelCode": "zread", "usage": 79 }
        ]
      },
      {
        "type": "TOKENS_LIMIT",
        "unit": 3,
        "number": 5,
        "usage": 200000000,
        "currentValue": 200112618,
        "remaining": 0,
        "percentage": 100,
        "nextResetTime": 1770398385482
      }
    ]
  }
}
```

### Field Reference

| Field | Type | Description |
|-------|------|-------------|
| `type` | `string` | Limit type: `TIME_LIMIT` or `TOKENS_LIMIT` |
| `unit` | `number` | Time unit (3 = hours?, 5 = ?) — needs further investigation |
| `number` | `number` | Multiplier for the unit (e.g., 5 hours) |
| `usage` | `number` | Total quota limit |
| `currentValue` | `number` | Current consumption |
| `remaining` | `number` | `usage - currentValue` (can be 0) |
| `percentage` | `number` | Usage percentage (0-100, integer) |
| `nextResetTime` | `number` | Epoch milliseconds — **only present on TOKENS_LIMIT** |
| `usageDetails` | `object[]` | Per-model breakdown — **only present on TIME_LIMIT** |

### Quota Types Explained

**TIME_LIMIT** — Tool call budget (search, web-reader, zread)
- Limit: 1,000 calls
- Tracks usage per tool model via `usageDetails`
- No `nextResetTime` observed — reset cycle unclear

**TOKENS_LIMIT** — Token consumption budget
- Limit: 200,000,000 (200M tokens)
- `nextResetTime` is epoch ms → convert: `new Date(1770398385482)` → `2026-02-06T22:49:45`
- `currentValue` can **exceed** `usage` (observed: 200,112,618 > 200,000,000)
- When maxed: `remaining: 0`, `percentage: 100`

### Sample Output (decoded)

```
QUOTA LIMITS:

1. TIME_LIMIT
   Limit:     1,000
   Current:      19
   Remaining:   981
   Percentage:   1%
   By model:
     search-prime: 16
     web-reader:   39
     zread:        79

2. TOKENS_LIMIT
   Limit:     200,000,000
   Current:   200,112,618
   Remaining:          0
   Percentage:      100%
   Next reset: 2026-02-06T22:49:45 (epoch ms: 1770398385482)
```

---

## API Behavior Notes

### Error Handling
- **Auth errors return HTTP 200** — must check `response.code` field
- Error shape: `{"code":401,"msg":"token expired or incorrect","success":false}`
- Missing time params on time-series endpoints: returns empty/invalid body

### Rate Limits
- No rate limiting observed during testing
- No `X-RateLimit-*` headers in responses
- Safe to poll `quota/limit` frequently (similar to Synthetic's `/v2/quotas`)

### Data Characteristics
- Time-series data is **always hourly** regardless of range
- `null` values in arrays mean zero activity (not missing data)
- 30-day queries work (744 data points) — no apparent range cap
- Both base URLs return identical data (use either for redundancy)
- `currentValue` can exceed `usage` limit (no hard cap enforcement on display)

### No Other Endpoints Found
Probed paths that returned 404: `quota`, `usage`, `billing`, `account`, `plan`, `subscription`, `status`, `health`. Only the 3 documented endpoints exist.

---

## SynTrack Integration Roadmap

### How Z.ai Maps to SynTrack's Architecture

| SynTrack Concept | Synthetic API | Z.ai Equivalent |
|-----------------|---------------|-----------------|
| Real-time quota snapshot | `GET /v2/quotas` | `GET /monitor/usage/quota/limit` |
| Subscription quota | `subscription.requests / limit` | `TOKENS_LIMIT.currentValue / usage` |
| Search quota | `search.hourly.requests / limit` | `TIME_LIMIT.currentValue / usage` |
| Tool call discounts | `toolCallDiscounts.requests / limit` | (included in TIME_LIMIT breakdown) |
| Reset time | `renewsAt` (ISO 8601) | `nextResetTime` (epoch ms) |
| Historical usage | Not available (SynTrack builds this) | `model-usage` + `tool-usage` (built-in!) |

### Key Differences from Synthetic API

1. **Z.ai provides historical data natively** — SynTrack's core value for Synthetic (building history from polling) is less needed. Z.ai already stores hourly time-series.

2. **Two quota types vs three** — Z.ai has `TOKENS_LIMIT` + `TIME_LIMIT` vs Synthetic's `subscription` + `search` + `toolCallDiscounts`.

3. **Reset time format differs** — Synthetic uses ISO 8601 strings; Z.ai uses epoch milliseconds.

4. **Error signaling** — Synthetic uses proper HTTP status codes; Z.ai returns HTTP 200 with error in body.

5. **Granularity** — Z.ai gives hourly buckets natively; Synthetic gives only the current snapshot.

### Integration Approach (Future)

#### Phase 1: Add Z.ai as a Second Provider
- New config: `ZAI_API_KEY`, `ZAI_BASE_URL` in `.env`
- New API client: `internal/api/zai_client.go` — mirrors `client.go` pattern
- New types: `internal/api/zai_types.go` — models the 3 response shapes
- Poll `quota/limit` on the same interval as Synthetic

#### Phase 2: Unified Quota Model
- Abstract `QuotaProvider` interface:
  ```go
  type QuotaProvider interface {
      Name() string
      FetchQuota(ctx context.Context) (*QuotaSnapshot, error)
  }
  ```
- Both Synthetic and Z.ai implement this interface
- `QuotaSnapshot` normalizes the data:
  ```go
  type QuotaSnapshot struct {
      Provider    string
      CapturedAt  time.Time
      Quotas      []QuotaInfo  // unified: name, limit, used, remaining, pct, resetsAt
  }
  ```

#### Phase 3: Leverage Z.ai Historical Data
- On dashboard load, fetch Z.ai time-series for charts instead of relying solely on polled snapshots
- Backfill: import Z.ai historical data into `quota_snapshots` table on first run
- Hybrid chart: SynTrack-polled data (real-time, sub-minute) + Z.ai data (hourly backfill)

#### Phase 4: Multi-Provider Dashboard
- Dashboard tabs or sections per provider
- Unified "all providers" overview card
- Per-provider quota cards with appropriate fields

### Database Schema Additions (Proposed)

```sql
-- Provider tracking (extends existing schema)
ALTER TABLE quota_snapshots ADD COLUMN provider TEXT NOT NULL DEFAULT 'synthetic';

-- Z.ai hourly cache (avoid re-fetching)
CREATE TABLE IF NOT EXISTS zai_hourly_cache (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    hour          TEXT NOT NULL,        -- "2026-02-06 15:00"
    model_calls   INTEGER,
    tokens_used   INTEGER,
    search_count  INTEGER,
    web_read_count INTEGER,
    zread_count   INTEGER,
    fetched_at    TEXT NOT NULL,
    UNIQUE(hour)
);
```

### Go Struct Sketches

```go
// internal/api/zai_types.go

type ZaiResponse[T any] struct {
    Code    int    `json:"code"`
    Msg     string `json:"msg"`
    Success bool   `json:"success"`
    Data    T      `json:"data"`
}

type ZaiModelUsage struct {
    XTime          []string   `json:"x_time"`
    ModelCallCount []*float64 `json:"modelCallCount"` // nullable
    TokensUsage    []*float64 `json:"tokensUsage"`    // nullable
    TotalUsage     struct {
        TotalModelCallCount int `json:"totalModelCallCount"`
        TotalTokensUsage    int `json:"totalTokensUsage"`
    } `json:"totalUsage"`
}

type ZaiToolUsage struct {
    XTime              []string   `json:"x_time"`
    NetworkSearchCount []*float64 `json:"networkSearchCount"`
    WebReadMcpCount    []*float64 `json:"webReadMcpCount"`
    ZreadMcpCount      []*float64 `json:"zreadMcpCount"`
    TotalUsage         struct {
        TotalNetworkSearchCount int             `json:"totalNetworkSearchCount"`
        TotalWebReadMcpCount    int             `json:"totalWebReadMcpCount"`
        TotalZreadMcpCount      int             `json:"totalZreadMcpCount"`
        TotalSearchMcpCount     int             `json:"totalSearchMcpCount"`
        ToolDetails             []ZaiToolDetail `json:"toolDetails"`
    } `json:"totalUsage"`
}

type ZaiToolDetail struct {
    ModelName       string `json:"modelName"`
    TotalUsageCount int    `json:"totalUsageCount"`
}

type ZaiQuotaLimit struct {
    Limits []ZaiLimit `json:"limits"`
}

type ZaiLimit struct {
    Type         string           `json:"type"`          // "TIME_LIMIT" | "TOKENS_LIMIT"
    Unit         int              `json:"unit"`
    Number       int              `json:"number"`
    Usage        int64            `json:"usage"`          // total quota
    CurrentValue int64            `json:"currentValue"`   // current consumption
    Remaining    int64            `json:"remaining"`
    Percentage   int              `json:"percentage"`     // 0-100
    NextResetTime *int64          `json:"nextResetTime"`  // epoch ms, only on TOKENS_LIMIT
    UsageDetails []ZaiUsageDetail `json:"usageDetails"`   // only on TIME_LIMIT
}

type ZaiUsageDetail struct {
    ModelCode string `json:"modelCode"`
    Usage     int    `json:"usage"`
}
```

---

## Open Questions for Integration

1. **What do `unit` and `number` mean in quota/limit?** (unit=5/number=1 for TIME_LIMIT, unit=3/number=5 for TOKENS_LIMIT) — likely time window definitions but undocumented.
2. **Does TIME_LIMIT have a reset cycle?** No `nextResetTime` observed — may reset monthly or on a different schedule.
3. **Can `currentValue` exceed `usage` indefinitely?** Observed 200,112,618 > 200,000,000. Is there a grace period or hard cutoff?
4. **Is there a webhook/push option?** Currently polling-only. No SSE or WebSocket endpoints found.
5. **Per-model token breakdown?** `quota/limit` shows per-model tool counts but not per-model token usage. The time-series `model-usage` is aggregate only.
