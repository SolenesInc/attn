package daemon_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	kittyShowImage   = `printf '\033_Ga=T,q=2,f=24,s=2,v=2,i=77;AQIDBAUGBwgJCgsM\033\\'`
	kittyClearImages = `printf '\033_Ga=d,q=2\033\\'`
)

var kittyImagePixels = []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

func TestKittyPlacementsReachOnlyClientsThatAskedForThem(t *testing.T) {
	onEachPtyBackend(t, testKittyPlacementsReachOnlyClientsThatAskedForThem)
}

func testKittyPlacementsReachOnlyClientsThatAskedForThem(t *testing.T, w *world) {
	session := w.Spawn(w.App(), shellHarness, w.Path("shop"))
	terminal := w.Terminal(session)
	framesOnly := transportPeer(w, protocol.CapabilityBinaryPtyOutput)
	peers := map[string]*testworld.Peer{
		"describes and decodes frames":   transportPeer(w, protocol.CapabilityKittyImages, protocol.CapabilityBinaryPtyOutput),
		"describes without frames":       transportPeer(w, protocol.CapabilityKittyImages),
		"decodes frames but never asked": framesOnly,
		"asked for neither":              transportPeer(w),
	}
	for _, p := range peers {
		kittyAttach(p, terminal)
	}
	typist := peers["asked for neither"]

	typist.TypeLine(session, kittyShowImage)
	for _, name := range []string{"describes and decodes frames", "describes without frames"} {
		placed := testworld.Await(peers[name], protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool { return string(m.ID) == terminal })
		if len(placed.Placements) != 1 || placed.Seq == 0 {
			t.Fatalf("%s: kitty_placements = %+v, want the one image at its output seq", name, placed)
		}
		if p := placed.Placements[0]; p.ImageID != 77 || p.PixelWidth != 2 || p.PixelHeight != 2 || p.ImageGeneration == 0 {
			t.Errorf("%s: placement = %+v, want image 77 at its natural 2x2 size with its generation", name, p)
		}
	}

	typist.TypeLine(session, kittyClearImages)
	typist.TypeLine(session, `printf 'mark%s\n' er-cleared`)
	for _, name := range []string{"describes and decodes frames", "describes without frames"} {
		cleared := testworld.Await(peers[name], protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool { return string(m.ID) == terminal })
		if cleared.Placements == nil || len(cleared.Placements) != 0 {
			t.Errorf("%s: after clearing, kitty_placements = %+v, want an explicit empty list", name, cleared)
		}
	}
	transportAwaitOutput(typist, terminal, "marker-cleared")
	framesOnly.AwaitScreen(session, "marker-cleared")
	for _, name := range []string{"decodes frames but never asked", "asked for neither"} {
		for _, e := range peers[name].Received() {
			if e.Event == protocol.EventKittyPlacements {
				t.Errorf("%s: received kitty_placements, want no placement traffic", name)
			}
		}
	}
}

func TestKittyImagesOnScreenAreServedInTheFormEachClientReads(t *testing.T) {
	onEachPtyBackend(t, testKittyImagesOnScreenAreServedInTheFormEachClientReads)
}

func testKittyImagesOnScreenAreServedInTheFormEachClientReads(t *testing.T, w *world) {
	app := w.App()
	session := w.Spawn(app, shellHarness, w.Path("shop"))
	terminal := w.Terminal(session)
	describer := transportPeer(w, protocol.CapabilityKittyImages)
	kittyAttach(describer, terminal)
	describer.TypeLine(session, kittyShowImage)
	testworld.Await(describer, protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool { return string(m.ID) == terminal })

	relayed := kittyImage(describer, terminal, 77)
	plain := kittyImage(transportPeer(w), terminal, 77)
	for name, result := range map[string]protocol.KittyImageResultMessage{"a client without frames": relayed, "a plain client": plain} {
		pixels, err := base64.StdEncoding.DecodeString(protocol.Deref(result.DataB64))
		if !result.Success || err != nil || !bytes.Equal(pixels, kittyImagePixels) || string(result.ID) != terminal || result.ImageID != 77 ||
			protocol.Deref(result.Width) != 2 || protocol.Deref(result.Height) != 2 || protocol.Deref(result.Format) != "rgb" || protocol.Deref(result.Generation) == 0 {
			t.Errorf("%s got image 77 as %+v (pixels %v, %v), want base64 rgb 2x2 pixels with a generation", name, result, pixels, err)
		}
	}

	framed := transportConnectRaw(t, w, protocol.CapabilityKittyImages, protocol.CapabilityBinaryPtyOutput)
	framed.send(protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: protocol.TerminalID(terminal), ImageID: 77})
	frame, err := protocol.DecodeKittyImageFrame(framed.next("a kitty image frame", func(f transportFrame) bool { return f.binary }).data)
	if err != nil {
		t.Fatalf("the binary answer does not decode as a kitty image frame: %v", err)
	}
	if string(frame.TerminalID) != terminal || frame.ImageID != 77 || frame.Width != 2 || frame.Height != 2 || frame.Format != protocol.KittyImageFormatCodeRGB ||
		frame.Generation != uint64(protocol.Deref(plain.Generation)) || !bytes.Equal(frame.Pixels, kittyImagePixels) {
		t.Errorf("a client with frames got image 77 as %+v, want the same rgb 2x2 pixels and generation as the base64 answer", frame)
	}

	for name, missing := range map[string]protocol.KittyImageResultMessage{
		"a plain client":       kittyImage(transportPeer(w), terminal, 404),
		"a client with frames": kittyRawImage(t, framed, terminal, 404),
	} {
		if missing.Success || missing.ImageID != 404 || !strings.Contains(protocol.Deref(missing.Error), "404") {
			t.Errorf("%s asking for a missing image got %+v, want a failure naming image 404", name, missing)
		}
	}

	attached := kittyAttach(transportPeer(w, protocol.CapabilityKittyImages, protocol.CapabilityBinaryPtyOutput), terminal)
	if attached.Snapshot == nil || len(attached.Snapshot.Placements) != 1 || attached.Snapshot.Placements[0].ImageID != 77 {
		t.Errorf("a client attaching while the image is on screen got snapshot %+v, want the placement of image 77", attached.Snapshot)
	}
}

func TestAKittyImageKeepsOneIdentityThatNoOtherSessionShares(t *testing.T) {
	onEachPtyBackend(t, func(t *testing.T, w *world) {
		app := w.App()
		describer := transportPeer(w, protocol.CapabilityKittyImages)
		generations := map[string]int{}
		var terminals []string
		for _, dir := range []string{"shop", "docs"} {
			session := w.Spawn(app, shellHarness, w.Path(dir))
			terminal := w.Terminal(session)
			kittyAttach(describer, terminal)
			describer.TypeLine(session, "clear; "+kittyShowImage)
			placed := testworld.Await(describer, protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool {
				return string(m.ID) == terminal && len(m.Placements) == 1
			})
			generations[terminal] = placed.Placements[0].ImageGeneration
			terminals = append(terminals, terminal)
		}
		shop, docs := terminals[0], terminals[1]
		if generations[shop] == generations[docs] || generations[shop] >= 1<<53 || generations[docs] >= 1<<53 {
			t.Errorf("image 77 has generation %d in one session and %d in the other, want distinct identities a JavaScript number holds exactly", generations[shop], generations[docs])
		}

		reattached := kittyAttach(transportPeer(w, protocol.CapabilityKittyImages), shop)
		if reattached.Snapshot == nil || len(reattached.Snapshot.Placements) != 1 || reattached.Snapshot.Placements[0].ImageGeneration != generations[shop] {
			t.Errorf("a client attaching later got snapshot placements %+v, want image 77 at generation %d", reattached.Snapshot, generations[shop])
		}
		describer.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: protocol.TerminalID(shop), Cols: 60, Rows: 12})
		resized := testworld.Await(describer, protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool { return string(m.ID) == shop })
		if len(resized.Placements) != 1 || resized.Placements[0].ImageGeneration != generations[shop] {
			t.Errorf("after a resize the placements are %+v, want image 77 described again at generation %d", resized.Placements, generations[shop])
		}
		if served := kittyImage(describer, shop, 77); protocol.Deref(served.Generation) != generations[shop] {
			t.Errorf("image 77 is served at generation %d, want the %d its placement names", protocol.Deref(served.Generation), generations[shop])
		}
	})
}

func TestAKittyStorageLimitOfZeroTurnsImagesOff(t *testing.T) {
	t.Setenv("ATTN_KITTY_STORAGE_LIMIT", "0")
	w := newWorld(t)
	session := w.Spawn(w.App(), shellHarness, w.Path("shop"))
	terminal := w.Terminal(session)
	describer := transportPeer(w, protocol.CapabilityKittyImages)
	kittyAttach(describer, terminal)

	describer.TypeLine(session, kittyShowImage+`; printf 'mark%s\n' er-drawn`)
	transportAwaitOutput(describer, terminal, "marker-drawn")
	describer.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: protocol.TerminalID(terminal), Cols: 60, Rows: 12})
	if missing := kittyImage(describer, terminal, 77); missing.Success {
		t.Errorf("with images off image 77 was served at generation %d", protocol.Deref(missing.Generation))
	}
	for _, e := range describer.Received() {
		if e.Event == protocol.EventKittyPlacements {
			t.Errorf("with images off the daemon described placements at seq %d", protocol.Deref(e.Seq))
		}
	}
}

func TestResizingASessionWithoutImagesDescribesNoPlacements(t *testing.T) {
	w := newWorld(t)
	session := w.Spawn(w.App(), shellHarness, w.Path("shop"))
	terminal := w.Terminal(session)
	describer := transportPeer(w, protocol.CapabilityKittyImages)
	kittyAttach(describer, terminal)

	describer.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: protocol.TerminalID(terminal), Cols: 60, Rows: 12})
	describer.TypeLine(session, `printf 'mark%s\n' er-resized`)
	transportAwaitOutput(describer, terminal, "marker-resized")
	for _, e := range describer.Received() {
		if e.Event == protocol.EventKittyPlacements {
			t.Errorf("a session that never drew an image described placements at seq %d", protocol.Deref(e.Seq))
		}
	}
}

func TestClientsGetTheImageStreamRewrittenAndAResyncWhenItsLayoutCannotBeCarried(t *testing.T) {
	onEachPtyBackend(t, func(t *testing.T, w *world) {
		session := w.Spawn(w.App(), shellHarness, w.Path("shop"))
		terminal := w.Terminal(session)
		describer := transportPeer(w, protocol.CapabilityKittyImages)
		kittyAttach(describer, terminal)
		tall := make([]byte, 16*128*3)
		payload := filepath.Join(w.Dir, "images")
		program := "\x1b[6;3Hhead\x1b_Ga=T,q=2,f=24,s=2,v=2,i=76;AQIDBAUGBwgJCgsM\x1b\\tail\r\n" +
			"\x1b[?1049h alt0\r\nalt1\r\nalt2\r\nalt3\r\nalt4\r\n\x1b[6;1Halt5" +
			"\x1b_Ga=T,q=2,f=24,s=16,v=128,i=78;" + base64.StdEncoding.EncodeToString(tall) + "\x1b\\"
		if err := os.WriteFile(payload, []byte(program), 0o600); err != nil {
			t.Fatal(err)
		}

		describer.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: protocol.TerminalID(terminal), Cols: 20, Rows: 6, Xpixel: protocol.Ptr(20 * 8), Ypixel: protocol.Ptr(6 * 16)})
		describer.TypeLine(session, "cat "+payload)
		var seen []byte
		testworld.Await(describer, protocol.EventPtyOutput, func(e protocol.WebSocketEvent) bool {
			if protocol.Deref(e.ID) == terminal {
				seen = append(seen, transportDecodeOutput(t, e)...)
			}
			return bytes.Contains(seen, []byte("alt5"))
		})
		if bytes.Contains(seen, []byte("\x1b_G")) || !bytes.Contains(seen, []byte("head")) || !bytes.Contains(seen, []byte("tail")) {
			t.Errorf("the client read %q, want the text around the image without the kitty APC", seen)
		}
		desync := testworld.Await(describer, protocol.EventPtyDesync, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == terminal })
		if reason := protocol.Deref(desync.Reason); reason != "kitty_layout_anchor_clamped" {
			t.Errorf("the client was told to resync because %q, want kitty_layout_anchor_clamped", reason)
		}
	})
}

func onEachPtyBackend(t *testing.T, script func(t *testing.T, w *world)) {
	for _, backend := range []string{"embedded", "worker"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "worker" {
				t.Setenv("ATTN_PTY_BACKEND", "worker")
				t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
			}
			w := newWorld(t)
			if backend == "worker" {
				t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
			}
			script(t, w)
		})
	}
}

func kittyAttach(p *testworld.Peer, terminal string) protocol.AttachResultMessage {
	p.T.Helper()
	return testworld.Request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
}

func kittyImage(p *testworld.Peer, terminal string, imageID int) protocol.KittyImageResultMessage {
	p.T.Helper()
	return testworld.Request(p, protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: protocol.TerminalID(terminal), ImageID: imageID},
		protocol.EventKittyImageResult, func(r protocol.KittyImageResultMessage) bool { return r.ImageID == imageID })
}

func kittyRawImage(t *testing.T, p *transportRawPeer, terminal string, imageID int) protocol.KittyImageResultMessage {
	t.Helper()
	p.send(protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: protocol.TerminalID(terminal), ImageID: imageID})
	var result protocol.KittyImageResultMessage
	frame := p.next("kitty_image_result", func(f transportFrame) bool { return f.event == protocol.EventKittyImageResult })
	if err := json.Unmarshal(frame.data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
