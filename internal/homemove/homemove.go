package homemove

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/enrollment"
)

const DirName = "home-move"

type Side string

const (
	SideFrom Side = "from"
	SideTo   Side = "to"
)

type Step string

const (
	StepStaged     Step = "staged"
	StepCommitted  Step = "committed"
	StepApplied    Step = "applied"
	StepRolledBack Step = "rolled-back"
	StepAborted    Step = "aborted"
	// StepCommitting is the old home's step from asking the target to commit until it commits itself.
	StepCommitting Step = "committing"
	// StepRollingBack asks this machine to roll the move back at its next start.
	StepRollingBack Step = "rolling-back"
	// StepDone is the old home's step once it has bridged to the new home: the new home has taken over, and the way
	// back is another move.
	StepDone Step = "done"
)

type Machine struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DaemonID string `json:"daemon_id"`
}

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode,omitempty"`
	// Link is the target of a symbolic link, which moves as the link itself.
	Link string `json:"link,omitempty"`
}

// Record is one machine's side of a move, in <data-dir>/home-move/<id>/move.json. The machine that applies it
// swaps in the database staged beside the record, writes Enrollment, and puts Roots in place.
type Record struct {
	ID         string            `json:"id"`
	Side       Side              `json:"side"`
	HomeID     string            `json:"home_id"`
	From       Machine           `json:"from"`
	To         Machine           `json:"to"`
	Step       Step              `json:"step"`
	Enrollment enrollment.Record `json:"enrollment"`
	Roots      []string          `json:"roots,omitempty"`
	Files      []File            `json:"files,omitempty"`
	Displaced  []string          `json:"displaced"`
	// Notebook names, on the target, where each of the home's Notebook roots is now.
	Notebook  []Remap `json:"notebook,omitempty"`
	Error     string  `json:"error,omitempty"`
	StartedAt string  `json:"started_at"`
	UpdatedAt string  `json:"updated_at"`
}

type Remap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (r Record) Open() bool { return r.Step == StepStaged || r.Step == StepCommitted }

func (r Record) Finished() bool { return r.Step == StepApplied }

func Dir(dataRoot, id string) string { return filepath.Join(dataRoot, DirName, id) }

func StagedDatabase(dataRoot, id string) string { return filepath.Join(Dir(dataRoot, id), "attn.db") }

func StagedFiles(dataRoot, id string) string { return filepath.Join(Dir(dataRoot, id), "files") }

func preMove(dataRoot, id string) string { return filepath.Join(Dir(dataRoot, id), "pre-move") }

func rolledBack(dataRoot, id string) string { return filepath.Join(Dir(dataRoot, id), "rolled-back") }

func recordPath(dataRoot, id string) string { return filepath.Join(Dir(dataRoot, id), "move.json") }

func Write(dataRoot string, record Record) error {
	if record.StartedAt == "" {
		record.StartedAt = now()
	}
	record.UpdatedAt = now()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(dataRoot, record.ID), 0o700); err != nil {
		return err
	}
	path := recordPath(dataRoot, record.ID)
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Read(dataRoot, id string) (Record, error) {
	data, err := os.ReadFile(recordPath(dataRoot, id))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("read move %s: %w", id, err)
	}
	return record, nil
}

// Latest is the newest move this data folder took part in.
func Latest(dataRoot string) (Record, bool, error) {
	entries, err := os.ReadDir(filepath.Join(dataRoot, DirName))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var latest Record
	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, err := Read(dataRoot, entry.Name())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Record{}, false, err
		}
		if !found || record.StartedAt > latest.StartedAt || record.StartedAt == latest.StartedAt && record.ID > latest.ID {
			latest, found = record, true
		}
	}
	return latest, found, nil
}

// Abort forgets a move that was staged and never committed. Nothing outside its folder changed.
func Abort(dataRoot, id string) error {
	record, err := Read(dataRoot, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.Step != StepStaged && record.Step != StepAborted {
		return fmt.Errorf("move %s is %s; only a staged move aborts", id, record.Step)
	}
	return os.RemoveAll(Dir(dataRoot, id))
}

// Apply finishes a committed move while no daemon runs on dataRoot: it keeps the database, the enrollment record
// and the moving files it replaces under pre-move/, then puts the staged ones in place. Each part checks what is
// already done, so a run cut short is finished by the next.
func Apply(dataRoot, dbPath string) (Record, bool, error) {
	record, found, err := Latest(dataRoot)
	if err != nil || !found || record.Step != StepCommitted {
		return record, false, err
	}
	pre := preMove(dataRoot, record.ID)
	if err := os.MkdirAll(filepath.Join(pre, "files"), 0o700); err != nil {
		return record, false, err
	}
	staged := StagedDatabase(dataRoot, record.ID)
	if exists(staged) {
		if exists(dbPath) {
			if err := checkpoint(dbPath); err != nil {
				return record, false, err
			}
			if err := os.Rename(dbPath, filepath.Join(pre, "attn.db")); err != nil {
				return record, false, err
			}
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := removeIfExists(dbPath + suffix); err != nil {
				return record, false, err
			}
		}
		crashpoint.At("home-move-apply-database")
		if err := os.Rename(staged, dbPath); err != nil {
			return record, false, err
		}
	}
	for _, name := range RewrittenFiles {
		live, kept := filepath.Join(dataRoot, name), filepath.Join(pre, name)
		if !exists(kept) && exists(live) {
			if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
				return record, false, err
			}
			if err := copyFile(live, kept); err != nil {
				return record, false, err
			}
		}
	}
	if err := enrollment.WriteRecord(dataRoot, record.Enrollment); err != nil {
		return record, false, err
	}
	crashpoint.At("home-move-apply-enrollment")
	if record.Displaced == nil {
		record.Displaced = []string{}
		for _, root := range record.Roots {
			if exists(filepath.Join(dataRoot, root)) {
				record.Displaced = append(record.Displaced, root)
			}
		}
		if err := Write(dataRoot, record); err != nil {
			return record, false, err
		}
	}
	crashpoint.At("home-move-apply-files")
	for _, root := range record.Roots {
		live, kept := filepath.Join(dataRoot, root), filepath.Join(pre, "files", root)
		incoming := filepath.Join(StagedFiles(dataRoot, record.ID), root)
		if record.Side == SideTo && !exists(incoming) {
			continue
		}
		if exists(live) && !exists(kept) {
			if err := moveInto(live, kept); err != nil {
				return record, false, err
			}
		}
		if record.Side == SideTo {
			if err := moveInto(incoming, live); err != nil {
				return record, false, err
			}
		}
	}
	record.Step = StepApplied
	return record, true, Write(dataRoot, record)
}

// Rollback puts back what Apply replaced on this machine, keeping what the move had put in place under
// rolled-back/. A committed move that was never applied is only marked rolled back.
func Rollback(dataRoot, dbPath, id string) (Record, error) {
	record, err := Read(dataRoot, id)
	if err != nil {
		return record, err
	}
	switch record.Step {
	case StepRolledBack:
		return record, nil
	case StepCommitted, StepApplied, StepRollingBack:
	default:
		return record, fmt.Errorf("move %s is %s; only a committed move rolls back", id, record.Step)
	}
	pre, back := preMove(dataRoot, id), rolledBack(dataRoot, id)
	if err := os.MkdirAll(filepath.Join(back, "files"), 0o700); err != nil {
		return record, err
	}
	if kept := filepath.Join(pre, "attn.db"); exists(kept) {
		if exists(dbPath) {
			if err := checkpoint(dbPath); err != nil {
				return record, err
			}
			if err := os.Rename(dbPath, filepath.Join(back, "attn.db")); err != nil {
				return record, err
			}
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := removeIfExists(dbPath + suffix); err != nil {
				return record, err
			}
		}
		if err := os.Rename(kept, dbPath); err != nil {
			return record, err
		}
	}
	for _, name := range RewrittenFiles {
		if kept := filepath.Join(pre, name); exists(kept) {
			if err := os.Rename(kept, filepath.Join(dataRoot, name)); err != nil {
				return record, err
			}
		}
	}
	for _, root := range record.Roots {
		live, kept := filepath.Join(dataRoot, root), filepath.Join(pre, "files", root)
		switch {
		case slices.Contains(record.Displaced, root) && exists(kept):
			if exists(live) {
				if err := moveInto(live, filepath.Join(back, "files", root)); err != nil {
					return record, err
				}
			}
			if err := moveInto(kept, live); err != nil {
				return record, err
			}
		case !slices.Contains(record.Displaced, root) && record.Side == SideTo && exists(live):
			if err := moveInto(live, filepath.Join(back, "files", root)); err != nil {
				return record, err
			}
		}
	}
	record.Step = StepRolledBack
	return record, Write(dataRoot, record)
}

// RootsOf names the top-level entries of the data folder that move with the home.
func RootsOf(dataRoot string) []string {
	var roots []string
	for _, root := range MovingRoots {
		if exists(filepath.Join(dataRoot, root)) {
			roots = append(roots, root)
		}
	}
	return roots
}

// MovingRoots are the data folder's entries that belong to the home rather than the machine. The repository
// cache (repos/, automation/repos/) is not among them: the new home clones again on first use.
var MovingRoots = []string{"crew", "attachments", "conversations", "quick-captures", "plugins", "automation/occurrences", "automation/runs", "asks", "cloud_sign_key", "cloud_noise_key", "cloud_home_id", "analytics-secret", "analytics-token"}

// RewrittenFiles are this machine's own files that a move rewrites in place, kept so a rollback restores them.
var RewrittenFiles = []string{enrollment.RecordFileName, NotebookSyncState}

const NotebookSyncState = "notebook-sync/state.json"

// NeverMoves are the entries that name this data folder's own daemon. A move refuses to carry them.
var NeverMoves = []string{"daemon-id", "agent-key", "bridge_host_key", "client-token", enrollment.RecordFileName}

func CheckRoots(roots []string) error {
	for _, root := range roots {
		clean := filepath.Clean(root)
		if clean != root || filepath.IsAbs(root) || strings.HasPrefix(root, "..") || !slices.Contains(MovingRoots, root) {
			return fmt.Errorf("a move does not carry %q", root)
		}
		for _, never := range NeverMoves {
			if root == never || strings.HasPrefix(never, root+"/") {
				return fmt.Errorf("a move never carries %s", never)
			}
		}
	}
	return nil
}

func moveInto(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return os.Rename(from, to)
}

func checkpoint(path string) error {
	db, err := sql.Open("sqlite3", "file:"+path+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer db.Close()
	var busy, frames, done int
	if err := db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &done); err != nil {
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	if busy != 0 {
		return fmt.Errorf("checkpoint %s: the database is in use", path)
	}
	return nil
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
