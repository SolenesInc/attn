package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/claudemod"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADaemonStartingOnANewVersionUpdatesTheModForLiveSessions(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	s.Stop()

	folder := claudemod.Dir(s.Dir)
	module := filepath.Join("hooks", "register.tsx")
	if err := os.MkdirAll(filepath.Join(folder, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, module), []byte("the earlier version's module"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.Start()
	s.App()

	current := t.TempDir()
	if err := claudemod.Install(current); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(current, module))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(folder, module))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("after the restart, with no launch, the module is %q, want the current one", got)
	}
}
