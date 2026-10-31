package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/victorarias/attn/internal/protocol"
)

const onboardingUsage = "usage: attn onboarding [done|again] [--profile <name|id>] [--json]"

func runOnboarding() {
	args := os.Args[2:]
	if hasHelpFlag(args) {
		fmt.Println(onboardingUsage + `

With no action, show whether the Chief's onboarding is pending, and which
path it takes: the full path for a new user, or the tour for a user who used
attn before the Chief.
  done   mark onboarding finished
  again  start the full path again in the Chief's next session

again doesn't wake the Chief. An awake Chief can run it, then start the full
path in its current conversation.`)
		return
	}
	fs := flag.NewFlagSet("onboarding", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := crewProfileFlag(fs)
	jsonOut := fs.Bool("json", false, "print JSON")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	action := protocol.OnboardingActionShow
	if len(positionals) == 1 {
		action = protocol.OnboardingAction(positionals[0])
	}
	if err != nil || len(positionals) > 1 || (action != protocol.OnboardingActionShow && action != protocol.OnboardingActionDone && action != protocol.OnboardingActionAgain) {
		fmt.Fprintln(os.Stderr, onboardingUsage)
		os.Exit(2)
	}
	response, err := crewClient(*profile).Onboarding(action)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *jsonOut {
		printJSON(response)
		return
	}
	switch pending := protocol.Deref(response.Pending); {
	case action == protocol.OnboardingActionDone:
		fmt.Printf("%s's onboarding is done. `attn onboarding again` runs the full path again.\n", response.Chief)
	case action == protocol.OnboardingActionAgain:
		fmt.Printf("%s's onboarding is pending again, on the full path. The Chief's next session opens with it; an awake Chief can start it now.\n", response.Chief)
	case pending == "":
		fmt.Printf("%s has no onboarding pending. `attn onboarding again` runs the full path again.\n", response.Chief)
	case pending == protocol.OnboardingPathTour:
		fmt.Printf("%s's onboarding is pending. It takes the tour, because the user used attn before the Chief.\n", response.Chief)
	default:
		fmt.Printf("%s's onboarding is pending. It takes the full path.\n", response.Chief)
	}
	if !response.Configured {
		fmt.Println("The Chief has no harness yet. Pick its harness and model in the sidebar, or run `attn crew set chief --agent <harness> --model <model>`.")
	}
}
