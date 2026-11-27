package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/probetui"
	"github.com/victorarias/attn/internal/protocol"
	"golang.org/x/sys/unix"
)

func main() {
	run()
	exit(0)
}

func maybePrintInstanceBanner() {
	config.PrintInstanceBanner(os.Stderr)
}

func renamedProfileCommandMessage(args []string) string {
	command := strings.Replace(args[0], "profile", "instance", 1)
	replacement := []string{command}
	for _, arg := range args[1:] {
		if arg == "--profile" {
			arg = "--instance"
		}
		replacement = append(replacement, arg)
	}
	return fmt.Sprintf("attn %s was renamed to attn %s. Run: attn %s", args[0], command, strings.Join(replacement, " "))
}

func runVersion(args []string) {
	if args[0] == "--version" {
		fmt.Println(buildinfo.Version)
		return
	}
	if len(args) > 1 && args[1] == "--json" {
		runBuildInfoJSON()
		return
	}
	dirty := ""
	if buildinfo.Dirty {
		dirty = ", dirty"
	}
	fmt.Printf("%s (%s, %s%s)\n", buildinfo.Version, buildinfo.Channel, shortSHA(buildinfo.GitCommit), dirty)
}

func runProtocolVersion() {
	fmt.Println(protocol.ProtocolVersion)
}

func runBuildInfoJSON() {
	printJSON(map[string]any{
		"version":           buildinfo.Version,
		"protocolVersion":   protocol.ProtocolVersion,
		"channel":           buildinfo.Channel,
		"buildTime":         buildinfo.BuildTime,
		"sourceFingerprint": buildinfo.SourceFingerprint,
		"gitCommit":         buildinfo.GitCommit,
		"dirty":             buildinfo.Dirty,
		"snapshotFormat":    buildinfo.SnapshotFormat,
	})
}

func runDaemonCommand() {
	if len(os.Args) < 3 {
		leaveInvocationUnrecorded()
		runDaemon()
		return
	}
	checkInstance(checkInstanceAndRouting)
	switch os.Args[2] {
	case "ensure":
		setCommandPath("daemon ensure")
		refuseDaemonCommandArgs("ensure", os.Args[3:])
		runDaemonEnsure()
	case "stop":
		setCommandPath("daemon stop")
		args := os.Args[3:]
		endTerminals := len(args) == 1 && args[0] == stopTerminalsFlag
		if !endTerminals {
			refuseDaemonCommandArgs("stop", args)
		}
		runDaemonStop(endTerminals)
	case "help", "-h", "--help":
		writeDaemonHelp(os.Stdout)
	default:
		recordUsageError("daemon", usageErrorUnknownCommand)
		fmt.Fprintf(os.Stderr, "unknown daemon subcommand %q (expected: ensure, stop)\n\n", os.Args[2])
		writeDaemonHelp(os.Stderr)
		exit(1)
	}
}

func refuseDaemonCommandArgs(command string, args []string) {
	if len(args) == 0 {
		return
	}
	if hasHelpFlag(args) {
		writeDaemonHelp(os.Stdout)
		exit(0)
	}
	recordUsageError("daemon "+command, usageErrorUnknownFlag)
	fmt.Fprintf(os.Stderr, "daemon %s: unknown argument %q; it takes no arguments\n\n", command, args[0])
	writeDaemonHelp(os.Stderr)
	exit(2)
}

func writeDaemonHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn daemon [ensure|stop]

With no command, runs the daemon in the foreground.

commands:
  ensure    start the daemon in the background, or restart one that does not
            match this binary, and print the outcome as JSON: status is
            started, already_running or restarted (with a reason)
  stop      stop the running daemon. Terminals keep running in their PTY
            host or workers, so they come back when the daemon starts again
  stop --terminals
            stop the daemon and end every terminal and agent of the instance

Both act on the active instance's daemon; attn instance status names it.
`)
}

func runDaemon() {
	startupSignal, err := daemonctl.TakeStartupSignal()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	startedByHomeUnit := daemonctl.TakeHomeUnitStart()
	if err := daemonctl.ExitWhenHarnessGoes(); err != nil {
		startupSignal.Failed(err)
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	socketPath, err := daemonPreflight()
	if err != nil {
		startupSignal.Failed(err)
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	d := daemon.New(socketPath)
	if startedByHomeUnit {
		d.StartedByHomeUnit()
	}
	d.RecoverGUIPath()
	d.RemoveLegacyStateFile()
	d.ScrubInheritedAgentSessionEnv()
	startResult := make(chan error, 1)
	go func() {
		startResult <- d.Start()
	}()
	select {
	case <-d.Started():
		if err := startupSignal.Ready(); err != nil {
			fmt.Fprintf(os.Stderr, "daemon readiness signal error: %v\n", err)
		}
	case err := <-startResult:
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			startupSignal.AlreadyRunning()
		} else {
			startupSignal.Failed(err)
		}
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		exit(1)
	}
	if err := <-startResult; err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		exit(1)
	}
}

func daemonPreflight() (string, error) {
	if err := config.ValidateInstance(); err != nil {
		return "", err
	}
	if err := config.ValidateInstanceRouting(); err != nil {
		return "", err
	}
	socketPath := config.SocketPath()
	if err := config.ValidateDaemonIsolation(socketPath); err != nil {
		return "", err
	}
	return socketPath, nil
}

func runDaemonEnsure() {
	binaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon ensure error: resolve executable: %v\n", err)
		exit(1)
	}
	result, err := daemonctl.Ensure(context.Background(), binaryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon ensure error: %v\n", err)
		exit(1)
	}
	printJSON(result)
}

const stopTerminalsFlag = "--terminals"

func runDaemonStop(endTerminals bool) {
	result, err := daemonctl.Stop(config.PIDPath(), "stop")
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon stop error: %v\n", err)
		exit(1)
	}
	if result.StopNoteErr != nil {
		fmt.Fprintf(os.Stderr, "daemon stop: %v; the next daemon start will record this stop as an unexpected exit\n", result.StopNoteErr)
	}
	if result.Stopped {
		if result.Forced {
			fmt.Printf("stopped daemon (pid %d) (force-killed)\n", result.PID)
		} else {
			fmt.Printf("stopped daemon (pid %d)\n", result.PID)
		}
	} else {
		fmt.Printf("daemon %s\n", result.Note)
	}
	if endTerminals {
		if err := stopTerminals(os.Stdout, config.DataDir()); err != nil {
			fmt.Fprintf(os.Stderr, "daemon stop: %v\n", err)
			exit(1)
		}
		return
	}
	if pids := runningTerminalRuntimes(config.DataDir()); len(pids) > 0 {
		fmt.Printf("terminals keep running in pids %v so they come back with the daemon; `attn daemon stop %s` ends them\n", pids, stopTerminalsFlag)
	}
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func parseInterspersedFlagArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	rest := args
	for {
		if err := parseFlags(fs, rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
}

func printJSON(v interface{}) {
	if err := fprintJSON(os.Stdout, v); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding json: %v\n", err)
		exit(1)
	}
}

func fprintJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func openAppWithDeepLink() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		exit(1)
	}

	label := ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-s" && i+1 < len(args) {
			label = args[i+1]
			break
		}
	}
	if label == "" {
		label = filepath.Base(cwd)
	}

	deepLink := fmt.Sprintf("%s://spawn?cwd=%s&label=%s",
		config.DeepLinkScheme(),
		url.QueryEscape(cwd),
		url.QueryEscape(label))

	if err := launchDeepLink(deepLink); err != nil {
		fmt.Fprintf(os.Stderr, "error opening app: %v\n", err)
		exit(1)
	}
}

func runProbeTUI() {
	fs := flag.NewFlagSet("_probe-tui", flag.ExitOnError)
	styleFlag := fs.String("style", "", "agent vocabulary to mirror: codex or claude")
	interval := fs.Duration("interval", 500*time.Millisecond, "frame repaint interval")
	if err := parseFlags(fs, os.Args[2:]); err != nil {
		exit(2)
	}

	style, err := probetui.ParseStyle(*styleFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn _probe-tui: %v\n", err)
		exit(2)
	}

	size := func() (int, int, error) {
		ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
		if err != nil {
			return 0, 0, fmt.Errorf("get terminal size: %w", err)
		}
		return int(ws.Col), int(ws.Row), nil
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	ctx, cancel := context.WithCancel(context.Background())
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-term
		cancel()
	}()
	defer signal.Stop(term)

	if err := probetui.Run(ctx, os.Stdout, style, size, winch, *interval); err != nil {
		fmt.Fprintf(os.Stderr, "attn _probe-tui: %v\n", err)
		exit(1)
	}
}
