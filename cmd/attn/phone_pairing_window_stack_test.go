package main_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const relayRefused = 4403

func startPairingStack(t *testing.T, vars ...string) (*testworld.Cloud, *testworld.Stack, *testworld.Peer) {
	t.Helper()
	cloud := testworld.StartCloud(t)
	s := testworld.NewStack(t)
	s.Vars = append(append(s.Vars, "ATTN_CLOUD_URL="+cloud.HomeURL), vars...)
	s.Start()
	app := s.App()
	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
	return cloud, s, app
}

func startPairingHome(t *testing.T, vars ...string) (*testworld.Cloud, *testworld.Peer) {
	t.Helper()
	cloud, _, app := startPairingStack(t, vars...)
	return cloud, app
}

func addProfile(t *testing.T, app *testworld.Peer, name string) protocol.Profile {
	t.Helper()
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, Name: name, RequestID: "create-" + name},
		protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "create-"+name })
	if created.Profile == nil {
		t.Fatalf("profile_create: %s", protocol.Deref(created.Error))
	}
	return *created.Profile
}

func cancelPairing(t *testing.T, app *testworld.Peer, profileID, id string) {
	t.Helper()
	result := testworld.Request(app, protocol.PhonePairCancelMessage{Cmd: protocol.CmdPhonePairCancel, RequestID: id, ProfileID: profileID},
		protocol.EventPhonePairCancelResult, func(m protocol.PhonePairCancelResultMessage) bool { return m.RequestID == id })
	if !result.Success {
		t.Fatalf("phone_pair_cancel: %s", protocol.Deref(result.Error))
	}
}

func openWindow(t *testing.T, app *testworld.Peer, profileID, id string) testworld.PhoneQR {
	t.Helper()
	return testworld.ParsePhoneQR(t, beginPairing(t, app, profileID, id).Qr)
}

func holdSocket(t *testing.T, cloud *testworld.Cloud, qr testworld.PhoneQR) *testworld.PhoneSocket {
	t.Helper()
	socket, err := testworld.NewPhone(t, cloud, "QR holder").DialToPair(qr)
	if err != nil {
		t.Fatalf("dial with a live relay token: %v", err)
	}
	return socket
}

func expectLapsed(t *testing.T, cloud *testworld.Cloud, socket *testworld.PhoneSocket, qr testworld.PhoneQR) {
	t.Helper()
	if code := socket.AwaitClosed(); code != relayRefused {
		t.Fatalf("the lapsed window's socket closed with %d, want the relay's %d", code, relayRefused)
	}
	if _, err := testworld.NewPhone(t, cloud, "Latecomer").DialToPair(qr); !errors.Is(err, testworld.ErrNotAdmitted) {
		t.Fatalf("dial after the window lapsed = %v, want the relay's 4403", err)
	}
}

func TestCancellingAPairingCodeClosesItsWindowAtTheRelay(t *testing.T) {
	t.Parallel()
	cloud, app := startPairingHome(t)
	profile := app.SelectedProfile()
	qr := openWindow(t, app, profile, "pair")
	socket := holdSocket(t, cloud, qr)

	listed := len(app.Log())
	cancelPairing(t, app, profile, "cancel")

	expectLapsed(t, cloud, socket, qr)
	testworld.AwaitSince(app, listed, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool { return len(m.Offers) == 0 })

	cancelPairing(t, app, profile, "cancel-again")
	after := openWindow(t, app, profile, "pair-after")
	phone := testworld.NewPhone(t, cloud, "Test iPhone")
	paired, err := phone.DialToPair(after)
	if err != nil {
		t.Fatal(err)
	}
	if err := paired.Pair(after); err != nil {
		t.Fatal(err)
	}
	paired.Hello()
}

func TestAnExpiredPairingCodeClosesItsWindowAtTheRelay(t *testing.T) {
	t.Parallel()
	cloud, app := startPairingHome(t, config.PairingLifetimeEnv+"=5s")
	qr := openWindow(t, app, app.SelectedProfile(), "pair")
	expectLapsed(t, cloud, holdSocket(t, cloud, qr), qr)
}

func TestShowingANewPairingCodeClosesTheOldWindow(t *testing.T) {
	t.Parallel()
	cloud, app := startPairingHome(t)
	profile := app.SelectedProfile()
	old := openWindow(t, app, profile, "pair-old")
	socket := holdSocket(t, cloud, old)

	fresh := openWindow(t, app, profile, "pair-new")

	if code := socket.AwaitClosed(); code != relayRefused {
		t.Fatalf("the replaced code's socket closed with %d, want the relay's %d", code, relayRefused)
	}
	holdSocket(t, cloud, fresh)
}

func TestUsingAPairingCodeKeepsThePhoneAndDropsOtherHoldersOfIt(t *testing.T) {
	t.Parallel()
	cloud, app := startPairingHome(t)
	qr := openWindow(t, app, app.SelectedProfile(), "pair")
	other := holdSocket(t, cloud, qr)

	phone := testworld.NewPhone(t, cloud, "Test iPhone")
	socket, err := phone.DialToPair(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()

	expectLapsed(t, cloud, other, qr)

	socket.Send(map[string]any{"cmd": "ping"})
	if raw, err := socket.Next(); err != nil {
		t.Fatalf("the new phone was cut off once its window closed: %v", err)
	} else if !strings.Contains(string(raw), `"event"`) {
		t.Fatalf("the new phone's ping was answered with %s", raw)
	}
	if resumed, err := phone.Dial(qr); err != nil {
		t.Fatalf("the paired phone's later dial, with no token: %v", err)
	} else if err := resumed.Resume(qr); err != nil {
		t.Fatal(err)
	}
}

func TestEachProfileHasItsOwnPairingWindow(t *testing.T) {
	t.Parallel()
	cloud, app := startPairingHome(t)
	home := app.SelectedProfile()
	work := addProfile(t, app, "Work")
	homeQR := openWindow(t, app, home, "pair-home")
	workQR := openWindow(t, app, work.ID, "pair-work")
	homeHolder := holdSocket(t, cloud, homeQR)

	phone := testworld.NewPhone(t, cloud, "Work phone")
	workSocket, err := phone.DialToPair(workQR)
	if err != nil {
		t.Fatal(err)
	}
	if err := workSocket.Pair(workQR); err != nil {
		t.Fatal(err)
	}
	if welcome := workSocket.Hello(); welcome.Profile.ID != work.ID {
		t.Fatalf("welcome = %+v, want profile %s", welcome, work.ID)
	}
	if _, err := testworld.NewPhone(t, cloud, "Latecomer").DialToPair(workQR); !errors.Is(err, testworld.ErrNotAdmitted) {
		t.Fatalf("dial with the used code = %v, want the relay's 4403", err)
	}

	otherHolder := holdSocket(t, cloud, homeQR)
	workQR2 := openWindow(t, app, work.ID, "pair-work-2")
	workHolder := holdSocket(t, cloud, workQR2)
	cancelPairing(t, app, work.ID, "cancel-work")
	expectLapsed(t, cloud, workHolder, workQR2)

	workSocket.Send(map[string]any{"cmd": "ping"})
	if _, err := workSocket.Next(); err != nil {
		t.Fatalf("the paired phone was cut off when another window closed: %v", err)
	}
	cancelPairing(t, app, home, "cancel-home")
	expectLapsed(t, cloud, homeHolder, homeQR)
	if code := otherHolder.AwaitClosed(); code != relayRefused {
		t.Fatalf("the second holder closed with %d, want the relay's %d", code, relayRefused)
	}
}

func TestQuittingTheAppClosesItsPairingWindowsAndKeepsPairedPhones(t *testing.T) {
	t.Parallel()
	cloud, s, app := startPairingStack(t)
	profile := app.SelectedProfile()
	work := addProfile(t, app, "Work")

	spent := openWindow(t, app, work.ID, "pair-work")
	phone := testworld.NewPhone(t, cloud, "Work phone")
	paired, err := phone.DialToPair(spent)
	if err != nil {
		t.Fatal(err)
	}
	if err := paired.Pair(spent); err != nil {
		t.Fatal(err)
	}
	paired.Hello()

	qr := openWindow(t, app, profile, "pair-home")
	holder := holdSocket(t, cloud, qr)

	app.Close()

	expectLapsed(t, cloud, holder, qr)
	paired.Send(map[string]any{"cmd": "ping"})
	if _, err := paired.Next(); err != nil {
		t.Fatalf("the paired phone was cut off when the app quit: %v", err)
	}
	resumed, err := phone.Dial(spent)
	if err != nil {
		t.Fatalf("the paired phone's later dial, with no token: %v", err)
	}
	if err := resumed.Resume(spent); err != nil {
		t.Fatal(err)
	}

	back := s.App()
	listed := testworld.Request[protocol.PhonesUpdatedMessage](back, protocol.PhonesGetMessage{Cmd: protocol.CmdPhonesGet}, protocol.EventPhonesUpdated, nil)
	if len(listed.Offers) != 0 {
		t.Fatalf("the app came back to live codes: %+v", listed.Offers)
	}
	if _, kept := pairingOf(listed, phone.DeviceID()); !kept {
		t.Fatalf("the app came back without the paired phone: %+v", listed.Pairings)
	}
}

func TestAClientLeavingEndsOnlyThePairingCodesItStillHolds(t *testing.T) {
	t.Parallel()
	cloud, s, leaving := startPairingStack(t)
	profile := leaving.SelectedProfile()
	work := addProfile(t, leaving, "Work")
	staying := s.App()

	replaced := openWindow(t, leaving, profile, "pair-replaced")
	replacedHolder := holdSocket(t, cloud, replaced)
	workQR := openWindow(t, leaving, work.ID, "pair-work")
	workHolder := holdSocket(t, cloud, workQR)
	kept := openWindow(t, staying, profile, "pair-kept")
	if code := replacedHolder.AwaitClosed(); code != relayRefused {
		t.Fatalf("the replaced code's socket closed with %d, want the relay's %d", code, relayRefused)
	}

	leaving.Close()
	expectLapsed(t, cloud, workHolder, workQR)

	socket, err := testworld.NewPhone(t, cloud, "Test iPhone").DialToPair(kept)
	if err != nil {
		t.Fatalf("the code another client holds closed with the client that left: %v", err)
	}
	if err := socket.Pair(kept); err != nil {
		t.Fatal(err)
	}
	socket.Hello()

	third := openWindow(t, staying, work.ID, "pair-third")
	thirdHolder := holdSocket(t, cloud, third)
	staying.Close()
	expectLapsed(t, cloud, thirdHolder, third)
}

func TestAPhoneWithoutARelayTokenIsToldToUpdate(t *testing.T) {
	t.Parallel()
	cloud, app := startPairingHome(t)
	qr := openWindow(t, app, app.SelectedProfile(), "pair")
	_, err := testworld.NewPhone(t, cloud, "Old phone").Dial(qr)
	if !errors.Is(err, testworld.ErrNotAdmitted) || !strings.Contains(err.Error(), "update the attn app") {
		t.Fatalf("dial with no token = %v, want the relay's 4403 naming the update", err)
	}
}

func TestDeletingAProfileClosesItsPairingWindow(t *testing.T) {
	t.Parallel()
	cloud, app := startPairingHome(t)
	work := addProfile(t, app, "Work")
	qr := openWindow(t, app, work.ID, "pair")
	socket := holdSocket(t, cloud, qr)
	deleted := testworld.Request(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, ProfileID: work.ID, ExpectedRevision: int(work.Revision), RequestID: "delete"},
		protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "delete" })
	if deleted.Error != nil {
		t.Fatalf("profile_delete: %s", *deleted.Error)
	}
	expectLapsed(t, cloud, socket, qr)
}
