package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPushWaitingForTheLinkDoesNotHoldTheDaemonsStop(t *testing.T) {
	cloud := testworld.StartCloud(t)
	w := newWorld(t, fakeagent.Claude)
	t.Setenv(config.CloudURLEnv, cloud.HomeURL)
	app := w.App()
	setSetting(t, app, "asks_enabled", "true")
	setSetting(t, app, "phone_access_enabled", "true")
	linked := func(m protocol.PhonesUpdatedMessage) bool { return m.Link.State == protocol.PhoneLinkStateConnected }
	testworld.Await(app, protocol.EventPhonesUpdated, linked)
	paired := testworld.Request(app, protocol.PhonePairBeginMessage{Cmd: protocol.CmdPhonePairBegin, RequestID: "pair", ProfileID: app.SelectedProfile()},
		protocol.EventPhonePairBeginResult, func(m protocol.PhonePairBeginResultMessage) bool { return m.RequestID == "pair" })
	if !paired.Success {
		t.Fatalf("phone_pair_begin: %s", protocol.Deref(paired.Error))
	}
	qr := testworld.ParsePhoneQR(t, paired.Offer.Qr)
	phone := testworld.NewPhone(t, cloud, "iPhone")
	socket, err := phone.Dial(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)

	before := len(app.Log())
	cloud.Outage()
	testworld.AwaitSince(app, before, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool { return !linked(m) })
	if _, err := w.Client().AskOpen(protocol.SessionID(asker), "", ":::question\nShip during the gap?\n- [ ] Yes\n- [ ] No\n:::\n", "", w.Path("shop")); err != nil {
		t.Fatalf("ask: %v", err)
	}
	w.awaitTimer(daemon.PhonePushLinkWaitTimer)

	w.stop()
}
