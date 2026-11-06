package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestDefaultNotebookRootStaysPutForAnInstancesOwnDataFolder(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	production, named := config.DataDirForInstance(""), config.DataDirForInstance("own-folder")
	for _, c := range []struct {
		name, instance, dataDir, want string
	}{
		{"production", "", production, filepath.Join(home, "attn-notebook")},
		{"production with a trailing slash", "", production + "/", filepath.Join(home, "attn-notebook")},
		{"a named instance", "own-folder", named, filepath.Join(home, "attn-notebook-own-folder")},
		{"a named instance with a trailing slash", "own-folder", named + "/", filepath.Join(home, "attn-notebook-own-folder")},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("ATTN_HARNESS_DATA_DIR", "")
			t.Setenv("ATTN_INSTANCE", c.instance)
			t.Setenv("ATTN_DATA_DIR", c.dataDir)
			if got, err := defaultNotebookRoot(); err != nil || got != c.want {
				t.Fatalf("defaultNotebookRoot() = %q, %v, want %q", got, err, c.want)
			}
		})
	}
}
