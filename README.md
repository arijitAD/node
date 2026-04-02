# Hyperliquid Non-Validating Node (Fills Only)

A minimal Hyperliquid non-validating node configured to extract trade fills data for downstream ingestion into ClickHouse.

## What This Does

- Runs a **non-validating** node that syncs with the Hyperliquid L1 network
- Streams **trade fills** to `~/hl/data/node_fills/hourly/{date}/{hour}` (also TWAP statuses)
- Uses `--replica-cmds-style recent-actions` to keep only the 2 latest height files, minimizing disk usage
- No other data is written (no order statuses, raw book diffs, etc.)

## Architecture

| Service | Purpose |
|---------|---------|
| `node`  | Runs `hl-visor run-non-validator --write-fills` to stream fills data |
| `pruner`| Cron job (daily at 3 AM) that deletes files older than 48 hours, **excluding** `node_fills` and `visor_child_stderr` |

The `hl-visor` binary manages the child `hl-node` process and handles automatic binary updates (downloaded, GPG-verified, and restarted transparently).

## Machine Specs

| vCPUs | RAM    | Storage    | OS              |
|-------|--------|------------|-----------------|
| 16    | 64 GB  | 500 GB SSD | Ubuntu 24.04    |

Ports 4001 and 4002 must be open for gossip. For lowest latency, run in Tokyo, Japan.

## Setup

### 1. Configure Chain

```bash
# Testnet
echo '{"chain": "Testnet"}' > ~/visor.json

# Mainnet
echo '{"chain": "Mainnet"}' > ~/visor.json
```

The Dockerfile defaults to **Testnet**. Update the `visor.json` line and binary URLs in the Dockerfile for Mainnet.

### 2. Run with Docker Compose

```bash
docker compose up -d
```

This starts both the node and pruner services. The node data is persisted in a Docker volume (`hl-data`).

### 3. Verify It's Running

Look for `applied block X` in the logs, indicating the node is streaming live data:

```bash
docker compose logs -f node
```

## Data Output

Fills are written as JSONL to:

```
~/hl/data/node_fills/hourly/{date}/{hour}
```

Each line is a fill event in the [Hyperliquid API fills format](https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/nodes/l1-data-schemas). The `deployerFee` field is included for HIP-3 fills.

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
