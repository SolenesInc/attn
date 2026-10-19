package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestExistingDashboardPRsKeepTheirPollTimesAfterUpgrade(t *testing.T) {
	for _, stamp := range []string{"", "2026-10-10T12:00:00.123456789Z"} {
		t.Run(fmt.Sprintf("timestamp=%s", stamp), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upgrade.db")
			db, err := openDBAtVersion(path, 1791657335754741)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO prs(id,repo,number,title,url,role,state,reason,last_updated,last_polled,muted,head_sha)
    VALUES('github.com:acme/shop#1','acme/shop',1,'Ship checkout','https://github.com/acme/shop/pull/1','author','waiting','ready_to_merge',?,?,1,'head-a')`, stamp, stamp); err != nil {
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
				if pr == nil || pr.LastUpdated != stamp || !pr.Muted || protocol.Deref(pr.HeadSHA) != "head-a" {
					t.Fatalf("upgraded PR=%+v, want preserved timestamps, mute and head", pr)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
