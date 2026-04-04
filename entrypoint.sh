#!/bin/sh
set -e

# Fetch current gossip peer IPs from Hyperliquid API and write config
echo "Fetching gossip peers..."
PEERS=$(curl -sf -X POST \
  --header "Content-Type: application/json" \
  --data '{ "type": "gossipRootIps" }' \
  https://api.hyperliquid.xyz/info)

if [ -n "$PEERS" ]; then
  # Transform plain IP strings ["1.2.3.4","5.6.7.8"] into [{"Ip":"1.2.3.4"},{"Ip":"5.6.7.8"}]
  ROOT_IPS=$(echo "$PEERS" | sed 's/"[^"]*"/{"Ip":&}/g')
  echo "{\"root_node_ips\": $ROOT_IPS, \"try_new_peers\": false, \"chain\": \"Mainnet\"}" > ~/override_gossip_config.json
  echo "Wrote override_gossip_config.json"
else
  echo "WARNING: Failed to fetch peers, starting without override_gossip_config.json"
fi

# Start hl-visor
exec ~/hl-visor run-non-validator --write-fills --replica-cmds-style recent-actions
