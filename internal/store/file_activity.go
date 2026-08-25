package store

import (
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const FileActivitySourceOpened = "opened"

const FileActivitySourceEdited = "edited"

const (
	editedWeight     = 0.6
	inWorkspaceBonus = 1.5
)

func sourceWeight(source string) float64 {
	if source == FileActivitySourceEdited {
		return editedWeight
	}
	return 1
}

func (s *Store) RecordFileActivity(path, source, sessionID string) {
	if path == "" || source == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}

	var session any
	if sessionID != "" {
		session = sessionID
	}
	s.execLog(`
		INSERT INTO file_activity (path, source, session_id, last_at, count)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(path, source) DO UPDATE SET
			session_id = COALESCE(excluded.session_id, session_id),
			last_at = excluded.last_at,
			count = count + 1`,
		path, source, session, time.Now().Format(time.RFC3339))
}

// Rows are never stat'd here — dead files are pruned when opening them fails.
func (s *Store) GetRecentFiles(limit int, root string) []protocol.FileActivity {
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	// Pre-truncating by last_at would hide old-but-frequent files.
	rows, err := s.db.Query(`SELECT path, source, session_id, last_at, count FROM file_activity`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	now := time.Now()
	prefix := workspacePrefix(root)
	merged := map[string]*protocol.FileActivity{}
	scores := map[string]float64{}
	var order []string

	for rows.Next() {
		var entry protocol.FileActivity
		var session *string
		if err := rows.Scan(&entry.Path, &entry.Source, &session, &entry.LastAt, &entry.Count); err != nil {
			continue
		}
		entry.SessionID = session

		scores[entry.Path] += frecencyScore(entry.Count, entry.LastAt, now) * sourceWeight(entry.Source)
		existing, ok := merged[entry.Path]
		if !ok {
			copied := entry
			merged[entry.Path] = &copied
			order = append(order, entry.Path)
			continue
		}
		existing.Count += entry.Count
		sameSecondOpen := entry.LastAt == existing.LastAt && entry.Source == FileActivitySourceOpened
		if entry.LastAt > existing.LastAt || sameSecondOpen {
			existing.LastAt = entry.LastAt
			existing.Source = entry.Source
			existing.SessionID = entry.SessionID
		}
	}

	for path := range scores {
		if prefix != "" && strings.HasPrefix(path, prefix) {
			scores[path] *= inWorkspaceBonus
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		li, lj := merged[order[i]], merged[order[j]]
		if scores[li.Path] != scores[lj.Path] {
			return scores[li.Path] > scores[lj.Path]
		}
		if li.LastAt != lj.LastAt {
			return li.LastAt > lj.LastAt
		}
		return li.Path < lj.Path
	})

	if len(order) > limit {
		order = order[:limit]
	}
	all := make([]protocol.FileActivity, 0, len(order))
	for _, path := range order {
		all = append(all, *merged[path])
	}
	return all
}

// Only matches paths inside root, so /repo never claims /repo-other.
func workspacePrefix(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "/" {
		return ""
	}
	return strings.TrimSuffix(root, "/") + "/"
}

func (s *Store) DeleteFileActivity(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	s.execLog("DELETE FROM file_activity WHERE path = ?", path)
}
