package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/builtin"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

type directLaunchArgs struct {
	label             string
	resumeID          string
	resumePicker      bool
	yoloMode          bool
	initialPromptFile string
	initialPrompt     string
}

func readInitialPromptFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func parseDirectLaunchArgs(args []string) (directLaunchArgs, error) {
	parsed := directLaunchArgs{}
	label := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-s":
			if i+1 >= len(args) {
				return directLaunchArgs{}, fmt.Errorf("flag -s needs a value")
			}
			label = args[i+1]
			i++
		case "--resume":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				parsed.resumeID = args[i+1]
				i++
			} else {
				parsed.resumePicker = true
			}
		case "--yolo":
			parsed.yoloMode = true
		case "--initial-prompt":
			if i+1 >= len(args) {
				return directLaunchArgs{}, fmt.Errorf("flag --initial-prompt needs a value")
			}
			parsed.initialPrompt = args[i+1]
			i++
		case "--initial-prompt-file":
			if i+1 >= len(args) {
				return directLaunchArgs{}, fmt.Errorf("flag --initial-prompt-file needs a value")
			}
			parsed.initialPromptFile = args[i+1]
			i++
		default:
			recordUsageError("attn", usageErrorUnknownFlag)
			return directLaunchArgs{}, fmt.Errorf("unknown flag %q", arg)
		}
	}
	if label == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return directLaunchArgs{}, err
		}
		label = filepath.Base(cwd)
	}
	parsed.label = label
	return parsed, nil
}

func builtinHarnesses() string {
	names := builtin.IDs()
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func runAgentDirectly(requestedAgent string) {
	leaveInvocationUnrecorded()
	parsed, err := parseDirectLaunchArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn: %v\n\n", err)
		writeHelp(os.Stderr)
		exit(1)
	}

	pathutil.EnsureGUIPath()

	launcher := builtin.Launcher(requestedAgent)
	if launcher == nil {
		fmt.Fprintf(os.Stderr, "attn: ATTN_AGENT %q is no harness attn runs: set it to one of %s\n", requestedAgent, builtinHarnesses())
		exit(1)
	}
	facts := launcher.Facts()

	if !facts.Options.Resume && (parsed.resumeID != "" || parsed.resumePicker) {
		fmt.Fprintf(os.Stderr, "warning: %s resume not supported yet (ignoring --resume)\n", facts.ID)
		parsed.resumeID = ""
		parsed.resumePicker = false
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		exit(1)
	}
	initialPrompt := parsed.initialPrompt
	if parsed.initialPromptFile != "" {
		content, readErr := readInitialPromptFile(parsed.initialPromptFile)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "error reading initial prompt: %v\n", readErr)
			exit(1)
		}
		initialPrompt = content
	}

	c := client.New("")
	terminalID := ownTerminalID()
	sessionID := currentSessionOrExit()
	if terminalID == "" {
		fmt.Fprintf(os.Stderr, "attn: a daemon-managed launch needs ATTN_TERMINAL_ID; the daemon sets it for every agent it starts\n")
		exit(1)
	}

	crewMember := consumeOneShotBoolEnv("ATTN_CREW_MEMBER")
	opts := harness.DirectLaunchOpts{
		TerminalID:      terminalID,
		CWD:             cwd,
		InitialPrompt:   initialPrompt,
		ResumeSessionID: parsed.resumeID,
		ResumePicker:    parsed.resumePicker,
		YoloMode:        parsed.yoloMode,
		Executable:      launcher.ResolveExecutable(""),
		SocketPath:      config.SocketPath(),
		WrapperPath:     resolveWrapperPath(),
		Guidance: prompts.Launch{
			Vent:       harness.ConsumeOneShotEnv("ATTN_VENT_ENABLED") == "true",
			ToolVents:  harness.ConsumeOneShotEnv("ATTN_TOOL_VENTS_ENABLED") == "true",
			CodeReview: harness.ConsumeOneShotEnv("ATTN_CODE_REVIEW_ENABLED") == "true",
		},
		AgentTools:       consumeAgentTools(),
		SharedAgentTools: consumeSharedAgentTools(),
	}

	if err := launcher.Prepare(opts); err != nil {
		fmt.Fprintf(os.Stderr, "warning: launch preparation failed for %s: %v\n", facts.ID, err)
	}

	if _, err := c.SeedReady(sessionID, "", false); err == nil {
		opts.Guidance.Garden = true
	}
	if asks, err := c.AskList(sessionID); err == nil {
		opts.Guidance.Asks = asks.Enabled
	}
	hasHooks := facts.Signals.ReportsTools
	opts.Guidance.SelfReportPullRequests = !hasHooks
	if prime, err := c.CrewPrime(sessionID); err == nil {
		opts.Guidance.Crew = protocol.Deref(prime.Guidance)
		opts.AwarenessDirs = prime.AwarenessDirs
	} else if crewMember {
		fmt.Fprintf(os.Stderr, "attn: crew priming: %v\n", err)
		exit(1)
	}
	opts.AutoApprove = consumeOneShotBoolEnv("ATTN_AUTO_APPROVE")
	opts.Model = harness.ConsumeOneShotEnv("ATTN_MODEL")
	opts.Effort = harness.ConsumeOneShotEnv("ATTN_EFFORT")
	_ = harness.ConsumeOneShotEnv("ATTN_EXECUTION_TARGET")
	window := harness.ConsumeOneShotEnv("ATTN_AUTO_COMPACT_WINDOW")
	if window == "" {
		window = harness.ConsumeOneShotEnv("ATTN_CHIEF_AUTO_COMPACT_WINDOW")
	}
	if window != "" {
		if n, err := strconv.Atoi(window); err == nil && n > 0 {
			opts.AutoCompactWindow = n
		}
	}
	cmd, buildEnv := launcher.Build(opts)
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = launchenv.MergeEnvironment(config.WithoutAgentSessionIdentityEnv(os.Environ()), buildEnv)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error starting %s: %v\n", facts.ID, err)
		exit(1)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for sig := range sigChan {
			_ = cmd.Process.Signal(sig)
		}
	}()

	err = cmd.Wait()

	if !hasHooks && facts.Recovery != harness.ByServer {
		transcriptPath := ""
		if opts.ResumeSessionID != "" {
			transcriptPath = launcher.FindTranscript(opts.ResumeSessionID)
		}
		if sendErr := c.SendStop(terminalID, transcriptPath, client.StopFacts{}); sendErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not send stop: %v\n", sendErr)
		}
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exit(exitErr.ExitCode())
		}
		exit(1)
	}
}

func consumeAgentTools() *harness.AgentTools {
	url, credential := harness.ConsumeOneShotEnv(harness.AgentToolsURLEnv), harness.ConsumeOneShotEnv(harness.AgentCredentialEnv)
	if url == "" || credential == "" {
		return nil
	}
	return &harness.AgentTools{URL: url, Credential: credential}
}

func consumeSharedAgentTools() *harness.AgentTools {
	raw := harness.ConsumeOneShotEnv(harness.SharedAgentToolsEnv)
	if raw == "" {
		return nil
	}
	var tools harness.AgentTools
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		fmt.Fprintf(os.Stderr, "attn: %s: %v\n", harness.SharedAgentToolsEnv, err)
		exit(1)
	}
	return &tools
}

func consumeOneShotBoolEnv(key string) bool {
	return harness.ConsumeOneShotEnv(key) == "1"
}

func resolveWrapperPath() string {
	return launchenv.ActiveAttnExecutable()
}
