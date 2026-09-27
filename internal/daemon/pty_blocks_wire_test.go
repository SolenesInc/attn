package daemon_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/protocol"
)

const blockBurst = `i=0
while [ $i -lt 150 ]; do
  printf '\033]133;A\007MARK-%04d\r\n\033]133;C;cmdline=mark-%04d\007filler-%04d-a\r\nfiller-%04d-b\r\n\033]133;D;0\007' $i $i $i $i
  i=$((i+1))
done
echo burst-$((1+1))
`

func TestEveryAttachDuringABlockBurstGetsBlocksThatPointAtTheirOwnPrompts(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	result, ws, pane := w.RequestSpawn(app, workspaceShell, w.Path("shop"))
	shell := result.ID
	burst := filepath.Join(w.Dir, "burst.sh")
	if err := os.WriteFile(burst, []byte(blockBurst), 0o755); err != nil {
		t.Fatal(err)
	}

	app.TypeLine(shell, "sh "+burst)
	var attaches []protocol.AttachResultMessage
	for i := range 20 {
		app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: shell, Cols: 60 + 20*(i%3), Rows: 30})
		attaches = append(attaches, kittyAttach(transportPeer(w), shell))
	}
	app.AwaitScreen(shell, "burst-2")
	settled := kittyAttach(transportPeer(w), shell)

	for i, attached := range append(attaches, settled) {
		lines := restoredSnapshotLines(t, attached)
		for _, block := range attached.Snapshot.Blocks {
			if block.PromptRow < 0 || block.PromptRow >= len(lines) || !strings.HasPrefix(lines[block.PromptRow], "MARK-") {
				t.Fatalf("attach %d: block %d points at row %d of a %d-row snapshot, want a MARK line", i, block.ID, block.PromptRow, len(lines))
			}
		}
	}
	lines := restoredSnapshotLines(t, settled)
	if len(settled.Snapshot.Blocks) != 150 {
		t.Fatalf("after the burst an attach got %d blocks, want 150", len(settled.Snapshot.Blocks))
	}
	for i, block := range settled.Snapshot.Blocks {
		if want := fmt.Sprintf("MARK-%04d", i); lines[block.PromptRow] != want {
			t.Errorf("block %d points at %q, want %q", i, lines[block.PromptRow], want)
		}
	}

	closing := transportPeer(w)
	closing.Send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: shell})
	closed := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: ws, PaneID: pane,
	}, protocol.CmdWorkspaceLayoutClosePane, ws)
	if !closed.Success {
		t.Fatalf("closing the pane while a client attached failed: %s", protocol.Deref(closed.Error))
	}
}

func restoredSnapshotLines(t *testing.T, attached protocol.AttachResultMessage) []string {
	t.Helper()
	if attached.Snapshot == nil {
		t.Fatalf("attach %+v carried no snapshot", attached)
	}
	payload, err := base64.StdEncoding.DecodeString(attached.Snapshot.SnapshotB64)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ghosttyvt.Restore(payload, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("the attach snapshot does not restore: %v", err)
	}
	defer restored.Close()
	lines := strings.Split(restored.PlainText(), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return lines
}
