package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path"
	"time"
)

// ErrViewExists is a keep or fork that names a view the profile already keeps.
var ErrViewExists = errors.New("the profile already keeps a view of that name")

// ErrStaleView is a save made from a version that is no longer the view's newest.
var ErrStaleView = errors.New("the view has a newer version")

// KeptView is a view the profile keeps: its newest version, the machine its
// commands run on, and the folder where that machine holds the version
// FolderVersion.
type KeptView struct {
	ProfileID string
	Name      string
	Version   int
	// MachineID is the machine the view's commands run on; a view without
	// commands has none and is held by the home, wherever the home is.
	MachineID     string
	Folder        string
	FolderVersion int
}

const viewFileName = "view.md"

// ViewFile is where the machine of view holds its view file.
func (v KeptView) ViewFile() string { return path.Join(v.Folder, viewFileName) }

// ViewVersion is one saved state of a kept view.
type ViewVersion struct {
	Version      int
	Note         string
	RestoredFrom int
	SavedAt      time.Time
	SavedBy      string
}

// KeepView keeps the files as version 1 of a new view named name in the
// profile, whose commands run on the machine machineID.
func (s *Store) KeepView(profileID, name, machineID string, version ViewVersion, files map[string][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO views (profile_id, name, version, machine_id) VALUES (?, ?, 1, ?) ON CONFLICT DO NOTHING`, profileID, name, machineID)
	if err != nil {
		return err
	}
	if inserted, err := result.RowsAffected(); err != nil {
		return err
	} else if inserted == 0 {
		return ErrViewExists
	}
	version.Version = 1
	if err := insertViewVersion(tx, profileID, name, version, files); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveViewVersion saves the files as the view's next version, when after is
// still its newest, and answers the version saved.
func (s *Store) SaveViewVersion(profileID, name string, after int, machineID string, version ViewVersion, files map[string][]byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE views SET version = version + 1, machine_id = ?1,
		folder = CASE WHEN machine_id = ?1 THEN folder ELSE '' END, folder_version = CASE WHEN machine_id = ?1 THEN folder_version ELSE 0 END
		WHERE profile_id = ?2 AND name = ?3 AND version = ?4`, machineID, profileID, name, after)
	if err != nil {
		return 0, err
	}
	if updated, err := result.RowsAffected(); err != nil {
		return 0, err
	} else if updated == 0 {
		return 0, ErrStaleView
	}
	version.Version = after + 1
	if err := insertViewVersion(tx, profileID, name, version, files); err != nil {
		return 0, err
	}
	return version.Version, tx.Commit()
}

func insertViewVersion(tx *sql.Tx, profileID, name string, version ViewVersion, files map[string][]byte) error {
	if _, err := tx.Exec(`INSERT INTO view_versions (profile_id, name, version, note, restored_from, saved_at, saved_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		profileID, name, version.Version, version.Note, version.RestoredFrom, formatStoredTime(version.SavedAt), version.SavedBy); err != nil {
		return err
	}
	for file, content := range files {
		if _, err := tx.Exec(`INSERT INTO view_files (profile_id, name, version, file, content) VALUES (?, ?, ?, ?, ?)`, profileID, name, version.Version, file, content); err != nil {
			return err
		}
	}
	return nil
}

// KeptView is the view the profile keeps under name, when it keeps one.
func (s *Store) KeptView(profileID, name string) (KeptView, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	view := KeptView{ProfileID: profileID, Name: name}
	err := s.db.QueryRow(`SELECT version, machine_id, folder, folder_version FROM views WHERE profile_id = ? AND name = ?`, profileID, name).
		Scan(&view.Version, &view.MachineID, &view.Folder, &view.FolderVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return view, false, nil
	}
	return view, err == nil, err
}

// KeptViews is every view the profile keeps, by name.
func (s *Store) KeptViews(profileID string) ([]KeptView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT name, version, machine_id, folder, folder_version FROM views WHERE profile_id = ? ORDER BY name`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var views []KeptView
	for rows.Next() {
		view := KeptView{ProfileID: profileID}
		if err := rows.Scan(&view.Name, &view.Version, &view.MachineID, &view.Folder, &view.FolderVersion); err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, rows.Err()
}

// ViewVersions is every saved version of the kept view, oldest first.
func (s *Store) ViewVersions(profileID, name string) ([]ViewVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT version, note, restored_from, saved_at, saved_by FROM view_versions WHERE profile_id = ? AND name = ? ORDER BY version`, profileID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []ViewVersion
	for rows.Next() {
		var version ViewVersion
		var savedAt string
		if err := rows.Scan(&version.Version, &version.Note, &version.RestoredFrom, &savedAt, &version.SavedBy); err != nil {
			return nil, err
		}
		version.SavedAt = parseStoredTime(savedAt)
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

// ViewFiles is the files of one saved version of the kept view.
func (s *Store) ViewFiles(profileID, name string, version int) (map[string][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT file, content FROM view_files WHERE profile_id = ? AND name = ? AND version = ?`, profileID, name, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := map[string][]byte{}
	for rows.Next() {
		var file string
		var content []byte
		if err := rows.Scan(&file, &content); err != nil {
			return nil, err
		}
		files[file] = content
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("the view %s has no version %d", name, version)
	}
	return files, nil
}

// SetViewFolder records that host holds version of the kept view in folder.
// When the folder moved on that host, as a home-held view's does when the
// home moves to a machine with another data folder, what the user answered
// in it moves along.
func (s *Store) SetViewFolder(profileID, name, host, folder string, version int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var was string
	if err := tx.QueryRow(`SELECT folder FROM views WHERE profile_id = ? AND name = ?`, profileID, name).Scan(&was); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE views SET folder = ?, folder_version = ? WHERE profile_id = ? AND name = ?`, folder, version, profileID, name); err != nil {
		return err
	}
	if was != "" && was != folder {
		from, to := path.Join(was, viewFileName), path.Join(folder, viewFileName)
		for _, statement := range []string{
			`UPDATE view_drafts SET path = ?3 WHERE host = ?1 AND path = ?2`,
			`UPDATE view_sends SET path = ?3 WHERE host = ?1 AND path = ?2`,
			`UPDATE attachments SET owner_id = ?1 || ':' || ?3 WHERE owner_kind = 'view' AND owner_id = ?1 || ':' || ?2`,
		} {
			if _, err := tx.Exec(statement, host, from, to); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// DeleteView forgets the kept view, its versions, and what the user started
// and answered in it.
func (s *Store) DeleteView(view KeptView, host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	file := view.ViewFile()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM view_files WHERE profile_id = ? AND name = ?`, []any{view.ProfileID, view.Name}},
		{`DELETE FROM view_versions WHERE profile_id = ? AND name = ?`, []any{view.ProfileID, view.Name}},
		{`DELETE FROM views WHERE profile_id = ? AND name = ?`, []any{view.ProfileID, view.Name}},
		{`DELETE FROM view_started WHERE profile_id = ? AND view = ? AND machine_id = ?`, []any{view.ProfileID, file, host}},
		{`DELETE FROM view_drafts WHERE host = ? AND path = ?`, []any{host, file}},
		{`DELETE FROM view_sends WHERE host = ? AND path = ?`, []any{host, file}},
	} {
		if _, err := tx.Exec(statement.sql, statement.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
