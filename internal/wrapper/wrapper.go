package wrapper

import (
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/hooks"
)

func GenerateSessionID() string {
	return uuid.New().String()
}

func DefaultLabel() string {
	dir, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return filepath.Base(dir)
}

// Creates a subdirectory to isolate from other temp files (avoids fs.watch issues).
func WriteSettingsConfig(tmpDir, sessionID, content string) (string, error) {
	settingsDir := filepath.Join(tmpDir, "attn-hooks-"+sessionID)
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		return "", err
	}
	configPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		return "", err
	}
	return configPath, nil
}

func WriteHooksConfig(tmpDir, sessionID, socketPath, wrapperPath string) (string, error) {
	content := hooks.Generate(sessionID, socketPath, wrapperPath, nil)
	return WriteSettingsConfig(tmpDir, sessionID, content)
}

func CleanupHooksConfig(configPath string) {
	os.Remove(configPath)
	os.Remove(filepath.Dir(configPath))
}
