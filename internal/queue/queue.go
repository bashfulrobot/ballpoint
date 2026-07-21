// Package queue is the append-only dispatch queue the TUI writes and the issue
// #6 dispatcher drains. Outward actions (a Slack nudge, an email) are queued
// here and never sent from the TUI.
package queue

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one queued outward action, serialised as one JSON object per line.
type Entry struct {
	ID       string    `json:"id"`
	TaskID   string    `json:"task_id"`
	TaskRef  string    `json:"task_ref"`
	Channel  string    `json:"channel"`
	To       string    `json:"to"`
	Body     string    `json:"body"`
	QueuedAt time.Time `json:"queued_at"`
}

func dir(root string) string  { return filepath.Join(root, "queue") }
func file(root string) string { return filepath.Join(dir(root), "pending.jsonl") }

// Append adds one entry. The file is append-only, so the dispatcher can stream
// it and the TUI never has to rewrite it.
func Append(root string, e Entry) error {
	if err := os.MkdirAll(dir(root), 0o700); err != nil {
		return fmt.Errorf("creating queue directory: %w", err)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding queue entry: %w", err)
	}
	f, err := os.OpenFile(file(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening queue: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("appending to queue: %w", err)
	}
	return nil
}

// Load reads every queued entry in order. A missing file is an empty queue.
func Load(root string) ([]Entry, error) {
	f, err := os.Open(file(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening queue: %w", err)
	}
	defer func() { _ = f.Close() }()

	var out []Entry
	sc := bufio.NewScanner(f)
	// A queued outward message body can be large; lift the line cap well above
	// the default 64 KiB so a long draft is not silently truncated mid-decode.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("decoding queue entry: %w", err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading queue: %w", err)
	}
	return out, nil
}
