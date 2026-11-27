package store

import (
	"path/filepath"
	"testing"
)

const beforeAsksRecordTheirPlace = 1795786129174042 - 1

func TestUpgradeGivesEachStoredAskItsAskingSessionsMachineAndFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, beforeAsksRecordTheirPlace)
	if err != nil {
		t.Fatal(err)
	}
	self := s.MachineID()
	for _, statement := range []string{
		`INSERT INTO sessions(id,user_label,directory,host) VALUES('on-home','h','/home/shop','` + self + `'),('on-far','f','/far/shop','machine-far')`,
		`INSERT INTO asks(id,session_id,address,subject,markdown,base_dir,created_at) VALUES('ask-home','on-home','session:on-home','a','m','/home/shop/docs','t'),('ask-far','on-far','session:on-far','a','m','/far/shop','t'),('ask-gone','pruned','session:pruned','a','m','/somewhere','t')`,
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
	for id, want := range map[string][2]string{
		"ask-home": {self, "/home/shop"},
		"ask-far":  {"machine-far", "/far/shop"},
		"ask-gone": {self, "/somewhere"},
	} {
		a, err := s.GetAsk(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got := [2]string{a.Host, a.Cwd}; got != want {
			t.Errorf("%s upgraded to machine and folder %q, want %q", id, got, want)
		}
	}
	if _, err := s.db.Exec(`UPDATE asks SET host = '' WHERE id = 'ask-far'`); err == nil {
		t.Error("an ask's machine was cleared, want it refused")
	}
}
