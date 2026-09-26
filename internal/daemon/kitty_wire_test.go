package daemon_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	kittyShowImage   = `printf '\033_Ga=T,q=2,f=24,s=2,v=2,i=77;AQIDBAUGBwgJCgsM\033\\'`
	kittyClearImages = `printf '\033_Ga=d,q=2\033\\'`
)

var kittyImagePixels = []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

func TestKittyPlacementsReachOnlyClientsThatAskedForThem(t *testing.T) {
	w := newWorld(t)
	framesOnly := w.App()
	session := w.Spawn(framesOnly, workspaceShell, w.Path("shop"))
	peers := map[string]*testworld.Peer{
		"describes and decodes frames":   transportPeer(w, protocol.CapabilityKittyImages, protocol.CapabilityBinaryPtyOutput),
		"describes without frames":       transportPeer(w, protocol.CapabilityKittyImages),
		"decodes frames but never asked": framesOnly,
		"asked for neither":              transportPeer(w),
	}
	for _, p := range peers {
		kittyAttach(p, session)
	}
	typist := peers["asked for neither"]

	typist.TypeLine(session, kittyShowImage)
	for _, name := range []string{"describes and decodes frames", "describes without frames"} {
		placed := testworld.Await(peers[name], protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool { return m.ID == session })
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
		cleared := testworld.Await(peers[name], protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool { return m.ID == session })
		if cleared.Placements == nil || len(cleared.Placements) != 0 {
			t.Errorf("%s: after clearing, kitty_placements = %+v, want an explicit empty list", name, cleared)
		}
	}
	transportAwaitOutput(typist, session, "marker-cleared")
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
	w := newWorld(t)
	app := w.App()
	session := w.Spawn(app, workspaceShell, w.Path("shop"))
	describer := transportPeer(w, protocol.CapabilityKittyImages)
	kittyAttach(describer, session)
	describer.TypeLine(session, kittyShowImage)
	testworld.Await(describer, protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool { return m.ID == session })

	relayed := kittyImage(describer, session, 77)
	plain := kittyImage(transportPeer(w), session, 77)
	for name, result := range map[string]protocol.KittyImageResultMessage{"a client without frames": relayed, "a plain client": plain} {
		pixels, err := base64.StdEncoding.DecodeString(protocol.Deref(result.DataB64))
		if !result.Success || err != nil || !bytes.Equal(pixels, kittyImagePixels) || result.ID != session || result.ImageID != 77 ||
			protocol.Deref(result.Width) != 2 || protocol.Deref(result.Height) != 2 || protocol.Deref(result.Format) != "rgb" || protocol.Deref(result.Generation) == 0 {
			t.Errorf("%s got image 77 as %+v (pixels %v, %v), want base64 rgb 2x2 pixels with a generation", name, result, pixels, err)
		}
	}

	framed := transportConnectRaw(t, w, protocol.CapabilityKittyImages, protocol.CapabilityBinaryPtyOutput)
	framed.send(protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: session, ImageID: 77})
	frame, err := protocol.DecodeKittyImageFrame(framed.next("a kitty image frame", func(f transportFrame) bool { return f.binary }).data)
	if err != nil {
		t.Fatalf("the binary answer does not decode as a kitty image frame: %v", err)
	}
	if frame.SessionID != session || frame.ImageID != 77 || frame.Width != 2 || frame.Height != 2 || frame.Format != protocol.KittyImageFormatCodeRGB ||
		frame.Generation != uint64(protocol.Deref(plain.Generation)) || !bytes.Equal(frame.Pixels, kittyImagePixels) {
		t.Errorf("a client with frames got image 77 as %+v, want the same rgb 2x2 pixels and generation as the base64 answer", frame)
	}

	for name, missing := range map[string]protocol.KittyImageResultMessage{
		"a plain client":       kittyImage(transportPeer(w), session, 404),
		"a client with frames": kittyRawImage(t, framed, session, 404),
	} {
		if missing.Success || missing.ImageID != 404 || !strings.Contains(protocol.Deref(missing.Error), "404") {
			t.Errorf("%s asking for a missing image got %+v, want a failure naming image 404", name, missing)
		}
	}

	attached := kittyAttach(transportPeer(w, protocol.CapabilityKittyImages, protocol.CapabilityBinaryPtyOutput), session)
	if attached.Snapshot == nil || len(attached.Snapshot.Placements) != 1 || attached.Snapshot.Placements[0].ImageID != 77 {
		t.Errorf("a client attaching while the image is on screen got snapshot %+v, want the placement of image 77", attached.Snapshot)
	}
}

func kittyAttach(p *testworld.Peer, session string) protocol.AttachResultMessage {
	p.T.Helper()
	return testworld.Request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: session},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == session })
}

func kittyImage(p *testworld.Peer, session string, imageID int) protocol.KittyImageResultMessage {
	p.T.Helper()
	return testworld.Request(p, protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: session, ImageID: imageID},
		protocol.EventKittyImageResult, func(r protocol.KittyImageResultMessage) bool { return r.ImageID == imageID })
}

func kittyRawImage(t *testing.T, p *transportRawPeer, session string, imageID int) protocol.KittyImageResultMessage {
	t.Helper()
	p.send(protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: session, ImageID: imageID})
	var result protocol.KittyImageResultMessage
	frame := p.next("kitty_image_result", func(f transportFrame) bool { return f.event == protocol.EventKittyImageResult })
	if err := json.Unmarshal(frame.data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
