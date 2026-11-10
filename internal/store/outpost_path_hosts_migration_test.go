package store

import (
	"path/filepath"
	"testing"
)

func TestOutpostPathHostsMigrationKeepsFileActivityAndDraftsOnTheHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paths.db")
	db, err := OpenDBAtSchemaVersion(path, 1794260594018865)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO file_activity(path,source,session_id,last_at,count) VALUES('/work/plan.md','opened','s1','2026-11-09T10:00:00+01:00',4)`,
		`INSERT INTO file_activity(path,source,session_id,last_at,count) VALUES('/work/plan.md','edited',NULL,'2026-11-09T11:00:00+01:00',2)`,
		`INSERT INTO markdown_annotation_drafts(path,annotations_json,generation,tombstone_generation,updated_at) VALUES('/work/plan.md','[{"id":"a1"}]',3,1,'2026-11-09T12:00:00Z')`,
		`INSERT INTO markdown_annotation_drafts(path,annotations_json,generation,tombstone_generation,updated_at) VALUES('attn://seed/s-1','[]',1,2,'2026-11-09T13:00:00Z')`,
		`INSERT INTO recent_locations(path,last_seen,use_count) VALUES('/work','2026-11-09T09:00:00Z',5)`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	type activity struct {
		host, path, source, lastAt string
		session                    *string
		count                      int
	}
	rows, err := upgraded.Query(`SELECT host, path, source, session_id, last_at, count FROM file_activity ORDER BY source`)
	if err != nil {
		t.Fatal(err)
	}
	var activities []activity
	for rows.Next() {
		var a activity
		if err := rows.Scan(&a.host, &a.path, &a.source, &a.session, &a.lastAt, &a.count); err != nil {
			t.Fatal(err)
		}
		activities = append(activities, a)
	}
	rows.Close()
	if len(activities) != 2 ||
		activities[0] != (activity{path: "/work/plan.md", source: "edited", lastAt: "2026-11-09T11:00:00+01:00", count: 2}) ||
		activities[1].host != "" || activities[1].source != "opened" || activities[1].session == nil || *activities[1].session != "s1" ||
		activities[1].lastAt != "2026-11-09T10:00:00+01:00" || activities[1].count != 4 {
		t.Fatalf("migrated file activity = %+v; want both rows kept on the home", activities)
	}

	type draft struct {
		host, path, annotations, updatedAt string
		generation, tombstone              int
	}
	draftRows, err := upgraded.Query(`SELECT host, path, annotations_json, generation, tombstone_generation, updated_at FROM markdown_annotation_drafts ORDER BY path`)
	if err != nil {
		t.Fatal(err)
	}
	var drafts []draft
	for draftRows.Next() {
		var d draft
		if err := draftRows.Scan(&d.host, &d.path, &d.annotations, &d.generation, &d.tombstone, &d.updatedAt); err != nil {
			t.Fatal(err)
		}
		drafts = append(drafts, d)
	}
	draftRows.Close()
	want := []draft{
		{path: "/work/plan.md", annotations: `[{"id":"a1"}]`, generation: 3, tombstone: 1, updatedAt: "2026-11-09T12:00:00Z"},
		{path: "attn://seed/s-1", annotations: "[]", generation: 1, tombstone: 2, updatedAt: "2026-11-09T13:00:00Z"},
	}
	if len(drafts) != 2 || drafts[0] != want[0] || drafts[1] != want[1] {
		t.Fatalf("migrated drafts = %+v; want %+v", drafts, want)
	}

	var host string
	var useCount int
	if err = upgraded.QueryRow(`SELECT host, use_count FROM recent_locations WHERE path='/work'`).Scan(&host, &useCount); err != nil || host != "" || useCount != 5 {
		t.Fatalf("recent location = host %q use_count %d err %v; want it kept on the home", host, useCount, err)
	}

	for _, statement := range []string{
		`INSERT INTO file_activity(host,path,source,last_at,count) VALUES('outpost-1','/work/plan.md','opened','2026-11-10T10:00:00Z',1)`,
		`INSERT INTO markdown_annotation_drafts(host,path,annotations_json,generation,updated_at) VALUES('outpost-1','/work/plan.md','[]',1,'2026-11-10T10:00:00Z')`,
	} {
		if _, err = upgraded.Exec(statement); err != nil {
			t.Fatalf("the same path on an outpost is its own row: %v", err)
		}
	}
}
