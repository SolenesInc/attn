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

func writeHandoffHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn handoff -m "<your letter>"

File the letter closing this crew member's session. You write it; attn files it into
handoffs/ in the member's home, with a UTC-stamped name, and never edits the
prose. A correction is a new letter.

Filing ends the session and the member sleeps until something wakes it,
whether or not the user is at the machine. When attn crew apply asked this
session for its letter because the member's harness changed, filing instead
starts the next session on the new harness from the letter.

With crew_v2_enabled on, the letter also records this session so attn can keep
its transcript.

If the session cannot end or the next one cannot start, the letter stays filed
and this session keeps running. --retry tries again with that letter.

Only the session living a member's day can file one. The note for whoever tends
a piece of work next is the other axis: attn seed note <id> -m "…" --handoff.

flags:
  -m <text>          the letter; - reads it from stdin
  --retry            turn the day over with the letter already filed; no -m
  --session <id>     the session closing its day (defaults to the current session)
  --json             print the machine result as JSON
`)
}

type handoffArgs struct {
	note    string
	retry   bool
	session protocol.SessionID
	json    bool
}

func parseHandoffArgs(args []string, envSession func() protocol.SessionID) (handoffArgs, error) {
	fs := flag.NewFlagSet("handoff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	message := fs.String("m", "", "the letter; - reads it from stdin")
	retry := fs.Bool("retry", false, "turn the day over with the letter already filed")
	session := fs.String("session", "", "the session closing its day (defaults to the current session)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := parseFlags(fs, args); err != nil {
		return handoffArgs{}, err
	}
	if fs.NArg() != 0 {
		return handoffArgs{}, fmt.Errorf("handoff takes its letter in -m, not %q", strings.Join(fs.Args(), " "))
	}
	if *retry && strings.TrimSpace(*message) != "" {
		return handoffArgs{}, errors.New("--retry turns the day over with the letter you already filed, so it takes no -m; drop one of the two")
	}
	if !*retry && strings.TrimSpace(*message) == "" {
		return handoffArgs{}, errors.New(`the letter is the handoff — pass it with -m "<your letter>", or -m - to pipe it in`)
	}
	parsed := handoffArgs{note: *message, retry: *retry, session: protocol.SessionID(strings.TrimSpace(*session)), json: *jsonOut}
	if parsed.session == "" {
		parsed.session = protocol.TrimID(envSession())
	}
	return parsed, nil
}

func runHandoff(args []string) {
	parsed, err := parseHandoffArgs(args, currentSessionOrExit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "handoff: %v\n", err)
		writeHandoffHelp(os.Stderr)
		os.Exit(2)
	}
	note := parsed.note
	if note == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "handoff: reading stdin: %v\n", err)
			os.Exit(1)
		}
		note = string(raw)
	}
	result, err := client.New("").CrewHandoff(parsed.session, note, parsed.retry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "handoff: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	printHandoffResult(result, parsed.retry)
}

func printHandoffResult(result *protocol.CrewHandoffResult, retry bool) {
	if retry {
		fmt.Printf("%s's letter was already filed at %s.\n", result.Name, result.Path)
	} else {
		fmt.Printf("%s's letter is filed at %s.\n", result.Name, result.Path)
	}
	if successorErr := strings.TrimSpace(protocol.Deref(result.SuccessorError)); successorErr != "" {
		fmt.Fprintf(os.Stderr, "handoff: the next session did not start: %s\nThis session is still running and %s is still bound to it. `attn handoff --retry` tries again; `attn crew sleep %[2]s` makes the retry end it with the member asleep. The letter above is filed, so do not write another.\n", successorErr, result.Name)
		os.Exit(1)
	}
	if protocol.Deref(result.Outcome) == protocol.CrewDayCloseSleep {
		fmt.Printf("%s is asleep. Nobody was woken behind you; the sidebar has %[1]s one click from a new day.\n", result.Name)
		return
	}
	fmt.Printf("%s's next session is %s on its new harness, starting now. This one ends here.\n",
		result.Name, agentShortID(string(protocol.Deref(result.SessionID))))
}
