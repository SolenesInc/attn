package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const onboardingReference = "attn skill --reference onboarding"

func onboardingPending(t *testing.T, cli *client.Client, action protocol.OnboardingAction, want protocol.OnboardingPath) *protocol.OnboardingResult {
	t.Helper()
	result, err := cli.Onboarding(action)
	if err != nil || protocol.Deref(result.Pending) != want {
		t.Fatalf("onboarding %s = %+v, %v; want pending %q", action, result, err, want)
	}
	return result
}

func TestANewUsersChiefRunsTheFullPathUntilItSaysDoneOnBothHarnesses(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			w := newWorld(t, harness)
			cli := w.Client()
			profileChief(t, w)
			if onboardingPending(t, cli, protocol.OnboardingActionShow, protocol.OnboardingPathFull).Configured {
				t.Fatal("a fresh Chief reports a harness")
			}
			setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr(string(harness))})
			first := startCrew(t, cli, "chief", "")
			run := w.Launched(string(first.SessionID))
			guidance := launchGuidance(t, w, run)
			for _, want := range []string{onboardingReference, "open with the full path", "attn onboarding done"} {
				if !strings.Contains(guidance, want) {
					t.Fatalf("the first day's launch lacks %q: %s", want, guidance)
				}
			}

			run.Prompted()
			run.Reply("Ready. <!-- attn:state=idle -->")
			crewHandoff(t, cli, string(first.SessionID), "Roles are set; the Notebook and the tour remain.", false)
			second := startCrew(t, cli, "chief", "")
			if got := launchGuidance(t, w, w.Launched(string(second.SessionID))); !strings.Contains(got, onboardingReference) {
				t.Fatalf("onboarding ended with the first letter: %s", got)
			}

			onboardingPending(t, cli, protocol.OnboardingActionDone, "")
			if got := crewPriming(t, cli, string(second.SessionID)); !strings.Contains(got, "You are the Chief") || strings.Contains(got, onboardingReference) {
				t.Fatalf("the Chief's priming after done should drop onboarding: %s", got)
			}
			onboardingPending(t, cli, protocol.OnboardingActionAgain, protocol.OnboardingPathFull)
			if got := crewPriming(t, cli, string(second.SessionID)); !strings.Contains(got, "open with the full path") {
				t.Fatalf("again did not bring the full path back: %s", got)
			}
			w.restart()
			onboardingPending(t, w.Client(), protocol.OnboardingActionShow, protocol.OnboardingPathFull)
			onboardingPending(t, w.Client(), protocol.OnboardingActionDone, "")
			w.restart()
			onboardingPending(t, w.Client(), protocol.OnboardingActionShow, "")
		})
	}
}

func TestAnExistingUsersChiefTakesTheTourAndNamesItsPredecessor(t *testing.T) {
	w, old := upgradedFromTodaysChief(t, t.TempDir(), store.LaunchIntent{})
	app, cli := w.App(), w.Client()
	onboardingPending(t, cli, protocol.OnboardingActionShow, protocol.OnboardingPathTour)
	woken := startCrew(t, cli, "chief", "")
	run := w.Launched(string(woken.SessionID))
	guidance := launchGuidance(t, w, run)
	for _, want := range []string{onboardingReference, "open with the tour", "attn agent msg " + old} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("the tour's launch lacks %q: %s", want, guidance)
		}
	}
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	crewHandoff(t, cli, string(woken.SessionID), "Met the old session; the tour continues.", false)
	next := launchGuidance(t, w, w.Launched(string(startCrew(t, cli, "chief", "").SessionID)))
	if !strings.Contains(next, "open with the tour") || strings.Contains(next, old) {
		t.Fatalf("the second day should keep the tour and drop the old session: %s", next)
	}

	work := createProfile(app, "Work")
	if result, err := cli.WithGardenProfile(app.SelectedProfile(), "").Onboarding(protocol.OnboardingActionDone); err != nil || result.Pending != nil {
		t.Fatalf("done in the default profile = %+v, %v", result, err)
	}
	workPending, err := cli.WithGardenProfile(work.ID, "").Onboarding(protocol.OnboardingActionShow)
	if err != nil || protocol.Deref(workPending.Pending) != protocol.OnboardingPathTour {
		t.Fatalf("a new profile in an install already in use = %+v, %v; want the tour, untouched by the other profile's done", workPending, err)
	}
}
