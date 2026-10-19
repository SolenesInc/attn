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

With no action, show whether this profile's Chief finished onboarding and
which first day its next wake runs: full, tour or none.
  done   finish onboarding; future wakes are ordinary
  again  run the full path again on the next wake, nap or attn crew prime

Inside attn, use the session's profile. Outside attn, use --profile when
several profiles exist. The Chief can follow the playbook in its current
conversation after again; the command does not wake it.`)
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
		fmt.Printf("%s finished onboarding. attn onboarding again runs it again.\n", result.Chief)
	case protocol.FirstDayTour:
		fmt.Printf("%s has not finished onboarding. This profile was in use before its Chief; the next wake opens with the short tour.\n", result.Chief)
	default:
		fmt.Printf("%s has not finished onboarding. The next wake opens with the full path.\n", result.Chief)
	}
	if !result.Configured {
		fmt.Println("Pick a harness and model in the Chief's slot to start.")
	}
}
