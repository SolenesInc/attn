package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
)

var copyKeys = map[protocol.CopyTable]string{
	protocol.CopyTableSessions:         "id",
	protocol.CopyTableTerminalBindings: "terminal_id",
}

type OutboxBatch struct {
	IDs  []int64
	Rows []protocol.CopyRow
}

type outboxEntry struct {
	id    int64
	table protocol.CopyTable
	key   string
}

// OutboxChanged closes when a copy change for any outpost is next written.
func (s *Store) OutboxChanged() <-chan struct{} {
	return s.writes.outbox.wait()
}

// OutboxBatch reads host's pending copy changes in the order they were written, as the current rows or
// their deletions, up to budget bytes of encoded rows. A binding carries its session's row, so the outpost
// never holds a binding without its session.
func (s *Store) OutboxBatch(host string, budget int) (OutboxBatch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := queryOutbox(s.db, host)
	if err != nil {
		return OutboxBatch{}, err
	}
	var batch OutboxBatch
	size := len("[]")
	carried := map[string]bool{}
	for _, entry := range entries {
		rows, err := currentCopyRows(s.db, host, entry, carried)
		if err != nil {
			return OutboxBatch{}, err
		}
		grown := size
		for _, row := range rows {
			encoded, err := json.Marshal(row)
			if err != nil {
				return OutboxBatch{}, err
			}
			grown += len(encoded) + len(",")
		}
		if grown > budget {
			if len(batch.IDs) == 0 {
				return OutboxBatch{}, fmt.Errorf("copy of %s %s for outpost %s is %d bytes, over the %d-byte batch budget", entry.table, entry.key, host, grown, budget)
			}
			break
		}
		size = grown
		for _, row := range rows {
			if row.Table == protocol.CopyTableSessions {
				carried[row.Key] = true
			}
		}
		batch.IDs = append(batch.IDs, entry.id)
		batch.Rows = append(batch.Rows, rows...)
	}
	return batch, nil
}

func queryOutbox(db *sql.DB, host string) ([]outboxEntry, error) {
	rows, err := db.Query(`SELECT id, tbl, key FROM outpost_outbox WHERE host = ? ORDER BY id`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []outboxEntry
	for rows.Next() {
		var entry outboxEntry
		if err := rows.Scan(&entry.id, &entry.table, &entry.key); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func currentCopyRows(db *sql.DB, host string, entry outboxEntry, carried map[string]bool) ([]protocol.CopyRow, error) {
	switch entry.table {
	case protocol.CopyTableSessions:
		row, err := copyRow(db, entry.table, entry.key, `SELECT * FROM sessions WHERE id = ? AND host = ?`, entry.key, host)
		return []protocol.CopyRow{row}, err
	case protocol.CopyTableTerminalBindings:
		row, err := copyRow(db, entry.table, entry.key, `SELECT b.* FROM terminal_bindings b JOIN sessions s ON s.id = b.session_id WHERE b.terminal_id = ? AND s.host = ?`, entry.key, host)
		if err != nil || row.Columns == nil {
			return []protocol.CopyRow{row}, err
		}
		session, _ := row.Columns["session_id"].(string)
		if carried[session] {
			return []protocol.CopyRow{row}, nil
		}
		owner, err := copyRow(db, protocol.CopyTableSessions, session, `SELECT * FROM sessions WHERE id = ?`, session)
		return []protocol.CopyRow{owner, row}, err
	}
	return nil, fmt.Errorf("outpost outbox entry %d names unknown table %q", entry.id, entry.table)
}

func copyRow(db *sql.DB, table protocol.CopyTable, key, query string, args ...any) (protocol.CopyRow, error) {
	row := protocol.CopyRow{Table: table, Key: key}
	rows, err := db.Query(query, args...)
	if err != nil {
		return row, err
	}
	defer rows.Close()
	if !rows.Next() {
		return row, rows.Err()
	}
	names, err := rows.Columns()
	if err != nil {
		return row, err
	}
	values := make([]any, len(names))
	targets := make([]any, len(names))
	for i := range values {
		targets[i] = &values[i]
	}
	if err := rows.Scan(targets...); err != nil {
		return row, err
	}
	row.Columns = make(protocol.RecordUnknown, len(names))
	for i, name := range names {
		row.Columns[name] = values[i]
	}
	return row, rows.Err()
}

func (s *Store) AckOutbox(ids []int64) error {
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM outpost_outbox WHERE id IN (SELECT value FROM json_each(?))`, string(encoded))
	return err
}

// ApplyCopy writes a batch of home's copy in one transaction. It is the only writer of rows with a host on
// an outpost, and marks the sessions it writes as copied from home.
func (s *Store) ApplyCopy(home string, rows []protocol.CopyRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sessions, bindings []protocol.CopyRow
	var deleted []string
	for _, row := range rows {
		switch {
		case row.Table == protocol.CopyTableSessions && row.Columns == nil:
			deleted = append(deleted, row.Key)
		case row.Table == protocol.CopyTableSessions:
			row.Columns["copied_from"] = home
			sessions = append(sessions, row)
		case row.Table == protocol.CopyTableTerminalBindings:
			bindings = append(bindings, row)
		default:
			return fmt.Errorf("copy row names unknown table %q", row.Table)
		}
	}
	columns := map[protocol.CopyTable]map[string]bool{}
	for table := range copyKeys {
		if columns[table], err = tableColumns(tx, string(table)); err != nil {
			return err
		}
	}
	for _, row := range append(sessions, bindings...) {
		if row.Columns == nil {
			_, err = tx.Exec(`DELETE FROM terminal_bindings WHERE terminal_id = ?`, row.Key)
		} else {
			err = upsertCopyRow(tx, row, columns[row.Table])
		}
		if err != nil {
			return fmt.Errorf("apply copy of %s %s: %w", row.Table, row.Key, err)
		}
	}
	if err := deleteCopiedSessions(tx, `copied_from = ? AND id IN (SELECT value FROM json_each(?))`, home, jsonList(deleted)); err != nil {
		return err
	}
	pausepoint.At(pausepoint.CopyBatchStaged)
	if err := tx.Commit(); err != nil {
		return err
	}
	s.announceTerminalBindingsLocked()
	return nil
}

// DropCopyNotFrom forgets every row copied here from a daemon other than home, so the terminals they bound
// are pruned. An empty home drops every copy.
func (s *Store) DropCopyNotFrom(home string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteCopiedSessions(tx, `copied_from != '' AND copied_from != ?`, home); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.announceTerminalBindingsLocked()
	return nil
}

func deleteCopiedSessions(tx *sql.Tx, where string, args ...any) error {
	selected := `SELECT id FROM sessions WHERE ` + where
	for _, table := range append([]string{"terminal_bindings"}, sessionOwnedTables...) {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE session_id IN (`+selected+`)`, args...); err != nil {
			return fmt.Errorf("drop copied %s: %w", table, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE `+where, args...); err != nil {
		return fmt.Errorf("drop copied sessions: %w", err)
	}
	return nil
}

func upsertCopyRow(tx *sql.Tx, row protocol.CopyRow, known map[string]bool) error {
	keyColumn := copyKeys[row.Table]
	if key, _ := row.Columns[keyColumn].(string); key != row.Key {
		return fmt.Errorf("column %s is %v, not the row key", keyColumn, row.Columns[keyColumn])
	}
	var names, updates []string
	var values []any
	for name, value := range row.Columns {
		if !known[name] {
			continue
		}
		names = append(names, name)
		values = append(values, value)
		if name != keyColumn {
			updates = append(updates, name+" = excluded."+name)
		}
	}
	query := `INSERT INTO ` + string(row.Table) + ` (` + strings.Join(names, ", ") + `) VALUES (` + strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ") + `) ON CONFLICT(` + keyColumn + `) DO `
	if len(updates) == 0 {
		query += `NOTHING`
	} else {
		query += `UPDATE SET ` + strings.Join(updates, ", ")
	}
	_, err := tx.Exec(query, values...)
	return err
}

func tableColumns(tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	if len(names) == 0 {
		return nil, errors.New("no columns for " + table)
	}
	return names, rows.Err()
}

func jsonList(values []string) string {
	if values == nil {
		return "[]"
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
