package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/ptyworker"
)

func runProfile() {
	if len(os.Args) < 3 {
		runProfileStatus()
		return
	}
	switch os.Args[2] {
	case "status":
		runProfileStatus()
	case "resolve":
		runProfileResolve(os.Args[3:])
	case "tauri-config":
		runProfileTauriConfig(os.Args[3:])
	case "clean":
		runProfileClean(os.Args[3:])
	case "stop-app":
		runProfileStopApp(os.Args[3:])
	case "set-origin":
		runProfileSetOrigin(os.Args[3:])
	case "list":
		runProfileList(os.Args[3:])
	case "env":
		runProfileEnvArgs(os.Args[3:])
	case "help", "-h", "--help":
		printProfileHelp(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown profile subcommand %q\n\n", os.Args[2])
		printProfileHelp(os.Stderr)
		os.Exit(1)
	}
}

type profileResolved struct {
	Profile        string `json:"profile"` // normalized ("" for default)
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
	DeepLinkScheme string `json:"deepLinkScheme"`
	E2EDaemonPort  string `json:"e2eDaemonPort"`
	E2EVitePort    string `json:"e2eVitePort"`
}

func resolveProfile(profile string) profileResolved {
	label := profile
	if label == "" {
		label = "default"
	}
	return profileResolved{
		Profile:        profile,
		Label:          label,
		DataDir:        config.DataDirForProfile(profile),
		Socket:         config.SocketPathForProfile(profile),
		DBPath:         filepath.Join(config.DataDirForProfile(profile), "attn.db"),
		WSPort:         config.WSPortForProfile(profile),
		BundleID:       config.BundleIdentifierForProfile(profile),
		AppName:        config.AppNameForProfile(profile),
		AppPath:        config.AppPathForProfile(profile),
		AppExecutable:  config.AppExecutableForProfile(profile),
		AppDaemon:      config.AppDaemonBinaryForProfile(profile),
		DeepLinkScheme: config.DeepLinkSchemeForProfile(profile),
		E2EDaemonPort:  config.E2EDaemonPortForProfile(profile),
		E2EVitePort:    config.E2EVitePortForProfile(profile),
	}
}

func (r profileResolved) field(key string) (string, bool) {
	switch key {
	case "profile":
		return r.Profile, true
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
	case "deepLinkScheme":
		return r.DeepLinkScheme, true
	case "e2eDaemonPort":
		return r.E2EDaemonPort, true
	case "e2eVitePort":
		return r.E2EVitePort, true
	}
	return "", false
}

func runProfileStatus() {
	r := resolveProfile(config.Profile())
	socketUp := fileExists(r.Socket)
	appInstalled := fileExists(r.AppPath)

	fmt.Printf("attn profile: %s\n\n", r.Label)
	fmt.Printf("  data dir   %s\n", r.DataDir)
	fmt.Printf("  socket     %s  (%s)\n", r.Socket, ynLabel(socketUp, "daemon socket present", "no daemon socket"))
	fmt.Printf("  ws port    %s\n", r.WSPort)
	fmt.Printf("  bundle id  %s\n", r.BundleID)
	fmt.Printf("  app        %s  (%s)\n", r.AppPath, ynLabel(appInstalled, "installed", "not installed"))
	fmt.Printf("  scheme     %s\n", r.DeepLinkScheme)
	fmt.Printf("  e2e ports  daemon %s · vite %s\n\n", r.E2EDaemonPort, r.E2EVitePort)

	if err := config.ValidateProfileRouting(); err != nil {
		fmt.Printf("CONFLICT — every other attn command refuses to run here:\n%v\n\n", err)
	}

	fmt.Println("Switch:   attn profile-env <name> | source   (fish: attn profile-env --fish <name> | source)")
	fmt.Println("Resolve:  attn profile resolve --json         (single value: --field wsPort)")
	fmt.Println("List:     attn profile list")
}

func runProfileResolve(args []string) {
	profile := config.Profile()
	field := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
		case "--profile":
			if i+1 >= len(args) {
				profileFatal("--profile requires a value")
			}
			i++
			p, err := config.NormalizeProfileName(args[i])
			if err != nil {
				profileFatal(err.Error())
			}
			profile = p
		case "--field":
			if i+1 >= len(args) {
				profileFatal("--field requires a key")
			}
			i++
			field = args[i]
		case "-h", "--help":
			printProfileHelp(os.Stdout)
			return
		default:
			profileFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	r := resolveProfile(profile)
	if field != "" {
		v, ok := r.field(field)
		if !ok {
			profileFatal(fmt.Sprintf("unknown field %q (valid: profile,label,dataDir,socket,dbPath,wsPort,bundleId,appName,appPath,appExecutable,appDaemon,deepLinkScheme,e2eDaemonPort,e2eVitePort)", field))
		}
		fmt.Println(v)
		return
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		profileFatal(err.Error())
	}
	fmt.Println(string(b))
}

func runProfileTauriConfig(args []string) {
	profile := config.Profile()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			if i+1 >= len(args) {
				profileFatal("--profile requires a value")
			}
			i++
			p, err := config.NormalizeProfileName(args[i])
			if err != nil {
				profileFatal(err.Error())
			}
			profile = p
		case "-h", "--help":
			printProfileHelp(os.Stdout)
			return
		default:
			profileFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	r := resolveProfile(profile)
	overlay := map[string]any{
		"$schema":     "https://schema.tauri.app/config/2",
		"productName": r.AppName,
		"identifier":  r.BundleID,
		"app": map[string]any{
			"windows": []any{
				map[string]any{
					"title":                r.AppName,
					"backgroundThrottling": "disabled",
				},
			},
		},
		"plugins": map[string]any{
			"deep-link": map[string]any{
				"desktop": map[string]any{
					"schemes": []string{r.DeepLinkScheme},
				},
			},
		},
	}
	b, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		profileFatal(err.Error())
	}
	fmt.Println(string(b))
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
				return "", false, fmt.Errorf("clean takes a single profile name, got %q and %q", name, args[i])
			}
			name = args[i]
		}
	}
	if name == "" {
		return "", false, fmt.Errorf("clean requires a profile name (e.g. `attn profile clean agent7`)")
	}
	normalized, err = config.NormalizeProfileName(name)
	if err != nil {
		return "", false, err
	}
	if normalized == "" && !force {
		return "", false, fmt.Errorf("refusing to clean the default (production) profile without --force; this removes %s and %s",
			config.DataDirForProfile(""), config.AppPathForProfile(""))
	}
	return normalized, force, nil
}

func runProfileClean(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			printProfileHelp(os.Stdout)
			return
		}
	}
	normalized, _, err := cleanPlan(args)
	if err != nil {
		profileFatal(err.Error())
	}
	r := resolveProfile(normalized)

	fmt.Printf(">>> Cleaning profile %s\n", r.Label)

	// The daemon outlives the app by design, so quit the app first.
	if msg, err := stopProfileApp(r); err != nil {
		fmt.Printf("  app      %v\n", err)
	} else {
		fmt.Printf("  app      %s\n", msg)
	}
	if msg := stopProfileDaemon(r); msg != "" {
		fmt.Printf("  daemon   %s\n", msg)
	} else {
		fmt.Printf("  daemon   stopped\n")
	}

	// The data dir removal below destroys the registry workers are found through:
	// reap before it goes, or a live worker is stranded.
	reportWorkerReap(ptyworker.ReapDataDir(r.DataDir))

	// A daemon that skipped its shutdown leaves these reparented to init, findable
	// only through these registries: reap before the registries go.
	reportProcReap("hosts", "session", hostsession.ReapDataDir(r.DataDir))
	reportProcReap("plugins", "plugin", plugins.ReapRuntimeProcesses(r.DataDir))

	// Forget the bundle first, so its id and deep-link scheme stop resolving to a
	// path we are about to delete.
	if fileExists(r.AppPath) {
		lsregisterForget(r.AppPath)
		if err := os.RemoveAll(r.AppPath); err != nil {
			profileFatal(fmt.Sprintf("remove app bundle %s: %v", r.AppPath, err))
		}
		fmt.Printf("  app      removed %s\n", r.AppPath)
	} else {
		fmt.Printf("  app      not installed (%s)\n", r.AppPath)
	}

	if fileExists(r.DataDir) {
		if err := os.RemoveAll(r.DataDir); err != nil {
			profileFatal(fmt.Sprintf("remove data dir %s: %v", r.DataDir, err))
		}
		fmt.Printf("  data     removed %s\n", r.DataDir)
	} else {
		fmt.Printf("  data     none (%s)\n", r.DataDir)
	}

	fmt.Printf("Cleaned profile %s.\n", r.Label)
}

func reportWorkerReap(results []ptyworker.ReapResult) {
	if len(results) == 0 {
		fmt.Printf("  workers  none registered\n")
		return
	}
	byOutcome := map[ptyworker.ReapOutcome]int{}
	for _, res := range results {
		byOutcome[res.Outcome]++
	}
	fmt.Printf("  workers  %d registered (%s)\n", len(results), summarizeReap(byOutcome))
	for _, res := range results {
		if res.Outcome != ptyworker.ReapUnidentified {
			continue
		}
		fmt.Printf("           ! session %s: pid %d could not be confirmed as its worker (%v); left running — check it with `ps -p %d` and kill it yourself if it is stale\n",
			res.SessionID, res.WorkerPID, res.Err, res.WorkerPID)
	}
}

func reportProcReap(label, noun string, results []procreap.ReapResult) {
	if len(results) == 0 {
		fmt.Printf("  %-8s none registered\n", label)
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
	fmt.Printf("  %-8s %d registered (%s)\n", label, len(results), strings.Join(parts, ", "))
	for _, res := range results {
		switch res.Outcome {
		case procreap.ReapUnidentified:
			fmt.Printf("           ! %s %s: pid %d could not be confirmed as its process (%v); left running — check it with `ps -p %d` and kill it yourself if it is stale\n",
				noun, res.ID, res.PID, res.Err, res.PID)
		case procreap.ReapSurvived:
			fmt.Printf("           ! %s %s: pid %d survived SIGKILL (%v)\n",
				noun, res.ID, res.PID, res.Err)
		case procreap.ReapUnreadable:
			fmt.Printf("           ! record %s could not be read (%v); whatever it described was not reaped — check `ps` for stray %s processes\n",
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

// The pid file's flock, not its presence, is the liveness gate (daemonctl.Stop). Its
// own-process-tree error maps to a note, so cleaning the profile you run under is non-fatal.
func stopProfileDaemon(r profileResolved) string {
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

func runProfileStopApp(args []string) {
	profile := config.Profile()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			if i+1 >= len(args) {
				profileFatal("--profile requires a value")
			}
			i++
			p, err := config.NormalizeProfileName(args[i])
			if err != nil {
				profileFatal(err.Error())
			}
			profile = p
		case "-h", "--help":
			printProfileHelp(os.Stdout)
			return
		default:
			profileFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}
	msg, err := stopProfileApp(resolveProfile(profile))
	if err != nil {
		profileFatal(err.Error())
	}
	fmt.Printf("  app      %s\n", msg)
}

// An error means the app may still be running: callers that replace the install
// tree must stop rather than orphan a process on a deleted inode.
func stopProfileApp(r profileResolved) (string, error) {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("osascript", "-e", fmt.Sprintf("tell application id %q to quit", r.BundleID)).Run()
		return "asked " + r.BundleID + " to quit", nil
	}
	return stopProfileAppByPIDFile(r)
}

// The daemon's own stop waits, for a process that tears down less: measured at
// 72ms from SIGTERM to gone (2026-08-30, attn-linux VM under Xvfb).
const (
	appStopSigtermWait = 5 * time.Second
	appStopSigkillWait = 2 * time.Second
)

func stopProfileAppByPIDFile(r profileResolved) (string, error) {
	pidPath := appPIDFilePath(r.DataDir)
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "not running (no " + pidPath + ")", nil
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
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		_ = os.Remove(pidPath)
		return fmt.Sprintf("not running (pid %d is gone; removed stale %s)", pid, pidPath), nil
	}
	// An app still running out of a replaced install tree is exactly what we
	// must stop; Linux marks its unlinked image, the path is still ours.
	exe = strings.TrimSuffix(exe, " (deleted)")
	if !sameExecutable(exe, r.AppExecutable) {
		return "", fmt.Errorf("pid %d is %s, not %s; left running", pid, exe, r.AppExecutable)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			_ = os.Remove(pidPath)
			return fmt.Sprintf("not running (stale %s)", pidPath), nil
		}
		return "", fmt.Errorf("SIGTERM pid %d failed: %w", pid, err)
	}
	if appProcessGoneWithin(pid, appStopSigtermWait) {
		_ = os.Remove(pidPath)
		return fmt.Sprintf("stopped pid %d", pid), nil
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if appProcessGoneWithin(pid, appStopSigkillWait) {
		_ = os.Remove(pidPath)
		return fmt.Sprintf("force-killed pid %d (did not exit on SIGTERM)", pid), nil
	}
	return "", fmt.Errorf("pid %d survived SIGKILL; check it with ps -p %d", pid, pid)
}

// /proc/<pid>/exe is already resolved, so an install root behind a symlink
// (a symlinked XDG_DATA_HOME) only matches once both sides are.
func sameExecutable(a, b string) bool {
	return a == b || resolvedPath(a) == resolvedPath(b)
}

func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// Written by the Tauri shell at startup, removed on a clean exit.
func appPIDFilePath(dataDir string) string {
	return filepath.Join(dataDir, "app.pid")
}

func appProcessGoneWithin(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

func lsregisterForget(appPath string) {
	if runtime.GOOS != "darwin" || !fileExists(lsregisterPath) {
		return
	}
	_ = exec.Command(lsregisterPath, "-u", appPath).Run()
}

func runProfileList(args []string) {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "-h", "--help":
			printProfileHelp(os.Stdout)
			return
		default:
			profileFatal(fmt.Sprintf("unknown flag %q for `attn profile list`", a))
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		profileFatal("cannot resolve home directory: " + err.Error())
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
				if config.ValidateProfileName(p) == nil {
					known[strings.ToLower(p)] = true
				}
			}
		}
	}

	for _, p := range installedAppProfiles(home) {
		known[p] = true
	}

	names := make([]string, 0, len(known))
	for p := range known {
		names = append(names, p)
	}
	sort.Strings(names)

	active := config.Profile()

	if asJSON {
		entries := make([]profileListEntry, 0, len(names))
		for _, p := range names {
			entries = append(entries, newProfileListEntry(p, active))
		}
		out, err := json.MarshalIndent(map[string]any{"profiles": entries}, "", "  ")
		if err != nil {
			profileFatal(err.Error())
		}
		fmt.Println(string(out))
		return
	}

	fmt.Printf("%-3s %-16s %-7s %-9s %-11s %s\n", "", "PROFILE", "PORT", "DATA", "APP", "ORIGIN")
	for _, p := range names {
		r := resolveProfile(p)
		marker := "  "
		if p == active {
			marker = "* "
		}
		origin := "—"
		if o := readProfileOrigin(r.DataDir); o != nil {
			origin = filepath.Base(o.Worktree)
		}
		fmt.Printf("%-3s %-16s %-7s %-9s %-11s %s\n",
			marker,
			r.Label,
			r.WSPort,
			ynLabel(fileExists(r.DataDir), "yes", "—"),
			ynLabel(fileExists(r.AppPath), "installed", "—"),
			origin,
		)
	}
	fmt.Println("\n* = active (ATTN_PROFILE)")
}

func installedAppProfiles(home string) []string {
	root := filepath.Dir(config.AppPathForProfile(""))
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
		if strings.EqualFold(name, config.AppNameForProfile("")) {
			found = append(found, "")
			continue
		}
		if p, ok := strings.CutPrefix(strings.ToLower(name), "attn-"); ok && config.ValidateProfileName(p) == nil {
			found = append(found, p)
		}
	}
	return found
}

func printProfileHelp(w *os.File) {
	fmt.Fprintln(w, `attn profile — inspect and resolve attn profiles

A profile fully isolates attn's runtime: data dir, socket, websocket port,
installed app (a macOS bundle, a directory tree elsewhere), and bundle identifier. ATTN_PROFILE selects it for every
entrypoint (CLI, daemon, e2e, real-app harness, build).

Usage:
  attn profile                 status of the active profile (ATTN_PROFILE)
  attn profile status          same
  attn profile resolve         resolved resources as JSON
  attn profile resolve --field wsPort      print one resolved value
  attn profile resolve --profile agent7    resolve a different profile
  attn profile tauri-config    Tauri --config overlay for the profile's build
  attn profile clean <name>    reap workers + hosts + plugin drivers, stop daemon, quit app, remove its app + data dir
  attn profile stop-app        quit the active profile's app (--profile <name> for another)
  attn profile list            every profile with data and/or an installed app
  attn profile list --json     same, machine-readable, with origin and what is running
  attn profile set-origin <name> [--worktree <dir>]
                               record the worktree a profile was installed from
  attn profile env <name>      alias of: attn profile-env <name>

Profile names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is the development
sibling (port 29849, ~/.attn-dev). `+"`clean`"+` refuses the default (production)
profile unless given --force.`)
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

func profileFatal(msg string) {
	fmt.Fprintln(os.Stderr, "attn profile: "+msg)
	os.Exit(1)
}
