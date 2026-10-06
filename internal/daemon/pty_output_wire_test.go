package daemon_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestPtyOutputArrivesInTheFormatEachClientAskedFor(t *testing.T) {
	w := newWorld(t)
	terminal := w.Terminal(w.Spawn(w.App(), shellHarness, w.Path("shop")))
	framed := transportConnectRaw(t, w, protocol.CapabilityBinaryPtyOutput)
	plain := transportConnectRaw(t, w)
	for _, p := range []*transportRawPeer{framed, plain} {
		p.send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)})
		p.next("attach_result", func(f transportFrame) bool { return f.event == protocol.EventAttachResult })
	}

	plain.send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: "printf 'mark%s\\n' er-one\r"})

	framedSeq := transportOutputInOneFormat(t, framed, terminal, "marker-one", true)
	jsonSeq := transportOutputInOneFormat(t, plain, terminal, "marker-one", false)
	if jsonSeq != framedSeq {
		t.Errorf("the output carrying marker-one reached the JSON client at seq %d and the binary client at seq %d, want the same", jsonSeq, framedSeq)
	}
}

func transportOutputInOneFormat(t *testing.T, p *transportRawPeer, terminal, text string, binary bool) uint32 {
	t.Helper()
	format := map[bool]string{true: "binary frames", false: "JSON pty_output"}
	var output []byte
	var seq uint32
	unrequested := 0
	outputFrame := func(f transportFrame) (string, uint32, []byte, bool) {
		switch {
		case f.binary:
			id, frameSeq, data, err := protocol.DecodePtyOutputFrame(f.data)
			if err != nil {
				t.Fatalf("a binary frame does not decode as PTY output: %v", err)
			}
			return string(id), frameSeq, data, true
		case f.event == protocol.EventPtyOutput:
			var e protocol.WebSocketEvent
			if err := json.Unmarshal(f.data, &e); err != nil {
				t.Fatalf("pty_output does not decode: %v", err)
			}
			return protocol.Deref(e.ID), uint32(protocol.Deref(e.Seq)), transportDecodeOutput(t, e), true
		}
		return "", 0, nil, false
	}
	p.next(text+" as "+format[binary], func(f transportFrame) bool {
		id, frameSeq, data, ok := outputFrame(f)
		if !ok || id != terminal {
			return false
		}
		if f.binary != binary {
			unrequested++
			return false
		}
		output = append(output, data...)
		seq = frameSeq
		return bytes.Contains(output, []byte(text))
	})
	p.send(protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings})
	p.next("settings_updated", func(f transportFrame) bool {
		if _, _, _, ok := outputFrame(f); ok && f.binary != binary {
			unrequested++
		}
		return f.event == protocol.EventSettingsUpdated
	})
	if unrequested > 0 {
		t.Errorf("a client that asked for %s also received %d PTY outputs as %s", format[binary], unrequested, format[!binary])
	}
	return seq
}

type transportOutput struct {
	seq   int
	index int
}

func transportAwaitOutput(p *testworld.Peer, terminal, text string) transportOutput {
	p.T.Helper()
	var seen []byte
	found := testworld.Await(p, protocol.EventPtyOutput, func(e protocol.WebSocketEvent) bool {
		if protocol.Deref(e.ID) != terminal {
			return false
		}
		seen = append(seen, transportDecodeOutput(p.T, e)...)
		return bytes.Contains(seen, []byte(text))
	})
	return transportOutput{seq: protocol.Deref(found.Seq), index: transportOutputIndex(p.T, p.Received(), terminal, text)}
}

func transportOutputIndex(t testing.TB, events []protocol.WebSocketEvent, terminal, text string) int {
	t.Helper()
	var seen []byte
	for i, e := range events {
		if e.Event != protocol.EventPtyOutput || protocol.Deref(e.ID) != terminal {
			continue
		}
		seen = append(seen, transportDecodeOutput(t, e)...)
		if bytes.Contains(seen, []byte(text)) {
			return i
		}
	}
	return -1
}

func transportDecodeOutput(t testing.TB, e protocol.WebSocketEvent) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(protocol.Deref(e.Data))
	if err != nil {
		t.Fatalf("pty_output data is not base64: %v", err)
	}
	return data
}
