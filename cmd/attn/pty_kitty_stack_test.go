package main_test

import (
	"encoding/base64"
	"slices"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestReopeningATerminalDescribesOnlyTheImagesItsSnapshotShows(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	snapshot := s.PauseAt(pausepoint.PtyAttachSnapshot)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	attachWithPolicy(t, app, shell, protocol.AttachPolicyFreshSpawn)
	show := func(row string, image int) string {
		return `printf '\033[` + row + `;1H\033_Ga=T,q=2,f=24,s=2,v=2,i=` + strconv.Itoa(image) + `;AQIDBAUGBwgJCgsM\033\\'`
	}
	app.TypeLine(shell, "clear; "+show("3", 70))
	placed := awaitPlacement(app, shell, 70)

	reopened := s.App()
	terminal := reopened.Terminal(shell)
	reopened.Send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal), AttachPolicy: protocol.Ptr(protocol.AttachPolicyRelaunchRestore)})
	snapshot.Await()
	app.TypeLine(shell, show("6", 71))
	moved := awaitPlacement(app, shell, 71)
	snapshot.Release()
	result := testworld.Await(reopened, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	if !result.Success || result.Snapshot == nil {
		t.Fatalf("reopening the terminal returned %+v, want a snapshot", result)
	}
	lastSeq := protocol.Deref(result.LastSeq)
	if images := placementImages(result.Snapshot.Placements); !slices.Equal(images, []int{70}) {
		t.Errorf("the reopened terminal's snapshot describes images %v, want only 70, the image placed before the snapshot", images)
	}
	if lastSeq < placed.Seq || lastSeq >= moved.Seq {
		t.Errorf("the snapshot covers output through seq %d, want at least %d (image 70 placed) and below %d (image 71 placed)", lastSeq, placed.Seq, moved.Seq)
	}
	if live := awaitPlacement(reopened, shell, 71); live.Seq <= lastSeq {
		t.Errorf("image 71 reached the reopened terminal at seq %d, which its snapshot through seq %d claims to cover", live.Seq, lastSeq)
	}
}

func TestAnImageArrivingInPiecesSendsNoEmptyOutput(t *testing.T) {
	t.Parallel()
	for _, backend := range []string{"worker", "embedded"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t)
			s.Vars = append(s.Vars, "ATTN_PTY_BACKEND="+backend)
			held := s.PauseAt(pausepoint.PtyOutputHeld)
			s.Start()
			app := s.App()
			shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
			attachWithPolicy(t, app, shell, protocol.AttachPolicyFreshSpawn)
			pixels := make([]byte, 8*8*3)
			for i := range pixels {
				pixels[i] = byte(i * 7 % 251)
			}
			payload := base64.StdEncoding.EncodeToString(pixels)
			// End readiness output at the marker so a delayed newline cannot mix with the image.
			app.TypeLine(shell, `stty -echo; printf arm''ed; read first; printf '\033_Ga=T,q=2,f=24,s=8,v=8,i=5;`+payload[:200]+
				`'; read rest; printf '`+payload[200:]+`\033\\done-%s\n' pieces`)
			app.AwaitScreen(shell, "armed")
			app.TypeLine(shell, "")
			held.Await()
			held.Release()
			app.TypeLine(shell, "")
			app.AwaitScreen(shell, "done-pieces")

			if empties := app.EmptyOutputs(shell); empties != 0 {
				t.Errorf("the terminal sent %d empty outputs while an image arrived in pieces, want none", empties)
			}
		})
	}
}

func awaitPlacement(p *testworld.Peer, session string, image int) protocol.KittyPlacementsMessage {
	p.T.Helper()
	terminal := p.Terminal(session)
	return testworld.Await(p, protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool {
		return string(m.ID) == terminal && slices.Contains(placementImages(m.Placements), image)
	})
}

func placementImages(placements []protocol.KittyPlacement) []int {
	images := make([]int, 0, len(placements))
	for _, placement := range placements {
		images = append(images, placement.ImageID)
	}
	return images
}
