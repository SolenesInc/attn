package main

import (
	"fmt"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
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

func runAgentDirectly(requestedAgent string) {
	parsed, err := parseDirectLaunchArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn: %v\n\n", err)
		writeHelp(os.Stderr)
		os.Exit(1)
	}

	pathutil.EnsureGUIPath()

	driver := agentdriver.Get(requestedAgent)
	if driver == nil {
		fmt.Fprintf(os.Stderr, "warning: unknown ATTN_AGENT %q, defaulting to codex\n", requestedAgent)
		driver = agentdriver.MustGet("codex")
	}
	caps := agentdriver.EffectiveCapabilities(driver)

	if !caps.HasResume && (parsed.resumeID != "" || parsed.resumePicker) {
		fmt.Fprintf(os.Stderr, "warning: %s resume not supported yet (ignoring --resume)\n", driver.Name())
		parsed.resumeID = ""
		parsed.resumePicker = false
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting cwd: %v\n", err)
		os.Exit(1)
	}
	initialPrompt := parsed.initialPrompt
	if parsed.initialPromptFile != "" {
		content, readErr := readInitialPromptFile(parsed.initialPromptFile)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "error reading initial prompt: %v\n", readErr)
			os.Exit(1)
		}
		initialPrompt = content
	}

	c := client.New("")
	terminalID := ownTerminalID()
	sessionID := currentSessionOrExit()
	if terminalID == "" {
		fmt.Fprintf(os.Stderr, "attn: a daemon-managed launch needs ATTN_TERMINAL_ID; the daemon sets it for every agent it starts\n")
		os.Exit(1)
	}

	opts := agentdriver.SpawnOpts{
		TerminalID:      terminalID,
		CWD:             cwd,
		Label:           parsed.label,
		InitialPrompt:   initialPrompt,
		ResumeSessionID: parsed.resumeID,
		ResumePicker:    parsed.resumePicker,
		YoloMode:        parsed.yoloMode,
		Executable:      driver.ResolveExecutable(""),
		SocketPath:      config.SocketPath(),
		WrapperPath:     resolveWrapperPath(),
		CodexRemote:     consumeOneShotEnv("ATTN_CODEX_REMOTE"),
	}

	if _, err := agentdriver.EnsureAttnSkillInstalled(driver.Name()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: skill synchronization failed for %s: %v\n", driver.Name(), err)
	}
	if preparer, ok := driver.(agentdriver.LaunchPreparer); ok {
		if err := preparer.PrepareLaunch(opts); err != nil {
			fmt.Fprintf(os.Stderr, "warning: launch preparation failed for %s: %v\n", driver.Name(), err)
		}
	}

	cleanupFns := []func(){}
	cleanup := func() {
		for i := len(cleanupFns) - 1; i >= 0; i-- {
			cleanupFns[i]()
		}
	}

	hasHooks := false
	if agentdriver.EffectiveCapabilities(driver).HasLaunchInstructions {
		if guide, err := c.NotebookGuide(sessionID); err == nil && guide != nil && guide.SessionIsChief {
			opts.NotebookRoot = guide.Root
		}
	}
	if _, err := c.SeedReady(sessionID, "", false); err == nil {
		opts.Garden = true
	}
	opts.SelfReportPullRequests = !caps.HasHooks
	if prime, err := c.CrewPrime(sessionID); err == nil {
		opts.CrewPriming = protocol.Deref(prime.Guidance)
		opts.AwarenessDirs = prime.AwarenessDirs
	}
	opts.AutoApprove = consumeOneShotBoolEnv("ATTN_AUTO_APPROVE")
	opts.TrustWorkingDirectory = consumeOneShotBoolEnv("ATTN_TRUST_WORKING_DIRECTORY")
	opts.Model = consumeOneShotEnv("ATTN_MODEL")
	opts.Effort = consumeOneShotEnv("ATTN_EFFORT")
	window := consumeOneShotEnv("ATTN_AUTO_COMPACT_WINDOW")
	if window == "" {
		window = consumeOneShotEnv("ATTN_CHIEF_AUTO_COMPACT_WINDOW")
	}
	if window != "" {
		if n, err := strconv.Atoi(window); err == nil && n > 0 {
			opts.AutoCompactWindow = n
		}
	}
	if cp, ok := agentdriver.GetConfigOverrideProvider(driver); ok {
		opts.ConfigOverrides = cp.GenerateConfigOverrides(opts)
	}
	if hp, ok := agentdriver.GetHookProvider(driver); ok {
		opts.HookSettings = hp.GenerateHooksConfig(opts)
		hasHooks = true
	}
	if ip, ok := agentdriver.GetInstructionsFileProvider(driver); ok {
		name, content := ip.GenerateInstructionsFile(opts)
		if strings.TrimSpace(content) != "" {
			dir, err := writeLauncherInstructions(string(terminalID), name, content)
			if err != nil {
				cleanup()
				fmt.Fprintf(os.Stderr, "error writing launch instructions: %v\n", err)
				os.Exit(1)
			}
			opts.InstructionsDir = dir
			cleanupFns = append(cleanupFns, func() { _ = os.RemoveAll(dir) })
		}
	}

	opts.InheritedInstructionsDirs = os.Getenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS")
	cmd := driver.BuildCommand(opts)
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = pty.MergeEnvironment(config.WithoutAgentSessionIdentityEnv(os.Environ()), driver.BuildEnv(opts))

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		cleanup()
		fmt.Fprintf(os.Stderr, "error starting %s: %v\n", driver.Name(), err)
		os.Exit(1)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigChan
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
	}()

	err = cmd.Wait()

	if !hasHooks {
		transcriptPath := ""
		if tf, ok := agentdriver.GetTranscriptFinder(driver); ok {
			if opts.ResumeSessionID != "" {
				transcriptPath = tf.FindTranscriptForResume(opts.ResumeSessionID)
			}
			if transcriptPath == "" {
				transcriptPath = tf.FindTranscript(string(terminalID), cwd, startedAt)
			}
		}
		if sendErr := c.SendStop(terminalID, transcriptPath, client.StopFacts{}); sendErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not send stop: %v\n", sendErr)
		}
	}

	cleanup()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

func consumeOneShotBoolEnv(key string) bool {
	return consumeOneShotEnv(key) == "1"
}

func consumeOneShotEnv(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	_ = os.Unsetenv(key)
	return value
}

func resolveWrapperPath() string {
	if value := strings.TrimSpace(os.Getenv("ATTN_WRAPPER_PATH")); value != "" {
		return value
	}
	if exePath, err := os.Executable(); err == nil && strings.TrimSpace(exePath) != "" {
		return exePath
	}
	return "attn"
}

func writeLauncherInstructions(terminalID, name, content string) (string, error) {
	dir := filepath.Join(os.TempDir(), "attn-instructions-"+terminalID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}
