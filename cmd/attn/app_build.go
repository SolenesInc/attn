package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func runAppNew(args []string) {
	var dir, name, description string
	rest := args
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--name" && i+1 < len(rest):
			i++
			name = rest[i]
		case a == "--description" && i+1 < len(rest):
			i++
			description = rest[i]
		case a == "-h" || a == "--help":
			writeAppHelp(os.Stdout)
			return
		case strings.HasPrefix(a, "-"):
			appFail("new", fmt.Errorf("unknown flag %q", a))
		default:
			if dir != "" {
				appFail("new", fmt.Errorf("takes one directory; got %q and %q", dir, a))
			}
			dir = a
		}
	}
	if dir == "" {
		fmt.Fprintln(os.Stderr, "usage: attn app new <path> [--name <name>] [--description <text>]")
		os.Exit(2)
	}
	manifest, err := appbuild.Scaffold(appbuild.ScaffoldOptions{
		Dir:         dir,
		Name:        name,
		Description: description,
		StoreDir:    config.AppsDir(),
		Log:         func(line string) { fmt.Fprintf(os.Stderr, "  %s\n", line) },
	})
	if err != nil {
		appFail("new", err)
	}
	abs, _ := filepath.Abs(dir)
	fmt.Printf("created app %s in %s\n", manifest.Name, abs)
	fmt.Printf("  edit src/index.ts, then: attn app apply %s\n", dir)
	fmt.Printf("  AGENTS.md (and CLAUDE.md, a symlink to it) is the whole brief — an agent needs nothing else\n")
}

func runAppApply(args []string) {
	dir, asJSON := appPathArgs("apply", args)
	result, res, err := applyApp(dir, os.Stderr)
	if err != nil {
		appFail("apply", err)
	}
	if asJSON {
		writeJSON(result)
		return
	}
	printApplied(result, res)
}

// A refused apply's artifact stays behind on purpose: the CLI cannot tell a refusal from a commit whose response was lost, so deleting it would sometimes delete the bundle a live version points at.
func applyApp(dir string, progress *os.File) (*protocol.AppApplyResult, appbuild.Result, error) {
	res, err := appbuild.Build(context.Background(), appbuild.Options{
		Dir:      dir,
		StoreDir: config.AppsDir(),
		Log: func(line string) {
			if progress != nil {
				fmt.Fprintf(progress, "  %s\n", line)
			}
		},
	})
	if err != nil {
		return nil, appbuild.Result{}, err
	}
	abs, _ := filepath.Abs(dir)
	result, err := appClient().AppApply(res.Manifest.Name, res.ContentHash, res.Declaration, abs)
	if err != nil {
		return nil, appbuild.Result{}, err
	}
	return result, res, nil
}

func printApplied(result *protocol.AppApplyResult, res appbuild.Result) {
	moved := result.PreviousVersionID != nil && *result.PreviousVersionID != result.VersionID
	var state string
	switch {
	case result.VersionCreated:
		state = fmt.Sprintf("new, %d bytes", totalArtifactBytes(res))
	case moved:
		state = "this content already had a version; nothing new was recorded"
	default:
		state = "unchanged — byte-identical to the version it already was"
	}
	fmt.Printf("applied app %s: version %d (%s), %s\n",
		result.Name, result.VersionID, appbuild.ShortHash(result.ContentHash), state)
	if moved {
		fmt.Printf("  was on version %d\n", *result.PreviousVersionID)
	}
	fmt.Printf("  artifact %s\n", result.ArtifactPath)
	// There is no bundle size cap — nothing measured would justify a number — so naming each artifact's size is what apply owes an author instead.
	if len(res.ViewBytes) > 0 {
		fmt.Printf("    %s  %d bytes\n", appbuild.ArtifactName, res.BundleBytes)
		for _, v := range res.ViewBytes {
			fmt.Printf("    views/%s.js  %d bytes\n", v.Name, v.Bytes)
		}
	}
}

func totalArtifactBytes(res appbuild.Result) int64 {
	total := res.BundleBytes
	for _, v := range res.ViewBytes {
		total += v.Bytes
	}
	return total
}

func runAppRollback(args []string) {
	var name string
	versionID := 0
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help":
			writeAppHelp(os.Stdout)
			return
		case strings.HasPrefix(a, "-"):
			appFail("rollback", fmt.Errorf("unknown flag %q", a))
		case name == "":
			name = a
		case versionID == 0:
			n, err := strconv.Atoi(a)
			if err != nil || n <= 0 {
				appFail("rollback", fmt.Errorf("%q is not a version id; `attn app status %s` lists them", a, name))
			}
			versionID = n
		default:
			appFail("rollback", fmt.Errorf("takes an app name and at most one version id; got an extra %q", a))
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: attn app rollback <name> [version]")
		os.Exit(2)
	}
	result, err := appClient().AppRollback(name, versionID)
	if err != nil {
		appFail("rollback", err)
	}
	if asJSON {
		writeJSON(result)
		return
	}
	target := fmt.Sprintf("version %d (%s)", result.VersionID, appbuild.ShortHash(result.ContentHash))
	switch {
	case versionID == 0 && result.PreviousVersionID != nil:
		fmt.Printf("rolled app %s back to %s, which was serving before version %d\n",
			result.Name, target, *result.PreviousVersionID)
	case result.PreviousVersionID != nil && *result.PreviousVersionID < result.VersionID:
		fmt.Printf("moved app %s forward to %s\n  was on version %d\n",
			result.Name, target, *result.PreviousVersionID)
	case result.PreviousVersionID != nil:
		fmt.Printf("rolled app %s back to %s\n  was on version %d\n",
			result.Name, target, *result.PreviousVersionID)
	default:
		fmt.Printf("rolled app %s back to %s\n", result.Name, target)
	}
	fmt.Printf("  artifact %s\n", result.ArtifactPath)
}

// 200ms: an editor's save is several filesystem events (write, rename, attribute change) plus a formatter's, past the tail of that burst and under the point a person notices a delay.
const devDebounce = 200 * time.Millisecond

func runAppDev(args []string) {
	dir, _ := appPathArgs("dev", args)
	abs, err := filepath.Abs(dir)
	if err != nil {
		appFail("dev", err)
	}
	manifest, err := appbuild.LoadManifest(abs)
	if err != nil {
		appFail("dev", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		appFail("dev", fmt.Errorf("watching %s: %w", abs, err))
	}
	defer watcher.Close()
	if err := watchAppTree(watcher, abs); err != nil {
		appFail("dev", err)
	}

	fmt.Printf("watching %s — every change is parsed, typechecked, bundled and applied\n", abs)
	fmt.Printf("shows apply results, build errors, and every handler invocation as it runs\n")
	fmt.Printf("ctrl-c to stop\n\n")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	stopWatching := make(chan struct{})
	defer close(stopWatching)
	go devWatchInvocations(manifest.Name, stopWatching)

	devApply(abs)

	var timer <-chan time.Time
	for {
		select {
		case <-stop:
			fmt.Println("\nstopped watching")
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !devRelevant(event.Name) {
				continue
			}
			// Watch new directories too, or edits inside one silently stop rebuilding.
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = watchAppTree(watcher, event.Name)
				}
			}
			timer = time.After(devDebounce)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
		case <-timer:
			timer = nil
			devApply(abs)
		}
	}
}

func devApply(dir string) {
	started := time.Now()
	result, res, err := applyApp(dir, nil)
	stamp := time.Now().Format("15:04:05")
	if err != nil {
		fmt.Printf("%s  not applied:\n%v\n\n", stamp, err)
		return
	}
	fmt.Printf("%s  ", stamp)
	printApplied(result, res)
	fmt.Printf("  in %s\n\n", time.Since(started).Round(time.Millisecond))
}

const devDialRetry = 2 * time.Second

func devWatchInvocations(app string, stop <-chan struct{}) {
	for {
		_ = appClient().AppWatch(app, stop, func(inv protocol.AppInvocationInfo) bool {
			fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), devInvocationLine(inv))
			return true
		})
		select {
		case <-stop:
			return
		case <-time.After(devDialRetry):
		}
	}
}

func devInvocationLine(inv protocol.AppInvocationInfo) string {
	line := fmt.Sprintf("%s  %s [%s]", inv.Status, inv.Handler, appInvocationWork(inv))
	if inv.DurationMs != nil {
		line += fmt.Sprintf(" in %dms", *inv.DurationMs)
	}
	if inv.Error != nil && *inv.Error != "" {
		line += "\n            " + *inv.Error
	}
	return line
}

// Also drops the generated file the build rewrites: a manifest edit changes it, so without this each rebuild triggers another.
func devRelevant(path string) bool {
	base := filepath.Base(path)
	switch base {
	case filepath.Base(appbuild.GeneratedFile):
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "node_modules" || part == ".git" {
			return false
		}
	}
	// Editors write through temporary files; reacting rebuilds a half-written tree.
	return !strings.HasSuffix(base, "~") && !strings.HasPrefix(base, ".#")
}

func watchAppTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if name := entry.Name(); path != root && (name == "node_modules" || name == ".git") {
			return filepath.SkipDir
		}
		if err := watcher.Add(path); err != nil {
			return fmt.Errorf("watching %s: %w", path, err)
		}
		return nil
	})
}

func appPathArgs(verb string, args []string) (string, bool) {
	dir := ""
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			if verb != "apply" {
				appFail(verb, errors.New("--json is only for apply"))
			}
			asJSON = true
		case a == "-h" || a == "--help":
			writeAppHelp(os.Stdout)
			os.Exit(0)
		case strings.HasPrefix(a, "-"):
			appFail(verb, fmt.Errorf("unknown flag %q", a))
		case dir != "":
			appFail(verb, fmt.Errorf("takes one directory; got %q and %q", dir, a))
		default:
			dir = a
		}
	}
	if dir == "" {
		dir = "."
	}
	return dir, asJSON
}
