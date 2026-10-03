package store

import (
	"database/sql"
	"fmt"
	"strings"
)

func migrateAutomationIDs(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TEMP TABLE automation_id_map (old_id TEXT PRIMARY KEY, new_id INTEGER UNIQUE);
 INSERT INTO automation_id_map SELECT id, row_number() OVER (ORDER BY created_at, id) FROM automation_definitions;`); err != nil {
		return err
	}
	tables := []string{"automation_definitions", "automation_occurrences", "automation_runs", "automation_provider_cursors", "automation_review_request_edges", "automation_continuity_bindings"}
	for _, table := range tables {
		var schema string
		if err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&schema); err != nil {
			return err
		}
		indexes, err := queryColumn[string](tx, `SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name=? AND sql IS NOT NULL ORDER BY name`, table)
		if err != nil {
			return err
		}
		columns, err := queryColumn[string](tx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			return err
		}
		temporary := table + "_numeric"
		schema = strings.Replace(schema, table, temporary, 1)
		if table == "automation_definitions" {
			schema = strings.Replace(schema, "id TEXT PRIMARY KEY", "id INTEGER PRIMARY KEY AUTOINCREMENT", 1)
		} else {
			schema = strings.Replace(schema, "definition_id TEXT NOT NULL", "definition_id INTEGER NOT NULL", 1)
		}
		if _, err := tx.Exec(schema); err != nil {
			return err
		}
		selectColumns := make([]string, len(columns))
		for i, column := range columns {
			selectColumns[i] = "source." + column
			if table == "automation_definitions" && column == "id" || table != "automation_definitions" && column == "definition_id" {
				selectColumns[i] = "mapping.new_id"
			}
			if table == "automation_definitions" && column == "spec_json" {
				selectColumns[i] = "json_set(source.spec_json, '$.id', mapping.new_id)"
			}
		}
		identity := "definition_id"
		if table == "automation_definitions" {
			identity = "id"
		}
		copySQL := fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM %s AS source JOIN automation_id_map AS mapping ON mapping.old_id=source.%s`, temporary, strings.Join(columns, ","), strings.Join(selectColumns, ","), table, identity)
		if _, err := tx.Exec(copySQL); err != nil {
			return err
		}
		if _, err := tx.Exec("DROP TABLE " + table); err != nil {
			return err
		}
		if _, err := tx.Exec("ALTER TABLE " + temporary + " RENAME TO " + table); err != nil {
			return err
		}
		for _, index := range indexes {
			if _, err := tx.Exec(index); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(`DROP TABLE automation_id_map`)
	return err
}
