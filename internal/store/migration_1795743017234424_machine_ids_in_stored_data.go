package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func init() {
	registerMigration(migration{
		version: 1795743017234424,
		desc:    "machine ids in stored data",
		apply:   applyMigration1795743017234424,
	})
}

func applyMigration1795743017234424(tx *sql.Tx) error {
	self := uuid.NewString()
	for _, statement := range []string{
		`CREATE TABLE machines ( id TEXT PRIMARY KEY, name TEXT NOT NULL, transport TEXT NOT NULL CHECK (transport IN ('self','ssh','reverse')), destination TEXT NOT NULL DEFAULT '', host_key TEXT, daemon_id TEXT UNIQUE, added_at TEXT NOT NULL, paired_at TEXT, home TEXT )`,
		`CREATE UNIQUE INDEX machines_one_self ON machines(transport) WHERE transport = 'self'`,
		`CREATE UNIQUE INDEX machines_destination ON machines(destination) WHERE transport != 'self'`,
		`INSERT INTO machines (id, name, transport, destination, host_key, daemon_id, added_at, paired_at, home) SELECT id, name, 'ssh', destination, host_key, daemon_id, added_at, paired_at, home FROM outposts`,
		`DROP TRIGGER outpost_copy_binding_delete`,
		`DROP TRIGGER outpost_copy_binding_insert`,
		`DROP TRIGGER outpost_copy_binding_update`,
		`DROP TRIGGER outpost_copy_session_delete`,
		`DROP TRIGGER outpost_copy_session_insert`,
		`DROP TRIGGER outpost_copy_session_update`,
		`DROP TRIGGER outpost_copy_setting_delete`,
		`DROP TRIGGER outpost_copy_setting_insert`,
		`DROP TRIGGER outpost_copy_setting_update`,
		`DROP TABLE outposts`,
		`CREATE TRIGGER outpost_copy_binding_delete AFTER DELETE ON terminal_bindings WHEN EXISTS (SELECT 1 FROM sessions s JOIN machines m ON m.id = s.host WHERE s.id = OLD.session_id AND m.transport != 'self') BEGIN DELETE FROM outpost_outbox WHERE tbl = 'terminal_bindings' AND key = OLD.terminal_id; INSERT INTO outpost_outbox (host, tbl, key) SELECT host, 'terminal_bindings', OLD.terminal_id FROM sessions WHERE id = OLD.session_id; END`,
		`CREATE TRIGGER outpost_copy_binding_insert AFTER INSERT ON terminal_bindings WHEN EXISTS (SELECT 1 FROM sessions s JOIN machines m ON m.id = s.host WHERE s.id = NEW.session_id AND m.transport != 'self') BEGIN DELETE FROM outpost_outbox WHERE tbl = 'terminal_bindings' AND key = NEW.terminal_id; INSERT INTO outpost_outbox (host, tbl, key) SELECT host, 'terminal_bindings', NEW.terminal_id FROM sessions WHERE id = NEW.session_id; END`,
		`CREATE TRIGGER outpost_copy_binding_update AFTER UPDATE ON terminal_bindings WHEN EXISTS (SELECT 1 FROM sessions s JOIN machines m ON m.id = s.host WHERE s.id IN (OLD.session_id, NEW.session_id) AND m.transport != 'self') BEGIN DELETE FROM outpost_outbox WHERE tbl = 'terminal_bindings' AND key = NEW.terminal_id; INSERT INTO outpost_outbox (host, tbl, key) SELECT s.host, 'terminal_bindings', NEW.terminal_id FROM sessions s JOIN machines m ON m.id = s.host WHERE s.id IN (OLD.session_id, NEW.session_id) AND m.transport != 'self' LIMIT 1; END`,
		`CREATE TRIGGER outpost_copy_session_delete AFTER DELETE ON sessions WHEN EXISTS (SELECT 1 FROM machines WHERE id = OLD.host AND transport != 'self') BEGIN DELETE FROM outpost_outbox WHERE tbl = 'sessions' AND key = OLD.id; INSERT INTO outpost_outbox (host, tbl, key) VALUES (OLD.host, 'sessions', OLD.id); END`,
		`CREATE TRIGGER outpost_copy_session_insert AFTER INSERT ON sessions WHEN EXISTS (SELECT 1 FROM machines WHERE id = NEW.host AND transport != 'self') BEGIN DELETE FROM outpost_outbox WHERE tbl = 'sessions' AND key = NEW.id; INSERT INTO outpost_outbox (host, tbl, key) VALUES (NEW.host, 'sessions', NEW.id); END`,
		`CREATE TRIGGER outpost_copy_session_update AFTER UPDATE ON sessions WHEN EXISTS (SELECT 1 FROM machines WHERE id = NEW.host AND transport != 'self') BEGIN DELETE FROM outpost_outbox WHERE tbl = 'sessions' AND key = NEW.id; INSERT INTO outpost_outbox (host, tbl, key) VALUES (NEW.host, 'sessions', NEW.id); END`,
		`CREATE TRIGGER outpost_copy_setting_delete AFTER DELETE ON settings WHEN OLD.copied_from = '' AND attn_home_setting(OLD.key) BEGIN DELETE FROM outpost_outbox WHERE tbl = 'settings' AND key = OLD.key; INSERT INTO outpost_outbox(host, tbl, key) SELECT id, 'settings', OLD.key FROM machines WHERE transport != 'self'; END`,
		`CREATE TRIGGER outpost_copy_setting_insert AFTER INSERT ON settings WHEN NEW.copied_from = '' AND attn_home_setting(NEW.key) BEGIN DELETE FROM outpost_outbox WHERE tbl = 'settings' AND key = NEW.key; INSERT INTO outpost_outbox(host, tbl, key) SELECT id, 'settings', NEW.key FROM machines WHERE transport != 'self'; END`,
		`CREATE TRIGGER outpost_copy_setting_update AFTER UPDATE ON settings WHEN NEW.copied_from = '' AND attn_home_setting(NEW.key) BEGIN DELETE FROM outpost_outbox WHERE tbl = 'settings' AND key = NEW.key; INSERT INTO outpost_outbox(host, tbl, key) SELECT id, 'settings', NEW.key FROM machines WHERE transport != 'self'; END`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("%s: %w", statement, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO machines (id, name, transport, added_at) VALUES (?, '', 'self', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`, self); err != nil {
		return err
	}
	for _, table := range []string{"sessions", "recent_locations", "file_activity", "markdown_annotation_drafts", "code_nav_setups"} {
		if _, err := tx.Exec(`UPDATE `+table+` SET host = ? WHERE host = ''`, self); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		for _, op := range []string{"INSERT", "UPDATE"} {
			if _, err := tx.Exec(`CREATE TRIGGER machine_required_` + table + `_` + strings.ToLower(op) + ` BEFORE ` + op + ` ON ` + table + ` WHEN NEW.host = '' BEGIN SELECT RAISE(ABORT, '` + table + `.host must name a machine'); END`); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE delegation_operations SET request_json = json_set(request_json, '$.resolved_host', ?) WHERE json_valid(request_json) AND json_extract(request_json, '$.resolved_host') = ''`, self); err != nil {
		return err
	}
	if err := migrateMachineSettingKeys1795743017234424(tx, self); err != nil {
		return err
	}
	if err := migrateGardenMachines1795743017234424(tx, self); err != nil {
		return err
	}
	return migrateMarkdownTiles1795743017234424(tx, self)
}

var machineSettingPrefixes1795743017234424 = []string{"code_review_setup_offer_local_", "new_session_destination_local_"}

func migrateMachineSettingKeys1795743017234424(tx *sql.Tx, self string) error {
	where := `(key = 'new_session_yolo_local' OR substr(key, 1, 30) = 'code_review_setup_offer_local_' OR substr(key, 1, 30) = 'new_session_destination_local_')`
	if _, err := tx.Exec(`DELETE FROM settings WHERE copied_from != '' AND ` + where); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT key, value FROM settings WHERE copied_from = '' AND ` + where)
	if err != nil {
		return err
	}
	type entry struct {
		key   string
		value sql.NullString
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.key, &e.value); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range entries {
		next := "new_session_yolo_" + self
		for _, prefix := range machineSettingPrefixes1795743017234424 {
			if rest, ok := strings.CutPrefix(e.key, prefix); ok {
				next = strings.TrimSuffix(prefix, "local_") + self + "_" + rest
			}
		}
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ? AND copied_from = ''`, e.key); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO settings (key, value, copied_from) VALUES (?, ?, '')`, next, e.value); err != nil {
			return err
		}
	}
	return nil
}

func migrateGardenMachines1795743017234424(tx *sql.Tx, self string) error {
	if _, dispatches, found, err := readCollection(tx, "core/garden", "dispatches"); err != nil {
		return err
	} else if found {
		if _, err := tx.Exec(`UPDATE `+dispatches+` SET body = json_set(body, '$.machine', ?), rev = rev + 1 WHERE json_extract(body, '$.machine') = ''`, self); err != nil {
			return err
		}
	}
	_, beds, found, err := readCollection(tx, "core/garden", "beds")
	if err != nil || !found {
		return err
	}
	_, err = tx.Exec(`UPDATE `+beds+` SET body = json_set(body, '$.repo_paths', (
			SELECT json_group_array(json_set(p.value, '$.machine', CASE WHEN json_extract(p.value, '$.machine') = '' THEN ? ELSE json_extract(p.value, '$.machine') END))
			FROM json_each(body, '$.repo_paths') p
		)), rev = rev + 1
		WHERE EXISTS (SELECT 1 FROM json_each(body, '$.repo_paths') p WHERE json_extract(p.value, '$.machine') = '')`, self)
	return err
}

func markdownTileID1795743017234424(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "tile-markdown-" + hex.EncodeToString(sum[:8])
}

func migrateMarkdownTiles1795743017234424(tx *sql.Tx, self string) error {
	rows, err := tx.Query(`SELECT id, tree_json, active_pane_id, focus_history FROM desktops WHERE tree_json LIKE '%"markdown"%'`)
	if err != nil {
		return err
	}
	type desktop struct{ id, tree, active, focus string }
	var desktops []desktop
	for rows.Next() {
		var d desktop
		if err := rows.Scan(&d.id, &d.tree, &d.active, &d.focus); err != nil {
			rows.Close()
			return err
		}
		desktops = append(desktops, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range desktops {
		decoder := json.NewDecoder(strings.NewReader(d.tree))
		decoder.UseNumber()
		var tree any
		if err := decoder.Decode(&tree); err != nil {
			return fmt.Errorf("desktop %s tree: %w", d.id, err)
		}
		renamed := map[string]string{}
		changed := placeMarkdownTiles1795743017234424(tree, self, renamed)
		if !changed {
			continue
		}
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(tree); err != nil {
			return err
		}
		active := d.active
		if next, ok := renamed[active]; ok {
			active = next
		}
		focus := d.focus
		var history []string
		if json.Unmarshal([]byte(d.focus), &history) == nil {
			for i, leaf := range history {
				if next, ok := renamed[leaf]; ok {
					history[i] = next
				}
			}
			next, err := json.Marshal(history)
			if err != nil {
				return err
			}
			focus = string(next)
		}
		if _, err := tx.Exec(`UPDATE desktops SET tree_json = ?, active_pane_id = ?, focus_history = ? WHERE id = ?`, strings.TrimSuffix(encoded.String(), "\n"), active, focus, d.id); err != nil {
			return err
		}
	}
	return nil
}

func placeMarkdownTiles1795743017234424(node any, self string, renamed map[string]string) bool {
	object, ok := node.(map[string]any)
	if !ok {
		return false
	}
	changed := false
	if children, ok := object["children"].([]any); ok {
		for _, child := range children {
			changed = placeMarkdownTiles1795743017234424(child, self, renamed) || changed
		}
	}
	params, _ := object["tile_params"].(string)
	params = strings.TrimSpace(params)
	if object["type"] != "tile" || object["tile_kind"] != "markdown" || params == "" || strings.HasPrefix(params, "{") {
		return changed
	}
	located, err := json.Marshal(struct {
		Host string `json:"host"`
		Path string `json:"path"`
	}{self, params})
	if err != nil {
		return changed
	}
	object["tile_params"] = string(located)
	if id, _ := object["tile_id"].(string); id == markdownTileID1795743017234424(params) {
		next := markdownTileID1795743017234424(self + "\x00" + params)
		object["tile_id"] = next
		renamed[id] = next
	}
	return true
}
