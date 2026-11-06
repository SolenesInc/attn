package fakeclaudeapi

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func WriteClaudeConfig(claude, configDir, work string) error {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(work)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	out, err := exec.Command(claude, "--version").Output()
	if err != nil {
		return fmt.Errorf("claude --version: %w", err)
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	trusted := map[string]any{"hasTrustDialogAccepted": true, "hasCompletedProjectOnboarding": true}
	state := map[string]any{
		"hasCompletedOnboarding":                        true,
		"lastOnboardingVersion":                         version,
		"lastReleaseNotesSeen":                          version,
		"hasSeenAutoDefaultNotice":                      true,
		"autoModeClassifierBillingNoticeAcknowledgedAt": time.Now().UnixMilli(),
		"theme":                 "dark",
		"customApiKeyResponses": map[string]any{"approved": []string{APIKey[len(APIKey)-20:]}, "rejected": []string{}},
		"projects":              map[string]any{work: trusted, resolved: trusted},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, ".claude.json"), encoded, 0o600)
}
