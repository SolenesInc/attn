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
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/probetui"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
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

func runPTYWorker() {
	fs := flag.NewFlagSet("pty-worker", flag.ExitOnError)
	var cfg ptyworker.Config
	var cols int
	var rows int
	var approvalRoute string
	fs.StringVar(&cfg.DaemonInstanceID, "daemon-instance-id", "", "daemon instance id")
	fs.StringVar(&cfg.SessionID, "session-id", "", "session id")
	fs.StringVar(&cfg.Agent, "agent", "", "session agent")
	fs.StringVar(&cfg.CWD, "cwd", "", "working directory")
	fs.IntVar(&cols, "cols", 80, "terminal cols")
	fs.IntVar(&rows, "rows", 24, "terminal rows")
	fs.StringVar(&cfg.Model, "model", "", "recorded agent model")
	fs.StringVar(&cfg.Effort, "effort", "", "recorded reasoning effort")
	fs.StringVar(&cfg.Label, "label", "", "session label")
	fs.StringVar(&cfg.ResumeSessionID, "resume-session-id", "", "resume session id")
	fs.BoolVar(&cfg.ResumePicker, "resume-picker", false, "resume picker")
	fs.BoolVar(&cfg.YoloMode, "yolo-mode", false, "launch agent in yolo mode")
	fs.StringVar(&approvalRoute, "approval-route", "", "effective approval route recorded for daemon recovery")
	fs.StringVar(&cfg.ThemeForeground, "theme-foreground", "", "terminal foreground color seeded for OSC 10/11/12 queries")
	fs.StringVar(&cfg.ThemeBackground, "theme-background", "", "terminal background color seeded for OSC 10/11/12 queries")
	fs.StringVar(&cfg.ThemeCursor, "theme-cursor", "", "terminal cursor color seeded for OSC 10/11/12 queries")
	var themeANSIPaletteJSON string
	fs.StringVar(&themeANSIPaletteJSON, "theme-ansi-palette-json", "", "JSON array of the 16 terminal ANSI palette colors")
	fs.StringVar(&cfg.Executable, "executable", "", "selected agent executable override")
	fs.StringVar(&cfg.ClaudeExecutable, "claude-executable", "", "claude executable override")
	fs.StringVar(&cfg.CodexExecutable, "codex-executable", "", "codex executable override")
	var unattendedLaunchJSON string
	fs.BoolVar(&cfg.AgentLoginShell, "agent-login-shell", false, "run the prepared agent through a login shell")
	fs.Func("agent-arg", "prepared agent argument (repeat for each argument)", func(value string) error { cfg.AgentArgv = append(cfg.AgentArgv, value); return nil })
	fs.Func("agent-env", "prepared agent environment entry (repeat for each entry)", func(value string) error { cfg.AgentEnv = append(cfg.AgentEnv, value); return nil })
	fs.StringVar(&unattendedLaunchJSON, "unattended-launch-json", "", "immutable unattended launch contract as JSON")
	fs.StringVar(&cfg.ExternalCWD, "external-cwd", "", "external plugin driver working directory")
	fs.StringVar(&cfg.AdoptHandoff, "adopt-handoff", "", "handoff file left by the worker image this one replaces")
	fs.IntVar(&cfg.AdoptPtmxFD, "adopt-ptmx-fd", 0, "inherited pty master descriptor")
	fs.IntVar(&cfg.AdoptListenerFD, "adopt-listener-fd", 0, "inherited unix listener descriptor")
	var readyFD int
	fs.IntVar(&readyFD, "ready-fd", 0, "inherited descriptor the worker writes to once it accepts connections")
	fs.StringVar(&cfg.RegistryPath, "registry-path", "", "registry path")
	fs.StringVar(&cfg.SocketPath, "socket-path", "", "socket path")
	fs.StringVar(&cfg.ControlToken, "control-token", "", "control token")
	fs.IntVar(&cfg.OwnerPID, "owner-pid", 0, "daemon owner pid")
	fs.StringVar(&cfg.OwnerStartedAt, "owner-started-at", "", "daemon owner started-at timestamp")
	fs.StringVar(&cfg.OwnerNonce, "owner-nonce", "", "daemon owner nonce")

	_ = parseFlags(fs, os.Args[2:])
	if approvalRoute != "" {
		cfg.ApprovalRoute = launchcontract.ApprovalRoute(approvalRoute)
		if !cfg.ApprovalRoute.Valid() {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --approval-route %q\n", approvalRoute)
			exit(1)
		}
	}
	if themeANSIPaletteJSON != "" {
		if err := json.Unmarshal([]byte(themeANSIPaletteJSON), &cfg.ThemeANSIPalette); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --theme-ansi-palette-json: %v\n", err)
			exit(1)
		}
	}

	if unattendedLaunchJSON != "" {
		if err := json.Unmarshal([]byte(unattendedLaunchJSON), &cfg.UnattendedLaunch); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --unattended-launch-json: %v\n", err)
			exit(1)
		}
		if err := cfg.UnattendedLaunch.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --unattended-launch-json: %v\n", err)
			exit(1)
		}
	}

	if readyFD > 0 {
		syscall.CloseOnExec(readyFD)
		cfg.Ready = os.NewFile(uintptr(readyFD), "pty-worker-ready")
	}

	cfg.HostName = os.Getenv("ATTN_PTY_HOST_NAME")
	if daemonEnvJSON := os.Getenv("ATTN_PTY_DAEMON_ENV"); daemonEnvJSON != "" {
		if err := json.Unmarshal([]byte(daemonEnvJSON), &cfg.DaemonEnv); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid ATTN_PTY_DAEMON_ENV: %v\n", err)
			exit(1)
		}
	}
	if cols > 0 {
		if cols > 65535 {
			fmt.Fprintf(os.Stderr, "pty-worker error: --cols must be <= 65535 (got %d)\n", cols)
			exit(1)
		}
		cfg.Cols = uint16(cols)
	}
	if rows > 0 {
		if rows > 65535 {
			fmt.Fprintf(os.Stderr, "pty-worker error: --rows must be <= 65535 (got %d)\n", rows)
			exit(1)
		}
		cfg.Rows = uint16(rows)
	}
	cfg.Debug = config.DebugLevel() >= config.LogDebug
	cfg.Logf = func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "[pty-worker] "+format+"\n", args...)
	}

	if scrubbed := config.ScrubInheritedAgentSessionEnv(); len(scrubbed) > 0 {
		cfg.Logf("scrubbed inherited agent session env before startup: %v", scrubbed)
	}

	if err := ptyworker.Run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "pty-worker error: %v\n", err)
		exit(1)
	}
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
		refuseDaemonCommandArgs("stop", os.Args[3:])
		runDaemonStop()
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
  stop      stop the running daemon

Both act on the active instance's daemon; attn instance status names it.
`)
}

func runDaemon() {
	startupSignal, err := daemonctl.TakeStartupSignal()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
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

func runDaemonStop() {
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
		return
	}
	fmt.Printf("daemon %s\n", result.Note)
}

func runList() {
	warnIfDaemonVersionMismatch()
	c := client.New("")
	result, err := c.List("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding list: %v\n", err)
		exit(1)
	}
}

func detectPresence() (sessionID string, present bool) {
	if os.Getenv("ATTN_INSIDE_APP") != "1" {
		return "", false
	}
	return string(currentSessionOrExit()), true
}

func runPresence() {
	fs := flag.NewFlagSet("presence", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bareSessionID := fs.Bool("session-id", false, "print only the session id")
	bareSeedIDs := fs.Bool("seed-ids", false, "print only the ids of the seeds the session works on")
	if err := parseFlags(fs, os.Args[2:]); err != nil || fs.NArg() != 0 || (*bareSessionID && *bareSeedIDs) {
		fmt.Fprintln(os.Stderr, "usage: attn presence [--session-id | --seed-ids]")
		exit(2)
	}
	warnIfDaemonVersionMismatch()
	sessionID, present := detectPresence()
	switch {
	case *bareSessionID:
		printLinesOrExit([]string{sessionID})
		return
	case *bareSeedIDs:
		printLinesOrExit(seedsOfSessionOrExit(protocol.SessionID(sessionID)))
		return
	}
	if !present {
		fmt.Println("not running inside attn")
		exit(1)
	}
	if sessionID == "" {
		fmt.Println("running inside attn")
		return
	}
	fmt.Printf("running inside attn (session %s)\n", sessionID)
}

func printLinesOrExit(ids []string) {
	ids = slices.DeleteFunc(ids, func(id string) bool { return id == "" })
	if len(ids) == 0 {
		exit(1)
	}
	for _, id := range ids {
		fmt.Println(id)
	}
}

func seedsOfSessionOrExit(sessionID protocol.SessionID) []string {
	if sessionID == "" {
		return nil
	}
	seedIDs, err := seedsOfSession(client.New(""), sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn: resolve the session's seeds: %v\n", err)
		exit(1)
	}
	return seedIDs
}

func seedsOfSession(c *client.Client, sessionID protocol.SessionID) ([]string, error) {
	listed, err := c.List("")
	if err != nil {
		return nil, err
	}
	var seedIDs []string
	for _, session := range listed.Sessions {
		if session.ID == sessionID && protocol.Deref(session.SeedID) != "" {
			seedIDs = append(seedIDs, protocol.Deref(session.SeedID))
		}
	}
	seeds, err := c.SeedList(sessionID, false, 0)
	if err != nil {
		return nil, err
	}
	for _, seed := range seeds.Seeds {
		if seed.Tender != nil && protocol.Deref(seed.Tender.SessionID) == sessionID && seed.Status == garden.StatusGrowing && !slices.Contains(seedIDs, seed.ID) {
			seedIDs = append(seedIDs, seed.ID)
		}
	}
	return seedIDs, nil
}

func runDelegate() {
	if len(os.Args) >= 3 && os.Args[2] == "roles" {
		runDelegateRoles(os.Args[3:])
		return
	}
	if len(os.Args) == 3 && (os.Args[2] == "-h" || os.Args[2] == "--help") {
		writeDelegateHelp(os.Stdout)
		return
	}
	if len(os.Args) >= 3 && os.Args[2] == "status" {
		setCommandPath("delegate status")
		fs := flag.NewFlagSet("delegate status", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		profile := fs.String("profile", "", "")
		session := fs.String("session", "", "")
		positionals, err := parseInterspersedFlagArgs(fs, os.Args[3:])
		if errors.Is(err, flag.ErrHelp) {
			writeDelegateHelp(os.Stdout)
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "delegate status: %v\n", err)
			exit(2)
		}
		if len(positionals) != 1 {
			fmt.Fprintln(os.Stderr, "delegate status: usage: attn delegate status <request-or-operation-id> [--profile <name|id>]")
			exit(2)
		}
		sessionID := protocol.SessionID(strings.TrimSpace(*session))
		if sessionID == "" {
			sessionID = currentSessionOrExit()
		}
		result, err := client.New("").WithGardenProfile(strings.TrimSpace(*profile), sessionID).DelegationStatus(positionals[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "delegate status: %v\n", err)
			exit(1)
		}
		printJSON(result)
		return
	}

	args, err := parseDelegateArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		exit(2)
	}
	warnIfDaemonVersionMismatch()
	c := client.New("").WithGardenProfile(protocol.Deref(args.request.ProfileID), protocol.Deref(args.request.SourceSessionID))
	fmt.Fprintf(os.Stderr, "delegation request: request_id=%s\n", args.request.RequestID)
	operation, err := c.StartDelegation(args.request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		exit(1)
	}
	fmt.Fprintf(os.Stderr, "delegation accepted: request_id=%s operation_id=%s session_id=%s\n",
		operation.RequestID, operation.OperationID, operation.SessionID)
	operation, err = waitDelegationCLI(c, operation, args.request.RequestID, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		exit(1)
	}
	if operation.Result != nil && operation.Result.FirstTurnUnconfirmed != nil {
		fmt.Fprintf(os.Stderr, "delegate: %s\n", *operation.Result.FirstTurnUnconfirmed)
	}
	printJSON(operation.Result)
	fmt.Fprintln(os.Stderr, prompts.RenderText("delegation", "started", prompts.Values{}))
}

func waitDelegationCLI(
	c *client.Client,
	operation *protocol.DelegationOperation,
	requestID string,
	progress io.Writer,
) (*protocol.DelegationOperation, error) {
	lastProgress := ""
	operation, err := c.AwaitDelegation(operation, func(seen *protocol.DelegationOperation) {
		if seen.Progress != "" && seen.Progress != lastProgress {
			fmt.Fprintf(progress, "delegation progress: %s\n", seen.Progress)
			lastProgress = seen.Progress
		}
	})
	if err != nil {
		return nil, fmt.Errorf("outcome unknown; inspect or retry with --request-id %s: %w", requestID, err)
	}
	if operation.State == protocol.DelegationOperationStateFailed {
		message := "unknown failure"
		if operation.Failure != nil {
			message = operation.Failure.Message
		}
		return nil, fmt.Errorf("%s (request_id=%s)", message, operation.RequestID)
	}
	if operation.Result == nil {
		return nil, errors.New("completed operation has no result")
	}
	return operation, nil
}

func writeDelegateHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn delegate (--brief TEXT | --brief-file PATH | --seed ID)
       --cwd PATH [role/model options] [checkout options]

Every delegation has a seed. A brief creates one; --seed uses its current
assignment. The delegated agent reads the seed when it starts.

assignment:
  --brief TEXT / --brief-file PATH   create a seed for new work
  --seed ID                          use an existing seed
  --priority                         put the delegate's owed turns first
  --handover                         transfer that seed to a new agent
  -m TEXT                            optional handover note: the next step
  --authorization TEXT               how far the user lets this task go: the
                                     steps to take beyond the project's
                                     guidance and the steps to stop before,
                                     written as instructions to the agent.
                                     Saved on the seed; the opening delivers
                                     it. Without it, a delegation or handover
                                     keeps the seed's current words

folder and Git:
  --host NAME                        target outpost name/id; home selects the home
                                     defaults to the source session's machine
  --cwd PATH                         required folder/repository on that machine
  --reuse-checkout --branch NAME     use the checkout on this branch
  --new-worktree --branch NAME --from REF
                                     create branch and worktree
  --new-worktree --existing-branch NAME
                                     create worktree for a local branch
  --worktree-path PATH               optional new-worktree destination
  --allow-worktree-reuse             explicitly share an occupied checkout

Inside Git, choose one complete checkout form. Outside Git, cwd is enough.
Handover leaves the previous agent running. It needs no force/confirm flag.
Paths are expanded and checked on the target machine. On your own machine,
relative cwd is based on your shell's directory. An unknown or unreachable
host refuses before acceptance. Retries keep the accepted machine and path.

session options:
	--request-id <id>          stable retry key (generated and printed when omitted; op- is reserved)
  --agent <name>             configured prompt-capable built-in or plugin agent
  --model <name>             pin the model; "default" selects the harness default
  --role <id>                use a configured role and its default choice
  --choice <id>              use an alternative within --role
  --fallback                 use the configured unmatched-work fallback
  --provider <id>            provider for a plugin harness model
  --effort <level>           pin the agent's effort; the harness reports its levels
                             in Settings; defaults to medium when supported
  --name <text>              session name (max 48 chars; defaults from brief or seed title)
  --desktop <ref>            desktop of your profile for the new agent: its
                             shortcut digit (1-9), its name as shown
                             (case-insensitive) or its id; the agent opens
                             at the end of that desktop's row without changing
                             what you see. Defaults to the source session's
                             desktop.
  --source-session <id>      source session (defaults to the current session)
  --yolo                     bypass agent approval prompts

Only a handover's predecessor is exempt from checkout occupancy. Any unrelated
active session requires --allow-worktree-reuse.

discovery:
  attn delegate roles [--json]  complete active roles, choices, and fallback
  attn delegate roles --help    change the saved roles, with history and rollback

inspection:
  attn delegate status <request-or-operation-id>
`)
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

type delegateCLIArgs struct {
	request protocol.DelegateMessage
}

func parseDelegateArgs(args []string) (delegateCLIArgs, error) {
	fs := flag.NewFlagSet("delegate", flag.ContinueOnError)
	profile := fs.String("profile", "", "profile name or id outside an attn session")
	fs.SetOutput(io.Discard)
	briefText := fs.String("brief", "", "delegated task brief")
	briefFile := fs.String("brief-file", "", "file containing the delegated task brief")
	seedID := fs.String("seed", "", "existing seed containing the assignment")
	priority := fs.Bool("priority", false, "mark the delegate as priority")
	handover := fs.Bool("handover", false, "transfer an existing seed to the new agent")
	handoffNote := fs.String("m", "", "optional handover note")
	authorization := fs.String("authorization", "", "how far the user lets this task go, saved on the seed")
	ticketID := fs.String("ticket", "", "retired: delegations bind a seed, not a ticket")
	confirm := fs.Bool("confirm", false, "retired: went with --ticket")
	agentName := fs.String("agent", "", "target agent (defaults to the source session agent)")
	role := fs.String("role", "", "configured delegation role")
	choice := fs.String("choice", "", "choice within the role")
	fallback := fs.Bool("fallback", false, "configured unmatched-work fallback")
	provider := fs.String("provider", "", "plugin model provider")
	model := fs.String("model", "", "pin the delegated agent's model (alias or full id)")
	effort := fs.String("effort", "", "pin the delegated agent's effort")
	name := fs.String("name", "", "session name; defaults from the brief or seed title")
	desktop := fs.String("desktop", "", "desktop of the caller's profile: shortcut digit, name or id")
	sourceSessionID := fs.String("source-session", "", "source session id (defaults to the current session)")
	yolo := fs.Bool("yolo", false, "launch the target agent in yolo mode")
	host := fs.String("host", "", "target machine (defaults to the source session host; home selects the home)")
	cwd := fs.String("cwd", "", "working folder or repository on the target machine")
	reuseCheckout := fs.Bool("reuse-checkout", false, "use the checkout at cwd on its current branch")
	newWorktree := fs.Bool("new-worktree", false, "create a worktree")
	branch := fs.String("branch", "", "branch for reuse or new branch creation")
	existingBranch := fs.String("existing-branch", "", "existing local branch for a new worktree")
	worktreeStart := fs.String("from", "", "starting ref for a new branch")
	worktreePath := fs.String("worktree-path", "", "custom new-worktree destination")
	plot := fs.String("plot", "", "retired: use --seed")
	newWorkspace := fs.Bool("new-workspace", false, "retired: delegation placement is internal")
	workspaceID := fs.String("workspace", "", "retired: delegation placement is internal")
	worktreeBranch := fs.String("worktree", "", "retired: use --new-worktree --branch")
	worktreeRepo := fs.String("repo", "", "retired: --cwd supplies repository context")
	noWorktree := fs.Bool("no-worktree", false, "retired: use --reuse-checkout")
	preferencesRevision := fs.String("preferences-revision", "", "retired: preferences resolve when accepted")
	requestID := fs.String("request-id", "", "stable delegation request id")
	allowWorktreeReuse := fs.Bool("allow-worktree-reuse", false, "allow active sessions to share a worktree")
	if err := parseFlags(fs, args); err != nil {
		return delegateCLIArgs{}, err
	}
	if fs.NArg() != 0 {
		return delegateCLIArgs{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	modelPin := strings.TrimSpace(*model)
	if modelPin == "" && strings.TrimSpace(*role) == "" && !*fallback {
		return delegateCLIArgs{}, errors.New("--model is required (for example, --model gpt-5.6-sol or --model opus); --effort defaults to medium when supported")
	}

	present := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { present[f.Name] = true })
	if *fallback && *role != "" {
		return delegateCLIArgs{}, errors.New("--role and --fallback cannot be combined")
	}
	if *choice != "" && *role == "" {
		return delegateCLIArgs{}, errors.New("--choice requires --role")
	}
	var modelOverride, effortOverride, providerOverride *string
	if present["model"] {
		value := strings.TrimSpace(*model)
		if value == "default" {
			value = ""
		}
		modelOverride = &value
	}
	if present["effort"] {
		value := strings.TrimSpace(*effort)
		if value == "default" {
			value = ""
		}
		effortOverride = &value
	}
	if present["provider"] && *role == "" && !*fallback {
		return delegateCLIArgs{}, errors.New("--provider requires --role or --fallback; direct plugin delegation uses provider/model")
	}
	if present["provider"] {
		value := strings.TrimSpace(*provider)
		providerOverride = &value
	}
	source := strings.TrimSpace(*sourceSessionID)
	if source == "" {
		source = string(currentSessionOrExit())
	}
	if ticket := strings.TrimSpace(*ticketID); ticket != "" {
		return delegateCLIArgs{}, fmt.Errorf(
			"--ticket retired: plant the work and dispatch at it — `attn seed plant %q -m \"<brief>\"`, then `attn delegate --seed <seed-id> --cwd <path>`", ticket)
	}
	if *confirm {
		return delegateCLIArgs{}, errors.New("--confirm retired: use --handover to transfer a seed; use attn seed tend for manual ownership")
	}
	if strings.TrimSpace(*plot) != "" {
		return delegateCLIArgs{}, errors.New("--plot retired: use --seed <id> without a second brief")
	}
	if *newWorkspace || strings.TrimSpace(*workspaceID) != "" {
		return delegateCLIArgs{}, errors.New("--workspace and --new-workspace retired: pass the explicit working folder with --cwd")
	}
	if strings.TrimSpace(*worktreeBranch) != "" {
		return delegateCLIArgs{}, errors.New("--worktree retired: use --new-worktree --branch <name> --from <ref>")
	}
	if strings.TrimSpace(*worktreeRepo) != "" {
		return delegateCLIArgs{}, errors.New("--repo retired: --cwd supplies repository context")
	}
	if *noWorktree {
		return delegateCLIArgs{}, errors.New("--no-worktree retired: use --reuse-checkout --branch <name>")
	}
	if present["preferences-revision"] || strings.TrimSpace(*preferencesRevision) != "" {
		return delegateCLIArgs{}, errors.New("--preferences-revision retired: Attn snapshots current preferences when it accepts the request")
	}
	if strings.TrimSpace(*briefText) != "" && strings.TrimSpace(*briefFile) != "" {
		return delegateCLIArgs{}, errors.New("pass only one of --brief or --brief-file")
	}
	brief := strings.TrimSpace(*briefText)
	if strings.TrimSpace(*briefFile) != "" {
		content, err := os.ReadFile(*briefFile)
		if err != nil {
			return delegateCLIArgs{}, fmt.Errorf("read brief file: %w", err)
		}
		brief = strings.TrimSpace(string(content))
	}
	seed := strings.TrimSpace(*seedID)
	if (brief == "") == (seed == "") {
		return delegateCLIArgs{}, errors.New("pass exactly one of --brief, --brief-file, or --seed")
	}
	if *handover && seed == "" {
		return delegateCLIArgs{}, errors.New("--handover requires --seed")
	}
	if strings.TrimSpace(*handoffNote) != "" && !*handover {
		return delegateCLIArgs{}, errors.New("-m requires --handover")
	}

	customCWD := strings.TrimSpace(*cwd)
	if customCWD == "" {
		return delegateCLIArgs{}, errors.New("--cwd is required")
	}
	cwdBase, err := os.Getwd()
	if err != nil {
		return delegateCLIArgs{}, fmt.Errorf("resolve caller working directory: %w", err)
	}
	requestedBranch := strings.TrimSpace(*branch)
	localBranch := strings.TrimSpace(*existingBranch)
	startingFrom := strings.TrimSpace(*worktreeStart)
	customWorktreePath := strings.TrimSpace(*worktreePath)
	stableRequestID := strings.TrimSpace(*requestID)
	if stableRequestID == "" {
		stableRequestID = uuid.NewString()
	}
	if *reuseCheckout && *newWorktree {
		return delegateCLIArgs{}, errors.New("choose only one of --reuse-checkout or --new-worktree")
	}
	if !*reuseCheckout && !*newWorktree {
		for _, name := range []string{"branch", "existing-branch", "from", "worktree-path", "allow-worktree-reuse"} {
			if present[name] {
				return delegateCLIArgs{}, fmt.Errorf("--%s requires --reuse-checkout or --new-worktree", name)
			}
		}
	}
	var checkout *protocol.DelegateCheckout
	if *reuseCheckout {
		if requestedBranch == "" {
			return delegateCLIArgs{}, errors.New("--reuse-checkout requires --branch")
		}
		if localBranch != "" || startingFrom != "" || customWorktreePath != "" {
			return delegateCLIArgs{}, errors.New("--reuse-checkout accepts --branch only")
		}
		checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: requestedBranch}
	}
	if *newWorktree {
		if (requestedBranch == "") == (localBranch == "") {
			return delegateCLIArgs{}, errors.New("--new-worktree requires either --branch <name> --from <ref> or --existing-branch <name>")
		}
		if requestedBranch != "" && startingFrom == "" {
			return delegateCLIArgs{}, errors.New("--new-worktree --branch requires --from")
		}
		if localBranch != "" && startingFrom != "" {
			return delegateCLIArgs{}, errors.New("--new-worktree --existing-branch does not accept --from")
		}
		kind := protocol.DelegateCheckoutKindNewWorktree
		selectedBranch := requestedBranch
		if localBranch != "" {
			kind = protocol.DelegateCheckoutKindExistingBranchWorktree
			selectedBranch = localBranch
		}
		checkout = &protocol.DelegateCheckout{Kind: kind, Branch: selectedBranch}
		if startingFrom != "" {
			checkout.From = protocol.Ptr(startingFrom)
		}
		if customWorktreePath != "" {
			checkout.Path = protocol.Ptr(customWorktreePath)
		}
	}
	if !*newWorktree && customWorktreePath != "" {
		return delegateCLIArgs{}, errors.New("--worktree-path requires --new-worktree")
	}

	assignment := protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr(brief)}
	if seed != "" {
		assignment = protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seed)}
		if *handover {
			assignment.Handover = &protocol.DelegateHandover{}
			if note := strings.TrimSpace(*handoffNote); note != "" {
				assignment.Handover.Note = protocol.Ptr(note)
			}
		}
	}
	request := protocol.DelegateMessage{Cmd: protocol.CmdDelegate, ProfileID: protocol.Ptr(strings.TrimSpace(*profile)), RequestID: stableRequestID, Assignment: assignment, Cwd: customCWD, CwdBase: &cwdBase, Checkout: checkout, Host: nonEmpty(strings.TrimSpace(*host))}
	if *priority {
		request.Priority = protocol.Ptr(true)
	}
	if text := strings.TrimSpace(*authorization); text != "" {
		request.Authorization = protocol.Ptr(text)
	}
	if source != "" {
		request.SourceSessionID = protocol.Ptr(protocol.SessionID(source))
	}
	if value := strings.TrimSpace(*agentName); value != "" {
		request.Agent = protocol.Ptr(value)
	}
	request.Model = modelOverride
	request.Effort = effortOverride
	request.Provider = providerOverride
	if value := strings.TrimSpace(*role); value != "" {
		request.Role = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(*choice); value != "" {
		request.Choice = protocol.Ptr(value)
	}
	if *fallback {
		request.Fallback = protocol.Ptr(true)
	}
	if value := strings.TrimSpace(*name); value != "" {
		request.Label = protocol.Ptr(value)
	}
	if present["desktop"] {
		value := strings.TrimSpace(*desktop)
		if value == "" {
			return delegateCLIArgs{}, errors.New("--desktop needs a shortcut digit (1-9), a desktop name or a desktop id")
		}
		request.Desktop = protocol.Ptr(value)
	}
	if *yolo {
		request.YoloMode = protocol.Ptr(true)
	}
	if *allowWorktreeReuse {
		request.AllowWorktreeReuse = protocol.Ptr(true)
	}
	return delegateCLIArgs{request: request}, nil
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

func runWrapper() {
	if os.Getenv("ATTN_DAEMON_MANAGED") == "1" {
		agentName := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_AGENT")))
		if agentName == "" {
			agentName = "codex"
		}
		runAgentDirectly(agentName)
		return
	}

	openAppWithDeepLink()
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

func nonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
