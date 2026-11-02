package daemon_test

import (
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
)

type testPhone struct {
	t         *testing.T
	toHome    chan []byte
	fromHome  chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	served    chan error
}

type phoneLink struct{ *testPhone }

func (l phoneLink) Recv() ([]byte, error) {
	select {
	case frame := <-l.toHome:
		return frame, nil
	case <-l.closed:
		return nil, io.EOF
	}
}

func (l phoneLink) Send(frame []byte) error {
	select {
	case l.fromHome <- frame:
		return nil
	case <-l.closed:
		return io.ErrClosedPipe
	}
}

func (l phoneLink) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func connectPhone(t *testing.T, w *world, deviceID, profileID string) *testPhone {
	t.Helper()
	p := &testPhone{t: t, toHome: make(chan []byte), fromHome: make(chan []byte, 64), closed: make(chan struct{}), served: make(chan error, 1)}
	go func() {
		p.served <- w.daemon.ServeGateway(daemon.GatewayDevice{DeviceID: deviceID, ProfileID: profileID}, phoneLink{p})
	}()
	t.Cleanup(func() {
		phoneLink{p}.Close()
		<-p.served
	})
	return p
}

func (p *testPhone) hello(api int) {
	p.t.Helper()
	frame, _ := json.Marshal(gateway.GatewayHello{Cmd: "hello", API: api, DeviceID: "phone"})
	select {
	case p.toHome <- frame:
	case <-p.closed:
		p.t.Fatal("the home closed the link before hello")
	}
}

func (p *testPhone) next(event string, out any) {
	p.t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	select {
	case frame := <-p.fromHome:
		var head struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(frame, &head); err != nil || head.Event != event {
			p.t.Fatalf("the home sent %s, want a %s event", frame, event)
		}
		if err := json.Unmarshal(frame, out); err != nil {
			p.t.Fatalf("decode %s: %v", frame, err)
		}
	case <-guard.C:
		p.t.Fatalf("still awaiting a %s event after fakeagent.HangGuard=%s", event, fakeagent.HangGuard)
	}
}

func (p *testPhone) sessions() []gateway.GatewaySession {
	p.t.Helper()
	var message gateway.GatewaySessions
	p.next("sessions", &message)
	return message.Sessions
}

func (p *testPhone) refusedWith(message string) {
	p.t.Helper()
	var refused gateway.GatewayRefused
	p.next("refused", &refused)
	if refused.Message != message {
		p.t.Fatalf("refused with %q, want %q", refused.Message, message)
	}
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	select {
	case <-p.closed:
	case <-guard.C:
		p.t.Fatalf("the home kept the link open after refusing it")
	}
}

func gatewaySessionIDs(sessions []gateway.GatewaySession) []string {
	ids := []string{}
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

const phoneAccessOff = "phone access is off on this home; turn it on in Settings → Experimental → Phone access"

func TestGatewayServesOnlyThePairedProfilesSessionsAndKeepsThemCurrent(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	personal := app.SelectedProfile()
	work := createProfile(app, "Work")
	for _, s := range []struct{ id, profile string }{{"mine", personal}, {"theirs", work.ID}} {
		if err := w.InjectSession(s.id, s.id+" label", w.Path(s.id), protocol.SessionAgentClaude, s.profile); err != nil {
			t.Fatal(err)
		}
	}

	phone := connectPhone(t, w, "phone-a", personal)
	phone.hello(99)
	var welcome gateway.GatewayWelcome
	phone.next("welcome", &welcome)
	if welcome.API != gateway.APIVersion || welcome.Profile.ID != personal || welcome.HomeName == "" {
		t.Fatalf("welcome = %+v, want api %d for profile %s from a named home", welcome, gateway.APIVersion, personal)
	}
	initial := phone.sessions()
	if len(initial) != 1 || initial[0].ID != "mine" || initial[0].Title != "mine label" || initial[0].State != gateway.GatewaySessionStateLaunching || initial[0].Offline {
		t.Fatalf("sessions = %+v, want only the launching session mine", initial)
	}

	if err := cli.UpdateState(protocol.TerminalID(w.Terminal("theirs")), protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	if err := cli.UpdateState(protocol.TerminalID(w.Terminal("mine")), protocol.StatePendingApproval); err != nil {
		t.Fatal(err)
	}
	waiting := phone.sessions()
	if len(waiting) != 1 || waiting[0].State != gateway.GatewaySessionStateNeedsApproval || waiting[0].WaitingSince == nil {
		t.Fatalf("sessions after mine asked for approval = %+v, want mine alone, needs_approval, with waiting_since", waiting)
	}

	if err := w.InjectSession("theirs-too", "theirs too", w.Path("theirs-too"), protocol.SessionAgentClaude, work.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.InjectSession("mine-too", "mine too", w.Path("mine-too"), protocol.SessionAgentCodex, personal); err != nil {
		t.Fatal(err)
	}
	if ids := gatewaySessionIDs(phone.sessions()); len(ids) != 2 || ids[0] != "mine" || ids[1] != "mine-too" {
		t.Fatalf("sessions after both profiles gained one = %v, want mine and mine-too", ids)
	}

	other := connectPhone(t, w, "phone-b", work.ID)
	other.hello(gateway.APIVersion)
	other.next("welcome", &welcome)
	if welcome.API != gateway.APIVersion || welcome.Profile.ID != work.ID || welcome.Profile.Name != "Work" {
		t.Fatalf("welcome for the work pairing = %+v", welcome)
	}
	if ids := gatewaySessionIDs(other.sessions()); len(ids) != 2 || ids[0] != "theirs" || ids[1] != "theirs-too" {
		t.Fatalf("the work pairing sees %v, want theirs and theirs-too", ids)
	}
}

func TestGatewayRefusesAHelloItCannotServe(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	profile := app.SelectedProfile()

	phone := connectPhone(t, w, "phone", profile)
	phone.hello(gateway.APIVersion)
	phone.refusedWith(phoneAccessOff)

	setSetting(t, app, "phone_access_enabled", "true")
	tooOld := connectPhone(t, w, "phone", profile)
	tooOld.hello(0)
	tooOld.refusedWith("hello asked for api 0; this home serves api 1 to 1")

	gone := connectPhone(t, w, "phone", "deleted-profile")
	gone.hello(gateway.APIVersion)
	gone.refusedWith("profile deleted-profile is gone from this home; pair the phone again")
}

func TestGatewayDropsPhonesWhenPhoneAccessTurnsOff(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	setSetting(t, app, "phone_access_enabled", "true")
	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.hello(gateway.APIVersion)
	var welcome gateway.GatewayWelcome
	phone.next("welcome", &welcome)
	phone.sessions()

	setSetting(t, app, "phone_access_enabled", "false")
	phone.refusedWith(phoneAccessOff)

	again := connectPhone(t, w, "phone", app.SelectedProfile())
	again.hello(gateway.APIVersion)
	again.refusedWith(phoneAccessOff)
}

func TestGatewayLetsTheDaemonStopWhileAPhoneHasNotSaidHello(t *testing.T) {
	w := newWorld(t)
	phone := connectPhone(t, w, "phone", w.App().SelectedProfile())

	w.stop()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	select {
	case err := <-phone.served:
		phone.served <- err
	case <-guard.C:
		t.Fatalf("the gateway kept serving %s after the daemon stopped", fakeagent.HangGuard)
	}
}
