package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Advances only to what a wait REPORTED: advancing to everything a poll saw
// loses events permanently.
type prWaitCursor struct {
	CommentIDs    []string  `json:"comment_ids,omitempty"`
	VerdictAt     time.Time `json:"verdict_at,omitempty"`
	FailureHead   string    `json:"failure_head,omitempty"`
	FailureChecks []string  `json:"failure_checks,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// MarshalJSON keeps zero timestamps out of the encoded cursor — `omitempty`
// does not apply to time.Time, and an encoded zero verdict_at reads as a bug.
func (c prWaitCursor) MarshalJSON() ([]byte, error) {
	type payload struct {
		CommentIDs    []string   `json:"comment_ids,omitempty"`
		VerdictAt     *time.Time `json:"verdict_at,omitempty"`
		FailureHead   string     `json:"failure_head,omitempty"`
		FailureChecks []string   `json:"failure_checks,omitempty"`
		UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	}
	out := payload{CommentIDs: c.CommentIDs, FailureHead: c.FailureHead, FailureChecks: c.FailureChecks}
	if !c.VerdictAt.IsZero() {
		out.VerdictAt = &c.VerdictAt
	}
	if !c.UpdatedAt.IsZero() {
		out.UpdatedAt = &c.UpdatedAt
	}
	return json.Marshal(out)
}

func (c prWaitCursor) empty() bool {
	return len(c.CommentIDs) == 0 && c.VerdictAt.IsZero() && c.FailureHead == ""
}

func (c prWaitCursor) seenComments() map[string]bool {
	seen := make(map[string]bool, len(c.CommentIDs))
	for _, id := range c.CommentIDs {
		seen[id] = true
	}
	return seen
}

// Check order comes from the API, so compare as a set.
func (c prWaitCursor) sameFailure(head string, checks []prCheck) bool {
	if c.FailureHead != head {
		return false
	}
	names := failedCheckNames(checks)
	sort.Strings(names)
	previous := append([]string(nil), c.FailureChecks...)
	sort.Strings(previous)
	return strings.Join(names, "\n") == strings.Join(previous, "\n")
}

// Comment surfaces are queried newest-100, so an ID older than that window can
// never come back as unseen.
const prCursorFileLimit = 500

const prCursorMaxAge = 30 * 24 * time.Hour

// Every segment is a legal filename without escaping: owner/repo cannot contain
// a slash, host is a domain.
func cursorPath(dir string, opts prWaitOptions) string {
	host := opts.Host
	if host == "" {
		host = "github.com"
	}
	return filepath.Join(dir, host, opts.Owner, opts.Name, fmt.Sprintf("%d.json", opts.Number))
}

func loadPRWaitCursor(dir string, opts prWaitOptions) (prWaitCursor, error) {
	if dir == "" {
		return prWaitCursor{}, nil
	}
	data, err := os.ReadFile(cursorPath(dir, opts))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return prWaitCursor{}, nil
		}
		return prWaitCursor{}, err
	}
	var cursor prWaitCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return prWaitCursor{}, fmt.Errorf("parse cursor %s: %w", cursorPath(dir, opts), err)
	}
	return cursor, nil
}

func savePRWaitCursor(dir string, opts prWaitOptions, cursor prWaitCursor, now time.Time) error {
	if dir == "" {
		return nil
	}
	if len(cursor.CommentIDs) > prCursorFileLimit {
		cursor.CommentIDs = cursor.CommentIDs[len(cursor.CommentIDs)-prCursorFileLimit:]
	}
	cursor.UpdatedAt = now
	path := cursorPath(dir, opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cursor, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".cursor-*")
	if err != nil {
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		os.Remove(temp.Name())
		return err
	}
	if err := temp.Close(); err != nil {
		os.Remove(temp.Name())
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		os.Remove(temp.Name())
		return err
	}
	prunePRWaitCursors(dir, now)
	return nil
}

func prunePRWaitCursors(dir string, now time.Time) {
	cutoff := now.Add(-prCursorMaxAge)
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil && info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
		return nil
	})
}
