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
dd bs=1 count=1 >/dev/null 2>&1
printf '\033]133;A\007$ \033]133;B\007'
received=''
while :; do
  byte=$(dd bs=1 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
  [ -n "$byte" ] || exit 0
  if [ -n "$pasted" ]; then
    printf '%s.' "$byte"
    continue
  fi
  received="$received$byte."
  case "$received" in
  *1b.5b.32.30.31.7e.) printf 'received %s' "$received"; pasted=1 ;;
  esac
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
		m.Typed = protocol.Ptr("make x\n\x1b[201~rm\r")
	})
	app.Type(session, "G", "user")
	paste := "received " + hexBytes("\x1b[200~make x[201~rm\x1b[201~")
	app.AwaitScreen(session, paste)
	app.Type(session, "Z", "user")

	app.AwaitScreen(session, paste+hexBytes("Z"))
	if received := string(app.Screen(session)); strings.Count(received, "received") != 1 {
		t.Errorf("the shell received %s, want one paste", received)
	}
}

func hexBytes(text string) string {
	var out strings.Builder
	for _, b := range []byte(text) {
		out.WriteString(hex.EncodeToString([]byte{b}) + ".")
	}
	return out.String()
}
