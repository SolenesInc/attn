package daemon

import (
	"context"
	"encoding/json"
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
	rec, err := d.sessionPullRequestIdentity(msg.ID, msg.URL)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.forwardedToSessionOwner(conn, rec.SessionID, msg) {
		return
	}
	if err := d.recordSessionPullRequest(rec); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handlePullRequestForget(conn net.Conn, msg *protocol.PullRequestForgetMessage) {
	rec, err := d.sessionPullRequestIdentity(msg.ID, msg.URL)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.forwardedToSessionOwner(conn, rec.SessionID, msg) {
		return
	}
	if err := d.forgetSessionPullRequest(rec); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

// A hub forwards these to the session's owner, which answers over its endpoint link
// and has no caller waiting; the log is where a rejection can still be read.
func (d *Daemon) handlePullRequestCreatedWS(msg *protocol.PullRequestCreatedMessage) {
	rec, err := d.sessionPullRequestIdentity(msg.ID, msg.URL)
	if err == nil {
		err = d.recordSessionPullRequest(rec)
	}
	if err != nil {
		d.logf("forwarded pull request report: %v", err)
	}
}

func (d *Daemon) handlePullRequestForgetWS(msg *protocol.PullRequestForgetMessage) {
	rec, err := d.sessionPullRequestIdentity(msg.ID, msg.URL)
	if err == nil {
		err = d.forgetSessionPullRequest(rec)
	}
	if err != nil {
		d.logf("forwarded pull request forget: %v", err)
	}
}

func (d *Daemon) recordSessionPullRequest(rec store.SessionPullRequestRecord) error {
	recorded, err := d.store.RecordSessionPullRequest(rec, time.Now())
	if err != nil {
		return fmt.Errorf("record pull request %s: %w", rec.PRID, err)
	}
	if recorded {
		d.publishFact(FactSessionPullRequestChanged, rec.SessionID, sessionPullRequestFact{PRID: rec.PRID})
	}
	return nil
}

func (d *Daemon) forgetSessionPullRequest(rec store.SessionPullRequestRecord) error {
	forgotten, err := d.store.ForgetSessionPullRequest(rec.SessionID, rec.PRID)
	if err != nil {
		return fmt.Errorf("forget pull request %s: %w", rec.PRID, err)
	}
	if !forgotten {
		return fmt.Errorf("session %s has no pull request %s recorded", rec.SessionID, rec.PRID)
	}
	d.publishFact(FactSessionPullRequestChanged, rec.SessionID, sessionPullRequestFact{PRID: rec.PRID})
	return nil
}

func (d *Daemon) sessionPullRequestIdentity(id, url string) (store.SessionPullRequestRecord, error) {
	var rec store.SessionPullRequestRecord
	sessionID := strings.TrimSpace(id)
	if sessionID == "" {
		return rec, fmt.Errorf("pull request report needs a session id")
	}
	if d.store.Get(sessionID) == nil && d.sessionOwnerEndpoint(sessionID) == "" {
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

// A hub holds no rows for a session it does not own, so the mutation travels to the
// daemon that does and its snapshot brings the result back.
func (d *Daemon) forwardedToSessionOwner(conn net.Conn, sessionID string, msg any) bool {
	endpointID := d.sessionOwnerEndpoint(sessionID)
	if endpointID == "" {
		return false
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("forward command for session %s: %v", sessionID, err))
		return true
	}
	if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, payload); err != nil {
		d.sendError(conn, fmt.Sprintf("reach the endpoint owning session %s: %v", sessionID, err))
		return true
	}
	d.sendOK(conn)
	return true
}

func (d *Daemon) sessionOwnerEndpoint(sessionID string) string {
	if d.hubManager == nil || d.store.Get(sessionID) != nil {
		return ""
	}
	endpointID, ok := d.hubManager.EndpointIDForSession(sessionID)
	if !ok {
		return ""
	}
	return endpointID
}
