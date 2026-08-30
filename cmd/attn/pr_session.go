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

func sessionPRClient() *client.Client {
	return client.New(config.SocketPath())
}

type sessionPRArgs struct {
	sessionID string
	url       string
	asJSON    bool
}

func parseSessionPRArgs(command string, args []string) (sessionPRArgs, error) {
	fs := flag.NewFlagSet("pr "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	asJSON := fs.Bool("json", false, "print the result as JSON")

	// flag stops at the first positional, so keep parsing past each one.
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return sessionPRArgs{}, err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}

	parsed := sessionPRArgs{sessionID: strings.TrimSpace(*session), asJSON: *asJSON}
	if parsed.sessionID == "" {
		parsed.sessionID = strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
	}
	if parsed.sessionID == "" {
		return parsed, errors.New("no session — pass --session <id>, or run inside an attn session")
	}
	if command == "ls" {
		return parsed, nil
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
	if command == "ls" {
		return listSessionPRs(parsed.sessionID, parsed.asJSON, stdout, stderr)
	}
	return recordOrForgetSessionPR(command, parsed.sessionID, parsed.url, stdout, stderr)
}

func recordOrForgetSessionPR(command, sessionID, url string, stdout, stderr io.Writer) int {
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

func listSessionPRs(sessionID string, asJSON bool, stdout, stderr io.Writer) int {
	sessions, err := sessionPRClient().Query("")
	if err != nil {
		fmt.Fprintf(stderr, "pr ls: %v\n", err)
		return prWaitExitError
	}
	var found *protocol.Session
	for i := range sessions {
		if sessions[i].ID == sessionID {
			found = &sessions[i]
			break
		}
	}
	if found == nil {
		fmt.Fprintf(stderr, "pr ls: unknown session %s\n", sessionID)
		return prWaitExitError
	}

	if asJSON {
		entries := found.PullRequests
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

	if len(found.PullRequests) == 0 {
		fmt.Fprintf(stdout, "session %s has opened no pull requests\n", sessionID)
		return 0
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PULL REQUEST\tSTATE\tCHECKS\tREVIEW\tURL")
	for _, pr := range found.PullRequests {
		fmt.Fprintf(w, "%s#%d\t%s\t%s\t%s\t%s\n",
			pr.Repository, pr.Number, pr.State,
			orDash(protocol.Deref(pr.CIStatus)), orDash(protocol.Deref(pr.ReviewStatus)), pr.URL)
	}
	w.Flush()
	return 0
}
