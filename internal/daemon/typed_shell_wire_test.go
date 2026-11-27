package daemon_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

const inputRecordingShell = `#!/bin/sh
stty raw -echo
printf '\033]133;A\007$ \033]133;B\007'
while :; do
  byte=$(dd bs=1 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
  [ -n "$byte" ] || exit 0
  printf '%s.' "$byte"
done
`

func TestATypedShellHoldsItsTextAtTheFirstPromptAsOnePasteThatNeverRuns(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	shell := filepath.Join(w.Dir, "recorder", "bash")
	if err := os.MkdirAll(filepath.Dir(shell), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fakeagent.WriteExecutable(shell, []byte(inputRecordingShell), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	w.start()
	app := w.App()

	session := w.Spawn(app, shellHarness, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Typed = protocol.Ptr("make deploy\n\x1b[201~rm -rf build\r")
	})
	app.Type(session, "Z", "user")

	app.AwaitScreen(session, hexBytes("\x1b[200~make deploy[201~rm -rf build\x1b[201~Z"))
	if received := string(app.Screen(session)); strings.Count(received, hexBytes("\x1b[200~")) != 1 || strings.Contains(received, "0d.") || strings.Contains(received, "0a.") {
		t.Errorf("the shell received %s, want one paste with no newline or carriage return", received)
	}
}

func hexBytes(text string) string {
	var out strings.Builder
	for _, b := range []byte(text) {
		out.WriteString(hex.EncodeToString([]byte{b}) + ".")
	}
	return out.String()
}
