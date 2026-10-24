package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func runVent() {
	os.Exit(executeVent(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
}

func executeVent(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if hasHelpFlag(args) {
		writeVentHelp(stdout)
		return 0
	}
	withdraw := len(args) > 0 && args[0] == "withdraw"
	if withdraw {
		args = args[1:]
	}
	fs := flag.NewFlagSet("vent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to the current session)")
	file := fs.String("file", "", "read the vent from this file instead of the arguments or stdin")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil {
		fmt.Fprintf(stderr, "vent: %v\n", err)
		return 2
	}
	if withdraw && len(positionals) != 1 {
		fmt.Fprintln(stderr, "vent withdraw: needs exactly one vent id")
		return 2
	}
	if !withdraw && len(positionals) > 0 && *file != "" {
		fmt.Fprintln(stderr, "vent: pass the vent as arguments or with --file, not both")
		return 2
	}
	sessionID := protocol.SessionID(strings.TrimSpace(*session))
	if sessionID == "" {
		sessionID = currentSessionOrExit()
	}
	if sessionID == "" {
		fmt.Fprintln(stderr, "vent: no session — pass --session <id>, or run inside an attn session")
		return 2
	}
	c := client.New(config.SocketPath())
	if withdraw {
		if err := c.WithdrawVent(sessionID, positionals[0]); err != nil {
			fmt.Fprintf(stderr, "vent withdraw: %v\n", daemonMessage(err))
			return 1
		}
		fmt.Fprintf(stdout, "Withdrew %s.\n", positionals[0])
		return 0
	}
	text, err := readVent(positionals, *file, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "vent: %v\n", err)
		return 1
	}
	vent, err := c.RecordVent(sessionID, text)
	if err != nil {
		fmt.Fprintf(stderr, "vent refused: %v\n", daemonMessage(err))
		return 1
	}
	fmt.Fprintf(stdout, "vent %s recorded. Keep working; nobody replies to vents.\n", vent.ID)
	return 0
}

func daemonMessage(err error) error {
	var daemonErr *client.DaemonError
	if errors.As(err, &daemonErr) {
		return errors.New(daemonErr.Message)
	}
	return err
}

func readVent(positionals []string, file string, stdin io.Reader) (string, error) {
	if len(positionals) > 0 {
		return strings.Join(positionals, " "), nil
	}
	if file != "" {
		data, err := os.ReadFile(file)
		return string(data), err
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", errors.New(`say what got in your way: attn vent "<what happened, what you expected, what you did instead>"`)
	}
	return string(data), nil
}

func writeVentHelp(w io.Writer) {
	fmt.Fprintf(w, `usage: attn vent "<what happened, what you expected, what you did instead>"
       attn vent --file note.md        (or the vent on stdin)
       attn vent withdraw <id>

Say what got in your way: a command that failed or behaved differently from
its help, instructions that contradict each other or the code, or a capability
you needed and had to work around. Vents stay on this machine for the user to
read. Nobody replies, so keep working.

attn records with each vent what it already knows: the session, its harness
and model, your stake (the seed a delegation assigned you), attn's build, the
experimental settings on when the session launched, and where your transcript
stood.

Limits: one vent per turn, and %d bytes of text. To add to a vent, withdraw
it and send one that covers both. Vents need usage analytics on
(analytics.enabled) when the session launched.

commands:
  (none)                 record a vent from the arguments, --file or stdin
  withdraw <id>          take back a vent you sent

flags:
  --file <path>          read the vent from a file
  --session <id>         session id (defaults to the current session)
`, analytics.VentTextLimit)
}
