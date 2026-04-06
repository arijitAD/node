#!/bin/bash
DATA_PATH="/home/hluser/hl/data"

# Folders to exclude from pruning
# - visor_child_stderr: preserve crash logs for debugging
# - node_fills_by_block: preserve fills data for downstream ingestion into ClickHouse.
#   The ingestion pipeline may have lag beyond the 48-hour pruning window.
#   node_fills is NOT excluded — we only use node_fills_by_block (--batch-by-block).
# Example: EXCLUDES=("visor_child_stderr" "rate_limited_ips" "node_logs")
EXCLUDES=("visor_child_stderr" "node_fills_by_block")

# Log startup for debugging
echo "$(date): Prune script started" >> /proc/1/fd/1

# Check if data directory exists
if [ ! -d "$DATA_PATH" ]; then
    echo "$(date): Error: Data directory $DATA_PATH does not exist." >> /proc/1/fd/1
    exit 1
fi

echo "$(date): Starting pruning process at $(date)" >> /proc/1/fd/1

# Get directory size before pruning
size_before=$(du -sh "$DATA_PATH" | cut -f1)
files_before=$(find "$DATA_PATH" -type f | wc -l)
echo "$(date): Size before pruning: $size_before with $files_before files" >> /proc/1/fd/1

# Build the -prune arguments for excluding directories
PRUNE_ARGS=()
for dir in "${EXCLUDES[@]}"; do
    PRUNE_ARGS+=(-path "*/$dir" -prune -o)
done

# Delete data older than 48 hours = 60 minutes * 48 hours
HOURS=$((60*48))
find "$DATA_PATH" -mindepth 1 "${PRUNE_ARGS[@]}" -type f -mmin +$HOURS -exec rm {} +

# Prune fills older than 60 days (separate retention policy)
FILLS_PATH="$DATA_PATH/node_fills_by_block"
FILLS_DAYS=60
if [ -d "$FILLS_PATH" ]; then
    fills_before=$(du -sh "$FILLS_PATH" | cut -f1)
    find "$FILLS_PATH" -type f -mtime +$FILLS_DAYS -delete
    find "$FILLS_PATH" -type d -empty -delete
    fills_after=$(du -sh "$FILLS_PATH" 2>/dev/null | cut -f1 || echo "0")
    echo "$(date): Fills pruning (>${FILLS_DAYS}d): $fills_before -> $fills_after" >> /proc/1/fd/1
fi

# Get directory size after pruning
size_after=$(du -sh "$DATA_PATH" | cut -f1)
files_after=$(find "$DATA_PATH" -type f | wc -l)
echo "$(date): Size after pruning: $size_after with $files_after files" >> /proc/1/fd/1
echo "$(date): Pruning completed. Reduced from $size_before to $size_after ($(($files_before - $files_after)) files removed)." >> /proc/1/fd/1
