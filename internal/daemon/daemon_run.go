package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/diag"
)

const runFile = "daemon-run.json"

const (
	previousEndStopped            = "stopped"
	previousEndMachineRestarted   = "machine_restarted"
	previousEndExitedUnexpectedly = "exited_unexpectedly"
	previousEndUnknown            = "unknown"
)

type daemonRun struct {
	ID        string    `json:"run"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

type previousEnd struct {
	End        string `json:"previous_end"`
	StopReason string `json:"stop_reason,omitempty"`
	Run        string `json:"previous_run,omitempty"`
}

func (d *Daemon) beginRun(now time.Time) (daemonRun, previousEnd, error) {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	run := daemonRun{ID: hex.EncodeToString(id), PID: os.Getpid(), StartedAt: now.UTC()}
	var problems []error
	prev, err := readRunFile(d.runFilePath())
	problems = append(problems, err)
	note, err := daemonctl.ReadStopNote(d.pidPath)
	problems = append(problems, err)
	boot, err := diag.BootTime()
	problems = append(problems, err)
	previous := classifyPreviousEnd(prev, note, boot)
	problems = append(problems, writeRunFile(d.runFilePath(), run), daemonctl.RemoveStopNote(d.pidPath))
	return run, previous, errors.Join(problems...)
}

func classifyPreviousEnd(prev *daemonRun, note *daemonctl.StopNote, boot time.Time) previousEnd {
	switch {
	case prev == nil:
		return previousEnd{End: previousEndUnknown}
	case note != nil && note.PID == prev.PID:
		return previousEnd{End: previousEndStopped, StopReason: note.Reason, Run: prev.ID}
	case boot.IsZero():
		return previousEnd{End: previousEndUnknown, Run: prev.ID}
	case boot.After(prev.StartedAt):
		return previousEnd{End: previousEndMachineRestarted, Run: prev.ID}
	default:
		return previousEnd{End: previousEndExitedUnexpectedly, Run: prev.ID}
	}
}

func (d *Daemon) runFilePath() string {
	return filepath.Join(filepath.Dir(d.pidPath), runFile)
}

func readRunFile(path string) (*daemonRun, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run file: %w", err)
	}
	var run daemonRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("read run file %s: %w", path, err)
	}
	return &run, nil
}

func writeRunFile(path string, run daemonRun) error {
	encoded, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode run file: %w", err)
	}
	return daemonctl.WriteFileAtomically(path, encoded)
}
