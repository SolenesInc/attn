package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	registerMigration(migration{version: 1791638944215407, desc: "garden parties", apply: applyMigration1791638944215407})
}
func applyMigration1791638944215407(tx *sql.Tx) error {
	tables := map[string]string{}
	rows, err := tx.Query(`SELECT collection,id FROM document_collections WHERE namespace='core/garden' OR (namespace='core/crew' AND collection='members')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		var id int
		if err := rows.Scan(&name, &id); err != nil {
			rows.Close()
			return err
		}
		tables[name] = fmt.Sprintf("doc_%d", id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	keys := map[string]string{}
	rows, err = tx.Query(`SELECT member_key FROM crew_members`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys[strings.ToLower(key)] = key
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	members := map[string]string{}
	rows, err = tx.Query(`SELECT id,member_key FROM sessions WHERE member_key!=''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return err
		}
		members[id] = key
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if table := tables["members"]; table != "" {
		rows, err = tx.Query(`SELECT id,coalesce(json_extract(body,'$.binding_session'),'') FROM ` + table)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key, id string
			if err := rows.Scan(&key, &id); err != nil {
				rows.Close()
				return err
			}
			keys[strings.ToLower(key)] = key
			if id != "" {
				members[id] = key
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	party := func(id string) string {
		if id == "" {
			return ""
		}
		if key := members[id]; key != "" {
			return "member:" + key
		}
		return "session:" + id
	}
	actor := func(m, id string) string {
		if key := keys[strings.ToLower(m)]; key != "" {
			return "member:" + key
		}
		if m == "attn" {
			return "attn"
		}
		if id != "" {
			return party(id)
		}
		return "user"
	}
	tender := func(m, id string) string {
		if key := keys[strings.ToLower(m)]; key != "" {
			return "member:" + key
		}
		if m != "" {
			return ""
		}
		return party(id)
	}
	type document struct {
		id      string
		body    []byte
		updated string
	}
	readDocuments := func(table string) ([]document, error) {
		rows, err := tx.Query(`SELECT id,body,updated_at FROM ` + table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var docs []document
		for rows.Next() {
			var d document
			if err := rows.Scan(&d.id, &d.body, &d.updated); err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
		return docs, rows.Err()
	}
	for _, collection := range []string{"seeds", "notes", "dispatches"} {
		table := tables[collection]
		if table == "" {
			continue
		}
		docs, err := readDocuments(table)
		if err != nil {
			return err
		}
		for _, doc := range docs {
			var body map[string]any
			if err := json.Unmarshal(doc.body, &body); err != nil {
				return err
			}
			text := func(field string) string { v, _ := body[field].(string); return v }
			switch collection {
			case "seeds":
				planter := actor(text("planter_member"), text("planter_session"))
				if planter == "user" && text("planter_session") == "" && text("planter_member") == "" {
					var automation bool
					if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM automation_runs WHERE seed_id=?)`, doc.id).Scan(&automation); err != nil {
						return err
					}
					if automation {
						planter = "attn"
					}
				}
				body["planter"] = planter
				m, id := text("tender_member"), text("tender_session")
				next := tender(m, id)
				if next != "" {
					body["tender"] = next
				}
				if m != "" && keys[strings.ToLower(m)] == "" {
					notes := tables["notes"]
					if notes == "" {
						return fmt.Errorf("drop named claim on %s: notes collection is missing", doc.id)
					}
					bytes := make([]byte, 6)
					alphabet := "0123456789abcdefghjkmnpqrstvwxyz"
					for {
						if _, err := rand.Read(bytes); err != nil {
							return err
						}
						for i := range bytes {
							bytes[i] = alphabet[int(bytes[i])%len(alphabet)]
						}
						var exists bool
						if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+notes+` WHERE id=?)`, "n-"+string(bytes)).Scan(&exists); err != nil {
							return err
						}
						if !exists {
							break
						}
					}
					note := map[string]any{"id": "n-" + string(bytes), "seed": doc.id, "kind": "note", "author": "attn", "body": fmt.Sprintf("The named claim by %s was removed. Assign this seed to a crew member with attn seed tend %s --for <name>.", m, doc.id)}
					encoded, err := json.Marshal(note)
					if err != nil {
						return err
					}
					if _, err := tx.Exec(`INSERT INTO `+notes+` (id,body,rev,created_at,updated_at) VALUES (?,?,1,?,?)`, note["id"], encoded, doc.updated, doc.updated); err != nil {
						return err
					}
				}
				if condition, ok := body["harvest_when"].(map[string]any); ok {
					m, _ := condition["set_by_member"].(string)
					id, _ := condition["set_by_session"].(string)
					condition["set_by"] = actor(m, id)
					delete(condition, "set_by_member")
					delete(condition, "set_by_session")
				}
				for _, field := range []string{"planter_member", "planter_session", "tender_member", "tender_session"} {
					delete(body, field)
				}
			case "notes":
				if _, already := body["author"]; already {
					continue
				}
				body["author"] = actor(text("author_member"), text("author_session"))
				delete(body, "author_member")
				delete(body, "author_session")
			case "dispatches":
				if m, id := text("dispatcher_member"), text("dispatcher_session"); m != "" || id != "" {
					body["dispatcher"] = actor(m, id)
				}
				delete(body, "dispatcher_member")
				delete(body, "dispatcher_session")
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE `+table+` SET body=? WHERE id=?`, encoded, doc.id); err != nil {
				return err
			}
		}
	}
	type watch struct{ old, next, seed, created string }
	rows, err = tx.Query(`SELECT watcher,seed_id,created_at FROM garden_seed_watches ORDER BY created_at,watcher`)
	if err != nil {
		return err
	}
	var watches []watch
	seen := map[[2]string]bool{}
	for rows.Next() {
		var w watch
		if err := rows.Scan(&w.old, &w.seed, &w.created); err != nil {
			rows.Close()
			return err
		}
		w.next = party(w.old)
		key := [2]string{w.next, w.seed}
		if !seen[key] {
			seen[key] = true
			watches = append(watches, w)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM garden_seed_watches`); err != nil {
		return err
	}
	for _, w := range watches {
		if _, err := tx.Exec(`INSERT INTO garden_seed_watches(watcher,seed_id,created_at) VALUES (?,?,?)`, w.next, w.seed, w.created); err != nil {
			return err
		}
	}
	type operation struct{ id, request, m, s string }
	rows, err = tx.Query(`SELECT request_id,request_json,handover_tender_member,handover_tender_session FROM delegation_operations`)
	if err != nil {
		return err
	}
	var ops []operation
	for rows.Next() {
		var o operation
		if err := rows.Scan(&o.id, &o.request, &o.m, &o.s); err != nil {
			rows.Close()
			return err
		}
		ops = append(ops, o)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, o := range ops {
		var request map[string]any
		if err := json.Unmarshal([]byte(o.request), &request); err != nil {
			return err
		}
		source, _ := request["source_session_id"].(string)
		if _, err := tx.Exec(`UPDATE delegation_operations SET dispatcher=?,handover_tender=? WHERE request_id=?`, actor("", source), tender(o.m, o.s), o.id); err != nil {
			return err
		}
	}
	type event struct {
		seq     int64
		payload string
	}
	rows, err = tx.Query(`SELECT seq,payload FROM bus_events WHERE name LIKE 'garden.seed.%'`)
	if err != nil {
		return err
	}
	var events []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.seq, &e.payload); err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, e := range events {
		var body map[string]any
		if err := json.Unmarshal([]byte(e.payload), &body); err != nil {
			return err
		}
		id, _ := body["caused_by_session_id"].(string)
		body["caused_by"] = actor("", id)
		delete(body, "caused_by_session_id")
		if id, _ := body["directly_notified_session_id"].(string); id != "" {
			body["directly_notified"] = party(id)
		}
		delete(body, "directly_notified_session_id")
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE bus_events SET payload=? WHERE seq=?`, string(encoded), e.seq); err != nil {
			return err
		}
	}
	return nil
}
