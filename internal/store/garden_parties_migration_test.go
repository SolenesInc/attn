package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
)

func TestGardenPartiesMigrationNamesEveryPartyAndDropsNamedClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parties.db")
	old, err := newStoreAtVersion(path, 1793418267734801-1)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := mustCreateProfile(t, old, "Work")
	if _, err := old.db.Exec(`
INSERT INTO crew_members(member_key,profile_id,name) VALUES ('keel',?,'Keel');
INSERT INTO sessions(id,user_label,directory,created_at,profile_id,member_key,closed_at) VALUES
 ('day','Keel','/tmp/parties','',?,'keel','2026-10-01T00:00:00Z'),
 ('plain','Plain','/tmp/parties','',?,'','');
INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,created_at,updated_at,seed_id)
 VALUES ('run','1','occurrence',1,'{}','delivered','','plain','now','now','s-automat');
INSERT INTO delegation_operations(request_id,operation_id,request_json,state,progress,session_id,created_at,updated_at,profile_id,handover_tender_session,handover_tender_member) VALUES
 ('to-member','op-member','{}','accepted','','delegate','now','now',?,'day','keel'),
 ('to-session','op-session','{}','accepted','','delegate','now','now',?,'plain','');
`, profile.ID, profile.ID, profile.ID, profile.ID, profile.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	put := func(collection string, fields []string, id string, body map[string]any) {
		t.Helper()
		schema := docstore.CollectionSchema{Namespace: garden.Namespace, Collection: collection}
		for _, field := range fields {
			schema.Fields = append(schema.Fields, docstore.FieldSpec{Name: field, Type: docstore.FieldString})
		}
		if _, err := old.DefineDocumentCollection(schema, now); err != nil {
			t.Fatal(err)
		}
		declared, _, err := old.DocumentCollection(schema.Namespace, schema.Collection)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.PutDocument(*declared, id, encoded, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	seed := func(id string, extra map[string]any) {
		body := map[string]any{"id": id, "profile_id": profile.ID, "title": id, "status": "growing"}
		for k, v := range extra {
			body[k] = v
		}
		put(garden.CollectionSeeds, []string{"tender_session"}, id, body)
	}
	seed("s-memberd", map[string]any{"planter_session": "day", "tender_session": "day", "tender_member": "keel",
		"harvest_when": map[string]any{"pull_request": "github.com:a/b#1", "url": "https://github.com/a/b/pull/1", "set_at": "now", "set_by_member": "attn"},
		"check_results": map[string]any{"C1": map[string]any{
			"report":  map[string]any{"summary": "proved", "evidence": []any{}, "by_session": "plain", "at": "then"},
			"verdict": map[string]any{"confirmed": true, "by_member": "Keel", "at": "then"},
		}}})
	seed("s-closedd", map[string]any{"planter_member": "keel", "tender_session": "day"})
	seed("s-session", map[string]any{"planter_session": "plain", "tender_session": "plain"})
	seed("s-namedcl", map[string]any{"tender_member": "Bob"})
	seed("s-automat", map[string]any{"tender_session": "plain"})
	notes := []string{"author_session", "author_member"}
	put(garden.CollectionNotes, notes, "n-member", map[string]any{"id": "n-member", "seed": "s-memberd", "kind": "note", "body": "by the member", "author_session": "day", "author_member": "keel"})
	put(garden.CollectionNotes, notes, "n-attn", map[string]any{"id": "n-attn", "seed": "s-memberd", "kind": "note", "body": "by attn", "author_member": "attn"})
	put(garden.CollectionNotes, notes, "n-user", map[string]any{"id": "n-user", "seed": "s-session", "kind": "note", "body": "from the app"})
	put(garden.CollectionDispatches, nil, "delegate", map[string]any{"session_id": "delegate", "crown": "s-memberd", "dispatcher_session": "day", "dispatcher_member": "keel"})
	put(garden.CollectionDispatches, nil, "undispatched", map[string]any{"session_id": "undispatched", "crown": "s-session"})
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	read := func(collection, id string) map[string]any {
		t.Helper()
		schema, _, err := upgraded.DocumentCollection(garden.Namespace, collection)
		if err != nil {
			t.Fatal(err)
		}
		doc, found, err := upgraded.GetDocument(*schema, id)
		if err != nil || !found {
			t.Fatalf("%s %s: found=%v %v", collection, id, found, err)
		}
		var body map[string]any
		if err := json.Unmarshal(doc.Body, &body); err != nil {
			t.Fatal(err)
		}
		for _, legacy := range []string{"planter_session", "planter_member", "tender_session", "tender_member", "author_session", "author_member", "dispatcher_session", "dispatcher_member"} {
			if _, kept := body[legacy]; kept {
				t.Errorf("%s %s kept %s: %v", collection, id, legacy, body)
			}
		}
		return body
	}
	for id, want := range map[string]struct{ planter, tender string }{
		"s-memberd": {"member:keel", "member:keel"},
		"s-closedd": {"member:keel", "member:keel"},
		"s-session": {"session:plain", "session:plain"},
		"s-namedcl": {"user", ""},
		"s-automat": {"attn", "session:plain"},
	} {
		body := read(garden.CollectionSeeds, id)
		if body["planter"] != want.planter || (body["tender"] != nil && body["tender"] != want.tender) || (body["tender"] == nil) != (want.tender == "") {
			t.Errorf("%s planter=%v tender=%v, want %s and %q", id, body["planter"], body["tender"], want.planter, want.tender)
		}
	}
	memberSeed, err := garden.Decode(mustJSON(t, read(garden.CollectionSeeds, "s-memberd")))
	if err != nil {
		t.Fatal(err)
	}
	result := memberSeed.CheckResults["C1"]
	if memberSeed.HarvestWhen.SetBy.String() != "attn" || result.Report.By.String() != "session:plain" || result.Verdict.By.String() != "member:keel" || result.Report.Summary != "proved" {
		t.Errorf("proof authors: set_by=%v report=%+v verdict=%+v", memberSeed.HarvestWhen.SetBy, result.Report, result.Verdict)
	}
	for id, want := range map[string]string{"n-member": "member:keel", "n-attn": "attn", "n-user": "user"} {
		if got := read(garden.CollectionNotes, id)["author"]; got != want {
			t.Errorf("%s author = %v, want %s", id, got, want)
		}
	}
	listed, _, err := upgraded.ReadQuery(docstore.Query{Namespace: garden.Namespace, Collection: garden.CollectionNotes, Limit: docstore.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	var dropped []string
	for _, doc := range listed.Documents {
		if note, err := garden.DecodeNote(doc.Body); err == nil && note.Seed == "s-namedcl" {
			dropped = append(dropped, note.Author.String()+": "+note.Body)
		}
	}
	if len(dropped) != 1 || !strings.HasPrefix(dropped[0], "attn: ") || !strings.Contains(dropped[0], `"Bob"`) || !strings.Contains(dropped[0], "attn seed tend s-namedcl --for") {
		t.Errorf("the dropped named claim left notes %q, want one note from attn naming Bob and --for", dropped)
	}
	if got := read(garden.CollectionDispatches, "delegate")["dispatcher"]; got != "member:keel" {
		t.Errorf("dispatcher = %v, want member:keel", got)
	}
	if _, has := read(garden.CollectionDispatches, "undispatched")["dispatcher"]; has {
		t.Error("a dispatch without a dispatcher gained one")
	}
	for request, want := range map[string]string{"to-member": "member:keel", "to-session": "session:plain"} {
		var tender string
		if err := upgraded.db.QueryRow(`SELECT handover_tender FROM delegation_operations WHERE request_id = ?`, request).Scan(&tender); err != nil || tender != want {
			t.Errorf("%s handover tender = %q %v, want %s", request, tender, err, want)
		}
	}
}

func mustJSON(t *testing.T, body map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
