package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func writeTerminalHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn terminal <command>

  ls [--desktop <ref> | --all] [--json]
  open [--host <outpost|home>] [--down] [--desktop <ref>] [--cwd <dir>] [-- <command>]
  run <terminal> "<command>" [--wait] [--timeout <seconds>] [--desktop <ref>]
  send <terminal> (--text "<text>" | --keys C-c Escape Enter ...) [--desktop <ref>]
  read <terminal> [--last | --scrollback [--lines <n>]]
  wait <terminal> --until prompt|exit [--timeout <seconds>]
  move [<terminal>] <desktop> [--json]
  close <terminal> [--desktop <ref>]

Run inside an attn agent. A target is a terminal id or unique terminal-id
prefix printed by ls. A session id/prefix or awake crew name also works when
that session shows in exactly one terminal; otherwise choose a terminal id.
All targets belong to the caller's profile.
Reads and moves reach every desktop; typing and closing on another desktop
require --desktop. Any agent may type into or move a shell. Typing into or
moving agents requires self, a transitive delegation chain, or the profile's
chief. Close agents with attn agent close instead.

Move defaults to your session. <desktop> is a shortcut digit, its name as shown
(case-insensitive), or its id. A missing numbered desktop is created. The
terminal keeps running beside the destination's active tile. The user's current
desktop stays put; a moved active tile becomes active at its destination only
when that desktop is not current. Move yourself and a shell with two calls:
  attn terminal move 8
  attn terminal move <shell> 8

Open defaults to your session's machine and directory. --host selects an
outpost by name or id; --host home selects the home. When the machine changes,
omitting --cwd starts in that machine's home directory. --cwd names a directory
on the target machine.
Claude, Codex and shells run on outposts. An agent's attn commands reach the
home. Spawning chief, crew and plugin agents on outposts is unavailable.

Open preserves focus: first terminal right (or --down), later terminals below
the last live terminal you opened there. --desktop starts at its active tile.
A missing numbered --desktop is created; ls, typing, and closing require an
existing desktop. Open with a command requires OSC 133: known unsupported
shells are refused before opening. First-prompt wait limit=30s (measured 9-176ms in fish/zsh/bash).
On timeout the terminal stays open, nothing is typed later; use send there.
Open without a command for shells without integration.
Commands run through the shell, preserving history. run requires a prompt;
--wait and wait --until prompt require OSC 133 shell integration. --wait reads
the next completed block and exits with its exit code. Avoid concurrent writers
to that shell when attributing output to a command. A timeout exits 124;
without --timeout the wait lasts until completion or the caller disconnects.

Keys: C-a through C-z, C-\\, C-], C-^, C-_, Escape, Enter, Tab, Backspace,
Space, Up, Down, Left, Right, Home, End, Delete, Insert, PageUp, PageDown,
F1 through F12. send writes bytes directly, as a keyboard does.
`)
}

type terminalArgs struct {
	action                                                    string
	positional                                                []string
	desktop, cwd, text, until, host                           string
	keys                                                      []string
	command                                                   string
	down, all, json, wait, last, scrollback, hasText, hasKeys bool
	lines                                                     int
	timeout                                                   time.Duration
}

func parseTerminalArgs(args []string) (terminalArgs, error) {
	var parsed terminalArgs
	if len(args) == 0 {
		return parsed, errors.New("a terminal command is required")
	}
	parsed.action = args[0]
	flags := map[string]bool{}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if parsed.action != "open" {
				return parsed, errors.New("-- command is only for open")
			}
			command := args[i+1:]
			if len(command) == 1 {
				parsed.command = command[0]
			} else {
				quoted := make([]string, len(command))
				for j, s := range command {
					quoted[j] = "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
				}
				parsed.command = strings.Join(quoted, " ")
			}
			break
		}
		if !strings.HasPrefix(arg, "--") {
			parsed.positional = append(parsed.positional, arg)
			continue
		}
		flags[arg] = true
		value := func() (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("%s needs a value", arg)
			}
			return args[i], nil
		}
		switch arg {
		case "--down":
			parsed.down = true
		case "--all":
			parsed.all = true
		case "--json":
			parsed.json = true
		case "--wait":
			parsed.wait = true
		case "--last":
			parsed.last = true
		case "--scrollback":
			parsed.scrollback = true
		case "--host", "--desktop", "--cwd", "--text", "--until", "--timeout", "--lines":
			v, err := value()
			if err != nil {
				return parsed, err
			}
			switch arg {
			case "--host":
				parsed.host = v
			case "--desktop":
				parsed.desktop = v
			case "--cwd":
				parsed.cwd = v
			case "--text":
				parsed.text = v
				parsed.hasText = true
			case "--until":
				parsed.until = v
			case "--timeout":
				parsed.timeout, err = time.ParseDuration(v + "s")
				if err != nil || parsed.timeout <= 0 {
					return parsed, fmt.Errorf("--timeout must be positive seconds; got %q", v)
				}
			case "--lines":
				parsed.lines, err = strconv.Atoi(v)
				if err != nil || parsed.lines <= 0 {
					return parsed, fmt.Errorf("--lines must be positive; got %q", v)
				}
			}
		case "--keys":
			parsed.hasKeys = true
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				parsed.keys = append(parsed.keys, args[i])
			}
			if len(parsed.keys) == 0 {
				return parsed, errors.New("--keys needs at least one key")
			}
		default:
			recordUsageError("terminal", usageErrorUnknownFlag)
			return parsed, fmt.Errorf("unknown flag %s", arg)
		}
	}
	allowed := map[string]string{
		"ls": "--desktop --all --json", "open": "--host --down --desktop --cwd --json", "run": "--wait --timeout --desktop --json", "send": "--text --keys --desktop", "read": "--last --scrollback --lines --json", "wait": "--until --timeout --json", "close": "--desktop", "move": "--json",
	}
	valid, ok := allowed[parsed.action]
	if !ok {
		recordUsageError("terminal", usageErrorUnknownCommand)
		return parsed, fmt.Errorf("unknown terminal command %q", parsed.action)
	}
	for f := range flags {
		if !strings.Contains(" "+valid+" ", " "+f+" ") {
			return parsed, fmt.Errorf("%s does not accept %s", parsed.action, f)
		}
	}
	want := 1
	if parsed.action == "ls" || parsed.action == "open" {
		want = 0
	}
	if parsed.action == "run" {
		want = 2
	}
	if parsed.action == "move" && len(parsed.positional) == 2 {
		want = 2
	}
	if len(parsed.positional) != want {
		return parsed, fmt.Errorf("%s requires %d positional arguments, got %d", parsed.action, want, len(parsed.positional))
	}
	if parsed.all && parsed.desktop != "" {
		return parsed, errors.New("choose --all or --desktop")
	}
	if parsed.action == "send" && parsed.hasText == parsed.hasKeys {
		return parsed, errors.New("send needs exactly one of --text or --keys")
	}
	if parsed.last && parsed.scrollback {
		return parsed, errors.New("choose --last or --scrollback")
	}
	if parsed.lines > 0 && !parsed.scrollback {
		return parsed, errors.New("--lines requires --scrollback")
	}
	if parsed.action == "wait" && parsed.until != "prompt" && parsed.until != "exit" {
		return parsed, errors.New("--until must be prompt or exit")
	}
	return parsed, nil
}

func terminalKeys(keys []string) (string, error) {
	named := map[string]string{"escape": "\x1b", "enter": "\r", "tab": "\t", "backspace": "\x7f", "space": " ", "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D", "home": "\x1b[H", "end": "\x1b[F", "insert": "\x1b[2~", "delete": "\x1b[3~", "pageup": "\x1b[5~", "pagedown": "\x1b[6~", "f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS", "f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~", "f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~"}
	var out strings.Builder
	for _, key := range keys {
		lower := strings.ToLower(key)
		if value, ok := named[lower]; ok {
			out.WriteString(value)
			continue
		}
		if len(lower) == 3 && strings.HasPrefix(lower, "c-") {
			c := lower[2]
			if c >= 'a' && c <= 'z' {
				out.WriteByte(c - 'a' + 1)
				continue
			}
			if c >= '\\' && c <= '_' {
				out.WriteByte(c & 31)
				continue
			}
		}
		return "", fmt.Errorf("unknown key %q; attn terminal --help lists supported keys", key)
	}
	return out.String(), nil
}

func runTerminal() {
	args := os.Args[2:]
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" {
			writeTerminalHelp(os.Stdout)
			return
		}
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "help" || args[0] == "-h")) {
		writeTerminalHelp(os.Stdout)
		return
	}
	parsed, err := parseTerminalArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "terminal:", err)
		writeTerminalHelp(os.Stderr)
		os.Exit(2)
	}
	caller := currentSessionOrExit()
	cli := client.New("")
	ctx := context.Background()
	if parsed.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, parsed.timeout)
		defer cancel()
	}
	code, err := executeTerminal(ctx, cli, caller, parsed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "terminal:", err)
		if errors.Is(err, context.DeadlineExceeded) {
			os.Exit(124)
		}
		os.Exit(1)
	}
	if code != 0 {
		os.Exit(code)
	}
}

func executeTerminal(ctx context.Context, cli *client.Client, caller protocol.SessionID, args terminalArgs) (int, error) {
	var message any
	var target string
	if len(args.positional) > 0 {
		target = args.positional[0]
	}
	desktop := nonEmpty(args.desktop)
	switch args.action {
	case "ls":
		message = protocol.TerminalListMessage{Cmd: protocol.CmdTerminalList, CallerSessionID: caller, Desktop: desktop, All: protocol.Ptr(args.all)}
	case "open":
		direction := protocol.TerminalDirectionRight
		if args.down {
			direction = protocol.TerminalDirectionDown
		}
		message = protocol.TerminalOpenMessage{Cmd: protocol.CmdTerminalOpen, CallerSessionID: caller, Host: nonEmpty(args.host), Desktop: desktop, Direction: &direction, Cwd: nonEmpty(args.cwd), Command: nonEmpty(args.command)}
	case "move":
		target = string(caller)
		if len(args.positional) == 2 {
			target = args.positional[0]
		}
		message = protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: caller, Target: target, Desktop: args.positional[len(args.positional)-1]}
	case "send", "run":
		data := args.text
		if args.hasKeys {
			var err error
			data, err = terminalKeys(args.keys)
			if err != nil {
				return 2, err
			}
		}
		if args.action == "run" {
			data = args.positional[1] + "\r"
		}
		message = protocol.TerminalInputMessage{Cmd: protocol.CmdTerminalInput, CallerSessionID: caller, Target: target, Desktop: desktop, Data: data, Run: protocol.Ptr(args.action == "run"), Wait: protocol.Ptr(args.wait)}
	case "read":
		mode := protocol.TerminalReadModeScreen
		if args.last {
			mode = protocol.TerminalReadModeLast
		}
		if args.scrollback {
			mode = protocol.TerminalReadModeScrollback
		}
		message = protocol.TerminalReadMessage{Cmd: protocol.CmdTerminalRead, CallerSessionID: caller, Target: target, Mode: mode, Lines: protocol.Ptr(args.lines)}
	case "wait":
		message = protocol.TerminalWaitMessage{Cmd: protocol.CmdTerminalWait, CallerSessionID: caller, Target: target, Until: protocol.TerminalWaitUntil(args.until)}
	case "close":
		message = protocol.TerminalCloseMessage{Cmd: protocol.CmdTerminalClose, CallerSessionID: caller, Target: target, Desktop: desktop}
	}
	response, err := cli.TerminalCommand(ctx, message)
	if err != nil {
		return 1, err
	}
	if args.action == "run" && args.wait {
		if response.TerminalInputResult == nil || response.TerminalInputResult.LastBlockID == nil {
			return 1, errors.New("daemon omitted last_block_id for run --wait")
		}
		response, err = cli.TerminalCommand(ctx, protocol.TerminalWaitMessage{Cmd: protocol.CmdTerminalWait, CallerSessionID: caller, Target: string(response.TerminalInputResult.TerminalID), Until: protocol.TerminalWaitUntilPrompt, AfterBlockID: response.TerminalInputResult.LastBlockID})
		if err != nil {
			return 1, err
		}
	}
	if args.json {
		var result any
		switch args.action {
		case "ls":
			result = response.TerminalEntries
		case "open":
			result = response.TerminalOpenResult
		case "move":
			result = response.TerminalMoveResult
		case "read":
			result = response.TerminalReadResult
		case "run":
			if args.wait {
				result = response.TerminalWaitResult
			} else {
				result = response.TerminalInputResult
			}
		case "wait":
			result = response.TerminalWaitResult
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			return 1, err
		}
	} else {
		switch args.action {
		case "ls":
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tAGENT\tSTATE\tDESKTOP\tPOSITION\tOPENED BY")
			for _, entry := range response.TerminalEntries {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", entry.ShortID, entry.Name, entry.Agent, entry.State, entry.DesktopLabel, protocol.Deref(entry.Position), protocol.Deref(entry.OpenedBy))
			}
			_ = w.Flush()
		case "open":
			if response.TerminalOpenResult == nil {
				return 1, errors.New("daemon omitted terminal_open_result")
			}
			fmt.Println(response.TerminalOpenResult.TerminalID)
		case "move":
			result := response.TerminalMoveResult
			if result == nil {
				return 1, errors.New("daemon omitted terminal_move_result")
			}
			if protocol.Deref(result.Unchanged) {
				fmt.Printf("%s already is on desktop %s\n", result.TerminalID, result.DesktopID)
			} else {
				fmt.Printf("%s moved to desktop %s\n", result.TerminalID, result.DesktopID)
			}
		case "read":
			if response.TerminalReadResult == nil {
				return 1, errors.New("daemon omitted terminal_read_result")
			}
			fmt.Println(response.TerminalReadResult.Text)
		case "wait", "run":
			if result := response.TerminalWaitResult; result != nil && result.Block != nil {
				fmt.Println(result.Block.Output)
			}
		}
	}
	if result := response.TerminalWaitResult; result != nil {
		if result.Block != nil {
			if result.Block.ExitCode == nil {
				return 1, errors.New("command finished without an exit code")
			}
			return *result.Block.ExitCode, nil
		}
		if protocol.Deref(result.Exited) {
			return protocol.Deref(result.ExitCode), nil
		}
	}
	return 0, nil
}

func nonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
