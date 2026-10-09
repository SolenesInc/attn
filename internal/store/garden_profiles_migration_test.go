package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestGardenProfileMigrationUsesTheConvertedDefaultIdentity(t *testing.T) {
	for _, state := range []string{garden.StatusPlanted, garden.StatusGrowing, garden.StatusDormant, garden.StatusHarvested, garden.StatusWithered} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attn.db")
			s, err := newStoreAtVersion(path, 1791587735114907-1)
			if err != nil {
				t.Fatal(err)
			}
			view, err := s.ProfileMigration()
			if err != nil {
				t.Fatal(err)
			}
			profile, err := s.GetProfile(view.Manifest.ProfileID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RenameProfile(profile.ID, "Renamed", profile.Revision); err != nil {
				t.Fatal(err)
			}
			oldRequest := `{"cmd":"delegate","request_id":"old-request","cwd":"/fixture","assignment":{"kind":"new","brief":"Original assignment"}}`
			if _, _, err := s.ClaimDelegationOperation("old-request", "op-original", "original-session", "", "", oldRequest, time.Now()); err != nil {
				t.Fatal(err)
			}
			schema := garden.SeedsSchema()
			schema.Fields = schema.Fields[1:]
			if _, err := s.DefineDocumentCollection(schema, time.Now()); err != nil {
				t.Fatal(err)
			}
			storedSchema, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]any{"id": "s-abc123", "title": "existing work", "body": "keep this", "status": state, "edges": []any{}})
			if _, err := s.db.Exec(`INSERT INTO `+storedSchema.Table+` (id, body, rev, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`, "s-abc123", string(body), "2026-09-01T00:00:00Z", "2026-09-01T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 165`); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			// Migration 168 rebuilds the inbox from tables it drops, so a replay stops before it.
			migrated, err := newStoreAtVersion(path, 167)
			if err != nil {
				t.Fatal(err)
			}
			defer migrated.Close()
			storedSchema, _, err = migrated.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
			if err != nil {
				t.Fatal(err)
			}
			doc, found, err := migrated.GetDocument(*storedSchema, "s-abc123")
			if err != nil || !found {
				t.Fatalf("migrated seed: %+v %v", doc, err)
			}
			seed, err := garden.Decode(doc.Body)
			if err != nil || seed.ProfileID != profile.ID || seed.Status != state || seed.Body != "keep this" {
				t.Fatalf("migration changed work: %+v %v", seed, err)
			}
			operation, err := migrated.GetDelegationOperation("old-request")
			if err != nil {
				t.Fatal(err)
			}
			var request protocol.DelegateMessage
			if err := json.Unmarshal([]byte(operation.RequestJSON), &request); err != nil || protocol.Deref(request.ProfileID) != profile.ID || protocol.Deref(request.Assignment.Brief) != "Original assignment" || operation.Operation.State != protocol.DelegationOperationStateAccepted {
				t.Fatalf("migration changed the pending assignment: %+v %v", request, err)
			}
		})
	}
}
