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

var sessionReopenActions = []protocol.SessionReopenAction{
	protocol.SessionReopenActionReopen,
	protocol.SessionReopenActionRecreateWorktreeAndReopen,
	protocol.SessionReopenActionFetchRecreateAndReopen,
	protocol.SessionReopenActionStartFreshSamePlace,
	protocol.SessionReopenActionStartFreshElsewhere,
	protocol.SessionReopenActionStartFreshDefaultBranch,
}

type sessionReopenArgs struct {
	target  string
	action  string
	cwd     string
	profile string
	json    bool
}

func parseSessionReopenArgs(args []string) (sessionReopenArgs, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return sessionReopenArgs{}, errors.New("exactly one session id is required")
	}
	target := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("session reopen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	action := fs.String("action", "", "the action to perform; `attn session show` lists the ones offered")
	cwd := fs.String("cwd", "", "where to start, for --action start_fresh_elsewhere")
	profile := fs.String("profile", "", "the profile to reopen into, when the session's own profile is gone")
	jsonOut := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return sessionReopenArgs{}, err
	}
	if fs.NArg() != 0 {
		return sessionReopenArgs{}, errors.New("exactly one session id is required")
	}
	named := strings.TrimSpace(*action)
	if named != "" && !knownSessionReopenAction(named) {
		return sessionReopenArgs{}, fmt.Errorf("%q is not a reopen action; the actions are %s",
			named, strings.Join(sessionReopenActionNames(), ", "))
	}
	return sessionReopenArgs{target: target, action: named, cwd: strings.TrimSpace(*cwd), profile: strings.TrimSpace(*profile), json: *jsonOut}, nil
}

func knownSessionReopenAction(name string) bool {
	for _, action := range sessionReopenActions {
		if string(action) == name {
			return true
		}
	}
	return false
}

func sessionReopenActionNames() []string {
	names := make([]string, 0, len(sessionReopenActions))
	for _, action := range sessionReopenActions {
		names = append(names, string(action))
	}
	return names
}

func runSessionReopen(args []string) {
	parsed, err := parseSessionReopenArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "session reopen: %v\n", err)
		writeSessionHelp(os.Stderr)
		os.Exit(2)
	}

	result, err := client.New("").SessionReopen(client.SessionReopenOptions{
		SessionID: parsed.target,
		Action:    parsed.action,
		Directory: parsed.cwd,
		ProfileID: parsed.profile,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "session reopen: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	fprintSessionReopen(os.Stdout, result)
}

func fprintSessionReopen(w io.Writer, result *protocol.SessionReopenResult) {
	if protocol.Deref(result.AlreadyRunning) {
		fmt.Fprintf(w, "%s is already running in profile %s\n", result.SessionID, result.ProfileID)
		return
	}
	if created := protocol.Deref(result.WorktreeCreated); created != "" {
		fmt.Fprintf(w, "recreated worktree %s\n", created)
	}
	fmt.Fprintf(w, "%s reopened in %s (profile %s, unplaced, %s)\n",
		result.SessionID, result.Directory, result.ProfileID, result.Action)
}

func fprintSessionReopenVerdict(w io.Writer, sessionID string, reopen *protocol.SessionReopen) {
	if reopen == nil {
		return
	}
	verdict := "no"
	if reopen.Reopenable {
		verdict = "yes"
	}
	if reason := strings.TrimSpace(protocol.Deref(reopen.Reason)); reason != "" {
		verdict += ": " + reason
	}
	fmt.Fprintf(w, "reopen     %s\n", verdict)
	if warning := strings.TrimSpace(protocol.Deref(reopen.Warning)); warning != "" {
		fmt.Fprintf(w, "warning    %s\n", warning)
	}
	if reopen.Checking {
		fmt.Fprintln(w, "checking   a branch check is running; ask again for a sharper verdict")
	}
	if reopen.ProfileDeleted {
		fmt.Fprintf(w, "lands in   profile %s is gone; pass --profile <id> to choose where\n", reopen.ProfileID)
	} else {
		fmt.Fprintf(w, "lands in   profile %s, unplaced\n", reopen.ProfileID)
	}
	fmt.Fprintf(w, "place      directory %s", reopen.DirectoryState)
	if branch := strings.TrimSpace(protocol.Deref(reopen.BranchState)); branch != "" {
		fmt.Fprintf(w, ", branch %s", branch)
	}
	fmt.Fprintln(w)
	if len(reopen.Actions) == 0 {
		fmt.Fprintln(w, "actions    none")
		return
	}
	names := make([]string, 0, len(reopen.Actions))
	for _, action := range reopen.Actions {
		names = append(names, string(action))
	}
	fmt.Fprintf(w, "actions    %s\n", strings.Join(names, ", "))
	fmt.Fprintf(w, "           attn session reopen %s --action %s\n", sessionID, names[0])
}
