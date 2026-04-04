# File Server — HTTP Access to Node Fills

The file server is an `nginx:alpine` container that serves the raw JSONL fills directory over HTTP.

## Why a File Server

The node writes fill data as append-only JSONL files to disk. Rather than running application logic to push data to consumers, we expose the files directly over HTTP and let consumers pull on their own schedule.

## How It Works

```
hl-visor                        nginx                          Consumer
   │                               │                               │
   │  write fills to JSONL file    │                               │
   ├──────────────────────────────►│                               │
   │  /data/node_fills/hourly/     │                               │
   │  20260404/08                  │                               │
   │                               │                               │
   │                               │  GET /20260404/08             │
   │                               │◄──────────────────────────────┤
   │                               │                               │
   │                               │  200 OK (raw JSONL body)      │
   │                               ├──────────────────────────────►│
   │                               │                               │
```

## URL Structure

| URL | Response |
|-----|----------|
| `GET /` | JSON array of date folders (e.g. `20260402`, `20260403`) |
| `GET /20260404/` | JSON array of hour files (e.g. `08`, `09`, `10`) |
| `GET /20260404/08` | Raw JSONL — one fill per line |

## Directory Listing Format

Directory listings are returned as JSON (`autoindex_format json`):

```json
[
  {"name":"20260402","type":"directory","mtime":"Wed, 02 Apr 2026 23:59:00 GMT"},
  {"name":"20260403","type":"directory","mtime":"Thu, 03 Apr 2026 23:59:00 GMT"},
  {"name":"20260404","type":"directory","mtime":"Fri, 04 Apr 2026 12:00:00 GMT"}
]
```

## Configuration

The nginx config is a single file mounted at `/etc/nginx/conf.d/default.conf`:

```nginx
server {
    listen 8080;
    root /data/node_fills/hourly;
    autoindex on;
    autoindex_format json;
}
```

## Networking

The container uses `network_mode: host` — it binds directly to the host's network interfaces. Port 8080 is available on the server's public IP without Docker port mapping.

## Example Usage

```bash
# List available dates
curl http://<server-ip>:8080/

# List hours for a specific date
curl http://<server-ip>:8080/20260404/

# Fetch fills for a specific hour
curl http://<server-ip>:8080/20260404/08

# Count fills in a file
curl -s http://<server-ip>:8080/20260404/08 | wc -l
```

## Files

```
fileserver/
└── nginx.conf    # 5-line nginx config (autoindex + JSON format)
```
