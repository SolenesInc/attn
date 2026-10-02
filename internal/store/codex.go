package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// CodexOwner binds an independent native root to one durable ledger identity.
type CodexOwner struct {
	SessionID    string
	ServerID     string
	NativeRootID string
	Context      json.RawMessage
	Archived     bool
}

type CodexView struct {
	RuntimeID     string
	ServerID      string
	LaunchOwnerID string
	SessionID     string
	ObservedAt    string
	RawTitle      string
	Generation    string
	Revision      uint64
	Resolution    string
}

func (s *Store) ReserveCodexOwner(owner CodexOwner) error {
	if s.db == nil {
		return errors.New("shared Codex requires a database")
	}
	_, err := s.db.Exec(`INSERT INTO codex_owners(session_id,server_id,context_json) VALUES(?,?,?)`, owner.SessionID, owner.ServerID, string(owner.Context))
	return err
}

func scanCodexOwner(row interface{ Scan(...any) error }) (*CodexOwner, error) {
	var o CodexOwner
	var id sql.NullString
	var context string
	if err := row.Scan(&o.SessionID, &o.ServerID, &id, &context, &o.Archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	o.NativeRootID = id.String
	o.Context = json.RawMessage(context)
	return &o, nil
}

func (s *Store) CodexOwner(sessionID string) (*CodexOwner, error) {
	if s.db == nil {
		return nil, nil
	}
	return scanCodexOwner(s.db.QueryRow(`SELECT session_id,server_id,native_root_id,context_json,archived FROM codex_owners WHERE session_id=?`, sessionID))
}

func (s *Store) CodexOwnerByRoot(serverID, rootID string) (*CodexOwner, error) {
	if s.db == nil {
		return nil, nil
	}
	return scanCodexOwner(s.db.QueryRow(`SELECT session_id,server_id,native_root_id,context_json,archived FROM codex_owners WHERE server_id=? AND native_root_id=?`, serverID, rootID))
}

func (s *Store) BindCodexRoot(sessionID, rootID string) error {
	result, err := s.db.Exec(`UPDATE codex_owners SET native_root_id=? WHERE session_id=? AND (native_root_id IS NULL OR native_root_id=?)`, rootID, sessionID, rootID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("codex owner %s cannot bind root %s", sessionID, rootID)
	}
	return nil
}

func (s *Store) RemoveUnusedCodexOwner(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM codex_owners WHERE session_id=? AND native_root_id IS NULL`, sessionID)
	return err
}

func (s *Store) SetCodexArchived(sessionID string, archived bool) error {
	_, err := s.db.Exec(`UPDATE codex_owners SET archived=? WHERE session_id=?`, archived, sessionID)
	return err
}

func (s *Store) CodexOwners(serverID string) ([]CodexOwner, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT session_id,server_id,native_root_id,context_json,archived FROM codex_owners WHERE server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var owners []CodexOwner
	for rows.Next() {
		o, err := scanCodexOwner(rows)
		if err != nil {
			return nil, err
		}
		owners = append(owners, *o)
	}
	return owners, rows.Err()
}

func (s *Store) ReserveCodexOwnerAndView(owner CodexOwner, view CodexView) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO codex_owners(session_id,server_id,context_json) VALUES(?,?,?)`, owner.SessionID, owner.ServerID, string(owner.Context)); err != nil {
		return err
	}
	if err := saveCodexView(tx.Exec, view); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveCodexView(v CodexView) error { return saveCodexView(s.db.Exec, v) }

func saveCodexView(exec func(string, ...any) (sql.Result, error), v CodexView) error {
	_, err := exec(`INSERT INTO codex_views(runtime_id,server_id,launch_owner_id,session_id,raw_title,observed_at,generation,revision,resolution) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(runtime_id) DO UPDATE SET session_id=excluded.session_id,raw_title=excluded.raw_title,observed_at=excluded.observed_at,generation=excluded.generation,revision=excluded.revision,resolution=excluded.resolution`, v.RuntimeID, v.ServerID, v.LaunchOwnerID, v.SessionID, v.RawTitle, v.ObservedAt, v.Generation, v.Revision, v.Resolution)
	return err
}

func (s *Store) CodexViews() ([]CodexView, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT runtime_id,server_id,launch_owner_id,session_id,raw_title,observed_at,generation,revision,resolution FROM codex_views`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var views []CodexView
	for rows.Next() {
		var v CodexView
		if err := rows.Scan(&v.RuntimeID, &v.ServerID, &v.LaunchOwnerID, &v.SessionID, &v.RawTitle, &v.ObservedAt, &v.Generation, &v.Revision, &v.Resolution); err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

func (s *Store) RemoveCodexView(runtimeID string) error {
	_, err := s.db.Exec(`DELETE FROM codex_views WHERE runtime_id=?`, runtimeID)
	return err
}

func (s *Store) UpdateCodexContext(id string, context json.RawMessage) error {
	_, err := s.db.Exec(`UPDATE codex_owners SET context_json=? WHERE session_id=?`, string(context), id)
	return err
}

// Consume only the pending name; concurrent launch-context changes remain intact.
func (s *Store) ConsumeCodexInitialName(id, root string) error {
	result, err := s.db.Exec(`UPDATE codex_owners SET context_json=json_remove(context_json,'$.InitialName') WHERE session_id=? AND native_root_id=? AND archived=0`, id, root)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("codex owner %s is no longer live on root %s", id, root)
	}
	return nil
}
