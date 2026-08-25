package notebook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MACHINE state under <root>/.attn/narrate/, not a notebook document: it never goes through Store.Write, whose CleanPath rejects dotdir and non-.md paths.
const (
	narrateDir       = "narrate"
	narrateStateFile = "state.json"

	narrateCronStateVersion = 1
)

func NarrateCronStateDir(root string) string {
	return filepath.Join(root, machineDir, narrateDir)
}

// enqueueDueDailyNarrates is its SOLE writer, so there is no two-writer race on state.json.
type NarrateCronState struct {
	Version       int    `json:"version"`
	ScheduledFrom string `json:"scheduled_from,omitempty"`
}

func LoadNarrateCronState(root string) (NarrateCronState, error) {
	path := filepath.Join(NarrateCronStateDir(root), narrateStateFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NarrateCronState{}, nil
	}
	if err != nil {
		return NarrateCronState{}, err
	}
	var state NarrateCronState
	if err := json.Unmarshal(data, &state); err != nil {
		return NarrateCronState{}, fmt.Errorf("notebook: parse %s: %w", narrateStateFile, err)
	}
	return state, nil
}

func SaveNarrateCronState(root string, state NarrateCronState) error {
	state.Version = narrateCronStateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(NarrateCronStateDir(root), narrateStateFile), data)
}
