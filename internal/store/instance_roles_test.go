package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/profiles"
)

func TestEachProfileHasItsOwnChief(t *testing.T) {
	s, _ := openProfileStore(t)
	home, _ := mustCreateProfile(t, s, "Home")
	work, _ := mustCreateProfile(t, s, "Work")
	addProfileSession(t, s, "home-chief", home.ID)
	addProfileSession(t, s, "work-chief", work.ID)
	addProfileSession(t, s, "work-successor", work.ID)

	if _, _, err := s.SetProfileChief("home-chief"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetProfileChief("work-chief"); err != nil {
		t.Fatal(err)
	}
	chiefs, err := s.ProfileChiefs()
	if err != nil || chiefs[home.ID] != "home-chief" || chiefs[work.ID] != "work-chief" {
		t.Fatalf("chiefs = %v, %v; want one per profile", chiefs, err)
	}

	profile, previous, err := s.SetProfileChief("work-successor")
	if err != nil || previous != "work-chief" || profile.ChiefSessionID != "work-successor" {
		t.Fatalf("transfer = %+v, previous %q, %v", profile, previous, err)
	}
	if claimed, err := s.ClaimProfileChief(work.ID, "late-launch"); err != nil || claimed {
		t.Fatalf("claiming a profile that has a chief = %v, %v; want refused", claimed, err)
	}
	if cleared, err := s.ClearProfileChief("work-chief"); err != nil || cleared != "" {
		t.Fatalf("clearing a former chief touched %q, %v", cleared, err)
	}
	if cleared, err := s.ClearProfileChief("work-successor"); err != nil || cleared != work.ID {
		t.Fatalf("clearing the chief = %q, %v; want %s", cleared, err, work.ID)
	}
	if claimed, err := s.ClaimProfileChief(work.ID, "late-launch"); err != nil || !claimed {
		t.Fatalf("claiming a chiefless profile = %v, %v", claimed, err)
	}

	addProfileSession(t, s, "closed", work.ID)
	if _, err := s.CloseSession("closed", SessionClose{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.SetProfileChief("closed")
	wantCode(t, err, profiles.CodeNotFound)
}

func TestMigration155MovesTheChiefIntoItsProfile(t *testing.T) {
	s, _ := openProfileStore(t)
	work, _ := mustCreateProfile(t, s, "Work")
	addProfileSession(t, s, "old-chief", work.ID)
	if _, err := s.db.Exec(`
		ALTER TABLE profiles DROP COLUMN chief_session_id;
		CREATE TABLE instance_roles (role TEXT PRIMARY KEY, session_id TEXT NOT NULL);
		INSERT INTO instance_roles (role, session_id) VALUES ('chief_of_staff', 'old-chief');
		DELETE FROM schema_migrations WHERE version >= 155;
	`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(s.db, ""); err != nil {
		t.Fatal(err)
	}
	chiefs, err := s.ProfileChiefs()
	if err != nil || len(chiefs) != 1 || chiefs[work.ID] != "old-chief" {
		t.Fatalf("chiefs after migration = %v, %v; want old-chief in %s", chiefs, err, work.ID)
	}
}
