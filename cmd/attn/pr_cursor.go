package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/prreadiness"
)

type prWaitCursor struct {
	prreadiness.Cursor
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

const prCursorMaxAge = 30 * 24 * time.Hour

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
	cursor.UpdatedAt = &now
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
