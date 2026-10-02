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
	rootPath       string
	cache          map[string]codexUsageCandidate
	archiveInfo    os.FileInfo
	archiveLoaded  bool
	archived       map[string]codexUsageCandidate
	archiveLineage map[string]struct{}
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
	err = filepath.WalkDir(sessionsDir, func(path string, entry fs.DirEntry, walkErr error) error {
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
	sort.Strings(paths)

	candidates := make(map[string]codexUsageCandidate, len(paths))
	for _, path := range paths {
		candidate, candidateErr := r.candidate(path)
		if candidateErr != nil || !candidate.complete || candidate.id == "" || candidate.parentID == "" {
			continue
		}
		candidates[path] = candidate
	}

	archiveDir := filepath.Join(filepath.Dir(sessionsDir), "archived_sessions")
	archiveInfo, statErr := os.Stat(archiveDir)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, statErr
	}
	refresh := !r.archiveLoaded || !sameUsageDirectory(r.archiveInfo, archiveInfo)
	for path, candidate := range r.archived {
		if !candidate.complete {
			if next, candidateErr := r.candidate(path); candidateErr == nil {
				candidate = next
				refresh = refresh || next.complete
			}
		}
		candidates[path] = candidate
	}
	root := UsageSource{ID: r.rootPath, Path: rootPath, Root: true}
	sources, lineage := codexUsageLineage(rootMeta.id, root, candidates)
	for id := range lineage {
		if _, known := r.archiveLineage[id]; !known {
			refresh = true
		}
	}
	if !refresh {
		return sources, nil
	}

	for path := range r.archived {
		delete(candidates, path)
	}
	archivePaths := make([]string, 0)
	entries, readErr := os.ReadDir(archiveDir)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return nil, readErr
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(archiveDir, entry.Name())
		if path == rootPath {
			continue
		}
		archivePaths = append(archivePaths, path)
		candidate, candidateErr := codexUsageCandidateAt(path, nil)
		if errors.Is(candidateErr, fs.ErrNotExist) {
			continue
		}
		if candidateErr != nil {
			return nil, candidateErr
		}
		candidates[path] = candidate
	}
	sources, lineage = codexUsageLineage(rootMeta.id, root, candidates)
	matched := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		matched[source.Path] = struct{}{}
	}
	r.archived = make(map[string]codexUsageCandidate)
	for _, path := range archivePaths {
		candidate, exists := candidates[path]
		_, belongs := matched[path]
		if exists && (belongs || !candidate.complete) {
			r.archived[path] = candidate
			r.cache[path] = candidate
		} else {
			delete(r.cache, path)
		}
	}
	for path := range r.cache {
		if filepath.Dir(path) == archiveDir && path != rootPath {
			if _, retained := r.archived[path]; !retained {
				delete(r.cache, path)
			}
		}
	}
	// The pre-scan stamp lets a concurrent archive move invalidate the next read.
	r.archiveInfo, r.archiveLoaded, r.archiveLineage = archiveInfo, true, lineage
	return sources, nil
}

func sameUsageDirectory(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.ModTime() == b.ModTime() && a.Size() == b.Size()
}

func codexUsageLineage(rootID string, root UsageSource, candidates map[string]codexUsageCandidate) ([]UsageSource, map[string]struct{}) {
	paths := make([]string, 0, len(candidates))
	remaining := make(map[string]codexUsageCandidate, len(candidates))
	for path, candidate := range candidates {
		if candidate.complete && candidate.id != "" && candidate.parentID != "" {
			paths = append(paths, path)
			remaining[path] = candidate
		}
	}
	sort.Strings(paths)
	lineage := map[string]struct{}{rootID: {}}
	sources := []UsageSource{root}
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
	return sources, lineage
}

func (r *codexUsageSourceResolver) candidate(path string) (codexUsageCandidate, error) {
	return codexUsageCandidateAt(path, r.cache)
}

func codexUsageCandidateAt(path string, cache map[string]codexUsageCandidate) (codexUsageCandidate, error) {
	info, err := os.Stat(path)
	if err != nil {
		return codexUsageCandidate{}, err
	}
	if cached, ok := cache[path]; ok && (cached.complete || cached.size == info.Size()) {
		return cached, nil
	}
	candidate := codexUsageCandidate{size: info.Size()}
	line, complete, err := firstCompleteJSONLRecord(path)
	if err != nil {
		return codexUsageCandidate{}, err
	}
	if !complete {
		if cache != nil {
			cache[path] = candidate
		}
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
	if cache != nil {
		cache[path] = candidate
	}
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
