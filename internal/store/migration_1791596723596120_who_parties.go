package store

import (
	"database/sql"
	"fmt"
	"strings"
)

func init() {
	registerMigration(migration{version: 1791596723596120, desc: "who parties", apply: applyMigration1791596723596120})
}
func applyMigration1791596723596120(tx *sql.Tx) error {
	members := make(map[string]string)
	live := make(map[string]bool)
	rows, err := tx.Query("SELECT id,member_key,closed_at FROM sessions")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, key, closed string
		if err := rows.Scan(&id, &key, &closed); err != nil {
			rows.Close()
			return err
		}
		if key != "" {
			members[id] = key
		}
		live[id] = closed == ""
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var collection int
	err = tx.QueryRow("SELECT id FROM document_collections WHERE namespace='core/crew' AND collection='members'").Scan(&collection)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		rows, err = tx.Query(fmt.Sprintf("SELECT id,coalesce(json_extract(body,'$.binding_session'),'') FROM doc_%d", collection))
		if err != nil {
			return err
		}
		for rows.Next() {
			var key, id string
			if err := rows.Scan(&key, &id); err != nil {
				rows.Close()
				return err
			}
			if live[id] {
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
		if key := members[id]; key != "" {
			return "member:" + key
		}
		return "session:" + id
	}
	type rewrite struct{ id, old, next string }
	var peers []rewrite
	rows, err = tx.Query("SELECT id,sender FROM peer_messages")
	if err != nil {
		return err
	}
	for rows.Next() {
		var r rewrite
		if err := rows.Scan(&r.id, &r.old); err != nil {
			rows.Close()
			return err
		}
		r.next = party(r.old)
		peers = append(peers, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, r := range peers {
		if _, err := tx.Exec("UPDATE peer_messages SET sender=? WHERE id=?", r.next, r.id); err != nil {
			return err
		}
	}
	type watch struct{ old, next, pr, success string }
	var watches []watch
	rows, err = tx.Query("SELECT watcher,session_id,pr_id,last_success_at FROM pull_request_watches ORDER BY last_success_at DESC,created_at DESC,watcher")
	if err != nil {
		return err
	}
	seen := make(map[[2]string]bool)
	var losers []watch
	for rows.Next() {
		var w watch
		var id string
		if err := rows.Scan(&w.old, &id, &w.pr, &w.success); err != nil {
			rows.Close()
			return err
		}
		w.next = w.old
		if raw, ok := strings.CutPrefix(w.old, "session:"); ok {
			w.next = party(raw)
		} else if strings.HasPrefix(w.old, "chief:") {
			w.next = party(id)
		}
		key := [2]string{w.next, w.pr}
		if seen[key] {
			losers = append(losers, w)
		} else {
			seen[key] = true
			watches = append(watches, w)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, w := range losers {
		if _, err := tx.Exec("DELETE FROM pull_request_watches WHERE watcher=? AND pr_id=?", w.old, w.pr); err != nil {
			return err
		}
	}
	for _, w := range watches {
		if _, err := tx.Exec("UPDATE pull_request_watches SET watcher=? WHERE watcher=? AND pr_id=?", w.next, w.old, w.pr); err != nil {
			return err
		}
	}
	var handbacks []rewrite
	rows, err = tx.Query("SELECT id,session_id,handback_to FROM presentations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var r rewrite
		var session string
		if err := rows.Scan(&r.id, &session, &r.old); err != nil {
			rows.Close()
			return err
		}
		r.next = r.old
		if raw, ok := strings.CutPrefix(r.old, "session:"); ok {
			r.next = party(raw)
		} else if r.old == "" || strings.HasPrefix(r.old, "chief:") {
			r.next = party(session)
		}
		handbacks = append(handbacks, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, r := range handbacks {
		if _, err := tx.Exec("UPDATE presentations SET handback_to=? WHERE id=?", r.next, r.id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE sessions SET closed_by='session:' || closed_by WHERE closed_by NOT IN ('','user'); UPDATE kept_conversations SET deleted_by='attn' WHERE deleted_by='sweep'"); err != nil {
		return err
	}
	type proposal struct {
		id                                   int
		by, next, kind, target, value, state string
	}
	var proposals []proposal
	var duplicateIDs []int
	asks := make(map[[4]string]bool)
	rows, err = tx.Query("SELECT id,proposed_by,kind,target,value,state FROM automode_proposals ORDER BY created_at,id")
	if err != nil {
		return err
	}
	for rows.Next() {
		var p proposal
		if err := rows.Scan(&p.id, &p.by, &p.kind, &p.target, &p.value, &p.state); err != nil {
			rows.Close()
			return err
		}
		p.next = p.by
		if p.by != "" {
			p.next = party(p.by)
		}
		key := [4]string{p.kind, p.target, p.value, p.next}
		if p.state == "pending" && asks[key] {
			duplicateIDs = append(duplicateIDs, p.id)
		} else {
			if p.state == "pending" {
				asks[key] = true
			}
			proposals = append(proposals, p)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range duplicateIDs {
		if _, err := tx.Exec("DELETE FROM automode_proposals WHERE id=?", id); err != nil {
			return err
		}
	}
	for _, p := range proposals {
		if _, err := tx.Exec("UPDATE automode_proposals SET proposed_by=? WHERE id=?", p.next, p.id); err != nil {
			return err
		}
	}
	return nil
}
