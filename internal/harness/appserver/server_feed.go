package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/transcript"
)

type feedKey struct {
	session      protocol.SessionID
	conversation string
}

type serverFeed struct {
	p            *serverConnection
	l            *Link
	c            *effectrpc.Client
	session      protocol.SessionID
	conversation string
	subagent     bool
	cancel       context.CancelFunc
	events       appServerEvents
	seen         int64
	launched     time.Time
	calls        map[string]serverToolCall
	usage        any
	compactions  any

	usageChanged       bool
	compactionsChanged bool
	titledFromHistory  bool
	sawHistory         bool
	baselined          bool
	baseline           map[string]serverModelUsage
}

type serverModelUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

type serverToolCall struct {
	name  string
	input json.RawMessage
}

type serverViewItem struct {
	Tag  string            `json:"_tag"`
	View json.RawMessage   `json:"view"`
	Ops  []json.RawMessage `json:"ops"`
}

type serverView struct {
	Entries []transcript.ServerEntry   `json:"entries"`
	Before  int64                      `json:"before"`
	Docs    map[string]json.RawMessage `json:"docs"`
}

func (l *Link) WatchesConversations() bool {
	if l == nil {
		return false
	}
	return l.Can(AbilityWatch)
}

func (p *serverConnection) openFeeds(c *effectrpc.Client, conversations map[string]bool, live bool) {
	sessions := p.sessions()
	for id, l := range sessions {
		conversation := p.c.host.Sessions().Conversation(id)
		if conversation == "" || !l.WatchesConversations() || (conversations != nil && !conversations[conversation]) {
			continue
		}
		key := feedKey{session: id, conversation: conversation}
		if _, open := p.feeds[key]; open {
			continue
		}
		ctx, cancel := context.WithCancel(p.c.host.Life().Context())
		stored := p.c.host.Records().Feed(id)
		f := &serverFeed{p: p, l: l, c: c, session: id, conversation: conversation, cancel: cancel, seen: stored.Entry, launched: p.c.host.Sessions().LaunchedAt(id), calls: map[string]serverToolCall{}}
		f.baselined = stored.UsageBaseline != "" || stored.Entry > 0
		_ = json.Unmarshal([]byte(stored.UsageBaseline), &f.baseline)
		if p.feeds == nil {
			p.feeds = make(map[feedKey]*serverFeed)
		}
		p.feeds[key] = f
		if !p.c.host.Life().Go("serverFeed", func() { f.run(ctx, live) }) {
			cancel()
			delete(p.feeds, key)
		}
	}
	if len(sessions) > 0 {
		p.scheduleListScan(c, live)
	}
}

func (p *serverConnection) scheduleListScan(c *effectrpc.Client, live bool) {
	if p.client() != c || len(p.sessions()) == 0 {
		return
	}
	if p.subagentScanClient == c {
		p.subagentScanAgain = true
		p.subagentScanLive = p.subagentScanLive || live
		return
	}
	p.subagentScanClient = c
	p.subagentScanAgain = false
	p.subagentScanLive = false
	if !p.c.host.Life().Go("serverConversations", func() {
		listed, err := p.listConversations(p.c.host.Life().Context(), c)
		p.events.run(p.c, func() {
			if p.client() != c {
				if p.subagentScanClient == c {
					p.subagentScanClient, p.subagentScanAgain, p.subagentScanLive = nil, false, false
				}
				return
			}
			if err == nil {
				p.openSubagentFeeds(c, listed, live)
				p.adoptListed(c, listed)
			}
			if p.subagentScanClient == c {
				again, againLive := p.subagentScanAgain, p.subagentScanLive
				p.subagentScanClient, p.subagentScanAgain, p.subagentScanLive = nil, false, false
				if again {
					p.scheduleListScan(c, againLive)
				}
			}
		})
	}) {
		p.subagentScanClient = nil
	}
}

func (p *serverConnection) openSubagentFeeds(c *effectrpc.Client, listed []listedConversation, live bool) {
	roots := make(map[string]map[protocol.SessionID]*Link)
	for id, l := range p.sessions() {
		if !l.WatchesConversations() {
			continue
		}
		conversation := p.c.host.Sessions().Conversation(id)
		if conversation == "" {
			continue
		}
		if roots[conversation] == nil {
			roots[conversation] = make(map[protocol.SessionID]*Link)
		}
		roots[conversation][id] = l
	}
	if len(roots) == 0 {
		return
	}
	byID := indexListedConversations(listed)
	for _, each := range listed {
		if !subagentOfRoot(each, byID) {
			continue
		}
		forkAt := int64(0)
		if each.Fork != nil {
			forkAt = each.Fork.At
		}
		for id, l := range roots[each.Lineage.Root] {
			p.openSubagentFeed(c, id, l, each.ID, forkAt, live)
		}
	}
}

func subagentOfRoot(each listedConversation, byID map[string]listedConversation) bool {
	if each.kind() != hostedSubagentConversation {
		return false
	}
	seen := map[string]bool{each.ID: true}
	for at := each; at.Lineage != nil && at.Lineage.Parent != ""; {
		if at.Lineage.Parent == each.Lineage.Root {
			return true
		}
		if seen[at.Lineage.Parent] {
			return false
		}
		seen[at.Lineage.Parent] = true
		parent, ok := byID[at.Lineage.Parent]
		if !ok {
			return true
		}
		if parent.kind() == workerConversation {
			return false
		}
		at = parent
	}
	return true
}

func (p *serverConnection) openSubagentFeed(c *effectrpc.Client, id protocol.SessionID, l *Link, conversation string, forkAt int64, live bool) {
	key := feedKey{session: id, conversation: conversation}
	if _, open := p.feeds[key]; open {
		return
	}
	ctx, cancel := context.WithCancel(p.c.host.Life().Context())
	stored := p.c.host.Records().SubagentFeed(id, conversation)
	seen := stored.Entry
	if seen == 0 && forkAt > 0 {
		seen = forkAt
	}
	f := &serverFeed{p: p, l: l, c: c, session: id, conversation: conversation, subagent: true, cancel: cancel, seen: seen, launched: p.c.host.Sessions().LaunchedAt(id), calls: map[string]serverToolCall{}}
	f.baselined = stored.UsageBaseline != "" || stored.Entry > 0
	_ = json.Unmarshal([]byte(stored.UsageBaseline), &f.baseline)
	if p.feeds == nil {
		p.feeds = make(map[feedKey]*serverFeed)
	}
	p.feeds[key] = f
	if !p.c.host.Life().Go("serverFeed", func() { f.run(ctx, live) }) {
		cancel()
		delete(p.feeds, key)
	}
}

func (p *serverConnection) closeFeed(id protocol.SessionID) {
	for key, f := range p.feeds {
		if key.session == id {
			f.cancel()
			delete(p.feeds, key)
		}
	}
}

func (p *serverConnection) closeFeeds() {
	for key, f := range p.feeds {
		f.cancel()
		delete(p.feeds, key)
	}
}

func (f *serverFeed) run(ctx context.Context, live bool) {
	err := f.startAfterInherited(ctx)
	if err == nil {
		err = f.watch(ctx, live)
	}
	f.p.events.run(f.p.c, func() {
		key := feedKey{session: f.session, conversation: f.conversation}
		if f.p.feeds[key] == f {
			delete(f.p.feeds, key)
		}
	})
	if err != nil && ctx.Err() == nil {
		f.l.logf("conversation feed of %s ended: %v", f.conversation, err)
	}
}

func (f *serverFeed) startAfterInherited(ctx context.Context) error {
	if f.seen > 0 || f.subagent {
		return nil
	}
	info, ok, err := f.l.findListedConversation(ctx, f.c, f.conversation)
	if err != nil || !ok {
		return err
	}
	if info.Fork != nil && info.Fork.At > 0 {
		f.seen, f.baselined = info.Fork.At, true
	}
	if info.kind() == workerConversation {
		f.baselined = true
		f.launched = time.Time{}
	}
	return nil
}

func (f *serverFeed) watch(ctx context.Context, live bool) error {
	window := map[string]any{"min": serverEntriesPerCall / 2, "max": serverEntriesPerCall}
	if f.seen > 0 {
		window["from"] = f.seen + 1
	}
	params := map[string]any{"conversation": f.conversation, "window": window}
	first := true
	return f.c.Stream(ctx, "conversation/watch", params, func(raw json.RawMessage) {
		var item serverViewItem
		if json.Unmarshal(raw, &item) != nil {
			return
		}
		fromLive := live || !first
		first = false
		f.events.run(f.p.c, func() {
			if ctx.Err() == nil {
				f.apply(ctx, item, fromLive)
			}
		})
	})
}

func (f *serverFeed) apply(ctx context.Context, item serverViewItem, live bool) {
	if _, open := f.p.c.host.Sessions().Evidence(f.session); !open {
		f.cancel()
		return
	}
	var appended []transcript.ServerEntry
	before := int64(0)
	switch item.Tag {
	case "view":
		appended, before = f.takeView(item.View)
	case "change":
		for _, raw := range item.Ops {
			entries, after := f.applyOp(raw)
			appended = append(appended, entries...)
			before = max(before, after)
		}
	default:
		return
	}
	seen := f.seen
	if before > f.seen {
		f.catchUp(ctx, before, live)
	}
	for _, entry := range appended {
		f.observe(ctx, entry, live)
	}
	if !f.baselined {
		f.baselined = true
		if f.sawHistory {
			f.baseline = f.usageTotals()
			if encoded, err := json.Marshal(f.baseline); err == nil {
				if f.subagent {
					f.p.c.host.Records().SaveSubagentUsageBaseline(f.session, f.conversation, string(encoded))
				} else {
					f.p.c.host.Records().SaveUsageBaseline(f.session, string(encoded))
				}
			}
		}
		if f.subagent {
			pausepoint.At(pausepoint.ServerSubagentFeedBaselined)
		} else {
			pausepoint.At(pausepoint.ServerFeedBaselined)
		}
	}
	if f.usageChanged {
		f.usageChanged = false
		f.reportUsage()
	}
	if !f.subagent && f.compactionsChanged {
		f.compactionsChanged = false
		f.reportCompactions()
	}
	if f.seen != seen {
		if f.subagent {
			f.p.c.host.Records().SaveSubagentFeedEntry(f.session, f.conversation, f.seen)
		} else {
			f.p.c.host.Records().SaveFeedEntry(f.session, f.seen)
		}
	}
}

func (f *serverFeed) takeView(raw json.RawMessage) ([]transcript.ServerEntry, int64) {
	var view serverView
	if json.Unmarshal(raw, &view) != nil {
		return nil, 0
	}
	f.takeDocs(view.Docs)
	return view.Entries, view.Before
}

func (f *serverFeed) takeDocs(docs map[string]json.RawMessage) {
	f.usage = decodeJSON(docs["pi.usage"])
	var live struct {
		Compactions json.RawMessage `json:"compactions"`
	}
	_ = json.Unmarshal(docs["pi.live"], &live)
	f.compactions = decodeJSON(live.Compactions)
	f.usageChanged, f.compactionsChanged = true, true
}

func (f *serverFeed) applyOp(raw json.RawMessage) ([]transcript.ServerEntry, int64) {
	var op []json.RawMessage
	if json.Unmarshal(raw, &op) != nil || len(op) == 0 {
		return nil, 0
	}
	var kind string
	_ = json.Unmarshal(op[0], &kind)
	if kind == "r" && len(op) == 2 {
		return f.takeView(op[1])
	}
	if len(op) < 2 {
		return nil, 0
	}
	var path []any
	if json.Unmarshal(op[1], &path) != nil || len(path) == 0 {
		return nil, 0
	}
	switch path[0] {
	case "entries":
		return insertedEntries(kind, path, op), 0
	case "before":
		var before int64
		if kind == "s" && len(op) == 3 && json.Unmarshal(op[2], &before) == nil {
			return nil, before
		}
	case "docs":
		f.applyDocOp(kind, path[1:], op[2:])
	}
	return nil, 0
}

func insertedEntries(kind string, path []any, op []json.RawMessage) []transcript.ServerEntry {
	var entries []transcript.ServerEntry
	switch {
	case kind == "p" && len(path) == 1 && len(op) == 5:
		_ = json.Unmarshal(op[4], &entries)
	case kind == "s" && len(path) == 1 && len(op) == 3:
		_ = json.Unmarshal(op[2], &entries)
	}
	return entries
}

func (f *serverFeed) applyDocOp(kind string, path []any, args []json.RawMessage) {
	if len(path) == 0 {
		if kind == "s" && len(args) == 1 {
			var docs map[string]json.RawMessage
			_ = json.Unmarshal(args[0], &docs)
			f.takeDocs(docs)
		}
		return
	}
	switch {
	case path[0] == "pi.usage":
		f.usage = applyJSONOp(f.usage, kind, path[1:], args)
		f.usageChanged = true
	case path[0] == "pi.live" && len(path) == 1:
		f.compactionsChanged = true
		if kind == "d" {
			f.compactions = nil
		} else if len(args) == 1 {
			var live struct {
				Compactions json.RawMessage `json:"compactions"`
			}
			_ = json.Unmarshal(args[0], &live)
			f.compactions = decodeJSON(live.Compactions)
		}
	case path[0] == "pi.live" && path[1] == "compactions":
		f.compactions = applyJSONOp(f.compactions, kind, path[2:], args)
		f.compactionsChanged = true
	}
}

func (f *serverFeed) catchUp(ctx context.Context, through int64, live bool) {
	params := map[string]any{"conversation": f.conversation, "from": f.seen + 1, "to": through, "order": "ascending", "limit": pageSize}
	err := pageEntries(ctx, f.c, params, func(entry transcript.ServerEntry) bool {
		f.observe(ctx, entry, live)
		return true
	})
	if err != nil {
		f.l.logf("conversation feed of %s: reading entries after %d: %v", f.conversation, f.seen, err)
	}
}

func (f *serverFeed) observe(ctx context.Context, entry transcript.ServerEntry, live bool) {
	if entry.ID <= f.seen {
		return
	}
	f.seen = entry.ID
	if _, open := f.p.c.host.Sessions().Evidence(f.session); !open {
		return
	}
	if entry.Kind == serverSubagent {
		f.p.events.run(f.p.c, func() { f.p.scheduleListScan(f.c, live) })
	}
	written, dated := entry.Written()
	if !dated {
		return
	}
	if written.Before(f.launched) {
		f.sawHistory = true
		if !f.subagent && entry.Kind == serverPrompt && !f.titledFromHistory {
			f.titledFromHistory = true
			f.p.c.host.Events().Prompt(harness.FromSession(f.session), time.Now(), harness.Prompt{Text: entry.Text(), History: true, Titles: true})
		}
		return
	}
	switch entry.Kind {
	case serverPrompt:
		if !f.subagent {
			f.p.c.host.Events().Prompt(harness.FromSession(f.session), time.Now(), harness.Prompt{Text: entry.Text(), Live: live, Titles: !f.titledFromHistory})
		}
	case "pi.assistant":
		for _, call := range entry.ToolCalls() {
			f.calls[call.ID] = serverToolCall{name: call.Name, input: call.Arguments}
		}
		if !f.subagent && strings.TrimSpace(entry.Text()) != "" {
			f.p.c.host.Events().Replied(f.session)
		}
	case "pi.tool-result":
		finished := false
		for _, result := range entry.ToolResults() {
			call, known := f.calls[result.ToolCallID]
			delete(f.calls, result.ToolCallID)
			if result.Failed {
				continue
			}
			if !known {
				call, known = f.requestBefore(ctx, entry.ID, result.ToolCallID)
			}
			if !known {
				continue
			}
			finished = true
			f.p.c.serverToolFinished(f.session, call.name, call.input, result.Content)
		}
		if finished {
			f.p.events.run(f.p.c, func() { f.p.scheduleListScan(f.c, live) })
		}
	}
}

const (
	serverPrompt   = "sigma.user"
	serverSubagent = "sigma.subagent"
)

func (f *serverFeed) requestBefore(ctx context.Context, result int64, id string) (serverToolCall, bool) {
	var request serverToolCall
	found := false
	params := map[string]any{"conversation": f.conversation, "to": result - 1, "order": "descending", "limit": serverEntriesPerCall}
	err := pageEntries(ctx, f.c, params, func(entry transcript.ServerEntry) bool {
		if entry.Kind != "pi.assistant" {
			return true
		}
		for _, call := range entry.ToolCalls() {
			if call.ID == id {
				request, found = serverToolCall{name: call.Name, input: call.Arguments}, true
				return false
			}
		}
		return true
	})
	if err != nil {
		f.l.logf("conversation feed of %s: reading the request of tool call %s: %v", f.conversation, id, err)
	}
	return request, found
}

func (f *serverFeed) usageTotals() map[string]serverModelUsage {
	var usage struct {
		Models map[string]serverModelUsage `json:"models"`
	}
	remarshal(f.usage, &usage)
	return usage.Models
}

func (f *serverFeed) reportUsage() {
	totals := f.usageTotals()
	source := f.l.b.agent
	obsPrefix := f.l.b.agent + ":"
	if f.subagent {
		source = f.l.b.agent + ":subagent:" + f.conversation
		obsPrefix = f.l.b.agent + ":" + f.conversation + ":"
	}
	observations := make([]sessioncost.Observation, 0, len(totals))
	for key, total := range totals {
		before := f.baseline[key]
		_, model, _ := strings.Cut(key, "/")
		usage := sessioncost.Usage{
			InputTokens:                  total.Input - before.Input,
			OutputTokens:                 total.Output - before.Output,
			CacheReadInputTokens:         total.CacheRead - before.CacheRead,
			UnclassifiedCacheWriteTokens: total.CacheWrite - before.CacheWrite,
			ReportedCostUSD:              total.Cost.Total - before.Cost.Total,
		}
		if f.subagent && !usage.HasUsage() {
			continue
		}
		observations = append(observations, sessioncost.Observation{
			ObservationID: obsPrefix + key,
			Model:         model,
			Purpose:       sessioncost.PurposeAgent,
			Usage:         usage,
		})
	}
	if len(observations) > 0 {
		f.p.c.host.Events().Usage(f.session, source, !f.subagent, observations)
	}
}

func (f *serverFeed) reportCompactions() {
	var compactions []struct {
		Trigger string `json:"trigger"`
	}
	remarshal(f.compactions, &compactions)
	if active, _ := f.p.c.host.Sessions().Evidence(f.session); active.Compacting == (len(compactions) > 0) {
		return
	}
	trigger := ""
	if len(compactions) > 0 {
		trigger = compactions[0].Trigger
	}
	f.p.c.host.Events().Compacting(harness.FromSession(f.session), len(compactions) > 0, trigger)
}

func decodeJSON(raw json.RawMessage) any {
	var value any
	if len(raw) > 0 && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return nil
}

func remarshal(value any, into any) bool {
	if value == nil {
		return false
	}
	raw, err := json.Marshal(value)
	return err == nil && json.Unmarshal(raw, into) == nil
}
