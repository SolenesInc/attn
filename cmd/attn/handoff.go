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

File the letter closing this crew member's session. You write it; attn files it
in the member's home under handoffs/, with a UTC-stamped name, and never edits
the prose. A correction is a new letter.

By default, attn decides the turnover from presence: a successor wakes while the
user is around, and the member sleeps while the user is away. --sleep and --nap
decide it explicitly.

With crew_memory_enabled off, filing immediately runs the turnover.

With crew_memory_enabled on, --summary is required: one line of at most 280 bytes
saying what this session was. After filing the letter and the session line, attn
keeps this session running while summaries are pending. Run attn memory summarize
and pipe the answers to attn memory summarize -. Repeat until none remain. The
last answer runs the requested turnover, including --sleep.

If turnover fails, the letter stays filed and this session keeps running. --retry
uses that letter and repairs a missing session line without adding another one.
With memory enabled it preserves the requested close. With memory disabled a
retry starts the next session regardless of presence. --retry --sleep explicitly
ends the session with no successor in either case.

Only the session bound to a crew member can file its letter. A note for whoever
tends a piece of work next belongs on its seed: attn seed note <id> -m "…" --handoff.

flags:
  -m <text>          the letter; - reads it from stdin
  --summary <line>   the session line, required with crew_memory_enabled
  --retry            close the session with its filed letter; no -m
  --sleep            end the session; do not start the next one
  --nap              start the next session even if the user is away
  --session <id>     the session to close (defaults to the current session)
  --json             print the machine result as JSON
`)
}

type handoffArgs struct {
	note    string
	summary string
	retry   bool
	close   protocol.CrewSessionClose
	session protocol.SessionID
	json    bool
}

func parseHandoffArgs(args []string, envSession func() protocol.SessionID) (handoffArgs, error) {
	fs := flag.NewFlagSet("handoff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	message := fs.String("m", "", "the letter; - reads it from stdin")
	summary := fs.String("summary", "", "one line for the memory, required with crew_memory_enabled")
	retry := fs.Bool("retry", false, "close the session with the letter already filed")
	sleep := fs.Bool("sleep", false, "end the session rather than starting the next one")
	nap := fs.Bool("nap", false, "start the next session even if the user is away")
	session := fs.String("session", "", "the session to close (defaults to the current session)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := parseFlags(fs, args); err != nil {
		return handoffArgs{}, err
	}
	if *sleep && *nap {
		return handoffArgs{}, errors.New("--sleep ends the session and --nap starts the next one; pass one or neither, and neither lets attn decide from whether the user is around")
	}
	if fs.NArg() != 0 {
		return handoffArgs{}, fmt.Errorf("handoff takes its letter in -m, not %q", strings.Join(fs.Args(), " "))
	}
	if *retry && strings.TrimSpace(*message) != "" {
		return handoffArgs{}, errors.New("--retry closes the session with the letter you already filed, so it takes no -m; drop one of the two")
	}
	if !*retry && strings.TrimSpace(*message) == "" {
		return handoffArgs{}, errors.New(`the letter is the handoff — pass it with -m "<your letter>", or -m - to pipe it in`)
	}
	parsed := handoffArgs{note: *message, summary: *summary, retry: *retry, session: protocol.SessionID(strings.TrimSpace(*session)), json: *jsonOut}
	switch {
	case *sleep:
		parsed.close = protocol.CrewSessionCloseSleep
	case *nap:
		parsed.close = protocol.CrewSessionCloseNap
	}
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
	result, err := client.New("").CrewHandoff(parsed.session, note, parsed.retry, parsed.close, parsed.summary)
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
	if pending := protocol.Deref(result.PendingSummaries); pending != 0 {
		fmt.Printf("Session line #%s filed; %d summaries pending. This session keeps running. Run attn memory summarize, then pipe the answers to attn memory summarize -. The last answer completes the requested turnover.\n", protocol.Deref(result.EntryID), pending)
		return
	}
	if napErr := strings.TrimSpace(protocol.Deref(result.NapError)); napErr != "" {
		fmt.Fprintf(os.Stderr, "handoff: could not wake a successor: %s\n%s still has this session. Run `attn handoff --retry` to use the filed letter; do not write another.\n", napErr, result.Name)
		os.Exit(1)
	}
	if protocol.Deref(result.Outcome) == protocol.CrewSessionCloseSleep {
		fmt.Printf("%s is asleep. No successor was started. Click %[1]s in the sidebar to wake them.\n", result.Name)
		return
	}
	fmt.Printf("%s is waking in session %s. This session is ending.\n",
		result.Name, agentShortID(string(protocol.Deref(result.SessionID))))
}
