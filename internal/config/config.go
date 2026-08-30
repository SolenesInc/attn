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
	// No loadConfig() here: package init runs before any TestMain, so an eager
	// load would trip attnDir()'s go-test backstop. Loading is lazy instead.
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

// Callers that read loadedConfig (DBPath, SocketPath) must call this first.
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

var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,15}$`)

func Profile() string {
	raw := strings.TrimSpace(os.Getenv("ATTN_PROFILE"))
	if raw == "" {
		return ""
	}
	normalized := strings.ToLower(raw)
	if !profileNamePattern.MatchString(normalized) {
		return ""
	}
	return normalized
}

func ValidateProfile() error {
	raw := os.Getenv("ATTN_PROFILE")
	if err := ValidateProfileName(raw); err != nil {
		return fmt.Errorf("invalid ATTN_PROFILE=%q: must match ^[a-z0-9][a-z0-9-]{0,15}$", strings.TrimSpace(raw))
	}
	return nil
}

func ProfileLabel() string {
	if p := Profile(); p != "" {
		return p
	}
	return "default"
}

func DeepLinkScheme() string {
	return DeepLinkSchemeForProfile(Profile())
}

func normalizeProfileForDerivation(profile string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "default" || !profileNamePattern.MatchString(p) {
		return ""
	}
	return p
}

// Single source of truth: Makefile, Rust build, and harness derive from this
// via `attn profile resolve`.
func BundleIdentifierForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "com.attn.manager"
	}
	return "com.attn.manager." + p
}

// Must match the Tauri productName.
func AppNameForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "attn"
	}
	return "attn-" + p
}

func AppPathForProfile(profile string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	name := AppNameForProfile(profile)
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

func AppExecutableForProfile(profile string) string {
	return AppExecutableInTree(AppPathForProfile(profile))
}

func AppExecutableInTree(appPath string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(appPath, "Contents", "MacOS", "app")
	}
	return filepath.Join(appPath, "bin", "attn-app")
}

func AppDaemonBinaryForProfile(profile string) string {
	return AppDaemonBinaryInTree(AppPathForProfile(profile))
}

func AppDaemonBinaryInTree(appPath string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(appPath, "Contents", "MacOS", "attn")
	}
	return filepath.Join(appPath, "bin", "attn")
}

// ~/.local/bin/attn has a bin/ and no install tree, so resources/ must exist
// before a tree counts as one.
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

// A distinct scheme per bundle, so macOS never cross-routes a spawn deep link
// to the wrong app.
func DeepLinkSchemeForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "attn"
	}
	return "attn-" + p
}

func ValidateProfileName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	normalized := strings.ToLower(trimmed)
	if !profileNamePattern.MatchString(normalized) {
		return fmt.Errorf("invalid profile name %q: must match ^[a-z0-9][a-z0-9-]{0,15}$", name)
	}
	return nil
}

// Lowercase+trim (a mixed-case form splits data dirs on the remote); the literal
// "default" maps to "" (else it builds ~/.attn-default while reusing port 9849).
func NormalizeProfileName(name string) (string, error) {
	if err := ValidateProfileName(name); err != nil {
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
	return defaultAttnDir(Profile())
}

// Presence-only check backstopping the 2026-07-18 production-DB loss: set
// ATTN_DATA_DIR to a temp dir, never redirect HOME.
func requireExplicitDataDirUnderTest() {
	if testing.Testing() && strings.TrimSpace(os.Getenv("ATTN_DATA_DIR")) == "" {
		panic("config: ATTN_DATA_DIR is not set under go test — tests must never resolve the real data dir. " +
			"Set ATTN_DATA_DIR to a temp dir (os.Setenv in a package TestMain, or t.Setenv per-test). " +
			"Never redirect HOME to work around this.")
	}
}

// ATTN_DB_PATH/ATTN_SOCKET_PATH/ATTN_CONFIG_PATH/ATTN_PLUGIN_DIR outrank the attnDir()
// chokepoint, so an inherited one could still route test I/O at the real database.
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

func defaultAttnDir(profile string) string {
	home, err := os.UserHomeDir()
	base := "/tmp/.attn"
	if err == nil {
		base = filepath.Join(home, ".attn")
	}
	if profile != "" {
		return base + "-" + profile
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

// Deliberately bypasses the attnDir() chokepoint — no ATTN_DATA_DIR override,
// no go-test backstop — so cross-profile probing works; tests must never write through this path.
func DataDirForProfile(profile string) string {
	home, err := os.UserHomeDir()
	base := "/tmp/.attn"
	if err == nil {
		base = filepath.Join(home, ".attn")
	}
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "default" {
		return base
	}
	if !profileNamePattern.MatchString(p) {
		return base
	}
	return base + "-" + p
}

// Same chokepoint bypass as DataDirForProfile; tests must never write here.
func SocketPathForProfile(profile string) string {
	return filepath.Join(DataDirForProfile(profile), "attn.sock")
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

// A runtime root (socket/PID/workers) split from the profile's data dir while
// still using its default DB lets an auxiliary daemon reap live sessions.
func ValidateDaemonIsolation(socketPath string) error {
	socketDir, err := comparableDaemonIsolationPath(filepath.Dir(strings.TrimSpace(socketPath)))
	if err != nil {
		return fmt.Errorf("resolve daemon socket root: %w", err)
	}
	profileDataDir, err := comparableDaemonIsolationPath(DataDir())
	if err != nil {
		return fmt.Errorf("resolve profile data dir: %w", err)
	}
	if socketDir == profileDataDir {
		return nil
	}

	dbPath, err := comparableDaemonIsolationPath(DBPath())
	if err != nil {
		return fmt.Errorf("resolve daemon DB path: %w", err)
	}
	defaultDBPath, err := comparableDaemonIsolationPath(filepath.Join(profileDataDir, "attn.db"))
	if err != nil {
		return fmt.Errorf("resolve profile DB path: %w", err)
	}
	if dbPath != defaultDBPath {
		return nil
	}

	return fmt.Errorf(
		"refusing to start daemon with socket root %q while DB path still resolves to the %s profile store %q; set ATTN_DB_PATH to an isolated database or use ATTN_PROFILE",
		socketDir,
		ProfileLabel(),
		defaultDBPath,
	)
}

func comparableDaemonIsolationPath(path string) (string, error) {
	return CanonicalRuntimePath(path)
}

// Routing checks must compare through this, never raw env/config strings (which may be CWD-relative).
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

// Bypasses the attnDir() chokepoint; tests must never write through this path.
func StatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/." + binaryName + "-state.json"
	}
	suffix := ""
	if p := Profile(); p != "" {
		suffix = "-" + p
	}
	return filepath.Join(home, "."+binaryName+"-state"+suffix+".json")
}

// Mirrors Tauri's BaseDirectory.AppLocalData resolution: the app shell writes the
// automation manifest and the frontend's debug JSONL here, WebKit its own state.
func AppLocalDataDirForProfile(profile string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	bundleID := BundleIdentifierForProfile(profile)
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", bundleID)
	}
	return filepath.Join(xdgDataHome(home), bundleID)
}

func AppLocalDataDir() string {
	return AppLocalDataDirForProfile(Profile())
}

func LogPath() string {
	return filepath.Join(attnDir(), "daemon.log")
}

// Default profile → 9849, "dev" → 29849, any other named profile a stable hash-derived
// port in [20000,29848]. The e2e port 19849 sits outside that range.
func WSPort() string {
	port := strings.TrimSpace(os.Getenv("ATTN_WS_PORT"))
	if port != "" {
		return port
	}
	return WSPortForProfile(Profile())
}

func WSPortForProfile(profile string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	switch p {
	case "", "default":
		return "9849"
	case "dev":
		return "29849"
	default:
		if !profileNamePattern.MatchString(p) {
			return "9849"
		}
		return derivedProfilePort(p)
	}
}

func profileFNV(profile string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(profile))
	return h.Sum32()
}

// Reserves 29849 for "dev" so future named profiles never collide with it.
func derivedProfilePort(profile string) string {
	port := 20000 + int(profileFNV(profile)%9849)
	return fmt.Sprintf("%d", port)
}

// default → 19849, named profiles hash into [30000,30999] — disjoint from prod
// 9849, dev 29849, the real-profile band [20000,29848], and Vite 1420/1421.
func E2EDaemonPortForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "19849"
	}
	return fmt.Sprintf("%d", 30000+int(profileFNV(p)%1000))
}

// named profiles hash into [31000,31999]; strictPort makes collisions fail loudly.
func E2EVitePortForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "1421"
	}
	return fmt.Sprintf("%d", 31000+int(profileFNV(p)%1000))
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

// The Tauri shell creates this file with owner-only permissions before it
// starts or connects to the daemon.
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
	// Force loopback so the endpoint can never be exposed off the machine.
	portPart := raw
	if i := strings.LastIndex(portPart, ":"); i >= 0 {
		portPart = portPart[i+1:]
	}
	if p, err := strconv.Atoi(portPart); err == nil && p > 0 && p <= 65535 {
		return fmt.Sprintf("127.0.0.1:%d", p), true
	}
	return "", false
}
