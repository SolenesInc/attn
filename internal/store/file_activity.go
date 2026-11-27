package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"sort"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const FileActivitySourceOpened = "opened"

const FileActivitySourceEdited = "edited"

const (
	editedWeight  = 0.6
	useHalfLife   = 48 * time.Hour
	freshHalfLife = 30 * time.Minute
	freshBoost    = 5.0
)

func sourceWeight(source string) float64 {
	if source == FileActivitySourceEdited {
		return editedWeight
	}
	return 1
}

// HostPath is a path on the machine whose id is Host.
type HostPath struct {
	Host string
	Path string
}

// FileActivity is one open or edit of a file, on the machine whose id is Host.
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

	at := activity.At.Truncate(time.Second)
	var session any
	if activity.SessionID != "" {
		session = activity.SessionID
	}
	use := 1.0
	var stored float64
	var storedAt string
	err := s.db.QueryRow("SELECT use_score, last_at FROM file_activity WHERE host = ? AND path = ? AND source = ?",
		activity.Host, activity.Path, activity.Source).Scan(&stored, &storedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		log.Printf("[store] read file activity: %v", err)
		return
	default:
		last, err := time.Parse(time.RFC3339, storedAt)
		if err != nil {
			last = at
		}
		if onlyNewer && !at.After(last) {
			return
		}
		if at.Before(last) {
			use = stored + halved(last.Sub(at), useHalfLife)
			at, session = last, nil
		} else {
			use = stored*halved(at.Sub(last), useHalfLife) + 1
		}
	}
	s.execLog(`
		INSERT INTO file_activity (host, path, source, session_id, last_at, count, use_score)
		VALUES (?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(host, path, source) DO UPDATE SET
			session_id = COALESCE(excluded.session_id, session_id),
			last_at = excluded.last_at,
			count = count + 1,
			use_score = excluded.use_score`,
		activity.Host, activity.Path, activity.Source, session, at.Local().Format(time.RFC3339), use)
}

func halved(age, halfLife time.Duration) float64 {
	return math.Exp2(-max(age, 0).Seconds() / halfLife.Seconds())
}

// GetRecentFiles ranks every machine's files best first.
func (s *Store) GetRecentFiles() []protocol.FileActivity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`SELECT host, path, source, session_id, last_at, count, use_score FROM file_activity`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	now := time.Now()
	type ranked struct {
		file   protocol.FileActivity
		use    float64
		latest time.Time
		score  float64
	}
	merged := map[HostPath]*ranked{}
	var order []HostPath

	for rows.Next() {
		var entry protocol.FileActivity
		var session *protocol.SessionID
		var key HostPath
		var use float64
		if err := rows.Scan(&key.Host, &entry.Path, &entry.Source, &session, &entry.LastAt, &entry.Count, &use); err != nil {
			continue
		}
		key.Path = entry.Path
		entry.SessionID = session
		entry.Host = s.WireHost(key.Host)
		at, err := time.Parse(time.RFC3339, entry.LastAt)
		decayed := 0.0
		if err == nil {
			decayed = sourceWeight(entry.Source) * use * halved(now.Sub(at), useHalfLife)
		}

		existing, ok := merged[key]
		if !ok {
			merged[key] = &ranked{file: entry, use: decayed, latest: at}
			order = append(order, key)
			continue
		}
		existing.use += decayed
		existing.file.Count += entry.Count
		sameSecondOpen := at.Equal(existing.latest) && entry.Source == FileActivitySourceOpened
		if at.After(existing.latest) || sameSecondOpen {
			existing.latest = at
			existing.file.LastAt = entry.LastAt
			existing.file.Source = entry.Source
			existing.file.SessionID = entry.SessionID
		}
	}

	for _, r := range merged {
		r.score = math.Log1p(r.use)
		if !r.latest.IsZero() {
			r.score += freshBoost * halved(now.Sub(r.latest), freshHalfLife)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		ki, kj := order[i], order[j]
		ri, rj := merged[ki], merged[kj]
		if ri.score != rj.score {
			return ri.score > rj.score
		}
		if !ri.latest.Equal(rj.latest) {
			return ri.latest.After(rj.latest)
		}
		if ki.Path != kj.Path {
			return ki.Path < kj.Path
		}
		return ki.Host < kj.Host
	})

	best := make([]protocol.FileActivity, 0, len(order))
	for _, key := range order {
		best = append(best, merged[key].file)
	}
	return best
}

// DeleteFileActivity forgets the paths on the machine named by host, empty for
// the home.
func (s *Store) DeleteFileActivity(host string, paths ...string) {
	if len(paths) == 0 {
		return
	}
	encodedPaths, _ := json.Marshal(paths)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	s.execLog("DELETE FROM file_activity WHERE host = ? AND path IN (SELECT value FROM json_each(?))", host, string(encodedPaths))
}
