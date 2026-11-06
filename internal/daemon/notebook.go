package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const originAgent = "agent"

const originExternal = "external"

const originUI = "ui"

func (d *Daemon) notebookStoreFor(profileID string) (*notebook.Store, error) {
	root, err := d.notebookRoot(profileID)
	if err != nil {
		return nil, err
	}
	d.notebookMu.Lock()
	if d.notebookStores == nil {
		d.notebookStores = make(map[string]*notebook.Store)
	}
	store := d.notebookStores[root]
	if store == nil {
		store = notebook.NewStore(root)
		d.notebookStores[root] = store
	}
	d.notebookMu.Unlock()
	d.ensureNotebookWatcher(root)
	return store, nil
}

func (d *Daemon) rootChanged(root string, paths []string) {
	d.broadcastFsChanged(root, originExternal, paths...)
	if !d.isNotebookRoot(root) {
		return
	}
	d.notebookFilesChanged()
	if !d.enrollmentStatus().IsHome() {
		return
	}
	artifactSeeds := map[string]struct{}{}
	for _, p := range paths {
		if seedID, ok := seedArtifactSeedFromNotebookPath(p); ok {
			artifactSeeds[seedID] = struct{}{}
		}
	}
	if len(artifactSeeds) > 0 {
		d.coalesceSnapshots(func() {
			for seedID := range artifactSeeds {
				if err := d.recordObservedSeedArtifacts(seedID); err != nil {
					d.logf("Garden artifact observation for %s: %v", seedID, err)
				}
			}
		})
	}
}

func (d *Daemon) notebookRoot(profileID string) (string, error) {
	if status := d.enrollmentStatus(); status.IsOutpost() {
		return d.outpostNotebookRoot(status, profileID)
	}
	return homeNotebookRoot(d.profileNotebookRoot(profileID))
}

func homeNotebookRoot(configured string) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		return expandProfileNotebookRoot(configured)
	}
	return defaultNotebookRoot()
}

func defaultNotebookRoot() (string, error) {
	if root := config.HarnessNotebookRoot(); root != "" {
		return root, nil
	}
	foreign, err := config.DataDirIsForeign()
	if err != nil {
		return "", fmt.Errorf("compare the data folder with the instance's own: %w", err)
	}
	if foreign {
		return filepath.Join(config.DataDir(), "notebook"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return notebook.DefaultRoot(home, config.Instance()), nil
}

func expandProfileNotebookRoot(configured string) (string, error) {
	root, err := expandNotebookRoot(configured)
	if err != nil {
		return "", err
	}
	if err := config.RequireWithinHarness(setting.NotebookRoot.Key, root); err != nil {
		return "", err
	}
	return root, nil
}

func (d *Daemon) profileNotebookRoot(profileID string) string {
	if profileID == "" || !d.enrollmentStatus().IsHome() {
		return ""
	}
	return strings.TrimSpace(d.store.ProfileSetting(profileID, setting.NotebookRoot.Key))
}

func expandNotebookRoot(configured string) (string, error) {
	if strings.Contains(configured, "\n") {
		return "", fmt.Errorf("%q has a line break, which a Notebook folder path cannot hold", configured)
	}
	if strings.HasPrefix(configured, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, configured[2:]), nil
	}
	return filepath.Clean(configured), nil
}

func (d *Daemon) notebookRoots() map[string]struct{} {
	roots := map[string]struct{}{}
	if d.enrollmentStatus().IsOutpost() {
		d.notebookSync.mu.Lock()
		for _, entry := range d.notebookSync.disk.Folders {
			roots[entry.Root] = struct{}{}
		}
		d.notebookSync.mu.Unlock()
		return roots
	}
	notebooks, err := d.profileNotebooks(d.notebookRoot)
	if err != nil {
		d.logf("notebook roots: %v", err)
	}
	for _, nb := range notebooks {
		roots[nb.path] = struct{}{}
	}
	return roots
}

func (d *Daemon) ensureNotebookScaffold(profileID string) (root string, created bool, err error) {
	store, err := d.notebookStoreFor(profileID)
	if err != nil {
		return "", false, err
	}
	var scaffolded []notebook.SelfWrite
	var scaffoldErr error
	d.mutateRoot(store.Root(), func() []notebook.SelfWrite {
		scaffolded, scaffoldErr = store.EnsureScaffold()
		return scaffolded
	})
	if len(scaffolded) > 0 {
		createdPaths := make([]string, len(scaffolded))
		for i, write := range scaffolded {
			createdPaths[i] = write.Rel
		}
		d.broadcastFsChanged(store.Root(), originAgent, createdPaths...)
		d.ensureNotebookWatcher(store.Root())
	}
	if scaffoldErr != nil {
		return "", false, scaffoldErr
	}
	return store.Root(), len(scaffolded) > 0, nil
}

func (d *Daemon) sessionNotebookProfile(sessionID protocol.SessionID) (string, error) {
	if d.enrollmentStatus().IsOutpost() {
		if protocol.TrimID(sessionID) == "" {
			return "", errors.New("on an outpost the Notebook belongs to the calling session's profile; run this inside an attn session")
		}
		return d.store.SessionProfileID(protocol.TrimID(sessionID))
	}
	profile, err := d.callerProfile(sessionID)
	if err != nil {
		return "", err
	}
	return profile.ID, nil
}

func (d *Daemon) handleNotebookGuide(conn net.Conn, msg *protocol.NotebookGuideMessage) {
	sessionID := protocol.TrimID(protocol.Deref(msg.SessionID))
	profileID, err := d.sessionNotebookProfile(sessionID)
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	root, err := d.notebookRoot(profileID)
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	sessionIsChief := d.isLegacyChiefSession(sessionID)
	if sessionIsChief {
		if _, _, serr := d.ensureNotebookScaffold(profileID); serr != nil {
			d.logf("notebook guide: ensure scaffold failed: %v", serr)
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		NotebookGuide: &protocol.NotebookGuideResult{
			Guidance:       hooks.ChiefGuidance(root),
			Root:           root,
			SessionIsChief: sessionIsChief,
		},
	})
}

func (d *Daemon) sendNotebookBacklinksWSResult(client *wsClient, requestID, path string) {
	var entries []protocol.NotebookEntry
	store, err := d.notebookStoreFor(d.profileForClient(client))
	if err == nil {
		var list []notebook.Entry
		if list, err = store.Backlinks(path); err == nil {
			entries = notebookEntriesToProtocol(list)
		}
	}
	msg := protocol.NotebookBacklinksResultMessage{
		Event:     protocol.EventNotebookBacklinksResult,
		RequestID: requestID,
		Success:   err == nil,
		Entries:   entries,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func notebookEntriesToProtocol(entries []notebook.Entry) []protocol.NotebookEntry {
	out := make([]protocol.NotebookEntry, 0, len(entries))
	for _, e := range entries {
		pe := protocol.NotebookEntry{Path: e.Path, Size: int(e.Size)}
		if e.Type != "" {
			pe.Type = protocol.Ptr(e.Type)
		}
		if e.Title != "" {
			pe.Title = protocol.Ptr(e.Title)
		}
		if e.Summary != "" {
			pe.Summary = protocol.Ptr(e.Summary)
		}
		if e.Updated != "" {
			pe.Updated = protocol.Ptr(e.Updated)
		}
		out = append(out, pe)
	}
	return out
}
