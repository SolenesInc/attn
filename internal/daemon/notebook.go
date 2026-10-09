package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const originAgent = "agent"

const originExternal = "external"

const originUI = "ui"

func (d *Daemon) notebookStoreFor(profileID string) (*notebook.Store, error) {
	root, err := d.notebookRoot(profileID)
	if err != nil {
		return nil, err
	}
	return d.notebookStoreAt(root)
}

func (d *Daemon) notebookStoreAt(root string) (*notebook.Store, error) {
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

func (d *Daemon) notebookRoot(profileID string) (string, error) {
	if err := d.requireHome("the Notebook"); err != nil {
		return "", err
	}
	configured := strings.TrimSpace(d.profileSetting(profileID, settingNotebookRoot))
	if configured == "" {
		return "", fmt.Errorf("profile %q has no notebook.root", profileID)
	}
	root := configured
	if strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		root = filepath.Join(home, root[2:])
	}
	root = filepath.Clean(root)
	if config.HarnessNotebookRoot() != "" {
		harness, err := config.CanonicalRuntimePath(os.Getenv("ATTN_HARNESS_DATA_DIR"))
		if err != nil {
			return "", err
		}
		resolved, err := config.CanonicalRuntimePath(root)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(harness, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("refusing Notebook root %q outside harness root %q", root, harness)
		}
	}
	return root, nil
}

func (d *Daemon) broadcastNotebookChanged(origin string, paths ...string) {
	d.coalesceSnapshots(func() {
		for _, path := range paths {
			d.publishFact(FactNotebookFileChanged, path, notebookChangeOrigin{Origin: origin})
		}
	})
}

type notebookChangeOrigin struct {
	Origin string `json:"origin"`
}

func (d *Daemon) projectNotebookChanged(ev bus.Event) {
	change, ok := decodeFact[notebookChangeOrigin](d, ev)
	if !ok {
		return
	}
	d.notebookPendingMu.Lock()
	if d.notebookPendingPaths == nil {
		d.notebookPendingPaths = map[string][]string{}
	}
	d.notebookPendingPaths[change.Origin] = append(d.notebookPendingPaths[change.Origin], ev.Subject)
	d.notebookPendingMu.Unlock()

	d.projectSnapshot("notebook_changed:"+change.Origin, func() {
		d.notebookPendingMu.Lock()
		paths := d.notebookPendingPaths[change.Origin]
		delete(d.notebookPendingPaths, change.Origin)
		d.notebookPendingMu.Unlock()
		if len(paths) == 0 {
			return
		}
		d.broadcastMessage(protocol.NotebookChangedMessage{
			Event:  protocol.EventNotebookChanged,
			Paths:  paths,
			Origin: change.Origin,
		})
	})
}

func (d *Daemon) ensureNotebookScaffold(profileID string) (root string, created bool, err error) {
	store, err := d.notebookStoreFor(profileID)
	if err != nil {
		return "", false, err
	}
	createdPaths, scaffoldErr := store.EnsureScaffold()
	if len(createdPaths) > 0 {
		writes := make([]notebook.SelfWrite, len(createdPaths))
		for i, p := range createdPaths {
			writes[i] = notebook.SelfWrite{Rel: p}
		}
		d.noteSelfWrite(store.Root(), writes...)
		d.broadcastNotebookChanged(originAgent, createdPaths...)
		d.broadcastFsChanged(store.Root(), originAgent, createdPaths...)
		d.ensureNotebookWatcher(store.Root())
	}
	if scaffoldErr != nil {
		return "", false, scaffoldErr
	}
	return store.Root(), len(createdPaths) > 0, nil
}

func (d *Daemon) handleNotebookGuide(conn net.Conn, msg *protocol.NotebookGuideMessage) {
	sessionID := protocol.TrimID(protocol.Deref(msg.SessionID))
	profile, err := d.settingsProfile(sessionID, "", "")
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	root, err := d.notebookRoot(profile.ID)
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	sessionIsChief := d.isChiefOfStaffSession(sessionID)
	if sessionIsChief {
		if _, _, serr := d.ensureNotebookScaffold(profile.ID); serr != nil {
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

func (d *Daemon) sendNotebookListWSResult(client *wsClient, requestID, prefix string, scope notebookRequestScope) {
	var entries []protocol.NotebookEntry
	store, err := d.notebookStoreAt(string(scope.root))
	if err == nil {
		var list []notebook.Entry
		if list, err = store.List(prefix); err == nil {
			entries = notebookEntriesToProtocol(list)
		}
	}
	msg := protocol.NotebookListResultMessage{
		Event:     protocol.EventNotebookListResult,
		RequestID: requestID,
		Success:   err == nil,
		Entries:   entries,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendNotebookReadWSResult(client *wsClient, requestID, path string, scope notebookRequestScope) {
	var result *protocol.NotebookReadResult
	store, err := d.notebookStoreAt(string(scope.root))
	if err == nil {
		var content []byte
		var hash string
		if content, hash, err = store.Read(path); err == nil {
			result = &protocol.NotebookReadResult{Path: path, Content: string(content), Hash: hash}
		}
	}
	msg := protocol.NotebookReadResultMessage{
		Event:     protocol.EventNotebookReadResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendNotebookBacklinksWSResult(client *wsClient, requestID, path string, scope notebookRequestScope) {
	var entries []protocol.NotebookEntry
	store, err := d.notebookStoreAt(string(scope.root))
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

func (d *Daemon) sendNotebookWriteWSResult(client *wsClient, requestID, path, content, baseHash string, scope notebookRequestScope) {
	var result *protocol.NotebookWriteResult
	store, err := d.notebookStoreAt(string(scope.root))
	if err == nil {
		changed := path
		if rel, cerr := notebook.CleanPath(path); cerr == nil {
			changed = rel
		}
		var hash string
		var conflict *notebook.Conflict
		if hash, conflict, err = store.Write(path, []byte(content), baseHash); err == nil {
			result = &protocol.NotebookWriteResult{Path: changed}
			if conflict != nil {
				result.Conflict = true
				if conflict.CurrentHash != "" {
					result.CurrentHash = protocol.Ptr(conflict.CurrentHash)
				}
			} else {
				result.Hash = protocol.Ptr(hash)
				d.noteSelfWrite(store.Root(), notebook.SelfWrite{Rel: changed, Hash: hash})
				d.broadcastNotebookChanged(originUI, changed)
				d.broadcastFsChanged(store.Root(), originUI, changed)
			}
		}
	}
	msg := protocol.NotebookWriteResultMessage{
		Event:     protocol.EventNotebookWriteResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

const maxInboxSelection = 32 << 10

func chiefInboxNudgePrompt(root string) string {
	return prompts.RenderText("chief", "inbox", prompts.Values{"inbox_path": filepath.Join(root, "inbox.md")})
}

func (d *Daemon) sendNotebookToChiefWSResult(client *wsClient, requestID, sourcePath, selection string, scope notebookRequestScope) {
	var result *protocol.NotebookSendToChiefResult
	store, err := d.notebookStoreAt(string(scope.root))
	if err == nil {
		if strings.TrimSpace(selection) == "" {
			err = fmt.Errorf("notebook: empty selection")
		} else if len(selection) > maxInboxSelection {
			err = fmt.Errorf("notebook: selection exceeds %d bytes", maxInboxSelection)
		}
	}
	if err == nil {
		var relPath, hash string
		if relPath, hash, err = store.AppendInbox(formatChiefInboxEntry(sourcePath, selection)); err == nil {
			d.noteSelfWrite(store.Root(), notebook.SelfWrite{Rel: relPath, Hash: hash})
			d.broadcastNotebookChanged(originUI, relPath)
			d.broadcastFsChanged(store.Root(), originUI, relPath)
			result = &protocol.NotebookSendToChiefResult{
				Path:   relPath,
				Nudged: d.nudgeChiefOfStaff(scope.profileID, requestID, chiefInboxNudgePrompt(store.Root())),
			}
		}
	}
	msg := protocol.NotebookSendToChiefResultMessage{
		Event:     protocol.EventNotebookSendToChiefResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func formatChiefInboxEntry(sourcePath, selection string) string {
	var b strings.Builder
	b.WriteString(chiefInboxSourceHeading(sourcePath))
	b.WriteString("\n\n")
	normalized := strings.ReplaceAll(selection, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	for _, line := range strings.Split(strings.TrimRight(normalized, "\n"), "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func chiefInboxSourceHeading(sourcePath string) string {
	rel, err := notebook.CleanPath(sourcePath)
	if err != nil {
		return "## From the Notebook"
	}
	rel = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '`' {
			return -1
		}
		return r
	}, rel)
	if rel == "" {
		return "## From the Notebook"
	}
	if !strings.ContainsAny(rel, " \t()[]<>") {
		return fmt.Sprintf("## From [/%s](/%s)", rel, rel)
	}
	return fmt.Sprintf("## From `/%s`", rel)
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
