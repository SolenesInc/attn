package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTheOnboardingToolsShowFinishAndRestartTheChiefsOnboarding(t *testing.T) {
	s := newSessionTools(t)
	for _, step := range []struct {
		tool string
		want protocol.OnboardingPath
	}{
		{"onboarding", protocol.OnboardingPathFull},
		{"onboarding_done", ""},
		{"onboarding_again", protocol.OnboardingPathFull},
	} {
		var result protocol.OnboardingResult
		structuredAs(t, s.shapes.call(step.tool, map[string]any{}), &result)
		if result.Chief == "" || protocol.Deref(result.Pending) != step.want {
			t.Errorf("%s answered %+v, want the Chief with %q pending", step.tool, result, step.want)
		}
	}
	s.shapes.finish("onboarding")
}

func TestAnAgentCannotChangeTheOnboardingOfAnotherProfile(t *testing.T) {
	p := newTwoProfileTools(t)
	cli := p.w.Client()
	before, err := cli.WithGardenProfile(p.side.ID, "").Onboarding(protocol.OnboardingActionShow)
	if err != nil {
		t.Fatal(err)
	}
	var onboarded protocol.OnboardingResult
	if err := cli.WithGardenProfile(p.side.ID, "").RunCommand("onboarding done", protocol.SessionID(p.here), struct{}{}, &onboarded); err == nil {
		t.Errorf("onboarding done naming the side profile from a session of the other answered %+v", onboarded)
	}
	p.mine.mustCall(t, "onboarding_done", map[string]any{})
	after, err := cli.WithGardenProfile(p.side.ID, "").Onboarding(protocol.OnboardingActionShow)
	if err != nil || protocol.Deref(after.Pending) != protocol.Deref(before.Pending) {
		t.Errorf("the side profile's onboarding went from %v to %+v, %v; want it untouched", protocol.Deref(before.Pending), after, err)
	}
}
