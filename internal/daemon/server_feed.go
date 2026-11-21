package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/transcript"
)

type serverFeed struct {
	p            *serverConnection
	l            *serverLink
	c            *effectrpc.Client
	session      protocol.SessionID
	conversation string
	cancel       context.CancelFunc
	events       linkEvents
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

func (l *serverLink) watchesConversations() bool {
	if l == nil {
		return false
	}
	declaration := l.b.declaration.Load()
	return declaration != nil && declaration.ConversationWatch
}

func (p *serverConnection) openFeeds(c *effectrpc.Client, conversations map[string]bool, live bool) {
	for id, l := range p.sessions() {
		conversation := p.d.linkConversation(id)
		if conversation == "" || !l.watchesConversations() || (conversations != nil && !conversations[conversation]) {
			continue
		}
		if _, open := p.feeds[id]; open {
			continue
		}
		ctx, cancel := context.WithCancel(p.d.life.Context())
		stored := p.d.store.ServerFeed(id)
		f := &serverFeed{p: p, l: l, c: c, session: id, conversation: conversation, cancel: cancel, seen: stored.Entry, launched: p.d.store.SessionLaunchedAt(id), calls: map[string]serverToolCall{}}
		f.baselined = stored.UsageBaseline != "" || stored.Entry > 0
		_ = json.Unmarshal([]byte(stored.UsageBaseline), &f.baseline)
		if p.feeds == nil {
			p.feeds = make(map[protocol.SessionID]*serverFeed)
		}
		p.feeds[id] = f
		if !p.d.life.Go("serverFeed", func() { f.run(ctx, live) }) {
			cancel()
			delete(p.feeds, id)
		}
	}
}

func (p *serverConnection) closeFeed(id protocol.SessionID) {
	if f := p.feeds[id]; f != nil {
		f.cancel()
		delete(p.feeds, id)
	}
}

func (p *serverConnection) closeFeeds() {
	for id := range p.feeds {
		p.closeFeed(id)
	}
}

func (f *serverFeed) run(ctx context.Context, live bool) {
	window := map[string]any{"min": serverEntriesPerCall / 2, "max": serverEntriesPerCall}
	if f.seen > 0 {
		window["from"] = f.seen + 1
	}
	params := map[string]any{"conversation": f.conversation, "window": window}
	first := true
	err := f.c.Stream(ctx, "conversation/watch", params, func(raw json.RawMessage) {
		var item serverViewItem
		if json.Unmarshal(raw, &item) != nil {
			return
		}
		fromLive := live || !first
		first = false
		f.events.run(f.p.d, func() {
			if ctx.Err() == nil {
				f.apply(ctx, item, fromLive)
			}
		})
	})
	f.p.events.run(f.p.d, func() {
		if f.p.feeds[f.session] == f {
			delete(f.p.feeds, f.session)
		}
	})
	if err != nil && ctx.Err() == nil {
		f.l.logf("conversation feed of %s ended: %v", f.conversation, err)
	}
}

func (f *serverFeed) apply(ctx context.Context, item serverViewItem, live bool) {
	if f.p.d.sessionMonitor(f.session) == nil {
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
				f.p.d.store.SaveServerUsageBaseline(f.session, string(encoded))
			}
		}
		pausepoint.At(pausepoint.ServerFeedBaselined)
	}
	if f.usageChanged {
		f.usageChanged = false
		f.reportUsage()
	}
	if f.compactionsChanged {
		f.compactionsChanged = false
		f.reportCompactions()
	}
	if f.seen != seen {
		f.p.d.store.SaveServerFeedEntry(f.session, f.seen)
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
	params := map[string]any{"conversation": f.conversation, "from": f.seen + 1, "to": through, "order": "ascending", "limit": sessionTranscriptPageSize}
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
	monitor := f.p.d.sessionMonitor(f.session)
	if monitor == nil {
		return
	}
	if written, dated := entry.Written(); !dated || written.Before(f.launched) {
		f.sawHistory = true
		if entry.Kind == serverPrompt && !f.titledFromHistory {
			f.titledFromHistory = true
			prompt := entry.Text()
			f.p.d.life.Go("titleResumedServerSession", func() { monitor.TitleFromPrompt(prompt, true) })
		}
		return
	}
	switch entry.Kind {
	case serverPrompt:
		prompt := entry.Text()
		typed := false
		if live {
			typed = monitor.ObservePromptSubmitted(time.Now())
			f.p.d.touchSession(f.session)
		}
		if !f.titledFromHistory {
			f.p.d.life.Go("maybeGenerateSessionTitleFromPrompt", func() { monitor.TitleFromPrompt(prompt, typed) })
		}
	case "pi.assistant":
		for _, call := range entry.ToolCalls() {
			f.calls[call.ID] = serverToolCall{name: call.Name, input: call.Arguments}
		}
		if strings.TrimSpace(entry.Text()) != "" {
			f.p.d.publishFact(FactSessionAssistantWindowChanged, string(f.session), nil)
		}
	case "pi.tool-result":
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
			f.p.d.serverToolFinished(f.session, call.name, call.input, result.Content)
		}
	}
}

const serverPrompt = "sigma.user"

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
	observations := make([]sessioncost.Observation, 0, len(totals))
	for key, total := range totals {
		before := f.baseline[key]
		_, model, _ := strings.Cut(key, "/")
		observations = append(observations, sessioncost.Observation{
			ObservationID: f.l.b.agent + ":" + key,
			Model:         model,
			Purpose:       sessioncost.PurposeAgent,
			Usage: sessioncost.Usage{
				InputTokens:                  total.Input - before.Input,
				OutputTokens:                 total.Output - before.Output,
				CacheReadInputTokens:         total.CacheRead - before.CacheRead,
				UnclassifiedCacheWriteTokens: total.CacheWrite - before.CacheWrite,
				ReportedCostUSD:              total.Cost.Total - before.Cost.Total,
			},
		})
	}
	if len(observations) > 0 {
		f.p.d.sessionMonitor(f.session).ReportServerUsage(f.l.b.agent, observations)
	}
}

func (f *serverFeed) reportCompactions() {
	var compactions []struct {
		Trigger string `json:"trigger"`
	}
	remarshal(f.compactions, &compactions)
	monitor := f.p.d.sessionMonitor(f.session)
	if active, _ := monitor.Evidence(); active.Compacting == (len(compactions) > 0) {
		return
	}
	trigger := ""
	if len(compactions) > 0 {
		trigger = compactions[0].Trigger
	}
	monitor.RecordCompaction(len(compactions) > 0, trigger)
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
