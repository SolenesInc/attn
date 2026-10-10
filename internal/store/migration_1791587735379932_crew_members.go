package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	registerMigration(migration{version: 1791587735379932, desc: "crew members", apply: applyMigration1791587735379932})
}
func applyMigration1791587735379932(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE crew_profiles RENAME TO crew_members;
 ALTER TABLE crew_members RENAME COLUMN member_id TO member_key;
 ALTER TABLE crew_members ADD COLUMN name TEXT NOT NULL DEFAULT '';
 ALTER TABLE crew_members ADD COLUMN latest_session TEXT NOT NULL DEFAULT '';
 DROP INDEX idx_crew_profiles_profile;
 DROP TRIGGER launch_review_crew_insert;
 DROP TRIGGER launch_review_crew_profile;`); err != nil {
		return err
	}
	rows, err := tx.Query("SELECT member_key,profile_id FROM crew_members ORDER BY profile_id,member_key")
	if err != nil {
		return err
	}
	type member struct {
		key, profile, name string
	}
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.key, &m.profile); err != nil {
			rows.Close()
			return err
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	namePattern := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,39}$`)
	idPattern := regexp.MustCompile(`^[A-Za-z]-[A-Za-z0-9]{6}$`)
	reservedNames := make(map[string]map[string]bool)
	for i := range members {
		m := &members[i]
		m.name = strings.ToUpper(m.key[:1]) + m.key[1:]
		reserved := false
		switch strings.ToLower(m.name) {
		case "chief", "attn", "user", "you":
			reserved = true
		}
		if reserved || !namePattern.MatchString(m.name) || idPattern.MatchString(m.name) {
			m.name = m.key + "-crew"
			log.Printf("crew migration: member %s named %s", m.key, m.name)
		}
		if reservedNames[m.profile] == nil {
			reservedNames[m.profile] = make(map[string]bool)
		}
		reservedNames[m.profile][strings.ToLower(m.name)] = true
	}
	assignedNames := make(map[string]map[string]bool)
	for _, m := range members {
		if assignedNames[m.profile] == nil {
			assignedNames[m.profile] = make(map[string]bool)
		}
		name := m.name
		if assignedNames[m.profile][strings.ToLower(name)] {
			for number := 2; ; number++ {
				suffix := "-" + strconv.Itoa(number)
				stem := m.name
				if len(stem)+len(suffix) > 40 {
					stem = stem[:40-len(suffix)]
				}
				name = stem + suffix
				if !reservedNames[m.profile][strings.ToLower(name)] {
					break
				}
			}
			log.Printf("crew migration: member %s named %s after a name collision", m.key, name)
		}
		assignedNames[m.profile][strings.ToLower(name)] = true
		reservedNames[m.profile][strings.ToLower(name)] = true
		if _, err := tx.Exec("UPDATE crew_members SET name = ? WHERE member_key = ?", name, m.key); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE INDEX idx_crew_members_profile ON crew_members(profile_id);
 CREATE UNIQUE INDEX idx_crew_members_name ON crew_members(profile_id,name COLLATE NOCASE);
 CREATE TRIGGER launch_review_crew_insert AFTER INSERT ON crew_members
 BEGIN UPDATE profile_migration SET revision=revision+1 WHERE phase='launch_required'; END;
 CREATE TRIGGER launch_review_crew_update AFTER UPDATE ON crew_members WHEN OLD.profile_id!=NEW.profile_id OR OLD.name!=NEW.name
 BEGIN UPDATE profile_migration SET revision=revision+1 WHERE phase='launch_required'; END;`); err != nil {
		return err
	}
	var collectionID int64
	err = tx.QueryRow("SELECT id FROM document_collections WHERE namespace='core/crew' AND collection='members'").Scan(&collectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err = tx.Query(fmt.Sprintf("SELECT id,json_extract(body,'$.binding_session') FROM doc_%d", collectionID))
	if err != nil {
		return err
	}
	type binding struct {
		key     string
		session sql.NullString
	}
	var bindings []binding
	for rows.Next() {
		var b binding
		if err := rows.Scan(&b.key, &b.session); err != nil {
			rows.Close()
			return err
		}
		bindings = append(bindings, b)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, b := range bindings {
		if !b.session.Valid || b.session.String == "" {
			continue
		}
		result, err := tx.Exec("UPDATE sessions SET member_key = ? WHERE id = ? AND closed_at = ''", b.key, b.session.String)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if _, err := tx.Exec("UPDATE crew_members SET latest_session = ? WHERE member_key = ?", b.session.String, b.key); err != nil {
			return err
		}
	}
	return nil
}
