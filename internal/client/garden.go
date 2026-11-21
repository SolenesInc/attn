package client

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

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

func (c *Client) SeedReviewKeep(seedID string, review protocol.SeedReviewActionContext) (*protocol.SeedReviewResult, error) {
	resp, err := c.send(protocol.SeedReviewKeepMessage{
		Cmd: protocol.CmdSeedReviewKeep, SeedID: seedID, Review: review,
	})
	if err != nil {
		return nil, err
	}
	if resp.SeedReviewResult == nil {
		return nil, fmt.Errorf("the daemon kept the seed growing but returned no review")
	}
	return resp.SeedReviewResult, nil
}

func (c *Client) SeedSendToChief(
	sessionID protocol.SessionID,
	document protocol.Seed,
	guidance string,
) (*protocol.SeedSendToChiefResult, error) {
	msg := protocol.SeedSendToChiefMessage{
		Cmd: protocol.CmdSeedSendToChief, SeedID: document.ID,
		ExpectedRev: document.Rev,
	}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if guidance != "" {
		msg.Guidance = protocol.Ptr(guidance)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedSendToChiefResult == nil {
		return nil, fmt.Errorf("the daemon accepted the seed for Chief but returned no result")
	}
	return resp.SeedSendToChiefResult, nil
}

func (c *Client) SeedArtifactTransfer(sessionID protocol.SessionID, seedID string, operation string, sourcePath string, filename string, destinationPath string, legacy *protocol.SeedArtifactReference) (*protocol.SeedArtifactTransferResult, error) {
	msg := protocol.SeedArtifactTransferMessage{
		Cmd: protocol.CmdSeedArtifactTransfer, SeedID: seedID, Operation: operation,
		LegacyReference: legacy,
	}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if sourcePath != "" {
		msg.SourcePath = protocol.Ptr(sourcePath)
	}
	if filename != "" {
		msg.Filename = protocol.Ptr(filename)
	}
	if destinationPath != "" {
		msg.DestinationPath = protocol.Ptr(destinationPath)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedArtifactTransferResult == nil {
		return nil, fmt.Errorf("the daemon accepted the artifact transfer but returned no receipt")
	}
	return resp.SeedArtifactTransferResult, nil
}

func (c *Client) SeedAuthorization(seedID, text string, clear bool) (*protocol.SeedAuthorizationResult, error) {
	msg := protocol.SeedAuthorizationMessage{Cmd: protocol.CmdSeedAuthorization, SeedID: seedID}
	if text != "" {
		msg.Text = protocol.Ptr(text)
	}
	if clear {
		msg.Clear = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedAuthorizationResult == nil {
		return nil, fmt.Errorf("the daemon accepted the authorization but returned no seed")
	}
	return resp.SeedAuthorizationResult, nil
}

type SeedReminderOptions struct {
	In      string
	At      string
	Message string
	Clear   bool
}

type SeedStandbyOptions struct {
	Note  string
	In    string
	At    string
	Stay  bool
	Clear bool
}

type SeedTransitionOptions struct {
	Assignee         string
	WhenMerged       bool
	PullRequestURL   string
	ClearHarvestWhen bool
}

func (c *Client) SeedTransition(sessionID protocol.SessionID, seedID string, verb string, reason string, force bool, opts SeedTransitionOptions) (*protocol.SeedTransitionResult, error) {
	msg := protocol.SeedTransitionMessage{Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: verb}
	if opts.WhenMerged {
		merged := protocol.SeedHarvestWhenMerged{}
		if opts.PullRequestURL != "" {
			merged.PullRequestURL = protocol.Ptr(opts.PullRequestURL)
		}
		msg.WhenMerged = &merged
	}
	if opts.ClearHarvestWhen {
		msg.ClearHarvestWhen = protocol.Ptr(true)
	}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if reason != "" {
		msg.Reason = protocol.Ptr(reason)
	}
	if opts.Assignee != "" {
		msg.Assignee = protocol.Ptr(opts.Assignee)
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

func (c *Client) SeedNote(sessionID protocol.SessionID, seedID string, body string, kind string, ring bool, artifact *protocol.SeedArtifactReference) (*protocol.SeedNoteResult, error) {
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body, Artifact: artifact}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
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

func (c *Client) SeedCheck(msg protocol.SeedCheckMessage) (*protocol.SeedCheckResult, error) {
	msg.Cmd = protocol.CmdSeedCheck
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SeedCheckResult == nil {
		return nil, fmt.Errorf("the daemon accepted the check move but returned nothing")
	}
	return resp.SeedCheckResult, nil
}

type SeedNotesQuery struct {
	Limit int
	Since *int
	Kind  string
}

func (c *Client) RunCommand(name string, session protocol.SessionID, args any, out any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	msg := protocol.RunCommandMessage{Cmd: protocol.CmdRunCommand, Command: name, Args: raw}
	if session == "" {
		session = c.gardenSession
	}
	if session != "" {
		msg.SessionID = protocol.Ptr(session)
	}
	if c.gardenProfile != "" {
		msg.ProfileID = protocol.Ptr(c.gardenProfile)
	}
	resp, err := c.send(msg)
	if err != nil {
		return err
	}
	if resp.CommandResult == nil {
		return fmt.Errorf("%s: the daemon answered without a result", name)
	}
	raw, err = json.Marshal(resp.CommandResult)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

type Plant struct {
	Title          string                         `json:"title"`
	Body           *string                        `json:"body,omitempty"`
	PartOf         *string                        `json:"part_of,omitempty"`
	DiscoveredFrom *string                        `json:"discovered_from,omitempty"`
	Checks         []protocol.SeedCheckDefinition `json:"-"`
}

func (c *Client) SeedPlant(sessionID protocol.SessionID, title string, body string, partOf string, discoveredFrom string) (*protocol.SeedPlantResult, error) {
	return c.SeedPlantWith(sessionID, Plant{Title: title, Body: optional(body), PartOf: optional(partOf), DiscoveredFrom: optional(discoveredFrom)})
}

func (c *Client) SeedPlantWith(sessionID protocol.SessionID, plant Plant) (*protocol.SeedPlantResult, error) {
	var checks []string
	for _, check := range plant.Checks {
		if protocol.Deref(check.Shared) {
			return nil, fmt.Errorf("seed plant defines no shared checks; plant a plot to share %q", check.Text)
		}
		if id := protocol.Deref(check.ID); id != "" {
			checks = append(checks, id+"="+check.Text)
		} else {
			checks = append(checks, check.Text)
		}
	}
	args := struct {
		Plant
		Checks []string `json:"checks,omitempty"`
	}{plant, checks}
	return commandResult[protocol.SeedPlantResult](c, "seed plant", sessionID, args)
}

type PlotChild struct {
	Title  string                         `json:"title"`
	Body   *string                        `json:"body,omitempty"`
	Checks []protocol.SeedCheckDefinition `json:"checks,omitempty"`
	After  []string                       `json:"after,omitempty"`
}

type Plot struct {
	Title          string                         `json:"title"`
	Body           *string                        `json:"body,omitempty"`
	PartOf         *string                        `json:"part_of,omitempty"`
	DiscoveredFrom *string                        `json:"discovered_from,omitempty"`
	Checks         []protocol.SeedCheckDefinition `json:"checks,omitempty"`
	Children       []PlotChild                    `json:"children"`
}

func (c *Client) SeedPlot(sessionID protocol.SessionID, plot Plot) (*protocol.SeedPlotResult, error) {
	return commandResult[protocol.SeedPlotResult](c, "seed plot", sessionID, map[string]any{"plot": plot})
}

func (c *Client) SeedSearch(sessionID protocol.SessionID, query string, limit int) (*protocol.SeedSearchResult, error) {
	args := map[string]any{"words": query}
	if limit != 0 {
		args["limit"] = limit
	}
	return commandResult[protocol.SeedSearchResult](c, "seed search", sessionID, args)
}

func (c *Client) SeedList(sessionID protocol.SessionID, stale bool, staleWindowSeconds int) (*protocol.SeedListResult, error) {
	args := map[string]any{}
	if stale {
		args["stale"] = true
	}
	if staleWindowSeconds > 0 {
		args["window"] = fmt.Sprintf("%ds", staleWindowSeconds)
	}
	return commandResult[protocol.SeedListResult](c, "seed ls", sessionID, args)
}

func (c *Client) SeedListStandby(sessionID protocol.SessionID) (*protocol.SeedListResult, error) {
	return commandResult[protocol.SeedListResult](c, "seed ls", sessionID, map[string]any{"standby": true})
}

func (c *Client) SeedShow(sessionID protocol.SessionID, seedID string) (*protocol.SeedShowResult, error) {
	return commandResult[protocol.SeedShowResult](c, "seed show", sessionID, map[string]any{"seed": seedID})
}

func (c *Client) SeedShowSince(sessionID protocol.SessionID, seedID string, bookmark int) (*protocol.SeedShowResult, error) {
	return commandResult[protocol.SeedShowResult](c, "seed show", sessionID, map[string]any{"seed": seedID, "since": bookmark})
}

func (c *Client) SeedEdit(seedID, body, baseBodySHA256, note string) (*protocol.SeedEditResult, error) {
	exported := fmt.Sprintf("<!-- attn-seed %s body-sha256 %s -->\n%s", seedID, baseBodySHA256, body)
	args := map[string]any{"seed": seedID, "body": exported}
	if note != "" {
		args["note"] = note
	}
	return commandResult[protocol.SeedEditResult](c, "seed edit", "", args)
}

func (c *Client) SeedReminder(seedID string, opts SeedReminderOptions) (*protocol.SeedReminderResult, error) {
	args := map[string]any{"seed": seedID, "in": opts.In, "at": opts.At, "message": opts.Message, "clear": opts.Clear}
	return commandResult[protocol.SeedReminderResult](c, "seed remind", "", args)
}

func (c *Client) SeedStandby(seedID string, opts SeedStandbyOptions) (*protocol.SeedStandbyResult, error) {
	args := map[string]any{"seed": seedID, "next": opts.Note, "in": opts.In, "at": opts.At, "stay": opts.Stay, "clear": opts.Clear}
	return commandResult[protocol.SeedStandbyResult](c, "seed standby", "", args)
}

func (c *Client) SeedRollback(seedID string, version *int) (*protocol.SeedRollbackResult, error) {
	args := map[string]any{"seed": seedID}
	if version != nil {
		args["version"] = *version
	}
	return commandResult[protocol.SeedRollbackResult](c, "seed rollback", "", args)
}

func (c *Client) SeedLink(seedID, kind, toSeedID string, unlink bool) (*protocol.SeedLinkResult, error) {
	name := "seed link"
	if unlink {
		name = "seed unlink"
	}
	return commandResult[protocol.SeedLinkResult](c, name, "", map[string]any{"a": seedID, "relation": kind, "b": toSeedID})
}

func (c *Client) SeedReady(sessionID protocol.SessionID, plot string, all bool) (*protocol.SeedReadyResult, error) {
	return commandResult[protocol.SeedReadyResult](c, "seed ready", sessionID, map[string]any{"plot": plot, "all": all})
}

func (c *Client) SeedNotes(sessionID protocol.SessionID, seedID string, query SeedNotesQuery) (*protocol.SeedNotesResult, error) {
	args := map[string]any{"seed": seedID, "kind": query.Kind}
	if query.Limit > 0 {
		args["limit"] = query.Limit
	}
	if query.Since != nil {
		args["since"] = *query.Since
	}
	return commandResult[protocol.SeedNotesResult](c, "seed notes", sessionID, args)
}

func (c *Client) SeedWatch(sessionID protocol.SessionID, seedID string, unwatch bool) (*protocol.SeedWatchResult, error) {
	name := "seed watch"
	if unwatch {
		name = "seed unwatch"
	}
	return commandResult[protocol.SeedWatchResult](c, name, sessionID, map[string]any{"seed": strings.TrimSpace(seedID)})
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
