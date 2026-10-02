package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/sessioncost"
)

type UsageSource struct {
	ID      string
	Path    string
	Root    bool
	Purpose string
}

type UsageSourceResolver interface {
	Discover() ([]UsageSource, error)
}

func NewClaudeUsageSourceResolver(rootPath string) UsageSourceResolver {
	return &claudeUsageSourceResolver{rootPath: filepath.Clean(rootPath)}
}

type claudeUsageSourceResolver struct {
	rootPath string
}

func (r *claudeUsageSourceResolver) Discover() ([]UsageSource, error) {
	sources := []UsageSource{{ID: r.rootPath, Path: r.rootPath, Root: true}}
	if filepath.Ext(r.rootPath) != ".jsonl" {
		return sources, nil
	}
	dir := strings.TrimSuffix(r.rootPath, ".jsonl")
	dir = filepath.Join(dir, "subagents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return sources, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		sources = append(sources, UsageSource{ID: path, Path: path})
	}
	sort.Slice(sources[1:], func(i, j int) bool {
		return sources[i+1].Path < sources[j+1].Path
	})
	return sources, nil
}

func NewCodexUsageSourceResolver(rootPath string) UsageSourceResolver {
	return &codexUsageSourceResolver{
		rootPath: codexUsageSourceIdentity(filepath.Clean(rootPath)),
		cache:    make(map[string]codexUsageCandidate),
	}
}

// ResolveCodexRolloutPath follows native archive moves without changing source identity.
func ResolveCodexRolloutPath(path string) string {
	path = codexUsageSourceIdentity(filepath.Clean(path))
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if sessionsDir := codexSessionsRoot(path); sessionsDir != "" {
			return filepath.Join(filepath.Dir(sessionsDir), "archived_sessions", filepath.Base(path))
		}
	}
	return path
}

type codexUsageSourceResolver struct {
	rootPath string
	cache    map[string]codexUsageCandidate
}

type codexUsageCandidate struct {
	size     int64
	complete bool
	id       string
	parentID string
	purpose  string
}

func (r *codexUsageSourceResolver) Discover() ([]UsageSource, error) {
	rootPath := ResolveCodexRolloutPath(r.rootPath)
	sessionsDir := codexSessionsRoot(r.rootPath)
	rootMeta, err := r.candidate(rootPath)
	if err != nil {
		return nil, err
	}
	if !rootMeta.complete || rootMeta.id == "" {
		return []UsageSource{{ID: r.rootPath, Path: rootPath, Root: true}}, nil
	}
	if sessionsDir == "" {
		return []UsageSource{{ID: r.rootPath, Path: rootPath, Root: true}}, nil
	}

	paths := make([]string, 0)
	for _, dir := range []string{sessionsDir, filepath.Join(filepath.Dir(sessionsDir), "archived_sessions")} {
		err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" || path == rootPath {
				return nil
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)

	candidates := make(map[string]codexUsageCandidate, len(paths))
	for _, path := range paths {
		candidate, candidateErr := r.candidate(path)
		if candidateErr != nil || !candidate.complete || candidate.id == "" || candidate.parentID == "" {
			continue
		}
		candidates[path] = candidate
	}

	lineage := map[string]struct{}{rootMeta.id: {}}
	sources := []UsageSource{{ID: r.rootPath, Path: rootPath, Root: true}}
	remaining := candidates
	for len(remaining) > 0 {
		added := false
		for _, path := range paths {
			candidate, ok := remaining[path]
			if !ok {
				continue
			}
			if _, ok := lineage[candidate.parentID]; !ok {
				continue
			}
			lineage[candidate.id] = struct{}{}
			sources = append(sources, UsageSource{ID: candidate.id, Path: path, Purpose: candidate.purpose})
			delete(remaining, path)
			added = true
		}
		if !added {
			break
		}
	}
	return sources, nil
}

func (r *codexUsageSourceResolver) candidate(path string) (codexUsageCandidate, error) {
	info, err := os.Stat(path)
	if err != nil {
		return codexUsageCandidate{}, err
	}
	if cached, ok := r.cache[path]; ok && (cached.complete || cached.size == info.Size()) {
		return cached, nil
	}
	candidate := codexUsageCandidate{size: info.Size()}
	line, complete, err := firstCompleteJSONLRecord(path)
	if err != nil {
		return codexUsageCandidate{}, err
	}
	if !complete {
		r.cache[path] = candidate
		return candidate, nil
	}
	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			ID             string          `json:"id"`
			ParentThreadID string          `json:"parent_thread_id"`
			Source         json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	candidate.complete = true
	if json.Unmarshal(line, &envelope) == nil && envelope.Type == "session_meta" {
		candidate.id = strings.TrimSpace(envelope.Payload.ID)
		candidate.parentID = codexThreadSpawnParent(envelope.Payload.Source)
		if candidate.parentID == "" && codexGuardianSource(envelope.Payload.Source) {
			candidate.parentID = strings.TrimSpace(envelope.Payload.ParentThreadID)
			candidate.purpose = sessioncost.PurposeGuardian
		}
	}
	r.cache[path] = candidate
	return candidate, nil
}

func firstCompleteJSONLRecord(path string) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return bytes.TrimSpace(line), true, nil
}

func codexThreadSpawnParent(raw json.RawMessage) string {
	var source struct {
		Subagent struct {
			ThreadSpawn *struct {
				ParentThreadID string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if json.Unmarshal(raw, &source) != nil || source.Subagent.ThreadSpawn == nil {
		return ""
	}
	return strings.TrimSpace(source.Subagent.ThreadSpawn.ParentThreadID)
}

func codexGuardianSource(raw json.RawMessage) bool {
	var source struct {
		Subagent struct {
			Other string `json:"other"`
		} `json:"subagent"`
	}
	return json.Unmarshal(raw, &source) == nil && source.Subagent.Other == "guardian"
}

func codexSessionsRoot(path string) string {
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "sessions" {
			return dir
		}
		if filepath.Base(dir) == "archived_sessions" {
			return filepath.Join(filepath.Dir(dir), "sessions")
		}
	}
	return ""
}

// Native archive flattens the dated rollout path; unarchive restores its date.
// Keep the live path as source identity so either location resumes the same cursor.
func codexUsageSourceIdentity(path string) string {
	if filepath.Base(filepath.Dir(path)) != "archived_sessions" {
		return path
	}
	stamp, _, _ := strings.Cut(strings.TrimPrefix(filepath.Base(path), "rollout-"), "T")
	date, err := time.Parse("2006-01-02", stamp)
	if err != nil {
		return path
	}
	return filepath.Join(filepath.Dir(filepath.Dir(path)), "sessions", date.Format("2006/01/02"), filepath.Base(path))
}

func NewReportedUsageSourceResolver(rootPath string) UsageSourceResolver {
	return &reportedUsageSourceResolver{rootPath: filepath.Clean(rootPath)}
}

type reportedUsageSourceResolver struct {
	rootPath string
}

func (r *reportedUsageSourceResolver) Discover() ([]UsageSource, error) {
	return []UsageSource{{ID: r.rootPath, Path: r.rootPath, Root: true}}, nil
}
