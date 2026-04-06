# Hyperliquid Non-Validating Node (Fills Only)

A minimal Hyperliquid non-validating node configured to extract trade fills data for downstream ingestion.

## What This Does

- Runs a **non-validating** node that syncs with the Hyperliquid L1 network
- Streams **trade fills** grouped by block to `~/hl/data/node_fills_by_block/hourly/{date}/{hour}` (also TWAP statuses)
- Uses `--batch-by-block` so each line is one block with `{block_number, block_time, events}` schema
- Uses `--replica-cmds-style recent-actions` to keep only the 2 latest height files, minimizing disk usage
- No other data is written (no order statuses, raw book diffs, etc.)
- Exposes fills via an **HTTP file server** for downstream consumers to pull

## Architecture

| Service      | Purpose |
|--------------|---------|
| `node`       | Runs `hl-visor run-non-validator --write-fills --batch-by-block` to stream fills data grouped by block |
| `pruner`     | Cron job (daily at 3 AM) that deletes files older than 48 hours, **excluding** `node_fills` and `visor_child_stderr` |
| `fileserver` | nginx file server exposing `node_fills_by_block/hourly/` over HTTP on port 8080 ([design doc](docs/fileserver.md)) |

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

Fills are written as JSONL (one block per line) to:

```
~/hl/data/node_fills_by_block/hourly/{date}/{hour}
```

Inside the container this resolves to:

```
/home/hluser/hl/data/node_fills_by_block/hourly/{date}/{hour}
```

> **Note:** The `--batch-by-block` flag causes fills to be written to `node_fills_by_block` instead of `node_fills`. Without this flag, the path would be `node_fills/hourly/{date}/{hour}`.

### Storage & Persistence

The data directory is mounted as a **Docker volume** (`hl-data`) shared by the `node`, `pruner`, and `fileserver` services. On the host machine the volume is stored at:

```
/var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills_by_block/hourly/{date}/{hour}
```

You can inspect the volume with:

```bash
docker volume inspect hyperliquid_hl-data
```

Check disk usage:

```bash
# Total volume size (all node data)
du -sh /var/lib/docker/volumes/hyperliquid_hl-data/_data/

# Fills data only
du -sh /var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills_by_block/
```

### Fill Format

With `--batch-by-block`, each line is one block containing all its fills. The schema is:

```jsonc
{
  "local_time": "...",          // Local wall-clock time when the block was processed
  "block_time": "...",          // L1 block timestamp
  "block_number": 70802809,     // L1 block height
  "events": [                   // All fills in this block
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
  ]
}
```

See the [Hyperliquid L1 data schema](https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/nodes/l1-data-schemas) for field details. The `deployerFee` field is included for HIP-3 fills.

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
- `node_fills` / `node_fills_by_block` — preserved for downstream ingestion

Since fills directories are excluded from automatic pruning, fills data will accumulate indefinitely. Use the commands below to prune old fills.

### Prune fills older than a specific retention period

From the host machine:

```bash
# Prune fills older than 7 days
sudo find /var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills_by_block/ -type f -mtime +7 -delete

# Prune fills older than 30 days
sudo find /var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills_by_block/ -type f -mtime +30 -delete

# Remove empty date directories left behind
sudo find /var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills_by_block/ -type d -empty -delete
```

### Automate fills pruning with cron

Add a cron job on the host to prune fills automatically. For example, to keep 7 days of data:

```bash
# Edit the host's crontab
sudo crontab -e

# Add this line (runs daily at 4 AM, after the general pruner runs at 3 AM)
0 4 * * * find /var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills_by_block/ -type f -mtime +7 -delete && find /var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills_by_block/ -type d -empty -delete
```

Change `-mtime +7` to `-mtime +30` for 30-day retention, or any number of days.

## Common Operations

### Rebuild after code changes

```bash
git pull
docker compose up -d --build
```

The node picks up where it left off — no resync required.

### Clear fills data only

To delete old fills without losing node state:

```bash
sudo rm -rf /var/lib/docker/volumes/hyperliquid_hl-data/_data/node_fills_by_block/hourly/*
docker compose restart node
```

### Restart a service

```bash
docker compose restart node
```

### Stop everything

```bash
docker compose down
```

> **⚠️ NEVER use `docker compose down -v`** unless you intend a full reset. The `-v` flag deletes the Docker volume containing all node state (synced blocks, ABCI state, gossip config). This forces a **full resync from the network** which takes 10+ minutes to bootstrap the ~830 MB initial state and then catch up to the current block height. To clear fills, delete only the fills directory as shown above.

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
