package daemon

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/store"
)

var ticketSlugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func ticketSlug(label string) string {
	s := ticketSlugStrip.ReplaceAllString(strings.ToLower(strings.TrimSpace(label)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		s = "ticket"
	}
	return s
}

const ticketSlugSequentialAttempts = 50

const ticketSlugRandomSuffixLen = 6

func (d *Daemon) createTicketWithUniqueSlug(template store.Ticket, base, author, ownerRole string, subscribers []string, now time.Time) (*store.Ticket, error) {
	for attempt := 0; attempt < ticketSlugSequentialAttempts+5; attempt++ {
		switch {
		case attempt == 0:
			template.ID = base
		case attempt < ticketSlugSequentialAttempts:
			template.ID = fmt.Sprintf("%s-%d", base, attempt+1)
		default:
			template.ID = fmt.Sprintf("%s-%s", base, strings.ToLower(uuid.NewString()[:ticketSlugRandomSuffixLen]))
		}
		created, err := d.store.CreateTicketWithSubscribers(template, author, ownerRole, subscribers, now)
		if err == nil {
			return created, nil
		}
		if !errors.Is(err, store.ErrTicketIDTaken) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate a unique ticket id from %q", base)
}
