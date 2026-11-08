package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestBusStatusReportsProducersAndRefusesToSwitchAnUnknownConsumer(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	before := producedBy(busStatus(t, app), "document.changed")
	put(t, cli, gateNS, "a", `{}`)

	status := busStatus(t, app)
	if produced := producedBy(status, "document.changed"); produced != before+1 || status.Rows < produced {
		t.Errorf("bus status counts %d document.changed facts in %d rows, want the %d before and the one write", produced, status.Rows, before)
	}
	if status.RecentWindowSeconds <= 0 || status.BaselineWindowSeconds <= 0 || status.SurgeRatePerHour <= 0 {
		t.Errorf("bus status carries recent window %v, baseline window %v and surge tripwire %v, want all set",
			status.RecentWindowSeconds, status.BaselineWindowSeconds, status.SurgeRatePerHour)
	}

	unknown := testworld.Request(app, protocol.BusSetConsumerEnabledMessage{
		Cmd: protocol.CmdBusSetConsumerEnabled, RequestID: "enable-nope", Consumer: "nope", Enabled: true,
	}, protocol.EventBusSetConsumerEnabledResult, func(m protocol.BusSetConsumerEnabledResultMessage) bool { return m.RequestID == "enable-nope" })
	if unknown.Success || protocol.Deref(unknown.Error) != "no consumer named nope" || unknown.Consumer != "nope" {
		t.Errorf("enabling an unknown consumer answered %+v, want a refusal naming nope", unknown)
	}
}

func TestADaemonStartsAndWritesWhileItsFirstProducerReportReadsTheLog(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	reading := w.PauseAt(pausepoint.BusProducersReading)
	w.start()
	reading.Await()

	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	before := producedBy(busStatus(t, app), "document.changed")
	put(t, cli, gateNS, "a", `{}`)
	if produced := producedBy(busStatus(t, app), "document.changed"); produced != before+1 {
		t.Errorf("bus status while the first report reads counts %d document.changed facts, want the %d before and the one write", produced, before)
	}
	reading.Release()
}
