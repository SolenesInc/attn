package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func ledgerQuery(msg *protocol.SessionListMessage, wantFacets bool) (store.SessionLedgerQuery, error) {
	scope := store.SessionLedgerLive
	switch {
	case protocol.Deref(msg.All):
		scope = store.SessionLedgerAll
	case protocol.Deref(msg.Closed):
		scope = store.SessionLedgerClosed
	}

	before := strings.TrimSpace(protocol.Deref(msg.Before))
	query := store.SessionLedgerQuery{
		Scope:       scope,
		Limit:       protocol.Deref(msg.Limit),
		Before:      before,
		WorkspaceID: strings.TrimSpace(protocol.Deref(msg.WorkspaceID)),
		Repository:  strings.TrimSpace(protocol.Deref(msg.Repository)),
		Facets:      wantFacets && before == "",
	}

	since, err := ledgerInstantArg("since", protocol.Deref(msg.Since))
	if err != nil {
		return store.SessionLedgerQuery{}, err
	}
	until, err := ledgerInstantArg("until", protocol.Deref(msg.Until))
	if err != nil {
		return store.SessionLedgerQuery{}, err
	}
	if !since.IsZero() && !until.IsZero() && !until.After(since) {
		return store.SessionLedgerQuery{}, fmt.Errorf("since %s is not before until %s; the window is half-open and would hold nothing",
			since.Format(time.RFC3339), until.Format(time.RFC3339))
	}
	query.Since, query.Until = since, until
	return query, nil
}

func ledgerInstantArg(name, raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not an RFC3339 instant like 2026-09-05T00:00:00Z", name, raw)
	}
	return at, nil
}

func sessionListStream(msg *protocol.SessionListMessage) (bool, error) {
	delivery := protocol.SessionReopenDeliveryInline
	if msg.ReopenDelivery != nil {
		delivery = *msg.ReopenDelivery
	}
	switch delivery {
	case protocol.SessionReopenDeliveryInline:
		return false, nil
	case protocol.SessionReopenDeliveryStream:
		if !protocol.Deref(msg.Reopen) {
			return false, errors.New("stream reopen delivery requires reopen=true")
		}
		return true, nil
	default:
		return false, fmt.Errorf("unknown reopen delivery %q", delivery)
	}
}

func (d *Daemon) sessionLedgerStoredPage(msg *protocol.SessionListMessage, wantFacets bool) (*protocol.SessionListResult, error) {
	query, err := ledgerQuery(msg, wantFacets)
	if err != nil {
		return nil, err
	}

	page, err := d.store.SessionLedger(query)
	if err != nil {
		var unknownCursor *store.ErrUnknownLedgerCursor
		var tooLarge *store.ErrLedgerLimitTooLarge
		if errors.As(err, &unknownCursor) || errors.As(err, &tooLarge) {
			return nil, err
		}
		d.logf("session ledger read failed: %v", err)
		return nil, errors.New("session ledger unavailable")
	}

	result := &protocol.SessionListResult{Entries: page.Entries, Omitted: page.Omitted, Facets: page.Facets}
	if result.Entries == nil {
		result.Entries = []protocol.SessionLedgerEntry{}
	}
	if page.NextBefore != "" {
		result.NextBefore = protocol.Ptr(page.NextBefore)
	}
	return result, nil
}

func (d *Daemon) sessionLedgerPage(msg *protocol.SessionListMessage, wantFacets bool) (*protocol.SessionListResult, error) {
	stream, err := sessionListStream(msg)
	if err != nil {
		return nil, err
	}
	if stream {
		return nil, errors.New("stream reopen delivery is available only over WebSocket")
	}
	result, err := d.sessionLedgerStoredPage(msg, wantFacets)
	if err != nil {
		return nil, err
	}
	if protocol.Deref(msg.Reopen) {
		result.Reopen, err = d.reopenVerdictsForPage(context.Background(), result.Entries)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (d *Daemon) handleSessionList(conn net.Conn, msg *protocol.SessionListMessage) {
	result, err := d.sessionLedgerPage(msg, false)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SessionListResult: result})
}

func (d *Daemon) reopenVerdictsForPage(
	ctx context.Context,
	entries []protocol.SessionLedgerEntry,
) ([]protocol.SessionReopenEntry, error) {
	closed := make([]int, 0, len(entries))
	for i := range entries {
		if protocol.Deref(entries[i].ClosedAt) != "" {
			closed = append(closed, i)
		}
	}
	verdicts := make([]protocol.SessionReopenEntry, len(closed))
	resolver := sessionReopenResolver{daemon: d}
	gitView := d.scheduledReopenGit(gitInteractive)
	resolveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	workers := make(chan struct{}, productionSessionReopenWorkers)
	for resultIndex, entryIndex := range closed {
		resultIndex, entry := resultIndex, entries[entryIndex]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case workers <- struct{}{}:
				defer func() { <-workers }()
			case <-resolveCtx.Done():
				return
			}
			verdict, err := resolver.ResolveEntry(resolveCtx, entry, gitView)
			if err != nil {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("resolve reopen eligibility for session %s: %w", entry.ID, err)
					cancel()
				})
				return
			}
			verdicts[resultIndex] = protocol.SessionReopenEntry{
				SessionID: entry.ID,
				Reopen:    *verdict.toProtocol(),
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return verdicts, nil
}

func (d *Daemon) handleSessionShow(conn net.Conn, msg *protocol.SessionShowMessage) {
	entry := d.store.SessionLedgerEntry(strings.TrimSpace(msg.SessionID))
	if entry == nil {
		d.sendError(conn, "session_not_found")
		return
	}
	result := &protocol.SessionShowResult{Entry: *entry}
	verdict, err := (sessionReopenResolver{daemon: d}).ResolveEntry(
		context.Background(), *entry, d.scheduledReopenGit(gitInteractive),
	)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("resolve reopen eligibility for session %s: %v", entry.ID, err))
		return
	}
	result.Reopen = verdict.toProtocol()
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SessionShowResult: result})
}

func (d *Daemon) sendSessionListWSResult(
	client *wsClient,
	msg *protocol.SessionListMessage,
	intent *reopenPageIntent,
) {
	stream, err := sessionListStream(msg)
	var result *protocol.SessionListResult
	if err == nil {
		if stream {
			result, err = d.sessionLedgerStoredPage(msg, true)
		} else {
			result, err = d.sessionLedgerPage(msg, true)
		}
	}
	reply := protocol.SessionListResultMessage{
		Event:     protocol.EventSessionListResult,
		RequestID: protocol.Deref(msg.RequestID),
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	}
	if !d.sendToClient(client, reply) || err != nil || !stream || intent == nil {
		return
	}
	if broker := d.sessionReopenBroker(); broker != nil {
		broker.CommitPage(*intent, reopenKeysForEntries(result.Entries))
	}
}

func reopenKeysForEntries(entries []protocol.SessionLedgerEntry) []reopenKey {
	keys := make([]reopenKey, 0, len(entries))
	for _, entry := range entries {
		closedAt := strings.TrimSpace(protocol.Deref(entry.ClosedAt))
		if closedAt != "" {
			keys = append(keys, reopenKey{SessionID: entry.ID, ClosedAt: closedAt})
		}
	}
	return keys
}

func (d *Daemon) sendSessionReopenWSResult(client *wsClient, msg *protocol.SessionReopenMessage) {
	action := protocol.SessionReopenAction("")
	if msg.Action != nil {
		action = *msg.Action
	}
	reply := protocol.SessionReopenResultMessage{
		Event:     protocol.EventSessionReopenResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	outcome, err := d.reopenSession(msg.SessionID, action, protocol.Deref(msg.Directory))
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	} else {
		reply.Success = true
		reply.Result = sessionReopenResult(outcome)
	}
	d.sendToClient(client, reply)
}

func (d *Daemon) sendSessionShowWSResult(client *wsClient, msg *protocol.SessionShowMessage) {
	reply := protocol.SessionShowResultMessage{
		Event:     protocol.EventSessionShowResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	if entry := d.store.SessionLedgerEntry(strings.TrimSpace(msg.SessionID)); entry != nil {
		reply.Success = true
		reply.Entry = entry
	} else {
		reply.Error = protocol.Ptr(fmt.Sprintf("this daemon never ran session %s", strings.TrimSpace(msg.SessionID)))
	}
	d.sendToClient(client, reply)
}

func projectSessionReopenRefreshed(d *Daemon, event bus.Event) {
	reopen, ok := decodeFact[protocol.SessionReopen](d, event)
	if !ok {
		return
	}
	d.wsHub.BroadcastValue(&protocol.SessionReopenRefreshedMessage{
		Event:     protocol.EventSessionReopenRefreshed,
		SessionID: event.Subject,
		Reopen:    reopen,
	})
}

func projectSessionClosed(d *Daemon, event bus.Event) {
	entry, ok := decodeFact[protocol.SessionLedgerEntry](d, event)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:              protocol.EventSessionClosed,
		SessionLedgerEntry: &entry,
	})
}
