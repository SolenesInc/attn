package crew

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lexicographic order over these names IS chronological order, which is why the
// freshest-letter read is a sort, not a stat of each file.
const HandoffStampLayout = "2006-01-02T15-04Z"

// MaxHandoffBytes is a tripwire: measured 2026-08-14 over the simulation's 23
// filed letters, the largest is 6,601 bytes. The refusal names both numbers.
const MaxHandoffBytes = 64000

func HandoffFileName(member string, at time.Time) string {
	return at.UTC().Format(HandoffStampLayout) + "-" + member + ".md"
}

var ErrHandoffExists = errors.New("a letter is already filed under that name")

func FileHandoff(homeDir, member, note string, at time.Time) (string, error) {
	if err := ValidateHandoffNote(note); err != nil {
		return "", err
	}
	dir := filepath.Join(homeDir, HandoffsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("making %s's handoffs directory at %s: %w", DisplayName(member), dir, err)
	}
	path := filepath.Join(dir, HandoffFileName(member, at))
	// O_EXCL is the enforcement, not a check before one: two letters racing for the
	// same minute cannot both land.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("%w: %s — a filed letter is never overwritten, so file the correction as its own letter a minute from now", ErrHandoffExists, path)
		}
		return "", fmt.Errorf("filing %s's letter at %s: %w", DisplayName(member), path, err)
	}
	defer file.Close()
	if _, err := file.WriteString(ensureTrailingNewline(note)); err != nil {
		return "", fmt.Errorf("writing %s's letter at %s: %w", DisplayName(member), path, err)
	}
	return path, nil
}

func ValidateHandoffNote(note string) error {
	if strings.TrimSpace(note) == "" {
		return errors.New("a handoff is the letter you write to your successor; there is nothing to file")
	}
	if len(note) > MaxHandoffBytes {
		return fmt.Errorf("this letter is %d bytes and one letter's limit is %d — the longest letter ever filed is 6,601, so this is something other than a letter", len(note), MaxHandoffBytes)
	}
	return nil
}

func ensureTrailingNewline(note string) string {
	if note == "" || note[len(note)-1] == '\n' {
		return note
	}
	return note + "\n"
}
