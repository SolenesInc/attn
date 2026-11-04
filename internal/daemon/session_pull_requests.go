package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type sessionPullRequestFact struct {
	PRID string `json:"pr_id"`
}

func (d *Daemon) handlePullRequestCreated(conn net.Conn, msg *protocol.PullRequestCreatedMessage) {
	sessionID := msg.ID
	if msg.TerminalID != nil {
		sessionID = d.sessionInTerminal(*msg.TerminalID)
	}
	rec, err := d.sessionPullRequestIdentity(sessionID, msg.URL)
	if err != nil {
		d.sendError(conn, err.Error())
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
	if err := d.forgetSessionPullRequest(rec); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handlePullRequestWatch(conn net.Conn, msg *protocol.PullRequestWatchMessage) {
	rec, err := d.sessionPullRequestIdentity(msg.ID, msg.URL)
	if err == nil {
		err = d.watchSessionPullRequest(rec, protocol.Deref(msg.SeedID), prreadiness.Mode(msg.Mode), protocol.Deref(msg.Reviewer))
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handlePullRequestUnwatch(conn net.Conn, msg *protocol.PullRequestUnwatchMessage) {
	rec, err := d.sessionPullRequestIdentity(msg.ID, msg.URL)
	if err == nil {
		if seedID := protocol.Deref(msg.SeedID); seedID != "" {
			err = d.unwatchSeedPullRequest(rec, seedID)
		} else {
			err = d.unwatchSessionPullRequest(rec)
		}
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handlePullRequestWatchList(conn net.Conn, msg *protocol.PullRequestWatchListMessage) {
	seed, err := d.seedOfSessionProfile(protocol.TrimID(msg.ID), msg.SeedID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	address := who.ToTenderOf(seed.ID)
	watched, err := d.store.PullRequestWatchesTo(address)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("pull requests watched for seed %s: %v", seed.ID, err))
		return
	}
	records := make([]store.SessionPullRequestRecord, 0, len(watched))
	byPR := make(map[string][]store.PullRequestWatch, len(watched))
	for _, w := range watched {
		records = append(records, w.Record)
		byPR[w.Watch.PRID] = append(byPR[w.Watch.PRID], w.Watch)
	}
	pullRequests := d.sessionPullRequestsForBroadcast(records, []who.Address{address}, byPR)
	if pullRequests == nil {
		pullRequests = []protocol.SessionPullRequest{}
	}
	json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SeedPullRequests: pullRequests})
}

func (d *Daemon) seedOfSessionProfile(sessionID protocol.SessionID, seedID string) (garden.Seed, error) {
	session := d.store.Get(sessionID)
	if session == nil {
		return garden.Seed{}, fmt.Errorf("unknown session %s", sessionID)
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return garden.Seed{}, err
	}
	if seed.ProfileID != session.ProfileID {
		return garden.Seed{}, fmt.Errorf("no seed %s is planted here; `attn seed ls` lists the garden", seed.ID)
	}
	return seed, nil
}

func (d *Daemon) watchSessionPullRequest(rec store.SessionPullRequestRecord, seedID string, mode prreadiness.Mode, reviewer string) error {
	if err := prreadiness.ValidateConfig(mode, reviewer); err != nil {
		return err
	}
	address, err := d.pullRequestWatchAddress(rec, seedID)
	if err != nil {
		return err
	}
	recorded, changed, err := d.store.WatchPullRequest(rec, address, mode, reviewer, time.Now())
	if err != nil {
		return fmt.Errorf("watch pull request %s: %w", rec.PRID, err)
	}
	if changed {
		d.kickInboxAfterCommit(address)
		d.publishSessionPullRequestMembershipChanged(rec.PRID, string(rec.SessionID))
		d.schedulePullRequestRefreshNow(rec.SessionID, rec.PRID)
	} else if recorded {
		d.publishFact(FactSessionPullRequestChanged, string(rec.SessionID), sessionPullRequestFact{PRID: rec.PRID})
	}
	return nil
}

func (d *Daemon) pullRequestWatchAddress(rec store.SessionPullRequestRecord, seedID string) (who.Address, error) {
	if seedID != "" {
		seed, err := d.seedOfSessionProfile(rec.SessionID, seedID)
		if err != nil {
			return who.Address{}, err
		}
		if garden.Closed(seed.Status) {
			return who.Address{}, fmt.Errorf("seed %s is %s; a closed seed cannot watch a pull request", seed.ID, seed.Status)
		}
		return who.ToTenderOf(seed.ID), nil
	}
	for _, held := range d.inboxRoleAddresses(rec.SessionID) {
		if _, exists := d.store.PullRequestWatch(held, rec.PRID); exists {
			return held, nil
		}
	}
	return d.inboxAddressOf(rec.SessionID), nil
}

func (d *Daemon) unwatchSeedPullRequest(rec store.SessionPullRequestRecord, seedID string) error {
	seed, err := d.seedOfSessionProfile(rec.SessionID, seedID)
	if err != nil {
		return err
	}
	stopped, err := d.stopPullRequestWatch(who.ToTenderOf(seed.ID), rec.PRID)
	if err != nil {
		return fmt.Errorf("unwatch pull request %s: %w", rec.PRID, err)
	}
	if !stopped {
		return fmt.Errorf("seed %s is not watching pull request %s", seed.ID, rec.PRID)
	}
	return nil
}

func (d *Daemon) stopSeedPullRequestWatches(seedID string) error {
	address := who.ToTenderOf(seedID)
	watched, err := d.store.PullRequestWatchesTo(address)
	if err != nil {
		return err
	}
	for _, w := range watched {
		if _, err := d.stopPullRequestWatch(address, w.Watch.PRID); err != nil {
			return fmt.Errorf("stop watching pull request %s: %w", w.Watch.PRID, err)
		}
	}
	return nil
}

func (d *Daemon) stopPullRequestWatch(address who.Address, prID string) (bool, error) {
	watch, exists := d.store.PullRequestWatch(address, prID)
	if !exists {
		return false, nil
	}
	stopped, err := d.store.StopPullRequestWatch(address, prID)
	if err != nil || !stopped {
		return false, err
	}
	d.kickInboxAfterCommit(address)
	d.publishSessionPullRequestMembershipChanged(prID, string(watch.SessionID))
	return true, nil
}

func (d *Daemon) unwatchSessionPullRequest(rec store.SessionPullRequestRecord) error {
	addresses := d.inboxRoleAddresses(rec.SessionID)
	changed := false
	for _, address := range addresses {
		stopped, err := d.store.StopPullRequestWatch(address, rec.PRID)
		if err != nil {
			return fmt.Errorf("unwatch pull request %s: %w", rec.PRID, err)
		}
		changed = changed || stopped
	}
	if !changed {
		return fmt.Errorf("session %s is not watching pull request %s", rec.SessionID, rec.PRID)
	}
	d.kickInboxAfterCommit(addresses...)
	d.publishSessionPullRequestMembershipChanged(rec.PRID, string(rec.SessionID))
	return nil
}

func (d *Daemon) publishSessionPullRequestMembershipChanged(prID string, extraSessionIDs ...string) {
	sessionIDs, err := d.store.SessionPullRequestSessionIDs(prID)
	if err != nil {
		d.logf("session pull requests: list sessions for %s membership change: %v", prID, err)
	}
	sessionIDs = append(sessionIDs, extraSessionIDs...)
	seen := make(map[string]bool, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if sessionID != "" && !seen[sessionID] {
			seen[sessionID] = true
			d.publishFact(FactSessionPullRequestChanged, sessionID, sessionPullRequestFact{PRID: prID})
		}
	}
}

func (d *Daemon) recordSessionPullRequest(rec store.SessionPullRequestRecord) error {
	result, err := Call[protocol.PullRequestLinkResult](context.Background(), d.router(), protocol.PullRequestLinkMessage{Cmd: protocol.CmdPullRequestLink, SessionID: rec.SessionID, Kind: protocol.PullRequestLinkKindRecord, PRID: rec.PRID, Repository: protocol.Ptr(rec.Repository), Number: protocol.Ptr(rec.Number), URL: protocol.Ptr(rec.URL)})
	if err != nil {
		return err
	}
	if !result.Applied {
		return fmt.Errorf("record pull request %s: monitor view was not applied", rec.PRID)
	}
	return nil
}

func (d *Daemon) forgetSessionPullRequest(rec store.SessionPullRequestRecord) error {
	if err := d.forgetMonitorPullRequest(rec.SessionID, rec.PRID); err != nil {
		return err
	}
	addresses := d.inboxRoleAddresses(rec.SessionID)
	forgotten := false
	for _, address := range addresses {
		removed, sessions, err := d.store.ForgetSessionPullRequest(rec.SessionID, address, rec.PRID)
		if err != nil {
			return fmt.Errorf("forget pull request %s: %w", rec.PRID, err)
		}
		for _, session := range sessions {
			if session != rec.SessionID && d.store.Get(session) != nil {
				if err := d.forgetMonitorPullRequest(session, rec.PRID); err != nil {
					return err
				}
			}
		}
		forgotten = forgotten || removed
	}
	if !forgotten {
		return fmt.Errorf("session %s has no pull request %s recorded", rec.SessionID, rec.PRID)
	}
	d.kickInboxAfterCommit(addresses...)
	d.publishSessionPullRequestMembershipChanged(rec.PRID, string(rec.SessionID))
	return nil
}

func (d *Daemon) sessionPullRequestIdentity(id protocol.SessionID, url string) (store.SessionPullRequestRecord, error) {
	var rec store.SessionPullRequestRecord
	sessionID := protocol.TrimID(id)
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

func (d *Daemon) pullRequestWatchesByPR() map[string][]store.PullRequestWatch {
	byPR := make(map[string][]store.PullRequestWatch)
	for _, watch := range d.store.PullRequestWatches() {
		byPR[watch.PRID] = append(byPR[watch.PRID], watch)
	}
	return byPR
}

func (d *Daemon) sessionPullRequestsForBroadcast(records []store.SessionPullRequestRecord, addresses []who.Address, watchesByPR map[string][]store.PullRequestWatch) []protocol.SessionPullRequest {
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
		if rec.CIStatus != "" {
			entry.CIStatus = protocol.Ptr(rec.CIStatus)
		}
		if rec.ReviewStatus != "" {
			entry.ReviewStatus = protocol.Ptr(rec.ReviewStatus)
		}
		entry.MergeableState = pullRequestField(rec.MergeableState)
		entry.StatusFetchedAt = pullRequestField(rec.StatusFetchedAt)
		entry.ReadinessState = pullRequestField(rec.ReadinessState)
		entry.ReadinessReason = pullRequestField(rec.ReadinessReason)
		entry.SettlingUntil = pullRequestField(rec.SettlingUntil)
		entry.WatchHealth = pullRequestField(rec.WatchHealth)
		entry.WatchError = pullRequestField(rec.WatchError)
		entry.WatchLastCheckedAt = pullRequestField(rec.WatchLastCheckedAt)
		watches := watchesByPR[rec.PRID]
		if len(watches) > 0 {
			for _, watch := range watches {
				if slices.Contains(addresses, watch.To) {
					entry.Watching = protocol.Ptr(true)
					entry.WatchMode = protocol.Ptr(protocol.PullRequestWatchMode(watch.Mode))
					entry.WatchReviewer = pullRequestField(watch.Reviewer)
				}
			}
		}
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

func (d *Daemon) sessionPullRequestsForSession(session *protocol.Session) []protocol.SessionPullRequest {
	addresses := []who.Address{who.ToSession(session.ID)}
	if member := protocol.Deref(session.CrewMember); member != "" {
		address, err := memberAddress(member)
		if err != nil {
			d.logf("pull requests of %s: %v", session.ID, err)
		} else {
			addresses = append(addresses, address)
		}
	}
	if protocol.Deref(session.ChiefOfStaff) {
		addresses = append(addresses, who.ToChief(session.ProfileID))
	}
	byPR := d.pullRequestWatchesByPR()
	bySession := d.store.SessionPullRequestsOf(withWatchHolders([]protocol.SessionID{session.ID}, byPR))
	return d.sessionPullRequestsForBroadcast(d.sessionPullRequestRecords(session.ID, addresses, bySession, byPR), addresses, byPR)
}

func withWatchHolders(ids []protocol.SessionID, byPR map[string][]store.PullRequestWatch) []protocol.SessionID {
	for _, watches := range byPR {
		for _, watch := range watches {
			ids = append(ids, watch.SessionID)
		}
	}
	return ids
}

func (d *Daemon) sessionPullRequestRecords(sessionID protocol.SessionID, addresses []who.Address, bySession map[protocol.SessionID][]store.SessionPullRequestRecord, byPR map[string][]store.PullRequestWatch) []store.SessionPullRequestRecord {
	records := append([]store.SessionPullRequestRecord(nil), bySession[sessionID]...)
	for prID, watches := range byPR {
		for _, watch := range watches {
			if !slices.Contains(addresses, watch.To) {
				continue
			}
			for _, rec := range bySession[watch.SessionID] {
				if rec.PRID != prID {
					continue
				}
				rec.SessionID = sessionID
				replaced := false
				for i := range records {
					if records[i].PRID == prID {
						records[i] = rec
						replaced = true
						break
					}
				}
				if !replaced {
					records = append(records, rec)
				}
			}
		}
	}
	return records
}

func (d *Daemon) forgetMonitorPullRequest(session protocol.SessionID, pr string) error {
	result, err := Call[protocol.PullRequestLinkResult](context.Background(), d.router(), protocol.PullRequestLinkMessage{Cmd: protocol.CmdPullRequestLink, SessionID: session, Kind: protocol.PullRequestLinkKindForget, PRID: pr})
	if err != nil {
		return err
	}
	if !result.Applied {
		return fmt.Errorf("forget pull request %s: the session's monitor could not save it", pr)
	}
	return nil
}
