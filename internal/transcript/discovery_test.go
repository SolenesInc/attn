package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAResumeLookupOpensOnlyRolloutsNamedForTheConversation(t *testing.T) {
	sessionsDir := t.TempDir()
	for i := range 512 {
		path := filepath.Join(sessionsDir, fmt.Sprintf("rollout-native-decoy-%03d.jsonl", i))
		if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"decoy"}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(sessionsDir, "rollout-2026-07-18-native-target.jsonl")
	if err := os.WriteFile(want, []byte(`{"type":"session_meta","payload":{"id":"native-target"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened := 0
	got := findCodexTranscriptForResumeIn(sessionsDir, "native-target", func(path string) ([]byte, error) {
		opened++
		return readFirstJSONLLine(path)
	})
	if got != want || opened != 1 {
		t.Fatalf("the lookup found %q after opening %d rollouts, want %q after opening one", got, opened, want)
	}
}
