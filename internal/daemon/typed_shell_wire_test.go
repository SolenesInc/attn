package daemon_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func inputRecordingShell(prompt, afterAKey string) string {
	return `#!/bin/sh
stty raw -echo
dd bs=1 count=1 >/dev/null 2>&1
printf '` + prompt + `'
if [ -n '` + afterAKey + `' ]; then
  dd bs=1 count=1 >/dev/null 2>&1
  printf '` + afterAKey + `'
fi
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
}

const (
	prompt           = `\033]133;A\007$ \033]133;B\007`
	bracketedPasteOn = `\033[?2004h`
	typedLines       = "cd x\n\tmake\x1b[201~rm\r"
)

func TestATypedShellThatReadsPastesHoldsItsLinesAtTheFirstPromptAsOnePasteThatNeverRuns(t *testing.T) {
	_, app, session := typeIntoRecordingShell(t, bracketedPasteOn, typedLines)
	app.AwaitScreen(session, "$ ")
	app.Type(session, "H", "user")

	awaitOnePasteThenTheUsersKey(t, app, session, "\x1b[200~cd x\n\tmake[201~rm\x1b[201~")
}

func TestATypedShellThatIgnoresPastesHoldsItsTextAtTheFirstPromptWithoutItsLineBreaksAsOnePasteThatNeverRuns(t *testing.T) {
	w, app, session := typeIntoRecordingShell(t, "", typedLines)
	w.awaitTimer(daemon.BracketedPasteGraceTimer)
	w.advance(daemon.BracketedPasteGrace)

	awaitOnePasteThenTheUsersKey(t, app, session, "\x1b[200~cd xmake[201~rm\x1b[201~")
}

func typeIntoRecordingShell(t *testing.T, afterAKey, typed string) (*world, *testworld.Peer, string) {
	t.Helper()
	w := &world{World: prepareWorld(t)}
	shell := filepath.Join(w.Dir, "recorder", "bash")
	if err := os.MkdirAll(filepath.Dir(shell), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fakeagent.WriteExecutable(shell, []byte(inputRecordingShell(prompt, afterAKey)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	w.start()
	app := w.App()
	session := w.Spawn(app, shellHarness, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Typed = protocol.Ptr(typed)
	})
	app.Type(session, "G", "user")
	return w, app, session
}

func awaitOnePasteThenTheUsersKey(t *testing.T, app *testworld.Peer, session, paste string) {
	t.Helper()
	received := "received " + hexBytes(paste)
	app.AwaitScreen(session, received)
	app.Type(session, "Z", "user")

	app.AwaitScreen(session, received+hexBytes("Z"))
	if screen := string(app.Screen(session)); strings.Count(screen, "received") != 1 {
		t.Errorf("the shell received %s, want one paste", screen)
	}
}

func hexBytes(text string) string {
	var out strings.Builder
	for _, b := range []byte(text) {
		out.WriteString(hex.EncodeToString([]byte{b}) + ".")
	}
	return out.String()
}
