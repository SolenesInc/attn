package daemon_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func inputRecordingShell(prompt string) string {
	return `#!/bin/sh
stty raw -echo
dd bs=1 count=1 >/dev/null 2>&1
printf '` + prompt + `'
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
	promptMark             = `\033]133;A\007`
	bashPromptReadingPaste = promptMark + `\033[?2004h$ \033]133;B\007`
	promptIgnoringPaste    = promptMark + `$ \033]133;B\007`
	typedLines             = "cd x\n\tmake\x1b[201~rm\r"
)

func TestATypedShellThatReadsPastesHoldsItsLinesAtTheFirstPromptAsOnePasteThatNeverRuns(t *testing.T) {
	app, session := typeIntoRecordingShell(t, bashPromptReadingPaste, typedLines)

	awaitOnePasteThenTheUsersKey(t, app, session, "\x1b[200~cd x\n\tmake[201~rm\x1b[201~")
}

func TestATypedShellThatIgnoresPastesHoldsItsTextOnOneLineAtTheFirstPromptAsOnePasteThatNeverRuns(t *testing.T) {
	app, session := typeIntoRecordingShell(t, promptIgnoringPaste, typedLines)

	awaitOnePasteThenTheUsersKey(t, app, session, "\x1b[200~cd xmake[201~rm\x1b[201~")
}

func typeIntoRecordingShell(t *testing.T, prompt, typed string) (*testworld.Peer, string) {
	t.Helper()
	w := &world{World: prepareWorld(t)}
	shell := filepath.Join(w.Dir, "recorder", "bash")
	if err := os.MkdirAll(filepath.Dir(shell), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fakeagent.WriteExecutable(shell, []byte(inputRecordingShell(prompt)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	w.start()
	app := w.App()
	session := w.Spawn(app, shellHarness, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Typed = protocol.Ptr(typed)
	})
	app.Type(session, "G", "user")
	return app, session
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
