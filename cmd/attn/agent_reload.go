package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func runAgentReload(args []string) {
	fs := flag.NewFlagSet("agent reload", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	all := fs.Bool("all", false, "reload all agents in the caller's profile (chief only)")
	source := fs.String("source-session", "", "caller session id (defaults to the current session)")
	jsonOut := fs.Bool("json", false, "print per-target outcomes as JSON")
	targets := []string{}
	var err error
	for rest := args; ; {
		if err = parseFlags(fs, rest); err != nil || fs.NArg() == 0 {
			break
		}
		target := strings.TrimSpace(fs.Arg(0))
		if target == "" {
			err = errors.New("target cannot be empty")
			break
		}
		targets = append(targets, target)
		rest = fs.Args()[1:]
	}
	if err == nil && *all == (len(targets) > 0) {
		err = errors.New("name one or more sessions or crew members, or use --all")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent reload: %v\n", err)
		os.Exit(2)
	}
	caller := protocol.TrimID(protocol.SessionID(*source))
	if caller == "" {
		caller = currentSessionOrExit()
	}
	if caller == "" {
		fmt.Fprintln(os.Stderr, "agent reload: no caller; run inside a session or pass --source-session <id>")
		os.Exit(2)
	}
	result, err := client.New("").AgentReload(targets, caller, *all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent reload: %v\n", err)
		os.Exit(1)
	}
	failed := false
	if *jsonOut {
		printJSON(result)
	}
	for _, outcome := range result.Outcomes {
		if !outcome.Success {
			failed = true
		}
		if *jsonOut {
			continue
		}
		if outcome.Success {
			fmt.Printf("reloaded session %s\n", outcome.Target)
		} else {
			fmt.Fprintf(os.Stderr, "agent reload %s: %s\n", outcome.Target, protocol.Deref(outcome.Error))
		}
	}
	if failed {
		os.Exit(1)
	}
}
