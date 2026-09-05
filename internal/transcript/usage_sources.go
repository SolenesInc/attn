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
)

type UsageSource struct {
	ID   string
	Path string
	Root bool
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
		rootPath: filepath.Clean(rootPath),
		cache:    make(map[string]codexUsageCandidate),
	}
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
}

func (r *codexUsageSourceResolver) Discover() ([]UsageSource, error) {
	rootMeta, err := r.candidate(r.rootPath)
	if err != nil {
		return nil, err
	}
	if !rootMeta.complete || rootMeta.id == "" {
		return []UsageSource{{ID: r.rootPath, Path: r.rootPath, Root: true}}, nil
	}
	sessionsDir := codexSessionsRoot(r.rootPath)
	if sessionsDir == "" {
		return []UsageSource{{ID: r.rootPath, Path: r.rootPath, Root: true}}, nil
	}

	paths := make([]string, 0)
	err = filepath.WalkDir(sessionsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" || path == r.rootPath {
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

	lineage := map[string]struct{}{rootMeta.id: {}}
	sources := []UsageSource{{ID: r.rootPath, Path: r.rootPath, Root: true}}
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
			sources = append(sources, UsageSource{ID: candidate.id, Path: path})
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
			ID     string          `json:"id"`
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	candidate.complete = true
	if json.Unmarshal(line, &envelope) == nil && envelope.Type == "session_meta" {
		candidate.id = strings.TrimSpace(envelope.Payload.ID)
		candidate.parentID = codexThreadSpawnParent(envelope.Payload.Source)
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

func codexSessionsRoot(path string) string {
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "sessions" {
			return dir
		}
	}
	return ""
}
