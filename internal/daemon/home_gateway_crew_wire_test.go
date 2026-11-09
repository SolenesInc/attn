package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/testworld"
)

func (p *testPhone) sessionsFrameUntil(match func(homegateway.HomeGatewaySessions) bool) homegateway.HomeGatewaySessions {
	p.t.Helper()
	for {
		var message homegateway.HomeGatewaySessions
		p.next("sessions", &message)
		if match(message) {
			return message
		}
	}
}

func (p *testPhone) startCrew(requestID, member string) {
	p.t.Helper()
	p.send(homegateway.HomeGatewayCrewWake{Cmd: "crew_wake", RequestID: requestID, CrewMember: member})
}

func betweenSessionsNames(frame homegateway.HomeGatewaySessions) []string {
	names := []string{}
	for _, member := range frame.AsleepCrew {
		names = append(names, member.CrewMemberName)
	}
	slices.Sort(names)
	return names
}

func sessionOfMember(frame homegateway.HomeGatewaySessions, member string) (homegateway.HomeGatewaySession, bool) {
	for _, session := range frame.Sessions {
		if protocol.Deref(session.CrewMember) == member {
			return session, true
		}
	}
	return homegateway.HomeGatewaySession{}, false
}

func TestHomeGatewayNamesACrewSessionAndTheChiefAndNoOrdinarySession(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	chiefMember := profileChief(t, w)
	ordinary := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(ordinary)
	startConfiguredChief(t, w, cli)
	chief := string(chiefSession(t, cli))
	keel := startCrew(t, cli, "keel", "")
	w.Launched(string(keel.SessionID))

	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.hello(homegateway.APIVersion)
	var welcome homegateway.HomeGatewayWelcome
	phone.next("welcome", &welcome)
	if welcome.API != homegateway.APIVersion {
		t.Fatalf("welcome api = %d, want %d", welcome.API, homegateway.APIVersion)
	}
	frame := phone.sessionsFrameUntil(func(homegateway.HomeGatewaySessions) bool { return true })

	bySession := map[string]homegateway.HomeGatewaySession{}
	for _, session := range frame.Sessions {
		bySession[session.ID] = session
	}
	plain := bySession[ordinary]
	if plain.ID == "" || plain.CrewMember != nil || plain.CrewMemberName != nil || plain.Chief != nil {
		t.Errorf("the ordinary session = %+v, want it listed with no crew data", plain)
	}
	if got := bySession[chief]; protocol.Deref(got.CrewMember) != chiefMember.ID || protocol.Deref(got.CrewMemberName) != chiefMember.Name || !protocol.Deref(got.Chief) {
		t.Errorf("the Chief's session = %+v, want crew member %s named %s and chief", got, chiefMember.ID, chiefMember.Name)
	}
	if got := bySession[string(keel.SessionID)]; protocol.Deref(got.CrewMember) != keel.Member || protocol.Deref(got.CrewMemberName) != keel.Name || got.Chief != nil {
		t.Errorf("Keel's session = %+v, want crew member %s named %s and not chief", got, keel.Member, keel.Name)
	}
	if got := betweenSessionsNames(frame); !slices.Equal(got, []string{"Alder", "Trellis"}) {
		t.Errorf("asleep crew = %v, want Alder and Trellis, the members with no session", got)
	}
}

func TestHomeGatewayListsAnAsleepChiefAndKeepsTheOtherProfilesCrewOut(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "phone_access_enabled", "true")
	chiefMember := profileChief(t, w)
	work := createProfile(app, "Work")

	workPhone := connectPhone(t, w, "work-phone", work.ID)
	workPhone.hello(homegateway.APIVersion)
	workFrame := workPhone.sessionsFrameUntil(func(homegateway.HomeGatewaySessions) bool { return true })
	for _, member := range workFrame.AsleepCrew {
		if member.CrewMember == chiefMember.ID || member.CrewMember == "keel" {
			t.Errorf("a phone of profile Work was shown %+v from the other profile", member)
		}
	}

	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.hello(homegateway.APIVersion)
	frame := phone.sessionsFrameUntil(func(homegateway.HomeGatewaySessions) bool { return true })
	if !slices.ContainsFunc(frame.AsleepCrew, func(member homegateway.HomeGatewayAsleepCrewMember) bool {
		return member.CrewMember == chiefMember.ID && protocol.Deref(member.Chief)
	}) {
		t.Errorf("asleep crew = %+v, want the asleep Chief %s marked chief", frame.AsleepCrew, chiefMember.ID)
	}
}

func TestAPhoneWakesAnAsleepMemberOnceAndHearsARefusalWithTheHomesReason(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.hello(homegateway.APIVersion)
	asleep := phone.sessionsFrameUntil(func(homegateway.HomeGatewaySessions) bool { return true })
	if got := betweenSessionsNames(asleep); !slices.Contains(got, "Keel") {
		t.Fatalf("asleep crew = %v, want Keel", got)
	}

	phone.startCrew("first", "keel")
	phone.startCrew("while-waking", "keel")
	woken := phone.sessionsFrameUntil(func(frame homegateway.HomeGatewaySessions) bool {
		_, awake := sessionOfMember(frame, "keel")
		return awake
	})
	session, _ := sessionOfMember(woken, "keel")
	if protocol.Deref(session.CrewMemberName) != "Keel" {
		t.Errorf("the woken session = %+v, want it named for Keel", session)
	}
	if slices.Contains(betweenSessionsNames(woken), "Keel") {
		t.Errorf("asleep crew = %v after Keel woke, want Keel gone from it", betweenSessionsNames(woken))
	}
	w.Launched(session.ID)

	answered := map[string]homegateway.HomeGatewayCrewWakeResult{}
	for len(answered) < 2 {
		var result homegateway.HomeGatewayCrewWakeResult
		phone.next("crew_wake_result", &result)
		answered[result.RequestID] = result
	}
	for id, result := range answered {
		if result.Error != nil {
			t.Errorf("wake %s = %+v, want success", id, result)
		}
	}

	phone.startCrew("while-awake", "keel")
	var awake homegateway.HomeGatewayCrewWakeResult
	phone.next("crew_wake_result", &awake)
	if awake.RequestID != "while-awake" || awake.Error != nil {
		t.Errorf("a wake of the awake Keel = %+v, want success", awake)
	}
	if got := crewSessionCount(t, cli); got != 1 {
		t.Errorf("three wakes of Keel left %d sessions, want its one", got)
	}

	phone.startCrew("refused", "stranger")
	var refused homegateway.HomeGatewayCrewWakeResult
	phone.next("crew_wake_result", &refused)
	if refused.RequestID != "refused" || refused.CrewMember != "stranger" || !strings.Contains(protocol.Deref(refused.Error), "stranger") {
		t.Errorf("a wake of an unknown member = %+v, want the home's reason naming stranger", refused)
	}
	if got := crewSessionCount(t, cli); got != 1 {
		t.Errorf("a refused wake left %d sessions, want 1", got)
	}
}

func TestAHomeRefusesAWakeOfAMemberOfAnotherProfile(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	work := createProfile(app, "Work")

	phone := connectPhone(t, w, "phone", work.ID)
	phone.hello(homegateway.APIVersion)
	phone.startCrew("other-profile", "keel")
	var refused homegateway.HomeGatewayCrewWakeResult
	phone.next("crew_wake_result", &refused)
	if refused.Error == nil {
		t.Errorf("a phone of profile Work woke Keel of the other profile: %+v", refused)
	}
	if got := crewSessionCount(t, cli); got != 0 {
		t.Errorf("a refused wake left %d sessions, want none", got)
	}
}

func TestAHomeServesNoWakeToAPhoneOfAnOlderAPI(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.hello(7)
	phone.startCrew("too-old", "keel")
	phone.send(homegateway.HomeGatewayPing{Cmd: "ping"})
	var pong homegateway.HomeGatewayPong
	phone.next("pong", &pong)
	if got := crewSessionCount(t, cli); got != 0 {
		t.Errorf("a wake from an api 7 phone left %d sessions, want none", got)
	}
}

func TestTheDesktopHearsThatAPhoneWokeTheMemberAndWhichPhone(t *testing.T) {
	cloud := testworld.StartCloud(t)
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	writeCrewHomes(t, w)
	t.Setenv(config.CloudURLEnv, cloud.HomeURL)
	w.start()
	app := w.App()
	setSetting(t, app, "phone_access_enabled", "true")
	testworld.Await(app, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool { return m.Link.State == protocol.PhoneLinkStateConnected })
	paired := testworld.Request(app, protocol.PhonePairBeginMessage{Cmd: protocol.CmdPhonePairBegin, RequestID: "pair", ProfileID: app.SelectedProfile()},
		protocol.EventPhonePairBeginResult, func(m protocol.PhonePairBeginResultMessage) bool { return m.RequestID == "pair" })
	if !paired.Success {
		t.Fatalf("phone_pair_begin: %s", protocol.Deref(paired.Error))
	}
	qr := testworld.ParsePhoneQR(t, paired.PairingCode.Qr)
	socket, err := testworld.NewPhone(t, cloud, "Victor's iPhone").DialToPair(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()

	socket.Send(homegateway.HomeGatewayCrewWake{Cmd: "crew_wake", RequestID: "wake", CrewMember: "keel"})
	arrival := testworld.Await(app, protocol.EventBackgroundLaunch, func(m protocol.BackgroundLaunchMessage) bool { return m.Name == "Keel" })
	w.Launched(string(arrival.SessionID))
	if arrival.RequestedBy != "Victor's iPhone" {
		t.Errorf("the desktop heard Keel's wake was requested by %q, want the phone that asked, Victor's iPhone", arrival.RequestedBy)
	}
}
