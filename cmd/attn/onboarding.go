package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func runOnboarding() {
	args := os.Args[2:]
	if hasHelpFlag(args) {
		fmt.Println(`usage: attn onboarding [done|again] [--profile <name|id>] [--json]

With no action, show whether the Chief's onboarding is pending, and which
path it takes: the full path, or the tour for a user who used attn before
the Chief.
  done   mark onboarding finished
  again  start the full path again in the Chief's next session

again doesn't wake the Chief. An awake Chief can run it, then start the full
path in their current conversation.`)
		return
	}
	fs := flag.NewFlagSet("onboarding", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", "", "profile name or id")
	jsonOut := fs.Bool("json", false, "print JSON")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	action := protocol.OnboardingActionShow
	if len(positionals) == 1 {
		action = protocol.OnboardingAction(positionals[0])
	}
	if err != nil || len(positionals) > 1 || (action != protocol.OnboardingActionShow && action != protocol.OnboardingActionDone && action != protocol.OnboardingActionAgain) {
		fmt.Fprintln(os.Stderr, "usage: attn onboarding [done|again] [--profile <name|id>] [--json]")
		os.Exit(2)
	}
	result, err := client.New("").Onboarding(action, currentSessionOrExit(), *profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "onboarding: "+err.Error())
		os.Exit(1)
	}
	if *jsonOut {
		printJSON(result)
		return
	}
	switch result.FirstDay {
	case protocol.FirstDayNone:
		fmt.Printf("%s has no onboarding pending. To run the full path, use `attn onboarding again`.\n", result.Chief)
	case protocol.FirstDayTour:
		fmt.Printf("%s's onboarding is pending. Their next session opens with the tour, because attn was in use before the Chief.\n", result.Chief)
	default:
		fmt.Printf("%s's onboarding is pending. Their next session opens with the full path.\n", result.Chief)
	}
	if !result.Configured {
		fmt.Println("The Chief has no harness yet. Pick their harness and model in the sidebar, or run `attn crew set chief --agent <harness> --model <model>`.")
	}
}
