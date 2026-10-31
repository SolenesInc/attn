package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) resolveKeptConversation(id string) (conversationKey, string, error) {
	scope := ""
	for _, prefix := range []string{"session:", "conversation:"} {
		if strings.HasPrefix(id, prefix) {
			scope, id = strings.TrimSuffix(prefix, ":"), strings.TrimPrefix(id, prefix)
			break
		}
	}
	entries, err := d.store.ConversationSessions(id)
	if err != nil {
		return conversationKey{}, "", err
	}
	matches := make(map[conversationKey]string)
	for _, entry := range entries {
		resumeID := entry.ResumeID
		if (scope != "conversation" && entry.ID == id) || (scope != "session" && resumeID == id) {
			if resumeID == "" {
				return conversationKey{}, "", fmt.Errorf("session %s has no conversation yet", entry.ID)
			}
			key := conversationKey{entry.Agent, resumeID}
			if _, exists := matches[key]; !exists {
				matches[key] = entry.ID
			}
		}
	}
	if scope != "session" {
		pins, err := d.store.ConversationPins()
		if err != nil {
			return conversationKey{}, "", err
		}
		for _, pin := range pins {
			key := conversationKey{pin.Agent, pin.ResumeID}
			if pin.ResumeID == id {
				if _, exists := matches[key]; !exists {
					matches[key] = string(pin.SessionID)
				}
			}
		}
		copies, err := d.store.KeptConversations()
		if err != nil {
			return conversationKey{}, "", err
		}
		for _, kept := range copies {
			key := conversationKey{kept.Agent, kept.ResumeID}
			if kept.ResumeID == id {
				if _, exists := matches[key]; !exists {
					matches[key] = ""
				}
			}
		}
	}
	if len(matches) == 0 {
		return conversationKey{}, "", fmt.Errorf("no session or conversation %q", id)
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for key, session := range matches {
			names = append(names, fmt.Sprintf("%s conversation %s (session %s)", key.agent, key.resumeID, session))
		}
		sort.Strings(names)
		return conversationKey{}, "", fmt.Errorf("ambiguous identifier %q: %s; use session:<id> or conversation:<id> to choose", id, strings.Join(names, ", "))
	}
	for key, session := range matches {
		if _, keeper := agentdriver.Get(key.agent).(agentdriver.ConversationKeeper); !keeper {
			return conversationKey{}, "", fmt.Errorf("%s keeps its own conversations; attn has nothing to keep", key.agent)
		}
		return key, session, nil
	}
	panic("unreachable")
}

func (d *Daemon) keptConversationList(includeDeleted bool) (*protocol.KeptConversationListResult, error) {
	d.conversationKeepMu.Lock()
	defer d.conversationKeepMu.Unlock()
	copies, err := d.store.KeptConversations()
	if err != nil {
		return nil, err
	}
	pins, err := d.store.ConversationPins()
	if err != nil {
		return nil, err
	}
	seeds, err := d.conversationSeedReferences()
	if err != nil {
		return nil, err
	}
	letters, _, err := d.crewLetterReferences()
	if err != nil {
		return nil, err
	}
	entries, err := d.store.ConversationSessions("")
	if err != nil {
		return nil, err
	}
	byKey := make(map[conversationKey][]store.ConversationSession)
	for _, entry := range entries {
		key := conversationKey{entry.Agent, entry.ResumeID}
		byKey[key] = append(byKey[key], entry)
	}

	result := &protocol.KeptConversationListResult{Rows: []protocol.KeptConversationRow{}}
	rowsByKey := make(map[conversationKey]protocol.KeptConversationRow)
	for _, copy := range copies {
		if !includeDeleted && !copy.DeletedAt.IsZero() {
			continue
		}
		key := conversationKey{copy.Agent, copy.ResumeID}
		rowsByKey[key] = protocol.KeptConversationRow{Agent: copy.Agent, ResumeID: copy.ResumeID, SourceBytes: protocol.Ptr(int(copy.Bytes)), Kept: protocolKeptConversation(copy, pins)}
	}
	live := make(map[conversationKey]bool)
	for _, session := range d.store.List("") {
		live[conversationKey{session.Agent, d.store.GetResumeSessionID(session.ID)}] = true
	}
	for _, pin := range pins {
		key := conversationKey{pin.Agent, pin.ResumeID}
		row := rowsByKey[key]
		row.Agent, row.ResumeID = key.agent, key.resumeID
		row.PinnedAt = protocol.Ptr(pin.PinnedAt.UTC().Format(time.RFC3339Nano))
		rowsByKey[key] = row
	}
	references := make(map[conversationKey]bool)
	for key := range seeds {
		references[key] = true
	}
	for key := range letters {
		references[key] = true
	}
	for key := range references {
		if _, keeper := agentdriver.Get(key.agent).(agentdriver.ConversationKeeper); !keeper {
			continue
		}
		row := rowsByKey[key]
		row.Agent, row.ResumeID = key.agent, key.resumeID
		rowsByKey[key] = row
		if len(byKey[key]) == 0 {
			entries, err := d.store.ConversationSessions(key.resumeID)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry.Agent == key.agent && entry.ResumeID == key.resumeID {
					byKey[key] = append(byKey[key], entry)
				}
			}
		}
	}
	for key, row := range rowsByKey {
		if row.Kept == nil || row.Kept.DeletedAt != nil {
			if row.PinnedAt == nil && !references[key] {
				continue
			}
			row.Kept, row.SourceBytes = nil, nil
			names := make([]string, 0, len(seeds[key])+len(letters[key]))
			for _, seed := range seeds[key] {
				names = append(names, "open seed "+seed.Slug)
			}
			for _, letter := range letters[key] {
				names = append(names, "crew letter "+letter.Member+" "+letter.Filename)
			}
			sort.Strings(names)
			if row.PinnedAt != nil {
				names = append(names, "pinned")
			}
			reason := strings.Join(names, "; ") + "; copying on the next pass"
			if live[key] {
				reason = strings.TrimSuffix(reason, "copying on the next pass")
				if conversationKeepQuiet() == 24*time.Hour {
					reason += "copy once quiet for a day"
				} else {
					reason += "copy once quiet for " + conversationKeepQuiet().String()
				}
			}
			row.PendingReason = protocol.Ptr(reason)
		}
		rowsByKey[key] = row
	}
	for key, row := range rowsByKey {
		row.Title, row.SessionIds, row.Seeds = key.resumeID, []protocol.SessionID{}, seeds[key]
		row.CrewLetters = letters[key]
		if row.Seeds == nil {
			row.Seeds = []protocol.KeptConversationSeed{}
		}
		sort.Slice(row.Seeds, func(i, j int) bool { return row.Seeds[i].ID < row.Seeds[j].ID })
		for i, entry := range byKey[key] {
			row.SessionIds = append(row.SessionIds, protocol.SessionID(entry.ID))
			if i == 0 && entry.Label != "" {
				row.Title = entry.Label
			}
		}
		if row.Kept == nil {
			result.PendingCount++
		} else if row.Kept.DeletedAt == nil {
			result.Count++
			result.StoredBytes += row.Kept.Bytes
			if date := protocol.Deref(row.Kept.DeleteAfter); date != "" && (result.NextDeleteAfter == nil || date < *result.NextDeleteAfter) {
				result.NextDeleteAfter = protocol.Ptr(date)
			}
		}
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Rows, func(i, j int) bool {
		a, b := result.Rows[i], result.Rows[j]
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		if a.Agent != b.Agent {
			return a.Agent < b.Agent
		}
		return a.ResumeID < b.ResumeID
	})
	return result, nil
}

func (d *Daemon) keepConversation(id string, keep bool) error {
	d.conversationKeepMu.Lock()
	defer d.conversationKeepMu.Unlock()
	key, session, err := d.resolveKeptConversation(id)
	if err != nil {
		return err
	}
	if host := d.sessionHost(protocol.SessionID(session)); keep && host != "" {
		return fmt.Errorf("outpost limit: conversations on %s are not kept", d.outpostName(host))
	}
	if keep {
		if !d.conversationKnown(agentdriver.Get(key.agent), key.resumeID) {
			return fmt.Errorf("nothing left to keep: %s no longer has conversation %s and attn has no copy", key.agent, key.resumeID)
		}
		err = d.store.PinConversation(key.agent, key.resumeID, protocol.SessionID(session), time.Now())
	} else {
		err = d.store.UnpinConversation(key.agent, key.resumeID)
	}
	if err != nil {
		return err
	}
	d.publishFact(factConversationKeptChanged, key.resumeID, nil)
	d.queueConversationKeep()
	return nil
}

func (d *Daemon) forgetConversation(id string, unlinkCrewLetters bool) (*protocol.KeptConversationForgetResult, error) {
	d.conversationKeepMu.Lock()
	defer d.conversationKeepMu.Unlock()
	key, _, err := d.resolveKeptConversation(id)
	if err != nil {
		return nil, err
	}
	result := &protocol.KeptConversationForgetResult{UnlinkedCrewLetters: []protocol.KeptConversationCrewLetter{}}
	forgotten, unlinking := false, false
	defer func() {
		if forgotten || len(result.UnlinkedCrewLetters) > 0 {
			d.publishFact(factConversationKeptChanged, key.resumeID, nil)
			d.queueConversationKeep()
		}
	}()
	d.gardenWatchMu.Lock()
	defer d.gardenWatchMu.Unlock()
	err = d.worktreeMaintenance.TryAutomaticRemoval(context.Background(), func(automaticWorktreeCleanupProtection) error {
		references, err := d.conversationSeedReferences()
		if err != nil {
			return err
		}
		if seeds := references[key]; len(seeds) > 0 {
			names := make([]string, 0, len(seeds))
			for _, seed := range seeds {
				names = append(names, fmt.Sprintf("%s (%s)", seed.Slug, seed.ID))
			}
			sort.Strings(names)
			return fmt.Errorf("conversation %s is kept by open seeds: %s; harvest or wither them before forgetting", key.resumeID, strings.Join(names, ", "))
		}
		letters, unreadable, err := d.crewLetterReferences()
		if err != nil {
			return err
		}
		if unreadable != nil {
			return fmt.Errorf("conversation %s may be kept by crew letters that cannot be read; restore them and retry: %w", key.resumeID, unreadable)
		}
		if len(letters[key]) > 0 && !unlinkCrewLetters {
			names := make([]string, 0, len(letters[key]))
			for _, letter := range letters[key] {
				names = append(names, letter.Member+" "+letter.Filename)
			}
			sort.Strings(names)
			return fmt.Errorf("conversation %s is kept by crew letters: %s; to leave the letters and delete their transcript, rerun with --unlink-crew-letters", key.resumeID, strings.Join(names, ", "))
		}
		kept, exists := d.store.KeptConversation(key.agent, key.resumeID)
		if !exists || !kept.DeletedAt.IsZero() {
			return fmt.Errorf("attn has no live copy of conversation %s to forget", key.resumeID)
		}
		sort.Slice(letters[key], func(i, j int) bool {
			a, b := letters[key][i], letters[key][j]
			if a.Member != b.Member {
				return a.Member < b.Member
			}
			return a.Filename < b.Filename
		})
		unlinking = len(letters[key]) > 0
		for _, letter := range letters[key] {
			path := filepath.Join(d.dataRoot, crew.HomesDirName, letter.Member, crew.HandoffsDirName, letter.Filename+".json")
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing crew letter session record %s: %w", path, err)
			}
			result.UnlinkedCrewLetters = append(result.UnlinkedCrewLetters, letter)
		}
		// Commit the deletion before unlinking; the keep job removes leftovers after a crash.
		if err := d.store.ForgetKeptConversation(key.agent, key.resumeID, time.Now()); err != nil {
			return err
		}
		forgotten = true
		if err := os.Remove(conversationArchive(key.agent, key.resumeID)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("conversation %s is forgotten; removing its archive failed (the keep job will retry): %w", key.resumeID, err)
		}
		return nil
	})
	if errors.Is(err, errAutomaticWorktreeCleanupPreempted) {
		return nil, fmt.Errorf("garden reference changes are in progress; retry forgetting conversation %s", key.resumeID)
	}
	if err != nil && unlinking {
		names := []string{}
		for _, letter := range result.UnlinkedCrewLetters {
			names = append(names, letter.Member+" "+letter.Filename)
		}
		removed := "none"
		if len(names) > 0 {
			removed = strings.Join(names, ", ")
		}
		if forgotten {
			err = fmt.Errorf("%w; unlinked crew letters: %s; letters are unchanged", err, removed)
		} else {
			err = fmt.Errorf("%w; unlinked crew letters: %s; letters are unchanged and attn's copy remains; fix the error and retry `attn conversation forget conversation:%s --unlink-crew-letters`", err, removed, key.resumeID)
		}
	}
	return result, err
}

func (d *Daemon) handleKeptConversationList(conn net.Conn, msg *protocol.KeptConversationListMessage) {
	result, err := d.keptConversationList(protocol.Deref(msg.IncludeDeleted))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, KeptConversationListResult: result})
}
func (d *Daemon) handleKeptConversationKeep(conn net.Conn, msg *protocol.KeptConversationKeepMessage) {
	if err := d.keepConversation(msg.SessionID, msg.Keep); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true})
}
func (d *Daemon) handleKeptConversationForget(conn net.Conn, msg *protocol.KeptConversationForgetMessage) {
	result, err := d.forgetConversation(msg.SessionID, protocol.Deref(msg.UnlinkCrewLetters))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, KeptConversationForgetResult: result})
}
func (d *Daemon) handleKeptConversationListWS(client *wsClient, msg *protocol.KeptConversationListMessage) {
	result, err := d.keptConversationList(protocol.Deref(msg.IncludeDeleted))
	event := &protocol.WebSocketEvent{Event: protocol.EventKeptConversationListResult, RequestID: msg.RequestID, Success: protocol.Ptr(err == nil), KeptConversationListResult: result}
	if err != nil {
		event.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, event)
}
func (d *Daemon) handleKeptConversationKeepWS(client *wsClient, msg *protocol.KeptConversationKeepMessage) {
	err := d.keepConversation(msg.SessionID, msg.Keep)
	event := &protocol.WebSocketEvent{Event: protocol.EventKeptConversationKeepResult, RequestID: msg.RequestID, Success: protocol.Ptr(err == nil)}
	if err != nil {
		event.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, event)
}
func (d *Daemon) handleKeptConversationForgetWS(client *wsClient, msg *protocol.KeptConversationForgetMessage) {
	result, err := d.forgetConversation(msg.SessionID, protocol.Deref(msg.UnlinkCrewLetters))
	event := &protocol.WebSocketEvent{Event: protocol.EventKeptConversationForgetResult, RequestID: msg.RequestID, Success: protocol.Ptr(err == nil), KeptConversationForgetResult: result}
	if err != nil {
		event.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, event)
}
