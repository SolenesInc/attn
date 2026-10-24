package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/toolhome"
)

func FindCodexTranscript(cwd string, startedAt time.Time) string {
	sessionsDir := codexSessionsDir()
	if sessionsDir == "" {
		return ""
	}

	type codexLine struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Cwd       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
			Source    string `json:"source"`
		} `json:"payload"`
	}

	var bestPath string
	var bestTime time.Time
	var fallbackPath string
	var fallbackModTime time.Time
	cwdClean := filepath.Clean(cwd)

	filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		if info.ModTime().Before(startedAt.Add(-5 * time.Minute)) {
			return nil
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}

		reader := bufio.NewReader(f)
		line, readErr := reader.ReadBytes('\n')
		_ = f.Close()
		if readErr != nil && len(line) == 0 {
			return nil
		}

		var entry codexLine
		if json.Unmarshal(bytes.TrimSpace(line), &entry) != nil {
			return nil
		}
		if entry.Type != "session_meta" {
			return nil
		}

		if entry.Payload.Source == "exec" {
			return nil
		}

		if !pathsEquivalent(entry.Payload.Cwd, cwdClean) {
			return nil
		}

		ts := entry.Payload.Timestamp
		if ts == "" {
			ts = entry.Timestamp
		}
		if ts == "" {
			return nil
		}

		sessionTime, parseErr := time.Parse(time.RFC3339Nano, ts)
		if parseErr == nil && !sessionTime.Before(startedAt.Add(-5*time.Minute)) {
			if bestPath == "" || sessionTime.After(bestTime) {
				bestPath = path
				bestTime = sessionTime
			}
			return nil
		}

		if fallbackPath == "" || info.ModTime().After(fallbackModTime) {
			fallbackPath = path
			fallbackModTime = info.ModTime()
		}

		return nil
	})

	if bestPath != "" {
		return bestPath
	}
	return fallbackPath
}

func FindCodexTranscriptForResume(resumeID string) string {
	resumeID = strings.TrimSpace(resumeID)
	if resumeID == "" {
		return ""
	}
	sessionsDir := codexSessionsDir()
	if sessionsDir == "" {
		return ""
	}
	return findCodexTranscriptForResumeIn(sessionsDir, resumeID, readFirstJSONLLine)
}

func FindArchivedCodexTranscript(resumeID string) string {
	resumeID = strings.TrimSpace(resumeID)
	sessionsDir := codexSessionsDir()
	if resumeID == "" || sessionsDir == "" {
		return ""
	}
	return findCodexTranscriptForResumeIn(filepath.Join(filepath.Dir(sessionsDir), "archived_sessions"), resumeID, readFirstJSONLLine)
}

func findCodexTranscriptForResumeIn(sessionsDir, resumeID string, readLine func(string) ([]byte, error)) string {
	var found string
	_ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), resumeID+".jsonl") {
			return nil
		}
		line, readErr := readLine(path)
		if readErr != nil && len(line) == 0 {
			return nil
		}
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				ID string `json:"id"`
			} `json:"payload"`
		}
		if json.Unmarshal(bytes.TrimSpace(line), &entry) == nil && entry.Type == "session_meta" && strings.TrimSpace(entry.Payload.ID) == resumeID {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func readFirstJSONLLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return bufio.NewReader(f).ReadBytes('\n')
}

func codexSessionsDir() string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "sessions")
	}
	homeDir, err := toolhome.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".codex", "sessions")
}

func pathsEquivalent(a, b string) bool {
	aClean := filepath.Clean(a)
	bClean := filepath.Clean(b)
	if aClean == bClean {
		return true
	}
	aReal, aErr := filepath.EvalSymlinks(aClean)
	bReal, bErr := filepath.EvalSymlinks(bClean)
	return aErr == nil && bErr == nil && filepath.Clean(aReal) == filepath.Clean(bReal)
}

func FindClaudeTranscript(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}

	homeDir, err := toolhome.Dir()
	if err != nil {
		return ""
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	transcriptName := sessionID + ".jsonl"
	var found string

	filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == projectsDir {
			return nil
		}

		transcriptPath := filepath.Join(path, transcriptName)
		if _, statErr := os.Stat(transcriptPath); statErr == nil {
			found = transcriptPath
			return filepath.SkipAll
		}

		return filepath.SkipDir
	})

	return found
}
