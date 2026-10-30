package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ", ") }

func (r *repeatedFlag) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func runSeedCheck(args []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		newSeedFlags("check").parse("check", args)
		seedUsageError("check", fmt.Errorf("needs a move: %s", checkVerbList()))
	}
	verb, err := garden.ParseCheckVerb(args[0])
	if err != nil {
		seedUsageError("check", err)
	}
	name := "check " + string(verb)
	f := newSeedFlags(name)
	positionals := f.parse(name, args[1:])
	msg, err := seedCheckMessage(verb, f, positionals)
	if err != nil {
		seedUsageError(name, err)
	}
	result, err := f.client().SeedCheck(msg)
	if err != nil {
		seedFail(name, err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fprintCheckMove(os.Stdout, result, f.ring)
}

func checkVerbList() string {
	names := make([]string, len(garden.CheckVerbs))
	for i, verb := range garden.CheckVerbs {
		names[i] = string(verb)
	}
	return strings.Join(names, ", ")
}

func seedCheckMessage(verb garden.CheckVerb, f *seedFlags, positionals []string) (protocol.SeedCheckMessage, error) {
	defines := verb == garden.CheckVerbAdd || verb == garden.CheckVerbEdit
	if len(positionals) == 0 {
		return protocol.SeedCheckMessage{}, fmt.Errorf("needs the seed id")
	}
	msg := protocol.SeedCheckMessage{SeedID: positionals[0], Verb: string(verb)}
	ids := positionals[1:]
	if defines && len(ids) > 0 && garden.ValidateCheckID(strings.ToUpper(ids[len(ids)-1])) != nil {
		return protocol.SeedCheckMessage{}, fmt.Errorf("%q is not a check id; the statement goes in -m", ids[len(ids)-1])
	}
	switch {
	case len(ids) == 1:
		msg.CheckID = protocol.Ptr(strings.ToUpper(ids[0]))
	case len(ids) == 0 && verb == garden.CheckVerbAdd:
	default:
		return protocol.SeedCheckMessage{}, fmt.Errorf("needs the seed id and one check id, got %d arguments", len(positionals))
	}
	if sessionID := f.sessionID(); sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	text := strings.TrimSpace(f.text("check " + string(verb)))
	switch {
	case defines && text == "":
		return protocol.SeedCheckMessage{}, fmt.Errorf("needs the statement in -m: what this seed must prove")
	case defines:
		msg.Text = protocol.Ptr(text)
	case text != "":
		msg.Summary = protocol.Ptr(text)
	}
	if f.shared {
		msg.Shared = protocol.Ptr(true)
	}
	if verb == garden.CheckVerbReport {
		if err := addReportEvidence(&msg, f.evidence, f.commit); err != nil {
			return protocol.SeedCheckMessage{}, err
		}
	}
	if member := strings.TrimSpace(f.member); member != "" {
		msg.Member = protocol.Ptr(member)
	}
	if f.ring {
		msg.Ring = protocol.Ptr(true)
	}
	return msg, nil
}

func addReportEvidence(msg *protocol.SeedCheckMessage, evidence []string, commit string) error {
	var problems []error
	if text := protocol.Deref(msg.Summary); text != "" {
		if _, err := garden.ParseReportText(text); err != nil {
			problems = append(problems, err)
		}
	}
	if rev := strings.TrimSpace(commit); rev != "" {
		full, err := exec.Command("git", "rev-parse", "--verify", rev+"^{commit}").Output()
		if err != nil {
			problems = append(problems, fmt.Errorf("--commit %q is not a commit in this directory's repository (git rev-parse): %w", rev, err))
		} else {
			msg.Commit = protocol.Ptr(strings.TrimSpace(string(full)))
		}
	}
	files := map[string]string{}
	for _, value := range evidence {
		value = strings.TrimSpace(value)
		path, isFile, err := localEvidenceFile(value)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !isFile {
			msg.Evidence = append(msg.Evidence, value)
			continue
		}
		name := filepath.Base(path)
		switch files[name] {
		case "":
			files[name] = path
			msg.EvidenceFiles = append(msg.EvidenceFiles, path)
		case path:
		default:
			problems = append(problems, fmt.Errorf("--evidence %s and %s are both named %s, and a seed keeps one file per name; rename one", files[name], path, name))
		}
	}
	return garden.JoinProblems(problems)
}

func localEvidenceFile(value string) (string, bool, error) {
	if cited, err := garden.ParseCheckEvidence(value, nil); err == nil && cited.URL != "" {
		return "", false, nil
	}
	info, err := os.Stat(value)
	if errors.Is(err, os.ErrNotExist) {
		if filepath.IsAbs(value) || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
			return "", false, fmt.Errorf("--evidence %s: no file at that path", value)
		}
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("--evidence %s: %w", value, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("--evidence %s is not a regular file; cite a file, an artifact on the seed, or an http(s) URL", value)
	}
	file, err := os.Open(value)
	if err != nil {
		return "", false, fmt.Errorf("--evidence %s: %w", value, err)
	}
	file.Close()
	path, err := filepath.Abs(value)
	return path, true, err
}

func fprintCheckMove(w io.Writer, result *protocol.SeedCheckResult, rang bool) {
	line := fmt.Sprintf("%s on %s", garden.CheckNoteHeadline(result.Note.Body), seedHandle(result.Seed))
	if len(result.Attached) > 0 {
		names := make([]string, 0, len(result.Attached))
		for _, attached := range result.Attached {
			name := attached.Filename
			if attached.Replaced {
				name += " (replacing the earlier file)"
			}
			names = append(names, name)
		}
		line += " · attached " + strings.Join(names, " and ")
	}
	if tally := checkTallyLong(seedOrPlotCheckTally(result.Seed)); tally != "" {
		line += " · checks " + tally
	}
	fmt.Fprintln(w, line)
	if rang {
		fprintRang(w, result.Seed.ID, result.RingRecipients)
	}
	if closedSeedStatus(result.Seed.Status) {
		fmt.Fprintf(w, "%s stays %s: a check never reopens a seed\n", result.Seed.ID, result.Seed.Status)
	}
}

func fprintRang(w io.Writer, seedID string, recipients []string) {
	if len(recipients) == 0 {
		fmt.Fprintf(w, "rang nobody: nobody else tends or watches %s\n", seedID)
		return
	}
	fmt.Fprintf(w, "rang %s\n", strings.Join(recipients, ", "))
}

func seedCheckTally(checks []protocol.SeedCheck) protocol.SeedCheckTally {
	var tally protocol.SeedCheckTally
	for _, check := range checks {
		if check.Removed {
			continue
		}
		tally.Total++
		switch garden.CheckState(check.State) {
		case garden.CheckReported:
			tally.Reported++
		case garden.CheckConfirmed:
			tally.Confirmed++
		case garden.CheckRejected:
			tally.Rejected++
		}
	}
	return tally
}

func plotCheckTally(p *protocol.SeedPlotProgress) protocol.SeedCheckTally {
	if p == nil || p.Checks == nil {
		return protocol.SeedCheckTally{}
	}
	return *p.Checks
}

func checkTallyLong(t protocol.SeedCheckTally) string {
	if t.Total == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d of %d confirmed", t.Confirmed, t.Total)}
	if t.Reported > 0 {
		parts = append(parts, fmt.Sprintf("%d to judge", t.Reported))
	}
	if t.Rejected > 0 {
		parts = append(parts, fmt.Sprintf("%d rejected", t.Rejected))
	}
	if open := t.Total - t.Confirmed - t.Reported - t.Rejected; open > 0 {
		parts = append(parts, fmt.Sprintf("%d open", open))
	}
	return strings.Join(parts, " · ")
}

func checkTallyShort(t protocol.SeedCheckTally) string {
	if t.Total == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("checks ✓%d/%d", t.Confirmed, t.Total)}
	if t.Reported > 0 {
		parts = append(parts, fmt.Sprintf("%d to judge", t.Reported))
	}
	if t.Rejected > 0 {
		parts = append(parts, fmt.Sprintf("%d rejected", t.Rejected))
	}
	return "  [" + strings.Join(parts, " · ") + "]"
}

func seedOrPlotCheckTally(seed protocol.Seed) protocol.SeedCheckTally {
	if seed.PlotProgress != nil {
		return plotCheckTally(seed.PlotProgress)
	}
	return seedCheckTally(seed.Checks)
}

func checkTallySuffix(seed protocol.Seed) string {
	return checkTallyShort(seedOrPlotCheckTally(seed))
}

func unsettledChecks(checks []protocol.SeedCheck) string {
	var parts []string
	for _, check := range checks {
		state := garden.CheckState(check.State)
		if !check.Removed && (state == garden.CheckOpen || state == garden.CheckRejected) {
			parts = append(parts, fmt.Sprintf("%s is %s.", check.ID, state))
		}
	}
	return strings.Join(parts, " ")
}

const checkWrapWidth = 80

func fprintSeedChecks(w io.Writer, seed protocol.Seed) {
	if len(seed.Checks) == 0 && len(seed.SharedChecks) == 0 {
		return
	}
	fmt.Fprintln(w)
	var removed []string
	live := 0
	for _, check := range seed.Checks {
		if check.Removed {
			removed = append(removed, check.ID)
		} else {
			live++
		}
	}
	if live > 0 {
		fmt.Fprintf(w, "checks  %s\n", checkTallyLong(seedCheckTally(seed.Checks)))
		for _, check := range seed.Checks {
			if !check.Removed {
				fprintCheck(w, check)
			}
		}
	}
	if len(seed.SharedChecks) > 0 {
		fmt.Fprintln(w, "  shared by every seed below")
		for _, shared := range seed.SharedChecks {
			if shared.Removed {
				removed = append(removed, shared.ID)
				continue
			}
			fprintSharedCheck(w, shared)
		}
	}
	if len(removed) > 0 {
		fmt.Fprintf(w, "  removed  %s — attn seed check restore %s <C#> brings one back\n", strings.Join(removed, ", "), seed.ID)
	}
	if live > 0 {
		fmt.Fprintf(w, "  report with: attn seed check report %s <C#> -m \"<summary line>\" --evidence <file|artifact|url>\n", seed.ID)
	}
}

func fprintCheck(w io.Writer, check protocol.SeedCheck) {
	status := check.State
	if check.Verdict != nil {
		status += " by " + orDash(crew.HolderName(protocol.Deref(check.Verdict.ByMember), protocol.Deref(check.Verdict.BySession)))
		if check.Report == nil {
			status += ", no report"
		}
	}
	if from := protocol.Deref(check.From); from != "" {
		status += " · shared from " + from
	}
	fmt.Fprintf(w, "  %s  %s\n", check.ID, status)
	indent := strings.Repeat(" ", len(check.ID)+4)
	fprintWrapped(w, indent, check.Text)
	if report := check.Report; report != nil {
		fprintLabeled(w, indent, "report  ", report.Summary)
		fprintLabeled(w, indent, "        ", reportProvenance(*report))
	}
	if verdict := check.Verdict; verdict != nil && protocol.Deref(verdict.Reason) != "" {
		by := orDash(crew.HolderName(protocol.Deref(verdict.ByMember), protocol.Deref(verdict.BySession)))
		fprintLabeled(w, indent, fmt.Sprintf("%-8s", check.State), fmt.Sprintf("%s · %s, %s", protocol.Deref(verdict.Reason), by, checkStamp(verdict.At)))
	}
}

func reportProvenance(report protocol.SeedCheckReport) string {
	var parts []string
	if protocol.Deref(report.Detail) {
		parts = append(parts, "detail in its note")
	}
	if cited := evidenceList(report.Evidence); cited != "" {
		parts = append(parts, cited)
	}
	if commit := protocol.Deref(report.Commit); commit != "" {
		if len(commit) > 7 {
			commit = commit[:7]
		}
		parts = append(parts, "@"+commit)
	}
	by := orDash(crew.HolderName(protocol.Deref(report.ByMember), protocol.Deref(report.BySession)))
	parts = append(parts, by+", "+checkStamp(report.At))
	return strings.Join(parts, " · ")
}

func evidenceList(evidence []protocol.SeedCheckEvidence) string {
	labels := make([]string, 0, len(evidence))
	for _, cited := range evidence {
		label := protocol.Deref(cited.Artifact) + protocol.Deref(cited.URL)
		if protocol.Deref(cited.ChangedSinceReport) {
			label += " (changed since this report)"
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}

func checkStamp(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at
	}
	return t.Local().Format("Jan 2 15:04")
}

func fprintSharedCheck(w io.Writer, shared protocol.SeedSharedCheck) {
	byState := map[string][]string{}
	confirmed := 0
	for _, entry := range shared.Seeds {
		byState[entry.State] = append(byState[entry.State], entry.SeedID)
		if entry.State == string(garden.CheckConfirmed) {
			confirmed++
		}
	}
	fmt.Fprintf(w, "  %s  %d of %d confirmed\n", shared.ID, confirmed, len(shared.Seeds))
	indent := strings.Repeat(" ", len(shared.ID)+4)
	fprintWrapped(w, indent, shared.Text)
	for _, state := range []garden.CheckState{garden.CheckConfirmed, garden.CheckReported, garden.CheckRejected, garden.CheckOpen} {
		if seeds := byState[string(state)]; len(seeds) > 0 {
			label := string(state)
			if state == garden.CheckReported {
				label = "to judge"
			}
			fprintLabeled(w, indent, fmt.Sprintf("%-10s ", label), strings.Join(seeds, " "))
		}
	}
}

func fprintLabeled(w io.Writer, indent, label, text string) {
	lines := wrapWords(text, checkWrapWidth-len(indent)-len(label))
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(w, "%s%s%s\n", indent, label, line)
		} else {
			fmt.Fprintf(w, "%s%s%s\n", indent, strings.Repeat(" ", len(label)), line)
		}
	}
}

func fprintWrapped(w io.Writer, indent, text string) {
	for _, line := range wrapWords(text, checkWrapWidth-len(indent)) {
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

func wrapWords(text string, width int) []string {
	var lines []string
	for _, paragraph := range strings.Split(strings.TrimSpace(text), "\n") {
		line := ""
		for _, word := range strings.Fields(paragraph) {
			switch {
			case line == "":
				line = word
			case len(line)+1+len(word) > width:
				lines = append(lines, line)
				line = word
			default:
				line += " " + word
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func parsePlantChecks(raw []string) []protocol.SeedCheckDefinition {
	out := make([]protocol.SeedCheckDefinition, 0, len(raw))
	for _, entry := range raw {
		check := protocol.SeedCheckDefinition{Text: strings.TrimSpace(entry)}
		if id, text, ok := strings.Cut(entry, "="); ok {
			if id = strings.ToUpper(strings.TrimSpace(id)); garden.ValidateCheckID(id) == nil {
				check = protocol.SeedCheckDefinition{ID: protocol.Ptr(id), Text: strings.TrimSpace(text)}
			}
		}
		out = append(out, check)
	}
	return out
}

func checkDefinitionsToProtocol(checks []garden.Check) []protocol.SeedCheckDefinition {
	out := make([]protocol.SeedCheckDefinition, 0, len(checks))
	for _, check := range checks {
		wire := protocol.SeedCheckDefinition{ID: protocol.Ptr(check.ID), Text: check.Text}
		if check.Shared {
			wire.Shared = protocol.Ptr(true)
		}
		out = append(out, wire)
	}
	return out
}
