package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// RawFill is the format written by hl-visor to node_fills.
// Each fill has two sides: side_info[0] = buyer, side_info[1] = seller.
type RawFill struct {
	Coin        string     `json:"coin"`
	Side        string     `json:"side"`      // "B" = buy-initiated (taker was buyer), "A" = sell-initiated
	Time        string     `json:"time"`      // "2024-07-26T08:26:25.899"
	Px          string     `json:"px"`        // Fill price
	Sz          string     `json:"sz"`        // Fill size
	Hash        string     `json:"hash"`      // Transaction hash
	TID         int64      `json:"tid"`       // Trade ID
	SideInfo    []SideInfo `json:"side_info"` // [0] = buyer, [1] = seller
	DeployerFee *string    `json:"deployerFee,omitempty"` // HIP-3 fills only
}

// SideInfo contains per-side information for a fill.
type SideInfo struct {
	User     string  `json:"user"`
	StartPos string  `json:"start_pos"` // Position size before this fill
	OID      int64   `json:"oid"`       // Order ID
	TwapID   *int64  `json:"twap_id"`
	CLOID    *string `json:"cloid"`     // Client order ID
}

// FlatFill is a single flattened row ready for ClickHouse insertion.
// Each raw fill produces two FlatFill rows (one per side).
type FlatFill struct {
	Address       string `json:"address"`
	Coin          string `json:"coin"`
	Side          string `json:"side"`           // "buy" or "sell"
	Size          string `json:"size"`
	Price         string `json:"price"`
	StartPosition string `json:"start_position"`
	Hash          string `json:"hash"`
	Timestamp     string `json:"timestamp"`
	TID           int64  `json:"tid"`
	OID           int64  `json:"oid"`
	CLOID         string `json:"cloid"`
	IsTaker       bool   `json:"is_taker"`       // true if this side was the taker
	ProcessedAt   string `json:"processed_at"`   // insertion time for ReplacingMergeTree
}

// TransformFill takes a raw JSONL line from node_fills and returns two flat rows.
func TransformFill(line string) ([]FlatFill, error) {
	var raw RawFill
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, fmt.Errorf("unmarshal fill: %w", err)
	}

	if len(raw.SideInfo) < 2 {
		return nil, fmt.Errorf("expected 2 side_info entries, got %d (hash=%s)", len(raw.SideInfo), raw.Hash)
	}

	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	// Determine which side was the taker based on the fill's side field.
	// "B" = buy-initiated → buyer was taker
	// "A" = sell-initiated → seller was taker
	buyerIsTaker := raw.Side == "B"

	buyerCLOID := ""
	if raw.SideInfo[0].CLOID != nil {
		buyerCLOID = *raw.SideInfo[0].CLOID
	}
	sellerCLOID := ""
	if raw.SideInfo[1].CLOID != nil {
		sellerCLOID = *raw.SideInfo[1].CLOID
	}

	return []FlatFill{
		{
			Address:       raw.SideInfo[0].User,
			Coin:          raw.Coin,
			Side:          "buy",
			Size:          raw.Sz,
			Price:         raw.Px,
			StartPosition: raw.SideInfo[0].StartPos,
			Hash:          raw.Hash,
			Timestamp:     raw.Time,
			TID:           raw.TID,
			OID:           raw.SideInfo[0].OID,
			CLOID:         buyerCLOID,
			IsTaker:       buyerIsTaker,
			ProcessedAt:   now,
		},
		{
			Address:       raw.SideInfo[1].User,
			Coin:          raw.Coin,
			Side:          "sell",
			Size:          raw.Sz,
			Price:         raw.Px,
			StartPosition: raw.SideInfo[1].StartPos,
			Hash:          raw.Hash,
			Timestamp:     raw.Time,
			TID:           raw.TID,
			OID:           raw.SideInfo[1].OID,
			CLOID:         sellerCLOID,
			IsTaker:       !buyerIsTaker,
			ProcessedAt:   now,
		},
	}, nil
}
