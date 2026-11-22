package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

type linkedConversations struct{ d *Daemon }

func (c linkedConversations) watching(ref harness.ConversationRef) *appserver.Link {
	if l := c.d.appServer.Link(string(ref.Harness)); l.WatchesConversations() {
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
	return l.TranscriptPage(ctx, ref.ID, cursor, limit)
}

func (c linkedConversations) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return fileConversations(c).LastReply(ctx, ref, max)
	case ref.ID == "":
		return "", nil
	}
	message, err := l.LastReply(ctx, ref.ID)
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
	return l.AssistantWindow(ctx, ref.ID)
}

func (c linkedConversations) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	l := c.watching(ref)
	switch {
	case l == nil:
		return fileConversations(c).Activity(ctx, ref, kind, cursor)
	case ref.ID == "":
		return protocol.TranscriptActivityResult{}, nil
	}
	return l.Activity(ctx, ref.ID, kind, cursor)
}

func (c linkedConversations) Version(ref harness.ConversationRef) string {
	return c.d.appServer.Link(string(ref.Harness)).Version()
}
