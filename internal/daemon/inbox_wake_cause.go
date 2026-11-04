package daemon

import (
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

// Tripwire: of 651 production inbox attempts from 2026-08-11 to 2026-11-04, 649 covered at most 6 items.
const wakeCauseListLimit = 8

func (d *Daemon) inboxWakeCause(a who.Address) (string, error) {
	deliveries, err := d.store.UnreadInboxDeliveries(a)
	if err != nil {
		return "", err
	}
	var lines []string
	unlisted := 0
	for _, delivery := range deliveries {
		if delivery.Item.Kind.Typed() {
			return "", nil
		}
		if delivery.Item.Attempts >= inbox.MaxAttempts {
			continue
		}
		line := d.wakeCauseLine(delivery)
		if line == "" || len(lines) == wakeCauseListLimit {
			unlisted++
			continue
		}
		lines = append(lines, "- "+line)
	}
	if len(lines) == 0 {
		return "", nil
	}
	if unlisted > 0 {
		lines = append(lines, "- "+prompts.RenderText("session", "wake-cause-more", prompts.Values{"count": strconv.Itoa(unlisted)}))
	}
	return strings.Join(lines, "\n"), nil
}

func (d *Daemon) wakeCauseLine(delivery store.InboxDelivery) string {
	switch delivery.Item.Kind {
	case inbox.SeedUpdate:
		return mailboxItemContent(delivery)
	case inbox.PeerMessage:
		if delivery.Peer != nil {
			return prompts.RenderText("session", "wake-cause-message", prompts.Values{"sender": d.peerSenderName(delivery.Peer.Sender)})
		}
	}
	return ""
}

func (d *Daemon) peerSenderName(sender who.Party) string {
	if key, member := sender.Member(); member {
		return d.crewMemberName(key.String())
	}
	id, _ := sender.Session()
	return d.launchRequester(id, "another agent")
}
