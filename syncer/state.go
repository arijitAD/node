package main

import (
	"encoding/json"
	"log"
	"os"
)

// State tracks the syncer's position across restarts.
// Only updated after a successful ClickHouse insert, giving at-least-once delivery.
type State struct {
	File string `json:"file"` // Absolute path to the file being tailed
	Line int    `json:"line"` // Last successfully processed line number (1-indexed)
	path string // Path to the state file on disk (not serialized)
}

// LoadState reads the persisted state from disk, or returns a fresh state if none exists.
func LoadState(path string) *State {
	s := &State{path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("no saved state at %s, starting fresh", path)
			return s
		}
		log.Printf("read state error: %v, starting fresh", err)
		return s
	}

	if err := json.Unmarshal(data, s); err != nil {
		log.Printf("parse state error: %v, starting fresh", err)
		return s
	}

	log.Printf("resumed state: file=%s line=%d", s.File, s.Line)
	return s
}

// Save persists the current state to disk atomically (write to temp, then rename).
func (s *State) Save() error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

