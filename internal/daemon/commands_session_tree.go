package daemon

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

const sessionShortIDLength = 8

type sessionTreeNode struct {
	session  protocol.Session
	seedSlug string
	children []*sessionTreeNode
}

func (d *Daemon) sessionTree(ctx context.Context, c command.Caller, profileID string) (*protocol.SessionListResult, error) {
	callerProfileID := d.callerScope(c)
	inView := func(profile string) bool {
		return (callerProfileID == "" || profile == callerProfileID) && (profileID == "" || profile == profileID)
	}
	profiles, err := d.liveProtocolProfiles()
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(profiles))
	for _, profile := range profiles {
		names[profile.ID] = profile.Name
	}
	var nodes []*sessionTreeNode
	byID := map[protocol.SessionID]*sessionTreeNode{}
	slugsByProfile := map[string]map[string]string{}
	for _, session := range d.sessionsForBroadcast(d.store.List("")) {
		if !inView(session.ProfileID) {
			continue
		}
		node := &sessionTreeNode{session: session}
		if seedID := protocol.Deref(session.SeedID); seedID != "" {
			slug, err := d.treeSeedSlug(ctx, slugsByProfile, session.ProfileID, seedID)
			if err != nil {
				return nil, err
			}
			node.seedSlug = slug
		}
		nodes = append(nodes, node)
		byID[session.ID] = node
	}
	for _, profile := range profiles {
		if !inView(profile.ID) {
			continue
		}
		onStandby, err := d.runSeedList(ctx, command.Caller{Profile: profile.ID}, command.SeedLsArgs{Standby: true})
		if err != nil {
			return nil, fmt.Errorf("read seeds on standby in profile %s: %w", profile.ID, err)
		}
		for _, seed := range onStandby.(*protocol.SeedListResult).Seeds {
			if seed.Standby != nil && seed.Standby.Tender != nil {
				nodes = append(nodes, dormantTreeNode(profile.ID, seed, time.Now()))
			}
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i].session, nodes[j].session
		if a.ProfileID != b.ProfileID {
			if names[a.ProfileID] != names[b.ProfileID] {
				return names[a.ProfileID] < names[b.ProfileID]
			}
			return a.ProfileID < b.ProfileID
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.ID < b.ID
	})
	var roots []*sessionTreeNode
	for _, node := range nodes {
		if parent := byID[protocol.Deref(node.session.DispatcherSessionID)]; parent != nil {
			parent.children = append(parent.children, node)
		} else {
			roots = append(roots, node)
		}
	}
	result := &protocol.SessionListResult{Entries: []protocol.SessionLedgerEntry{}, Tree: []protocol.SessionTreeProfile{}}
	for _, root := range roots {
		if n := len(result.Tree); n == 0 || result.Tree[n-1].ProfileID != root.session.ProfileID {
			result.Tree = append(result.Tree, protocol.SessionTreeProfile{ProfileID: root.session.ProfileID, ProfileName: names[root.session.ProfileID], Rows: []protocol.SessionTreeRow{}})
		}
		group := &result.Tree[len(result.Tree)-1]
		group.Rows = appendTreeRows(group.Rows, root, "", "")
	}
	return result, nil
}

func (d *Daemon) treeSeedSlug(ctx context.Context, slugsByProfile map[string]map[string]string, profileID, seedID string) (string, error) {
	slugs := slugsByProfile[profileID]
	if slugs == nil {
		listed, err := d.runSeedList(ctx, command.Caller{Profile: profileID}, command.SeedLsArgs{})
		if err != nil {
			return "", fmt.Errorf("read seeds in profile %s: %w", profileID, err)
		}
		seeds := listed.(*protocol.SeedListResult).Seeds
		slugs = make(map[string]string, len(seeds))
		for _, seed := range seeds {
			slugs[seed.ID] = seed.StepSlug
		}
		slugsByProfile[profileID] = slugs
	}
	if slug, known := slugs[seedID]; known {
		return slug, nil
	}
	shown, err := d.showFor(ctx, command.Caller{Profile: profileID}, seedID, nil)
	if err != nil {
		return "", fmt.Errorf("read seed %s in profile %s: %w", seedID, profileID, err)
	}
	slugs[seedID] = shown.Seed.StepSlug
	return shown.Seed.StepSlug, nil
}

func dormantTreeNode(profileID string, seed protocol.SeedRow, now time.Time) *sessionTreeNode {
	tender := seed.Standby.Tender
	return &sessionTreeNode{
		session: protocol.Session{
			ID: tender.SessionID, Label: tender.Label, Agent: protocol.SessionAgent(tender.Agent), ProfileID: profileID,
			State: "standby", SeedID: protocol.Ptr(seed.ID),
			DispatcherSessionID: tender.DispatcherSessionID, Dispatcher: tender.Dispatcher,
		},
		seedSlug: seed.StepSlug + " · " + standbyWaitsFor(seed, now),
	}
}

func standbyWaitsFor(seed protocol.SeedRow, now time.Time) string {
	standby := seed.Standby
	if protocol.Deref(standby.ResumeBlocked) != "" {
		return "resume blocked"
	}
	if seed.Reminder != nil {
		if at, err := time.Parse(time.RFC3339, seed.Reminder.At); err == nil {
			return "until " + shortClock(at.Local(), now)
		}
	}
	if len(standby.PullRequests) > 0 {
		numbers := make([]string, len(standby.PullRequests))
		for i, pr := range standby.PullRequests {
			numbers[i] = "PR " + pr.PRID[strings.LastIndex(pr.PRID, "#"):]
		}
		return strings.Join(numbers, ", ")
	}
	note, _, _ := strings.Cut(standby.Note, "\n")
	return note
}

func shortClock(at, now time.Time) string {
	if at.Year() == now.Year() && at.YearDay() == now.YearDay() {
		return at.Format("15:04")
	}
	return at.Format("Jan 2 15:04")
}

func sessionShortID(id string) string {
	if len(id) <= sessionShortIDLength {
		return id
	}
	return id[:sessionShortIDLength]
}

func appendTreeRows(rows []protocol.SessionTreeRow, node *sessionTreeNode, prefix, branch string) []protocol.SessionTreeRow {
	session := node.session
	label := session.Label
	if branch == "" && (protocol.Deref(session.SeedID) != "" || protocol.Deref(session.DispatcherSessionID) != "" || session.Dispatcher != nil) {
		dispatcher := "dispatcher"
		if session.Dispatcher != nil && !strings.HasPrefix(string(session.Dispatcher.Ref), "session:") {
			dispatcher += " " + session.Dispatcher.Name
		}
		label += " (" + dispatcher + " closed or absent)"
	}
	if parent := protocol.Deref(session.ParentSessionID); parent != "" {
		label += " (shell split from " + sessionShortID(string(parent)) + ")"
	}
	turn := "-"
	if protocol.Deref(session.TurnOwed) {
		turn = "owed"
	}
	if until := protocol.Deref(session.TurnSnoozedUntil); until != "" {
		turn = "snoozed until " + until
	}
	row := protocol.SessionTreeRow{Prefix: prefix + branch, SessionID: session.ID, Label: label, Agent: string(session.Agent), State: string(session.State), Turn: turn}
	if node.seedSlug != "" {
		row.Seed = protocol.Ptr(node.seedSlug)
	}
	rows = append(rows, row)
	childPrefix := prefix
	if branch != "" {
		if branch == "└─ " {
			childPrefix += "   "
		} else {
			childPrefix += "│  "
		}
	}
	for i, child := range node.children {
		childBranch := "├─ "
		if i == len(node.children)-1 {
			childBranch = "└─ "
		}
		rows = appendTreeRows(rows, child, childPrefix, childBranch)
	}
	return rows
}
