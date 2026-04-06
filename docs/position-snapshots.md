# Hyperliquid Position Snapshots

How to capture daily user position snapshots by combining node fills data with Hyperliquid API calls.

## Strategy

1. **Derive active users** from fills/activity data already flowing through the node → ClickHouse pipeline
2. **Call the Hyperliquid API** per user to get their current perp positions, spot holdings, and account values
3. **Store snapshots** in ClickHouse (or S3) for historical analysis

This avoids the complexity of deserializing ABCI state files from the node, while giving us full control over snapshot frequency and freshness.

---

## API Endpoints

Base URL: `https://api.hyperliquid.xyz/info`
Method: `POST`
Content-Type: `application/json`

### 1. Perp Positions — `clearinghouseState`

Returns a user's open perpetual positions and margin summary.

```json
{
  "type": "clearinghouseState",
  "user": "0x...",
  "dex": ""
}
```

| Parameter | Type   | Required | Description |
|-----------|--------|----------|-------------|
| `type`    | string | ✅       | `"clearinghouseState"` |
| `user`    | string | ✅       | 42-char hex wallet address |
| `dex`     | string | ❌       | Perp dex name. Defaults to `""` (first/main Hyperliquid dex). Must be specified for HIP-3 dexes. |

**Example response:**

```json
{
  "assetPositions": [
    {
      "position": {
        "coin": "ETH",
        "szi": "0.0335",
        "entryPx": "2986.3",
        "positionValue": "100.02765",
        "unrealizedPnl": "-0.0134",
        "returnOnEquity": "-0.0026789",
        "liquidationPx": "2866.26936529",
        "leverage": {
          "type": "isolated",
          "value": 20,
          "rawUsd": "-95.059824"
        },
        "marginUsed": "4.967826",
        "maxLeverage": 50,
        "cumFunding": {
          "allTime": "514.085417",
          "sinceChange": "0.0",
          "sinceOpen": "0.0"
        }
      },
      "type": "oneWay"
    }
  ],
  "crossMaintenanceMarginUsed": "0.0",
  "crossMarginSummary": {
    "accountValue": "13104.514502",
    "totalMarginUsed": "0.0",
    "totalNtlPos": "0.0",
    "totalRawUsd": "13104.514502"
  },
  "marginSummary": {
    "accountValue": "13109.482328",
    "totalMarginUsed": "4.967826",
    "totalNtlPos": "100.02765",
    "totalRawUsd": "13009.454678"
  },
  "time": 1708622398623,
  "withdrawable": "13104.514502"
}
```

> **Note:** Under unified account or portfolio margin, use the spot balances endpoint instead for trading account balance across spot and perps.

### 2. Spot Holdings — `spotClearinghouseState`

Returns a user's spot token balances.

```json
{
  "type": "spotClearinghouseState",
  "user": "0x..."
}
```

| Parameter | Type   | Required | Description |
|-----------|--------|----------|-------------|
| `type`    | string | ✅       | `"spotClearinghouseState"` |
| `user`    | string | ✅       | 42-char hex wallet address |

### 3. List All Perp Dexes — `perpDexs`

Returns all available perp dexes (needed to know which `dex` values to pass).

```json
{
  "type": "perpDexs"
}
```

**Example response:**

```json
[
  null,
  {
    "name": "xyz",
    "fullName": "Trade[XYZ]",
    "deployer": "0x5e89b26d8d66da9888c835c9bfcc2aa51813e152",
    ...
  }
]
```

The first element (`null`) represents the main Hyperliquid perp dex meaning `dex: ""`.

---

## HIP-3 Dex Handling

The `dex` parameter is **optional** and defaults to the main Hyperliquid perp dex. You must make **separate API calls per dex** for users who trade on HIP-3 builder-deployed dexes.

Known HIP-3 dexes (from fills data coin prefixes):

| Dex Name   | Coin Prefix | Description |
|------------|-------------|-------------|
| `""`       | No prefix   | Main Hyperliquid (BTC, ETH, SOL, ...) |
| `xyz`      | `xyz:`      | Trade[XYZ] — indices, equities, commodities |
| `ventuals` | `ventuals:` | Ventuals — pre-market perps |
| `felix`    | `felix:`    | Felix |
| `markets`  | `markets:`  | Markets.xyz |
| `dreamcash`| `dreamcash:`| Dreamcash |
| `hyena`    | `hyena:`    | HyENA — USDe-margined crypto perps |

**How to determine which dexes to query per user:**
- Parse the user's fills from ClickHouse
- Extract the coin prefix (e.g., `xyz:TSLA` → dex is `xyz`, no prefix → default dex)
- Query `clearinghouseState` for each unique dex the user has traded on

---

## Rate Limits

- Info endpoint: **~1,200 requests/minute** (20 req/sec)
- `clearinghouseState` has weight **20** (heavier than simple lookups)
- Effective throughput: **~60 users/sec** at weight 20 (1200/20)
- For 10,000 users across 1 dex: ~2.8 minutes
- For 10,000 users across 3 dexes: ~8.3 minutes

**Recommendation:** Run snapshot jobs during off-peak hours. Throttle to ~15 req/sec to stay well under limits.

---

## Deriving Open Positions from Fills Data

Each fill in the Hyperliquid data contains enough information to determine whether a user has an open position. There are two key fields that make this possible:

### Key Fields in Each Fill

| Field | Type | Description |
|-------|------|-------------|
| `start_position` | decimal | The user's position size in this coin **before** this fill was executed. Positive = long, negative = short, zero = flat. |
| `direction` | string | Describes what happened to the position (see direction values below). |
| `side` | string | `buy` or `sell` — which side of the trade this user was on. |
| `size` | decimal | The size of this fill (always positive). |
| `coin` | string | Market identifier (e.g., `BTC`, `xyz:TSLA`). |
| `address` | string | User wallet address. |

### Direction Values

The `direction` field tells you exactly what happened to the user's position:

| Direction | Meaning |
|-----------|---------|
| `Open Long` | User had no position (or was flat), now has a long |
| `Open Short` | User had no position (or was flat), now has a short |
| `Close Long` | User is reducing/closing a long position |
| `Close Short` | User is reducing/closing a short position |
| `Long > Short` | User flipped from long to short in a single fill |
| `Short > Long` | User flipped from short to long in a single fill |
| `Liquidated Cross Long` | Cross-margin long was liquidated |
| `Liquidated Cross Short` | Cross-margin short was liquidated |
| `Liquidated Isolated Long` | Isolated-margin long was liquidated |
| `Liquidated Isolated Short` | Isolated-margin short was liquidated |
| `Auto-Deleveraging` | ADL event |

### Computing the Resulting Position

The position **after** a fill can be computed from `start_position`, `side`, and `size`:

```
if side == "buy":
    end_position = start_position + size
if side == "sell":
    end_position = start_position - size
```

**Example walkthrough:**

| # | Fill | start_position | side | size | end_position | direction |
|---|------|---------------|------|------|--------------|-----------|
| 1 | Buy 1.0 BTC | 0.0 | buy | 1.0 | **1.0** | Open Long |
| 2 | Buy 0.5 BTC | 1.0 | buy | 0.5 | **1.5** | Open Long |
| 3 | Sell 0.3 BTC | 1.5 | sell | 0.3 | **1.2** | Close Long |
| 4 | Sell 1.2 BTC | 1.2 | sell | 1.2 | **0.0** | Close Long |
| 5 | Sell 2.0 BTC | 0.0 | sell | 2.0 | **-2.0** | Open Short |
| 6 | Buy 3.0 BTC | -2.0 | buy | 3.0 | **1.0** | Short > Long |

### Raw Node Fill Format

The raw fills from the node (`~/hl/data/node_fills/hourly/{date}/{hour}`) use `start_pos` inside `side_info`:

```json
{
  "coin": "ETH",
  "side": "B",
  "time": "2024-07-26T08:26:25.899",
  "px": "3200.5",
  "sz": "0.5",
  "hash": "0xabc...",
  "side_info": [
    {
      "user": "0x1234...",
      "start_pos": "2.0",
      "oid": 12345,
      "twap_id": null,
      "cloid": null
    },
    {
      "user": "0x5678...",
      "start_pos": "-1.5",
      "oid": 67890,
      "twap_id": null,
      "cloid": null
    }
  ]
}
```

- `side_info[0]` is the **buyer** — their position goes from `start_pos` to `start_pos + sz`
- `side_info[1]` is the **seller** — their position goes from `start_pos` to `start_pos - sz`
- `side`: `"B"` = buy-initiated (taker was buyer), `"A"` = sell-initiated (taker was seller)

### Approach 1: Use `start_position` from the Latest Fill (Recommended)

The simplest way to know if a user has an open position is to look at their **most recent fill** per coin and compute the end position:

```sql
-- Find users with open perp positions (ClickHouse)
SELECT
    address,
    coin,
    -- end_position after the latest fill
    if(side = 'buy',
       start_position + size,
       start_position - size
    ) AS current_position
FROM (
    SELECT
        address,
        coin,
        side,
        size,
        start_position,
        ROW_NUMBER() OVER (
            PARTITION BY address, coin
            ORDER BY timestamp DESC
        ) AS rn
    FROM hyperliquid.fills
    WHERE asset_class = 'perp'
)
WHERE rn = 1
  AND current_position != 0
ORDER BY abs(current_position) DESC
```

### Approach 2: Aggregate Net Signed Size

If `start_position` is not available (e.g., you only have partial history), you can compute the cumulative net position from all fills:

```sql
-- Compute net position from cumulative fills (requires COMPLETE fill history)
SELECT
    address,
    coin,
    SUM(
        CASE WHEN side = 'buy' THEN size ELSE -size END
    ) AS net_position
FROM hyperliquid.fills
WHERE asset_class = 'perp'
GROUP BY address, coin
HAVING net_position != 0
ORDER BY abs(net_position) DESC
```

> **⚠️ Warning:** Approach 2 only works if you have the **complete** fill history for all users from the beginning. If you start ingesting fills mid-stream, users may have pre-existing positions that won't be captured. Approach 1 (using `start_position`) is more reliable because each fill is self-describing.

### Edge Cases

| Scenario | What happens |
|----------|-------------|
| **Liquidation** | The fill has `is_liquidation = true` and direction is `Liquidated *`. The resulting position is typically zero (fully liquidated) or reduced. |
| **ADL (Auto-Deleveraging)** | Similar to liquidation — the counterparty's position is force-reduced. Direction = `Auto-Deleveraging`. |
| **Position flip** | A single fill can flip a user from long to short (or vice versa). Direction = `Long > Short` or `Short > Long`. The `start_position` will be one sign, the end position the opposite. |
| **Partial close** | Direction is `Close Long` or `Close Short`, but the position is not fully closed. Check `end_position != 0`. |
| **Spot fills** | Direction is `Buy` or `Sell`. Spot doesn't have `start_position` semantics — it's just a token swap. Query `spotClearinghouseState` API instead. |

### Putting It Together: Users to Snapshot

```sql
-- Get all (user, dex) pairs that likely have open positions
-- Use this list to drive API snapshot calls
SELECT
    address,
    -- Extract dex from coin prefix
    if(position(coin, ':') > 0,
       substring(coin, 1, position(coin, ':') - 1),
       ''
    ) AS dex,
    groupArray(coin) AS coins,
    count() AS fill_count
FROM (
    SELECT
        address,
        coin,
        if(side = 'buy',
           start_position + size,
           start_position - size
        ) AS end_position,
        ROW_NUMBER() OVER (
            PARTITION BY address, coin
            ORDER BY timestamp DESC
        ) AS rn
    FROM hyperliquid.fills
    WHERE asset_class = 'perp'
)
WHERE rn = 1
  AND end_position != 0
GROUP BY address, dex
ORDER BY fill_count DESC
```

This gives you a list of `(address, dex)` pairs to call `clearinghouseState` on.

### Batched Workflow

Each workflow run captures the current time as a **cutoff**, materializes the full list of users with open positions into a temporary table, then the snapshot service drains it in batches. Once complete, the table is dropped.

**Step 1 — Create the work queue:**

The `{cutoff:DateTime64(3)}` parameter should be set to `now()` at the start of the workflow run. This freezes the point-in-time view — fills ingested after the cutoff are excluded, ensuring the `ROW_NUMBER()` window produces a consistent snapshot.

```sql
CREATE TABLE hyperliquid.snapshot_queue
ENGINE = MergeTree()
ORDER BY (dex, address)
AS
SELECT DISTINCT
    address,
    if(position(coin, ':') > 0,
       substring(coin, 1, position(coin, ':') - 1),
       ''
    ) AS dex
FROM (
    SELECT
        address,
        coin,
        if(side = 'buy',
           start_position + size,
           start_position - size
        ) AS end_position,
        ROW_NUMBER() OVER (
            PARTITION BY address, coin
            ORDER BY timestamp DESC
        ) AS rn
    FROM hyperliquid.fills
    WHERE asset_class = 'perp'
      AND timestamp <= {cutoff:DateTime64(3)}
)
WHERE rn = 1
  AND end_position != 0
```

This runs a single query and writes the result directly into a table — no intermediate steps.

**Step 2 — Process in batches:**

```sql
SELECT address, dex
FROM hyperliquid.snapshot_queue
ORDER BY dex, address
LIMIT {batch_size:UInt32}
OFFSET {batch_offset:UInt32}
```

**Step 3 — Clean up:**

```sql
DROP TABLE hyperliquid.snapshot_queue
```

**Workflow loop** (pseudo-code):

```
-- 1. Materialize the work queue
run Step 1

-- 2. Get total and batch through
total      = SELECT count() FROM snapshot_queue
batch_size = 500
batches    = ceil(total / batch_size)

for i in 0..batches:
    rows = run Step 2 with batch_offset = i * batch_size
    for each (address, dex, coins) in rows:
        call clearinghouseState(address, dex)
        call spotClearinghouseState(address)
        write snapshot to ClickHouse / S3
    sleep(throttle)

-- 3. Drop the work queue
run Step 3
```

> **Why a table instead of pagination over the raw query?** The position-derivation query uses `ROW_NUMBER()` over the entire fills table — expensive to re-run per batch. Materializing once into `snapshot_queue` means the heavy query runs exactly once, and batch reads are trivial index scans.

> **Crash safety:** If the service crashes mid-run, `snapshot_queue` still exists. On the next run, check if the table exists — if so, resume from where you left off (track the last processed offset) or drop and recreate.

> [!NOTE]
> **Phase 2 TODO — Incremental scan optimization**
>
> The current approach (Phase 1) re-derives positions from the *entire* fills table on every run. In Phase 2, we can optimize by only re-computing positions for `(address, coin)` pairs that had new fills since the last cutoff. This would involve:
> - Persisting the cutoff timestamp between runs
> - Querying only fills in the `(last_cutoff, current_cutoff]` window to find affected users
> - Merging those with the previous snapshot's position state
>
> This reduces the heavy `ROW_NUMBER()` scan to just the delta, but requires careful handling of edge cases (e.g., first run, backfills, users who closed positions between runs). Needs more design thought before implementing.

---

## Ingestion Flow

```
┌─────────────────────────────────────────────────┐
│  ClickHouse (fills / activity data)             │
│                                                 │
│  SELECT DISTINCT address                        │
│  FROM hyperliquid.fills                         │
│  WHERE date >= today() - 7                      │
│    AND ... (has non-zero derived position)      │
└────────────────┬────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────┐
│  Snapshot Service (Go / Python)                 │
│                                                 │
│  For each user:                                 │
│    1. Determine which dexes they trade on       │
│       (from coin prefix in fills data)          │
│    2. POST clearinghouseState per dex           │
│    3. POST spotClearinghouseState               │
│    4. Write results to ClickHouse / S3          │
│                                                 │
│  Throttle: 15 req/sec                           │
│  Schedule: daily cron (e.g., 00:00 UTC)         │
└─────────────────────────────────────────────────┘
```

---

## Hydromancer Reservoir (Alternative / Supplement)

Hydromancer provides pre-built daily snapshots via S3 (requester pays). Useful for backfill or cross-validation.

**Bucket:** `s3://hydromancer-reservoir` (region: `ap-northeast-1`)

| Dataset | S3 Path |
|---------|---------|
| Perp positions | `by_dex/hyperliquid/snapshots/perp/date=YYYY-MM-DD/*.parquet` |
| Spot holdings  | `global/snapshots/spot/date=YYYY-MM-DD/*.parquet` |
| Account values | `global/snapshots/account_values/date=YYYY-MM-DD/*.parquet` |

**Data availability:** August 2025 onward (gap: late Oct – mid Dec 2025). Updated weekly. Latest data as of 2026-04-02: `date=2026-04-01`.

**Schema reference:** https://docs.hydromancer.xyz/reservoir/schema-reference/snapshots

### Quick Start (DuckDB)

```sql
INSTALL httpfs; LOAD httpfs;
SET s3_region = 'ap-northeast-1';
SET s3_access_key_id = '<KEY>';
SET s3_secret_access_key = '<SECRET>';

-- Top BTC positions
SELECT user, size, notional, entry_price, leverage_type, leverage
FROM read_parquet('s3://hydromancer-reservoir/by_dex/hyperliquid/snapshots/perp/date=2026-04-01/*.parquet')
WHERE market = 'BTC'
ORDER BY abs(size) DESC
LIMIT 20;
```

---

## References

- [Hyperliquid API — Perpetuals Info Endpoint](https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/info-endpoint/perpetuals)
- [Hyperliquid API — Spot Info Endpoint](https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/info-endpoint/spot)
- [Hyperliquid API — Rate Limits](https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/rate-limits-and-user-limits)
- [Hydromancer Reservoir — Snapshots Schema](https://docs.hydromancer.xyz/reservoir/schema-reference/snapshots)
- [Hydromancer Reservoir — Hyperliquid Datasets](https://docs.hydromancer.xyz/reservoir/hyperliquid)
