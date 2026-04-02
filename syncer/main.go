package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// Config holds all syncer configuration, loaded from environment variables.
type Config struct {
	FillsDir        string
	StateFile       string
	ClickHouseURL   string
	ClickHouseDB    string
	ClickHouseTable string
	ClickHouseUser  string
	ClickHousePass  string
	BatchSize       int
	BatchTimeout    time.Duration
}

func loadConfig() Config {
	batchSize, _ := strconv.Atoi(getEnv("BATCH_SIZE", "100"))
	batchTimeout, _ := time.ParseDuration(getEnv("BATCH_TIMEOUT", "5s"))

	return Config{
		FillsDir:        getEnv("FILLS_DIR", "/home/hluser/hl/data/node_fills/hourly"),
		StateFile:       getEnv("STATE_FILE", "/home/hluser/hl/data/syncer_state.json"),
		ClickHouseURL:   getEnv("CLICKHOUSE_URL", "http://localhost:8123"),
		ClickHouseDB:    getEnv("CLICKHOUSE_DB", "hyperliquid"),
		ClickHouseTable: getEnv("CLICKHOUSE_TABLE", "fills"),
		ClickHouseUser:  getEnv("CLICKHOUSE_USER", "default"),
		ClickHousePass:  getEnv("CLICKHOUSE_PASSWORD", ""),
		BatchSize:       batchSize,
		BatchTimeout:    batchTimeout,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	cfg := loadConfig()

	log.Printf("syncer starting: fills_dir=%s clickhouse=%s/%s.%s batch=%d/%s",
		cfg.FillsDir, cfg.ClickHouseURL, cfg.ClickHouseDB, cfg.ClickHouseTable,
		cfg.BatchSize, cfg.BatchTimeout)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down...", sig)
		cancel()
	}()

	state := LoadState(cfg.StateFile)
	ch := NewClickHouseClient(cfg)
	tailer := NewTailer(cfg, state, ch)

	if err := tailer.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("tailer error: %v", err)
	}

	log.Println("syncer stopped")
}
