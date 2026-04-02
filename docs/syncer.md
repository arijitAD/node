# Syncer — Realtime Fills Ingestion

The syncer is a Go sidecar service that streams fill data from the node into a remote ClickHouse server in near-real-time.

## Why Go Over rclone/rsync

Tools like `rclone sync` and `rsync` operate at **file-level granularity** — they compare source and destination files by size or checksum and re-upload the entire file when a difference is detected. They cannot partially copy or append to an existing remote file.

This is a problem for node fills because `hl-visor` writes data as **append-only JSONL files** that grow throughout each hour:

```
node_fills/hourly/20260402/08    ← actively being appended to for ~60 minutes
node_fills/hourly/20260402/09    ← next hour's file
```

With rclone/rsync:
- Every sync cycle would re-upload the **entire** current hour's file, even if only a few lines were appended
- A file that grows from 1MB to 50MB over an hour would be re-uploaded dozens of times
- To avoid uploading partial files, you'd need `--min-age` which adds **up to 1 hour of latency**
- No way to track which lines have already been processed — you'd rely entirely on ClickHouse-side deduplication

The Go syncer solves this by reading at **line granularity**:
- `fsnotify` detects appends instantly (no polling)
- Only new lines are read from the checkpoint position
- Each line is a complete fill — no partial read risk
- Checkpoint tracks exact file + line number for crash recovery
- Data reaches ClickHouse in **seconds**, not hours
## How It Works

```
hl-visor                        syncer                          ClickHouse
   │                               │                               │
   │  append fill to JSONL file    │                               │
   ├──────────────────────────────►│                               │
   │                               │  fsnotify fires Write event  │
   │                               │──┐                           │
   │                               │  │ read checkpoint:          │
   │                               │  │ {"file":"..08","line":42} │
   │                               │  │                           │
   │                               │  │ skip lines 1-42           │
   │                               │  │ read lines 43+            │
   │                               │  │ transform + batch         │
   │                               │◄─┘                           │
   │                               │                               │
   │                               │  POST JSONEachRow            │
   │                               ├──────────────────────────────►│
   │                               │                               │
   │                               │  200 OK                      │
   │                               │◄──────────────────────────────┤
   │                               │                               │
   │                               │  save checkpoint:            │
   │                               │  {"file":"..08","line":57}   │
   │                               │                               │
```

### Step by Step

1. **`hl-visor` appends** a fill line to `node_fills/hourly/{date}/{hour}`
2. **`fsnotify` fires** a `Write` event — the syncer is notified instantly (no polling)
3. **Syncer reads the checkpoint** file (`syncer_state.json`) to find the last successfully processed file and line number
4. **Skips** already-processed lines, reads only the new ones
5. **Transforms** each raw fill: flattens `side_info[0]` (buyer) and `side_info[1]` (seller) into two separate rows
6. **Batches** rows (100 rows or 5 seconds, whichever comes first)
7. **Inserts** the batch into ClickHouse via `POST /?query=INSERT INTO ... FORMAT JSONEachRow`
8. **Saves checkpoint** only after ClickHouse confirms the insert → `{"file": "...", "line": 57}`

---

## Checkpoint Design

The syncer tracks its position using a JSON checkpoint file stored on the shared Docker volume:

```json
{"file": "/home/hluser/hl/data/node_fills/hourly/20260402/08", "line": 42}
```

| Field  | Description |
|--------|-------------|
| `file` | Absolute path to the fills file currently being tailed |
| `line` | Last line number successfully inserted into ClickHouse (1-indexed) |

### Why Line Numbers?

Since the data is **JSONL** (one JSON object per line), each line maps 1:1 to a fill event. Line-based checkpointing is:

- **Human-readable** — open `syncer_state.json` and see exactly where you are
- **Debuggable** — run `head -n 42 <file>` to see everything that was processed
- **Correct by construction** — each line is a complete fill, no partial reads

### Crash Recovery

The checkpoint is saved **only after** ClickHouse confirms the insert. This gives **at-least-once** delivery:

| Scenario | What happens |
|----------|-------------|
| **Crash before insert** | Checkpoint unchanged → lines are re-read and sent on restart |
| **Crash after insert, before checkpoint** | Checkpoint unchanged → last batch is re-sent, `ReplacingMergeTree` deduplicates |
| **Crash after checkpoint** | Clean resume from exact line |

The checkpoint file itself is written atomically (write to `.tmp`, then `rename`) to prevent corruption.

### Hour Rollover

When `hl-visor` starts writing to a new hourly file (e.g., `08` → `09`):

1. `fsnotify` fires a `Create` event for the new file
2. Syncer finishes reading any remaining lines from the old file
3. Flushes the batch and saves checkpoint
4. Switches tracking to the new file with `line: 0`

---

## Transformation

Each raw node fill has a nested `side_info` array with two entries. The syncer flattens this into **two rows** (one per side):

**Input** (one line from `node_fills`):

```json
{
  "coin": "ETH",
  "side": "B",
  "time": "2024-07-26T08:26:25.899",
  "px": "3200.5",
  "sz": "0.5",
  "hash": "0xabc...",
  "side_info": [
    {"user": "0x1234...", "start_pos": "2.0", "oid": 12345},
    {"user": "0x5678...", "start_pos": "-1.5", "oid": 67890}
  ]
}
```

**Output** (two rows inserted into ClickHouse):

```json
{"address":"0x1234...","coin":"ETH","side":"buy","size":"0.5","price":"3200.5","start_position":"2.0","hash":"0xabc...","timestamp":"2024-07-26T08:26:25.899","oid":12345,"is_taker":true,"processed_at":"2024-07-26 08:26:30"}
{"address":"0x5678...","coin":"ETH","side":"sell","size":"0.5","price":"3200.5","start_position":"-1.5","hash":"0xabc...","timestamp":"2024-07-26T08:26:25.899","oid":67890,"is_taker":false,"processed_at":"2024-07-26 08:26:30"}
```

Key mappings:
- `side_info[0]` → **buyer** row, `side_info[1]` → **seller** row
- `side: "B"` → buyer was taker (`is_taker: true`), `side: "A"` → seller was taker
- `processed_at` → insertion timestamp for `ReplacingMergeTree` versioning

---

## Configuration

All configuration is via environment variables (set in `docker-compose.yml`):

| Variable | Default | Description |
|----------|---------|-------------|
| `FILLS_DIR` | `/home/hluser/hl/data/node_fills/hourly` | Path to the node fills directory |
| `STATE_FILE` | `/home/hluser/hl/data/syncer_state.json` | Path to the checkpoint file |
| `CLICKHOUSE_URL` | `http://localhost:8123` | ClickHouse HTTP endpoint |
| `CLICKHOUSE_DB` | `hyperliquid` | Target database |
| `CLICKHOUSE_TABLE` | `fills` | Target table |
| `CLICKHOUSE_USER` | `default` | Auth user |
| `CLICKHOUSE_PASSWORD` | _(empty)_ | Auth password |
| `BATCH_SIZE` | `100` | Max rows per batch before flushing |
| `BATCH_TIMEOUT` | `5s` | Max time before flushing a partial batch |

---

## Error Handling

| Error | Behavior |
|-------|----------|
| **ClickHouse unreachable** | Retries 3 times with exponential backoff (2s, 4s, 6s). Batch stays in memory. |
| **Malformed fill line** | Logged and skipped. Line counter still advances to avoid re-reading on restart. |
| **Fills directory missing** | Syncer polls every 5s until the directory appears (node may not have written yet). |
| **Checkpoint file missing** | Starts fresh from the latest available fills file. |

---

## Dual Pipeline Architecture

The syncer is the **realtime** half of a dual-pipeline architecture. See [README.md](../README.md#ingestion-architecture) for how it interacts with the historical S3 batch pipeline and how `ReplacingMergeTree` reconciles both.

---

## Files

```
syncer/
├── main.go         # Entry point, config loading, signal handling
├── tailer.go       # fsnotify watcher, line-based reading, batching
├── transform.go    # Raw fill → flat rows transformation
├── clickhouse.go   # ClickHouse HTTP client with retry
├── state.go        # Checkpoint persistence (atomic file writes)
├── Dockerfile      # Multi-stage build → ~10MB Alpine image
├── go.mod
└── go.sum
```
