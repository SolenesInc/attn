package daemon_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestPtyOutputArrivesInTheFormatEachClientAskedFor(t *testing.T) {
	w := newWorld(t)
	session := w.Spawn(w.App(), workspaceShell, w.Path("shop"))
	framed := transportConnectRaw(t, w, protocol.CapabilityBinaryPtyOutput)
	framed.send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: session})
	framed.next("attach_result", func(f transportFrame) bool { return f.event == protocol.EventAttachResult })
	plain := transportPeer(w)

	plain.TypeLine(session, `printf 'mark%s\n' er-one`)

	jsonSeq := transportAwaitOutput(plain, session, "marker-one").seq
	var framedOutput []byte
	var framedSeq uint32
	framed.next("a binary frame carrying marker-one", func(f transportFrame) bool {
		if !f.binary {
			return false
		}
		id, seq, data, err := protocol.DecodePtyOutputFrame(f.data)
		if err != nil {
			t.Fatalf("a binary frame does not decode as PTY output: %v", err)
		}
		if id != session {
			return false
		}
		framedOutput = append(framedOutput, data...)
		framedSeq = seq
		return bytes.Contains(framedOutput, []byte("marker-one"))
	})
	if uint32(jsonSeq) != framedSeq {
		t.Errorf("the output carrying marker-one reached the JSON client at seq %d and the binary client at seq %d, want the same", jsonSeq, framedSeq)
	}
}

type transportOutput struct {
	seq   int
	index int
}

func transportAwaitOutput(p *testworld.Peer, session, text string) transportOutput {
	p.T.Helper()
	var seen []byte
	found := testworld.Await(p, protocol.EventPtyOutput, func(e protocol.WebSocketEvent) bool {
		if protocol.Deref(e.ID) != session {
			return false
		}
		seen = append(seen, transportDecodeOutput(p.T, e)...)
		return bytes.Contains(seen, []byte(text))
	})
	return transportOutput{seq: protocol.Deref(found.Seq), index: transportOutputIndex(p.T, p.Received(), session, text)}
}

func transportOutputIndex(t testing.TB, events []protocol.WebSocketEvent, session, text string) int {
	t.Helper()
	var seen []byte
	for i, e := range events {
		if e.Event != protocol.EventPtyOutput || protocol.Deref(e.ID) != session {
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
