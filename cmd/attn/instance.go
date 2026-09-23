package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/desktopentry"
	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

func runInstance() {
	if len(os.Args) < 3 {
		runInstanceStatus()
		return
	}
	switch os.Args[2] {
	case "status":
		runInstanceStatus()
	case "resolve":
		runInstanceResolve(os.Args[3:])
	case "tauri-config":
		runInstanceTauriConfig(os.Args[3:])
	case "clean":
		runInstanceClean(os.Args[3:])
	case "stop-app":
		runInstanceStopApp(os.Args[3:])
	case "register-scheme":
		runInstanceRegisterScheme(os.Args[3:])
	case "set-origin":
		runInstanceSetOrigin(os.Args[3:])
	case "list":
		runInstanceList(os.Args[3:])
	case "env":
		runInstanceEnvArgs(os.Args[3:])
	case "help", "-h", "--help":
		printInstanceHelp(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown instance subcommand %q\n\n", os.Args[2])
		printInstanceHelp(os.Stderr)
		os.Exit(1)
	}
}

type instanceResolved struct {
	Instance       string `json:"instance"`
	Label          string `json:"label"`
	DataDir        string `json:"dataDir"`
	Socket         string `json:"socket"`
	DBPath         string `json:"dbPath"`
	WSPort         string `json:"wsPort"`
	BundleID       string `json:"bundleId"`
	AppName        string `json:"appName"`
	AppPath        string `json:"appPath"`
	AppExecutable  string `json:"appExecutable"`
	AppDaemon      string `json:"appDaemon"`
	AppLocalData   string `json:"appLocalDataDir"`
	AppLock        string `json:"appLockPath"`
	DeepLinkScheme string `json:"deepLinkScheme"`
	DesktopEntry   string `json:"desktopEntry,omitempty"`
	E2EDaemonPort  string `json:"e2eDaemonPort"`
	E2EVitePort    string `json:"e2eVitePort"`
	MockGitHubPort string `json:"mockGitHubPort"`
}

func resolveInstance(instance string) instanceResolved {
	label := instance
	if label == "" {
		label = "default"
	}
	return instanceResolved{
		Instance:       instance,
		Label:          label,
		DataDir:        config.DataDirForInstance(instance),
		Socket:         config.SocketPathForInstance(instance),
		DBPath:         filepath.Join(config.DataDirForInstance(instance), "attn.db"),
		WSPort:         config.WSPortForInstance(instance),
		BundleID:       config.BundleIdentifierForInstance(instance),
		AppName:        config.AppNameForInstance(instance),
		AppPath:        config.AppPathForInstance(instance),
		AppExecutable:  config.AppExecutableForInstance(instance),
		AppDaemon:      config.AppDaemonBinaryForInstance(instance),
		AppLocalData:   config.AppLocalDataDirForInstance(instance),
		AppLock:        config.AppLockPathForInstance(instance),
		DeepLinkScheme: config.DeepLinkSchemeForInstance(instance),
		DesktopEntry:   desktopEntryPath(config.AppNameForInstance(instance)),
		E2EDaemonPort:  config.E2EDaemonPortForInstance(instance),
		E2EVitePort:    config.E2EVitePortForInstance(instance),
		MockGitHubPort: config.MockGitHubPortForInstance(instance),
	}
}

func (r instanceResolved) field(key string) (string, bool) {
	switch key {
	case "instance":
		return r.Instance, true
	case "label":
		return r.Label, true
	case "dataDir":
		return r.DataDir, true
	case "socket":
		return r.Socket, true
	case "dbPath":
		return r.DBPath, true
	case "wsPort":
		return r.WSPort, true
	case "bundleId":
		return r.BundleID, true
	case "appName":
		return r.AppName, true
	case "appPath":
		return r.AppPath, true
	case "appExecutable":
		return r.AppExecutable, true
	case "appDaemon":
		return r.AppDaemon, true
	case "appLocalDataDir":
		return r.AppLocalData, true
	case "appLockPath":
		return r.AppLock, true
	case "deepLinkScheme":
		return r.DeepLinkScheme, true
	case "desktopEntry":
		return r.DesktopEntry, true
	case "e2eDaemonPort":
		return r.E2EDaemonPort, true
	case "e2eVitePort":
		return r.E2EVitePort, true
	case "mockGitHubPort":
		return r.MockGitHubPort, true
	}
	return "", false
}

func runInstanceStatus() {
	r := resolveInstance(config.Instance())
	socketUp := fileExists(r.Socket)
	appInstalled := fileExists(r.AppPath)

	fmt.Printf("attn instance: %s\n\n", r.Label)
	fmt.Printf("  data dir   %s\n", r.DataDir)
	fmt.Printf("  socket     %s  (%s)\n", r.Socket, ynLabel(socketUp, "daemon socket present", "no daemon socket"))
	fmt.Printf("  ws port    %s\n", r.WSPort)
	fmt.Printf("  bundle id  %s\n", r.BundleID)
	fmt.Printf("  app        %s  (%s)\n", r.AppPath, ynLabel(appInstalled, "installed", "not installed"))
	fmt.Printf("  app data   %s  (%s)\n", r.AppLocalData, ynLabel(fileExists(r.AppLocalData), "present", "none"))
	fmt.Printf("  scheme     %s\n", r.DeepLinkScheme)
	if r.DesktopEntry != "" {
		fmt.Printf("  handler    %s  (%s)\n", r.DesktopEntry, ynLabel(fileExists(r.DesktopEntry), "registered", "not registered"))
	}
	fmt.Printf("  e2e ports  daemon %s · vite %s\n", r.E2EDaemonPort, r.E2EVitePort)
	fmt.Printf("  mock gh    %s\n\n", r.MockGitHubPort)

	if err := config.ValidateInstanceRouting(); err != nil {
		fmt.Printf("CONFLICT — every other attn command refuses to run here:\n%v\n\n", err)
	}

	fmt.Println("Switch:   attn instance-env <name> | source   (fish: attn instance-env --fish <name> | source)")
	fmt.Println("Resolve:  attn instance resolve --json         (single value: --field wsPort)")
	fmt.Println("List:     attn instance list")
}

func runInstanceResolve(args []string) {
	instance := config.Instance()
	field := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
		case "--instance":
			if i+1 >= len(args) {
				instanceFatal("--instance requires a value")
			}
			i++
			p, err := config.NormalizeInstanceName(args[i])
			if err != nil {
				instanceFatal(err.Error())
			}
			instance = p
		case "--field":
			if i+1 >= len(args) {
				instanceFatal("--field requires a key")
			}
			i++
			field = args[i]
		case "-h", "--help":
			printInstanceHelp(os.Stdout)
			return
		default:
			instanceFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	r := resolveInstance(instance)
	if field != "" {
		v, ok := r.field(field)
		if !ok {
			instanceFatal(fmt.Sprintf("unknown field %q (valid: instance,label,dataDir,socket,dbPath,wsPort,bundleId,appName,appPath,appExecutable,appDaemon,appLocalDataDir,appLockPath,deepLinkScheme,desktopEntry,e2eDaemonPort,e2eVitePort,mockGitHubPort)", field))
		}
		fmt.Println(v)
		return
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		instanceFatal(err.Error())
	}
	fmt.Println(string(b))
}

func runInstanceTauriConfig(args []string) {
	instance := config.Instance()
	basePath := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--base":
			if i+1 >= len(args) {
				instanceFatal("--base requires a value")
			}
			i++
			basePath = args[i]
		case "--instance":
			if i+1 >= len(args) {
				instanceFatal("--instance requires a value")
			}
			i++
			p, err := config.NormalizeInstanceName(args[i])
			if err != nil {
				instanceFatal(err.Error())
			}
			instance = p
		case "-h", "--help":
			printInstanceHelp(os.Stdout)
			return
		default:
			instanceFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	window := map[string]any{}
	if basePath != "" {
		base, err := os.ReadFile(basePath)
		if err != nil {
			instanceFatal(err.Error())
		}
		window, err = baseMainWindow(base)
		if err != nil {
			instanceFatal(fmt.Sprintf("%s: %v", basePath, err))
		}
	}
	b, err := json.MarshalIndent(tauriConfigOverlay(resolveInstance(instance), window), "", "  ")
	if err != nil {
		instanceFatal(err.Error())
	}
	fmt.Println(string(b))
}

func tauriConfigOverlay(r instanceResolved, baseWindow map[string]any) map[string]any {
	window := map[string]any{}
	for k, v := range baseWindow {
		window[k] = v
	}
	window["title"] = r.AppName
	window["backgroundThrottling"] = "disabled"
	return map[string]any{
		"$schema":     "https://schema.tauri.app/config/2",
		"productName": r.AppName,
		"identifier":  r.BundleID,
		"app": map[string]any{
			"windows": []any{window},
		},
		"plugins": map[string]any{
			"deep-link": map[string]any{
				"desktop": map[string]any{
					"schemes": []string{r.DeepLinkScheme},
				},
			},
		},
	}
}

func baseMainWindow(tauriConf []byte) (map[string]any, error) {
	var conf struct {
		App struct {
			Windows []map[string]any `json:"windows"`
		} `json:"app"`
	}
	if err := json.Unmarshal(tauriConf, &conf); err != nil {
		return nil, err
	}
	if len(conf.App.Windows) == 0 {
		return nil, fmt.Errorf("no app.windows entry to inherit")
	}
	return conf.App.Windows[0], nil
}

func cleanPlan(args []string) (normalized string, force bool, err error) {
	name := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force", "-f":
			force = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", false, fmt.Errorf("unknown flag %q", args[i])
			}
			if name != "" {
				return "", false, fmt.Errorf("clean takes a single instance name, got %q and %q", name, args[i])
			}
			name = args[i]
		}
	}
	if name == "" {
		return "", false, fmt.Errorf("clean requires an instance name (e.g. `attn instance clean agent7`)")
	}
	normalized, err = config.NormalizeInstanceName(name)
	if err != nil {
		return "", false, err
	}
	if normalized == "" && !force {
		return "", false, fmt.Errorf("refusing to clean the default (production) instance without --force; this removes %s, %s and %s",
			config.DataDirForInstance(""), config.AppPathForInstance(""), config.AppLocalDataDirForInstance(""))
	}
	return normalized, force, nil
}

func runInstanceClean(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			printInstanceHelp(os.Stdout)
			return
		}
	}
	normalized, _, err := cleanPlan(args)
	if err != nil {
		instanceFatal(err.Error())
	}
	if err := cleanInstance(os.Stdout, resolveInstance(normalized)); err != nil {
		instanceFatal(err.Error())
	}
}

func cleanInstance(w io.Writer, r instanceResolved) error {
	fmt.Fprintf(w, ">>> Cleaning instance %s\n", r.Label)

	msg, err := stopInstanceApp(r)
	if err != nil {
		return fmt.Errorf("app not stopped: %w; nothing was removed, since a live app rewrites %s as fast as it is deleted. Quit it and re-run; --force does not cover a live app", err, r.AppLocalData)
	}
	fmt.Fprintf(w, "  app      %s\n", msg)

	release, err := holdAppLock(r)
	if err != nil {
		return err
	}
	defer release()
	if err := refuseRelaunchedApp(r); err != nil {
		return err
	}

	if msg := stopInstanceDaemon(r); msg != "" {
		fmt.Fprintf(w, "  daemon   %s\n", msg)
	} else {
		fmt.Fprintf(w, "  daemon   stopped\n")
	}

	reportWorkerReap(w, ptyworker.ReapDataDir(r.DataDir))
	sharedHosts := ptyhost.ReapDataDir(r.DataDir)
	reportProcReap(w, "pty hosts", "generation", sharedHosts)
	for _, result := range sharedHosts {
		if result.Outcome != procreap.ReapTerminated && result.Outcome != procreap.ReapAlreadyGone {
			return fmt.Errorf("shared PTY host %s was not stopped (%s): %v; instance data was preserved", result.ID, result.Outcome, result.Err)
		}
	}

	reportProcReap(w, "plugins", "plugin", plugins.ReapRuntimeProcesses(r.DataDir))

	if err := refuseRelaunchedApp(r); err != nil {
		return err
	}

	if fileExists(r.AppPath) {
		lsregisterForget(r.AppPath)
		if err := os.RemoveAll(r.AppPath); err != nil {
			return fmt.Errorf("remove app bundle %s: %w", r.AppPath, err)
		}
		fmt.Fprintf(w, "  app      removed %s\n", r.AppPath)
	} else {
		fmt.Fprintf(w, "  app      not installed (%s)\n", r.AppPath)
	}

	if r.DesktopEntry != "" {
		removed, err := desktopentry.Remove(r.AppName)
		if err != nil {
			fmt.Fprintf(w, "  scheme   %v\n", err)
		} else if removed {
			fmt.Fprintf(w, "  scheme   removed %s\n", r.DesktopEntry)
		} else {
			fmt.Fprintf(w, "  scheme   not registered (%s)\n", r.DesktopEntry)
		}
	}

	localData, err := removeAppLocalData(r)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "  app data %s\n", localData)

	if fileExists(r.DataDir) {
		if err := os.RemoveAll(r.DataDir); err != nil {
			return fmt.Errorf("remove data dir %s: %w", r.DataDir, err)
		}
		fmt.Fprintf(w, "  data     removed %s\n", r.DataDir)
	} else {
		fmt.Fprintf(w, "  data     none (%s)\n", r.DataDir)
	}

	fmt.Fprintf(w, "Cleaned instance %s.\n", r.Label)
	return nil
}

func refuseRelaunchedApp(r instanceResolved) error {
	pidPath := appPIDFilePath(r.DataDir)
	if !fileExists(pidPath) {
		return nil
	}
	return fmt.Errorf("%s reappeared after the app was stopped: it has been relaunched; nothing was removed", pidPath)
}

func holdAppLock(r instanceResolved) (func(), error) {
	dir := filepath.Dir(r.AppLock)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create app lock dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(r.AppLock, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open app lock %s: %w", r.AppLock, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s is held (%v): a %s app instance is running and holds it for as long as it lives; nothing was removed. Quit every one and re-run", r.AppLock, err, r.Label)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func removeAppLocalData(r instanceResolved) (string, error) {
	if !fileExists(r.AppLocalData) {
		return fmt.Sprintf("none (%s)", r.AppLocalData), nil
	}
	if err := os.RemoveAll(r.AppLocalData); err != nil {
		return "", fmt.Errorf("remove app local data dir %s: %w", r.AppLocalData, err)
	}
	return "removed " + r.AppLocalData, nil
}

func reportWorkerReap(w io.Writer, results []ptyworker.ReapResult) {
	if len(results) == 0 {
		fmt.Fprintf(w, "  workers  none registered\n")
		return
	}
	byOutcome := map[ptyworker.ReapOutcome]int{}
	for _, res := range results {
		byOutcome[res.Outcome]++
	}
	fmt.Fprintf(w, "  workers  %d registered (%s)\n", len(results), summarizeReap(byOutcome))
	for _, res := range results {
		if res.Outcome != ptyworker.ReapUnidentified {
			continue
		}
		fmt.Fprintf(w, "           ! session %s: pid %d could not be confirmed as its worker (%v); left running — check it with `ps -p %d` and kill it yourself if it is stale\n",
			res.SessionID, res.WorkerPID, res.Err, res.WorkerPID)
	}
}

func reportProcReap(w io.Writer, label, noun string, results []procreap.ReapResult) {
	if len(results) == 0 {
		fmt.Fprintf(w, "  %-8s none registered\n", label)
		return
	}
	byOutcome := map[procreap.ReapOutcome]int{}
	for _, res := range results {
		byOutcome[res.Outcome]++
	}
	order := []procreap.ReapOutcome{
		procreap.ReapTerminated,
		procreap.ReapKilled,
		procreap.ReapAlreadyGone,
		procreap.ReapUnidentified,
		procreap.ReapSurvived,
		procreap.ReapUnreadable,
	}
	parts := make([]string, 0, len(order))
	for _, outcome := range order {
		if n := byOutcome[outcome]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, outcome))
		}
	}
	fmt.Fprintf(w, "  %-8s %d registered (%s)\n", label, len(results), strings.Join(parts, ", "))
	for _, res := range results {
		switch res.Outcome {
		case procreap.ReapUnidentified:
			fmt.Fprintf(w, "           ! %s %s: pid %d could not be confirmed as its process (%v); left running — check it with `ps -p %d` and kill it yourself if it is stale\n",
				noun, res.ID, res.PID, res.Err, res.PID)
		case procreap.ReapSurvived:
			fmt.Fprintf(w, "           ! %s %s: pid %d survived SIGKILL (%v)\n",
				noun, res.ID, res.PID, res.Err)
		case procreap.ReapUnreadable:
			fmt.Fprintf(w, "           ! record %s could not be read (%v); whatever it described was not reaped — check `ps` for stray %s processes\n",
				res.ID, res.Err, noun)
		}
	}
}

func summarizeReap(byOutcome map[ptyworker.ReapOutcome]int) string {
	order := []ptyworker.ReapOutcome{
		ptyworker.ReapRemoved,
		ptyworker.ReapSignalled,
		ptyworker.ReapAlreadyGone,
		ptyworker.ReapUnidentified,
	}
	parts := make([]string, 0, len(order))
	for _, outcome := range order {
		if n := byOutcome[outcome]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, outcome))
		}
	}
	return strings.Join(parts, ", ")
}

func stopInstanceDaemon(r instanceResolved) string {
	pidPath := filepath.Join(r.DataDir, "attn.pid")
	result, err := daemonctl.Stop(pidPath)
	if err != nil {
		if strings.Contains(err.Error(), "own process tree") {
			return fmt.Sprintf("skipped (%v)", err)
		}
		return err.Error()
	}
	if !result.Stopped {
		return result.Note
	}
	if result.Forced {
		return fmt.Sprintf("force-killed pid %d (did not exit on SIGTERM)", result.PID)
	}
	return ""
}

func runInstanceStopApp(args []string) {
	instance := config.Instance()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--instance":
			if i+1 >= len(args) {
				instanceFatal("--instance requires a value")
			}
			i++
			p, err := config.NormalizeInstanceName(args[i])
			if err != nil {
				instanceFatal(err.Error())
			}
			instance = p
		case "-h", "--help":
			printInstanceHelp(os.Stdout)
			return
		default:
			instanceFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}
	msg, err := stopInstanceApp(resolveInstance(instance))
	if err != nil {
		instanceFatal(err.Error())
	}
	fmt.Printf("  app      %s\n", msg)
}

var (
	appStopQuitWait     = 15 * time.Second
	appStopSigtermWait  = 5 * time.Second
	appStopSigkillWait  = 2 * time.Second
	appStopPollInterval = 50 * time.Millisecond
)

func stopInstanceApp(r instanceResolved) (string, error) {
	pidPath := appPIDFilePath(r.DataDir)
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return stopAppWithoutPIDFile(r, pidPath)
		}
		return "", fmt.Errorf("could not read %s: %w", pidPath, err)
	}
	text := strings.TrimSpace(string(raw))
	pid, err := strconv.Atoi(text)
	if err != nil || pid <= 0 {
		return "", fmt.Errorf("%s holds %q, not a pid; left alone", pidPath, text)
	}
	if pid == os.Getpid() || pid == os.Getppid() {
		return "", fmt.Errorf("refusing to signal pid %d: it is this command's own process tree", pid)
	}
	switch own, exe, idErr := appPIDOwnership(r, pid); own {
	case pidGone:
		return releaseAppPID(pidPath, pid, fmt.Sprintf("not running (pid %d is gone; removed stale %s)", pid, pidPath))
	case pidUnidentified:
		return "", unidentifiedPIDError(pid, pidPath, "before the quit request", idErr)
	case pidForeign:
		return "", fmt.Errorf("pid %d is %s, not %s; left running", pid, exe, r.AppExecutable)
	}
	return quitAppPID(r, pid, pidPath)
}

func quitAppPID(r instanceResolved, pid int, pidPath string) (string, error) {
	if requestAppQuit(r.BundleID) && appProcessGoneWithin(pid, appStopQuitWait) {
		return releaseAppPID(pidPath, pid, fmt.Sprintf("quit pid %d", pid))
	}
	if left, err := appLeftPID(r, pid, pidPath, "after the quit request"); err != nil {
		return "", err
	} else if left {
		return releaseAppPID(pidPath, pid, fmt.Sprintf("quit pid %d (it is another process now)", pid))
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return releaseAppPID(pidPath, pid, fmt.Sprintf("not running (stale %s)", pidPath))
		}
		return "", fmt.Errorf("SIGTERM pid %d failed: %w", pid, err)
	}
	if appProcessGoneWithin(pid, appStopSigtermWait) {
		return releaseAppPID(pidPath, pid, fmt.Sprintf("stopped pid %d", pid))
	}
	if left, err := appLeftPID(r, pid, pidPath, "after SIGTERM"); err != nil {
		return "", err
	} else if left {
		return releaseAppPID(pidPath, pid, fmt.Sprintf("stopped pid %d (it is another process now)", pid))
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if appProcessGoneWithin(pid, appStopSigkillWait) {
		return releaseAppPID(pidPath, pid, fmt.Sprintf("force-killed pid %d (did not exit on SIGTERM)", pid))
	}
	return "", fmt.Errorf("pid %d survived SIGKILL; check it with `ps -p %d`", pid, pid)
}

type pidOwnership int

const (
	pidGone pidOwnership = iota
	pidOurs
	pidForeign
	pidUnidentified
)

func appPIDOwnership(r instanceResolved, pid int) (pidOwnership, string, error) {
	if processGone(pid) {
		return pidGone, "", nil
	}
	exe, err := lookupProcessExecutable(pid)
	if err != nil {
		if processGone(pid) {
			return pidGone, "", nil
		}
		return pidUnidentified, "", err
	}
	if sameExecutable(exe, r.AppExecutable) {
		return pidOurs, exe, nil
	}
	return pidForeign, exe, nil
}

var lookupProcessExecutable = processExecutable

func appLeftPID(r instanceResolved, pid int, pidPath, stage string) (bool, error) {
	switch own, _, idErr := appPIDOwnership(r, pid); own {
	case pidGone, pidForeign:
		return true, nil
	case pidUnidentified:
		return false, unidentifiedPIDError(pid, pidPath, stage, idErr)
	}
	return false, nil
}

func unidentifiedPIDError(pid int, pidPath, stage string, idErr error) error {
	return fmt.Errorf("pid %d from %s is alive and could not be identified %s (%v); left running — check it with `ps -p %d`", pid, pidPath, stage, idErr, pid)
}

func releaseAppPID(pidPath string, pid int, note string) (string, error) {
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return note, nil
		}
		return "", fmt.Errorf("could not re-read %s after stopping pid %d: %w", pidPath, pid, err)
	}
	if text := strings.TrimSpace(string(raw)); text != strconv.Itoa(pid) {
		return "", fmt.Errorf("%s names pid %s now, not the %d that was just stopped: the app was relaunched; left alone", pidPath, text, pid)
	}
	if err := os.Remove(pidPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("remove %s: %w", pidPath, err)
	}
	return note, nil
}

func sameExecutable(a, b string) bool {
	return a == b || resolvedPath(a) == resolvedPath(b)
}

func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func appPIDFilePath(dataDir string) string {
	return filepath.Join(dataDir, "app.pid")
}

func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func appProcessGoneWithin(pid int, timeout time.Duration) bool {
	return waitUntil(timeout, func() bool { return processGone(pid) })
}

func waitUntil(timeout time.Duration, done func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if done() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(appStopPollInterval)
	}
}

const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

func lsregisterForget(appPath string) {
	if runtime.GOOS != "darwin" || !fileExists(lsregisterPath) {
		return
	}
	_ = exec.Command(lsregisterPath, "-u", appPath).Run()
}

func runInstanceList(args []string) {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "-h", "--help":
			printInstanceHelp(os.Stdout)
			return
		default:
			instanceFatal(fmt.Sprintf("unknown flag %q for `attn instance list`", a))
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		instanceFatal("cannot resolve home directory: " + err.Error())
	}

	known := map[string]bool{"": true}

	if entries, err := os.ReadDir(home); err == nil {
		for _, e := range entries {
			name := e.Name()
			if name == ".attn" {
				known[""] = true
				continue
			}
			if p, ok := strings.CutPrefix(name, ".attn-"); ok && e.IsDir() {
				if config.ValidateInstanceName(p) == nil {
					known[strings.ToLower(p)] = true
				}
			}
		}
	}

	for _, p := range installedAppInstances(home) {
		known[p] = true
	}

	for _, p := range appLocalDataInstances() {
		known[p] = true
	}

	names := make([]string, 0, len(known))
	for p := range known {
		names = append(names, p)
	}
	sort.Strings(names)

	active := config.Instance()

	if asJSON {
		entries := make([]instanceListEntry, 0, len(names))
		for _, p := range names {
			entries = append(entries, newInstanceListEntry(p, active))
		}
		out, err := json.MarshalIndent(map[string]any{"instances": entries}, "", "  ")
		if err != nil {
			instanceFatal(err.Error())
		}
		fmt.Println(string(out))
		return
	}

	fmt.Printf("%-3s %-16s %-7s %-9s %-9s %-11s %s\n", "", "INSTANCE", "PORT", "DATA", "APPDATA", "APP", "ORIGIN")
	for _, p := range names {
		r := resolveInstance(p)
		marker := "  "
		if p == active {
			marker = "* "
		}
		origin := "—"
		if o := readInstanceOrigin(r.DataDir); o != nil {
			origin = filepath.Base(o.Worktree)
		}
		fmt.Printf("%-3s %-16s %-7s %-9s %-9s %-11s %s\n",
			marker,
			r.Label,
			r.WSPort,
			ynLabel(fileExists(r.DataDir), "yes", "—"),
			ynLabel(fileExists(r.AppLocalData), "yes", "—"),
			ynLabel(fileExists(r.AppPath), "installed", "—"),
			origin,
		)
	}
	fmt.Println("\n* = active (ATTN_INSTANCE)")
}

func installedAppInstances(home string) []string {
	root := filepath.Dir(config.AppPathForInstance(""))
	if runtime.GOOS == "darwin" {
		root = filepath.Join(home, "Applications")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	found := []string{}
	for _, e := range entries {
		name := e.Name()
		if runtime.GOOS == "darwin" {
			base, ok := strings.CutSuffix(name, ".app")
			if !ok {
				continue
			}
			name = base
		} else if !fileExists(config.AppExecutableInTree(filepath.Join(root, name))) {
			continue
		}
		if strings.EqualFold(name, config.AppNameForInstance("")) {
			found = append(found, "")
			continue
		}
		if p, ok := strings.CutPrefix(strings.ToLower(name), "attn-"); ok && config.ValidateInstanceName(p) == nil {
			found = append(found, p)
		}
	}
	return found
}

func appLocalDataInstances() []string {
	root := filepath.Dir(config.AppLocalDataDirForInstance(""))
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	prodBundleID := config.BundleIdentifierForInstance("")
	found := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == prodBundleID {
			found = append(found, "")
			continue
		}
		if p, ok := strings.CutPrefix(e.Name(), prodBundleID+"."); ok && config.ValidateInstanceName(p) == nil {
			found = append(found, strings.ToLower(p))
		}
	}
	return found
}

func printInstanceHelp(w *os.File) {
	fmt.Fprintln(w, `attn instance — inspect and resolve attn instances

An instance fully isolates attn's runtime: data dir, socket, websocket port,
installed app (a macOS bundle, a directory tree elsewhere), the app's local data
dir (Tauri's app_local_data_dir), and bundle identifier. ATTN_INSTANCE selects it
for every entrypoint (CLI, daemon, e2e, real-app harness, build).

Usage:
  attn instance                 status of the active instance (ATTN_INSTANCE)
  attn instance status          same
  attn instance resolve         resolved resources as JSON
  attn instance resolve --field wsPort      print one resolved value
  attn instance resolve --instance agent7   resolve a different instance
  attn instance tauri-config    Tauri --config overlay for the instance's build
  attn instance register-scheme Linux only: claim <scheme>:// for this instance's app in the desktop database
  attn instance clean <name>    reap workers + plugin drivers, stop daemon, quit app, remove its app, app local data, data dir, and scheme handler
  attn instance stop-app        quit the active instance's app (--instance <name> for another)
  attn instance list            every instance with data and/or an installed app
  attn instance list --json     same, machine-readable, with origin and what is running
  attn instance set-origin <name> [--worktree <dir>]
                                record the worktree an instance was installed from
  attn instance env <name>      alias of: attn instance-env <name>

Instance names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is the development
sibling (port 29849, ~/.attn-dev). `+"`clean`"+` refuses the default (production)
instance unless given --force.`)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ynLabel(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

func instanceFatal(msg string) {
	fmt.Fprintln(os.Stderr, "attn instance: "+msg)
	os.Exit(1)
}
