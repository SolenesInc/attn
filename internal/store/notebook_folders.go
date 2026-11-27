package store

import (
	"database/sql"
	"errors"
	"time"
)

type NotebookFolderKey struct {
	ID, Key, CopyName string
}

// NotebookFolderKeys returns the stored key of each Notebook root, storing fresh(root) for a root seen first.
func (s *Store) NotebookFolderKeys(roots []string, fresh func(root string) NotebookFolderKey) (map[string]NotebookFolderKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make(map[string]NotebookFolderKey, len(roots))
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, root := range roots {
		var key NotebookFolderKey
		switch err := tx.QueryRow(`SELECT id, folder_key, copy_name FROM notebook_folders WHERE root_path = ?`, root).Scan(&key.ID, &key.Key, &key.CopyName); {
		case errors.Is(err, sql.ErrNoRows):
			key = fresh(root)
			if _, err := tx.Exec(`INSERT INTO notebook_folders (id, folder_key, copy_name, root_path, created_at) VALUES (?, ?, ?, ?, ?)`, key.ID, key.Key, key.CopyName, root, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		}
		keys[root] = key
	}
	return keys, tx.Commit()
}
