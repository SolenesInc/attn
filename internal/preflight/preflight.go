package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	StatusPass = "pass"
	StatusWarn = "warn"
	StatusFail = "fail"
)

type Options struct {
	Agent        string
	AgentSource  string
	Model        string
	ModelSource  string
	Effort       string
	EffortSource string
	WorkingDir   string
	AppPath      string
}

type ResolvedValue struct {
	Value  string `json:"value,omitempty"`
	Source string `json:"source"`
}

type Launch struct {
	Agent  ResolvedValue `json:"agent"`
	Model  ResolvedValue `json:"model"`
	Effort ResolvedValue `json:"effort"`
}

type Routing struct {
	Profile  string `json:"profile"`
	Label    string `json:"label"`
	DataDir  string `json:"data_dir"`
	Socket   string `json:"socket"`
	WSPort   string `json:"ws_port"`
	BundleID string `json:"bundle_id"`
	AppPath  string `json:"app_path"`
}

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Action  string `json:"action,omitempty"`
}

type Report struct {
	Status  string  `json:"status"`
	Routing Routing `json:"routing"`
	Launch  Launch  `json:"launch"`
	Checks  []Check `json:"checks"`
}

func (r Report) OK() bool { return r.Status != StatusFail }

type daemonHealth struct {
	Protocol         string `json:"protocol"`
	Profile          string `json:"profile"`
	DataDir          string `json:"data_dir"`
	SocketPath       string `json:"socket_path"`
	RoutingPathError string `json:"routing_path_error"`
	Port             any    `json:"port"`
	HeadlessTasks    string `json:"headless_tasks"`
}

type prober struct {
	lookPath      func(string) (string, error)
	writable      func(string) error
	pathIsSocket  func(string) error
	goCachePaths  func(context.Context) ([]string, error)
	appProtocol   func(context.Context, string) (string, error)
	daemonHealth  func(context.Context, string) (daemonHealth, error)
	daemonAgents  func(context.Context, string) ([]agentdriver.Descriptor, error)
	requiredTools []string
}

func defaultProber() prober {
	return prober{
		lookPath:      exec.LookPath,
		writable:      probeWritableDirectory,
		pathIsSocket:  requireSocket,
		goCachePaths:  resolveGoCachePaths,
		appProtocol:   readAppProtocol,
		daemonHealth:  fetchDaemonHealth,
		daemonAgents:  fetchDaemonAgents,
		requiredTools: []string{"git", "gh", "go", "pnpm", "cargo", "make"},
	}
}

func Run(ctx context.Context, opts Options) Report {
	return run(ctx, opts, defaultProber())
}

func run(ctx context.Context, opts Options, p prober) Report {
	routing, routingErr := resolveRouting()
	if appPath := strings.TrimSpace(opts.AppPath); appPath != "" {
		routing.AppPath = filepath.Clean(appPath)
	}
	launch := resolveLaunch(opts)
	report := Report{Status: StatusPass, Routing: routing, Launch: launch}
	add := func(check Check) { report.Checks = append(report.Checks, check) }

	agents, agentsErr := p.daemonAgents(ctx, routing.WSPort)
	agent := checkLaunchAgent(launch, agents, agentsErr, add)
	if agent != nil {
		if launch.Model.Value != "" && !agent.ModelPin {
			add(fail("launch.model", fmt.Sprintf("agent %q does not support model pins", launch.Agent.Value), "Remove --model or select an agent that supports model pins."))
		}
		if launch.Effort.Value != "" && !agent.EffortPin {
			add(fail("launch.effort", fmt.Sprintf("agent %q does not support effort pins", launch.Agent.Value), "Remove --effort or select an agent that supports effort pins."))
		}
	}

	tools := append([]string(nil), p.requiredTools...)
	if agent != nil && agentsErr != nil {
		tools = append(tools, agent.Executable)
	}
	sort.Strings(tools)
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool] {
			continue
		}
		seen[tool] = true
		path, err := p.lookPath(tool)
		if err != nil {
			add(fail("tool."+filepath.Base(tool), fmt.Sprintf("required tool %q was not found on PATH", tool), fmt.Sprintf("Install %s or configure its executable path, then rerun preflight.", tool)))
			continue
		}
		add(pass("tool."+filepath.Base(tool), path))
	}
	if agent != nil && agentsErr == nil {
		add(agentHealthCheck(*agent))
	}

	workingDir := strings.TrimSpace(opts.WorkingDir)
	if workingDir == "" {
		workingDir, _ = os.Getwd()
	}
	paths := []struct{ name, path, action string }{
		{"path.working_directory", workingDir, "Choose a writable checkout or fix its permissions."},
		{"path.profile_data", routing.DataDir, "Install or initialize the selected non-production profile, then fix the data directory permissions."},
		{"path.applications", filepath.Dir(routing.AppPath), "Create a writable app install directory for the selected profile."},
	}
	for _, item := range paths {
		if err := p.writable(item.path); err != nil {
			add(fail(item.name, fmt.Sprintf("%s is not writable: %v", item.path, err), item.action))
		} else {
			add(pass(item.name, item.path))
		}
	}

	cachePaths, err := p.goCachePaths(ctx)
	if err != nil {
		add(fail("path.go_caches", fmt.Sprintf("could not resolve Go cache paths: %v", err), "Run `go env GOCACHE GOMODCACHE` and fix the reported tool or cache-path problem."))
	} else {
		for i, path := range cachePaths {
			name := "path.go_build_cache"
			if i == 1 {
				name = "path.go_module_cache"
			}
			if err := p.writable(path); err != nil {
				add(fail(name, fmt.Sprintf("%s is not writable: %v", path, err), "Point Go at a writable cache or fix this directory's permissions."))
			} else {
				add(pass(name, path))
			}
		}
	}

	if routingErr != nil {
		add(fail("routing.socket", "could not canonicalize active routing paths: "+routingErr.Error(), "Use absolute paths for ATTN_DATA_DIR and ATTN_SOCKET_PATH, or fix inaccessible symlink ancestors."))
	} else if err := p.pathIsSocket(routing.Socket); err != nil {
		add(fail("routing.socket", fmt.Sprintf("expected daemon socket %s is unavailable: %v", routing.Socket, err), "Start the daemon for the active profile and verify ATTN_PROFILE/ATTN_SOCKET_PATH routing."))
	} else {
		add(pass("routing.socket", routing.Socket))
	}

	appProtocol, appErr := p.appProtocol(ctx, routing.AppPath)
	if appErr != nil {
		add(fail("protocol.cli_app", fmt.Sprintf("could not read the installed app protocol: %v", appErr), "Build and install this profile's app, then rerun preflight."))
	} else if appProtocol != protocol.ProtocolVersion {
		add(fail("protocol.cli_app", fmt.Sprintf("CLI protocol %s does not match installed app protocol %s", protocol.ProtocolVersion, appProtocol), "Rebuild and install the selected profile from this checkout."))
	} else {
		add(pass("protocol.cli_app", "CLI and installed app use protocol "+appProtocol))
	}

	health, healthErr := p.daemonHealth(ctx, routing.WSPort)
	if healthErr != nil {
		add(fail("routing.daemon", fmt.Sprintf("daemon health is unavailable on 127.0.0.1:%s: %v", routing.WSPort, healthErr), "Start the daemon for the active profile and verify the selected port is reachable."))
		add(fail("protocol.app_daemon", "app/daemon protocol compatibility could not be verified", "Start the selected profile's daemon and rerun preflight."))
	} else {
		actualPort := fmt.Sprint(health.Port)
		if health.RoutingPathError != "" {
			add(fail("routing.daemon", "daemon could not canonicalize its routing paths: "+health.RoutingPathError, "Use absolute daemon routing paths or fix inaccessible symlink ancestors, then restart this profile's daemon."))
		} else if health.Profile != routing.Label || health.DataDir != routing.DataDir || health.SocketPath != routing.Socket || actualPort != routing.WSPort {
			add(fail("routing.daemon", fmt.Sprintf("daemon routing mismatch: profile=%s data_dir=%s socket=%s port=%s", health.Profile, health.DataDir, health.SocketPath, actualPort), "Select the intended profile with `attn profile-env`, clear inherited routing overrides, and restart only that profile's daemon."))
		} else {
			add(pass("routing.daemon", fmt.Sprintf("profile=%s socket=%s port=%s", health.Profile, health.SocketPath, actualPort)))
		}
		if appErr != nil {
			add(fail("protocol.app_daemon", "app/daemon protocol compatibility could not be verified because the app protocol is unavailable", "Build and install this profile's app, then rerun preflight."))
		} else if appProtocol == health.Protocol {
			add(pass("protocol.app_daemon", "installed app and daemon use protocol "+health.Protocol))
		} else {
			add(fail("protocol.app_daemon", fmt.Sprintf("installed app protocol %s does not match daemon protocol %s", appProtocol, health.Protocol), "Restart the daemon from the selected profile's installed app."))
		}
	}

	add(pass("headless.tasks", headlessTasksSummary(health, healthErr)))

	for _, check := range report.Checks {
		if check.Status == StatusFail {
			report.Status = StatusFail
			break
		}
		if check.Status == StatusWarn {
			report.Status = StatusWarn
		}
	}
	return report
}

// The daemon's own mode is the one that decides what runs; this CLI's env only
// stands in when the daemon cannot be reached or predates the field.
func headlessTasksSummary(health daemonHealth, healthErr error) string {
	if healthErr == nil {
		if mode := strings.TrimSpace(health.HeadlessTasks); mode != "" {
			return "daemon headless tasks " + mode
		}
	}
	return "daemon mode unavailable; this CLI resolves headless tasks " + headless.Describe()
}

// The daemon is the process that spawns, so its catalog is the truth about an
// agent. Without it, only the built-in drivers this CLI carries can be checked.
func checkLaunchAgent(launch Launch, agents []agentdriver.Descriptor, agentsErr error, add func(Check)) *agentdriver.Descriptor {
	name := launch.Agent.Value
	if agentsErr != nil {
		driver := agentdriver.Get(name)
		if driver == nil {
			add(warn("launch.agent", fmt.Sprintf("daemon could not list its agents (%v), so preflight cannot tell whether a plugin provides agent %q", agentsErr, name), "Start this profile's daemon from this build and rerun preflight."))
			return nil
		}
		caps := agentdriver.EffectiveCapabilities(driver)
		add(warn("launch.agent", fmt.Sprintf("daemon could not list its agents (%v); %s was checked against this CLI's built-in driver instead", agentsErr, name), "Start this profile's daemon from this build and rerun preflight to check the launch against it."))
		return &agentdriver.Descriptor{Name: name, Executable: driver.ResolveExecutable(""), ModelPin: caps.HasModelPin, EffortPin: caps.HasEffortPin}
	}
	names := make([]string, 0, len(agents))
	for i := range agents {
		names = append(names, agents[i].Name)
		if agents[i].Name != name {
			continue
		}
		if agents[i].Plugin != "" {
			add(pass("launch.agent", fmt.Sprintf("plugin %s provides %s", agents[i].Plugin, name)))
		} else {
			add(pass("launch.agent", name+" is built into the daemon"))
		}
		return &agents[i]
	}
	add(fail("launch.agent", fmt.Sprintf("daemon does not provide agent %q; it provides %s", name, strings.Join(names, ", ")), "Select one of the daemon's agents with --agent, or install and connect the plugin that provides it."))
	return nil
}

func agentHealthCheck(agent agentdriver.Descriptor) Check {
	name := "tool." + filepath.Base(agent.Executable)
	if agent.Plugin != "" {
		name = "plugin." + agent.Plugin
	}
	switch agent.Health {
	case agentdriver.HealthHealthy:
		return pass(name, agent.Detail)
	case agentdriver.HealthUnhealthy:
		return fail(name, fmt.Sprintf("daemon cannot launch %s: %s", agent.Name, agent.Detail), fmt.Sprintf("Install %s or configure its executable path, then rerun preflight.", agent.Name))
	}
	return warn(name, fmt.Sprintf("daemon has not confirmed %s is launchable yet: %s", agent.Name, agent.Detail), "Wait for the plugin health check and rerun preflight.")
}

func resolveLaunch(opts Options) Launch {
	agentName := strings.ToLower(strings.TrimSpace(opts.Agent))
	if agentName == "" {
		agentName = "codex"
	}
	agentSource := opts.AgentSource
	if agentSource == "" {
		agentSource = "default"
	}
	return Launch{
		Agent:  ResolvedValue{Value: agentName, Source: agentSource},
		Model:  resolvedLaunchValue(opts.Model, opts.ModelSource),
		Effort: resolvedLaunchValue(strings.ToLower(strings.TrimSpace(opts.Effort)), opts.EffortSource),
	}
}

func resolvedLaunchValue(value, source string) ResolvedValue {
	value = strings.TrimSpace(value)
	if value == "" {
		return ResolvedValue{Source: "agent_default"}
	}
	if source == "" {
		source = "explicit"
	}
	return ResolvedValue{Value: value, Source: source}
}

func resolveRouting() (Routing, error) {
	profile := config.Profile()
	label := profile
	if label == "" {
		label = "default"
	}
	rawDataDir := config.DataDir()
	rawSocket := config.SocketPath()
	dataDir, dataErr := config.CanonicalRuntimePath(rawDataDir)
	if dataErr != nil {
		dataDir = absoluteRoutingFallback(rawDataDir)
	}
	socket, socketErr := config.CanonicalRuntimePath(rawSocket)
	if socketErr != nil {
		socket = absoluteRoutingFallback(rawSocket)
	}
	routing := Routing{
		Profile: profile, Label: label,
		DataDir: dataDir, Socket: socket,
		WSPort: config.WSPort(), BundleID: config.BundleIdentifierForProfile(profile),
		AppPath: config.AppPathForProfile(profile),
	}
	if dataErr != nil || socketErr != nil {
		return routing, fmt.Errorf("data_dir: %v; socket: %v", dataErr, socketErr)
	}
	return routing, nil
}

func absoluteRoutingFallback(path string) string {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func probeWritableDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	file, err := os.CreateTemp(path, ".attn-preflight-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func requireSocket(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("path is not a Unix socket")
	}
	return nil
}

func resolveGoCachePaths(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "env", "-json", "GOCACHE", "GOMODCACHE")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var values struct {
		Build  string `json:"GOCACHE"`
		Module string `json:"GOMODCACHE"`
	}
	if err := json.Unmarshal(output, &values); err != nil {
		return nil, err
	}
	if values.Build == "" || values.Module == "" {
		return nil, fmt.Errorf("go returned an empty cache path")
	}
	return []string{values.Build, values.Module}, nil
}

func readAppProtocol(ctx context.Context, appPath string) (string, error) {
	binary := config.AppDaemonBinaryInTree(appPath)
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("%s: %w", binary, err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, binary, "--protocol-version").Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("app returned an empty protocol version")
	}
	return value, nil
}

func fetchDaemonHealth(ctx context.Context, port string) (daemonHealth, error) {
	var health daemonHealth
	err := fetchDaemonJSON(ctx, port, "/health", &health)
	return health, err
}

func fetchDaemonAgents(ctx context.Context, port string) ([]agentdriver.Descriptor, error) {
	var payload struct {
		Agents []agentdriver.Descriptor `json:"agents"`
	}
	err := fetchDaemonJSON(ctx, port, "/agents", &payload)
	return payload.Agents, err
}

func fetchDaemonJSON(ctx context.Context, port, path string, out any) error {
	requestCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+net.JoinHostPort("127.0.0.1", port)+path, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func pass(name, summary string) Check { return Check{Name: name, Status: StatusPass, Summary: summary} }
func fail(name, summary, action string) Check {
	return Check{Name: name, Status: StatusFail, Summary: summary, Action: action}
}
func warn(name, summary, action string) Check {
	return Check{Name: name, Status: StatusWarn, Summary: summary, Action: action}
}
