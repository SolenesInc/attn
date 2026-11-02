package main_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func setPhoneAccess(t *testing.T, app *testworld.Peer, on bool) {
	t.Helper()
	value, id := "false", "phone-access-off"
	if on {
		value, id = "true", "phone-access-on"
	}
	result := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "phone_access_enabled", Value: value, RequestID: protocol.Ptr(id)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == id })
	if !protocol.Deref(result.Success) {
		t.Fatal(protocol.Deref(result.Error))
	}
}

func linkConnected(m protocol.PhonesUpdatedMessage) bool {
	return m.Link.State == protocol.PhoneLinkStateConnected
}

func beginPairing(t *testing.T, app *testworld.Peer, profileID, id string) protocol.PhonePairingOffer {
	t.Helper()
	result := testworld.Request(app, protocol.PhonePairBeginMessage{Cmd: protocol.CmdPhonePairBegin, RequestID: id, ProfileID: profileID},
		protocol.EventPhonePairBeginResult, func(m protocol.PhonePairBeginResultMessage) bool { return m.RequestID == id })
	if !result.Success {
		t.Fatalf("phone_pair_begin: %s", protocol.Deref(result.Error))
	}
	return *result.Offer
}

func pairingOf(m protocol.PhonesUpdatedMessage, deviceID string) (protocol.PhonePairing, bool) {
	for _, pairing := range m.Pairings {
		if pairing.DeviceID == deviceID {
			return pairing, true
		}
	}
	return protocol.PhonePairing{}, false
}

func TestAPhonePairsReconnectsAndIsRevoked(t *testing.T) {
	t.Parallel()
	cloud := testworld.StartCloud(t)
	s := testworld.NewStack(t)
	s.Vars = append(s.Vars, "ATTN_CLOUD_URL="+cloud.HomeURL)
	s.Start()
	app := s.App()
	profile := app.SelectedProfile()

	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
	offer := beginPairing(t, app, profile, "pair-1")
	qr := testworld.ParsePhoneQR(t, offer.Qr)
	if qr.ProfileID != profile || qr.Cloud != cloud.HomeURL {
		t.Fatalf("QR = %+v, want profile %s and cloud %s", qr, profile, cloud.HomeURL)
	}

	phone := testworld.NewPhone(t, cloud, "Test iPhone")
	socket, err := phone.Dial(qr)
	if err != nil {
		t.Fatal(err)
	}
	paired := len(app.Log())
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	if welcome := socket.Hello(); welcome.Profile.ID != profile {
		t.Fatalf("welcome = %+v, want profile %s", welcome, profile)
	}
	listed := testworld.AwaitSince(app, paired, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool {
		pairing, ok := pairingOf(m, phone.DeviceID())
		return ok && pairing.Connected
	})
	if pairing, _ := pairingOf(listed, phone.DeviceID()); pairing.Name != "Test iPhone" || pairing.ProfileID != profile || len(listed.Offers) != 0 {
		t.Fatalf("after pairing: pairing %+v, offers %+v", pairing, listed.Offers)
	}

	t.Run("reconnects after the relay socket is cut", func(t *testing.T) {
		reconnected := len(app.Log())
		if cut := cloud.Cut(); cut != 1 {
			t.Fatalf("cut %d home connections, want 1", cut)
		}
		socket.AwaitPresence(false)
		socket.AwaitPresence(true)
		testworld.AwaitSince(app, reconnected, protocol.EventPhonesUpdated, linkConnected)
		if err := socket.Resume(qr); err != nil {
			t.Fatal(err)
		}
		if welcome := socket.Hello(); welcome.Profile.ID != profile {
			t.Fatalf("welcome after reconnect = %+v", welcome)
		}
	})

	t.Run("refuses a second use of the pairing secret", func(t *testing.T) {
		if _, err := testworld.NewPhone(t, cloud, "Second phone").Dial(qr); !errors.Is(err, testworld.RefusedStatus(http.StatusForbidden)) {
			t.Fatalf("dial with the window closed = %v, want the relay's 403", err)
		}
		beginPairing(t, app, profile, "pair-2")
		second, err := testworld.NewPhone(t, cloud, "Second phone").Dial(qr)
		if err != nil {
			t.Fatal(err)
		}
		if code := closeCode(t, second.Pair(qr)); code != 4000 {
			t.Fatalf("second use of the secret closed with %d, want the home's close (4000)", code)
		}
	})

	t.Run("refuses the device after revoke", func(t *testing.T) {
		revoke := protocol.PhonePairingRevokeMessage{Cmd: protocol.CmdPhonePairingRevoke, RequestID: "revoke", DeviceID: phone.DeviceID(), ProfileID: profile}
		revoked := len(app.Log())
		result := testworld.Request(app, revoke, protocol.EventPhonePairingRevokeResult, func(m protocol.PhonePairingRevokeResultMessage) bool { return m.RequestID == "revoke" })
		if !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
		if code := socket.AwaitClosed(); code != 4000 {
			t.Fatalf("revoked device's socket closed with %d, want the home's close (4000)", code)
		}
		testworld.AwaitSince(app, revoked, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool {
			_, ok := pairingOf(m, phone.DeviceID())
			return !ok
		})
		beginPairing(t, app, profile, "pair-3")
		again, err := phone.Dial(qr)
		if err != nil {
			t.Fatal(err)
		}
		if code := closeCode(t, again.Resume(qr)); code != 4000 {
			t.Fatalf("revoked device's handshake closed with %d, want the home's close (4000)", code)
		}
	})
}

func TestDeletingAProfileForgetsItsPhones(t *testing.T) {
	t.Parallel()
	cloud := testworld.StartCloud(t)
	s := testworld.NewStack(t)
	s.Vars = append(s.Vars, "ATTN_CLOUD_URL="+cloud.HomeURL)
	s.Start()
	app := s.App()
	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)

	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, Name: "Work", RequestID: "work"},
		protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "work" })
	if created.Profile == nil {
		t.Fatalf("profile_create: %s", protocol.Deref(created.Error))
	}
	work := *created.Profile
	qr := testworld.ParsePhoneQR(t, beginPairing(t, app, work.ID, "pair-work").Qr)
	phone := testworld.NewPhone(t, cloud, "Work phone")
	socket, err := phone.Dial(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()
	beginPairing(t, app, app.SelectedProfile(), "window-open")

	forgotten := len(app.Log())
	deleted := testworld.Request(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, ProfileID: work.ID, ExpectedRevision: int(work.Revision), RequestID: "delete-work"},
		protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "delete-work" })
	if deleted.Error != nil {
		t.Fatalf("profile_delete: %s", *deleted.Error)
	}
	if code := socket.AwaitClosed(); code != 4000 {
		t.Fatalf("the deleted profile's phone closed with %d, want the home's close (4000)", code)
	}
	testworld.AwaitSince(app, forgotten, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool {
		_, ok := pairingOf(m, phone.DeviceID())
		return !ok
	})
	again, err := phone.Dial(qr)
	if err != nil {
		t.Fatal(err)
	}
	if code := closeCode(t, again.Resume(qr)); code != 4000 {
		t.Fatalf("the deleted profile's phone reconnected; its handshake closed with %d, want the home's close (4000)", code)
	}
}

func TestPhoneAccessOffHoldsNoConnectionToTheCloud(t *testing.T) {
	t.Parallel()
	cloud := testworld.StartCloud(t)
	s := testworld.NewStack(t)
	s.Vars = append(s.Vars, "ATTN_CLOUD_URL="+cloud.HomeURL)
	s.Start()
	app := s.App()

	off := testworld.Request[protocol.PhonesUpdatedMessage](app, protocol.PhonesGetMessage{Cmd: protocol.CmdPhonesGet}, protocol.EventPhonesUpdated, nil)
	if off.Link.State != protocol.PhoneLinkStateOff || cloud.Open() != 0 {
		t.Fatalf("phone access off: link %+v, %d connections to the Cloud", off.Link, cloud.Open())
	}
	select {
	case <-cloud.Dials():
		t.Fatal("the home dialed the Cloud with phone access off")
	default:
	}

	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
	setPhoneAccess(t, app, false)
	guard := time.After(fakeagent.HangGuard)
	for cloud.Open() > 0 {
		select {
		case <-cloud.Closed():
		case <-guard:
			t.Fatalf("%d of the home's connections to the Cloud stayed open %s after phone access turned off", cloud.Open(), fakeagent.HangGuard)
		}
	}
	turnedOff := testworld.Request(app, protocol.PhonesGetMessage{Cmd: protocol.CmdPhonesGet}, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool {
		return m.Link.State == protocol.PhoneLinkStateOff
	})
	if open := cloud.Open(); open != 0 {
		t.Fatalf("after turning phone access off: link %+v, %d connections to the Cloud", turnedOff.Link, open)
	}
}

func closeCode(t *testing.T, err error) websocket.StatusCode {
	t.Helper()
	if err == nil {
		t.Fatal("the handshake succeeded; want the home to refuse it")
	}
	return websocket.CloseStatus(err)
}
