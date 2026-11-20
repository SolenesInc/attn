package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) seedCommands() map[string]command.Handler {
	handlers := map[string]command.Handler{
		"seed search": command.Typed(d.runSeedSearch),
		"seed plant":  command.Typed(d.runSeedPlant),
		"seed plot":   command.Typed(d.runSeedPlot),
		"seed link": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedLinkArgs) (any, error) {
			return d.runSeedLink(c, a.A, a.Relation, a.B, false)
		}),
		"seed unlink": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedUnlinkArgs) (any, error) {
			return d.runSeedLink(c, a.A, a.Relation, a.B, true)
		}),
		"seed ready": command.Typed(d.runSeedReady),
		"seed ls":    command.Typed(d.runSeedList),
		"seed show":  command.Typed(d.runSeedShow),
		"seed notes": command.Typed(d.runSeedNotes),
		"seed prime": command.Typed(d.runSeedPrime),
		"seed guide": command.Typed(d.runSeedGuide),
		"seed tend": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedTendArgs) (any, error) {
			return d.runSeedMove(c, "tend", a.Seed, a.Reason, a.For, a.Force)
		}),
		"seed note": command.Typed(d.runSeedNote),
		"seed park": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedParkArgs) (any, error) {
			return d.runSeedMove(c, "park", a.Seed, a.Reason, "", a.Force)
		}),
		"seed replant": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedReplantArgs) (any, error) {
			return d.runSeedMove(c, "replant", a.Seed, a.Reason, "", a.Force)
		}),
		"seed wither": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedWitherArgs) (any, error) {
			return d.runSeedMove(c, "wither", a.Seed, a.Reason, "", a.Force)
		}),
		"seed send-to-chief": command.Typed(d.runSeedSendToChief),
		"seed harvest":       command.Typed(d.runSeedHarvest),
		"seed watch": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedWatchArgs) (any, error) {
			return d.runSeedWatch(c, a.Seed, false)
		}),
		"seed unwatch": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedUnwatchArgs) (any, error) {
			return d.runSeedWatch(c, a.Seed, true)
		}),
		"seed remind":        command.Typed(d.runSeedRemind),
		"seed standby":       command.Typed(d.runSeedStandby),
		"seed export":        command.Typed(d.runSeedExport),
		"seed edit":          command.Typed(d.runSeedEdit),
		"seed rollback":      command.Typed(d.runSeedRollback),
		"seed authorization": command.Typed(d.runSeedAuthorization),
		"seed attach": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedAttachArgs) (any, error) {
			return d.runSeedArtifact(ctx, c, "attach", artifactArgs{a.Seed, a.Path, a.Repo, a.Notebook, a.URL, a.Mode, "", false, a.Note})
		}),
		"seed detach": command.Typed(func(ctx context.Context, c command.Caller, a command.SeedDetachArgs) (any, error) {
			return d.runSeedArtifact(ctx, c, "detach", artifactArgs{a.Seed, a.Path, a.Repo, a.Notebook, a.URL, "", a.To, a.Reference, a.Note})
		}),
		"seed review start":  command.Typed(d.runSeedReviewStart),
		"seed review show":   command.Typed(d.runSeedReviewShow),
		"seed review cancel": command.Typed(d.runSeedReviewCancel),
		"seed review retry":  command.Typed(d.runSeedReviewRetry),
		"seed review keep":   command.Typed(d.runSeedReviewKeep),
	}
	for _, verb := range []string{"add", "edit", "remove", "restore", "report", "confirm", "reject", "uncheck", "reopen"} {
		handlers["seed check "+verb] = d.checkCommand(verb)
	}
	return handlers
}

func gardenScope(c command.Caller) (*protocol.SessionID, *string) {
	var session *protocol.SessionID
	if c.Session != "" {
		session = protocol.Ptr(c.Session)
	}
	var profile *string
	if c.Profile != "" {
		profile = protocol.Ptr(c.Profile)
	}
	return session, profile
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalTrue(value bool) *bool {
	if !value {
		return nil
	}
	return protocol.Ptr(true)
}

func bookmark(since *int64) (*int, error) {
	if since == nil {
		return nil, nil
	}
	if *since < 0 {
		return nil, command.Refuse("bad_bookmark", "{since} %d is not a bookmark; pass the number a read printed on its bookmark line", *since)
	}
	return protocol.Ptr(int(*since)), nil
}

func (d *Daemon) scoped(msg any, cmd string) error {
	return d.scopeGardenRequest(cmd, msg, "")
}

func (d *Daemon) runSeedSearch(_ context.Context, c command.Caller, args command.SeedSearchArgs) (any, error) {
	msg := seedSearchRequest{Cmd: cmdSeedSearch, Query: args.Words}
	if args.Limit != nil && *args.Limit != 0 {
		msg.Limit = protocol.Ptr(int(*args.Limit))
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedSearch(&msg)
}

func plantChecks(raw []string) []protocol.SeedCheckDefinition {
	checks := make([]protocol.SeedCheckDefinition, 0, len(raw))
	for _, entry := range raw {
		definition := protocol.SeedCheckDefinition{Text: strings.TrimSpace(entry)}
		if id, text, ok := strings.Cut(entry, "="); ok {
			id = strings.ToUpper(strings.TrimSpace(id))
			if garden.ValidateCheckID(id) == nil {
				definition.ID = protocol.Ptr(id)
				definition.Text = strings.TrimSpace(text)
			}
		}
		checks = append(checks, definition)
	}
	return checks
}

func (d *Daemon) runSeedPlant(_ context.Context, c command.Caller, args command.SeedPlantArgs) (any, error) {
	msg := seedPlantRequest{
		Cmd:            cmdSeedPlant,
		Title:          args.Title,
		Checks:         plantChecks(args.Checks),
		Body:           optionalText(args.Body),
		PartOf:         optionalText(strings.TrimSpace(args.PartOf)),
		DiscoveredFrom: optionalText(strings.TrimSpace(args.DiscoveredFrom)),
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedPlant(&msg, d.sessionOrOwner(c.Session))
}

type plotPayload struct {
	Title          string                         `json:"title"`
	Body           string                         `json:"body"`
	PartOf         string                         `json:"part_of"`
	DiscoveredFrom string                         `json:"discovered_from"`
	Checks         []protocol.SeedCheckDefinition `json:"checks"`
	Children       []struct {
		Title  string                         `json:"title"`
		Body   string                         `json:"body"`
		Checks []protocol.SeedCheckDefinition `json:"checks"`
		After  []string                       `json:"after"`
	} `json:"children"`
}

func decodeStrict(raw json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}

func (d *Daemon) runSeedPlot(_ context.Context, c command.Caller, args command.SeedPlotArgs) (any, error) {
	var plot plotPayload
	if err := decodeStrict(args.Plot, &plot); err != nil {
		return nil, command.Refuse("not_a_plot", "this is not a plot payload: %v", err)
	}
	msg := seedPlotRequest{
		Cmd:            cmdSeedPlot,
		Title:          plot.Title,
		Checks:         plot.Checks,
		Body:           optionalText(plot.Body),
		PartOf:         optionalText(plot.PartOf),
		DiscoveredFrom: optionalText(plot.DiscoveredFrom),
		Children:       []seedPlotChild{},
	}
	for _, child := range plot.Children {
		msg.Children = append(msg.Children, seedPlotChild{Title: child.Title, Body: optionalText(child.Body), Checks: child.Checks, After: child.After})
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedPlot(&msg, d.sessionOrOwner(c.Session))
}

func (d *Daemon) runSeedLink(c command.Caller, a, relation, b string, unlink bool) (any, error) {
	msg := seedLinkRequest{Cmd: cmdSeedLink, SeedID: a, Kind: relation, ToSeedID: b, Unlink: optionalTrue(unlink)}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedLink(&msg, d.sessionOrOwner(c.Session))
}

func parseWindow(raw string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		if hours, err := time.ParseDuration(days + "h"); err == nil {
			return hours * 24, nil
		}
	}
	if window, err := time.ParseDuration(raw); err == nil && window > 0 {
		return window, nil
	}
	return 0, command.Refuse("bad_window", "%s is not a window; say it like 72h or 14d", strconv.Quote(raw))
}

func (d *Daemon) runSeedList(_ context.Context, c command.Caller, args command.SeedLsArgs) (any, error) {
	plot := strings.TrimSpace(args.Plot)
	if !args.Stale && args.Window != "" {
		return nil, command.Refuse("window_without_stale", "{window} is the stale window; it only means something with {stale}")
	}
	if plot != "" && args.Stale {
		return nil, command.Refuse("plot_and_stale", "{plot} lists every child of the plot and {stale} narrows the whole garden; pass one of them")
	}
	if args.Standby && (plot != "" || args.Stale) {
		return nil, command.Refuse("standby_with_scope", "{standby} narrows the whole garden to seeds on standby; pass it without {plot} or {stale}")
	}
	msg := seedListRequest{Cmd: cmdSeedList}
	switch {
	case plot != "":
		msg.Plot = protocol.Ptr(plot)
	case args.Standby:
		msg.Standby = protocol.Ptr(true)
	default:
		msg.Stale = optionalTrue(args.Stale)
		if raw := strings.TrimSpace(args.Window); raw != "" {
			window, err := parseWindow(raw)
			if err != nil {
				return nil, err
			}
			if seconds := int(window / time.Second); seconds > 0 {
				msg.StaleWindowSeconds = protocol.Ptr(seconds)
			}
		}
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedList(&msg)
}

func (d *Daemon) runSeedShow(ctx context.Context, c command.Caller, args command.SeedShowArgs) (any, error) {
	if args.Since != nil && args.Section != "" {
		return nil, command.Refuse("since_and_section", "{since} reads log entries and {section} reads the body; pass one")
	}
	since, err := bookmark(args.Since)
	if err != nil {
		return nil, err
	}
	return d.showFor(ctx, c, args.Seed, since)
}

func (d *Daemon) showFor(ctx context.Context, c command.Caller, seed string, since *int) (*protocol.SeedShowResult, error) {
	msg := seedShowRequest{Cmd: cmdSeedShow, SeedID: seed, Since: since}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedShow(ctx, &msg)
}

func (d *Daemon) runSeedNotes(_ context.Context, c command.Caller, args command.SeedNotesArgs) (any, error) {
	since, err := bookmark(args.Since)
	if err != nil {
		return nil, err
	}
	msg := seedNotesRequest{Cmd: cmdSeedNotes, SeedID: args.Seed, Since: since, Kind: optionalText(strings.TrimSpace(args.Kind))}
	if args.Limit != nil && *args.Limit > 0 {
		msg.Limit = protocol.Ptr(int(*args.Limit))
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedNotesRead(&msg)
}

func (d *Daemon) runSeedNote(_ context.Context, c command.Caller, args command.SeedNoteArgs) (any, error) {
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: args.Seed, Body: args.Text, Ring: optionalTrue(args.Ring)}
	if args.Handoff {
		msg.Kind = protocol.Ptr(string(garden.NoteKindHandoff))
	}
	return d.noteFor(c, &msg)
}

func (d *Daemon) noteFor(c command.Caller, msg *protocol.SeedNoteMessage) (*protocol.SeedNoteResult, error) {
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedNote(msg, d.sessionOrOwner(c.Session))
}

func (d *Daemon) runSeedReady(_ context.Context, c command.Caller, args command.SeedReadyArgs) (any, error) {
	msg := seedReadyRequest{Cmd: cmdSeedReady, Plot: optionalText(strings.TrimSpace(args.Plot)), All: optionalTrue(args.All)}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedReady(&msg)
}

func (d *Daemon) runSeedPrime(ctx context.Context, c command.Caller, _ command.SeedPrimeArgs) (any, error) {
	return d.runSeedReady(ctx, c, command.SeedReadyArgs{})
}

func (d *Daemon) runSeedGuide(context.Context, command.Caller, command.SeedGuideArgs) (any, error) {
	guide, err := prompts.Builtin().Render("authoring-agent", "seed-guide", nil)
	if err != nil {
		return nil, err
	}
	return guide.Text, nil
}

func (d *Daemon) runSeedMove(c command.Caller, verb, seed, reason, assignee string, force bool) (any, error) {
	msg := protocol.SeedTransitionMessage{Cmd: protocol.CmdSeedTransition, SeedID: seed, Verb: verb, Assignee: optionalText(strings.TrimSpace(assignee)), Force: optionalTrue(force)}
	if reason != "" {
		if verb == string(garden.VerbPark) {
			msg.Comment = protocol.Ptr(reason)
		} else {
			msg.Reason = protocol.Ptr(reason)
		}
	}
	return d.moveFor(c, &msg)
}

func (d *Daemon) moveFor(c command.Caller, msg *protocol.SeedTransitionMessage) (*protocol.SeedTransitionResult, error) {
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedTransition(msg, d.sessionOrOwner(c.Session))
}

func pullRequestURLProblem(raw string) string {
	trimmed := strings.TrimSpace(raw)
	const shape = "pull request url must be an https repository pull URL"
	rest, ok := strings.CutPrefix(trimmed, "https://")
	if !ok || !strings.Contains(rest, "/") || strings.ContainsFunc(trimmed, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) || strings.ContainsAny(rest, "?#") {
		return shape
	}
	host, path, _ := strings.Cut(rest, "/")
	if host == "" || strings.ContainsAny(host, "@:") {
		return shape
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "pull request url path must be /owner/repository/pull/number"
	}
	for _, part := range parts {
		if part == "" || strings.Contains(part, "%") {
			return "pull request url contains an invalid path component"
		}
	}
	if number, err := strconv.ParseInt(parts[3], 10, 64); err != nil || number <= 0 {
		return "pull request url number must be positive"
	}
	return ""
}

func (d *Daemon) runSeedHarvest(_ context.Context, c command.Caller, args command.SeedHarvestArgs) (any, error) {
	msg := protocol.SeedTransitionMessage{Cmd: protocol.CmdSeedTransition, SeedID: args.Seed, Verb: string(garden.VerbHarvest), Force: optionalTrue(args.Force), Reason: optionalText(args.Reason)}
	switch {
	case !args.WhenMerged && !args.Clear:
		if args.PR != "" {
			return nil, command.Refuse("pr_without_when_merged", "needs exactly one seed id, got 2: attn seed harvest s-7k3f9m")
		}
	case !args.WhenMerged:
		return nil, command.Refuse("clear_without_when_merged", "{clear} disarms harvest-on-merge; say the whole thing: `attn seed harvest <id> --when-merged --clear`")
	case args.Clear:
		if args.Reason != "" {
			return nil, command.Refuse("clear_with_reason", "{clear} takes no reason; put it on the log with `attn seed note <id> -m \"…\"`")
		}
		if args.PR != "" {
			return nil, command.Refuse("clear_with_pr", "needs exactly one seed id, got 2: attn seed harvest s-7k3f9m --when-merged --clear")
		}
		msg.ClearHarvestWhen = protocol.Ptr(true)
	default:
		if args.PR != "" {
			if problem := pullRequestURLProblem(args.PR); problem != "" {
				return nil, command.Refuse("not_a_pr_url", "%s is not a pull request url: %s", strconv.Quote(args.PR), problem)
			}
		}
		msg.WhenMerged = &protocol.SeedHarvestWhenMerged{PullRequestURL: optionalText(args.PR)}
	}
	return d.moveFor(c, &msg)
}

func (d *Daemon) runSeedSendToChief(ctx context.Context, c command.Caller, args command.SeedSendToChiefArgs) (any, error) {
	shown, err := d.showFor(ctx, c, strings.TrimSpace(args.Seed), nil)
	if err != nil {
		return nil, err
	}
	msg := protocol.SeedSendToChiefMessage{Cmd: protocol.CmdSeedSendToChief, SeedID: shown.Seed.ID, ExpectedRev: shown.Seed.Rev, Guidance: optionalText(strings.TrimSpace(args.Guidance))}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.sendSeedToChief(&msg, protocol.Deref(msg.ProfileID), d.sessionOrOwner(c.Session))
	if err != nil {
		return nil, gardenFailure("send-to-chief", err)
	}
	return result, nil
}

type checkArgs struct {
	Seed      string   `json:"seed"`
	ID        string   `json:"id"`
	Statement string   `json:"statement"`
	Reason    string   `json:"reason"`
	Summary   string   `json:"summary"`
	Detail    string   `json:"detail"`
	How       string   `json:"how"`
	Proof     string   `json:"proof"`
	Evidence  []string `json:"evidence"`
	Commit    string   `json:"commit"`
	Shared    bool     `json:"shared"`
	Ring      bool     `json:"ring"`
}

func (d *Daemon) checkCommand(verb string) command.Handler {
	run := func(ctx context.Context, c command.Caller, args checkArgs) (any, error) {
		return d.runSeedCheck(ctx, c, verb, args)
	}
	switch verb {
	case "add":
		return command.Typed(func(ctx context.Context, c command.Caller, a command.SeedCheckAddArgs) (any, error) {
			return run(ctx, c, checkArgs{Seed: a.Seed, ID: a.ID, Statement: a.Statement, Shared: a.Shared, Ring: a.Ring})
		})
	case "edit":
		return command.Typed(func(ctx context.Context, c command.Caller, a command.SeedCheckEditArgs) (any, error) {
			return run(ctx, c, checkArgs{Seed: a.Seed, ID: a.ID, Statement: a.Statement, Ring: a.Ring})
		})
	case "report":
		return command.Typed(func(ctx context.Context, c command.Caller, a command.SeedCheckReportArgs) (any, error) {
			return run(ctx, c, checkArgs{Seed: a.Seed, ID: a.ID, Summary: a.Summary, Detail: a.Detail, Evidence: a.Evidence, Commit: a.Commit, Ring: a.Ring})
		})
	case "confirm":
		return command.Typed(func(ctx context.Context, c command.Caller, a command.SeedCheckConfirmArgs) (any, error) {
			return run(ctx, c, checkArgs{Seed: a.Seed, ID: a.ID, How: a.How, Ring: a.Ring})
		})
	case "reject":
		return command.Typed(func(ctx context.Context, c command.Caller, a command.SeedCheckRejectArgs) (any, error) {
			return run(ctx, c, checkArgs{Seed: a.Seed, ID: a.ID, Proof: a.Proof, Ring: a.Ring})
		})
	default:
		return command.Typed(func(ctx context.Context, c command.Caller, a command.SeedCheckRemoveArgs) (any, error) {
			return run(ctx, c, checkArgs{Seed: a.Seed, ID: a.ID, Reason: a.Reason, Ring: a.Ring})
		})
	}
}

func (d *Daemon) runSeedCheck(ctx context.Context, c command.Caller, verb string, args checkArgs) (any, error) {
	defines := verb == "add" || verb == "edit"
	if defines && args.ID != "" && garden.ValidateCheckID(strings.ToUpper(args.ID)) != nil {
		return nil, command.Refuse("not_a_check_id", "%s is not a check id; the statement goes in {statement}", strconv.Quote(args.ID))
	}
	msg := protocol.SeedCheckMessage{Cmd: protocol.CmdSeedCheck, SeedID: args.Seed, Verb: verb, Shared: optionalTrue(args.Shared), Ring: optionalTrue(args.Ring)}
	if args.ID != "" {
		msg.CheckID = protocol.Ptr(strings.ToUpper(args.ID))
	}
	text := args.Statement + args.Reason + args.How + args.Proof + args.Summary
	if args.Detail != "" {
		text += "\n" + args.Detail
	}
	text = strings.TrimSpace(text)
	switch {
	case defines && text == "":
		return nil, command.Refuse("needs_statement", "needs the statement in {statement}: what this seed must prove")
	case defines:
		msg.Text = protocol.Ptr(text)
	case text != "":
		msg.Summary = protocol.Ptr(text)
	}
	msg.Commit = optionalText(args.Commit)
	for _, evidence := range args.Evidence {
		if filepath.IsAbs(evidence) {
			msg.EvidenceFiles = append(msg.EvidenceFiles, evidence)
		} else {
			msg.Evidence = append(msg.Evidence, evidence)
		}
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	outcome, err := d.applySeedCheck(ctx, &msg, d.sessionOrOwner(c.Session))
	if err != nil {
		return nil, gardenFailure("check", err)
	}
	return &protocol.SeedCheckResult{Seed: outcome.seed, CheckID: outcome.checkID, Note: outcome.note, RingRecipients: outcome.ringRecipients, Attached: outcome.attached}, nil
}

func (d *Daemon) runSeedWatch(c command.Caller, seed string, unwatch bool) (any, error) {
	msg := seedWatchRequest{Cmd: cmdSeedWatch, SeedID: seed, SourceSessionID: c.Session, Unwatch: optionalTrue(unwatch)}
	_, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	return d.seedWatch(&msg)
}

func (d *Daemon) runSeedRemind(_ context.Context, c command.Caller, args command.SeedRemindArgs) (any, error) {
	text := strings.TrimSpace(args.Message)
	switch {
	case args.Clear && (args.In != "" || args.At != "" || text != ""):
		return nil, command.Refuse("clear_with_reminder", "{clear} takes no time or message")
	case !args.Clear && (args.In == "") == (args.At == ""):
		return nil, command.Refuse("needs_time", "pass one of {in} or {at}, or {clear}")
	case !args.Clear && text == "":
		return nil, command.Refuse("needs_message", "{message} names what the reminder says")
	}
	msg := seedReminderRequest{Cmd: cmdSeedReminder, SeedID: args.Seed, In: optionalText(args.In), At: optionalText(args.At), Message: optionalText(text), Clear: optionalTrue(args.Clear)}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.seedReminderRequest(&msg, d.sessionOrOwner(c.Session))
	if err != nil {
		return nil, gardenFailure("remind", err)
	}
	return result, nil
}

func (d *Daemon) runSeedStandby(_ context.Context, c command.Caller, args command.SeedStandbyArgs) (any, error) {
	note := strings.TrimSpace(args.Next)
	switch {
	case args.Clear && (note != "" || args.Stay || args.In != "" || args.At != ""):
		return nil, command.Refuse("clear_with_standby", "{clear} takes no note, time or {stay}")
	case args.In != "" && args.At != "":
		return nil, command.Refuse("in_and_at", "pass {in} or {at}, not both")
	case !args.Clear && note == "":
		return nil, command.Refuse("needs_next", "{next} says what to do when mail resumes you")
	}
	msg := seedStandbyRequest{Cmd: cmdSeedStandby, SeedID: args.Seed, Note: optionalText(note), In: optionalText(args.In), At: optionalText(args.At), Stay: optionalTrue(args.Stay), Clear: optionalTrue(args.Clear)}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, closeSelf, err := d.seedStandbyRequest(&msg, d.sessionOrOwner(c.Session))
	if err != nil {
		return nil, gardenFailure("standby", err)
	}
	if closeSelf == nil {
		return result, nil
	}
	closed, closing, refusal := d.beginAgentClose(closeSelf)
	if refusal != nil {
		return nil, fmt.Errorf("%s is on standby, but the session did not close: %s", result.Seed.ID, refusal.message)
	}
	result.Close = closed
	if protocol.Deref(closed.PendingCompact) {
		return result, nil
	}
	return command.Then{Result: result, After: func() { d.finishSessionClose(closed.TargetSessionID, closing) }}, nil
}

func stampBody(id, body string) string {
	sum := sha256.Sum256([]byte(body))
	return fmt.Sprintf("<!-- attn-seed %s body-sha256 %s -->\n%s", id, hex.EncodeToString(sum[:]), body)
}

func (d *Daemon) runSeedExport(ctx context.Context, c command.Caller, args command.SeedExportArgs) (any, error) {
	shown, err := d.showFor(ctx, c, args.Seed, nil)
	if err != nil {
		return nil, err
	}
	text := stampBody(shown.Seed.ID, shown.Seed.Body)
	return &protocol.SeedExportResult{SeedID: shown.Seed.ID, Bytes: len(text), Text: protocol.Ptr(text)}, nil
}

func editRecipe(id string) string {
	return fmt.Sprintf("edit takes an exported file; run attn seed export %s, edit it, then attn seed edit %s -m - < %s.md", id, id, id)
}

func (d *Daemon) runSeedEdit(_ context.Context, c command.Caller, args command.SeedEditArgs) (any, error) {
	if args.Body == "" && args.Base == "" {
		return nil, command.Refuse("not_exported", "%s", editRecipe(args.Seed))
	}
	msg := seedEditRequest{Cmd: cmdSeedEdit, SeedID: args.Seed, Body: args.Body, BaseBodySHA256: args.Base, Note: protocol.Ptr(args.Note)}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.applySeedBodyEdit(&msg, d.sessionOrOwner(c.Session))
	if err != nil {
		var conflict seedBodyConflictError
		if errors.As(err, &conflict) {
			err = fmt.Errorf("%w; run attn seed export %s and reapply your change", err, msg.SeedID)
		}
		return nil, gardenFailure("edit", err)
	}
	return result, nil
}

func (d *Daemon) runSeedRollback(_ context.Context, c command.Caller, args command.SeedRollbackArgs) (any, error) {
	msg := seedRollbackRequest{Cmd: cmdSeedRollback, SeedID: args.Seed}
	if args.Version != nil {
		msg.Version = protocol.Ptr(int(*args.Version))
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.rollbackSeedBody(&msg, d.sessionOrOwner(c.Session))
	if err != nil {
		return nil, gardenFailure("rollback", err)
	}
	return result, nil
}

func (d *Daemon) runSeedAuthorization(_ context.Context, c command.Caller, args command.SeedAuthorizationArgs) (any, error) {
	words := strings.TrimSpace(args.Words)
	if args.Words != "" && words == "" {
		return nil, command.Refuse("empty_words", "the words are empty; {clear} removes the authorization")
	}
	if words != "" && args.Clear {
		return nil, command.Refuse("words_and_clear", "pass the new words or {clear}, not both")
	}
	msg := protocol.SeedAuthorizationMessage{Cmd: protocol.CmdSeedAuthorization, SeedID: args.Seed, Text: optionalText(words), Clear: optionalTrue(args.Clear)}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.seedAuthorizationRequest(&msg, d.sessionOrOwner(c.Session))
	if err != nil {
		return nil, gardenFailure("authorization", err)
	}
	return result, nil
}

type artifactArgs struct {
	Seed, Path, Repo, Notebook, URL, Mode, To string
	Reference                                 bool
	Note                                      string
}

func (d *Daemon) runSeedArtifact(ctx context.Context, c command.Caller, verb string, a artifactArgs) (any, error) {
	repo, notebook, url := strings.TrimSpace(a.Repo), strings.TrimSpace(a.Notebook), strings.TrimSpace(a.URL)
	moved, copied := a.Mode == "move", a.Mode == "copy"
	plainManaged := a.Path != "" && repo == "" && notebook == "" && url == "" && !a.Reference
	managed := plainManaged && (verb == "attach" || verb == "detach")
	if managed || moved || copied || a.To != "" {
		return d.transferArtifact(ctx, c, verb, a, plainManaged)
	}
	if a.Reference && (a.Path == "" || repo != "" || notebook != "" || url != "") {
		return nil, command.Refuse("reference_needs_path", "{reference} removes one old linked {path} without {repo}")
	}
	path := strings.TrimSpace(a.Path)
	var named []string
	for _, flag := range []struct{ name, value string }{{"{notebook}", notebook}, {"{path}", path}, {"{url}", url}} {
		if flag.value != "" {
			named = append(named, flag.name)
		}
	}
	switch len(named) {
	case 0:
		return nil, command.Refuse("no_document", "name the document: {path} <file> [{repo} <repository>], {notebook} <document-id>, or {url} <url>")
	case 1:
	default:
		return nil, command.Refuse("two_documents", "%s were all given and a reference names one document; run it once per document", strings.Join(named, " and "))
	}
	reference := protocol.SeedArtifactReference{}
	switch {
	case path != "":
		reference.Kind, reference.Path, reference.Repository = "markdown_file", protocol.Ptr(path), optionalText(repo)
	case notebook != "":
		reference.Kind, reference.NotebookDocumentID = "notebook", protocol.Ptr(notebook)
	default:
		reference.Kind, reference.URL = "url", protocol.Ptr(url)
	}
	kind := garden.NoteKindAttach
	if verb == "detach" {
		kind = garden.NoteKindDetach
	}
	return d.noteFor(c, &protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: a.Seed, Body: a.Note, Kind: protocol.Ptr(string(kind)), Artifact: &reference})
}

func (d *Daemon) transferArtifact(ctx context.Context, c command.Caller, verb string, a artifactArgs, plainManaged bool) (any, error) {
	if a.Note != "" {
		return nil, command.Refuse("transfer_with_note", "managed artifact transfers carry their own receipt; {note} applies to log associations")
	}
	msg := protocol.SeedArtifactTransferMessage{Cmd: protocol.CmdSeedArtifactTransfer, SeedID: a.Seed}
	if verb == "attach" {
		switch {
		case !plainManaged:
			return nil, command.Refuse("transfer_needs_local_path", "--move and --copy apply only to a local {path} without {repo}")
		case a.Mode == "":
			return nil, command.Refuse("transfer_needs_mode", "local attach requires exactly one of {mode}; --move is recommended")
		}
		msg.Operation, msg.SourcePath = a.Mode, protocol.Ptr(a.Path)
	} else {
		switch {
		case !plainManaged:
			return nil, command.Refuse("transfer_needs_managed_path", "{to} applies only to a managed {path} without {repo}")
		case a.To == "":
			return nil, command.Refuse("detach_needs_to", "managed detach requires {to} <destination>; use {reference} to remove an old linked path")
		}
		msg.Operation, msg.Filename, msg.DestinationPath = "detach", protocol.Ptr(a.Path), protocol.Ptr(a.To)
	}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	result, err := d.submitSeedArtifactTransfer(ctx, &msg, d.sessionOrOwner(c.Session))
	if err != nil {
		return nil, gardenFailure("artifact", err)
	}
	return result, nil
}

func (d *Daemon) reviewResult(verb string, run *garden.ReviewRun, items []garden.ReviewItem, candidates int, err error) (*protocol.SeedReviewResult, error) {
	if err != nil {
		return nil, gardenFailure("review "+verb, err)
	}
	result := &protocol.SeedReviewResult{CandidateCount: candidates}
	if run != nil {
		review := gardenReviewToProtocol(*run, items)
		result.Review = &review
	}
	return result, nil
}

func (d *Daemon) runSeedReviewStart(_ context.Context, c command.Caller, _ command.SeedReviewStartArgs) (any, error) {
	msg := protocol.SeedReviewStartMessage{Cmd: protocol.CmdSeedReviewStart}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	run, items, err := d.startGardenReview(context.Background(), protocol.Deref(msg.ProfileID))
	return d.reviewResult("start", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) showReview(c command.Caller, reviewID string) (*protocol.SeedReviewResult, error) {
	msg := protocol.SeedReviewShowMessage{Cmd: protocol.CmdSeedReviewShow, ReviewID: optionalText(reviewID)}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	if reviewID == "" {
		run, items, count, err := d.gardenReviewOverview(protocol.Deref(msg.ProfileID))
		return d.reviewResult("show", run, items, count, err)
	}
	run, items, err := d.showGardenReview(reviewID, protocol.Deref(msg.ProfileID))
	return d.reviewResult("show", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) runSeedReviewShow(_ context.Context, c command.Caller, args command.SeedReviewShowArgs) (any, error) {
	return d.showReview(c, args.Review)
}

func (d *Daemon) runSeedReviewCancel(_ context.Context, c command.Caller, args command.SeedReviewCancelArgs) (any, error) {
	msg := protocol.SeedReviewCancelMessage{Cmd: protocol.CmdSeedReviewCancel, ReviewID: args.Review}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	run, items, err := d.cancelGardenReview(msg.ReviewID)
	return d.reviewResult("cancel", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) runSeedReviewRetry(_ context.Context, c command.Caller, args command.SeedReviewRetryArgs) (any, error) {
	msg := protocol.SeedReviewRetryMessage{Cmd: protocol.CmdSeedReviewRetry, ReviewID: args.Review, SeedID: args.Seed}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	run, _, err := d.retryGardenReviewItem(msg.ReviewID, msg.SeedID)
	var items []garden.ReviewItem
	if err == nil {
		run, items, err = d.showGardenReview(run.ID)
	}
	return d.reviewResult("retry", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) runSeedReviewKeep(_ context.Context, c command.Caller, args command.SeedReviewKeepArgs) (any, error) {
	shown, err := d.showReview(c, args.Review)
	if err != nil {
		return nil, err
	}
	msg := protocol.SeedReviewKeepMessage{Cmd: protocol.CmdSeedReviewKeep, SeedID: args.Seed, Review: protocol.SeedReviewActionContext{ReviewID: args.Review}}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return nil, err
	}
	var receipt *protocol.GardenReviewItem
	if shown.Review != nil {
		for i := range shown.Review.Items {
			if shown.Review.Items[i].SeedID == msg.SeedID {
				receipt = &shown.Review.Items[i]
			}
		}
	}
	if receipt == nil {
		return nil, command.Refuse("not_in_review", "seed %s is not part of Garden review %s", args.Seed, args.Review)
	}
	msg.Review.EvidenceVersion = receipt.EvidenceVersion
	run, items, err := d.keepGardenReviewItem(msg.Review, msg.SeedID)
	return d.reviewResult("keep", &run, items, unresolvedGardenReviewItemCount(items), err)
}
