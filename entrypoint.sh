#!/bin/sh
set -e

# Fetch current gossip peer IPs from Hyperliquid API and write config
echo "Fetching gossip peers..."
PEERS=$(curl -sf -X POST \
  --header "Content-Type: application/json" \
  --data '{ "type": "gossipRootIps" }' \
  https://api.hyperliquid.xyz/info)

if [ -n "$PEERS" ]; then
  echo "{\"root_node_ips\": $PEERS}" > ~/override_gossip_config.json
  echo "Wrote override_gossip_config.json with peers: $PEERS"
else
  echo "WARNING: Failed to fetch peers, starting without override_gossip_config.json"
fi

# Start hl-visor
exec ~/hl-visor run-non-validator --write-fills --replica-cmds-style recent-actions
