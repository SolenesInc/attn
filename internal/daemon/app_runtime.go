package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

const (
	// Also the reserved app name in internal/apps: `attn app logs runtime` must
	// mean one thing.
	appRuntimeChildName = "runtime"

	// The build, this daemon and the hub remote install all have to agree on it.
	appRuntimeBinaryName = apps.RuntimeHostBinaryName

	// Bump it here and in apphost/src/index.ts together —
	// TestAppRuntimeAPIVersionMatchesTheHost fails when only one moves.
	appRuntimeAPIVersion = 5
)

const appRuntimeHostOverride = "ATTN_APP_RUNTIME_HOST"

// No PATH search: a daemon launched by the macOS app has a minimal PATH and
// would find a different binary, or a stranger.
func resolveAppRuntimeHost() (string, error) {
	if override := strings.TrimSpace(os.Getenv(appRuntimeHostOverride)); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("%s points at %s, which is not there (%v)", appRuntimeHostOverride, override, err)
		}
		return override, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating this daemon's own executable to find %s beside it: %w", appRuntimeBinaryName, err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolving this daemon's own executable to find %s beside it: %w", appRuntimeBinaryName, err)
	}

	candidates := appRuntimeHostCandidates(executable, config.Profile())
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf(
		"the app runtime binary %s is not installed; looked in %s. It is built by `make build-app-runtime-host` (or any `make install`), and %s overrides the location",
		appRuntimeBinaryName, strings.Join(candidates, " and "), appRuntimeHostOverride)
}

func appRuntimeHostCandidates(executable, profile string) []string {
	candidates := []string{}
	binDir := filepath.Dir(executable)
	if resources := config.InstallResourcesDir(executable); resources != "" {
		candidates = append(candidates, filepath.Join(resources, "app-runtime", appRuntimeBinaryName))
	}
	// A named profile looks for its own copy first — profile-isolated daemons on a
	// remote share one `~/.local/bin`; the unsuffixed name stays last.
	if profile != "" {
		candidates = append(candidates, filepath.Join(binDir, apps.RuntimeHostBinaryNameForProfile(profile)))
	}
	return append(candidates, filepath.Join(binDir, appRuntimeBinaryName))
}

// Not under appsDir: everything there is named after an app, and `log` would
// collide with an app called log.
func appRuntimeLogDir(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "app-runtime-log")
}

func AppRuntimeLogPath(socketPath string) string {
	return filepath.Join(appRuntimeLogDir(socketPath), appRuntimeChildName+".log")
}

// Duplicated in apphost/src/index.ts; the parity test is
// TestAppLogTagMatchesTheHost.
func appRuntimeAppTag(app string) string { return "[app " + app + "] " }

const appRuntimeSelfTag = "[runtime] "

func (d *Daemon) ensureAppRuntimeSupervisor() *supervise.Supervisor {
	d.appRuntimeMu.Lock()
	defer d.appRuntimeMu.Unlock()
	if d.appRuntimeSupervisor == nil {
		options := d.appRuntimeSupervise
		options.LogDir = appRuntimeLogDir(d.socketPath)
		options.OnChange = func(string) {
			d.publishFact(FactAppRuntimeChanged, appRuntimeChildName, nil)
		}
		options.OnGiveUp = d.recordAppRuntimeParked
		options.Logf = d.logf
		d.appRuntimeSupervisor = supervise.New(options)
		d.adoptPersistedParkLocked(d.appRuntimeSupervisor)
	}
	return d.appRuntimeSupervisor
}

// Lives in the constructor because every way to reach the supervisor goes through
// ensureAppRuntimeSupervisor, so no caller can outrun the restore.
func (d *Daemon) adoptPersistedParkLocked(supervisor *supervise.Supervisor) {
	if d.store == nil {
		return
	}
	park, ok, err := d.store.GetSupervisedPark(appRuntimeChildName)
	if err != nil {
		d.logf("apps: reading the persisted app-runtime park: %v", err)
		return
	}
	if !ok {
		return
	}
	exit := &supervise.Exit{
		At:       park.ExitAt,
		ExitCode: park.ExitCode,
		Signal:   park.ExitSignal,
		Error:    park.ExitError,
	}
	if err := supervisor.AdoptParked(appRuntimeChildName, supervise.Park{
		ParkedAt:       park.ParkedAt,
		RestartAttempt: park.RestartAttempt,
		LastExit:       exit,
	}); err != nil {
		d.logf("apps: restoring the app runtime's park: %v", err)
		return
	}
	d.logf("app runtime is still parked from %s (%d restarts, last exit: %s); `attn app runtime restart` tries again",
		park.ParkedAt.Format(time.RFC3339), park.RestartAttempt, exit.String())
}

// Building it unconditionally would report a stopped runtime where the truth is
// that none was ever started.
func (d *Daemon) restoreAppRuntimePark() {
	if d.store == nil {
		return
	}
	if _, ok, err := d.store.GetSupervisedPark(appRuntimeChildName); err != nil || !ok {
		return
	}
	d.ensureAppRuntimeSupervisor()
}

// Doubles as un-park: supervise.Ensure resets the restart counter, which is what
// `attn app runtime restart` needs after a crash loop.
func (d *Daemon) ensureAppRuntime() error {
	return d.startAppRuntime(true)
}

// Reviving here would make parking unreachable: the bus retries a failing delivery
// forever. Measured on a broken host: three parkings in five and a half minutes.
func (d *Daemon) startAppRuntimeForDispatch() error {
	return d.startAppRuntime(false)
}

func (d *Daemon) startAppRuntime(revive bool) error {
	host, err := resolveAppRuntimeHost()
	if err != nil {
		return err
	}
	// exec refuses to chdir into a directory that is not there, so a daemon with no
	// apps would fail to start the runtime for an unrelated reason.
	if err := os.MkdirAll(d.appsDir, 0o755); err != nil {
		return fmt.Errorf("creating the app artifact directory %s to start the runtime in: %w", d.appsDir, err)
	}
	start := func(req supervise.StartRequest) (supervise.Process, error) {
		cmd := exec.Command(host)
		cmd.Dir = d.appsDir
		cmd.Env = d.appRuntimeEnv(req.Generation)
		process, err := supervise.StartCommand(cmd, req.Log)
		if err != nil {
			return nil, fmt.Errorf("start the app runtime (%s): %w", host, err)
		}
		return process, nil
	}
	supervisor := d.ensureAppRuntimeSupervisor()
	if !revive {
		return supervisor.EnsureUnlessParked(appRuntimeChildName, start)
	}
	err = supervisor.Ensure(appRuntimeChildName, start)
	// Keeping the durable record would re-park on the next daemon start.
	d.forgetAppRuntimePark()
	return err
}

func (d *Daemon) forgetAppRuntimePark() {
	if d.store == nil {
		return
	}
	cleared, err := d.store.ClearSupervisedPark(appRuntimeChildName)
	if err != nil {
		d.logf("apps: clearing the persisted app-runtime park: %v", err)
		return
	}
	if cleared {
		d.logf("app runtime revived; it will start again on the next daemon start too")
	}
}

// The daemon may itself be running inside an agent session whose CLAUDE_CODE_*
// variables would leak into app code.
func (d *Daemon) appRuntimeEnv(generation uint64) []string {
	return d.pluginCommandEnv(
		"ATTN_SOCKET_PATH="+d.socketPath,
		"ATTN_APP_RUNTIME_GENERATION="+strconv.FormatUint(generation, 10),
	)
}

func (d *Daemon) stopAppRuntime() {
	d.appRuntimeMu.Lock()
	supervisor := d.appRuntimeSupervisor
	d.appRuntimeMu.Unlock()
	if supervisor != nil {
		supervisor.Shutdown()
	}
}

func (d *Daemon) appRuntimeSnapshot() (supervise.Snapshot, bool) {
	d.appRuntimeMu.Lock()
	supervisor := d.appRuntimeSupervisor
	d.appRuntimeMu.Unlock()
	if supervisor == nil {
		return supervise.Snapshot{}, false
	}
	return supervisor.Snapshot(appRuntimeChildName)
}

const notificationKindAppRuntimeParked = "app_runtime_parked"

// Persisting comes first: a daemon that dies between the two writes should come
// back parked and silent rather than running and about to crash-loop.
func (d *Daemon) recordAppRuntimeParked(_ string, snapshot supervise.Snapshot) {
	detail := ""
	if snapshot.LastExit != nil {
		detail = snapshot.LastExit.String()
	}
	d.logf("app runtime parked after %d restarts without a stable connection: %s", snapshot.RestartAttempt, detail)
	if d.store == nil {
		return
	}
	d.persistAppRuntimePark(snapshot)
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:     notificationKindAppRuntimeParked,
		Severity: store.NotificationCritical,
		Title:    "Apps stopped running",
		Body: fmt.Sprintf(
			"attn restarted the shared app runtime %d times without it ever staying up, and has stopped trying. No app's handlers are running. `attn app runtime status` shows why it exited; `attn app runtime restart` tries again.",
			snapshot.RestartAttempt),
		Detail:     detail,
		SourceKind: "app_runtime",
		SourceID:   appRuntimeChildName,
	}, d.appNow())
	if err != nil {
		d.logf("notifications: add app-runtime-parked notification: %v", err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

// A park that lived only in memory made every daemon restart lazy-start the same
// broken host and raise a second critical notification for one outage.
func (d *Daemon) persistAppRuntimePark(snapshot supervise.Snapshot) {
	park := store.SupervisedPark{
		Child:          appRuntimeChildName,
		ParkedAt:       snapshot.ParkedAt,
		RestartAttempt: snapshot.RestartAttempt,
	}
	if park.ParkedAt.IsZero() {
		park.ParkedAt = d.appNow()
	}
	if snapshot.LastExit != nil {
		park.ExitAt = snapshot.LastExit.At
		park.ExitCode = snapshot.LastExit.ExitCode
		park.ExitSignal = snapshot.LastExit.Signal
		park.ExitError = snapshot.LastExit.Error
	}
	if err := d.store.SaveSupervisedPark(park); err != nil {
		d.logf("apps: persisting the app-runtime park: %v", err)
	}
}

func (d *Daemon) appNow() time.Time {
	if d.appClock != nil {
		return d.appClock()
	}
	return time.Now()
}

// A tripwire, not a budget: a scaffolded handler doing real document work measured
// 0–1ms warm (receipt in the plan doc).
const appDispatchTimeout = 60 * time.Second

// Artifact is an absolute path, and that is the whole hot-reload story: versions
// are content-addressed and `import()` caches by path.
type appDispatchRequest struct {
	Dispatch    string           `json:"dispatch"`
	App         string           `json:"app"`
	VersionID   int64            `json:"version_id"`
	Artifact    string           `json:"artifact"`
	Handler     string           `json:"handler"`
	Collections []string         `json:"collections"`
	Event       appDispatchEvent `json:"event"`
}

type appDispatchEvent struct {
	Name        string `json:"name"`
	Subject     string `json:"subject"`
	Seq         int64  `json:"seq"`
	Payload     any    `json:"payload"`
	PublishedAt string `json:"published_at"`
}

type appDispatchResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type appCommandRequest struct {
	Dispatch    string          `json:"dispatch"`
	App         string          `json:"app"`
	VersionID   int64           `json:"version_id"`
	Artifact    string          `json:"artifact"`
	Handler     string          `json:"handler"`
	Collections []string        `json:"collections"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

type appReconcileRequest struct {
	Dispatch    string             `json:"dispatch"`
	App         string             `json:"app"`
	VersionID   int64              `json:"version_id"`
	Artifact    string             `json:"artifact"`
	Collections []string           `json:"collections"`
	Reason      appReconcileReason `json:"reason"`
}

type appReconcileReason struct {
	Causes           []string         `json:"causes"`
	Version          int64            `json:"version"`
	ThroughSeq       int64            `json:"throughSeq"`
	Gap              *appReconcileGap `json:"gap,omitempty"`
	PreviousVersions []int64          `json:"previousVersions"`
}

type appReconcileGap struct {
	Cursor   int64 `json:"cursor"`
	Earliest int64 `json:"earliest"`
	Missed   int64 `json:"missed"`
}

// A handler that returned nothing carries no payload, which is different from
// one that returned null.
type appCommandDispatchResult struct {
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Why an app cannot address another app documents: the namespace is resolved
// from the record here — the wire has no namespace field for an app to fill in.
type appDispatch struct {
	id          string
	app         string
	namespace   string
	versionID   int64
	collections map[string]struct{}
}

func (d *Daemon) registerAppDispatch(dispatch *appDispatch) {
	d.appDispatchMu.Lock()
	defer d.appDispatchMu.Unlock()
	if d.appDispatches == nil {
		d.appDispatches = make(map[string]*appDispatch)
	}
	d.appDispatchSeq++
	dispatch.id = strconv.FormatUint(d.appDispatchSeq, 10)
	d.appDispatches[dispatch.id] = dispatch
}

func (d *Daemon) releaseAppDispatch(id string) {
	d.appDispatchMu.Lock()
	defer d.appDispatchMu.Unlock()
	delete(d.appDispatches, id)
}

// An id no longer in flight is a handler that used its context after returning,
// and is refused rather than served against whatever app is running now.
func (d *Daemon) lookupAppDispatch(id string) (*appDispatch, error) {
	d.appDispatchMu.Lock()
	defer d.appDispatchMu.Unlock()
	dispatch, ok := d.appDispatches[id]
	if !ok {
		return nil, fmt.Errorf(
			"dispatch %s is not in flight; a collection can only be reached from inside the handler it was given to, and this call arrived after that handler returned",
			id)
	}
	return dispatch, nil
}

type appRuntimeFailure struct{ err error }

func (e *appRuntimeFailure) Error() string { return e.err.Error() }
func (e *appRuntimeFailure) Unwrap() error { return e.err }

func runtimeFailure(format string, args ...any) error {
	return &appRuntimeFailure{err: fmt.Errorf(format, args...)}
}

func isRuntimeFailure(err error) bool {
	var failure *appRuntimeFailure
	return errors.As(err, &failure)
}

func (d *Daemon) appRuntimeConnected() *appRuntimeConnection {
	d.appRuntimeMu.Lock()
	defer d.appRuntimeMu.Unlock()
	return d.appRuntimeConn
}
