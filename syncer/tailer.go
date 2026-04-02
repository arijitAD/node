package main

import (
	"bufio"
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Tailer watches the node_fills directory for new data and streams it to ClickHouse.
//
// It uses fsnotify for instant file-change detection and tracks the last
// processed line number per file for crash-resilient resume. On restart,
// it skips lines already sent to ClickHouse — no data is re-inserted.
type Tailer struct {
	cfg   Config
	state *State
	ch    *ClickHouseClient
	batch []FlatFill
	// lineCount tracks how many lines we've read from the current file
	// in this session (used to update state.Line after flush).
	lineCount int
}

// NewTailer creates a new Tailer.
func NewTailer(cfg Config, state *State, ch *ClickHouseClient) *Tailer {
	return &Tailer{
		cfg:   cfg,
		state: state,
		ch:    ch,
		batch: make([]FlatFill, 0, cfg.BatchSize),
	}
}

// Run starts the tailing loop. It blocks until the context is cancelled.
func (t *Tailer) Run(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Watch the root fills directory
	if err := watcher.Add(t.cfg.FillsDir); err != nil {
		// Directory may not exist yet if the node hasn't written any fills
		log.Printf("fills dir %s not found, waiting for it to appear...", t.cfg.FillsDir)
		if err := t.waitForDir(ctx, t.cfg.FillsDir); err != nil {
			return err
		}
		if err := watcher.Add(t.cfg.FillsDir); err != nil {
			return err
		}
	}

	// Watch all existing subdirectories (date folders)
	t.watchSubdirs(watcher)

	// Catch up from saved state before entering the watch loop
	t.catchUp()

	ticker := time.NewTicker(t.cfg.BatchTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Flush remaining batch before shutdown
			t.flush()
			return nil

		case event := <-watcher.Events:
			// New directory created (new date folder) — start watching it
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					watcher.Add(event.Name)
					log.Printf("watching new dir: %s", event.Name)
				}
			}

			// File was written to or created — read new lines
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				if info, err := os.Stat(event.Name); err == nil && !info.IsDir() {
					t.processFile(event.Name)
				}
			}

		case err := <-watcher.Errors:
			log.Printf("watcher error: %v", err)

		case <-ticker.C:
			// Periodic flush for partial batches
			t.flush()
		}
	}
}

// waitForDir polls until the directory exists or the context is cancelled.
func (t *Tailer) waitForDir(ctx context.Context, dir string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(dir); err == nil {
				return nil
			}
		}
	}
}

// watchSubdirs recursively adds all subdirectories to the fsnotify watcher.
func (t *Tailer) watchSubdirs(watcher *fsnotify.Watcher) {
	filepath.Walk(t.cfg.FillsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		if err := watcher.Add(path); err != nil {
			log.Printf("watch %s: %v", path, err)
		}
		return nil
	})
}

// catchUp reads any unprocessed data from the saved state file,
// then checks if there are newer files to process.
func (t *Tailer) catchUp() {
	if t.state.File != "" {
		// Resume reading from saved line
		if _, err := os.Stat(t.state.File); err == nil {
			t.processFile(t.state.File)
		}
	}

	// Check for any files newer than the saved state
	latest := t.findLatestFile()
	if latest != "" && latest != t.state.File {
		log.Printf("found newer file: %s", latest)
		t.state.File = latest
		t.state.Line = 0
		t.lineCount = 0
		t.processFile(latest)
	}
}

// findLatestFile returns the path to the most recent fills file.
// Relies on lexicographic ordering of date/hour directory names.
func (t *Tailer) findLatestFile() string {
	var files []string
	filepath.Walk(t.cfg.FillsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	return files[len(files)-1]
}

// processFile reads new lines from the given file, skipping lines already processed.
// If the file is different from the currently tracked file (hour rollover),
// it finishes reading the old file first, then switches.
func (t *Tailer) processFile(filePath string) {
	// Hour rollover: finish reading the old file before switching
	if t.state.File != "" && filePath != t.state.File && filePath > t.state.File {
		t.readFrom(t.state.File, t.state.Line)
		t.flush()
		// Switch tracking to the new file
		t.state.File = filePath
		t.state.Line = 0
		t.lineCount = 0
	}

	// Determine how many lines to skip
	skipLines := 0
	if filePath == t.state.File {
		skipLines = t.state.Line
	}

	t.readFrom(filePath, skipLines)
}

// readFrom reads new lines from a file, skipping the first `skipLines` lines.
// Each new line is transformed and added to the batch.
func (t *Tailer) readFrom(filePath string, skipLines int) {
	f, err := os.Open(filePath)
	if err != nil {
		log.Printf("open %s: %v", filePath, err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer

	// Skip already-processed lines
	lineNum := 0
	for lineNum < skipLines && scanner.Scan() {
		lineNum++
	}

	// Read new lines
	linesRead := 0
	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		if strings.TrimSpace(line) == "" {
			continue
		}

		rows, err := TransformFill(line)
		if err != nil {
			log.Printf("transform line %d: %v", lineNum, err)
			continue
		}

		t.batch = append(t.batch, rows...)
		linesRead++

		// Flush when batch is full
		if len(t.batch) >= t.cfg.BatchSize {
			t.lineCount = lineNum
			t.flush()
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("scanner %s: %v", filePath, err)
	}

	// Update line count for next flush
	t.lineCount = lineNum
	t.state.File = filePath

	if linesRead > 0 {
		log.Printf("read %d fills from %s (line %d → %d)",
			linesRead, filepath.Base(filePath), skipLines, lineNum)
	}
}

// flush sends the accumulated batch to ClickHouse and saves state on success.
func (t *Tailer) flush() {
	if len(t.batch) == 0 {
		return
	}

	if err := t.ch.Insert(t.batch); err != nil {
		log.Printf("insert failed (%d rows): %v — will retry on next flush", len(t.batch), err)
		return // Keep batch in memory; don't advance state
	}

	log.Printf("inserted %d rows into clickhouse", len(t.batch))
	t.batch = t.batch[:0]

	// Save state ONLY after successful insert (at-least-once guarantee)
	t.state.Line = t.lineCount
	if err := t.state.Save(); err != nil {
		log.Printf("save state: %v", err)
	}
}
