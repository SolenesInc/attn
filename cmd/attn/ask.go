package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func runAsk() {
	os.Exit(executeAsk(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
}

func executeAsk(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if hasHelpFlag(args) {
		writeAskHelp(stdout)
		return 0
	}
	command := ""
	if len(args) > 0 && (args[0] == "list" || args[0] == "close") {
		command, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to the current session)")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	subject := fs.String("subject", "", "the ask's subject")
	file := fs.String("file", "", "read the ask from this markdown file instead of stdin")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "ask: %v\n", err)
		return 2
	}
	sessionID := protocol.SessionID(strings.TrimSpace(*session))
	if sessionID == "" {
		sessionID = currentSessionOrExit()
	}
	if sessionID == "" {
		fmt.Fprintln(stderr, "ask: no session — pass --session <id>, or run inside an attn session")
		return 2
	}
	c := client.New(config.SocketPath())
	var err error
	switch command {
	case "list":
		err = listAsks(c, sessionID, *asJSON, stdout)
	case "close":
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "ask close: needs exactly one ask id")
			return 2
		}
		if err = c.AskClose(sessionID, fs.Arg(0)); err == nil {
			fmt.Fprintf(stdout, "Withdrew %s.\n", fs.Arg(0))
		}
	default:
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "ask: unexpected argument %q; pipe the ask on stdin or pass --file\n", fs.Arg(0))
			return 2
		}
		err = openAsk(c, sessionID, *subject, *file, *asJSON, stdin, stdout)
	}
	if daemonErr := (*client.DaemonError)(nil); errors.As(err, &daemonErr) {
		err = errors.New(daemonErr.Message)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", strings.TrimSpace("ask "+command), err)
		return 1
	}
	return 0
}

func openAsk(c *client.Client, sessionID protocol.SessionID, subject, file string, asJSON bool, stdin io.Reader, stdout io.Writer) error {
	markdown, baseDir, err := readAsk(file, stdin)
	if err != nil {
		return err
	}
	opened, err := c.AskOpen(sessionID, markdown, subject, baseDir)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(stdout).Encode(opened)
	}
	fmt.Fprintf(stdout, "Asked %s: %q. The answer arrives in your attn inbox.\n", opened.ID, opened.Subject)
	return nil
}

func readAsk(file string, stdin io.Reader) (string, string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", "", err
		}
		abs, err := filepath.Abs(file)
		if err != nil {
			return "", "", err
		}
		return string(data), filepath.Dir(abs), nil
	}
	data, err := io.ReadAll(io.LimitReader(stdin, ask.MaxMarkdownBytes+1))
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", "", errors.New("pipe the ask's markdown on stdin, or pass --file")
	}
	cwd, err := os.Getwd()
	return string(data), cwd, err
}

func listAsks(c *client.Client, sessionID protocol.SessionID, asJSON bool, stdout io.Writer) error {
	result, err := c.AskList(sessionID)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(stdout).Encode(result)
	}
	if !result.Enabled {
		fmt.Fprintln(stdout, "Asks are off for this session.")
		return nil
	}
	if len(result.Asks) == 0 {
		fmt.Fprintln(stdout, "No open asks.")
		return nil
	}
	for _, open := range result.Asks {
		fmt.Fprintf(stdout, "%s  %s\n", open.ID, open.Subject)
	}
	return nil
}

func writeAskHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn ask [flags] < ask.md
       attn ask list
       attn ask close <id>

Ask the user something they can answer from attn, without opening your terminal.
The ask is markdown; question blocks inside it become choices the user picks:

  :::question          one choice     - [ ] label — why
  :::question-multi    any number     Recommended: <label>
  :::question-text     a written answer

The first line of a block is the question; its other lines are context.
attn ask prints the ask's id and returns at once. The answer arrives in your
attn inbox in the user's words.

commands:
  (none)                 open an ask from stdin or --file
  list                   this session's open asks
  close <id>             withdraw an ask you no longer need answered

flags:
  --subject <text>       the ask's subject (defaults to its first question)
  --file <path>          read the ask from a file; images resolve beside it
  --session <id>         session id (defaults to the current session)
  --json                 print the result as JSON
`)
}
