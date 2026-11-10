package store

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const FileActivitySourceOpened = "opened"

const FileActivitySourceEdited = "edited"

const (
	editedWeight = 0.6
	inRootBonus  = 1.5
)

func sourceWeight(source string) float64 {
	if source == FileActivitySourceEdited {
		return editedWeight
	}
	return 1
}

// HostPath is a path on the machine named by Host, empty for the home.
type HostPath struct {
	Host string
	Path string
}

// FileActivity is one open or edit of a file, on the machine named by Host
// (empty for the home).
type FileActivity struct {
	Host      string
	Path      string
	Source    string
	SessionID protocol.SessionID
	At        time.Time
}

// RecordFileActivity counts the activity as a new use of the file.
func (s *Store) RecordFileActivity(activity FileActivity) {
	s.recordFileActivity(activity, false)
}

// CatchUpFileActivity records activity reported again after a gap, counting it
// only when it is newer than what the file already holds, so activity already
// recorded before the gap is not counted twice.
func (s *Store) CatchUpFileActivity(activity FileActivity) {
	s.recordFileActivity(activity, true)
}

func (s *Store) recordFileActivity(activity FileActivity, onlyNewer bool) {
	if activity.Path == "" || activity.Source == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}

	var session any
	if activity.SessionID != "" {
		session = activity.SessionID
	}
	newer := ""
	if onlyNewer {
		newer = "WHERE julianday(excluded.last_at) > julianday(file_activity.last_at)"
	}
	s.execLog(`
		INSERT INTO file_activity (host, path, source, session_id, last_at, count)
		VALUES (?, ?, ?, ?, ?, 1)
		ON CONFLICT(host, path, source) DO UPDATE SET
			session_id = COALESCE(excluded.session_id, session_id),
			last_at = excluded.last_at,
			count = count + 1
		`+newer,
		activity.Host, activity.Path, activity.Source, session, activity.At.Local().Format(time.RFC3339))
}

func (s *Store) GetRecentFiles(limit int, root string) []protocol.FileActivity {
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`SELECT host, path, source, session_id, last_at, count FROM file_activity`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	now := time.Now()
	prefix := rootPrefix(root)
	merged := map[HostPath]*protocol.FileActivity{}
	scores := map[HostPath]float64{}
	var order []HostPath

	for rows.Next() {
		var entry protocol.FileActivity
		var session *protocol.SessionID
		var key HostPath
		if err := rows.Scan(&key.Host, &entry.Path, &entry.Source, &session, &entry.LastAt, &entry.Count); err != nil {
			continue
		}
		key.Path = entry.Path
		entry.SessionID = session
		if key.Host != "" {
			entry.Host = protocol.Ptr(key.Host)
		}

		scores[key] += frecencyScore(entry.Count, entry.LastAt, now) * sourceWeight(entry.Source)
		existing, ok := merged[key]
		if !ok {
			copied := entry
			merged[key] = &copied
			order = append(order, key)
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

	for key := range scores {
		if prefix != "" && key.Host == "" && strings.HasPrefix(key.Path, prefix) {
			scores[key] *= inRootBonus
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		ki, kj := order[i], order[j]
		li, lj := merged[ki], merged[kj]
		if scores[ki] != scores[kj] {
			return scores[ki] > scores[kj]
		}
		if li.LastAt != lj.LastAt {
			return li.LastAt > lj.LastAt
		}
		if ki.Path != kj.Path {
			return ki.Path < kj.Path
		}
		return ki.Host < kj.Host
	})

	if len(order) > limit {
		order = order[:limit]
	}
	all := make([]protocol.FileActivity, 0, len(order))
	for _, key := range order {
		all = append(all, *merged[key])
	}
	return all
}

func rootPrefix(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "/" {
		return ""
	}
	return strings.TrimSuffix(root, "/") + "/"
}

// HomeFileActivityPaths lists the home's paths with activity; another
// machine's paths can only be checked on that machine.
func (s *Store) HomeFileActivityPaths() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query("SELECT DISTINCT path FROM file_activity WHERE host = ''")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

func (s *Store) DeleteHomeFileActivity(paths ...string) {
	if len(paths) == 0 {
		return
	}
	encodedPaths, _ := json.Marshal(paths)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	s.execLog("DELETE FROM file_activity WHERE host = '' AND path IN (SELECT value FROM json_each(?))", string(encodedPaths))
}
