package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func runAsk() {
	exit(executeAsk(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
}

func executeAsk(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if hasHelpFlag(args) {
		writeAskHelp(stdout)
		return 0
	}
	command := ""
	if len(args) > 0 && (args[0] == "list" || args[0] == "show" || args[0] == "close") {
		command, args = args[0], args[1:]
		setCommandPath("ask " + command)
	}
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "session id (defaults to the current session)")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	subject := fs.String("subject", "", "the ask's subject")
	seed := fs.String("seed", "", "a seed you tend that the ask is about")
	file := fs.String("file", "", "read the ask from this markdown file instead of stdin")
	reason := fs.String("m", "", "why you withdraw an ask of an agent you oversee")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil {
		fmt.Fprintf(stderr, "ask: %v\n", err)
		return 2
	}
	if usage := askUsageError(command, positionals); usage != "" {
		fmt.Fprintln(stderr, usage)
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
	switch command {
	case "list":
		err = listAsks(c, sessionID, *asJSON, stdout)
	case "show":
		err = showAsk(c, sessionID, positionals[0], *asJSON, stdout)
	case "close":
		if err = c.AskClose(sessionID, positionals[0], *reason); err == nil {
			fmt.Fprintf(stdout, "Withdrew %s.\n", positionals[0])
		}
	default:
		err = openAsk(c, sessionID, *seed, *subject, *file, *asJSON, stdin, stdout)
	}
	var daemonErr *client.DaemonError
	if errors.As(err, &daemonErr) {
		err = errors.New(daemonErr.Message)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", strings.TrimSpace("ask "+command), err)
		return 1
	}
	return 0
}

func askUsageError(command string, positionals []string) string {
	switch {
	case (command == "close" || command == "show") && len(positionals) != 1:
		return fmt.Sprintf("ask %s: needs exactly one ask id", command)
	case command == "list" && len(positionals) > 0:
		return fmt.Sprintf("ask list: unexpected argument %q", positionals[0])
	case command == "" && len(positionals) > 0:
		return fmt.Sprintf("ask: unexpected argument %q; pipe the ask on stdin or pass --file", positionals[0])
	}
	return ""
}

func openAsk(c *client.Client, sessionID protocol.SessionID, seedID, subject, file string, asJSON bool, stdin io.Reader, stdout io.Writer) error {
	markdown, baseDir, err := readAsk(file, stdin)
	if err != nil {
		return err
	}
	parts, err := ask.Parse(markdown)
	if err != nil {
		return err
	}
	images, err := readAskImages(ask.ImagePaths(parts, baseDir))
	if err != nil {
		return err
	}
	opened, err := c.AskOpen(sessionID, seedID, markdown, subject, baseDir, images...)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(stdout).Encode(opened)
	}
	if opened.SeedID == nil {
		fmt.Fprintf(stdout, "Asked %s: %q. attn types the answer into this conversation as the user's message.\n", opened.ID, opened.Subject)
		return nil
	}
	fmt.Fprintf(stdout, "Asked %s about %s: %q. End your turn: your session stays open so the user can find you, and attn types the answer into this conversation. The seed cannot be harvested until the user answers.\n",
		opened.ID, askSeedName(*opened), opened.Subject)
	return nil
}

func readAskImages(paths []string) ([]protocol.AskImage, error) {
	total := int64(0)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("the ask shows the image %s, which cannot be read: %w", path, errors.Unwrap(err))
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("the ask shows the image %s, which is not a file", path)
		}
		total += info.Size()
	}
	if total > ask.MaxImageBytes {
		return nil, fmt.Errorf("an ask's images are copied with it and hold at most max_ask_image_bytes=%d (%d MiB) together; these %d images hold %d bytes. Show fewer or smaller images",
			ask.MaxImageBytes, ask.MaxImageBytes>>20, len(paths), total)
	}
	images := make([]protocol.AskImage, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("the ask shows the image %s, which cannot be read: %w", path, err)
		}
		images = append(images, protocol.AskImage{Path: path, DataBase64: base64.StdEncoding.EncodeToString(data)})
	}
	return images, nil
}

func askSeedName(a protocol.Ask) string {
	if slug := protocol.Deref(a.SeedSlug); slug != "" {
		return slug
	}
	return protocol.Deref(a.SeedID)
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
	}
	switch {
	case len(result.Asks) == 0 && len(result.Overseen) == 0:
		if result.Enabled {
			fmt.Fprintln(stdout, "No open asks.")
		}
		return nil
	case len(result.Asks) == 0 && result.Enabled:
		fmt.Fprintln(stdout, "You have no open asks.")
	}
	for _, open := range result.Asks {
		fmt.Fprintln(stdout, askRow(open))
	}
	if len(result.Overseen) == 0 {
		return nil
	}
	if len(result.Asks) > 0 || result.Enabled {
		fmt.Fprintln(stdout)
	}
	fmt.Fprintln(stdout, "Asked by agents you oversee. These are not your asks, and their answers go to the askers.")
	fmt.Fprintln(stdout, `Read one: attn ask show <id>. Withdraw one: attn ask close <id> -m "<why>".`)
	for _, overseen := range result.Overseen {
		fmt.Fprintf(stdout, "%s  · from %s\n", askRow(overseen.Ask), askerName(overseen.From))
	}
	return nil
}

func askRow(a protocol.Ask) string {
	if a.SeedID == nil {
		return fmt.Sprintf("%s  %s", a.ID, a.Subject)
	}
	return fmt.Sprintf("%s  %s  %s", a.ID, askSeedName(a), a.Subject)
}

func askerName(from protocol.ActorView) string {
	session, ok := strings.CutPrefix(string(from.Ref), "session:")
	if !ok || strings.HasPrefix(session, from.Name) {
		return from.Name
	}
	return fmt.Sprintf("%s (%.8s)", from.Name, session)
}

func showAsk(c *client.Client, sessionID protocol.SessionID, askID string, asJSON bool, stdout io.Writer) error {
	shown, err := c.AskShow(sessionID, askID)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(stdout).Encode(shown)
	}
	a := shown.Ask
	fmt.Fprintf(stdout, "%s  %s\n", a.ID, a.Subject)
	fmt.Fprintf(stdout, "asked by  %s\n", askerName(shown.From))
	if a.SeedID != nil {
		fmt.Fprintf(stdout, "seed      %s\n", askSeedName(a))
	}
	fmt.Fprintf(stdout, "open      %s\n", askAge(a.CreatedAt))
	if !shown.Held {
		fmt.Fprintf(stdout, "Not your ask. The user answers it, and the answer goes to its asker. Withdraw it with attn ask close %s -m \"<why>\".\n", a.ID)
	}
	fmt.Fprintf(stdout, "\n%s\n", strings.TrimRight(shown.Markdown, "\n"))
	return nil
}

func askAge(createdAt string) string {
	at, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return createdAt
	}
	switch age := time.Since(at); {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 48*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}

func writeAskHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn ask [flags] < ask.md
       attn ask list
       attn ask show <id>
       attn ask close <id> [-m <why>]

Ask the user what you cannot settle alone. They answer from attn, without
opening your terminal.

After you ask, carry on with work that does not depend on the answer, then end
your turn. Your session stays open, so the user can find you, talk to you and
run commands in your terminal, and attn types the answer into this conversation
as the user's message. While any of your asks is open, attn refuses a standby
that would close your session, and names each ask.

When a delegation assigned you a seed, the ask is about that seed; --seed makes
it about another seed you tend. The user sees which seed it concerns. The
answer goes to whoever tends the seed by then. The seed stays open until the
user answers: attn refuses an agent's harvest and names the open ask. Harvest
after you act on the answer. Withering the seed withdraws its open asks.

To ask permission for a risky action, name the exact action and its target in
the question, such as the command, branch or environment. The answer approves
only what the question names.

The ask is markdown. Question blocks in it ask the user something; an ask
without one just tells them something:

  :::question            pick one choice
  :::question-multi      pick any number of choices
  :::question-text       write an answer

A block's first line is the question; the rest is context. Choices are
"- [ ] label" or "- [ ] label — why"; "Recommended: <label>" marks yours.

  attn ask --subject "Cache store" <<'ASK'
  Deploys empty the cache, so the first requests after one are slow.

  :::question
  Which store should keep the cache?
  - [ ] Postgres — already deployed
  - [ ] Redis — faster, one more service
  Recommended: Postgres
  :::
  ASK

Images (![alt](path)) resolve against the working directory, or beside
--file. attn copies them when the ask opens, at most 5 MiB in all, so the
user sees them as they were. An image that cannot be read refuses the ask.

attn ask prints the ask's id and returns at once. Carry on with work that does
not depend on the answer. When the user answers, attn types the answer into
the conversation as their message, approvals included. By then they have
likely moved on and will not read what you write back there. If their answer
asks you something, reply with a new ask. If you still need them, ask again.

You can also read and withdraw the asks of the agents you oversee: the agents
below you in your delegation chain, or every agent in your profile when you
are the Chief. They are not your asks, and only the user answers them. attn ask
list shows them apart from yours. To withdraw one that no longer needs the
user, say why with -m: attn notes the reason on the ask's seed and tells the
agent that asked, so it does not wait for an answer.

commands:
  (none)                 open an ask from stdin or --file
  list                   your open asks and those about seeds you tend, then
                         the open asks of the agents you oversee
  show <id>              print one ask: who asked, its seed, its age and text
  close <id>             withdraw an ask you no longer need answered

flags:
  --seed <id>            a seed you tend that the ask is about (defaults to
                         the seed your delegation assigned you)
  --subject <text>       the ask's subject (defaults to its first question)
  --file <path>          read the ask from a file; images resolve beside it
  --session <id>         session id (defaults to the current session)
  --json                 print the result as JSON
  -m <why>               with close: why you withdraw an ask of an agent you
                         oversee; required for those, and sent to the asker
`)
}
