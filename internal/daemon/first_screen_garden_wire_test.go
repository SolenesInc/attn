package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWritesGoThroughWhileTheGardenIsFirstRead(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	reading := w.PauseAt(pausepoint.GardenSeedsReading)
	w.start()
	reading.Await()

	cli := w.Client()
	defineRequests(t, cli, gateNS)
	put(t, cli, gateNS, "a", `{}`)

	app := w.ConnectApp()
	reading.Release()
	testworld.Await[protocol.InitialStateMessage](app, protocol.EventInitialState, nil)
}

func TestADocumentQueryHoldsUpNoWrite(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	reading := w.PauseAt(pausepoint.DocumentQueryReading)
	answered := make(chan error, 1)
	go func() {
		_, err := w.Client().DocQuery(protocol.DocumentQuery{Namespace: gateNS, Collection: requests})
		answered <- err
	}()
	reading.Await()

	put(t, cli, gateNS, "a", `{}`)

	reading.Release()
	if err := <-answered; err != nil {
		t.Fatalf("the held query: %v", err)
	}
	if got := query(t, cli, protocol.DocumentQuery{Namespace: gateNS, Collection: requests}); len(got) != 1 {
		t.Errorf("after the write the query finds %d documents, want 1", len(got))
	}
}
