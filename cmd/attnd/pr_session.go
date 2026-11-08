package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
)

func sessionPRClient() *client.Client {
	return client.New(config.SocketPath())
}

type sessionPRArgs struct {
	sessionID protocol.SessionID
	seedID    string
	url       string
	mode      prreadiness.Mode
	reviewer  string
	asJSON    bool
}

func parseSessionPRArgs(command string, args []string) (sessionPRArgs, error) {
	fs := flag.NewFlagSet("pr "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to the current session)")
	seed := fs.String("seed", "", "seed id whose tender the watch addresses")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	modeValue := fs.String("mode", string(prreadiness.ModeGreen), "green, codex, or formal-review")
	reviewer := fs.String("reviewer", "", "reviewer login (formal-review only)")

	var positional []string
	for rest := args; ; {
		if err := parseFlags(fs, rest); err != nil {
			return sessionPRArgs{}, err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}

	mode, err := prreadiness.ParseMode(*modeValue)
	if err != nil {
		return sessionPRArgs{}, err
	}
	parsed := sessionPRArgs{
		sessionID: protocol.SessionID(strings.TrimSpace(*session)), seedID: strings.TrimSpace(*seed), asJSON: *asJSON,
		mode: mode, reviewer: strings.TrimSpace(*reviewer),
	}
	if parsed.sessionID == "" {
		parsed.sessionID = currentSessionOrExit()
	}
	if parsed.sessionID == "" {
		return parsed, errors.New("no session — pass --session <id>, or run inside an attn session")
	}
	if command == "ls" {
		return parsed, nil
	}
	if command == "status" {
		if len(positional) > 1 {
			return parsed, errors.New("accepts at most one pull request url")
		}
		if len(positional) == 1 {
			parsed.url = positional[0]
		}
		return parsed, nil
	}
	if command == "watch" {
		if err := prreadiness.ValidateConfig(parsed.mode, parsed.reviewer); err != nil {
			return parsed, err
		}
	}
	if len(positional) != 1 {
		return parsed, errors.New("needs exactly one pull request url")
	}
	parsed.url = positional[0]
	return parsed, nil
}

func executeSessionPRCommand(command string, args []string, stdout, stderr io.Writer) int {
	parsed, err := parseSessionPRArgs(command, args)
	if err != nil {
		fmt.Fprintf(stderr, "pr %s: %v\n", command, err)
		return prWaitExitUsage
	}
	if parsed.seedID != "" && command != "ls" && command != "status" && command != "watch" && command != "unwatch" {
		fmt.Fprintf(stderr, "pr %s: --seed applies to watch, unwatch, ls and status\n", command)
		return prWaitExitUsage
	}
	if command == "ls" || command == "status" {
		entries, owner, code := sessionPRsToList(parsed, command, stderr)
		if code != 0 {
			return code
		}
		return printSessionPRs(entries, owner, parsed.url, command == "status", parsed.asJSON, stdout, stderr)
	}
	if command == "watch" || command == "unwatch" {
		return watchOrUnwatchSessionPR(command, parsed, stdout, stderr)
	}
	return recordOrForgetSessionPR(command, parsed.sessionID, parsed.url, stdout, stderr)
}

func watchOrUnwatchSessionPR(command string, parsed sessionPRArgs, stdout, stderr io.Writer) int {
	c := sessionPRClient()
	var err error
	if command == "watch" {
		if err = prreadiness.ValidateConfig(parsed.mode, parsed.reviewer); err == nil {
			err = c.WatchSessionPullRequest(parsed.sessionID, parsed.seedID, parsed.url, protocol.PullRequestWatchMode(parsed.mode), parsed.reviewer)
		}
	} else {
		err = c.UnwatchSessionPullRequest(parsed.sessionID, parsed.seedID, parsed.url)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pr %s: %v\n", command, err)
		return prWaitExitError
	}
	if parsed.seedID != "" && command == "watch" {
		fmt.Fprintf(stdout, "watching %s in %s mode for seed %s; updates reach the seed's tender\n", parsed.url, parsed.mode, parsed.seedID)
	} else if parsed.seedID != "" {
		fmt.Fprintf(stdout, "stopped watching %s for seed %s\n", parsed.url, parsed.seedID)
	} else if command == "watch" {
		fmt.Fprintf(stdout, "watching %s in %s mode for session %s; updates arrive in the agent inbox\n", parsed.url, parsed.mode, parsed.sessionID)
	} else {
		fmt.Fprintf(stdout, "stopped watching %s for session %s\n", parsed.url, parsed.sessionID)
	}
	return 0
}

func recordOrForgetSessionPR(command string, sessionID protocol.SessionID, url string, stdout, stderr io.Writer) int {
	c := sessionPRClient()
	report, verb := c.RecordPullRequestCreated, "recorded"
	if command == "forget" {
		report, verb = c.ForgetSessionPullRequest, "forgot"
	}
	if err := report(sessionID, url); err != nil {
		fmt.Fprintf(stderr, "pr %s: %v\n", command, err)
		return prWaitExitError
	}
	fmt.Fprintf(stdout, "%s %s for session %s\n", verb, url, sessionID)
	return 0
}

type prListOwner struct {
	kind, id string
}

func sessionPRsToList(parsed sessionPRArgs, command string, stderr io.Writer) ([]protocol.SessionPullRequest, prListOwner, int) {
	if parsed.seedID != "" {
		entries, err := sessionPRClient().SeedPullRequests(parsed.sessionID, parsed.seedID)
		if err != nil {
			fmt.Fprintf(stderr, "pr %s: %v\n", command, err)
			return nil, prListOwner{}, prWaitExitError
		}
		return entries, prListOwner{kind: "seed", id: parsed.seedID}, 0
	}
	sessions, err := sessionPRClient().Query("")
	if err != nil {
		fmt.Fprintf(stderr, "pr %s: %v\n", command, err)
		return nil, prListOwner{}, prWaitExitError
	}
	for i := range sessions {
		if sessions[i].ID == parsed.sessionID {
			return sessions[i].PullRequests, prListOwner{kind: "session", id: string(sessions[i].ID)}, 0
		}
	}
	fmt.Fprintf(stderr, "pr %s: unknown session %s\n", command, parsed.sessionID)
	return nil, prListOwner{}, prWaitExitError
}

func printSessionPRs(pullRequests []protocol.SessionPullRequest, owner prListOwner, url string, watchesOnly, asJSON bool, stdout, stderr io.Writer) int {
	entries := make([]protocol.SessionPullRequest, 0, len(pullRequests))
	for _, pr := range pullRequests {
		if watchesOnly && !protocol.Deref(pr.Watching) {
			continue
		}
		if url != "" && pr.URL != url {
			continue
		}
		entries = append(entries, pr)
	}

	if asJSON {
		if entries == nil {
			entries = []protocol.SessionPullRequest{}
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(entries); err != nil {
			fmt.Fprintf(stderr, "pr ls: %v\n", err)
			return prWaitExitError
		}
		return 0
	}

	if len(entries) == 0 {
		if watchesOnly || owner.kind == "seed" {
			fmt.Fprintf(stdout, "%s %s has no watched pull requests\n", owner.kind, owner.id)
		} else {
			fmt.Fprintf(stdout, "session %s has opened no pull requests\n", owner.id)
		}
		return 0
	}
	w := newTableWriter(stdout)
	fmt.Fprintln(w, "PULL REQUEST\tSTATE\tMODE\tREADINESS\tHEALTH\tURL")
	for _, pr := range entries {
		fmt.Fprintf(w, "%s#%d\t%s\t%s\t%s\t%s\t%s\n",
			pr.Repository, pr.Number, pr.State,
			orDash(string(protocol.Deref(pr.WatchMode))), orDash(protocol.Deref(pr.ReadinessState)),
			orDash(protocol.Deref(pr.WatchHealth)), pr.URL)
	}
	w.Flush()
	return 0
}
