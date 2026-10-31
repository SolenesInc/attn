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
	registerMigration(migration{
		version: 1793408289794144,
		desc:    "crew members",
		apply:   applyMigration1793408289794144,
	})
}

func applyMigration1793408289794144(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE crew_profiles RENAME TO crew_members;
 ALTER TABLE crew_members RENAME COLUMN member_id TO member_key;
 ALTER TABLE crew_members ADD COLUMN name TEXT NOT NULL DEFAULT '';
 ALTER TABLE crew_members ADD COLUMN latest_session TEXT NOT NULL DEFAULT '';
 ALTER TABLE sessions ADD COLUMN member_key TEXT NOT NULL DEFAULT '';
 DROP INDEX idx_crew_profiles_profile;
 DROP TRIGGER launch_review_crew_insert;
 DROP TRIGGER launch_review_crew_profile;`); err != nil {
		return err
	}
	if err := nameMigratedCrewMembers(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX idx_crew_members_profile ON crew_members(profile_id);
 CREATE UNIQUE INDEX idx_crew_members_name ON crew_members(profile_id, name COLLATE NOCASE);
 CREATE INDEX idx_sessions_member_key ON sessions(member_key) WHERE member_key != '';
 CREATE TRIGGER launch_review_crew_insert AFTER INSERT ON crew_members
 BEGIN UPDATE profile_migration SET revision = revision + 1 WHERE phase = 'launch_required'; END;
 CREATE TRIGGER launch_review_crew_update AFTER UPDATE ON crew_members WHEN OLD.profile_id != NEW.profile_id OR OLD.name != NEW.name
 BEGIN UPDATE profile_migration SET revision = revision + 1 WHERE phase = 'launch_required'; END;`); err != nil {
		return err
	}
	return recordMigratedMemberSessions(tx)
}

func nameMigratedCrewMembers(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT member_key, profile_id FROM crew_members ORDER BY profile_id, member_key`)
	if err != nil {
		return err
	}
	type member struct{ key, profile, name string }
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.key, &m.profile); err != nil {
			rows.Close()
			return err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	namePattern := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,39}$`)
	idPattern := regexp.MustCompile(`^[A-Za-z]-[A-Za-z0-9]{6}$`)
	preferred := make(map[string]map[string]bool)
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
			log.Printf("crew migration: member %s is named %s", m.key, m.name)
		}
		if preferred[m.profile] == nil {
			preferred[m.profile] = make(map[string]bool)
		}
		preferred[m.profile][strings.ToLower(m.name)] = true
	}
	assigned := make(map[string]map[string]bool)
	for _, m := range members {
		if assigned[m.profile] == nil {
			assigned[m.profile] = make(map[string]bool)
		}
		name := m.name
		if assigned[m.profile][strings.ToLower(name)] {
			for number := 2; ; number++ {
				suffix := "-" + strconv.Itoa(number)
				stem := m.name
				if len(stem)+len(suffix) > 40 {
					stem = stem[:40-len(suffix)]
				}
				name = stem + suffix
				if !preferred[m.profile][strings.ToLower(name)] && !assigned[m.profile][strings.ToLower(name)] {
					break
				}
			}
			log.Printf("crew migration: member %s is named %s; %s was taken in its profile", m.key, name, m.name)
		}
		assigned[m.profile][strings.ToLower(name)] = true
		if _, err := tx.Exec(`UPDATE crew_members SET name = ? WHERE member_key = ?`, name, m.key); err != nil {
			return err
		}
	}
	return nil
}

func recordMigratedMemberSessions(tx *sql.Tx) error {
	var collectionID int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace='core/crew' AND collection='members'`).Scan(&collectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := tx.Query(fmt.Sprintf(`SELECT id, coalesce(json_extract(body, '$.binding_session'), '') FROM doc_%d`, collectionID))
	if err != nil {
		return err
	}
	bindings := make(map[string]string)
	for rows.Next() {
		var key, session string
		if err := rows.Scan(&key, &session); err != nil {
			rows.Close()
			return err
		}
		if session != "" {
			bindings[key] = session
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for key, session := range bindings {
		result, err := tx.Exec(`UPDATE sessions SET member_key = ? WHERE id = ? AND closed_at = '' AND member_key = ''`, key, session)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated == 0 {
			continue
		}
		if _, err := tx.Exec(`UPDATE crew_members SET latest_session = ? WHERE member_key = ?`, session, key); err != nil {
			return err
		}
	}
	return nil
}
