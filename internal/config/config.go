package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var binaryName string

func init() {
	binaryName = filepath.Base(os.Args[0])
}

type configFile struct {
	DBPath     string `json:"db_path"`
	SocketPath string `json:"socket_path"`
}

var (
	loadedConfig configFile
	configLoaded bool
	configMu     sync.RWMutex
)

func ensureConfigLoaded() {
	configMu.RLock()
	loaded := configLoaded
	configMu.RUnlock()
	if !loaded {
		loadConfig()
	}
}

func loadConfig() {
	configMu.Lock()
	defer configMu.Unlock()

	loadedConfig = configFile{}
	configLoaded = true

	configPath := os.Getenv("ATTN_CONFIG_PATH")
	if configPath == "" {
		configPath = filepath.Join(attnDir(), "config.json")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	json.Unmarshal(data, &loadedConfig)
}

func ReloadForTesting() {
	loadConfig()
}

var instanceNamePattern = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,15}$`)
})

func Instance() string {
	raw := strings.TrimSpace(os.Getenv("ATTN_INSTANCE"))
	if raw == "" {
		return ""
	}
	normalized := strings.ToLower(raw)
	if !instanceNamePattern().MatchString(normalized) {
		return ""
	}
	return normalized
}

func ValidateInstance() error {
	raw := os.Getenv("ATTN_INSTANCE")
	if err := ValidateInstanceName(raw); err != nil {
		return fmt.Errorf("invalid ATTN_INSTANCE=%q: must match ^[a-z0-9][a-z0-9-]{0,15}$", strings.TrimSpace(raw))
	}
	return nil
}

func InstanceLabel() string {
	if p := Instance(); p != "" {
		return p
	}
	return "default"
}

func DeepLinkScheme() string {
	return DeepLinkSchemeForInstance(Instance())
}

func normalizeInstanceForDerivation(instance string) string {
	p := strings.ToLower(strings.TrimSpace(instance))
	if p == "" || p == "default" || !instanceNamePattern().MatchString(p) {
		return ""
	}
	return p
}

func BundleIdentifierForInstance(instance string) string {
	p := normalizeInstanceForDerivation(instance)
	if p == "" {
		return "com.attn.manager"
	}
	return "com.attn.manager." + p
}

func AppNameForInstance(instance string) string {
	p := normalizeInstanceForDerivation(instance)
	if p == "" {
		return "attn"
	}
	return "attn-" + p
}

func AppPathForInstance(instance string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	name := AppNameForInstance(instance)
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Applications", name+".app")
	}
	return filepath.Join(xdgDataHome(home), name)
}

func xdgDataHome(home string) string {
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return dataHome
	}
	return filepath.Join(home, ".local", "share")
}

func AppExecutableForInstance(instance string) string {
	return AppExecutableInTree(AppPathForInstance(instance))
}

func AppExecutableInTree(appPath string) string {
	if runtime.GOOS != "darwin" {
		return filepath.Join(appPath, "bin", "attn-app")
	}
	native := filepath.Join(appPath, "Contents", "MacOS", "attn-app")
	if _, err := os.Stat(native); err == nil {
		return native
	}
	return filepath.Join(appPath, "Contents", "MacOS", "app")
}

func BuildIdentityInTree(appPath string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(appPath, "Contents", "Resources", "build-identity.json")
	}
	return filepath.Join(appPath, "resources", "build-identity.json")
}

func AppDaemonBinaryForInstance(instance string) string {
	return AppDaemonBinaryInTree(AppPathForInstance(instance))
}

func AppCliBinaryForInstance(instance string) string {
	return filepath.Join(filepath.Dir(AppDaemonBinaryForInstance(instance)), "attn")
}

func AppDaemonBinaryInTree(appPath string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(appPath, "Contents", "MacOS", "attnd")
	}
	return filepath.Join(appPath, "bin", "attnd")
}

func DeepLinkSchemeForInstance(instance string) string {
	p := normalizeInstanceForDerivation(instance)
	if p == "" {
		return "attn"
	}
	return "attn-" + p
}

func ValidateInstanceName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	normalized := strings.ToLower(trimmed)
	if !instanceNamePattern().MatchString(normalized) {
		return fmt.Errorf("invalid instance name %q: must match ^[a-z0-9][a-z0-9-]{0,15}$", name)
	}
	return nil
}

func NormalizeInstanceName(name string) (string, error) {
	if err := ValidateInstanceName(name); err != nil {
		return "", err
	}
	canonical := strings.ToLower(strings.TrimSpace(name))
	if canonical == "default" {
		canonical = ""
	}
	return canonical, nil
}

func attnDir() string {
	if override := strings.TrimSpace(os.Getenv("ATTN_DATA_DIR")); override != "" {
		return filepath.Clean(override)
	}
	requireExplicitDataDirUnderTest()
	return defaultAttnDir(Instance())
}

func requireExplicitDataDirUnderTest() {
	if testing.Testing() && strings.TrimSpace(os.Getenv("ATTN_DATA_DIR")) == "" {
		panic("config: ATTN_DATA_DIR is not set under go test — tests must never resolve the real data dir. " +
			"Set ATTN_DATA_DIR to a temp dir (os.Setenv in a package TestMain, or t.Setenv per-test). " +
			"Never redirect HOME to work around this.")
	}
}

func ScopeTestEnvironment(dataDir string) {
	if !testing.Testing() {
		panic("config.ScopeTestEnvironment is test-only")
	}
	os.Setenv("ATTN_DATA_DIR", dataDir)
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")
	os.Unsetenv("ATTN_PLUGIN_DIR")
	os.Unsetenv("ATTN_CLIENT_TOKEN")
	os.Unsetenv(AnalyticsIngestURLEnv)
	unsetInheritedGitConfig()
}

// Git config injected through the environment (such as url.insteadOf rewrites) changes what attn's git calls see.
func unsetInheritedGitConfig() {
	os.Unsetenv("GIT_CONFIG_PARAMETERS")
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "GIT_CONFIG_COUNT" || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			os.Unsetenv(name)
		}
	}
}

func defaultAttnDir(instance string) string {
	home, err := os.UserHomeDir()
	base := "/tmp/.attn"
	if err == nil {
		base = filepath.Join(home, ".attn")
	}
	if instance != "" {
		return base + "-" + instance
	}
	return base
}

const (
	AnalyticsIngestURLEnv      = "ATTN_ANALYTICS_INGEST_URL"
	CloudURLEnv                = "ATTN_CLOUD_URL"
	PairingLifetimeEnv         = "ATTN_PAIRING_LIFETIME"
	defaultAnalyticsIngestURL  = "https://attn-telemetry.caketroll.cloud"
	unreachableAnalyticsIngest = "http://127.0.0.1:1"
)

func AnalyticsIngestURL() string {
	if url := strings.TrimSpace(os.Getenv(AnalyticsIngestURLEnv)); url != "" {
		return strings.TrimRight(url, "/")
	}
	if nonProductionInstance() != "" {
		return unreachableAnalyticsIngest
	}
	return defaultAnalyticsIngestURL
}

func AnalyticsIngestOffReason() string {
	if strings.TrimSpace(os.Getenv(AnalyticsIngestURLEnv)) != "" {
		return ""
	}
	instance := nonProductionInstance()
	if instance == "" {
		return ""
	}
	return fmt.Sprintf("Uploads go to %s, where nothing listens, because this is %s, not production. Start its daemon with %s=<url> to upload to an attn-ingest.",
		unreachableAnalyticsIngest, instance, AnalyticsIngestURLEnv)
}

func nonProductionInstance() string {
	if instance := Instance(); instance != "" {
		return "instance " + instance
	}
	production := defaultAttnDir("")
	if dir := filepath.Clean(filepath.Dir(SocketPath())); dir != production {
		return "data folder " + dir
	}
	if db := filepath.Clean(DBPath()); db != filepath.Join(production, "attn.db") {
		return "database " + db
	}
	return ""
}

func DataDir() string {
	return attnDir()
}

func ConfigPath() string {
	if envPath := strings.TrimSpace(os.Getenv("ATTN_CONFIG_PATH")); envPath != "" {
		return filepath.Clean(envPath)
	}
	return filepath.Join(attnDir(), "config.json")
}

func PluginDir() string {
	if envPath := strings.TrimSpace(os.Getenv("ATTN_PLUGIN_DIR")); envPath != "" {
		return envPath
	}
	return filepath.Join(attnDir(), "plugins")
}

func DataDirForInstance(instance string) string {
	home, err := os.UserHomeDir()
	base := "/tmp/.attn"
	if err == nil {
		base = filepath.Join(home, ".attn")
	}
	p := strings.ToLower(strings.TrimSpace(instance))
	if p == "" || p == "default" {
		return base
	}
	if !instanceNamePattern().MatchString(p) {
		return base
	}
	return base + "-" + p
}

func SocketPathForInstance(instance string) string {
	return filepath.Join(DataDirForInstance(instance), "attn.sock")
}

func DBPath() string {
	if envPath := os.Getenv("ATTN_DB_PATH"); envPath != "" {
		return envPath
	}

	ensureConfigLoaded()
	configMu.RLock()
	configPath := loadedConfig.DBPath
	configMu.RUnlock()
	if configPath != "" {
		return configPath
	}

	return filepath.Join(attnDir(), "attn.db")
}

func SocketPath() string {
	if envPath := os.Getenv("ATTN_SOCKET_PATH"); envPath != "" {
		return envPath
	}

	ensureConfigLoaded()
	configMu.RLock()
	configPath := loadedConfig.SocketPath
	configMu.RUnlock()
	if configPath != "" {
		return configPath
	}

	return filepath.Join(attnDir(), "attn.sock")
}

const (
	sessionHomeEnv   = "ATTN_SESSION_HOME"
	sessionBinaryEnv = "ATTN_SESSION_BINARY"
)

func ValidateDaemonIsolation(socketPath string) error {
	if err := validateDaemonDatabaseIsolation(socketPath); err != nil {
		return err
	}
	return ValidateSessionHome()
}

func validateDaemonDatabaseIsolation(socketPath string) error {
	socketDir, err := comparableDaemonIsolationPath(filepath.Dir(strings.TrimSpace(socketPath)))
	if err != nil {
		return fmt.Errorf("resolve daemon socket root: %w", err)
	}
	instanceDataDir, err := comparableDaemonIsolationPath(DataDir())
	if err != nil {
		return fmt.Errorf("resolve instance data dir: %w", err)
	}
	if socketDir == instanceDataDir {
		return nil
	}

	dbPath, err := comparableDaemonIsolationPath(DBPath())
	if err != nil {
		return fmt.Errorf("resolve daemon DB path: %w", err)
	}
	defaultDBPath, err := comparableDaemonIsolationPath(filepath.Join(instanceDataDir, "attn.db"))
	if err != nil {
		return fmt.Errorf("resolve instance DB path: %w", err)
	}
	if dbPath != defaultDBPath {
		return nil
	}

	return fmt.Errorf(
		"refusing to start daemon with socket root %q while DB path still resolves to the %s instance store %q; set ATTN_DB_PATH to an isolated database or use ATTN_INSTANCE",
		socketDir,
		InstanceLabel(),
		defaultDBPath,
	)
}

// EnvOutsideSessions removes what marks a process as a session's child, so a
// process the daemon starts can replace the daemon as a person's terminal can.
func EnvOutsideSessions(env []string) []string {
	drop := []string{sessionHomeEnv, sessionBinaryEnv, "ATTN_WRAPPER_PATH", "ATTN_INSIDE_APP", "ATTN_DAEMON_MANAGED", "ATTN_PTY_WORKER", "ATTN_TERMINAL_ID", "ATTN_SESSION_ID", "ATTN_AGENT"}
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains(drop, name) {
			kept = append(kept, entry)
		}
	}
	return kept
}

func InstanceOriginPath(dataDir string) string { return filepath.Join(dataDir, "origin.json") }

// InstanceOriginWorktree is the worktree this instance was installed from, or "".
func InstanceOriginWorktree(dataDir string) string {
	data, err := os.ReadFile(InstanceOriginPath(dataDir))
	if err != nil {
		return ""
	}
	var origin struct {
		Worktree string `json:"worktree"`
	}
	if json.Unmarshal(data, &origin) != nil {
		return ""
	}
	return strings.TrimSpace(origin.Worktree)
}

func SessionMarkerEnv(dataDir string) []string {
	binary, _ := os.Executable()
	return []string{
		sessionHomeEnv + "=" + canonicalOrGiven(dataDir),
		sessionBinaryEnv + "=" + canonicalOrGiven(binary),
	}
}

func canonicalOrGiven(path string) string {
	if canonical, err := CanonicalRuntimePath(path); err == nil {
		return canonical
	}
	return path
}

func ValidateSessionHome() error {
	rawHome := strings.TrimSpace(os.Getenv(sessionHomeEnv))
	if rawHome == "" || normalizeInstanceForDerivation(Instance()) != "" {
		return nil
	}
	home, err := CanonicalRuntimePath(rawHome)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", sessionHomeEnv, err)
	}

	routes := []struct{ name, path string }{
		{"ATTN_DATA_DIR", DataDir()},
		{"ATTN_DB_PATH", DBPath()},
		{"ATTN_SOCKET_PATH", SocketPath()},
	}
	inside := 0
	var described strings.Builder
	for _, route := range routes {
		resolved, err := CanonicalRuntimePath(route.path)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", route.name, err)
		}
		where := "outside"
		if runtimePathWithin(resolved, home) {
			inside++
			where = "inside"
		}
		fmt.Fprintf(&described, "  %s=%s (%s)\n", route.name, resolved, where)
	}
	switch inside {
	case 0:
		return nil
	case len(routes):
		return validateSessionBinary(home)
	default:
		return fmt.Errorf(
			"refusing to start a daemon or open a database: this process is inside an attn session of %s, and its paths point only partly there:\n%sA partly inherited environment acts on that home's daemon or database.\nSelect an instance with `eval \"$(attn instance-env <name>)\"`, or point all three outside %s",
			home, described.String(), home,
		)
	}
}

func validateSessionBinary(home string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve this binary: %w", err)
	}
	this, err := CanonicalRuntimePath(executable)
	if err != nil {
		return fmt.Errorf("resolve this binary: %w", err)
	}
	sessionBinary := strings.TrimSpace(os.Getenv(sessionBinaryEnv))
	if this == sessionBinary || this == daemonBesideCLI(sessionBinary) {
		return nil
	}
	return fmt.Errorf(
		"refusing to start a daemon or open the database on %s from %s.\nThis process is inside an attn session whose daemon runs from %s.\nA different build here would replace that daemon and migrate its database.\nTo test this build, select an instance: eval \"$(%s instance-env <name>)\"\nTo make it the installed build, run `make install`. To run it anyway, start it from a terminal outside attn",
		home, this, sessionBinary, this,
	)
}

func daemonBesideCLI(binary string) string {
	if filepath.Base(binary) != "attn" {
		return binary
	}
	return filepath.Join(filepath.Dir(binary), "attnd")
}

func comparableDaemonIsolationPath(path string) (string, error) {
	return CanonicalRuntimePath(path)
}

func CanonicalRuntimePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)

	existing := absolute
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return absolute, nil
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
}

func StatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/." + binaryName + "-state.json"
	}
	suffix := ""
	if p := Instance(); p != "" {
		suffix = "-" + p
	}
	return filepath.Join(home, "."+binaryName+"-state"+suffix+".json")
}

func AppLocalDataDirForInstance(instance string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	bundleID := BundleIdentifierForInstance(instance)
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", bundleID)
	}
	return filepath.Join(xdgDataHome(home), bundleID)
}

func AppLocalDataDir() string {
	return AppLocalDataDirForInstance(Instance())
}

func AppLockPathForInstance(instance string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	label := strings.ToLower(strings.TrimSpace(instance))
	if label == "" || !instanceNamePattern().MatchString(label) {
		label = "default"
	}
	return filepath.Join(home, ".attn.locks", "app-"+label+".lock")
}

func LogPath() string {
	return filepath.Join(attnDir(), "daemon.log")
}

func WSPort() string {
	port := strings.TrimSpace(os.Getenv("ATTN_WS_PORT"))
	if port != "" {
		return port
	}
	return WSPortForInstance(Instance())
}

func instanceFNV(instance string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(instance))
	return h.Sum32()
}

func E2EDaemonPortForInstance(instance string) string {
	p := normalizeInstanceForDerivation(instance)
	if p == "" {
		return "19849"
	}
	return fmt.Sprintf("%d", 30000+int(instanceFNV(p)%1000))
}

func E2EVitePortForInstance(instance string) string {
	p := normalizeInstanceForDerivation(instance)
	if p == "" {
		return "1421"
	}
	return fmt.Sprintf("%d", 31000+int(instanceFNV(p)%1000))
}

func MockGitHubPortForInstance(instance string) string {
	p := normalizeInstanceForDerivation(instance)
	if p == "" {
		return "19850"
	}
	return fmt.Sprintf("%d", 32000+int(instanceFNV(p)%1000))
}

func WSBindAddress() string {
	addr := strings.TrimSpace(os.Getenv("ATTN_WS_BIND"))
	if addr == "" {
		return "127.0.0.1"
	}
	return addr
}

func BrowserHostToken() string {
	if token := strings.TrimSpace(os.Getenv("ATTN_BROWSER_HOST_TOKEN")); token != "" {
		return token
	}
	data, err := os.ReadFile(filepath.Join(attnDir(), "browser-host-token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func PIDPath() string {
	socketPath := SocketPath()
	return filepath.Join(filepath.Dir(socketPath), "attn.pid")
}

const (
	LogError = iota
	LogWarn
	LogInfo
	LogDebug
	LogTrace
)

func DebugLevel() int {
	switch os.Getenv("DEBUG") {
	case "trace":
		return LogTrace
	case "debug":
		return LogDebug
	case "info":
		return LogInfo
	case "warn":
		return LogWarn
	case "1", "true":
		return LogDebug
	default:
		return LogError
	}
}

const DefaultPprofPort = 6060

func PprofAddr() (addr string, enabled bool) {
	raw := strings.TrimSpace(os.Getenv("ATTN_PPROF"))
	if raw == "" {
		return "", false
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "no":
		return "", false
	case "1", "on", "true", "yes":
		return fmt.Sprintf("127.0.0.1:%d", DefaultPprofPort), true
	}
	portPart := raw
	if i := strings.LastIndex(portPart, ":"); i >= 0 {
		portPart = portPart[i+1:]
	}
	if p, err := strconv.Atoi(portPart); err == nil && p > 0 && p <= 65535 {
		return fmt.Sprintf("127.0.0.1:%d", p), true
	}
	return "", false
}

func ConversationsDir() string {
	return filepath.Join(DataDir(), "conversations")
}
