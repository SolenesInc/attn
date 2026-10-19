package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func onboarding(t *testing.T, cli *client.Client, action protocol.OnboardingAction, want protocol.FirstDay) *protocol.OnboardingResult {
	t.Helper()
	result, err := cli.Onboarding(action, "", "")
	if err != nil || result == nil || result.FirstDay != want || result.Onboarded != (want == protocol.FirstDayNone) {
		t.Fatalf("onboarding %s = %+v, %v; want %s", action, result, err, want)
	}
	return result
}

func TestChiefOnboardingFullDoneAndAgainReachBothHarnesses(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			w := newWorld(t, harness)
			cli := w.Client()
			if onboarding(t, cli, protocol.OnboardingActionShow, protocol.FirstDayFull).Configured {
				t.Fatal("fresh Chief is configured")
			}
			registerSessions(t, w, cli, "opened-before-setup")
			id := configureChief(t, w, harness, chiefModel(harness))
			run := w.Launched(id)
			for _, want := range []string{"attn skill --reference onboarding", "Open with the full path."} {
				if got := launchGuidance(t, run); !strings.Contains(got, want) {
					t.Fatalf("launch lacks %q: %s", want, got)
				}
			}
			run.Prompted()
			run.Reply("Ready. <!-- attn:state=idle -->")
			onboarding(t, cli, protocol.OnboardingActionDone, protocol.FirstDayNone)
			if got := crewPriming(t, cli, id); strings.Contains(got, "attn skill --reference onboarding") {
				t.Fatalf("done remains in priming: %s", got)
			}
			if _, err := cli.CrewHandoff(protocol.SessionID(id), "Onboarding is done.", false, protocol.CrewDayCloseSleep); err != nil {
				t.Fatal(err)
			}
			next, err := cli.CrewWake("chief", "", "")
			if err != nil {
				t.Fatal(err)
			}
			ordinary := w.Launched(string(next.SessionID))
			if got := launchGuidance(t, ordinary); strings.Contains(got, "attn skill --reference onboarding") {
				t.Fatalf("ordinary wake has onboarding: %s", got)
			}
			onboarding(t, cli, protocol.OnboardingActionAgain, protocol.FirstDayFull)
			if got := crewPriming(t, cli, string(next.SessionID)); !strings.Contains(got, "Open with the full path.") {
				t.Fatalf("again absent: %s", got)
			}
			w.restart()
			onboarding(t, w.Client(), protocol.OnboardingActionShow, protocol.FirstDayFull)
			onboarding(t, w.Client(), protocol.OnboardingActionDone, protocol.FirstDayNone)
			w.restart()
			onboarding(t, w.Client(), protocol.OnboardingActionShow, protocol.FirstDayNone)
		})
	}
}

func TestAnEstablishedProfileKeepsItsTourAsOldSessionsChange(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	db, err := store.OpenDBAtSchemaVersion(filepath.Join(w.Dir, "attn.db"), 1792359946029014-1)
	if err != nil {
		t.Fatal(err)
	}
	var profile string
	if err := db.QueryRow("SELECT id FROM profiles WHERE deleted_at='' LIMIT 1").Scan(&profile); err != nil {
		t.Fatal(err)
	}
	stamp := "2026-10-10T10:00:00Z"
	_, err = db.Exec("INSERT INTO sessions(id,label,directory,state,state_since,state_updated_at,last_seen,agent,profile_id) VALUES('legacy','Existing work',?,'idle',?,?,?,'claude',?)", w.Dir, stamp, stamp, stamp, profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w.start()
	cli := w.Client()
	onboarding(t, cli, protocol.OnboardingActionShow, protocol.FirstDayTour)
	id := configureChief(t, w, fakeagent.Claude, "sonnet")
	if got := launchGuidance(t, w.Launched(id)); !strings.Contains(got, "open with the short tour") {
		t.Fatalf("tour launch: %s", got)
	}
	registerSessions(t, w, cli, "legacy")
	onboarding(t, cli, protocol.OnboardingActionShow, protocol.FirstDayTour)
	if _, err := cli.AgentClose("legacy", protocol.SessionID(id), "Existing work finished"); err != nil {
		t.Fatal(err)
	}
	w.restart()
	onboarding(t, w.Client(), protocol.OnboardingActionShow, protocol.FirstDayTour)
	onboarding(t, w.Client(), protocol.OnboardingActionAgain, protocol.FirstDayFull)
	w.restart()
	onboarding(t, w.Client(), protocol.OnboardingActionShow, protocol.FirstDayFull)
}

func TestUpgradedChiefSkipsOnboarding(t *testing.T) {
	w := prepareChiefUpgrade(t, false, false)
	onboarding(t, w.Client(), protocol.OnboardingActionShow, protocol.FirstDayNone)
	result, err := w.Client().CrewWake("chief", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := launchGuidance(t, w.Launched(string(result.SessionID))); strings.Contains(got, "attn skill --reference onboarding") {
		t.Fatalf("upgraded Chief got onboarding: %s", got)
	}
}

func TestOnlyThePendingChiefCanShowCalloutsWithTheToggleOff(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "callouts_enabled", "false")
	id := configureChief(t, w, fakeagent.Claude, "sonnet")
	target := calloutAppPeer(w.World, "tauri://localhost", browserHostToken)
	selectProfile(target, app.SelectedProfile())
	answered := make(chan error, 1)
	go func() {
		_, err := cli.CalloutShow(protocol.SessionID(id), []protocol.CalloutShowStep{{Anchor: "garden", Text: "Your work"}, {Anchor: "commands", Text: "Find commands here"}})
		answered <- err
	}()
	request := testworld.Await[protocol.CalloutShowRequestMessage](target, protocol.EventCalloutShowRequest, nil)
	if len(request.Steps) != 2 {
		t.Fatalf("tour steps: %+v", request)
	}
	target.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: request.RequestID})
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	onboarding(t, cli, protocol.OnboardingActionDone, protocol.FirstDayNone)
	_, err := cli.CalloutShow(protocol.SessionID(id), []protocol.CalloutShowStep{{Anchor: "garden", Text: "Your work"}})
	calloutRefused(t, err, "Settings > Experimental > Agent callouts")
	registerSessions(t, w, cli, "other")
	onboarding(t, cli, protocol.OnboardingActionAgain, protocol.FirstDayFull)
	_, err = cli.CalloutAnchors("other")
	calloutRefused(t, err, "Settings > Experimental > Agent callouts")
}

func TestOnboardingIsScopedToTheRequestingProfile(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	other := createProfile(app, "Other")
	if _, err := cli.Onboarding(protocol.OnboardingActionDone, "", other.ID); err != nil {
		t.Fatal(err)
	}
	own, err := cli.Onboarding(protocol.OnboardingActionShow, "", app.SelectedProfile())
	if err != nil || own.FirstDay != protocol.FirstDayFull {
		t.Fatalf("other profile changed own onboarding: %+v %v", own, err)
	}
	if _, err := cli.Onboarding(protocol.OnboardingActionShow, "", "missing-profile"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}
