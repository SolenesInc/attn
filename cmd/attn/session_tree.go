package main

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

type sessionTreeNode struct {
	session  protocol.Session
	seedSlug string
	children []*sessionTreeNode
}

func runSessionTree(w io.Writer, cli *client.Client, profileID string) error {
	caller, err := currentSession(cli)
	if err != nil {
		return err
	}
	listed, err := cli.List("")
	if err != nil {
		return err
	}
	callerProfileID := ""
	if caller != "" {
		for _, session := range listed.Sessions {
			if session.ID == caller {
				callerProfileID = session.ProfileID
				break
			}
		}
		if callerProfileID == "" {
			return fmt.Errorf("caller session %s is not in the live list", caller)
		}
	}
	profiles := make(map[string]string, len(listed.Profiles))
	for _, profile := range listed.Profiles {
		profiles[profile.ID] = profile.Name
	}
	var nodes []*sessionTreeNode
	byID := make(map[protocol.SessionID]*sessionTreeNode, len(listed.Sessions))
	slugsByProfile := map[string]map[string]string{}
	for _, session := range listed.Sessions {
		if callerProfileID != "" && session.ProfileID != callerProfileID {
			continue
		}
		if profileID != "" && session.ProfileID != profileID {
			continue
		}
		node := &sessionTreeNode{session: session}
		if seedID := protocol.Deref(session.SeedID); seedID != "" {
			slugs := slugsByProfile[session.ProfileID]
			if slugs == nil {
				seeds, err := cli.WithGardenProfile(session.ProfileID, "").SeedList("", false, 0)
				if err != nil {
					return fmt.Errorf("read seeds in profile %s: %w", session.ProfileID, err)
				}
				slugs = make(map[string]string, len(seeds.Seeds))
				for _, seed := range seeds.Seeds {
					slugs[seed.ID] = seed.StepSlug
				}
				slugsByProfile[session.ProfileID] = slugs
			}
			slug, known := slugs[seedID]
			if !known {
				shown, err := cli.WithGardenProfile(session.ProfileID, "").SeedShow("", seedID)
				if err != nil {
					return fmt.Errorf("read seed %s in profile %s: %w", seedID, session.ProfileID, err)
				}
				slug = shown.Seed.StepSlug
				slugs[seedID] = slug
			}
			node.seedSlug = slug
		}
		nodes = append(nodes, node)
		byID[session.ID] = node
	}
	if len(nodes) == 0 {
		fmt.Fprintln(w, emptySessionListMessage(sessionListArgs{profile: profileID}))
		return nil
	}
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i].session, nodes[j].session
		if a.ProfileID != b.ProfileID {
			if profiles[a.ProfileID] != profiles[b.ProfileID] {
				return profiles[a.ProfileID] < profiles[b.ProfileID]
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
	var profile string
	var table *tabwriter.Writer
	for _, root := range roots {
		if table == nil || root.session.ProfileID != profile {
			if table != nil {
				table.Flush()
				fmt.Fprintln(w)
			}
			profile = root.session.ProfileID
			fmt.Fprintf(w, "%s (%s)\n", profiles[profile], profile)
			table = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(table, "SESSION\tAGENT\tSTATE\tTURN\tSEED")
		}
		fprintSessionTreeNode(table, root, "", "")
	}
	table.Flush()
	return nil
}

func fprintSessionTreeNode(w io.Writer, node *sessionTreeNode, prefix, branch string) {
	session := node.session
	label := session.Label
	if branch == "" && (protocol.Deref(session.SeedID) != "" || protocol.Deref(session.DispatcherSessionID) != "" || protocol.Deref(session.DispatcherMember) != "") {
		dispatcher := "dispatcher"
		if member := protocol.Deref(session.DispatcherMember); member != "" {
			dispatcher += " " + member
		}
		label += " (" + dispatcher + " closed or absent)"
	}
	if parent := protocol.Deref(session.ParentSessionID); parent != "" {
		label += " (shell split from " + agentShortID(string(parent)) + ")"
	}
	turn := "-"
	if protocol.Deref(session.TurnOwed) {
		turn = "owed"
	}
	if until := protocol.Deref(session.TurnSnoozedUntil); until != "" {
		turn = "snoozed until " + until
	}
	fmt.Fprintf(w, "%s%s%s  %s\t%s\t%s\t%s\t%s\n", prefix, branch,
		agentShortID(string(session.ID)), label, session.Agent, session.State, turn, orDash(node.seedSlug))
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
		fprintSessionTreeNode(w, child, childPrefix, childBranch)
	}
}
