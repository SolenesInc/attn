package daemon_test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSynchronousClientRoundTripsLegalSeedBodies(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	seed := plantSeedAs(t, cli, "", "large body")
	for _, body := range []string{strings.Repeat("a", 100*1024), strings.Repeat("<", 64*1024), strings.Repeat("<", garden.MaxBodyBytes)} {
		edited, err := editSeedBody(t, cli, seed, body)
		if err != nil {
			t.Fatalf("edit %d body bytes: %v", len(body), err)
		}
		shown, err := cli.SeedShow("", seed)
		if err != nil || edited.Seed.Body != body || shown.Seed.Body != body {
			t.Fatalf("edit %d body bytes did not round-trip: %v", len(body), err)
		}
		t.Logf("seed_edit round-tripped %d body bytes", len(body))
	}
}

func TestTheSynchronousClientNamesOversizeFramesBeforeSending(t *testing.T) {
	w := newWorld(t)
	cli := w.Client().WithCaller("")
	body := strings.Repeat("<", garden.MaxBodyBytes)
	msg := protocol.SeedPlotMessage{Body: protocol.Ptr("Complete the assigned work."),
		Cmd: protocol.CmdSeedPlot, Title: "oversize plot",
		Children: []protocol.SeedPlotChild{{Title: "one", Body: &body}, {Title: "two", Body: &body}},
	}
	frame, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.SeedPlot("", "", msg)
	want := fmt.Sprintf("seed_plot request: max_socket_frame_bytes=%d, asked for %d bytes", protocol.MaxSocketFrameBytes, len(frame))
	if err == nil || err.Error() != want {
		t.Fatalf("oversize request: %v, want %q", err, want)
	}
	listed, err := cli.SeedList("", false, 0)
	if err != nil || len(listed.Seeds) != 0 {
		t.Fatalf("refused plot planted seeds: %+v, %v", listed, err)
	}
	t.Log(want)
}

type socketWriteFailure struct{ net.Conn }

func (c socketWriteFailure) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p[:len(p)-1])
	if err != nil {
		return n, err
	}
	return n, syscall.EPIPE
}

func TestTheSynchronousClientReadsTheReplyAfterAWriteFailure(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := client.NewWithDial(w.Socket, func() (net.Conn, error) {
			conn, err := w.DialUnix()
			return socketWriteFailure{conn}, err
		})
		planted, err := cli.SeedPlant("", "reply survives write failure", "Complete the assigned work.", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if shown, err := w.Client().SeedShow("", planted.Seed.ID); err != nil || shown.Seed.Title != planted.Seed.Title {
			t.Fatalf("read back planted seed: %v", err)
		}
	})
}

func TestTheWebSocketAcceptsTheSocketFrameBudget(t *testing.T) {
	w := newWorld(t)
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := w.App()
	spec := manualAutomation(w, strings.Repeat("<", 256*1024))
	result := testworld.Request(app, protocol.AutomationValidateMessage{
		Cmd: protocol.CmdAutomationValidate, DefinitionYaml: spec, RequestID: protocol.Ptr("large-frame"),
	}, protocol.EventAutomationValidateResult, func(m protocol.AutomationValidateResultMessage) bool {
		return protocol.Deref(m.RequestID) == "large-frame"
	})
	if !result.Success {
		t.Fatalf("validate a definition within the socket frame budget: %s", protocol.Deref(result.Error))
	}
}

func TestTheSynchronousClientPlantsPlotsWithinTheFrameLimit(t *testing.T) {
	w := newWorld(t)
	body := strings.Repeat("plan\n", 64*1024)
	plot, err := w.Client().SeedPlot("", "", protocol.SeedPlotMessage{
		Title: "large plot", Body: &body,
		Children: []protocol.SeedPlotChild{{Title: "large child", Body: &body}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plot.Crown.Body != body || len(plot.Children) != 1 || plot.Children[0].Body != body {
		t.Fatal("plot bodies did not round-trip")
	}
}
