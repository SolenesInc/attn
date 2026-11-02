package daemon_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
	"github.com/victorarias/attn/internal/testworld"
)

type testPhone struct {
	t         *testing.T
	toHome    chan []byte
	fromHome  chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	served    chan error
	asks      map[string]gateway.GatewayAsk
	askIDs    []string
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
	p := &testPhone{t: t, toHome: make(chan []byte), fromHome: make(chan []byte, 64), closed: make(chan struct{}), served: make(chan error, 1), asks: map[string]gateway.GatewayAsk{}}
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

func (p *testPhone) send(message any) {
	p.t.Helper()
	frame, _ := json.Marshal(message)
	select {
	case p.toHome <- frame:
	case <-p.closed:
		p.t.Fatalf("the home closed the link before %s", frame)
	}
}

// next skips events until one of kind event arrives, keeping the asks it passes as a phone does.
func (p *testPhone) next(event string, out any) {
	p.t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	for {
		frame := p.receive(guard, event)
		var head struct {
			Event string `json:"event"`
		}
		_ = json.Unmarshal(frame, &head)
		if head.Event != event {
			continue
		}
		if err := json.Unmarshal(frame, out); err != nil {
			p.t.Fatalf("decode %s: %v", frame, err)
		}
		return
	}
}

func (p *testPhone) receive(guard *time.Timer, awaiting string) []byte {
	p.t.Helper()
	select {
	case frame := <-p.fromHome:
		var head struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(frame, &head); err != nil {
			p.t.Fatalf("the home sent %s, which is not a gateway event", frame)
		}
		if len(frame) > cloudlink.MaxMessageBytes {
			p.t.Fatalf("the home sent a %s event of %d bytes; a relay frame carries at most cloudlink.MaxMessageBytes=%d", head.Event, len(frame), cloudlink.MaxMessageBytes)
		}
		switch head.Event {
		case "ask":
			var message gateway.GatewayAskEvent
			if err := json.Unmarshal(frame, &message); err != nil {
				p.t.Fatalf("decode %s: %v", frame, err)
			}
			p.asks[message.Ask.ID] = message.Ask
		case "asks":
			var message gateway.GatewayAsks
			if err := json.Unmarshal(frame, &message); err != nil {
				p.t.Fatalf("decode %s: %v", frame, err)
			}
			p.askIDs = message.Ids
		}
		return frame
	case <-guard.C:
		p.t.Fatalf("still awaiting %s after fakeagent.HangGuard=%s; the phone shows asks %v", awaiting, fakeagent.HangGuard, p.askIDs)
		return nil
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
	tooOld.refusedWith(fmt.Sprintf("hello asked for api 0; this home serves api 1 to %d", gateway.APIVersion))

	gone := connectPhone(t, w, "phone", "deleted-profile")
	gone.hello(gateway.APIVersion)
	gone.refusedWith("profile deleted-profile is gone from this home; pair the phone again")
}

func TestGatewayAnswersEachPingSoAPhoneKnowsItsHomeIsAwake(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	profile := app.SelectedProfile()
	if err := w.InjectSession("mine", "mine label", w.Path("mine"), protocol.SessionAgentClaude, profile); err != nil {
		t.Fatal(err)
	}
	phone := connectPhone(t, w, "phone", profile)
	phone.hello(gateway.APIVersion)
	var welcome gateway.GatewayWelcome
	phone.next("welcome", &welcome)
	if welcome.API < 2 {
		t.Fatalf("welcome api = %d; a phone pings only a home that serves api 2 or later", welcome.API)
	}
	phone.sessions()

	var pong gateway.GatewayPong
	for range 2 {
		phone.send(gateway.GatewayPing{Cmd: "ping"})
		phone.next("pong", &pong)
	}
	if err := cli.UpdateState(protocol.TerminalID(w.Terminal("mine")), protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	if got := phone.sessions(); len(got) != 1 || got[0].State != gateway.GatewaySessionStateWorking {
		t.Fatalf("sessions after pings = %+v, want mine working", got)
	}
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

// showsAsks waits until the asks the phone shows match, and returns them.
func (p *testPhone) showsAsks(match func([]gateway.GatewayAsk) bool) []gateway.GatewayAsk {
	p.t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	for {
		if p.askIDs != nil {
			asks := []gateway.GatewayAsk{}
			for _, id := range p.askIDs {
				asks = append(asks, p.asks[id])
			}
			if match(asks) {
				return asks
			}
		}
		p.receive(guard, "matching asks")
	}
}

func (p *testPhone) askList() []gateway.GatewayAsk {
	p.t.Helper()
	return p.showsAsks(func([]gateway.GatewayAsk) bool { return true })
}

func (p *testPhone) welcomed() {
	p.t.Helper()
	p.hello(gateway.APIVersion)
	var welcome gateway.GatewayWelcome
	p.next("welcome", &welcome)
}

func (p *testPhone) answer(askID string, answers []gateway.GatewayAskAnswer, note string) gateway.GatewayAskAnswerResult {
	p.t.Helper()
	requestID := uuid.NewString()
	p.send(gateway.GatewayAskAnswerCommand{Cmd: "ask_answer", RequestID: requestID, AskID: askID, Answers: answers, Note: &note})
	var result gateway.GatewayAskAnswerResult
	p.next("ask_answer_result", &result)
	if result.RequestID != requestID {
		p.t.Fatalf("answer result %+v, want request %s", result, requestID)
	}
	return result
}

func TestGatewayShowsThePairedProfilesAsksAndAnswersThemAsTheDesktopDoes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	setSetting(t, app, "asks_enabled", "true")
	home := app.SelectedProfile()
	side := createProfile(app, "Side")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(asker)
	sideAsker := w.Spawn(w.AppOn(side.ID), fakeagent.Claude, w.Path("side"))
	w.Launched(sideAsker)
	sideAsk, err := cli.AskOpen(protocol.SessionID(sideAsker), "", cacheAsk, "", w.Path("side"))
	if err != nil {
		t.Fatalf("side ask: %v", err)
	}

	phone := connectPhone(t, w, "phone", home)
	phone.welcomed()
	if asks := phone.askList(); len(asks) != 0 {
		t.Fatalf("the phone starts with %+v, want no asks: the only one belongs to another profile", asks)
	}

	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	asks := phone.showsAsks(func(asks []gateway.GatewayAsk) bool { return len(asks) == 1 })
	if asks[0].ID != asked.ID || asks[0].SessionID != asker || asks[0].Subject != "Which store should keep the cache?" || asks[0].Reply != nil {
		t.Fatalf("the phone shows %+v, want the open ask %s from %s", asks, asked.ID, asker)
	}
	parts := asks[0].Parts
	if len(parts) != 2 || parts[0].Markdown == nil || parts[1].Question == nil {
		t.Fatalf("the ask reads %+v, want the prose then the question", parts)
	}
	question := parts[1].Question
	if question.Kind != gateway.GatewayAskQuestionKindOne || len(question.Choices) != 2 || protocol.Deref(question.Recommended) != "Postgres" || question.Choices[0].Description == nil {
		t.Fatalf("the question reads %+v, want one of two described choices with Postgres recommended", question)
	}

	if crossed := phone.answer(sideAsk.ID, nil, "from the phone"); crossed.Error == nil {
		t.Fatal("the phone answered an ask of another profile")
	}
	if result := phone.answer(asked.ID, []gateway.GatewayAskAnswer{{Key: "q1", Picked: []string{"Postgres"}}}, "Keep it boring."); result.Error != nil {
		t.Fatalf("answering from the phone failed: %s", *result.Error)
	}
	replied := phone.showsAsks(func(asks []gateway.GatewayAsk) bool { return len(asks) == 1 && asks[0].Reply != nil })
	if !slices.Equal(replied[0].Reply.Answers[0].Picked, []string{"Postgres"}) || protocol.Deref(replied[0].Reply.Note) != "Keep it boring." || replied[0].Reply.AnsweredAt.IsZero() {
		t.Fatalf("after the answer the phone shows %+v, want the reply with Postgres and the note", replied)
	}
	if again := phone.answer(asked.ID, nil, "twice"); again.Error == nil || *again.Error != "this ask was already answered or withdrawn" {
		t.Fatalf("a second answer got %+v, want the desktop's refusal", again)
	}

	typed := agent.Prompted()
	for _, want := range []string{asked.ID, "Answer: Postgres (the recommended choice)", "> Keep it boring."} {
		if !strings.Contains(typed, want) {
			t.Errorf("the agent was typed %q, missing %q", typed, want)
		}
	}
	shown := testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 && m.Asks[0].Reply != nil })
	if protocol.Deref(shown.Asks[0].Reply.Note) != "Keep it boring." {
		t.Fatalf("the desktop shows the reply %+v, want the phone's", shown.Asks[0].Reply)
	}
}

func TestGatewayTellsThePhoneAnAskIsTooLargeForAFrame(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	long := strings.Repeat("A \"quoted\" line.\n", (64<<10-len(cacheAsk))/18) + cacheAsk
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", long, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask of %d bytes: %v", len(long), err)
	}

	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.welcomed()
	asks := phone.askList()
	if len(asks) != 1 || asks[0].ID != asked.ID || len(asks[0].Parts) != 0 || asks[0].TooLargeBytes == nil || asks[0].MaxFrameBytes == nil || *asks[0].TooLargeBytes <= *asks[0].MaxFrameBytes {
		t.Fatalf("the phone shows %+v, want the ask without parts, with its size past the frame's", asks)
	}
}

func TestGatewayKeepsAPhoneConnectedWhateverAnAnswersNoteHolds(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	angled, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "Angled", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}
	control, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "Control", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}
	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.welcomed()
	phone.showsAsks(func(asks []gateway.GatewayAsk) bool { return len(asks) == 2 })

	angledNote := strings.Repeat("<", 11_000)
	controlNote := strings.Repeat("\x01", 11_000)
	for id, note := range map[string]string{angled.ID: angledNote, control.ID: controlNote} {
		if result := phone.answer(id, nil, note); result.Error != nil {
			t.Fatalf("answering %s: %s", id, *result.Error)
		}
	}
	asks := phone.showsAsks(func(asks []gateway.GatewayAsk) bool {
		return len(asks) == 2 && asks[0].Reply != nil && asks[1].Reply != nil
	})
	byID := map[string]gateway.GatewayAsk{asks[0].ID: asks[0], asks[1].ID: asks[1]}
	if shown := byID[angled.ID]; shown.TooLargeBytes != nil || protocol.Deref(shown.Reply.Note) != angledNote {
		t.Fatalf("the ask answered with 11,000 '<' arrived as %+v, want it whole with its note", shown.TooLargeBytes)
	}
	if shown := byID[control.ID]; shown.TooLargeBytes == nil || len(shown.Parts) != 0 || shown.Reply.Note != nil || shown.Reply.AnsweredAt.IsZero() {
		t.Fatalf("the ask whose note escapes past a frame arrived as %+v, want it answered and too large, without parts or note", shown)
	}
	phone.send(gateway.GatewayPing{Cmd: "ping"})
	var pong gateway.GatewayPong
	phone.next("pong", &pong)
}

func TestGatewaySendsAnAsksImageInChunksAndSaysWhyOneCannotBeRead(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	setSetting(t, app, "asks_enabled", "true")
	side := createProfile(app, "Side")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	shot := make([]byte, 100_000)
	for i := range shot {
		shot[i] = byte(i * 7)
	}
	markdown := "![the board](shots/board.png) ![gone](gone.png) ![notes](notes.txt)\n\n" + cacheAsk
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", markdown, "", w.Path("shop"),
		protocol.AskImage{Path: w.Path("shop") + "/shots/board.png", DataBase64: base64.StdEncoding.EncodeToString(shot)})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.welcomed()
	phone.askList()
	read := func(phone *testPhone, src string, offset int) (gateway.GatewayAskImageChunk, *gateway.GatewayAskImageError) {
		return phone.readImage(asked.ID, src, offset)
	}

	var got []byte
	for offset := 0; ; {
		chunk, failed := read(phone, "shots/board.png", offset)
		if failed != nil {
			t.Fatalf("reading the board at %d: %s", offset, failed.Message)
		}
		data, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if err != nil || chunk.Offset != offset || chunk.Size != len(shot) || chunk.MimeType != "image/png" || len(data) == 0 {
			t.Fatalf("chunk at %d = offset %d, size %d, %s, %d bytes (%v)", offset, chunk.Offset, chunk.Size, chunk.MimeType, len(data), err)
		}
		got = append(got, data...)
		offset += len(data)
		if offset == chunk.Size {
			break
		}
	}
	if !bytes.Equal(got, shot) {
		t.Fatalf("the phone assembled %d bytes that differ from the %d-byte image", len(got), len(shot))
	}

	for _, c := range []struct{ src, want string }{
		{"gone.png", "holds no copy of " + w.Path("shop") + "/gone.png"},
		{"notes.txt", "notes.txt is not a local image"},
		{"https://example.com/board.png", "https://example.com/board.png is not a local image"},
	} {
		if _, failed := read(phone, c.src, 0); failed == nil || !strings.Contains(failed.Message, c.want) {
			t.Errorf("reading %s answered %+v, want an error containing %q", c.src, failed, c.want)
		}
	}

	stranger := connectPhone(t, w, "stranger", side.ID)
	stranger.welcomed()
	stranger.askList()
	for _, src := range []string{"shots/board.png", "notes.txt"} {
		if _, failed := read(stranger, src, 0); failed == nil || failed.Message != "ask "+asked.ID+" is gone" {
			t.Errorf("a phone paired to another profile reading %s of %s got %+v, want the ask to read as gone", src, asked.ID, failed)
		}
	}
}

func (p *testPhone) readImage(askID, src string, offset int) (gateway.GatewayAskImageChunk, *gateway.GatewayAskImageError) {
	p.t.Helper()
	requestID := uuid.NewString()
	p.send(gateway.GatewayAskImage{Cmd: "ask_image", RequestID: requestID, AskID: askID, Src: src, Offset: offset})
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	for {
		frame := p.receive(guard, src)
		var head struct {
			Event     string `json:"event"`
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(frame, &head)
		if head.RequestID != requestID {
			continue
		}
		var chunk gateway.GatewayAskImageChunk
		var failed gateway.GatewayAskImageError
		if head.Event == "ask_image_error" {
			_ = json.Unmarshal(frame, &failed)
			return chunk, &failed
		}
		if err := json.Unmarshal(frame, &chunk); err != nil || head.Event != "ask_image_chunk" {
			p.t.Fatalf("the home answered %s, want a chunk or an error", frame)
		}
		return chunk, nil
	}
}

func TestGatewayReadsTheHTMLImageAPhoneNames(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	shop := w.Path("shop")
	image := func(name, data string) protocol.AskImage {
		return protocol.AskImage{Path: shop + "/" + name, DataBase64: base64.StdEncoding.EncodeToString([]byte(data))}
	}
	open := func(markdown string, images ...protocol.AskImage) string {
		t.Helper()
		asked, err := cli.AskOpen(protocol.SessionID(asker), "", markdown+"\n\n"+cacheAsk, "", shop, images...)
		if err != nil {
			t.Fatalf("ask: %v", err)
		}
		return asked.ID
	}
	named := open(`<img src="caf&eacute;.png"> <img src="a&amp;copy;.png">`, image("café.png", "cafe"), image("a&copy;.png", "escaped copy"))
	colliding := open(`<img src="a&amp;copy;.png"> <img src="a&copy;.png">`, image("a©.png", "copyright sign"))

	type read struct{ askID, src, want, failure string }
	reads := func(phone *testPhone, cases []read) {
		t.Helper()
		for _, c := range cases {
			chunk, failed := phone.readImage(c.askID, c.src, 0)
			data, _ := base64.StdEncoding.DecodeString(chunk.DataBase64)
			switch {
			case c.failure != "" && (failed == nil || !strings.Contains(failed.Message, c.failure)):
				t.Errorf("reading %s returned %q and error %+v, want an error containing %q", c.src, data, failed, c.failure)
			case c.failure == "" && failed != nil:
				t.Errorf("reading %s: %s", c.src, failed.Message)
			case c.failure == "" && string(data) != c.want:
				t.Errorf("reading %s returned %q, want %q", c.src, data, c.want)
			}
		}
	}

	t.Run("a phone that decodes every reference", func(t *testing.T) {
		phone := connectPhone(t, w, "phone", app.SelectedProfile())
		phone.welcomed()
		phone.askList()
		reads(phone, []read{
			{named, "café.png", "cafe", ""},
			{named, "a&copy;.png", "escaped copy", ""},
			{colliding, "a&copy;.png", "", "holds no copy of " + shop + "/a&copy;.png"},
			{colliding, "a©.png", "copyright sign", ""},
		})
	})

	t.Run("an installed phone that leaves named references other than amp, lt, gt, quot and apos", func(t *testing.T) {
		phone := connectPhone(t, w, "installed", app.SelectedProfile())
		phone.hello(4)
		var welcome gateway.GatewayWelcome
		phone.next("welcome", &welcome)
		phone.askList()
		reads(phone, []read{
			{named, "caf&eacute;.png", "cafe", ""},
			{named, "a&copy;.png", "escaped copy", ""},
			{colliding, "a&copy;.png", "", "shows both " + shop + "/a&copy;.png and " + shop + "/a©.png"},
		})
	})
}
