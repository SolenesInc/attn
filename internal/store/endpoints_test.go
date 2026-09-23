package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStoreEndpointCRUD(t *testing.T) {
	s := New()

	record, err := s.AddEndpoint("gpu-box", "user@example", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}
	if record.ID == "" {
		t.Fatal("AddEndpoint() returned empty ID")
	}
	if !record.Enabled {
		t.Fatal("AddEndpoint() should default enabled=true")
	}
	if record.Instance != "" {
		t.Fatalf("AddEndpoint() Instance = %q, want empty", record.Instance)
	}

	got := s.GetEndpoint(record.ID)
	if got == nil {
		t.Fatal("GetEndpoint() returned nil")
	}
	if got.Name != "gpu-box" {
		t.Fatalf("GetEndpoint().Name = %q, want gpu-box", got.Name)
	}
	if got.SSHTarget != "user@example" {
		t.Fatalf("GetEndpoint().SSHTarget = %q, want user@example", got.SSHTarget)
	}

	name := "gpu-box-2"
	target := "dev@example"
	enabled := false
	instance := "dev"
	updated, err := s.UpdateEndpoint(record.ID, EndpointUpdate{
		Name:      &name,
		SSHTarget: &target,
		Enabled:   &enabled,
		Instance:  &instance,
	})
	if err != nil {
		t.Fatalf("UpdateEndpoint() error = %v", err)
	}
	if updated.Name != name {
		t.Fatalf("UpdateEndpoint().Name = %q, want %q", updated.Name, name)
	}
	if updated.SSHTarget != target {
		t.Fatalf("UpdateEndpoint().SSHTarget = %q, want %q", updated.SSHTarget, target)
	}
	if updated.Enabled {
		t.Fatal("UpdateEndpoint().Enabled = true, want false")
	}
	if updated.Instance != instance {
		t.Fatalf("UpdateEndpoint().Instance = %q, want %q", updated.Instance, instance)
	}

	list := s.ListEndpoints()
	if len(list) != 1 {
		t.Fatalf("ListEndpoints() len = %d, want 1", len(list))
	}
	if list[0].ID != record.ID {
		t.Fatalf("ListEndpoints()[0].ID = %q, want %q", list[0].ID, record.ID)
	}
	if list[0].Instance != instance {
		t.Fatalf("ListEndpoints()[0].Instance = %q, want %q", list[0].Instance, instance)
	}

	if err := s.RemoveEndpoint(record.ID); err != nil {
		t.Fatalf("RemoveEndpoint() error = %v", err)
	}
	if got := s.GetEndpoint(record.ID); got != nil {
		t.Fatalf("GetEndpoint() after remove = %+v, want nil", got)
	}
}

func TestAddEndpointWithInstance(t *testing.T) {
	s := New()

	record, err := s.AddEndpoint("gpu-box", "user@example", "dev")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}
	if record.Instance != "dev" {
		t.Fatalf("AddEndpoint() Instance = %q, want dev", record.Instance)
	}

	got := s.GetEndpoint(record.ID)
	if got == nil || got.Instance != "dev" {
		t.Fatalf("GetEndpoint() Instance = %q, want dev", got.Instance)
	}
}

func TestAddEndpointNormalizesInstanceCase(t *testing.T) {
	s := New()

	record, err := s.AddEndpoint("gpu-box", "user@example", "DEV")
	if err != nil {
		t.Fatalf("AddEndpoint(\"DEV\") error = %v", err)
	}
	if record.Instance != "dev" {
		t.Fatalf("AddEndpoint(\"DEV\") Instance = %q, want %q (must be lowercased so $ATTN_INSTANCE on the remote — which is already lowercased by config.Instance() — produces the same data dir as the install path the hub builds locally)", record.Instance, "dev")
	}
}

func TestAddEndpointMapsDefaultInstanceToEmpty(t *testing.T) {
	s := New()
	for _, input := range []string{"default", "DEFAULT", "  default  "} {
		t.Run(input, func(t *testing.T) {
			record, err := s.AddEndpoint("gpu-box", "user@example", input)
			if err != nil {
				t.Fatalf("AddEndpoint(%q) error = %v", input, err)
			}
			if record.Instance != "" {
				t.Fatalf("AddEndpoint(%q) Instance = %q, want \"\" (literal \"default\" must canonicalize to empty)", input, record.Instance)
			}
			_ = s.RemoveEndpoint(record.ID)
		})
	}
}

func TestUpdateEndpointClearsInstanceWithEmptyString(t *testing.T) {
	s := New()
	record, err := s.AddEndpoint("gpu-box", "user@example", "dev")
	if err != nil {
		t.Fatalf("AddEndpoint(): %v", err)
	}
	if record.Instance != "dev" {
		t.Fatalf("setup: instance = %q, want dev", record.Instance)
	}

	empty := ""
	updated, err := s.UpdateEndpoint(record.ID, EndpointUpdate{Instance: &empty})
	if err != nil {
		t.Fatalf("UpdateEndpoint(instance=\"\") error = %v", err)
	}
	if updated.Instance != "" {
		t.Fatalf("UpdateEndpoint(instance=\"\") Instance = %q, want empty (a non-nil empty pointer must clear the instance back to default)", updated.Instance)
	}

	got := s.GetEndpoint(record.ID)
	if got == nil || got.Instance != "" {
		t.Fatalf("GetEndpoint() Instance = %q, want empty", got.Instance)
	}
}

func TestUpdateEndpointNormalizesInstanceCase(t *testing.T) {
	s := New()
	record, err := s.AddEndpoint("gpu-box", "user@example", "")
	if err != nil {
		t.Fatalf("AddEndpoint(): %v", err)
	}

	upper := "DEV"
	updated, err := s.UpdateEndpoint(record.ID, EndpointUpdate{Instance: &upper})
	if err != nil {
		t.Fatalf("UpdateEndpoint(instance=\"DEV\") error = %v", err)
	}
	if updated.Instance != "dev" {
		t.Fatalf("UpdateEndpoint(instance=\"DEV\") Instance = %q, want \"dev\"", updated.Instance)
	}
}

func TestAddEndpointRejectsInvalidInstance(t *testing.T) {
	s := New()

	cases := []string{
		"with space",
		"a-very-long-instance-name-over-limit",
		"-leading-dash",
	}
	for _, instance := range cases {
		t.Run(instance, func(t *testing.T) {
			if _, err := s.AddEndpoint("gpu-box", "user@example", instance); err == nil {
				t.Fatalf("AddEndpoint(%q) succeeded, want validation error", instance)
			}
		})
	}
}

func TestEndpointMigration34BackfillsBlankInstance(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE endpoints (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			ssh_target TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		INSERT INTO endpoints (id, name, ssh_target, enabled, created_at, updated_at)
		VALUES ('endpoint-1', 'gpu', 'user@host', 1, '2026-01-01', '2026-01-01');
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for v := 1; v <= 33; v++ {
		if _, err := db.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`,
			v,
		); err != nil {
			t.Fatalf("seed migrations: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := openSeededDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db2.Close()

	var instance string
	if err := db2.QueryRow(`SELECT instance FROM endpoints WHERE id = 'endpoint-1'`).Scan(&instance); err != nil {
		t.Fatalf("scan instance: %v", err)
	}
	if instance != "" {
		t.Fatalf("legacy endpoint instance = %q, want empty", instance)
	}

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if version < 34 {
		t.Fatalf("schema version = %d, want >=34", version)
	}
}

func TestMigration151CarriesProfileColumnsIntoInstances(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`
		ALTER TABLE endpoints RENAME COLUMN instance TO profile;
		ALTER TABLE instance_roles RENAME TO profile_roles;
		INSERT INTO endpoints (id, name, ssh_target, enabled, profile, created_at, updated_at)
		VALUES ('endpoint-1', 'gpu', 'user@host', 1, 'dev', '2026-01-01', '2026-01-01');
		INSERT INTO profile_roles (role, session_id) VALUES ('chief_of_staff', 'session-a');
		DELETE FROM schema_migrations WHERE version >= 151;
	`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatal(err)
	}
	if got := s.GetEndpoint("endpoint-1"); got == nil || got.Instance != "dev" {
		t.Fatalf("migrated endpoint = %+v, want instance dev", got)
	}
	if got := s.GetInstanceRole("chief_of_staff"); got != "session-a" {
		t.Fatalf("migrated chief of staff = %q, want session-a", got)
	}
}
