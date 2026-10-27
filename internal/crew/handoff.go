package crew

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const HandoffStampLayout = "2006-01-02T15-04Z"

const MaxHandoffBytes = 64000

const MaxHandoffFileBytes = MaxHandoffBytes + 1

type handoffSession struct {
	SessionID protocol.SessionID `json:"session_id"`
}

func HandoffSession(path string) (protocol.SessionID, error) {
	body, err := os.ReadFile(path + ".json")
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var record handoffSession
	if err := json.Unmarshal(body, &record); err != nil {
		return "", fmt.Errorf("reading letter session at %s.json: %w", path, err)
	}
	return record.SessionID, nil
}

func FileHandoff(homeDir, member, note string, at time.Time, sessionID protocol.SessionID) (string, error) {
	if err := ValidateHandoffNote(note); err != nil {
		return "", err
	}
	dir := filepath.Join(homeDir, HandoffsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("making %s's handoffs directory at %s: %w", DisplayName(member), dir, err)
	}
	pattern := "." + at.UTC().Format(HandoffStampLayout) + "-" + member + "_" + at.UTC().Format("05.000000000") + "-*.pending"
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("filing %s's letter in %s: %w", DisplayName(member), dir, err)
	}
	defer file.Close()
	defer os.Remove(file.Name())
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file.Name()), "."), ".pending") + ".md"
	path := filepath.Join(dir, name)
	if _, err := file.WriteString(ensureTrailingNewline(note)); err != nil {
		return "", fmt.Errorf("writing %s's letter at %s: %w", DisplayName(member), path, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing %s's letter at %s: %w", DisplayName(member), path, err)
	}
	if sessionID != "" {
		body, err := json.Marshal(handoffSession{SessionID: sessionID})
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path+".json", append(body, '\n'), 0o600); err != nil {
			return "", fmt.Errorf("recording %s's letter session at %s: %w", DisplayName(member), path, err)
		}
	}
	if err := os.Rename(file.Name(), path); err != nil {
		if sessionID != "" {
			_ = os.Remove(path + ".json")
		}
		return "", fmt.Errorf("filing %s's letter at %s: %w", DisplayName(member), path, err)
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
