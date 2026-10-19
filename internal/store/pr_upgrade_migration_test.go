package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestExistingDashboardPRsKeepWhatTheUserDidAfterUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := openDBAtVersion(path, 1791657335754741)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO prs(id,repo,number,title,url,role,state,reason,last_updated,last_polled,muted,head_sha)
    VALUES('github.com:acme/shop#1','acme/shop',1,'Ship checkout','https://github.com/acme/shop/pull/1','author','waiting','ready_to_merge','2026-10-10T12:00:00Z','2026-10-10T12:00:00Z',1,'head-a')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		s, err := NewWithDB(path)
		if err != nil {
			t.Fatal(err)
		}
		pr := s.GetPR("github.com:acme/shop#1")
		if pr == nil || !pr.Muted || protocol.Deref(pr.HeadSHA) != "head-a" {
			t.Fatalf("upgraded PR=%+v, want mute and head preserved", pr)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
