package daemon_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const lineFlood = `i=0
while [ ! -e "$1" ]; do
  printf 'L%06d.\n' $i
  i=$((i+1))
done
echo "flood-ended-after-$i-lines"
`

var (
	floodLine  = regexp.MustCompile(`L(\d+)\.`)
	floodEnded = regexp.MustCompile(`flood-ended-after-(\d+)-lines`)
)

func TestAClientAttachingMidFloodContinuesFromItsSnapshotWithoutAGapOrARepeat(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	shell := w.Spawn(app, workspaceShell, w.Path("shop"))
	flood := filepath.Join(w.Dir, "flood.sh")
	if err := os.WriteFile(flood, []byte(lineFlood), 0o755); err != nil {
		t.Fatal(err)
	}

	stop := filepath.Join(w.Dir, "stop-flood")
	app.TypeLine(shell, "sh "+flood+" "+stop)
	app.AwaitScreen(shell, "L000100.")
	type attached struct {
		peer   *testworld.Peer
		result protocol.AttachResultMessage
	}
	var clients []attached
	for range 12 {
		peer := transportPeer(w)
		clients = append(clients, attached{peer, kittyAttach(peer, shell)})
	}
	if err := os.WriteFile(stop, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for i, client := range clients {
		transportAwaitOutput(client.peer, shell, "-lines")
		lines := restoredSnapshotLines(t, client.result)
		last := ""
		for _, line := range lines {
			if line != "" {
				last = line
			}
		}
		stream := []byte(last)
		for _, e := range client.peer.Received() {
			if e.Event == protocol.EventPtyOutput && protocol.Deref(e.ID) == shell && protocol.Deref(e.Seq) > protocol.Deref(client.result.LastSeq) {
				stream = append(stream, transportDecodeOutput(t, e)...)
			}
		}
		ended := floodEnded.FindStringSubmatch(string(stream))
		if ended == nil {
			t.Fatalf("client %d never saw the flood end", i)
		}
		total, _ := strconv.Atoi(ended[1])
		matches := floodLine.FindAllStringSubmatch(string(stream), -1)
		if len(matches) == 0 {
			t.Errorf("client %d saw no flood lines after a snapshot ending %q", i, last)
			continue
		}
		previous, _ := strconv.Atoi(matches[0][1])
		for _, match := range matches[1:] {
			n, _ := strconv.Atoi(match[1])
			if n != previous+1 {
				t.Errorf("client %d attached at seq %d went from line %d to %d, want every line once in order", i, protocol.Deref(client.result.LastSeq), previous, n)
				break
			}
			previous = n
		}
		if previous != total-1 {
			t.Errorf("client %d stopped at line %d, want %d", i, previous, total-1)
		}
	}
	exitWorkspaceShells(app, shell)
}
