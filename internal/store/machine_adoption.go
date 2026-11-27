package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
)

var machineHostTables = []string{"recent_locations", "file_activity", "markdown_annotation_drafts", "code_nav_setups"}

var machineSettingKeyPrefixes = []string{"new_session_yolo_", "code_review_setup_offer_", "new_session_destination_"}

// AdoptMachineID gives this machine the id its home knows it by: its self row and every stored reference to it move
// from the current id to id in one transaction, written as the copy applier writes so that its own rows may sit beside
// the home's copies. A reference already stored under id wins over one it would replace, and copies stay as the home
// sent them.
func (s *Store) AdoptMachineID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := s.MachineID()
	if id == "" || id == from {
		return nil
	}
	if err := transactWritingCopies(context.Background(), s.db, func(tx *sql.Tx) error {
		return moveMachineReferences(tx, from, id)
	}); err != nil {
		return err
	}
	for _, collection := range []string{garden.CollectionDispatches, "beds"} {
		if _, table, found, err := readCollection(s.db, garden.Namespace, collection); err == nil && found {
			s.writes.documents.reset(table)
		}
	}
	s.machineID.Store(&id)
	if s.writes != nil {
		s.writes.sessions.Add(1)
	}
	return s.reloadSettingsLocked()
}

func moveMachineReferences(tx *sql.Tx, from, to string) error {
	if _, err := tx.Exec(`UPDATE machines SET id = ? WHERE id = ? AND transport = 'self'`, to, from); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET host = ? WHERE host = ? AND copied_from = ''`, to, from); err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	if _, err := tx.Exec(`UPDATE asks SET host = ? WHERE host = ?`, to, from); err != nil {
		return fmt.Errorf("asks: %w", err)
	}
	for _, table := range machineHostTables {
		if _, err := tx.Exec(`UPDATE OR IGNORE `+table+` SET host = ? WHERE host = ?`, to, from); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE host = ?`, from); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	if _, err := tx.Exec(`UPDATE delegation_operations SET request_json = json_set(request_json, '$.resolved_host', ?) WHERE json_valid(request_json) AND json_extract(request_json, '$.resolved_host') = ?`, to, from); err != nil {
		return err
	}
	if err := moveMachineSettingKeys(tx, from, to); err != nil {
		return err
	}
	if err := moveGardenMachines(tx, from, to); err != nil {
		return err
	}
	return moveMarkdownTiles(tx, from, to)
}

func moveMachineSettingKeys(tx *sql.Tx, from, to string) error {
	rows, err := tx.Query(`SELECT key FROM settings WHERE copied_from = ''`)
	if err != nil {
		return err
	}
	moves := map[string]string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		if next, ok := settingKeyOnMachine(key, from, to); ok {
			moves[key] = next
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for key, next := range moves {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO settings (key, value, copied_from) SELECT ?, value, '' FROM settings WHERE key = ? AND copied_from = ''`, next, key); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ? AND copied_from = ''`, key); err != nil {
			return err
		}
	}
	return nil
}

func settingKeyOnMachine(key, from, to string) (string, bool) {
	for _, prefix := range machineSettingKeyPrefixes {
		rest, ok := strings.CutPrefix(key, prefix+from)
		if ok && (rest == "" || strings.HasPrefix(rest, "_")) {
			return prefix + to + rest, true
		}
	}
	return "", false
}

func moveGardenMachines(tx *sql.Tx, from, to string) error {
	if _, dispatches, found, err := readCollection(tx, garden.Namespace, garden.CollectionDispatches); err != nil {
		return err
	} else if found {
		if _, err := tx.Exec(`UPDATE `+dispatches+` SET body = json_set(body, '$.machine', ?), rev = rev + 1 WHERE json_extract(body, '$.machine') = ?`, to, from); err != nil {
			return err
		}
	}
	_, beds, found, err := readCollection(tx, garden.Namespace, "beds")
	if err != nil || !found {
		return err
	}
	_, err = tx.Exec(`UPDATE `+beds+` SET body = json_set(body, '$.repo_paths', (
			SELECT json_group_array(json_set(p.value, '$.machine', CASE WHEN json_extract(p.value, '$.machine') = ? THEN ? ELSE json_extract(p.value, '$.machine') END))
			FROM json_each(body, '$.repo_paths') p
		)), rev = rev + 1
		WHERE EXISTS (SELECT 1 FROM json_each(body, '$.repo_paths') p WHERE json_extract(p.value, '$.machine') = ?)`, from, to, from)
	return err
}

func moveMarkdownTiles(tx *sql.Tx, from, to string) error {
	rows, err := tx.Query(`SELECT id, tree_json, active_pane_id, focus_history FROM desktops WHERE instr(tree_json, ?) > 0`, from)
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
		if !moveMarkdownTilesIn(tree, from, to, renamed) {
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

func moveMarkdownTilesIn(node any, from, to string, renamed map[string]string) bool {
	object, ok := node.(map[string]any)
	if !ok {
		return false
	}
	changed := false
	if children, ok := object["children"].([]any); ok {
		for _, child := range children {
			changed = moveMarkdownTilesIn(child, from, to, renamed) || changed
		}
	}
	params, _ := object["tile_params"].(string)
	if object["type"] != "tile" || object["tile_kind"] != "markdown" {
		return changed
	}
	var located struct {
		Host string `json:"host"`
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(params), &located) != nil || located.Host != from {
		return changed
	}
	located.Host = to
	encoded, err := json.Marshal(located)
	if err != nil {
		return changed
	}
	object["tile_params"] = string(encoded)
	if id, _ := object["tile_id"].(string); id == layouttree.MarkdownTileID(from, located.Path) {
		next := layouttree.MarkdownTileID(to, located.Path)
		object["tile_id"] = next
		renamed[id] = next
	}
	return true
}
