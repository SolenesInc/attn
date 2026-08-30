package daemon

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type sessionPullRequestFact struct {
	PRID string `json:"pr_id"`
}

func (d *Daemon) handlePullRequestCreated(conn net.Conn, msg *protocol.PullRequestCreatedMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	rec, err := d.sessionPullRequestIdentity(sessionID, msg.URL)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	recorded, err := d.store.RecordSessionPullRequest(rec, time.Now())
	if err != nil {
		d.sendError(conn, fmt.Sprintf("record pull request %s: %v", rec.PRID, err))
		return
	}
	d.sendOK(conn)

	// A repeat report changes nothing, so it must not wake every client either.
	if recorded {
		d.publishFact(FactSessionPullRequestChanged, sessionID, sessionPullRequestFact{PRID: rec.PRID})
	}
}

func (d *Daemon) handlePullRequestForget(conn net.Conn, msg *protocol.PullRequestForgetMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	rec, err := d.sessionPullRequestIdentity(sessionID, msg.URL)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	forgotten, err := d.store.ForgetSessionPullRequest(sessionID, rec.PRID)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("forget pull request %s: %v", rec.PRID, err))
		return
	}
	if !forgotten {
		d.sendError(conn, fmt.Sprintf("session %s has no pull request %s recorded", sessionID, rec.PRID))
		return
	}
	d.sendOK(conn)
	d.publishFact(FactSessionPullRequestChanged, sessionID, sessionPullRequestFact{PRID: rec.PRID})
}

// A well-formed pull request URL is recorded whatever host it names: the agent did
// open it. Whether attn can poll that host is the refresh job's answer, not this one.
func (d *Daemon) sessionPullRequestIdentity(sessionID, url string) (store.SessionPullRequestRecord, error) {
	var rec store.SessionPullRequestRecord
	if sessionID == "" {
		return rec, fmt.Errorf("pull request report needs a session id")
	}
	if d.store.Get(sessionID) == nil {
		return rec, fmt.Errorf("unknown session %s", sessionID)
	}
	host, owner, repository, number, err := automation.ParsePullRequestURL(url)
	if err != nil {
		return rec, fmt.Errorf("pull request url %q: %w", strings.TrimSpace(url), err)
	}
	identity := host + "/" + owner + "/" + repository
	return store.SessionPullRequestRecord{
		SessionID:  sessionID,
		PRID:       host + ":" + owner + "/" + repository + "#" + strconv.Itoa(number),
		Repository: identity,
		Number:     number,
		URL:        strings.TrimSpace(url),
	}, nil
}

func sessionPullRequestsForBroadcast(records []store.SessionPullRequestRecord) []protocol.SessionPullRequest {
	if len(records) == 0 {
		return nil
	}
	out := make([]protocol.SessionPullRequest, 0, len(records))
	for _, rec := range records {
		entry := protocol.SessionPullRequest{
			Repository: rec.Repository,
			Number:     rec.Number,
			URL:        rec.URL,
			CreatedAt:  rec.CreatedAt,
			State:      sessionPullRequestState(rec),
		}
		entry.Title = pullRequestField(rec.Title)
		entry.CIStatus = pullRequestField(rec.CIStatus)
		entry.ReviewStatus = pullRequestField(rec.ReviewStatus)
		entry.MergeableState = pullRequestField(rec.MergeableState)
		entry.StatusFetchedAt = pullRequestField(rec.StatusFetchedAt)
		out = append(out, entry)
	}
	return out
}

// Draft is a flag on an open pull request in GitHub's model and a state in ours,
// because the line has room for one word.
func sessionPullRequestState(rec store.SessionPullRequestRecord) string {
	if rec.Draft && rec.State == "open" {
		return "draft"
	}
	return rec.State
}

func pullRequestField(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return protocol.Ptr(value)
}

func (d *Daemon) sessionPullRequestsForSession(sessionID string) []protocol.SessionPullRequest {
	return sessionPullRequestsForBroadcast(d.store.ListSessionPullRequests(sessionID))
}
