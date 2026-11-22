package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/activity"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/statemarker"
	"github.com/victorarias/attn/internal/transcript"
)

type linkedConversations struct{ d *Daemon }

func (c linkedConversations) watching(ref harness.ConversationRef) *serverLink {
	if l := c.d.serverLinkFor(string(ref.Harness)); l.watchesConversations() {
		return l
	}
	return nil
}

func (c linkedConversations) Page(ctx context.Context, ref harness.ConversationRef, cursor string, limit int) (transcript.EventPage, error) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return fileConversations(c).Page(ctx, ref, cursor, limit)
	case ref.ID == "":
		return transcript.EventPage{}, errTranscriptUnavailable
	}
	return l.transcriptPage(ctx, ref.ID, cursor, limit)
}

func (c linkedConversations) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return fileConversations(c).LastReply(ctx, ref, max)
	case ref.ID == "":
		return "", nil
	}
	message, err := l.lastReply(ctx, ref.ID)
	if runes := []rune(message); len(runes) > max {
		message = string(runes[len(runes)-max:])
	}
	return message, err
}

func (c linkedConversations) Window(ctx context.Context, ref harness.ConversationRef) (harness.AssistantWindow, bool) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return fileConversations(c).Window(ctx, ref)
	case ref.ID == "":
		return harness.AssistantWindow{}, false
	}
	return l.assistantWindow(ctx, ref.ID)
}

func (c linkedConversations) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return fileConversations(c).Activity(ctx, ref, kind, cursor)
	case ref.ID == "":
		return protocol.TranscriptActivityResult{}, nil
	}
	return l.activity(ctx, ref.ID, kind, cursor)
}

func (l *serverLink) entries(ctx context.Context, params map[string]any, each func(transcript.ServerEntry) bool) error {
	return l.entriesFrom(ctx, true, params, each)
}

func (l *serverLink) entriesFrom(ctx context.Context, start bool, params map[string]any, each func(transcript.ServerEntry) bool) error {
	p, err := l.connection()
	if err != nil {
		return err
	}
	c, release, err := p.borrow(ctx, l, start)
	if err != nil {
		return err
	}
	defer release()
	return pageEntries(ctx, c, params, each)
}

const serverEntriesPerCall = 8

func pageEntries(ctx context.Context, c *effectrpc.Client, params map[string]any, each func(transcript.ServerEntry) bool) error {
	if limit, ok := params["limit"].(int); !ok || limit > serverEntriesPerCall {
		params["limit"] = serverEntriesPerCall
	}
	for {
		var page struct {
			Entries []transcript.ServerEntry `json:"entries"`
			Next    json.RawMessage          `json:"next"`
		}
		callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
		err := c.Call(callCtx, "conversation/entries", params, &page)
		cancel()
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			if !each(entry) {
				return nil
			}
		}
		if len(page.Next) == 0 || string(page.Next) == "null" {
			return nil
		}
		params["cursor"] = page.Next
	}
}

func (l *serverLink) transcriptPage(ctx context.Context, conversation, cursor string, limit int) (transcript.EventPage, error) {
	return l.transcriptPageFrom(ctx, true, conversation, cursor, limit)
}

func (l *serverLink) transcriptPageFrom(ctx context.Context, start bool, conversation, cursor string, limit int) (transcript.EventPage, error) {
	from, skip, err := transcript.ParseServerCursor(cursor)
	if err != nil {
		return transcript.EventPage{}, err
	}
	page := transcript.EventPage{Events: []transcript.Event{}, NextCursor: strings.TrimSpace(cursor), AtEnd: true}
	params := map[string]any{"conversation": conversation, "order": "ascending", "limit": limit}
	if from > 0 {
		params["from"] = from
	}
	var invalid error
	err = l.entriesFrom(ctx, start, params, func(entry transcript.ServerEntry) bool {
		events := transcript.ServerEntryEvents(entry)
		if entry.ID == from {
			if skip > len(events) {
				invalid = transcript.ErrInvalidCursor
				return false
			}
			events = events[skip:]
		}
		if remaining := limit - len(page.Events); len(events) > remaining {
			page.Events = append(page.Events, events[:remaining]...)
			page.NextCursor, page.AtEnd = page.Events[len(page.Events)-1].Cursor, false
			return false
		}
		page.Events = append(page.Events, events...)
		page.NextCursor = transcript.ServerCursor(entry.ID, len(transcript.ServerEntryEvents(entry)))
		return true
	})
	if invalid != nil {
		return transcript.EventPage{}, invalid
	}
	return page, err
}

func (l *serverLink) lastReply(ctx context.Context, conversation string) (string, error) {
	reply := ""
	err := l.entries(ctx, map[string]any{"conversation": conversation, "order": "descending", "limit": sessionTranscriptPageSize}, func(entry transcript.ServerEntry) bool {
		if entry.Kind != "pi.assistant" {
			return true
		}
		reply = statemarker.Strip(entry.Text())
		return strings.TrimSpace(reply) == ""
	})
	return reply, err
}

func (l *serverLink) newestEntry(ctx context.Context, conversation string) (transcript.ServerEntry, bool, error) {
	var newest transcript.ServerEntry
	found := false
	err := l.entriesFrom(ctx, false, map[string]any{"conversation": conversation, "order": "descending", "limit": 1}, func(entry transcript.ServerEntry) bool {
		newest, found = entry, true
		return false
	})
	return newest, found, err
}

func (l *serverLink) lastWritten(ctx context.Context, conversation string) (time.Time, bool, error) {
	var written time.Time
	found := false
	err := l.entriesFrom(ctx, false, map[string]any{"conversation": conversation, "order": "descending", "limit": serverEntriesPerCall}, func(entry transcript.ServerEntry) bool {
		found = true
		at, dated := entry.Written()
		written = at
		return !dated
	})
	return written, found, err
}

func (l *serverLink) activity(ctx context.Context, conversation, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	var result protocol.TranscriptActivityResult
	switch kind {
	case "head":
		newest, found, err := l.newestEntry(ctx, conversation)
		if err != nil || !found {
			return result, nil
		}
		result.Available = true
		result.Head = protocol.Ptr(transcript.ServerCursor(newest.ID, len(transcript.ServerEntryEvents(newest))))
	case "modified":
		written, found, err := l.lastWritten(ctx, conversation)
		if err != nil || !found {
			return result, nil
		}
		result.Available = true
		result.ModifiedAt = protocol.Ptr(written.Format(time.RFC3339Nano))
	case "events":
		result.Available = true
		window, err := activity.ReadPages(cursor, func(cursor string, limit int) (transcript.EventPage, error) {
			return l.transcriptPageFrom(ctx, false, conversation, cursor, limit)
		})
		if err != nil {
			kind := transcript.ReadErrorKind(err)
			if errors.Is(err, activity.ErrDeltaTooLarge) {
				kind = "delta_too_large"
			}
			result.ErrorKind, result.Error = protocol.Ptr(protocol.MonitorReadErrorKind(kind)), protocol.Ptr(err.Error())
			return result, nil
		}
		encoded, err := json.Marshal(window)
		if err != nil {
			return result, err
		}
		result.Window = protocol.Ptr(string(encoded))
	default:
		return result, fmt.Errorf("unknown transcript activity read %q", kind)
	}
	return result, nil
}

func (l *serverLink) assistantWindow(ctx context.Context, conversation string) (harness.AssistantWindow, bool) {
	var newestFirst []transcript.ServerEntry
	read := 0
	more := false
	err := l.entriesFrom(ctx, false, map[string]any{"conversation": conversation, "order": "descending", "limit": sessionTranscriptPageSize}, func(entry transcript.ServerEntry) bool {
		if read == sessionTranscriptPageSize {
			more = true
			return false
		}
		read++
		newestFirst = append(newestFirst, entry)
		return true
	})
	if err != nil {
		return sessionmonitor.AssistantWindow{}, false
	}
	window := sessionmonitor.NewAnnotatableWindow()
	if more {
		window.MarkPrefixOmitted()
	}
	for i := len(newestFirst) - 1; i >= 0; i-- {
		window.Apply(transcript.ServerEntryEvents(newestFirst[i]))
	}
	messages, report := window.Snapshot()
	return sessionmonitor.AssistantWindow{Status: protocol.SessionMessageWindowStatusReady, Messages: messages, Report: report}, true
}

func (l *serverLink) conversationsResumable(refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	known, running, err := l.conversationIDs(l.d.life.Context())
	availability := make(map[harness.ConversationRef]harness.Availability, len(refs))
	for _, ref := range refs {
		switch {
		case !running:
			availability[ref] = harness.Availability{Resumable: true}
		case err != nil:
			availability[ref] = harness.Availability{CheckFailed: true, Reason: fmt.Sprintf("cannot check conversation %s on %s's server: %v", ref.ID, ref.Harness, err)}
		case !known[ref.ID] && l.d.store.ServerConversationSeen(ref.ID):
			availability[ref] = harness.Availability{Reason: fmt.Sprintf("conversation %s is no longer on %s's server", ref.ID, ref.Harness)}
		default:
			availability[ref] = harness.Availability{Resumable: true}
		}
	}
	return availability
}

func (l *serverLink) conversationIDs(ctx context.Context) (known map[string]bool, running bool, err error) {
	p, err := l.connection()
	if err != nil {
		return nil, false, err
	}
	c, release, err := p.borrow(ctx, l, false)
	if err != nil {
		return nil, false, err
	}
	defer release()
	var conversations []struct {
		ID string `json:"id"`
	}
	callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
	defer cancel()
	if err := c.Call(callCtx, "conversation/list", nil, &conversations); err != nil {
		return nil, true, err
	}
	known = make(map[string]bool, len(conversations))
	for _, conversation := range conversations {
		known[conversation.ID] = true
	}
	return known, true, nil
}

func (c linkedConversations) Version(ref harness.ConversationRef) string {
	return c.d.serverLinkFor(string(ref.Harness)).serverVersion()
}
