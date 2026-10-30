package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const frozenForwardHeader = `{"caller":""}`

func TestOutpostForwardFrozenTranscript(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	w.start()
	connected := make(chan *bridge.Conn, 1)
	startFarOutpost(t, w, func(conn *bridge.Conn) { connected <- conn })
	t.Cleanup(w.stop)
	app := w.App()
	setSetting(t, app, "outposts_enabled", "true")
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected
	})
	peer := <-connected
	ctx, cancel := context.WithTimeout(t.Context(), fakeagent.HangGuard)
	defer cancel()
	channel, requests, err := peer.Open(ctx, "forward", []byte(frozenForwardHeader))
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	stop := context.AfterFunc(ctx, func() { _ = channel.Close() })
	defer stop()
	completed := make(chan error, 1)
	go func() {
		count := 0
		var problem error
		for request := range requests {
			count++
			valid := request.Type == "forward-complete" && request.WantReply && len(request.Payload) == 0
			if !valid {
				problem = fmt.Errorf("completion request changed: type=%q reply=%v payload=%q", request.Type, request.WantReply, request.Payload)
			}
			_ = request.Reply(valid, nil)
		}
		if count != 1 {
			problem = fmt.Errorf("received %d completion requests, want one", count)
		}
		completed <- problem
	}()
	if _, err := io.WriteString(channel, `{"cmd":"query"}`); err != nil {
		t.Fatal(err)
	}
	if err := channel.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(channel)
	if err != nil {
		t.Fatal(err)
	}
	var response protocol.Response
	if err := json.Unmarshal(data, &response); err != nil || !response.Ok {
		t.Fatalf("frozen forward query response: %s, %v", data, err)
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
}
