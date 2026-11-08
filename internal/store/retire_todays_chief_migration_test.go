package store

import (
	"path/filepath"
	"testing"
)

const beforeRetiringTodaysChief = 1794102704740595

func TestUpgradeMovesTodaysChiefMailToTheChiefMember(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, beforeRetiringTodaysChief)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO profiles(id,name,created_at,chief_member) VALUES('profile-has','Has','t','keel')`,
		`INSERT INTO crew_members(member_key,profile_id,name) VALUES('keel','profile-has','Chief')`,
		`INSERT INTO profiles(id,name,created_at) VALUES('profile-new-one','New','t')`,
		`INSERT INTO profiles(id,name,created_at) VALUES('profile-quiet','Quiet','t')`,
		`INSERT INTO quick_captures(profile_id,id,inbox_item_id,submission,mailbox_kind,body,created_at) VALUES('profile-has','c1','i1','{}','chief','b','t')`,
		`INSERT INTO settings(key,value) VALUES('chief_model_claude','opus'),('chief_effort_codex','low'),('chief_context_window_cap','300000'),('default_model_claude','sonnet')`,
		`INSERT INTO asks(id,session_id,address,subject,markdown,base_dir,created_at) VALUES('ask-has','s','chief:profile-has','a','m','b','t'),('ask-new','s','chief:profile-new-one','a','m','b','t'),('ask-gone','s','chief:profile-deleted-long-ago','a','m','b','t')`,
		`INSERT INTO inbox_items(id,address,kind,coalesce_key,created_at) VALUES('old-dup','chief:profile-has','ring','pr','t'),('member-dup','member:keel','ring','pr','t'),('moved','chief:profile-has','ring','other','t')`,
		`INSERT INTO inbox_delivery(address,outstanding_at) VALUES('chief:profile-has','t'),('member:keel','t')`,
		`INSERT INTO pull_request_watches(address,session_id,pr_id,mode,created_at) VALUES('chief:profile-has','s','pr-1','m','t'),('member:keel','s','pr-1','m','t'),('chief:profile-has','s','pr-2','m','t')`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	text := func(query string, args ...any) string {
		t.Helper()
		var got string
		if err := s.db.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return got
	}
	const newKey = "chief-new-one"
	for query, want := range map[string]string{
		`SELECT chief_member FROM profiles WHERE id='profile-new-one'`:                      newKey,
		`SELECT profile_id FROM crew_members WHERE member_key='chief-new-one'`:              "profile-new-one",
		`SELECT chief_member FROM profiles WHERE id='profile-quiet'`:                        "",
		`SELECT address FROM asks WHERE id='ask-has'`:                                       "member:keel",
		`SELECT address FROM asks WHERE id='ask-new'`:                                       "member:" + newKey,
		`SELECT address FROM inbox_items WHERE id='moved'`:                                  "member:keel",
		`SELECT address FROM pull_request_watches WHERE pr_id='pr-2'`:                       "member:keel",
		`SELECT value FROM settings WHERE key='default_model_claude'`:                       "sonnet",
		`SELECT mailbox_kind || ':' || mailbox_member_id FROM quick_captures WHERE id='c1'`: "crew_member:keel",
		`SELECT count(*) FROM settings WHERE key GLOB 'chief_*'`:                            "0",
		`SELECT count(*) FROM asks WHERE id='ask-gone'`:                                     "0",
		`SELECT group_concat(id) FROM inbox_items WHERE coalesce_key='pr'`:                  "member-dup",
		`SELECT count(*) FROM pull_request_watches WHERE pr_id='pr-1'`:                      "1",
		`SELECT count(*) FROM inbox_delivery`:                                               "1",
	} {
		if got := text(query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	if _, err := s.Asks(); err != nil {
		t.Fatalf("reading the migrated asks: %v", err)
	}
}
