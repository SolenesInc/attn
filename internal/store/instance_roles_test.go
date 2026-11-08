package store

import (
	"testing"
)

func TestMigration161MovesTheChiefIntoItsProfile(t *testing.T) {
	s, _ := openProfileStore(t)
	work, _ := mustCreateProfile(t, s, "Work")
	addProfileSession(t, s, "old-chief", work.ID)
	if _, err := s.db.Exec(`
		ALTER TABLE profiles DROP COLUMN chief_session_id;
		ALTER TABLE sessions ADD COLUMN session_cost_json TEXT NOT NULL DEFAULT '';
		CREATE TABLE instance_roles (role TEXT PRIMARY KEY, session_id TEXT NOT NULL);
		INSERT INTO instance_roles (role, session_id) VALUES ('chief_of_staff', 'old-chief');
		DROP TRIGGER launch_review_crew_insert;
		DROP TRIGGER launch_review_crew_update;
		DROP INDEX idx_crew_members_profile;
		DROP INDEX idx_crew_members_name;
		ALTER TABLE crew_members RENAME TO crew_profiles;
		ALTER TABLE crew_profiles RENAME COLUMN member_key TO member_id;
		DELETE FROM schema_migrations WHERE version >= 161;
	`); err != nil {
		t.Fatal(err)
	}
	// Migration 168 rebuilds the inbox from tables it drops, so a replay stops before it.
	if err := migrateDBThrough(s.db, "", 167); err != nil {
		t.Fatal(err)
	}
	chiefs, err := s.ProfileChiefs()
	if err != nil || len(chiefs) != 1 || chiefs[work.ID] != "old-chief" {
		t.Fatalf("chiefs after migration = %v, %v; want old-chief in %s", chiefs, err, work.ID)
	}
}
