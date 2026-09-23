package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

const originFileName = "origin.json"

type instanceOrigin struct {
	Worktree   string `json:"worktree"`
	Branch     string `json:"branch,omitempty"`
	RecordedAt string `json:"recordedAt"`
}

func originPath(dataDir string) string { return filepath.Join(dataDir, originFileName) }

func writeInstanceOrigin(dataDir string, origin instanceOrigin) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	payload, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return err
	}
	path := originPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readInstanceOrigin(dataDir string) *instanceOrigin {
	data, err := os.ReadFile(originPath(dataDir))
	if err != nil {
		return nil
	}
	var origin instanceOrigin
	if err := json.Unmarshal(data, &origin); err != nil {
		return nil
	}
	if strings.TrimSpace(origin.Worktree) == "" {
		return nil
	}
	return &origin
}

func runInstanceSetOrigin(args []string) {
	name := ""
	worktree := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--worktree":
			if i+1 >= len(args) {
				instanceFatal("--worktree requires a directory")
			}
			i++
			worktree = args[i]
		case "-h", "--help":
			printInstanceHelp(os.Stdout)
			return
		default:
			if strings.HasPrefix(args[i], "-") {
				instanceFatal(fmt.Sprintf("unknown flag %q", args[i]))
			}
			if name != "" {
				instanceFatal(fmt.Sprintf("set-origin takes a single instance name, got %q and %q", name, args[i]))
			}
			name = args[i]
		}
	}
	if name == "" {
		instanceFatal("set-origin requires an instance name (e.g. `attn instance set-origin agent7`)")
	}
	normalized, err := config.NormalizeInstanceName(name)
	if err != nil {
		instanceFatal(err.Error())
	}
	if normalized == "" {
		instanceFatal("refusing to record an origin for the default (production) instance")
	}
	if worktree == "" {
		worktree, err = os.Getwd()
		if err != nil {
			instanceFatal(err.Error())
		}
	}
	abs, err := filepath.Abs(worktree)
	if err != nil {
		instanceFatal(err.Error())
	}

	origin := instanceOrigin{
		Worktree:   abs,
		Branch:     gitBranchAt(abs),
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	dataDir := config.DataDirForInstance(normalized)
	if err := writeInstanceOrigin(dataDir, origin); err != nil {
		instanceFatal(fmt.Sprintf("record origin: %v", err))
	}
	fmt.Printf("recorded origin for instance %s: %s", normalized, origin.Worktree)
	if origin.Branch != "" {
		fmt.Printf(" (%s)", origin.Branch)
	}
	fmt.Println()
}

func gitBranchAt(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return ""
	}
	return branch
}

type instanceListEntry struct {
	Instance      string          `json:"instance"`
	Label         string          `json:"label"`
	DataDir       string          `json:"dataDir"`
	AppPath       string          `json:"appPath"`
	AppLocalData  string          `json:"appLocalDataDir"`
	WSPort        string          `json:"wsPort"`
	Active        bool            `json:"active"`
	HasData       bool            `json:"hasData"`
	HasApp        bool            `json:"hasApp"`
	HasAppLocal   bool            `json:"hasAppLocalData"`
	DaemonRunning bool            `json:"daemonRunning"`
	LiveWorkers   int             `json:"liveWorkers"`
	Origin        *instanceOrigin `json:"origin,omitempty"`
}

func newInstanceListEntry(instance string, active string) instanceListEntry {
	r := resolveInstance(instance)
	return instanceListEntry{
		Instance:      instance,
		Label:         r.Label,
		DataDir:       r.DataDir,
		AppPath:       r.AppPath,
		AppLocalData:  r.AppLocalData,
		WSPort:        r.WSPort,
		Active:        instance == active,
		HasData:       fileExists(r.DataDir),
		HasApp:        fileExists(r.AppPath),
		HasAppLocal:   fileExists(r.AppLocalData),
		DaemonRunning: socketLive(r.Socket),
		LiveWorkers:   countLiveWorkers(r.DataDir),
		Origin:        readInstanceOrigin(r.DataDir),
	}
}

func socketLive(path string) bool {
	if path == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func countLiveWorkers(dataDir string) int {
	paths, err := filepath.Glob(filepath.Join(dataDir, "workers", "*", "registry", "*.json"))
	if err != nil {
		return 0
	}
	live := make(map[int]struct{})
	for _, path := range paths {
		entry, err := ptyworker.ReadRegistry(path)
		if err != nil {
			continue
		}
		if ptyworker.ProcessAlive(entry.WorkerPID) {
			live[entry.WorkerPID] = struct{}{}
		}
	}
	for _, path := range ptyhost.HostRegistryPaths(dataDir) {
		entry, err := ptyhost.ReadHostRegistry(path)
		if err == nil && ptyworker.ProcessAlive(entry.HostPID) {
			live[entry.HostPID] = struct{}{}
		}
	}
	return len(live)
}
