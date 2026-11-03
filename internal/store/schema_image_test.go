package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

type databaseCell struct {
	table, column string
	row           int
}

func TestANewDatabaseHoldsWhatReplayingEveryMigrationMakesUnderItsOwnIdentity(t *testing.T) {
	secondResolutionColumns := map[string]bool{"schema_migrations.applied_at": true}
	replays := []map[databaseCell]string{replayedDatabase(t), replayedDatabase(t)}
	created := []map[databaseCell]string{newDatabase(t), newDatabase(t)}
	for cell, replayed := range replays[0] {
		if secondResolutionColumns[cell.table+"."+cell.column] {
			continue
		}
		first, second := created[0][cell], created[1][cell]
		if replayed == replays[1][cell] {
			if first != replayed || second != replayed {
				t.Errorf("%s.%s row %d: new databases hold %q and %q, a replay holds %q", cell.table, cell.column, cell.row, first, second, replayed)
			}
			continue
		}
		if first == second || first == replayed || second == replayed {
			t.Errorf("%s.%s row %d differs between two replays (%q, %q) but new databases share a value (%q, %q)", cell.table, cell.column, cell.row, replayed, replays[1][cell], first, second)
		}
	}
	for _, database := range created {
		if len(database) != len(replays[0]) {
			t.Errorf("a new database holds %d cells, a replay holds %d", len(database), len(replays[0]))
		}
	}
}

func replayedDatabase(t *testing.T) map[databaseCell]string {
	t.Helper()
	image, err := replaySchemaImage()
	if err != nil {
		t.Fatal(err)
	}
	var cells map[databaseCell]string
	if err := withImage(image, func(db *sql.DB) error {
		cells, err = databaseCells(db)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return cells
}

func newDatabase(t *testing.T) map[databaseCell]string {
	t.Helper()
	db, err := OpenDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cells, err := databaseCells(db)
	if err != nil {
		t.Fatal(err)
	}
	return cells
}

func databaseCells(db *sql.DB) (map[databaseCell]string, error) {
	cells := map[databaseCell]string{}
	objects, err := db.Query(`SELECT type, name, coalesce(sql, '') FROM sqlite_master`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for objects.Next() {
		var kind, name, definition string
		if err := objects.Scan(&kind, &name, &definition); err != nil {
			objects.Close()
			return nil, err
		}
		cells[databaseCell{table: "sqlite_master", column: kind + " " + name}] = definition
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	objects.Close()
	for _, table := range tables {
		rows, err := db.Query(fmt.Sprintf(`SELECT * FROM %q ORDER BY 1`, table))
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		for row := 0; rows.Next(); row++ {
			values := make([]sql.NullString, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				return nil, err
			}
			for i, column := range columns {
				cells[databaseCell{table: table, column: column, row: row}] = fmt.Sprintf("%v", values[i])
			}
		}
		rows.Close()
	}
	return cells, nil
}
