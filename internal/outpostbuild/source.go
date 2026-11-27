package outpostbuild

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/victorarias/attn/internal/bridge"
)

const sourceFileName = "build-source.json"

// Source names the machine that holds the source of a build made from source
// elsewhere, which this machine received from it.
type Source struct {
	Build     bridge.Build `json:"build"`
	MachineID string       `json:"machine_id"`
	Name      string       `json:"name"`
}

func WriteSource(dataDir string, source Source) error {
	data, err := json.Marshal(source)
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, sourceFileName)
	if err := os.WriteFile(path+".tmp", data, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func ForgetSource(dataDir string) error {
	if err := os.Remove(filepath.Join(dataDir, sourceFileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// SourceOf returns the machine holding build's source when this machine received build from it.
func SourceOf(dataDir string, build bridge.Build) (Source, bool) {
	data, err := os.ReadFile(filepath.Join(dataDir, sourceFileName))
	if err != nil {
		return Source{}, false
	}
	var source Source
	if json.Unmarshal(data, &source) != nil || source.Build != build || source.MachineID == "" {
		return Source{}, false
	}
	return source, true
}
