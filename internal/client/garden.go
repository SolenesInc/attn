package client

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) SeedPlant(sessionID, title, body, partOf, discoveredFrom, member, resumeID, resumeCwd, resumeAgent string) (*protocol.SeedPlantResult, error) {
	msg := protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: title}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if body != "" {
		msg.Body = protocol.Ptr(body)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	if partOf != "" {
		msg.PartOf = protocol.Ptr(partOf)
	}
	if discoveredFrom != "" {
		msg.DiscoveredFrom = protocol.Ptr(discoveredFrom)
	}
	if resumeID != "" || resumeCwd != "" || resumeAgent != "" {
		msg.ResumeSessionID = protocol.Ptr(resumeID)
		msg.ResumeCwd = protocol.Ptr(resumeCwd)
		msg.ResumeAgent = protocol.Ptr(resumeAgent)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedPlantResult == nil {
		return nil, fmt.Errorf("the daemon accepted the planting but returned no seed")
	}
	return resp.SeedPlantResult, nil
}

func (c *Client) SeedSetResume(seedID, resumeID, cwd, agent string, clear bool) (*protocol.SeedSetResumeResult, error) {
	msg := protocol.SeedSetResumeMessage{Cmd: protocol.CmdSeedSetResume, SeedID: seedID}
	if clear {
		msg.Clear = protocol.Ptr(true)
	} else {
		msg.ResumeSessionID = protocol.Ptr(resumeID)
		msg.ResumeCwd = protocol.Ptr(cwd)
		msg.ResumeAgent = protocol.Ptr(agent)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedSetResumeResult == nil {
		return nil, fmt.Errorf("the daemon accepted the resume identity but returned no seed")
	}
	return resp.SeedSetResumeResult, nil
}

func (c *Client) SeedList(sessionID string, stale bool, staleWindowSeconds int) (*protocol.SeedListResult, error) {
	msg := protocol.SeedListMessage{Cmd: protocol.CmdSeedList}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if stale {
		msg.Stale = protocol.Ptr(true)
	}
	if staleWindowSeconds > 0 {
		msg.StaleWindowSeconds = protocol.Ptr(staleWindowSeconds)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedListResult == nil {
		return nil, fmt.Errorf("the daemon answered without a seed list")
	}
	return resp.SeedListResult, nil
}

func (c *Client) SeedShow(sessionID, seedID string) (*protocol.SeedShowResult, error) {
	msg := protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: seedID}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedShowResult == nil {
		return nil, fmt.Errorf("the daemon answered without a seed")
	}
	return resp.SeedShowResult, nil
}

func (c *Client) SeedReviewStart() (*protocol.SeedReviewResult, error) {
	resp, err := c.send(protocol.SeedReviewStartMessage{Cmd: protocol.CmdSeedReviewStart})
	if err != nil {
		return nil, err
	}
	if resp.SeedReviewResult == nil {
		return nil, fmt.Errorf("the daemon started the Garden review but returned no review")
	}
	return resp.SeedReviewResult, nil
}

func (c *Client) SeedReviewShow(reviewID string) (*protocol.SeedReviewResult, error) {
	msg := protocol.SeedReviewShowMessage{Cmd: protocol.CmdSeedReviewShow}
	if reviewID != "" {
		msg.ReviewID = protocol.Ptr(reviewID)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedReviewResult == nil {
		return nil, fmt.Errorf("the daemon answered without a Garden review")
	}
	return resp.SeedReviewResult, nil
}

func (c *Client) SeedReviewCancel(reviewID string) (*protocol.SeedReviewResult, error) {
	resp, err := c.send(protocol.SeedReviewCancelMessage{
		Cmd: protocol.CmdSeedReviewCancel, ReviewID: reviewID,
	})
	if err != nil {
		return nil, err
	}
	if resp.SeedReviewResult == nil {
		return nil, fmt.Errorf("the daemon canceled the Garden review but returned no review")
	}
	return resp.SeedReviewResult, nil
}

func (c *Client) SeedReviewRetry(reviewID, seedID string) (*protocol.SeedReviewResult, error) {
	resp, err := c.send(protocol.SeedReviewRetryMessage{
		Cmd: protocol.CmdSeedReviewRetry, ReviewID: reviewID, SeedID: seedID,
	})
	if err != nil {
		return nil, err
	}
	if resp.SeedReviewResult == nil {
		return nil, fmt.Errorf("the daemon retried the Garden review item but returned no review")
	}
	return resp.SeedReviewResult, nil
}

func (c *Client) SeedEdit(seedID, body string) (*protocol.SeedEditResult, error) {
	resp, err := c.send(protocol.SeedEditMessage{Cmd: protocol.CmdSeedEdit, SeedID: seedID, Body: body})
	if err != nil {
		return nil, err
	}
	if resp.SeedEditResult == nil {
		return nil, fmt.Errorf("the daemon accepted the edit but returned no seed")
	}
	return resp.SeedEditResult, nil
}

func (c *Client) SeedTransition(sessionID, seedID, verb, reason, member string, force bool) (*protocol.SeedTransitionResult, error) {
	msg := protocol.SeedTransitionMessage{Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: verb}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if reason != "" {
		if verb == "park" {
			msg.Comment = protocol.Ptr(reason)
		} else {
			msg.Reason = protocol.Ptr(reason)
		}
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	if force {
		msg.Force = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedTransitionResult == nil {
		return nil, fmt.Errorf("the daemon accepted the move but returned no seed")
	}
	return resp.SeedTransitionResult, nil
}

func (c *Client) SeedNote(sessionID, seedID, body, member, kind string, ring bool, artifact *protocol.SeedArtifactReference) (*protocol.SeedNoteResult, error) {
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body, Artifact: artifact}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	if kind != "" {
		msg.Kind = protocol.Ptr(kind)
	}
	if ring {
		msg.Ring = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedNoteResult == nil {
		return nil, fmt.Errorf("the daemon accepted the note but returned nothing")
	}
	return resp.SeedNoteResult, nil
}

func (c *Client) SeedLink(seedID, kind, toSeedID string, unlink bool) (*protocol.SeedLinkResult, error) {
	msg := protocol.SeedLinkMessage{
		Cmd: protocol.CmdSeedLink, SeedID: seedID, Kind: kind, ToSeedID: toSeedID,
	}
	if unlink {
		msg.Unlink = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedLinkResult == nil {
		return nil, fmt.Errorf("the daemon accepted the edge but returned no seed")
	}
	return resp.SeedLinkResult, nil
}

func (c *Client) SeedReady(sessionID, plot string, all bool) (*protocol.SeedReadyResult, error) {
	msg := protocol.SeedReadyMessage{Cmd: protocol.CmdSeedReady}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if plot != "" {
		msg.Plot = protocol.Ptr(plot)
	}
	if all {
		msg.All = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedReadyResult == nil {
		return nil, fmt.Errorf("the daemon answered without a ready list")
	}
	return resp.SeedReadyResult, nil
}

func (c *Client) SeedNotes(sessionID, seedID string, limit int) (*protocol.SeedNotesResult, error) {
	msg := protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: seedID}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedNotesResult == nil {
		return nil, fmt.Errorf("the daemon answered without a log")
	}
	return resp.SeedNotesResult, nil
}

func (c *Client) SeedWatch(sessionID, seedID string, unwatch bool) (*protocol.SeedWatchResult, error) {
	msg := protocol.SeedWatchMessage{
		Cmd: protocol.CmdSeedWatch, SourceSessionID: sessionID, SeedID: seedID,
	}
	if unwatch {
		msg.Unwatch = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedWatchResult == nil {
		return nil, fmt.Errorf("the daemon accepted the watch change but returned no state")
	}
	return resp.SeedWatchResult, nil
}

func (c *Client) SeedPlot(sessionID, member string, spec protocol.SeedPlotMessage) (*protocol.SeedPlotResult, error) {
	spec.Cmd = protocol.CmdSeedPlot
	if sessionID != "" {
		spec.SourceSessionID = protocol.Ptr(sessionID)
	}
	if member != "" {
		spec.Member = protocol.Ptr(member)
	}
	resp, err := c.send(spec)
	if err != nil {
		return nil, err
	}
	if resp.SeedPlotResult == nil {
		return nil, fmt.Errorf("the daemon accepted the plot but returned no seeds")
	}
	return resp.SeedPlotResult, nil
}
