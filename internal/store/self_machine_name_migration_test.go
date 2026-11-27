package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const beforeSelfMachineTakesTheHostname = 1795791471971587 - 1

func TestUpgradeNamesTheMachinesOwnNamelessRowAfterItsHostname(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	short, _, _ := strings.Cut(host, ".")
	for _, c := range []struct{ name, outpost, want string }{
		{"", short + "-other", short},
		{short + "-kept", short + "-other", short + "-kept"},
		{"", strings.ToUpper(short), ""},
	} {
		path := filepath.Join(t.TempDir(), "attn.db")
		s, err := newStoreAtVersion(path, beforeSelfMachineTakesTheHostname)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE machines SET name = ? WHERE transport = 'self'`, c.name); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddOutpost(c.outpost, "user@"+c.outpost); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = NewWithDB(path)
		if err != nil {
			t.Fatal(err)
		}
		self, err := s.SelfMachine()
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if self.Name != c.want {
			t.Errorf("the machine's own row named %q beside outpost %q upgraded to %q, want %q", c.name, c.outpost, self.Name, c.want)
		}
	}
}
