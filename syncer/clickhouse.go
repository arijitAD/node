package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

// ClickHouseClient inserts rows into ClickHouse via the HTTP interface.
type ClickHouseClient struct {
	cfg    Config
	client *http.Client
}

// NewClickHouseClient creates a new ClickHouse HTTP client.
func NewClickHouseClient(cfg Config) *ClickHouseClient {
	return &ClickHouseClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Insert sends a batch of rows to ClickHouse using JSONEachRow format.
// Retries up to 3 times with exponential backoff on failure.
func (c *ClickHouseClient) Insert(rows []FlatFill) error {
	if len(rows) == 0 {
		return nil
	}

	// Encode all rows as JSONL (one JSON object per line)
	var buf bytes.Buffer
	for _, row := range rows {
		if err := json.NewEncoder(&buf).Encode(row); err != nil {
			return fmt.Errorf("encode row: %w", err)
		}
	}
	body := buf.Bytes()

	// Build the ClickHouse HTTP URL with the INSERT query
	query := fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow",
		c.cfg.ClickHouseDB, c.cfg.ClickHouseTable)

	u, err := url.Parse(c.cfg.ClickHouseURL)
	if err != nil {
		return fmt.Errorf("parse clickhouse url: %w", err)
	}
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()

	// Retry with backoff
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 2 * time.Second
			log.Printf("retry %d/%d after %s", attempt+1, 3, backoff)
			time.Sleep(backoff)
		}

		req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		if c.cfg.ClickHouseUser != "" {
			req.Header.Set("X-ClickHouse-User", c.cfg.ClickHouseUser)
		}
		if c.cfg.ClickHousePass != "" {
			req.Header.Set("X-ClickHouse-Key", c.cfg.ClickHousePass)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http error: %w", err)
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return nil
		}

		lastErr = fmt.Errorf("clickhouse %d: %s", resp.StatusCode, string(respBody))
	}

	return fmt.Errorf("after 3 attempts: %w", lastErr)
}
