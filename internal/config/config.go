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
	"strconv"
	"strings"
	"sync"
	"testing"
)

var binaryName string

func init() {
	binaryName = filepath.Base(os.Args[0])
}

func BinaryName() string {
	return binaryName
}

func SetBinaryName(name string) {
	binaryName = name
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

func reloadConfig() {
	loadConfig()
}

func ReloadForTesting() {
	loadConfig()
}

var instanceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,15}$`)

func Instance() string {
	raw := strings.TrimSpace(os.Getenv("ATTN_INSTANCE"))
	if raw == "" {
		return ""
	}
	normalized := strings.ToLower(raw)
	if !instanceNamePattern.MatchString(normalized) {
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
	if p == "" || p == "default" || !instanceNamePattern.MatchString(p) {
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
	if runtime.GOOS == "darwin" {
		return filepath.Join(appPath, "Contents", "MacOS", "app")
	}
	return filepath.Join(appPath, "bin", "attn-app")
}

func AppDaemonBinaryForInstance(instance string) string {
	return AppDaemonBinaryInTree(AppPathForInstance(instance))
}

func AppDaemonBinaryInTree(appPath string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(appPath, "Contents", "MacOS", "attn")
	}
	return filepath.Join(appPath, "bin", "attn")
}

func InstallResourcesDir(executable string) string {
	binDir := filepath.Dir(executable)
	parent := filepath.Dir(binDir)
	if filepath.Base(binDir) == "MacOS" && filepath.Base(parent) == "Contents" {
		return filepath.Join(parent, "Resources")
	}
	if filepath.Base(binDir) == "bin" {
		resources := filepath.Join(parent, "resources")
		if info, err := os.Stat(resources); err == nil && info.IsDir() {
			return resources
		}
	}
	return ""
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
	if !instanceNamePattern.MatchString(normalized) {
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

func AppsDir() string {
	return filepath.Join(attnDir(), "apps")
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
	if !instanceNamePattern.MatchString(p) {
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

func ValidateDaemonIsolation(socketPath string) error {
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
	if label == "" || !instanceNamePattern.MatchString(label) {
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

func WSPortForInstance(instance string) string {
	p := strings.ToLower(strings.TrimSpace(instance))
	switch p {
	case "", "default":
		return "9849"
	case "dev":
		return "29849"
	default:
		if !instanceNamePattern.MatchString(p) {
			return "9849"
		}
		return derivedInstancePort(p)
	}
}

func instanceFNV(instance string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(instance))
	return h.Sum32()
}

func derivedInstancePort(instance string) string {
	port := 20000 + int(instanceFNV(instance)%9849)
	return fmt.Sprintf("%d", port)
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

func WSAuthToken() string {
	return strings.TrimSpace(os.Getenv("ATTN_WS_AUTH_TOKEN"))
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
