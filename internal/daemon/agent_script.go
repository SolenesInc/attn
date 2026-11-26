package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/catalog"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/mcpserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/script"
	"github.com/victorarias/attn/internal/stepclock"
)

const (
	scriptCommandLimit = 1000             // Reading and noting each child of a plot with 96 children is 192 commands.
	scriptCallLimit    = 50 * time.Second // The entry attn gives Claude Code times out at 60 s, and two delegate calls of 20 s fit.
	scriptCallGrace    = 2 * time.Second  // 200 scripts ended within 2 ms of their last answer on ai-sandbox.

	scriptCallTimer stepclock.Name = "agent-script-call"
	scriptKillTimer stepclock.Name = "agent-script-kill"
)

const (
	scriptLedgerChars  = 58_000 // 192 notes of 190 characters, each after a read, took 41,294. attn's lines take their room before the script's text, so the ledger, with one more line of at most ledgerArgsChars and ledgerReceiptChars, stays within script.ResultText.
	ledgerStringChars  = 120    // The notes a script cut out of other text wrongly were 44 to 60.
	ledgerArgsChars    = 1_000  // The longest of 131 changing calls in the stored bench runs, a plot, wrote 719.
	ledgerReceiptChars = 20_000 // A plot of 96 children answered 12,940.
)

const (
	codeActs         = "acts"
	codeCommandLimit = "command_limit"
	codeChangeLimit  = "change_limit"
	codeCallLimit    = "call_limit"
	codeHomeLost     = "home_lost"
)

var errScriptCallLimit = fmt.Errorf("one call runs at most %d s", int(scriptCallLimit.Seconds()))

type scriptTool struct {
	name string
	most catalog.Effect
}

var scriptToolEffects = map[string]catalog.Effect{doToolName: catalog.EffectAct, recordToolName: catalog.EffectRecord}

type scriptRun struct {
	d           *Daemon
	call        *agentCall
	tool        scriptTool
	whole       context.Context
	working     context.Context
	ran         int
	limit       string
	limitStops  string
	results     []string
	ledger      []string
	ledgerChars int
	readOnce    []readOnce
}

type readOnce struct {
	call   string
	result string
}

func (d *Daemon) runAgentScript(ctx context.Context, call *agentCall, tool scriptTool, source string) mcpserver.Result {
	whole, kill := context.WithCancelCause(ctx)
	defer kill(nil)
	working, end := context.WithCancelCause(whole)
	defer end(nil)
	endTimer := d.clock.AfterFunc(scriptCallTimer, scriptCallLimit, func() { end(errScriptCallLimit) })
	defer endTimer.Stop()
	killTimer := d.clock.AfterFunc(scriptKillTimer, scriptCallLimit+scriptCallGrace, func() { kill(errScriptCallLimit) })
	defer killTimer.Stop()

	started := time.Now()
	run := &scriptRun{d: d, call: call, tool: tool, whole: whole, working: working}
	out := script.Run(whole, script.Job{Source: source, Functions: d.agentToolSet.commandNames, Limits: script.DefaultLimits}, run.command)
	if context.Cause(working) == errScriptCallLimit {
		run.limit, run.limitStops = errScriptCallLimit.Error(), "no command after the limit ran"
	}
	ending := "ok"
	if failed := run.failure(out); failed != "" {
		ending, _, _ = strings.Cut(failed, "\n")
	}
	d.logf("agent tools: %s by %s (session %s) ran %d commands, %d changed something, in %s: %s", tool.name, call.who, call.caller.Session, run.ran, len(run.ledger), time.Since(started).Round(time.Millisecond), cutEnd(ending, loggedTextChars))
	return run.result(out)
}

func (run *scriptRun) command(name string, args json.RawMessage) script.Answer {
	tool := run.d.agentToolSet.tools[name]
	if refusal := run.atLimit(tool); refusal != nil {
		return refusedAnswer(name, refusal)
	}
	run.ran++
	refusal := run.refuses(tool, args)
	var result any
	var shownOnce bool
	if refusal == nil {
		result, shownOnce, refusal = run.runInTime(tool, args)
		if refusal != nil && tool.changes() && mayHaveTakenEffect(refusal) {
			run.record(ledgerCall(name, args) + " -> " + refusal.Code + ": it may have taken effect")
		}
	}
	run.d.logAgentCommand(run.call, name, refusal)
	if refusal != nil {
		return refusedAnswer(name, refusal)
	}
	answer := scriptAnswer(tool, result)
	shown := answer.Text + answer.JSON
	if tool.changes() {
		line := ledgerCall(name, args)
		if !tool.receipt.Whole() {
			line += " -> " + ledgerValue(json.RawMessage(tool.json(tool.answer(result))), ledgerReceiptChars)
			shown = ""
		}
		run.record(line)
	}
	if shownOnce {
		run.readOnce = append(run.readOnce, readOnce{call: ledgerCall(name, args), result: tool.json(result)})
	}
	if shown != "" && shown != "{}" {
		run.results = append(run.results, shown)
	}
	return answer
}

func refusedAnswer(name string, refusal *command.Refusal) script.Answer {
	return script.Answer{Refusal: &script.Refusal{Code: refusal.Code, Message: toolCatalog().ToolText(refusal.Message), Fix: toolCatalog().ToolText(refusal.Fix), Command: name}}
}

func (run *scriptRun) record(line string) {
	run.ledger = append(run.ledger, line)
	run.ledgerChars += utf8.RuneCountInString(line) + 1
}

func (run *scriptRun) atLimit(tool agentTool) *command.Refusal {
	if run.ran == scriptCommandLimit {
		run.limit, run.limitStops = fmt.Sprintf("one call runs at most %d commands", scriptCommandLimit), "no command after the limit ran"
		return &command.Refusal{Code: codeCommandLimit, Message: run.limit, Fix: "Run the rest in another call."}
	}
	if tool.changes() && run.ledgerChars > scriptLedgerChars {
		run.limit, run.limitStops = fmt.Sprintf("one call's answer shows at most %d characters of changes", scriptLedgerChars), "attn refused each change after the limit"
		return &command.Refusal{Code: codeChangeLimit, Message: run.limit, Fix: "Run the rest in another call."}
	}
	if run.working.Err() != nil {
		return &command.Refusal{Code: codeCallLimit, Message: errScriptCallLimit.Error() + ", and they have passed", Fix: "Run the rest in another call."}
	}
	return nil
}

func (run *scriptRun) refuses(tool agentTool, args json.RawMessage) *command.Refusal {
	if tool.def.Effect == catalog.EffectAct && run.tool.most != catalog.EffectAct {
		return &command.Refusal{Code: codeActs, Message: run.tool.name + " runs commands that read or record, and " + tool.def.Name + " acts", Fix: "Call it in a script for attn_do."}
	}
	return tool.withRules(checkArguments(tool.def.Name, args, tool.input))
}

func (run *scriptRun) runInTime(tool agentTool, args json.RawMessage) (any, bool, *command.Refusal) {
	type outcome struct {
		result    any
		shownOnce bool
		refusal   *command.Refusal
	}
	answered := make(chan outcome)
	var after func()
	running := run.d.life.Go("agent command "+tool.def.Name, func() {
		result, refusal := run.d.runAgentCommand(run.working, run.call.caller, tool.def.Command, args)
		var shownOnce bool
		if then, ok := result.(command.Then); ok {
			result, after, shownOnce = then.Result, then.After, then.ShownOnce
		}
		select {
		case answered <- outcome{result, shownOnce, refusal}:
		case <-run.whole.Done():
			if after != nil {
				after()
			}
		}
	})
	if !running {
		return nil, false, &command.Refusal{Code: "stopping", Message: "attn's daemon is stopping", Fix: "Call it again once the daemon is back."}
	}
	select {
	case got := <-answered:
		if after != nil {
			run.call.after = append(run.call.after, after)
		}
		if got.refusal != nil && run.working.Err() != nil {
			return nil, false, run.cutOff(tool)
		}
		return got.result, got.shownOnce, tool.withRules(got.refusal)
	case <-run.whole.Done():
		return nil, false, run.cutOff(tool)
	}
}

func (run *scriptRun) cutOff(tool agentTool) *command.Refusal {
	return &command.Refusal{Code: codeCallLimit, Message: errScriptCallLimit.Error() + ", and " + tool.def.Name + " had not ended", Fix: "It may have taken effect. Read what it changes before you call it again."}
}

func mayHaveTakenEffect(refusal *command.Refusal) bool {
	return refusal.Code == codeCallLimit || refusal.Code == codeHomeLost
}

func scriptAnswer(tool agentTool, result any) script.Answer {
	switch text := result.(type) {
	case command.Teaching:
		return script.Answer{Text: toolCatalog().TeachingToolText(string(text))}
	case string:
		return script.Answer{Text: text}
	}
	return script.Answer{JSON: tool.json(result)}
}

func (run *scriptRun) result(out script.Output) mcpserver.Result {
	body := out.Logs
	switch {
	case out.Has:
		body = append(body, out.Returned)
	case len(out.Logs) == 0 && out.Failed == "":
		body = run.results
	}
	said, saidShortened := plainBackticks(strings.Join(body, "\n"))
	threw, threwShortened := plainBackticks(out.Failed)
	failed := run.failure(out) != ""
	fence := fenceFor(said + threw)
	room := script.ResultText - 2*len(fence) - len("\n")*3 - len("\n\n")*2 - leftOutNoticeChars
	attnSays := run.attnLines(out, failed, run.unshownReads(said))
	if utf8.RuneCountInString(joinParts([]string{said, threw}, "\n\n"))+out.Dropped > room-utf8.RuneCountInString(attnSays) {
		attnSays = run.attnLines(out, failed, run.unshownReads(""))
	}
	readsRoom := room - utf8.RuneCountInString(run.attnLines(out, failed, "")) - len("\n\n") - len("…")
	if utf8.RuneCountInString(attnSays) > room {
		attnSays = run.attnLines(out, failed, cutEnd(run.shortReads(), max(0, readsRoom)))
	}
	output, leftOut := fitScriptText(said, threw, out.Dropped, saidShortened+threwShortened, room-utf8.RuneCountInString(attnSays))
	var scriptSays string
	if output != "" {
		scriptSays = fence + "\n" + output + "\n" + fence
	}
	text := joinParts([]string{joinParts([]string{scriptSays, leftOut}, "\n"), attnSays}, "\n\n")
	if text == "" {
		text = "The script ran and returned nothing."
	}
	return mcpserver.Result{IsError: failed, Content: []mcpserver.Content{mcpserver.Text(text)}}
}

func (run *scriptRun) attnLines(out script.Output, failed bool, reads string) string {
	var lines []string
	if failed {
		lines = append(lines, run.limitPassed(out), run.whatStands(out))
	}
	return joinParts(append(lines, reads, run.ledgerText()), "\n\n")
}

func (run *scriptRun) failure(out script.Output) string {
	return joinParts([]string{out.Failed, run.limitPassed(out)}, "\n")
}

func (run *scriptRun) limitPassed(out script.Output) string {
	if run.limit == "" || strings.Contains(out.Failed, run.limit) {
		return ""
	}
	return "The call reached a limit: " + run.limit + ". The script went on, and " + run.limitStops + "."
}

const longestFence = 16 // The longest run of backticks in the repository's markdown and catalog is 4.

var backtickRun = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("`(?:[\\p{Cf}\\p{Mn}]*`)*") })

func plainBackticks(text string) (string, int) {
	shortened := 0
	plain := backtickRun().ReplaceAllStringFunc(text, func(run string) string {
		if strings.Count(run, "`") < longestFence {
			return run
		}
		shortened += utf8.RuneCountInString(run) - (longestFence - 1)
		return strings.Repeat("`", longestFence-1)
	})
	return plain, shortened
}

func fenceFor(text string) string {
	longest := 0
	for _, run := range backtickRun().FindAllString(text, -1) {
		longest = max(longest, strings.Count(run, "`"))
	}
	return strings.Repeat("`", max(3, longest+1))
}

func joinParts(parts []string, separator string) string {
	var kept []string
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, separator)
}

func (run *scriptRun) whatStands(out script.Output) string {
	stands := "Nothing changed."
	switch {
	case len(run.ledger) > 0:
		stands = "The changes below stand, so do not run the script again."
	case len(run.readOnce) > 0:
		stands = "Nothing changed, but what the script read is marked read now."
	}
	if out.Line > 0 {
		return fmt.Sprintf("Nothing after line %d ran. %s", out.Line, stands)
	}
	return stands
}

func (run *scriptRun) unshownReads(said string) string {
	var lines []string
	for _, read := range run.readOnce {
		if !strings.Contains(said, read.result) {
			lines = append(lines, read.call+" -> "+read.result)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "marked read:\n" + strings.Join(lines, "\n")
}

func (run *scriptRun) shortReads() string {
	if len(run.readOnce) == 0 {
		return ""
	}
	ids := []string{"marked read; agent_inbox({message: id}) reads one message whole:"}
	said := []string{"what they hold, with long text cut:"}
	for _, read := range run.readOnce {
		ids = append(ids, read.call+" -> "+strings.Join(inboxItemIDs(read.result), ", "))
		said = append(said, read.call+" -> "+ledgerValue(json.RawMessage(read.result), math.MaxInt))
	}
	return strings.Join(ids, "\n") + "\n\n" + strings.Join(said, "\n")
}

func inboxItemIDs(result string) []string {
	var batch protocol.AgentInboxBatchResult
	_ = json.Unmarshal([]byte(result), &batch)
	var ids []string
	for _, item := range batch.Items {
		ids = append(ids, item.ItemID)
	}
	if batch.Item != nil {
		ids = append(ids, batch.Item.ItemID)
	}
	if batch.Message != nil {
		ids = append(ids, batch.Message.MessageID)
	}
	return ids
}

func (run *scriptRun) ledgerText() string {
	if len(run.ledger) == 0 {
		return ""
	}
	return "changed:\n" + strings.Join(run.ledger, "\n")
}

const (
	leftOutNotice    = "attn left out %d of %d characters the script returned, logged or threw. Return only the parts you need."
	shortenedRunNote = " attn shortens each run of backticks to %d."
)

var leftOutNoticeChars = len(fmt.Sprintf(leftOutNotice+shortenedRunNote, math.MaxInt, math.MaxInt, longestFence))

func fitScriptText(said, threw string, dropped, shortened, limit int) (string, string) {
	saidChars, threwChars := utf8.RuneCountInString(said), utf8.RuneCountInString(threw)
	total := saidChars + threwChars + dropped + shortened
	output := joinParts([]string{said, threw}, "\n\n")
	keptSaid, keptThrew := saidChars, threwChars
	if utf8.RuneCountInString(output)+dropped > limit {
		keptThrew = min(threwChars, max(0, limit))
		keptSaid = min(saidChars, max(0, limit-keptThrew-len("\n\n")))
		output = joinParts([]string{string([]rune(said)[:keptSaid]), string([]rune(threw)[:keptThrew])}, "\n\n")
	}
	leftOut := total - keptSaid - keptThrew
	if leftOut == 0 {
		return output, ""
	}
	notice := fmt.Sprintf(leftOutNotice, leftOut, total)
	if shortened > 0 {
		notice += fmt.Sprintf(shortenedRunNote, longestFence-1)
	}
	return output, notice
}

func ledgerCall(name string, args json.RawMessage) string {
	return name + " " + ledgerValue(args, ledgerArgsChars)
}

func ledgerValue(raw json.RawMessage, limit int) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out strings.Builder
	writeLedgerValue(&out, decoder)
	return cutEnd(out.String(), limit)
}

func writeLedgerValue(out *strings.Builder, decoder *json.Decoder) {
	token, err := decoder.Token()
	if err != nil {
		return
	}
	switch value := token.(type) {
	case json.Delim:
		out.WriteString(value.String())
		for item := 0; decoder.More(); item++ {
			if item > 0 {
				out.WriteString(", ")
			}
			if value == '{' {
				key, _ := decoder.Token()
				out.WriteString(ledgerKey(key.(string)) + ": ")
			}
			writeLedgerValue(out, decoder)
		}
		closing, _ := decoder.Token()
		fmt.Fprint(out, closing)
	case string:
		out.WriteString(quoted(cutEnd(value, ledgerStringChars)))
	default:
		written, _ := json.Marshal(value)
		out.Write(written)
	}
}

var identifier = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`) })

func ledgerKey(key string) string {
	if identifier().MatchString(key) {
		return key
	}
	return quoted(key)
}

func quoted(text string) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return strings.TrimSuffix(out.String(), "\n")
}

func cutEnd(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}

func commandRules(name string) (string, bool) {
	name = strings.TrimPrefix(strings.NewReplacer(" ", "_", "-", "_").Replace(name), "attn_")
	var parts, family []string
	for _, def := range agentToolsCatalog().Tools {
		if def.Name == name {
			parts = append(parts, def.rules()+"\n\n"+def.Description)
		}
		if strings.HasPrefix(def.Name, name+"_") {
			family = append(family, def.Signature+": "+def.Title)
		}
	}
	if len(family) > 0 {
		parts = append(parts, strings.Join(family, "\n"))
	}
	return strings.Join(parts, "\n\n"), len(parts) > 0
}

func (def agentToolDefinition) rules() string {
	var schema struct {
		Properties map[string]struct {
			Type        string
			Description string
			Enum        []string
			Items       struct{ Type string }
		}
	}
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		panic(fmt.Sprintf("%s: input schema: %v", def.Name, err))
	}
	lines := []string{def.Signature}
	cmd, _ := toolCatalog().Command(def.Command)
	for _, arg := range cmd.Args {
		property, taken := schema.Properties[arg.Name]
		if !taken {
			continue
		}
		line := "- " + arg.Name + ": " + property.Type
		if property.Items.Type != "" {
			line += " of " + property.Items.Type
		}
		if len(property.Enum) > 0 {
			line += ", one of " + strings.Join(property.Enum, ", ")
		}
		if property.Description != "" {
			line += ". " + property.Description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (tool agentTool) withRules(refusal *command.Refusal) *command.Refusal {
	if refusal == nil || refusal.Code != command.CodeUsage {
		return refusal
	}
	explained := *refusal
	explained.Fix = strings.TrimSpace(refusal.Fix + "\n" + tool.def.rules())
	return &explained
}

func (tool agentTool) changes() bool { return tool.def.Effect != catalog.EffectRead }
