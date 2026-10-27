package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func writeCodexHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn codex <command>

  status [--profile <name|id>] [--json]
  update [--profile <name|id>]
  restart [--now] [--profile <name|id>]

status shows the profile's shared Codex: the Codex its app-server runs, the
installed and latest ones, a restart and what it waits on, and the sessions
whose terminals still run an older Codex.
update runs Codex's own update here, with the Codex executable attn launches,
then has attn read the installed Codex again. Shared Codex moves to it once
no turn runs.
restart restarts the app-server on the installed Codex once no turn runs,
also when it already runs that Codex. --now stops turns waiting on an
approval or a question, gives running turns a minute to finish, then stops
them.

Inside an attn session the profile is the session's; elsewhere pass
--profile when there is more than one.
`)
}

func runCodex() {
	os.Exit(executeCodex(os.Args[2:], os.Stdout, os.Stderr))
}

func executeCodex(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasHelpFlag(args) {
		writeCodexHelp(stdout)
		return 0
	}
	action := args[0]
	fs := flag.NewFlagSet("codex "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", "", "profile name or id")
	asJSON := fs.Bool("json", false, "print the status as JSON")
	now := fs.Bool("now", false, "restart now")
	positionals, err := parseInterspersedFlagArgs(fs, args[1:])
	if err == nil && len(positionals) > 0 {
		err = fmt.Errorf("unexpected argument %q", positionals[0])
	}
	if err == nil && *asJSON && action != "status" {
		err = errors.New("--json is only for status")
	}
	if err == nil && *now && action != "restart" {
		err = errors.New("--now is only for restart")
	}
	if err != nil {
		fmt.Fprintf(stderr, "codex %s: %v\n", action, err)
		return 2
	}
	c := client.New(config.SocketPath())
	caller, err := currentSession(c)
	if err != nil {
		fmt.Fprintf(stderr, "codex %s: resolve the current session: %v\n", action, err)
		return 1
	}
	status := func(refresh bool) (*protocol.CodexStatusResult, bool) {
		result, err := c.CodexStatus(caller, *profile, refresh)
		if err != nil {
			fmt.Fprintf(stderr, "codex %s: %v\n", action, daemonMessage(err))
			return nil, false
		}
		return result, true
	}
	switch action {
	case "status":
		result, ok := status(false)
		if !ok {
			return 1
		}
		if *asJSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			_ = encoder.Encode(result)
			return 0
		}
		writeCodexStatus(stdout, result, sessionNames(c))
		return 0
	case "update":
		before, ok := status(false)
		if !ok {
			return 1
		}
		update := exec.Command(before.Executable, "update")
		update.Stdin, update.Stdout, update.Stderr = os.Stdin, stdout, stderr
		code := 0
		if err := update.Run(); err != nil {
			var exited *exec.ExitError
			if !errors.As(err, &exited) {
				fmt.Fprintf(stderr, "codex update: run %s update: %v\n", before.Executable, err)
				return 1
			}
			code = exited.ExitCode()
		}
		after, ok := status(true)
		if !ok {
			return 1
		}
		fmt.Fprintln(stdout)
		writeCodexStatus(stdout, after, sessionNames(c))
		return code
	case "restart":
		result, ok := status(false)
		if !ok {
			return 1
		}
		if err := c.RestartSharedCodex(result.ProfileID, *now); err != nil {
			fmt.Fprintf(stderr, "codex restart: %v\n", daemonMessage(err))
			return 1
		}
		if *now {
			fmt.Fprintf(stdout, "Restarting shared Codex of profile %s now.\n", result.ProfileName)
		} else {
			fmt.Fprintf(stdout, "Shared Codex of profile %s restarts once no turn runs.\n", result.ProfileName)
		}
		return 0
	default:
		fmt.Fprintf(stderr, "codex: unknown command %q\n\n", action)
		writeCodexHelp(stderr)
		return 2
	}
}

func sessionNames(c *client.Client) map[protocol.SessionID]string {
	names := map[protocol.SessionID]string{}
	listed, err := c.List("")
	if err != nil {
		return names
	}
	for _, session := range listed.Sessions {
		if session.Label != "" {
			names[session.ID] = session.Label
		}
	}
	return names
}

func writeCodexStatus(w io.Writer, result *protocol.CodexStatusResult, names map[protocol.SessionID]string) {
	name := func(id protocol.SessionID) string {
		if label := names[id]; label != "" {
			return label
		}
		return string(id)
	}
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer table.Flush()
	fmt.Fprintf(table, "profile\t%s\n", result.ProfileName)
	fmt.Fprintf(table, "codex\t%s\n", result.Executable)
	state := result.SharedCodex
	if state == nil {
		fmt.Fprintf(table, "shared\toff; turn it on in Settings → Experimental\n")
		return
	}
	version := func(v *string, missing string) string {
		if protocol.Deref(v) == "" {
			return missing
		}
		return *v
	}
	fmt.Fprintf(table, "server\t%s\n", version(state.RunningVersion, "not running; the next shared Codex terminal starts it"))
	fmt.Fprintf(table, "installed\t%s\n", version(state.InstalledVersion, "unknown"))
	fmt.Fprintf(table, "latest\t%s  (codex doctor, daily)\n", version(state.LatestVersion, "not checked yet"))
	restart := string(state.Restart)
	switch state.Restart {
	case protocol.SharedCodexRestartPending, protocol.SharedCodexRestartRestarting:
		if force := protocol.Deref(state.ForceAt); force != "" {
			if at, err := time.Parse(time.RFC3339Nano, force); err == nil {
				restart += fmt.Sprintf(", force stop in %s", time.Until(at).Round(time.Second))
			}
		}
		if len(state.Blockers) > 0 {
			waiting := make([]string, 0, len(state.Blockers))
			for _, blocker := range state.Blockers {
				waiting = append(waiting, fmt.Sprintf("%s (%s)", name(blocker.SessionID), blocker.Reason))
			}
			restart += ", waiting on " + strings.Join(waiting, ", ")
		}
	case protocol.SharedCodexRestartFailed:
		restart += ": " + protocol.Deref(state.Error)
	}
	fmt.Fprintf(table, "restart\t%s\n", restart)
	if len(state.StaleSessions) > 0 {
		stale := make([]string, 0, len(state.StaleSessions))
		for _, session := range state.StaleSessions {
			stale = append(stale, name(session))
		}
		fmt.Fprintf(table, "old tiles\t%s\n", strings.Join(stale, ", "))
	}
}
