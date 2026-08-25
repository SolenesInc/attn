package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Previous is carried rather than read back: the runtime draining the outgoing
// version's handlers would otherwise read a pointer that already moved.
type appVersionChanged struct {
	Name        string `json:"name"`
	VersionID   int64  `json:"version_id"`
	PreviousID  int64  `json:"previous_version_id,omitempty"`
	ContentHash string `json:"content_hash"`
	Reason      string `json:"reason"`
}

const (
	appVersionReasonApply    = "apply"
	appVersionReasonRollback = "rollback"
)

func (d *Daemon) handleAppApply(conn net.Conn, msg *protocol.AppApplyMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	lane := d.appLane(name)
	lane.Lock()
	defer lane.Unlock()
	hash := strings.TrimSpace(msg.ContentHash)
	if err := validateContentHash(hash); err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: %v", name, err))
		return
	}
	declaration := msg.Declaration
	if err := validateDeclaration(name, declaration); err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: %v", name, err))
		return
	}

	// Derived from the app and the hash, never taken from the caller.
	path := appbuild.ArtifactPath(d.appsDir, name, hash)
	bundle, err := os.ReadFile(path)
	if err != nil {
		d.sendError(conn, fmt.Sprintf(
			"app apply %s: no built artifact at %s (%v); the build places it there before asking the daemon to record it, so this apply was not built by this attn's data directory (%s)",
			name, path, err, d.appsDir))
		return
	}
	viewNames, err := appbuild.DeclaredViewNames(declaration)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: %v", name, err))
		return
	}
	views, err := appbuild.ReadViewArtifacts(d.appsDir, name, hash, viewNames)
	if err != nil {
		d.sendError(conn, fmt.Sprintf(
			"app apply %s: %v; the build places every declared view beside the bundle before asking the daemon to record it, so this apply was not built by this attn's data directory (%s)",
			name, err, d.appsDir))
		return
	}
	if actual := appbuild.VersionHash(declaration, bundle, views); actual != hash {
		d.sendError(conn, fmt.Sprintf(
			"app apply %s: the artifacts at %s hash to %s, not the %s this apply claims; nothing was recorded",
			name, appbuild.ArtifactDir(d.appsDir, name, hash), actual, hash))
		return
	}

	previous, err := d.currentAppVersion(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: %v", name, err))
		return
	}
	if previous != 0 {
		current, ok, err := d.store.GetAppVersion(previous)
		if err != nil {
			d.sendError(conn, fmt.Sprintf("app apply %s: reading current version %d: %v", name, previous, err))
			return
		}
		if ok && current.ContentHash != hash {
			if err := requireVersionMoveReconcile(name, fmt.Sprintf("version %d", previous), "content "+appbuild.ShortHash(hash), declaration); err != nil {
				d.sendError(conn, "app apply "+name+": "+err.Error())
				return
			}
		}
	}
	version, created, err := d.store.CommitAppVersion(store.AppVersion{
		AppName:      name,
		ContentHash:  hash,
		Declaration:  declaration,
		ArtifactPath: path,
	}, time.Now())
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: recording the version: %v", name, err))
		return
	}
	if source := strings.TrimSpace(protocol.Deref(msg.SourcePath)); source != "" {
		d.logf("app apply %s: version %d (%s) from %s", name, version.ID, appbuild.ShortHash(hash), source)
	}
	d.syncAppRuntimeForVersion(name)
	d.publishAppVersionChanged(name, version, previous, appVersionReasonApply)

	result := protocol.AppApplyResult{
		Name:           name,
		VersionID:      int(version.ID),
		ContentHash:    version.ContentHash,
		ArtifactPath:   version.ArtifactPath,
		VersionCreated: created,
	}
	if previous != 0 {
		result.PreviousVersionID = protocol.Ptr(int(previous))
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, AppApplyResult: &result})
}

func (d *Daemon) handleAppRollback(conn net.Conn, msg *protocol.AppRollbackMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	lane := d.appLane(name)
	lane.Lock()
	defer lane.Unlock()
	app, ok, err := d.store.GetApp(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
		return
	}
	if !ok {
		d.sendError(conn, d.unknownAppError("rollback", name))
		return
	}
	versions, err := d.store.ListAppVersions(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the versions of app %q: %v", name, err))
		return
	}
	target, err := pickRollbackTarget(name, app, versions, msg.VersionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if err := requireVersionMoveReconcile(name, fmt.Sprintf("version %d", app.CurrentVersionID), fmt.Sprintf("version %d", target.ID), target.Declaration); err != nil {
		d.sendError(conn, "app rollback "+name+": "+err.Error())
		return
	}
	if msg.VersionID == nil {
		err = d.store.StepAppVersionBack(name, target.ID, time.Now())
	} else {
		err = d.store.SetAppCurrentVersion(name, target.ID, time.Now())
	}
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app rollback %s: %v", name, err))
		return
	}
	d.syncAppRuntimeForVersion(name)
	d.publishAppVersionChanged(name, target, app.CurrentVersionID, appVersionReasonRollback)

	result := protocol.AppRollbackResult{
		Name:         name,
		VersionID:    int(target.ID),
		ContentHash:  target.ContentHash,
		ArtifactPath: target.ArtifactPath,
	}
	if app.CurrentVersionID != 0 {
		result.PreviousVersionID = protocol.Ptr(int(app.CurrentVersionID))
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, AppRollbackResult: &result})
}

// versions arrives newest-id-first, as ListAppVersions returns it.
func pickRollbackTarget(name string, app store.App, versions []store.AppVersion, requested *int) (store.AppVersion, error) {
	if len(versions) == 0 {
		return store.AppVersion{}, fmt.Errorf("app rollback %s: it has no versions to roll back to; `attn app apply <path>` builds its first", name)
	}
	if requested != nil {
		id := int64(*requested)
		for _, v := range versions {
			if v.ID == id {
				if id == app.CurrentVersionID {
					return store.AppVersion{}, fmt.Errorf("app rollback %s: it is already on version %d; %s", name, id, versionsSentence(versions, app.CurrentVersionID))
				}
				return v, nil
			}
		}
		return store.AppVersion{}, fmt.Errorf("app rollback %s: version %d is not a version of this app; %s", name, id, versionsSentence(versions, app.CurrentVersionID))
	}
	// One step back along the serving history, not the numerically previous id:
	// an app that went good, broken, fixed has the broken one below its pointer.
	if app.CurrentVersionID == 0 {
		return store.AppVersion{}, fmt.Errorf("app rollback %s: it is not on any version, so there is no back to go to; name one of them. %s",
			name, versionsSentence(versions, 0))
	}
	if app.PreviousServingVersionID == 0 {
		return store.AppVersion{}, fmt.Errorf("app rollback %s: version %d is the oldest version in its serving history, so there is no further back to go; name the version to roll onto. %s",
			name, app.CurrentVersionID, versionsSentence(versions, app.CurrentVersionID))
	}
	for _, v := range versions {
		if v.ID == app.PreviousServingVersionID {
			return v, nil
		}
	}
	return store.AppVersion{}, fmt.Errorf("app rollback %s: version %d is recorded as serving before version %d but is not among this app's versions; name the version to roll onto. %s",
		name, app.PreviousServingVersionID, app.CurrentVersionID, versionsSentence(versions, app.CurrentVersionID))
}

func versionsSentence(versions []store.AppVersion, current int64) string {
	parts := make([]string, 0, len(versions))
	for _, v := range versions {
		label := fmt.Sprintf("%d (%s)", v.ID, appbuild.ShortHash(v.ContentHash))
		if v.ID == current {
			label += " — current"
		}
		parts = append(parts, label)
	}
	return "its versions, newest first: " + strings.Join(parts, ", ")
}

func (d *Daemon) publishAppVersionChanged(name string, version store.AppVersion, previous int64, reason string) {
	if version.ID == previous {
		return
	}
	d.publishFact(FactAppVersionChanged, name, appVersionChanged{
		Name:        name,
		VersionID:   version.ID,
		PreviousID:  previous,
		ContentHash: version.ContentHash,
		Reason:      reason,
	})
}

func (d *Daemon) currentAppVersion(name string) (int64, error) {
	app, ok, err := d.store.GetApp(name)
	if err != nil {
		return 0, fmt.Errorf("reading app %q: %w", name, err)
	}
	if !ok {
		return 0, nil
	}
	return app.CurrentVersionID, nil
}

func validateContentHash(hash string) error {
	const want = 64
	if len(hash) != want {
		return fmt.Errorf("content hash %q is %d characters; a version hash is %d lowercase hex characters", hash, len(hash), want)
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("content hash %q contains %q; a version hash is %d lowercase hex characters", hash, r, want)
		}
	}
	return nil
}

func validateDeclaration(name, declaration string) error {
	if strings.TrimSpace(declaration) == "" {
		return fmt.Errorf("the declaration snapshot is empty; a version records what its manifest said")
	}
	var probe struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(declaration), &probe); err != nil {
		return fmt.Errorf("the declaration snapshot is not JSON: %w", err)
	}
	if probe.Name != name {
		return fmt.Errorf("the declaration snapshot names app %q, not %q", probe.Name, name)
	}
	return nil
}

func requireVersionMoveReconcile(name, current, requested, declaration string) error {
	var manifest appbuild.Manifest
	if err := json.Unmarshal([]byte(declaration), &manifest); err != nil {
		return fmt.Errorf("the declaration for requested %s is not readable: %w", requested, err)
	}
	if len(manifest.EventPatterns()) == 0 || manifest.Reconcile {
		return nil
	}
	return fmt.Errorf(
		"refusing to move %s from %s to %s: the requested subscribed version does not declare reconcile; add `reconcile = true` and implement the reconcile export so existing collections can be rebuilt",
		name, current, requested)
}
