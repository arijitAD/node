# Hyperliquid Non-Validating Node (Fills Only)

A minimal Hyperliquid non-validating node configured to extract trade fills data for downstream ingestion.

## What This Does

- Runs a **non-validating** node that syncs with the Hyperliquid L1 network
- Streams **trade fills** to `~/hl/data/node_fills/hourly/{date}/{hour}` (also TWAP statuses)
- Uses `--replica-cmds-style recent-actions` to keep only the 2 latest height files, minimizing disk usage
- No other data is written (no order statuses, raw book diffs, etc.)
- Exposes fills via an **HTTP file server** for downstream consumers to pull

## Architecture

| Service      | Purpose |
|--------------|---------|
| `node`       | Runs `hl-visor run-non-validator --write-fills` to stream fills data |
| `pruner`     | Cron job (daily at 3 AM) that deletes files older than 48 hours, **excluding** `node_fills` and `visor_child_stderr` |
| `fileserver` | nginx file server exposing `node_fills/hourly/` over HTTP on port 8080 ([design doc](docs/fileserver.md)) |

The `hl-visor` binary manages the child `hl-node` process and handles automatic binary updates (downloaded, GPG-verified, and restarted transparently).

## Machine Specs

| vCPUs | RAM    | Storage    | OS              |
|-------|--------|------------|-----------------|
| 16    | 64 GB  | 500 GB SSD | Ubuntu 24.04    |

Ports 4001 and 4002 must be open for gossip. Port 8080 must be open for the file server. For lowest latency, run in Tokyo, Japan.

## Setup

### 1. Configure Chain

```bash
# Testnet
echo '{"chain": "Testnet"}' > ~/visor.json

# Mainnet
echo '{"chain": "Mainnet"}' > ~/visor.json
```

The Dockerfile defaults to **Mainnet**. Update the `visor.json` line and binary URLs in the Dockerfile for Testnet if needed.

### 2. Run with Docker Compose

```bash
docker compose up -d
```

This starts the node, pruner, and file server services. The node data is persisted in a Docker volume (`hl-data`).

### 3. Verify It's Running

Look for `applied block X` in the logs, indicating the node is streaming live data:

```bash
docker compose logs -f node
```

Verify the file server is serving fills:

```bash
curl http://localhost:8080/
```

## Data Output

### File Path

Fills are written as JSONL to:

```
~/hl/data/node_fills/hourly/{date}/{hour}
```

Inside the container this resolves to:

```
/home/hluser/hl/data/node_fills/hourly/{date}/{hour}
```

### Storage & Persistence

The data directory is mounted as a **Docker volume** (`hl-data`) shared by the `node`, `pruner`, and `fileserver` services. On the host machine the volume is stored at:

```
/var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills/hourly/{date}/{hour}
```

You can inspect the volume with:

```bash
docker volume inspect hyperliquid_hl-data
```

### Fill Format

Each line is a JSON object in the [Hyperliquid L1 data schema](https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/nodes/l1-data-schemas). The `deployerFee` field is included for HIP-3 fills.

```jsonc
{
  "coin": "ETH",               // Market identifier (e.g. "BTC", "xyz:TSLA")
  "side": "B",                 // "B" = buy-initiated, "A" = sell-initiated
  "time": "2024-07-26T08:26:25.899",
  "px": "3200.5",              // Fill price
  "sz": "0.5",                 // Fill size
  "hash": "0xabc...",          // Transaction hash
  "side_info": [
    {
      "user": "0x1234...",     // Buyer address
      "start_pos": "2.0",     // Position size before this fill
      "oid": 12345,
      "twap_id": null,
      "cloid": null
    },
    {
      "user": "0x5678...",     // Seller address
      "start_pos": "-1.5",
      "oid": 67890,
      "twap_id": null,
      "cloid": null
    }
  ]
}
```

- `side_info[0]` is the **buyer** — position goes from `start_pos` → `start_pos + sz`
- `side_info[1]` is the **seller** — position goes from `start_pos` → `start_pos - sz`

## File Server

The `fileserver` service is an `nginx:alpine` container that serves the fills directory over HTTP with JSON directory listings. See [docs/fileserver.md](docs/fileserver.md) for details.

```
http://<server-ip>:8080/                    → JSON listing of date folders
http://<server-ip>:8080/20260404/           → JSON listing of hour files
http://<server-ip>:8080/20260404/08         → raw JSONL fills for that hour
```

The file server uses `network_mode: host`, binding directly to the host's network interfaces. No Docker port mapping is involved — port 8080 is available on the server's public IP.

## Pruning

The pruner sidecar runs daily at 3 AM and deletes all files in `~/hl/data/` older than 48 hours, **except**:

- `visor_child_stderr` — crash logs for debugging
- `node_fills` — preserved for downstream ingestion (your pipeline must handle cleanup)

> **Important:** Since `node_fills` is excluded from pruning, fills data will accumulate indefinitely. Ensure your ingestion pipeline deletes files after successful ingest, or add a separate cleanup mechanism with a longer retention window.

## Mainnet Seed Peers

For Mainnet, at least one peer IP must be configured in `~/override_gossip_config.json`. Query available peers:

```bash
curl -X POST --header "Content-Type: application/json" --data '{ "type": "gossipRootIps" }' https://api.hyperliquid.xyz/info
```

## Troubleshooting

Crash logs from the child process are written to:

```
~/hl/data/visor_child_stderr/{date}/{node_binary_index}
```

## References

- [Upstream node repo](https://github.com/hyperliquid-dex/node)
- [L1 data schemas](https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/nodes/l1-data-schemas)
- [Reading L1 data](https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/nodes/reading-l1-data)
