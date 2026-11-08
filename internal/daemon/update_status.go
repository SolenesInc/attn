package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/selfupdate"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
)

const (
	releaseCheckTimer    stepclock.Name = "daemon-release-check"
	releaseCheckInterval                = 6 * time.Hour
	releaseLookupTimeout                = 30 * time.Second
)

type releaseAvailability struct {
	mu        sync.Mutex
	available selfupdate.Release
	checkedAt time.Time
	lastError string
}

func (d *Daemon) runReleaseCheckLoop() {
	d.checkLatestRelease()
	d.every(releaseCheckTimer, releaseCheckInterval, d.checkLatestRelease)
}

func (d *Daemon) checkLatestRelease() {
	if !d.install().GetsReleases() {
		return
	}
	ctx, cancel := context.WithTimeout(d.life.Context(), releaseLookupTimeout)
	defer cancel()
	latest, err := selfupdate.FindLatest(ctx, buildinfo.ReleaseSource)
	r := &d.releases
	r.mu.Lock()
	r.checkedAt = time.Now()
	if err != nil {
		r.lastError = err.Error()
	} else {
		r.lastError = ""
		r.available = selfupdate.Release{}
		if selfupdate.IsNewer(buildinfo.Version, latest.Version) {
			r.available = latest
		}
	}
	r.mu.Unlock()
	if err != nil {
		d.logf("release check: %v", err)
	}
	d.publishFact(FactSettingChanged, setting.UpdateAvailableVersion.Key, nil)
}

func (d *Daemon) install() selfupdate.Install {
	executable, err := os.Executable()
	if err != nil {
		d.logf("update status: locate this executable: %v", err)
	}
	return selfupdate.Detect(selfupdate.Facts{
		Channel:     buildinfo.Channel,
		Fingerprint: buildinfo.SourceFingerprint,
		Repository:  buildinfo.RepositoryPath,
		Instance:    config.Instance(),
		Enrolled:    d.enrollmentStatus().IsOutpost(),
		Executable:  executable,
	})
}

func (d *Daemon) updateStatus() protocol.UpdateStatus {
	install := d.install()
	status := protocol.UpdateStatus{
		Kind:      string(install.Kind),
		Channel:   buildinfo.Channel,
		Version:   buildinfo.Version,
		Commit:    buildinfo.GitCommit,
		BuildTime: buildinfo.BuildTime,
		Dirty:     buildinfo.Dirty,
	}
	if install.Kind == selfupdate.Unknown {
		status.Refusal = protocol.Ptr(install.RefusalNamingChannel().Error())
	}
	r := &d.releases
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.available.Version != "" {
		status.AvailableVersion = protocol.Ptr(r.available.Version)
		status.AvailableURL = protocol.Ptr(r.available.URL)
	}
	if !r.checkedAt.IsZero() {
		status.CheckedAt = protocol.Ptr(r.checkedAt.UTC().Format(time.RFC3339))
	}
	if r.lastError != "" {
		status.CheckError = protocol.Ptr(r.lastError)
	}
	return status
}

func (d *Daemon) addUpdateSettings(settings map[string]any) {
	status := d.updateStatus()
	settings[setting.UpdateInstallKind.Key] = status.Kind
	settings[setting.UpdateCurrentVersion.Key] = status.Version
	settings[setting.UpdateCurrentCommit.Key] = status.Commit
	settings[setting.UpdateAvailableVersion.Key] = protocol.Deref(status.AvailableVersion)
	settings[setting.UpdateAvailableURL.Key] = protocol.Deref(status.AvailableURL)
	settings[setting.UpdateCheckedAt.Key] = protocol.Deref(status.CheckedAt)
	settings[setting.UpdateCheckError.Key] = protocol.Deref(status.CheckError)
}

func (d *Daemon) handleUpdateStatus(ctx context.Context, conn net.Conn) {
	if !d.on(setting.SelfUpdateEnabled) {
		d.sendError(conn, setting.SelfUpdateEnabled.Refusal().Error())
		return
	}
	status := d.updateStatus()
	if origin, remote := OriginOf(ctx); remote {
		status.HomeKind = protocol.Ptr(status.Kind)
		status.Kind = string(selfupdate.Outpost)
		status.Outpost = protocol.Ptr(origin.Name)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, UpdateStatus: &status})
}
