package main

import (
	"context"
	"encoding/base64"
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

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/probetui"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"golang.org/x/sys/unix"
)

type hookInput struct {
	SessionID        string           `json:"session_id"`
	TranscriptPath   string           `json:"transcript_path"`
	Prompt           string           `json:"prompt"`
	ToolName         string           `json:"tool_name"`
	ToolInput        json.RawMessage  `json:"tool_input"`
	ToolResponse     json.RawMessage  `json:"tool_response"`
	CWD              string           `json:"cwd"`
	BackgroundTasks  []backgroundTask `json:"background_tasks"`
	SessionCrons     []sessionCron    `json:"session_crons"`
	PermissionMode   string           `json:"permission_mode"`
	Message          string           `json:"message"`
	NotificationType string           `json:"notification_type"`
	AgentID          string           `json:"agent_id"`
	ErrorType        string           `json:"error_type"`
	ErrorMessage     string           `json:"error_message"`
	Trigger          string           `json:"trigger"`
}

type backgroundTask struct {
	Type        string `json:"type"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

type sessionCron struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`
	Recurring bool   `json:"recurring"`
	Prompt    string `json:"prompt"`
}

func main() {
	if isProtocolVersionCommand(os.Args) {
		runProtocolVersion()
		return
	}

	if isBuildInfoJSONCommand(os.Args) {
		runBuildInfoJSON()
		return
	}

	if isVersionCommand(os.Args) {
		switch {
		case os.Args[1] == "--version":
			fmt.Println(buildinfo.Version)
		case len(os.Args) > 2 && os.Args[2] == "--json":
			runBuildInfoJSON()
		default:
			runVersion()
		}
		return
	}

	if len(os.Args) >= 2 && (os.Args[1] == "profile" || os.Args[1] == "profile-env") {
		fmt.Fprintln(os.Stderr, renamedProfileCommandMessage(os.Args[1:]))
		os.Exit(2)
	}

	if len(os.Args) >= 2 && os.Args[1] == "instance-env" {
		runInstanceEnv()
		return
	}

	daemonStart := len(os.Args) == 2 && os.Args[1] == "daemon"
	if !daemonStart {
		if err := config.ValidateInstance(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !isInstanceGroupCommand(os.Args) {
			if err := config.ValidateInstanceRouting(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}

	if len(os.Args) < 2 {
		maybePrintInstanceBanner()
		runWrapper()
		return
	}

	switch os.Args[1] {
	case "daemon":
		maybePrintInstanceBanner()
		runDaemonCommand()
	case "pty-worker":
		runPTYWorker()
	case "automation":
		maybePrintInstanceBanner()
		runAutomationCommand()
	case "preflight":
		maybePrintInstanceBanner()
		runPreflight()
	case "pr":
		runPRCommand()
	case "plugin":
		maybePrintInstanceBanner()
		runPluginCommand()
	case "list":
		maybePrintInstanceBanner()
		runList()
	case "presence":
		runPresence()
	case "skill":
		runSkill()
	case "prompts":
		runPrompts()
	case "activity":
		maybePrintInstanceBanner()
		runActivity()
	case "delegate":
		maybePrintInstanceBanner()
		runDelegate()
	case "ticket":
		maybePrintInstanceBanner()
		runTicket()
	case "session":
		maybePrintInstanceBanner()
		runSession()
	case "terminal":
		maybePrintInstanceBanner()
		runTerminal()
	case "agent":
		maybePrintInstanceBanner()
		runAgent()
	case "settings":
		runSettings()
	case "state":
		maybePrintInstanceBanner()
		runState()
	case "debug":
		maybePrintInstanceBanner()
		runDebug()
	case "db":
		maybePrintInstanceBanner()
		runDB()
	case "bus":
		maybePrintInstanceBanner()
		runBus()
	case "enrollment":
		maybePrintInstanceBanner()
		runEnrollment()
	case "seed":
		maybePrintInstanceBanner()
		runSeed()
	case "crew":
		maybePrintInstanceBanner()
		runCrew()
	case "handoff":
		maybePrintInstanceBanner()
		runHandoff(os.Args[2:])
	case "doc":
		maybePrintInstanceBanner()
		runDoc()
	case "automode":
		maybePrintInstanceBanner()
		runAutoMode()
	case "journal":
		maybePrintInstanceBanner()
		runJournal()
	case "vision-check":
		runVisionCheck()
	case "ask":
		maybePrintInstanceBanner()
		runAsk()
	case "turn":
		maybePrintInstanceBanner()
		runTurn()
	case "conversation":
		maybePrintInstanceBanner()
		runConversation()
	case "worktree":
		maybePrintInstanceBanner()
		runWorktree()
	case "instance":
		runInstance()
	case "open":
		maybePrintInstanceBanner()
		runOpen()
	case "callout":
		runCallout()
	case "browser":
		maybePrintInstanceBanner()
		runBrowser()
	case "help", "-h", "--help":
		runHelp()
	case "_hook-stop":
		runHookStop()
	case "_hook-session-start":
		runHookSessionStart()
	case "_hook-notification":
		runHookNotification()
	case "_hook-stop-failure":
		runHookStopFailure()
	case "_hook-compact":
		runHookCompact()
	case "_hook-state":
		runHookState()
	case "_hook-todo":
		// Claude sessions launched before todo tracking was removed still call
		// this hook until they restart; exiting 0 keeps it from showing an error.
	case "_hook-tool-use":
		runHookToolUse()
	case "_probe-tui":
		runProbeTUI()
	default:
		if len(os.Args[1]) > 0 && os.Args[1][0] == '-' {
			maybePrintInstanceBanner()
			runWrapper()
		} else {
			fmt.Fprintf(os.Stderr, "attn %s: unknown command %q\n\n", buildinfo.Version, os.Args[1])
			writeHelp(os.Stderr)
			os.Exit(1)
		}
	}
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

func isInstanceGroupCommand(args []string) bool {
	return len(args) >= 2 && args[1] == "instance"
}

func isVersionCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "--version", "version":
		return true
	default:
		return false
	}
}

func isProtocolVersionCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[1] == "--protocol-version"
}

func isBuildInfoJSONCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[1] == "--build-info-json"
}

func runVersion() {
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
		"channel":           buildinfo.Channel,
		"buildTime":         buildinfo.BuildTime,
		"sourceFingerprint": buildinfo.SourceFingerprint,
		"gitCommit":         buildinfo.GitCommit,
		"dirty":             buildinfo.Dirty,
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
	fs.StringVar(&cfg.CopilotExecutable, "copilot-executable", "", "copilot executable override")
	var unattendedLaunchJSON string
	fs.BoolVar(&cfg.AgentLoginShell, "agent-login-shell", false, "run the prepared agent through a login shell")
	fs.Func("agent-arg", "prepared agent argument (repeat for each argument)", func(value string) error { cfg.AgentArgv = append(cfg.AgentArgv, value); return nil })
	fs.Func("agent-env", "prepared agent environment entry (repeat for each entry)", func(value string) error { cfg.AgentEnv = append(cfg.AgentEnv, value); return nil })
	fs.StringVar(&unattendedLaunchJSON, "unattended-launch-json", "", "immutable unattended launch contract as JSON")
	fs.StringVar(&cfg.ExternalCWD, "external-cwd", "", "external plugin driver working directory")
	fs.StringVar(&cfg.AdoptHandoff, "adopt-handoff", "", "handoff file left by the worker image this one replaces")
	fs.IntVar(&cfg.AdoptPtmxFD, "adopt-ptmx-fd", 0, "inherited pty master descriptor")
	fs.IntVar(&cfg.AdoptListenerFD, "adopt-listener-fd", 0, "inherited unix listener descriptor")
	fs.StringVar(&cfg.RegistryPath, "registry-path", "", "registry path")
	fs.StringVar(&cfg.SocketPath, "socket-path", "", "socket path")
	fs.StringVar(&cfg.ControlToken, "control-token", "", "control token")
	fs.IntVar(&cfg.OwnerPID, "owner-pid", 0, "daemon owner pid")
	fs.StringVar(&cfg.OwnerStartedAt, "owner-started-at", "", "daemon owner started-at timestamp")
	fs.StringVar(&cfg.OwnerNonce, "owner-nonce", "", "daemon owner nonce")

	_ = fs.Parse(os.Args[2:])
	if approvalRoute != "" {
		cfg.ApprovalRoute = launchcontract.ApprovalRoute(approvalRoute)
		if !cfg.ApprovalRoute.Valid() {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --approval-route %q\n", approvalRoute)
			os.Exit(1)
		}
	}
	if themeANSIPaletteJSON != "" {
		if err := json.Unmarshal([]byte(themeANSIPaletteJSON), &cfg.ThemeANSIPalette); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --theme-ansi-palette-json: %v\n", err)
			os.Exit(1)
		}
	}

	if unattendedLaunchJSON != "" {
		if err := json.Unmarshal([]byte(unattendedLaunchJSON), &cfg.UnattendedLaunch); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --unattended-launch-json: %v\n", err)
			os.Exit(1)
		}
		if err := cfg.UnattendedLaunch.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid --unattended-launch-json: %v\n", err)
			os.Exit(1)
		}
	}

	if daemonEnvJSON := os.Getenv("ATTN_PTY_DAEMON_ENV"); daemonEnvJSON != "" {
		if err := json.Unmarshal([]byte(daemonEnvJSON), &cfg.DaemonEnv); err != nil {
			fmt.Fprintf(os.Stderr, "pty-worker error: invalid ATTN_PTY_DAEMON_ENV: %v\n", err)
			os.Exit(1)
		}
	}
	if cols > 0 {
		if cols > 65535 {
			fmt.Fprintf(os.Stderr, "pty-worker error: --cols must be <= 65535 (got %d)\n", cols)
			os.Exit(1)
		}
		cfg.Cols = uint16(cols)
	}
	if rows > 0 {
		if rows > 65535 {
			fmt.Fprintf(os.Stderr, "pty-worker error: --rows must be <= 65535 (got %d)\n", rows)
			os.Exit(1)
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
		os.Exit(1)
	}
}

func runDaemonCommand() {
	if len(os.Args) < 3 {
		runDaemon()
		return
	}
	switch os.Args[2] {
	case "ensure":
		runDaemonEnsure()
	case "stop":
		runDaemonStop()
	default:
		fmt.Fprintf(os.Stderr, "unknown daemon subcommand %q (expected: ensure, stop)\n", os.Args[2])
		os.Exit(1)
	}
}

func runDaemon() {
	startupSignal, err := daemonctl.TakeStartupSignal()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	socketPath, err := daemonPreflight()
	if err != nil {
		startupSignal.Failed(err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
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
		os.Exit(1)
	}
	if err := <-startResult; err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
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
		os.Exit(1)
	}
	result, err := daemonctl.Ensure(context.Background(), binaryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon ensure error: %v\n", err)
		os.Exit(1)
	}
	printJSON(result)
}

func runDaemonStop() {
	result, err := daemonctl.Stop(config.PIDPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon stop error: %v\n", err)
		os.Exit(1)
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
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding list: %v\n", err)
		os.Exit(1)
	}
}

func detectPresence() (sessionID string, present bool) {
	if os.Getenv("ATTN_INSIDE_APP") != "1" {
		return "", false
	}
	return string(currentSessionOrExit()), true
}

func runPresence() {
	warnIfDaemonVersionMismatch()
	sessionID, present := detectPresence()
	if !present {
		fmt.Println("not running inside attn")
		os.Exit(1)
	}
	if sessionID == "" {
		fmt.Println("running inside attn")
		return
	}
	fmt.Printf("running inside attn (session %s)\n", sessionID)
}

func runHelp() {
	writeHelp(os.Stdout)
}

func writeHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn <command>

commands:
  presence                          check whether the current shell runs inside attn
  agent list                        name every session running here
  agent peek <session-or-member>    read a session without interrupting it
  agent msg <target> "text"            notify a live session, crew member or seed's tender
  agent inbox [message-id]              read durable agent notifications
  agent msg-status <message-id>         inspect a sent peer message
  terminal <command>                open, read, type into, wait on and close terminals
  session <command>                 read the session ledger and a session's conversation
  state explain <id>                replay why a session's state is what it is
  delegate --brief-file <path> --cwd <path>    start another agent on a new seed
  delegate --seed <id> --cwd <path>            start another agent on an existing seed
  journal append --entry <text>     serialized append to the daily notebook journal
  open <file.md|seed-id> [--session <id>]   show a document in attn
  callout <anchors|show>            point at the app with a callout
  browser <command>                 open and control the in-app browser
  automation <command>              manage and run durable automations
  settings list|get|set             read and change daemon settings
  preflight                         diagnose tools, paths, routing, and launch settings
  pr <command>                      watch or inspect pull request readiness
  list                              list sessions and profiles
  activity [clear <id>]             what each agent is doing right now
  conversation <command>            list, keep forever or forget attn's conversation copies
  worktree <command>                every tracked worktree, the sweep and the keep pin
  ask [list|close]                  ask the user something they answer from attn
  turn open|settle                  put yourself in the user's queue, or take yourself out
  debug <command>                   probe debug artifacts (incidents, logs)
  db <command>                      database maintenance (restore from backup)
  bus <command>                     event bus: consumer cursors, lag, kill switch
  enrollment <command>              this data folder's enrollment: status, leave
  seed <command>                    the garden: plant, tend, harvest, note, ls
  crew <command>                    the crew roster: who exists, who is awake
  handoff -m "<letter>"             file this crew member's letter; the day turns over
  doc <command>                     document store: collections, documents, live queries
  vision-check <image> <question>   answer a question about an image (single LLM call)
  daemon <command>                  manage the daemon
	  daemon ensure|stop                ensure the daemon is running, or stop it
  instance <status|resolve|list>    show / resolve the active instance's resources
  instance-env <instance|--unset>   print shell commands for selecting an instance
  skill [--reference <name>|--list] print the bundled agent skill and its references
  prompts <command>                inspect prompt sources and scenario composition
  version [--json]                  print version, channel and commit
`)
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
		f := newSeedFlags("delegate status")
		positionals := f.parse("delegate status", os.Args[3:])
		if len(positionals) != 1 {
			fmt.Fprintln(os.Stderr, "delegate status: usage: attn delegate status <request-or-operation-id> [--profile <name|id>]")
			os.Exit(2)
		}
		result, err := f.client().DelegationStatus(positionals[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "delegate status: %v\n", err)
			os.Exit(1)
		}
		printJSON(result)
		return
	}

	args, err := parseDelegateArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		os.Exit(2)
	}
	warnIfDaemonVersionMismatch()
	c := client.New("").WithGardenProfile(protocol.Deref(args.request.ProfileID), protocol.Deref(args.request.SourceSessionID))
	fmt.Fprintf(os.Stderr, "delegation request: request_id=%s\n", args.request.RequestID)
	operation, err := c.StartDelegation(args.request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "delegation accepted: request_id=%s operation_id=%s session_id=%s\n",
		operation.RequestID, operation.OperationID, operation.SessionID)
	operation, err = waitDelegationCLI(c, operation, args.request.RequestID, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate: %v\n", err)
		os.Exit(1)
	}
	if operation.Result != nil && operation.Result.FirstTurnUnconfirmed != nil {
		fmt.Fprintf(os.Stderr, "delegate: %s\n", *operation.Result.FirstTurnUnconfirmed)
	}
	printJSON(operation.Result)
}

func waitDelegationCLI(
	c *client.Client,
	operation *protocol.DelegationOperation,
	requestID string,
	progress io.Writer,
) (*protocol.DelegationOperation, error) {
	lastProgress := ""
	for operation.State == protocol.DelegationOperationStateAccepted || operation.State == protocol.DelegationOperationStatePreparing {
		if operation.Progress != "" && operation.Progress != lastProgress {
			fmt.Fprintf(progress, "delegation progress: %s\n", operation.Progress)
			lastProgress = operation.Progress
		}
		time.Sleep(250 * time.Millisecond)
		next, err := c.DelegationStatus(operation.OperationID)
		if err != nil {
			return nil, fmt.Errorf("outcome unknown; inspect or retry with --request-id %s: %w", requestID, err)
		}
		operation = next
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
  -m TEXT                            optional handover note

folder and Git:
  --cwd PATH                         required working folder/repository
  --reuse-checkout --branch NAME     use the checkout on this branch
  --new-worktree --branch NAME --from REF
                                     create branch and worktree
  --new-worktree --existing-branch NAME
                                     create worktree for a local branch
  --worktree-path PATH               optional new-worktree destination
  --allow-worktree-reuse             explicitly share an occupied checkout

Inside Git, choose one complete checkout form. Outside Git, cwd is enough.
Handover leaves the previous agent running. It needs no force/confirm flag.

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
                             beside that desktop's active pane without changing
                             what you see. Defaults to beside the source
                             session.
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

func runJournal() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeJournalHelp(os.Stdout)
		return
	}
	warnIfDaemonVersionMismatch()
	switch os.Args[2] {
	case "append":
		if hasHelpFlag(os.Args[3:]) {
			writeJournalHelp(os.Stdout)
			return
		}
		runJournalAppend(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "journal: unknown command %q\n", os.Args[2])
		writeJournalHelp(os.Stderr)
		os.Exit(2)
	}
}

type journalAppendArgs struct {
	sessionID protocol.SessionID
	date      string
	entry     string
	jsonOut   bool
}

func parseJournalAppendArgs(args []string) (journalAppendArgs, error) {
	fs := flag.NewFlagSet("journal append", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	entryText := fs.String("entry", "", "journal entry markdown")
	entryFile := fs.String("entry-file", "", "file containing the journal entry markdown")
	date := fs.String("date", "", "journal date as YYYY-MM-DD (defaults to today)")
	sessionID := fs.String("session", "", "session id (optional; defaults to the current session)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return journalAppendArgs{}, err
	}
	if fs.NArg() != 0 {
		return journalAppendArgs{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if strings.TrimSpace(*entryText) != "" && strings.TrimSpace(*entryFile) != "" {
		return journalAppendArgs{}, errors.New("pass only one of --entry or --entry-file")
	}
	entry := strings.TrimSpace(*entryText)
	if strings.TrimSpace(*entryFile) != "" {
		content, err := os.ReadFile(*entryFile)
		if err != nil {
			return journalAppendArgs{}, fmt.Errorf("read entry file: %w", err)
		}
		entry = strings.TrimSpace(string(content))
	}
	if entry == "" {
		return journalAppendArgs{}, errors.New("--entry or --entry-file is required")
	}
	source := strings.TrimSpace(*sessionID)
	if source == "" {
		source = string(currentSessionOrExit())
	}
	return journalAppendArgs{
		sessionID: protocol.SessionID(source),
		date:      strings.TrimSpace(*date),
		entry:     entry,
		jsonOut:   *jsonOutput,
	}, nil
}

func runJournalAppend(args []string) {
	parsed, err := parseJournalAppendArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "journal append: %v\n", err)
		writeJournalHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").AppendJournal(parsed.sessionID, parsed.date, parsed.entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "journal append: %v\n", err)
		os.Exit(1)
	}
	if parsed.jsonOut {
		printJSON(result)
		return
	}
	fmt.Printf("appended to %s\n", result.RelPath)
}

func writeJournalHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn journal <command>

commands:
  append (--entry <text> | --entry-file <path>) [--date YYYY-MM-DD] [--session <id>] [--json]
        serialized append to the notebook's daily journal (journal/<date>.md)
        through the daemon — the contention-safe way an agent writes the
        journal, instead of editing the file directly with its own file-edit
        tools (which races other agents writing the same file).
        date defaults to today. --json prints rel_path and hash.

The session defaults to the current session.
`)
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
	cwd := fs.String("cwd", "", "working folder or repository")
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
	if err := fs.Parse(args); err != nil {
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
	customCWD, err := filepath.Abs(git.CanonicalizePath(customCWD))
	if err != nil {
		return delegateCLIArgs{}, fmt.Errorf("resolve --cwd %q: %w", *cwd, err)
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
	request := protocol.DelegateMessage{Cmd: protocol.CmdDelegate, ProfileID: protocol.Ptr(strings.TrimSpace(*profile)), RequestID: stableRequestID, Assignment: assignment, Cwd: customCWD, Checkout: checkout}
	if *priority {
		request.Priority = protocol.Ptr(true)
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

func parseOpenArgs(args []string) (rawPath string, sessionFlag string, profileFlag string, err error) {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id (defaults to the current session, then the selected session)")

	profileID := fs.String("profile", "", "profile name or id for a seed outside attn")
	var positionals []string
	rest := args
	for {
		if perr := fs.Parse(rest); perr != nil {
			return "", "", "", perr
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}

	if len(positionals) == 0 {
		return "", "", "", fmt.Errorf("missing <file.md|seed-id> argument")
	}
	if len(positionals) > 1 {
		return "", "", "", fmt.Errorf("unexpected extra arguments: %v", positionals[1:])
	}
	return strings.TrimSpace(positionals[0]), strings.TrimSpace(*sessionID), strings.TrimSpace(*profileID), nil
}

func isSeedOpenTarget(target string) bool {
	return strings.HasPrefix(strings.TrimSpace(target), "s-")
}

func runOpen() {
	warnIfDaemonVersionMismatch()
	rawPath, sessionFlag, profileFlag, err := parseOpenArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn open: %v\nusage: attn open <file.md|seed-id> [--session <id>]\n", err)
		os.Exit(1)
	}

	resolvedSession := protocol.SessionID(sessionFlag)
	if resolvedSession == "" {
		resolvedSession = currentSessionOrExit()
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if isSeedOpenTarget(rawPath) {
		if err := c.WithGardenProfile(profileFlag, "").OpenSeed(rawPath, resolvedSession); err != nil {
			fmt.Fprintf(os.Stderr, "open: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("opened %s\n", rawPath)
		return
	}
	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: resolve path: %v\n", err)
		os.Exit(1)
	}
	if err := c.OpenMarkdown(absPath, resolvedSession); err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("opened %s\n", absPath)
}

func parseInterspersedFlagArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
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

func browserSessionID(sessionFlag string) string {
	if sessionID := strings.TrimSpace(sessionFlag); sessionID != "" {
		return sessionID
	}
	return string(currentSessionOrExit())
}

func printBrowserUsage(w io.Writer) {
	fmt.Fprint(w, `usage: attn browser <command>

commands:
  open <url> [--session <id>]                 open or navigate the browser tile
  snapshot [--session <id>]                   print a semantic page snapshot
  find --using <strategy> --value <value>      find an element and return its reference
  wait --using <strategy> --value <value>      wait for attached/visible/hidden/detached
  click --selector <css>|--element <id>        click an element
  type --selector <css>|--element <id> --text  replace an input's value
  back | forward | reload                     navigate browser history
  press --text <key>                          send a keyboard key
  scroll [--x <px>] [--y <px>]                scroll the page
  cookies                                     list cookies for the current page
  command <action> [--params <json>]           call the WebDriver-shaped API directly
  screenshot [path] [--session <id>]          save a PNG (default: attn-browser.png)
  pdf [path] [--params <json>]                 save a PDF (default: attn-browser.pdf)
`)
}

func encodeBrowserParams(params map[string]interface{}) string {
	data, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func runBrowser() {
	warnIfDaemonVersionMismatch()
	if len(os.Args) < 3 {
		printBrowserUsage(os.Stderr)
		os.Exit(1)
	}

	subcommand := os.Args[2]
	fs := flag.NewFlagSet("browser "+subcommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionFlag := fs.String("session", "", "session id (defaults to the current session, then the selected session)")
	selector := fs.String("selector", "", "CSS selector")
	text := fs.String("text", "", "text to enter")
	paramsJSON := fs.String("params", "{}", "JSON object with action parameters")
	using := fs.String("using", "", "locator strategy")
	value := fs.String("value", "", "locator or form value")
	name := fs.String("name", "", "accessible name or cookie name")
	element := fs.String("element", "", "WebDriver element reference id")
	state := fs.String("state", "attached", "wait state")
	timeout := fs.Int("timeout", 5000, "timeout in milliseconds")
	all := fs.Bool("all", false, "return all matching elements")
	deltaX := fs.Int("x", 0, "horizontal scroll delta")
	deltaY := fs.Int("y", 0, "vertical scroll delta")
	positionals, err := parseInterspersedFlagArgs(fs, os.Args[3:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn browser %s: %v\n", subcommand, err)
		printBrowserUsage(os.Stderr)
		os.Exit(1)
	}

	sessionID := protocol.SessionID(browserSessionID(*sessionFlag))
	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	textSet := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "text" {
			textSet = true
		}
	})
	switch subcommand {
	case "open":
		if len(positionals) != 1 {
			fmt.Fprintln(os.Stderr, "attn browser open: expected exactly one <url>")
			os.Exit(1)
		}
		if err := c.OpenBrowser(positionals[0], sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "browser open: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("opened %s\n", positionals[0])
	case "snapshot":
		if len(positionals) != 0 {
			fmt.Fprintln(os.Stderr, "attn browser snapshot: unexpected arguments")
			os.Exit(1)
		}
		data, err := c.BrowserControl("snapshot", "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser snapshot: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "find":
		if len(positionals) != 0 || strings.TrimSpace(*using) == "" || strings.TrimSpace(*value) == "" {
			fmt.Fprintln(os.Stderr, "attn browser find: --using and --value are required")
			os.Exit(1)
		}
		params := map[string]interface{}{"using": *using, "value": *value}
		if *name != "" {
			params["name"] = *name
		}
		action := "find_element"
		if *all {
			action = "find_elements"
		}
		data, err := c.BrowserCommand(action, encodeBrowserParams(params), "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser find: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "wait":
		if len(positionals) != 0 || strings.TrimSpace(*using) == "" || strings.TrimSpace(*value) == "" {
			fmt.Fprintln(os.Stderr, "attn browser wait: --using and --value are required")
			os.Exit(1)
		}
		params := map[string]interface{}{"using": *using, "value": *value, "state": *state, "timeout": *timeout}
		if *name != "" {
			params["name"] = *name
		}
		data, err := c.BrowserCommand("wait_for", encodeBrowserParams(params), "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser wait: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "click":
		if len(positionals) != 0 || (strings.TrimSpace(*selector) == "" && strings.TrimSpace(*element) == "") {
			fmt.Fprintln(os.Stderr, "attn browser click: --selector <css> or --element <id> is required")
			os.Exit(1)
		}
		var data string
		if strings.TrimSpace(*element) != "" {
			data, err = c.BrowserCommand("click_element", encodeBrowserParams(map[string]interface{}{"element": *element}), "", "", sessionID)
		} else {
			data, err = c.BrowserControl("click", strings.TrimSpace(*selector), "", sessionID)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser click: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "type":
		if len(positionals) != 0 || (strings.TrimSpace(*selector) == "" && strings.TrimSpace(*element) == "") || !textSet {
			fmt.Fprintln(os.Stderr, "attn browser type: --selector <css> or --element <id>, plus --text, are required")
			os.Exit(1)
		}
		var data string
		if strings.TrimSpace(*element) != "" {
			params := encodeBrowserParams(map[string]interface{}{"element": *element})
			if _, err = c.BrowserCommand("clear_element", params, "", "", sessionID); err == nil {
				data, err = c.BrowserCommand("send_keys_to_element", encodeBrowserParams(map[string]interface{}{"element": *element, "text": *text}), "", "", sessionID)
			}
		} else {
			data, err = c.BrowserControl("type", strings.TrimSpace(*selector), *text, sessionID)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser type: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "reload", "back", "forward":
		if len(positionals) != 0 {
			fmt.Fprintln(os.Stderr, "attn browser reload: unexpected arguments")
			os.Exit(1)
		}
		data, err := c.BrowserControl(subcommand, "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser reload: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "press":
		if len(positionals) != 0 || !textSet || *text == "" {
			fmt.Fprintln(os.Stderr, "attn browser press: --text <key> is required")
			os.Exit(1)
		}
		actions := []map[string]interface{}{{"type": "key", "id": "keyboard", "actions": []map[string]interface{}{{"type": "keyDown", "value": *text}, {"type": "keyUp", "value": *text}}}}
		data, err := c.BrowserCommand("perform_actions", encodeBrowserParams(map[string]interface{}{"actions": actions}), "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser press: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "scroll":
		if len(positionals) != 0 {
			fmt.Fprintln(os.Stderr, "attn browser scroll: unexpected arguments")
			os.Exit(1)
		}
		actions := []map[string]interface{}{{"type": "wheel", "id": "wheel", "actions": []map[string]interface{}{{"type": "scroll", "deltaX": *deltaX, "deltaY": *deltaY}}}}
		data, err := c.BrowserCommand("perform_actions", encodeBrowserParams(map[string]interface{}{"actions": actions}), "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser scroll: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "cookies":
		data, err := c.BrowserCommand("get_all_cookies", "{}", "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser cookies: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "command":
		if len(positionals) != 1 {
			fmt.Fprintln(os.Stderr, "attn browser command: expected exactly one <action>")
			os.Exit(1)
		}
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(*paramsJSON), &params); err != nil || params == nil {
			fmt.Fprintln(os.Stderr, "attn browser command: --params must be a JSON object")
			os.Exit(1)
		}
		data, err := c.BrowserCommand(positionals[0], *paramsJSON, "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser command: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(data)
	case "screenshot":
		if len(positionals) > 1 {
			fmt.Fprintln(os.Stderr, "attn browser screenshot: expected at most one [path]")
			os.Exit(1)
		}
		path := "attn-browser.png"
		if len(positionals) == 1 {
			path = positionals[0]
		}
		data, err := c.BrowserControl("screenshot", "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser screenshot: %v\n", err)
			os.Exit(1)
		}
		png, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser screenshot: decode PNG: %v\n", err)
			os.Exit(1)
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser screenshot: resolve path: %v\n", err)
			os.Exit(1)
		}
		if err := writePrivateFile(absPath, png); err != nil {
			fmt.Fprintf(os.Stderr, "browser screenshot: write %s: %v\n", absPath, err)
			os.Exit(1)
		}
		fmt.Println(absPath)
	case "pdf":
		if len(positionals) > 1 {
			fmt.Fprintln(os.Stderr, "attn browser pdf: expected at most one [path]")
			os.Exit(1)
		}
		path := "attn-browser.pdf"
		if len(positionals) == 1 {
			path = positionals[0]
		}
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(*paramsJSON), &params); err != nil || params == nil {
			fmt.Fprintln(os.Stderr, "attn browser pdf: --params must be a JSON object")
			os.Exit(1)
		}
		data, err := c.BrowserCommand("print_page", *paramsJSON, "", "", sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser pdf: %v\n", err)
			os.Exit(1)
		}
		pdf, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser pdf: decode PDF: %v\n", err)
			os.Exit(1)
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "browser pdf: resolve path: %v\n", err)
			os.Exit(1)
		}
		if err := writePrivateFile(absPath, pdf); err != nil {
			fmt.Fprintf(os.Stderr, "browser pdf: write %s: %v\n", absPath, err)
			os.Exit(1)
		}
		fmt.Println(absPath)
	default:
		fmt.Fprintf(os.Stderr, "unknown browser command: %s\n", subcommand)
		printBrowserUsage(os.Stderr)
		os.Exit(1)
	}
}

func printJSON(v interface{}) {
	if err := fprintJSON(os.Stdout, v); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding json: %v\n", err)
		os.Exit(1)
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
		os.Exit(1)
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
		os.Exit(1)
	}
}

func runHookStop() {
	var input hookInput
	transcriptPath := ""
	if err := json.NewDecoder(os.Stdin).Decode(&input); err == nil {
		transcriptPath = input.TranscriptPath
	}
	sessionID := hookCaller(hookTerminalIDFromArgOrEnv(2), input)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-stop [session_id]\n")
		os.Exit(1)
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.SendStop(sessionID, transcriptPath, stopFacts(input)); err != nil {
		fmt.Fprintf(os.Stderr, "error sending stop: %v\n", err)
		os.Exit(1)
	}
}

func stopFacts(input hookInput) client.StopFacts {
	facts := client.StopFacts{PendingSessionCrons: len(input.SessionCrons)}
	for _, t := range input.BackgroundTasks {
		facts.BackgroundTasks = append(facts.BackgroundTasks, protocol.StopBackgroundTask{
			Type:   t.Type,
			Status: t.Status,
			Name:   protocol.Ptr(t.Description),
		})
	}
	return facts
}

func runHookSessionStart() {
	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	sessionID := hookCaller(hookTerminalIDFromArgOrEnv(2), input)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-session-start [session_id]\n")
		os.Exit(1)
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	observeAgentConversation(c, sessionID, input.SessionID, input.TranscriptPath)
}

func runHookState() {
	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	sessionID, state, hookEvent := parseHookStateArgs()
	sessionID = hookCaller(sessionID, input)
	if sessionID == "" || state == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-state [session_id] <state>\n")
		os.Exit(1)
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	observePromptConversation(c, sessionID, hookEvent, input)
	if err := c.UpdateStateFromHookEvidence(sessionID, state, input.PermissionMode, hookEvent, input.Prompt); err != nil {
		fmt.Fprintf(os.Stderr, "error updating state: %v\n", err)
		os.Exit(1)
	}
}

func runHookNotification() {
	sessionID := hookTerminalIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-notification [session_id]\n")
		os.Exit(1)
	}

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	if strings.TrimSpace(input.NotificationType) == "" {
		return
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.RecordNotification(sessionID, input.NotificationType, input.Message); err != nil {
		fmt.Fprintf(os.Stderr, "error recording notification: %v\n", err)
		os.Exit(1)
	}
}

func runHookStopFailure() {
	sessionID := hookTerminalIDFromArgOrEnv(2)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-stop-failure [session_id]\n")
		os.Exit(1)
	}

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	errorType := strings.TrimSpace(input.ErrorType)
	if errorType == "" {
		return
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.RecordStopFailure(sessionID, errorType, input.ErrorMessage); err != nil {
		fmt.Fprintf(os.Stderr, "error recording stop failure: %v\n", err)
		os.Exit(1)
	}
}

func runHookCompact() {
	sessionID := hookTerminalIDFromArgOrEnv(2)
	if sessionID == "" || len(os.Args) < 4 {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-compact <session_id> <start|end>\n")
		os.Exit(1)
	}
	active := strings.TrimSpace(os.Args[3]) == "start"

	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if err := c.RecordCompaction(sessionID, active, input.Trigger); err != nil {
		fmt.Fprintf(os.Stderr, "error recording compaction: %v\n", err)
		os.Exit(1)
	}
}

func runHookToolUse() {
	var input hookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	sessionID := hookCaller(hookTerminalIDFromArgOrEnv(2), input)
	if sessionID == "" {
		fmt.Fprintf(os.Stderr, "usage: attn _hook-tool-use [session_id]\n")
		os.Exit(1)
	}

	c := client.New(strings.TrimSpace(os.Getenv("ATTN_SOCKET_PATH")))
	if strings.TrimSpace(input.AgentID) == "" {
		if err := c.UpdateState(sessionID, protocol.StateWorking); err != nil {
			fmt.Fprintf(os.Stderr, "error updating state: %v\n", err)
			os.Exit(1)
		}
	}

	if edited := hooks.MarkdownEdits(input.ToolName, input.ToolInput, input.CWD); len(edited) > 0 {
		if err := c.RecordFilesEdited(sessionID, edited); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not record edited files: %v\n", err)
		}
	}

	if sent := hooks.SentFiles(input.ToolName, input.ToolInput, input.CWD); len(sent) > 0 {
		if err := c.OpenSentFilesFromTerminal(sessionID, sent); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open sent files: %v\n", err)
		}
	}

	for _, url := range hooks.PullRequestCreated(input.ToolName, input.ToolInput, input.ToolResponse) {
		if err := c.RecordPullRequestCreatedFromTerminal(sessionID, url); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not record pull request %s: %v\n", url, err)
		}
	}
}

func runProbeTUI() {
	fs := flag.NewFlagSet("_probe-tui", flag.ExitOnError)
	styleFlag := fs.String("style", "", "agent vocabulary to mirror: codex or claude")
	interval := fs.Duration("interval", 500*time.Millisecond, "frame repaint interval")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	style, err := probetui.ParseStyle(*styleFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn _probe-tui: %v\n", err)
		os.Exit(2)
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
		os.Exit(1)
	}
}

func hookCaller(id protocol.TerminalID, input hookInput) protocol.TerminalID {
	profile, shared := os.LookupEnv(harness.CodexSharedProfileEnv)
	if !shared {
		return id
	}
	return harness.CodexThreadTerminal(profile, input.SessionID)
}

func hookTerminalIDFromArgOrEnv(index int) protocol.TerminalID {
	if len(os.Args) > index {
		return protocol.TerminalID(strings.TrimSpace(os.Args[index]))
	}
	return ownTerminalID()
}

func parseHookStateArgs() (terminalID protocol.TerminalID, state string, hookEvent string) {
	if len(os.Args) < 3 {
		return "", "", ""
	}
	first := strings.TrimSpace(os.Args[2])
	if hookStateValue(first) {
		event := ""
		if len(os.Args) >= 4 {
			event = strings.TrimSpace(os.Args[3])
		}
		return ownTerminalID(), first, event
	}
	if len(os.Args) < 4 {
		return "", "", ""
	}
	event := ""
	if len(os.Args) >= 5 {
		event = strings.TrimSpace(os.Args[4])
	}
	return protocol.TerminalID(first), strings.TrimSpace(os.Args[3]), event
}

func hookStateValue(value string) bool {
	switch value {
	case protocol.StateLaunching, protocol.StateWorking, protocol.StatePendingApproval,
		protocol.StateWaitingInput, protocol.StateIdle, protocol.StateUnknown,
		protocol.StateScheduled, "recoverable":
		return true
	default:
		return false
	}
}

type agentConversationObserver interface {
	ObserveAgentConversation(terminalID protocol.TerminalID, agentSessionID, transcriptPath string) error
}

func observePromptConversation(c agentConversationObserver, terminalID protocol.TerminalID, hookEvent string, input hookInput) {
	if !strings.EqualFold(strings.TrimSpace(hookEvent), "user_prompt_submit") {
		return
	}
	observeAgentConversation(c, terminalID, input.SessionID, input.TranscriptPath)
}

func observeAgentConversation(c agentConversationObserver, terminalID protocol.TerminalID, agentSessionID string, transcriptPath string) {
	agentSessionID = protocol.TrimID(agentSessionID)
	transcriptPath = strings.TrimSpace(transcriptPath)
	if agentSessionID == "" || transcriptPath == "" {
		return
	}
	if err := c.ObserveAgentConversation(terminalID, agentSessionID, transcriptPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not observe agent conversation: %v\n", err)
	}
}

func runTicket() {
	verb := ""
	if len(os.Args) > 2 {
		verb = os.Args[2]
	}
	signpostTicketVerb(verb)
}
