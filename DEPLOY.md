# Deploy & Run

Quick guide to deploy the Hyperliquid node on a fresh Ubuntu 24.04 server.

## Prerequisites

| Requirement | Minimum |
|-------------|---------|
| OS          | Ubuntu 24.04 |
| vCPUs       | 16 |
| RAM         | 64 GB |
| Storage     | 500 GB SSD |
| Open ports  | 4001, 4002 (gossip), 8080 (file server) |

## 1. Install Docker

```bash
curl -fsSL https://get.docker.com | sh
```

Verify:

```bash
docker --version
docker compose version
```

## 2. Open Ports

```bash
ufw allow 4001/tcp
ufw allow 4002/tcp
ufw allow 8080/tcp
```

## 3. Clone & Start

```bash
cd /root
git clone https://github.com/datadash-xyz/node.git
cd node
docker compose up -d --build
```

> **Private repo?** Use a personal access token:
> ```bash
> git clone https://<TOKEN>@github.com/datadash-xyz/node.git
> ```
>
> Or copy from your local machine instead:
> ```bash
> scp -r ./node root@<SERVER_IP>:/root/node
> ```

## 4. Verify

### Check services are running

```bash
docker compose ps
```

You should see three services: `node`, `pruner`, `fileserver`.

### Watch the node sync

```bash
docker compose logs -f node
```

Look for `applied block X` — this means the node is syncing with the Hyperliquid L1 network. Initial sync may take a while.

### Test the file server

Once fills start appearing:

```bash
# List available dates
curl http://localhost:8080/

# List hours for a date
curl http://localhost:8080/20260404/

# Fetch fills for a specific hour
curl http://localhost:8080/20260404/08
```

From a remote machine, replace `localhost` with the server's public IP.

## Monitoring

### Service status

```bash
docker compose ps
```

### Logs

```bash
# All services
docker compose logs -f

# Specific service
docker compose logs -f node
docker compose logs -f pruner
docker compose logs -f fileserver
```

### Disk usage

```bash
# Overall disk
df -h /

# Docker volume size (fills data)
du -sh $(docker volume inspect hyperliquid_hl-data --format '{{ .Mountpoint }}')/node_fills/
```

### Crash logs

If the node crashes, check the visor stderr logs:

```bash
docker compose exec node ls ~/hl/data/visor_child_stderr/
```

Or from the host:

```bash
ls $(docker volume inspect hyperliquid_hl-data --format '{{ .Mountpoint }}')/visor_child_stderr/
```

## Common Operations

### Restart a service

```bash
docker compose restart node
```

### Rebuild after code changes

```bash
docker compose up -d --build
```

### Stop everything

```bash
docker compose down
```

### Stop and remove all data (⚠️ destructive)

```bash
docker compose down -v
```

### Update to latest code

```bash
cd /root/node
git pull
docker compose up -d --build
```
