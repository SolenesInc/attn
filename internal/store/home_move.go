package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/victorarias/attn/internal/setting"
)

// machineTables hold what a daemon knows about its own machine rather than its home, in insert order: a move
// keeps each machine's rows on that machine.
var machineTables = []string{"worktrees", "monitored_worktree_repositories", "forgotten_worktree_pins", "repo_integration_branches", "repo_merged_branches", "worktree_sweep_log", "plugin_server_owners"}

var ErrFenced = errors.New("the home is moving; this database takes no writes until the move ends")

// writesAFenceAllows are the daemon's own bookkeeping, which a moving home keeps writing: the event bus, the copies
// queued for outposts, and its bridges' pins. None of it is home state the new home needs from after the snapshot.
var writesAFenceAllows = []string{"bus_events", "bus_consumers", "outpost_outbox", "machines"}

// Fence refuses every write to the home's state until lift runs, so nothing is written after the snapshot a move
// carries. A document write fails at once with ErrFenced; any other write fails at its commit.
func (s *Store) Fence() (lift func()) {
	s.writes.documents.fenced.Store(true)
	return func() { s.writes.documents.fenced.Store(false) }
}

// Snapshot writes a consistent copy of this database to path.
func (s *Store) Snapshot(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := s.db.Exec("VACUUM INTO ?", path)
	return err
}

// PathRemap moves a Notebook root and the profiles that use it to the target's copy of that folder.
type PathRemap struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Profiles []string `json:"profiles,omitempty"`
}

// MovedHome rewrites a snapshot of the home's database into the database the target runs as the new home.
type MovedHome struct {
	Home, Target  string
	HomeTransport string
	HomeDial      string
	HomeHostKey   string
	HomeDaemonID  string
	Remaps        []PathRemap
	// TargetStream is the suffix the target's analytics streams carry at the home, the same one its forwards carry;
	// empty when analytics is off, which leaves nothing of the target's to forward.
	TargetStream string
}

// BuildMovedHome writes, at out, the new home's database for this machine, the target, from a snapshot of the
// home's and this machine's own database at machineDB: the two machines' rows trade roles by id, this machine's own
// tables, session runtime and machine settings replace the home's, its unsent analytics join the home's as forwarded
// records, and Notebook roots move to their copies here. No other row changes, so every stored machine reference
// stays valid. The analytics stay the home's, as the analytics token that moves with the home names their sequences.
func BuildMovedHome(ctx context.Context, m MovedHome, snapshot, machineDB, out string) error {
	if m.HomeTransport != "ssh" && m.HomeTransport != TransportReverse {
		return fmt.Errorf("the old home cannot be reached as %q", m.HomeTransport)
	}
	if err := schemaOfThisBuild(snapshot); err != nil {
		return err
	}
	if err := copySnapshot(snapshot, out); err != nil {
		return err
	}
	return rewriteAttached(ctx, out, machineDB, "machine", func(tx *sql.Tx) error {
		var self string
		if err := tx.QueryRow(`SELECT id FROM main.machines WHERE transport = 'self'`).Scan(&self); err != nil {
			return fmt.Errorf("the snapshot names no home machine: %w", err)
		}
		if self != m.Home {
			return fmt.Errorf("the snapshot is of machine %s, not the home %s", self, m.Home)
		}
		var hostKey, daemonID, pairedAt any
		if m.HomeHostKey != "" {
			hostKey, daemonID, pairedAt = m.HomeHostKey, m.HomeDaemonID, time.Now().UTC().Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec(`UPDATE main.machines SET transport = ?, destination = ?, host_key = ?, daemon_id = ?, paired_at = ? WHERE id = ?`, m.HomeTransport, m.HomeDial, hostKey, daemonID, pairedAt, m.Home); err != nil {
			return err
		}
		result, err := tx.Exec(`UPDATE main.machines SET transport = 'self', destination = '', host_key = NULL, daemon_id = NULL, paired_at = NULL WHERE id = ?`, m.Target)
		if err := outpostWriteResult(m.Target, result, err); err != nil {
			return fmt.Errorf("the home has no row for the target: %w", err)
		}
		for _, table := range slices.Backward(machineTables) {
			if _, err := tx.Exec(`DELETE FROM main.` + table); err != nil {
				return err
			}
		}
		for _, table := range machineTables {
			if _, err := tx.Exec(`INSERT INTO main.` + table + ` SELECT * FROM machine.` + table); err != nil {
				return fmt.Errorf("keep this machine's %s: %w", table, err)
			}
		}
		for _, statement := range []string{
			`DELETE FROM main.session_runtime WHERE session_id IN (SELECT session_id FROM machine.session_runtime WHERE session_id IN (SELECT id FROM main.sessions WHERE host = ?1))`,
			`INSERT INTO main.session_runtime SELECT * FROM machine.session_runtime WHERE session_id IN (SELECT id FROM main.sessions WHERE host = ?1)`,
			`UPDATE main.session_runtime SET home_id = '' WHERE session_id IN (SELECT id FROM main.sessions WHERE host = ?1)`,
			`DELETE FROM main.settings WHERE NOT attn_home_setting(key)`,
			`INSERT INTO main.settings SELECT * FROM machine.settings WHERE copied_from = '' AND NOT attn_home_setting(key)`,
			`DELETE FROM main.outpost_outbox`,
		} {
			if _, err := tx.Exec(statement, m.Target); err != nil {
				return fmt.Errorf("%s: %w", statement, err)
			}
		}
		if m.TargetStream != "" {
			for _, statement := range []string{
				`INSERT INTO main.analytics_builds (build) SELECT build FROM machine.analytics_builds WHERE id IN (SELECT build_id FROM machine.analytics_outbox WHERE sent_at IS NULL) ON CONFLICT(build) DO NOTHING`,
				`INSERT INTO main.analytics_outbox (stream, seq, at, record, build_id)
					SELECT o.stream || ?1, o.seq, o.at, json_set(o.record, '$.stream', o.stream || ?1), (SELECT id FROM main.analytics_builds WHERE build = b.build)
					FROM machine.analytics_outbox o LEFT JOIN machine.analytics_builds b ON b.id = o.build_id
					WHERE o.sent_at IS NULL ORDER BY o.id
					ON CONFLICT(stream, seq) DO NOTHING`,
			} {
				if _, err := tx.Exec(statement, m.TargetStream); err != nil {
					return fmt.Errorf("forward this machine's unsent analytics: %w", err)
				}
			}
		}
		for _, remap := range m.Remaps {
			if _, err := tx.Exec(`UPDATE main.notebook_folders SET root_path = ? WHERE root_path = ?`, remap.To, remap.From); err != nil {
				return err
			}
			for _, profile := range remap.Profiles {
				if _, err := tx.Exec(`INSERT INTO main.profile_settings (profile_id, key, value) VALUES (?, ?, ?) ON CONFLICT(profile_id, key) DO UPDATE SET value = excluded.value`, profile, setting.NotebookRoot.Key, remap.To); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// BuildMovedOutpost writes, at out, the database the old home runs as an outpost of homeID: a new database with
// its own machine tables and settings, its home's settings as copies, and copies of the open sessions it runs, so
// its terminals keep their sessions until the new home's first copy arrives.
func BuildMovedOutpost(ctx context.Context, snapshot, out, homeID, machineID string) error {
	if err := os.Remove(out); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fresh, err := NewWithDB(out)
	if err != nil {
		return err
	}
	if err := fresh.Close(); err != nil {
		return err
	}
	return rewriteAttached(ctx, out, snapshot, "home", func(tx *sql.Tx) error {
		for _, table := range machineTables {
			if _, err := tx.Exec(`DELETE FROM main.` + table + `; `); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO main.` + table + ` SELECT * FROM home.` + table); err != nil {
				return fmt.Errorf("keep this machine's %s: %w", table, err)
			}
		}
		for _, statement := range []string{
			`UPDATE main.machines SET id = ?1 WHERE transport = 'self'`,
			`INSERT INTO main.sessions SELECT * FROM home.sessions WHERE host = ?1 AND closed_at = ''`,
			`UPDATE main.sessions SET copied_from = ?2`,
			`INSERT INTO main.terminal_bindings SELECT * FROM home.terminal_bindings WHERE session_id IN (SELECT id FROM main.sessions)`,
			`INSERT INTO main.session_runtime SELECT * FROM home.session_runtime WHERE session_id IN (SELECT id FROM main.sessions)`,
			`DELETE FROM main.settings`,
			`INSERT INTO main.settings SELECT * FROM home.settings WHERE copied_from = ''`,
			`UPDATE main.settings SET copied_from = ?2 WHERE attn_home_setting(key)`,
		} {
			if _, err := tx.Exec(statement, machineID, homeID); err != nil {
				return fmt.Errorf("%s: %w", statement, err)
			}
		}
		return nil
	})
}

func copySnapshot(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}

func rewriteAttached(ctx context.Context, path, other, name string, rewrite func(*sql.Tx) error) error {
	db, _ := connectSQLite(path)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS `+name, "file:"+other+"?mode=ro"); err != nil {
		return fmt.Errorf("attach %s: %w", other, err)
	}
	defer conn.ExecContext(context.Background(), `DETACH DATABASE `+name)
	if err := conn.Raw(func(raw any) error { raw.(*sqliteConnection).writesCopies = true; return nil }); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := rewrite(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func schemaOfThisBuild(path string) error {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	version, err := GetSchemaVersion(db)
	if err != nil {
		return err
	}
	if latest := LatestSchemaVersion(); version != latest {
		return fmt.Errorf("the home's database is at schema %d and this machine's build at %d; both machines must run one build", version, latest)
	}
	return nil
}
