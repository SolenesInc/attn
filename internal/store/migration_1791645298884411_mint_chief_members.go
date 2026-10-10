package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

func init() {
	registerMigration(migration{version: 1791645298884411, desc: "mint chief members", apply: applyMigration1791645298884411})
}

func applyMigration1791645298884411(tx *sql.Tx) error {
	rows, err := tx.Query("SELECT id, chief_session_id FROM profiles WHERE deleted_at = '' ORDER BY id")
	if err != nil {
		return err
	}
	type profile struct{ id, previous string }
	var profiles []profile
	for rows.Next() {
		var p profile
		if err := rows.Scan(&p.id, &p.previous); err != nil {
			rows.Close()
			return err
		}
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range profiles {
		var key string
		for {
			var raw [3]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return err
			}
			key = "m-" + hex.EncodeToString(raw[:])
			var exists bool
			if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM crew_members WHERE member_key = ?)", key).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				break
			}
		}
		if _, err := tx.Exec("INSERT INTO crew_members(member_key,profile_id,name) VALUES(?,?,'Chief')", key, p.id); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE profiles SET chief_member = ? WHERE id = ?", key, p.id); err != nil {
			return err
		}
		if p.previous != "" {
			var agent, directory, intent string
			err := tx.QueryRow("SELECT agent,directory,launch_intent FROM sessions WHERE id = ?", p.previous).Scan(&agent, &directory, &intent)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				var launch struct {
					Model  string `json:"model"`
					Effort string `json:"effort"`
				}
				if intent != "" {
					if err := json.Unmarshal([]byte(intent), &launch); err != nil {
						return err
					}
				}
				model, effort := launch.Model, launch.Effort
				for _, setting := range []struct {
					key   string
					value *string
				}{{"chief_model_" + agent, &model}, {"chief_effort_" + agent, &effort}} {
					var value string
					err := tx.QueryRow("SELECT value FROM settings WHERE key = ?", setting.key).Scan(&value)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return err
					}
					if strings.TrimSpace(value) != "" {
						*setting.value = value
					}
				}
				if _, err := tx.Exec("INSERT INTO chief_upgrades(profile_id,chief_member,previous_session,agent,model,effort,cwd) VALUES(?,?,?,?,?,?,?)", p.id, key, p.previous, agent, model, effort, directory); err != nil {
					return err
				}
				if _, err := tx.Exec("UPDATE sessions SET label = 'Chief (previous)' WHERE id = ?", p.previous); err != nil {
					return err
				}
			}
		}
		for _, table := range []string{"inbox_items", "inbox_delivery"} {
			if _, err := tx.Exec("UPDATE "+table+" SET address = ? WHERE address = ?", "member:"+key, "chief:"+p.id); err != nil {
				return err
			}
		}
	}
	for _, table := range []string{"inbox_items", "inbox_delivery"} {
		if _, err := tx.Exec("DELETE FROM " + table + " WHERE address GLOB 'chief:*' AND NOT EXISTS (SELECT 1 FROM profiles WHERE deleted_at='' AND id=substr(" + table + ".address,length('chief:')+1))"); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DELETE FROM settings WHERE key LIKE 'chief\_model\_%' ESCAPE '\' OR key LIKE 'chief\_effort\_%' ESCAPE '\' OR key = 'chief_context_window_cap'`)
	return err
}
