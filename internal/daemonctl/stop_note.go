package daemonctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const stopNoteFile = "daemon-stop.json"

type StopNote struct {
	PID    int       `json:"pid"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

func WriteStopNote(pidPath string, pid int, reason string) error {
	encoded, err := json.Marshal(StopNote{PID: pid, Reason: reason, At: time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("encode stop note: %w", err)
	}
	return WriteFileAtomically(stopNotePath(pidPath), encoded)
}

func ReadStopNote(pidPath string) (*StopNote, error) {
	data, err := os.ReadFile(stopNotePath(pidPath))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read stop note: %w", err)
	}
	var note StopNote
	if err := json.Unmarshal(data, &note); err != nil {
		return nil, fmt.Errorf("read stop note %s: %w", stopNotePath(pidPath), err)
	}
	return &note, nil
}

func RemoveStopNote(pidPath string) error {
	if err := os.Remove(stopNotePath(pidPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove stop note: %w", err)
	}
	return nil
}

func stopNotePath(pidPath string) string {
	return filepath.Join(filepath.Dir(pidPath), stopNoteFile)
}

func WriteFileAtomically(path string, data []byte) error {
	draft, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(draft.Name())
	if _, err := draft.Write(data); err != nil {
		draft.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := draft.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(draft.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
