package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func runCallout() {
	os.Exit(executeCallout(os.Args[2:], os.Stdout, os.Stderr))
}

func executeCallout(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasHelpFlag(args) {
		writeCalloutHelp(stdout)
		return 0
	}
	command, args := args[0], args[1:]
	fail := func(err error) int {
		var daemonErr *client.DaemonError
		if errors.As(err, &daemonErr) {
			err = errors.New(daemonErr.Message)
		}
		fmt.Fprintf(stderr, "callout %s: %v\n", command, err)
		return 1
	}
	if command != "show" && command != "anchors" {
		recordUsageError("callout", usageErrorUnknownCommand)
		return fail(fmt.Errorf("unknown command %q; use show or anchors", command))
	}
	fs := flag.NewFlagSet("callout "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to the current session)")
	var asJSON bool
	if command == "anchors" {
		fs.BoolVar(&asJSON, "json", false, "print anchors as JSON")
	}
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil {
		return fail(err)
	}
	if command == "anchors" && len(positionals) > 0 {
		return fail(fmt.Errorf("unexpected argument %q", positionals[0]))
	}
	if command == "show" {
		if len(positionals)%2 != 0 {
			return fail(fmt.Errorf("an anchor needs its text: %q is the last argument", positionals[len(positionals)-1]))
		}
		if len(positionals) == 0 {
			return fail(fmt.Errorf("a callout needs at least one anchor and its text"))
		}
	}
	sessionID := protocol.SessionID(strings.TrimSpace(*session))
	if sessionID == "" {
		sessionID = currentSessionOrExit()
	}
	if sessionID == "" {
		return fail(fmt.Errorf("no session; pass --session <id>, or run inside an attn session"))
	}
	c := client.New(config.SocketPath())
	if command == "show" {
		steps := make([]protocol.CalloutShowStep, 0, len(positionals)/2)
		for i := 0; i < len(positionals); i += 2 {
			steps = append(steps, protocol.CalloutShowStep{Anchor: positionals[i], Text: positionals[i+1]})
		}
		message, err := c.CalloutShow(sessionID, steps)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, message)
		return 0
	}
	result, err := c.CalloutAnchors(sessionID)
	if err != nil {
		return fail(err)
	}
	if asJSON {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return fail(err)
		}
	} else {
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, anchor := range result.Anchors {
			visibility := ""
			if anchor.OnScreen != nil {
				visibility = "not on screen"
				if *anchor.OnScreen {
					visibility = "on screen"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", anchor.Name, visibility, anchor.Description)
		}
		_ = w.Flush()
	}
	if !result.AppShowingProfile {
		return fail(fmt.Errorf("the attn app is not showing your profile"))
	}
	return 0
}

func writeCalloutHelp(w io.Writer) {
	fmt.Fprint(w, `Point at the attn app with short callouts (experimental, off by default).
The user can turn them on in Settings > Experimental > Agent callouts.

Usage:
  attn callout anchors [--json]
  attn callout show <anchor> <text> [<anchor> <text>]...

List anchors first to see which are on screen. Agent anchors accept a session
id, short id or crew name (agent:<who>); desktop anchors accept a slot digit,
name or id (desktop:<which>). Enter on either goes there.

A callout has at most 20 steps, each at most 160 characters. A new callout
replaces the one showing. The command returns when the app accepts it; the
user steps through it with Enter or →, and Esc or a click outside ends it.
A missing fixed anchor refuses the whole callout. Every refusal exits 1.
Use --session <id> to point from a specific session.

Examples:
  attn callout anchors
  attn callout show garden "Work you and your agents track"
  attn callout show agent:a1b2 "Needs your review on the migration plan"
`)
}
