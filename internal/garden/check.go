package garden

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/protocol"
)

type Check struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Shared  bool   `json:"shared,omitempty"`
	Removed bool   `json:"removed,omitempty"`
}

type CheckResult struct {
	Report  *CheckReport  `json:"report,omitempty"`
	Verdict *CheckVerdict `json:"verdict,omitempty"`
}

type CheckReport struct {
	Summary   string             `json:"summary"`
	Evidence  []CheckEvidence    `json:"evidence"`
	Commit    string             `json:"commit,omitempty"`
	Detail    bool               `json:"detail,omitempty"`
	BySession protocol.SessionID `json:"by_session,omitempty"`
	ByMember  string             `json:"by_member,omitempty"`
	At        string             `json:"at"`
}

type CheckEvidence struct {
	Artifact   string `json:"artifact,omitempty"`
	URL        string `json:"url,omitempty"`
	Size       int64  `json:"size,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

type CheckVerdict struct {
	Confirmed bool               `json:"confirmed"`
	Reason    string             `json:"reason,omitempty"`
	BySession protocol.SessionID `json:"by_session,omitempty"`
	ByMember  string             `json:"by_member,omitempty"`
	At        string             `json:"at"`
}

type CheckVerb string

const (
	CheckVerbAdd     CheckVerb = "add"
	CheckVerbEdit    CheckVerb = "edit"
	CheckVerbRemove  CheckVerb = "remove"
	CheckVerbRestore CheckVerb = "restore"
	CheckVerbReport  CheckVerb = "report"
	CheckVerbConfirm CheckVerb = "confirm"
	CheckVerbReject  CheckVerb = "reject"
	CheckVerbUncheck CheckVerb = "uncheck"
	CheckVerbReopen  CheckVerb = "reopen"
)

var CheckVerbs = []CheckVerb{
	CheckVerbAdd, CheckVerbEdit, CheckVerbRemove, CheckVerbRestore,
	CheckVerbReport, CheckVerbConfirm, CheckVerbReject, CheckVerbUncheck, CheckVerbReopen,
}

func ParseCheckVerb(raw string) (CheckVerb, error) {
	verb := CheckVerb(strings.TrimSpace(strings.ToLower(raw)))
	if slices.Contains(CheckVerbs, verb) {
		return verb, nil
	}
	names := make([]string, len(CheckVerbs))
	for i, v := range CheckVerbs {
		names[i] = string(v)
	}
	return "", fmt.Errorf("%q is not something a check does; the moves are %s", raw, strings.Join(names, ", "))
}

type CheckMove struct {
	Verb     CheckVerb
	CheckID  string
	Text     string
	Shared   bool
	Summary  string
	Evidence []string
	Commit   string
	Actor    Tender
}

type CheckState string

const (
	CheckOpen      CheckState = "open"
	CheckReported  CheckState = "reported"
	CheckConfirmed CheckState = "confirmed"
	CheckRejected  CheckState = "rejected"
)

func (r CheckResult) State() CheckState {
	switch {
	case r.Verdict != nil && r.Verdict.Confirmed:
		return CheckConfirmed
	case r.Verdict != nil:
		return CheckRejected
	case r.Report != nil:
		return CheckReported
	}
	return CheckOpen
}

type EffectiveCheck struct {
	Check
	From   string
	Result CheckResult
}

type SharedCheckRollup struct {
	Check
	Seeds []RollupEntry
}

type RollupEntry struct {
	SeedID    string
	State     CheckState
	HasReport bool
}

type CheckTally struct {
	Total     int
	Reported  int
	Confirmed int
	Rejected  int
}

func (t *CheckTally) Add(state CheckState) {
	t.Total++
	switch state {
	case CheckReported:
		t.Reported++
	case CheckConfirmed:
		t.Confirmed++
	case CheckRejected:
		t.Rejected++
	}
}

func (t CheckTally) Open() int { return t.Total - t.Reported - t.Confirmed - t.Rejected }

const (
	MaxChecksPerSeed      = 200
	MaxCheckTextChars     = 2000
	MaxCheckSummaryChars  = 400
	MaxCheckEvidenceItems = 100
)

func ValidateCheckID(id string) error {
	digits, ok := strings.CutPrefix(id, "C")
	if !ok || digits == "" || strings.HasPrefix(digits, "0") {
		return fmt.Errorf("%q is not a check id: a check id is C and a number, like C1", id)
	}
	if _, err := strconv.ParseUint(digits, 10, 32); err != nil {
		return fmt.Errorf("%q is not a check id: a check id is C and a number, like C1", id)
	}
	return nil
}

func validateCheckText(id, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%s needs a statement: what this seed must prove", id)
	}
	if n := utf8.RuneCountInString(text); n > MaxCheckTextChars {
		return fmt.Errorf("max_check_text_chars=%d, and %s's statement is %d; a check is one statement, the detail goes in the body", MaxCheckTextChars, id, n)
	}
	return nil
}

func validateCheckLine(id, field, text string) error {
	if n := utf8.RuneCountInString(text); n > MaxCheckSummaryChars {
		return fmt.Errorf("max_check_%s_chars=%d, and this %s for %s is %d; the long version goes in a note (`attn seed note`)", field, MaxCheckSummaryChars, field, id, n)
	}
	return nil
}

type ReportText struct{ Summary, Detail string }

func ParseReportText(text string) (ReportText, error) {
	if n := len(text); n > MaxNoteBytes {
		return ReportText{}, fmt.Errorf("max_note_bytes=%d, and this report's -m is %d bytes; the detail says what a reviewer needs to judge the check, and long output goes in an --evidence file", MaxNoteBytes, n)
	}
	summary, detail, _ := strings.Cut(strings.TrimSpace(text), "\n")
	summary = strings.TrimSpace(summary)
	if n := utf8.RuneCountInString(summary); n > MaxCheckSummaryChars {
		return ReportText{}, fmt.Errorf("max_check_summary_chars=%d, and this report's first line is %d; the first line of -m is the summary `show` prints, and the lines after it are detail kept in the report's check note", MaxCheckSummaryChars, n)
	}
	return ReportText{Summary: summary, Detail: strings.TrimSpace(detail)}, nil
}

func JoinProblems(problems []error) error {
	switch len(problems) {
	case 0:
		return nil
	case 1:
		return problems[0]
	}
	lines := make([]string, len(problems))
	for i, problem := range problems {
		lines[i] = "  - " + problem.Error()
	}
	return fmt.Errorf("%d problems; fix each and run the same command again:\n%s", len(problems), strings.Join(lines, "\n"))
}

func ValidateCheckDefinitions(checks []Check) error {
	if n := len(checks); n > MaxChecksPerSeed {
		return fmt.Errorf("max_checks_per_seed=%d, asked for %d", MaxChecksPerSeed, n)
	}
	seen := map[string]bool{}
	for _, check := range checks {
		if err := ValidateCheckID(check.ID); err != nil {
			return err
		}
		if seen[check.ID] {
			return fmt.Errorf("%s is defined twice; one id names one statement", check.ID)
		}
		seen[check.ID] = true
		if err := validateCheckText(check.ID, check.Text); err != nil {
			return err
		}
	}
	return nil
}

func (t PlotTree) leaf(id string) bool { return len(t.children[id]) == 0 }

func (t PlotTree) ancestors(id string) []Seed {
	out := []Seed{}
	seen := map[string]bool{id: true}
	seed, ok := t.byID[id]
	for ok {
		parent, hasParent := ParentOf(seed)
		if !hasParent || seen[parent] {
			break
		}
		seen[parent] = true
		seed, ok = t.byID[parent]
		if ok {
			out = append(out, seed)
		}
	}
	return out
}

func (t PlotTree) root(id string) string {
	ancestors := t.ancestors(id)
	if len(ancestors) == 0 {
		return id
	}
	return ancestors[len(ancestors)-1].ID
}

func sharedResultKey(plotID, checkID string) string { return plotID + "/" + checkID }

func (t PlotTree) EffectiveChecks(id string) []EffectiveCheck {
	seed, ok := t.byID[id]
	if !ok {
		return nil
	}
	var out []EffectiveCheck
	for _, check := range seed.Checks {
		if !check.Shared {
			out = append(out, EffectiveCheck{Check: check, Result: seed.CheckResults[check.ID]})
		}
	}
	if !t.leaf(id) {
		return out
	}
	for _, plot := range t.ancestors(id) {
		for _, check := range plot.Checks {
			if check.Shared {
				out = append(out, EffectiveCheck{Check: check, From: plot.ID, Result: seed.CheckResults[sharedResultKey(plot.ID, check.ID)]})
			}
		}
	}
	return out
}

func (t PlotTree) SharedCheckRollups(id string) []SharedCheckRollup {
	seed, ok := t.byID[id]
	if !ok {
		return nil
	}
	var leaves []Seed
	var out []SharedCheckRollup
	for _, check := range seed.Checks {
		if !check.Shared {
			continue
		}
		if leaves == nil {
			for _, member := range t.InPlot(id) {
				if member.ID != id && t.leaf(member.ID) {
					leaves = append(leaves, member)
				}
			}
		}
		rollup := SharedCheckRollup{Check: check, Seeds: make([]RollupEntry, 0, len(leaves))}
		for _, leaf := range leaves {
			result := leaf.CheckResults[sharedResultKey(id, check.ID)]
			rollup.Seeds = append(rollup.Seeds, RollupEntry{SeedID: leaf.ID, State: result.State(), HasReport: result.Report != nil})
		}
		out = append(out, rollup)
	}
	return out
}

func (t PlotTree) CheckTally(crownID string) CheckTally {
	var tally CheckTally
	count := func(id string) {
		for _, check := range t.EffectiveChecks(id) {
			if !check.Removed {
				tally.Add(check.Result.State())
			}
		}
	}
	count(crownID)
	for _, seed := range t.descendants(crownID) {
		count(seed.ID)
	}
	return tally
}

func (t PlotTree) UsedCheckID(plotID string, checks []Check) (checkID, seedID string, used bool) {
	owners := map[string]string{}
	for _, seed := range t.InPlot(t.root(plotID)) {
		for _, check := range seed.Checks {
			owners[check.ID] = seed.ID
		}
	}
	for _, check := range checks {
		if owner, ok := owners[check.ID]; ok {
			return check.ID, owner, true
		}
	}
	return "", "", false
}

func (t PlotTree) NextCheckID(seedID string) string {
	return checkIDNumbered(highestCheckNumber(t.checksUnder(t.root(seedID))) + 1)
}

func (t PlotTree) NumberChecks(plotID string, checks []Check) []Check {
	next := max(highestCheckNumber(t.checksUnder(t.root(plotID))), highestCheckNumber(checks)) + 1
	out := slices.Clone(checks)
	for i := range out {
		if out[i].ID == "" {
			out[i].ID = checkIDNumbered(next)
			next++
		}
	}
	return out
}

func highestCheckNumber(checks []Check) uint64 {
	var highest uint64
	for _, check := range checks {
		if n, err := strconv.ParseUint(strings.TrimPrefix(check.ID, "C"), 10, 32); err == nil {
			highest = max(highest, n)
		}
	}
	return highest
}

func checkIDNumbered(n uint64) string { return "C" + strconv.FormatUint(n, 10) }

func usedCheckIDRefusal(checkID, owner, nextID string) error {
	return fmt.Errorf("%s is already used by %s in this plot, and one id names one statement in a plot tree; the next free id is %s, or leave the id out and attn picks it", checkID, owner, nextID)
}

func (t PlotTree) RefuseUsedCheckIDs(plotID string, checks []Check) error {
	if checkID, owner, used := t.UsedCheckID(plotID, checks); used {
		return usedCheckIDRefusal(checkID, owner, t.NextCheckID(plotID))
	}
	return nil
}

func (t PlotTree) checksUnder(id string) []Check {
	var out []Check
	for _, seed := range t.InPlot(id) {
		out = append(out, seed.Checks...)
	}
	return out
}

func ParseCheckEvidence(raw string, artifacts []CheckEvidence) (CheckEvidence, error) {
	raw = strings.TrimSpace(raw)
	names := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Artifact == raw {
			return artifact, nil
		}
		names = append(names, artifact.Artifact)
	}
	if parsed, err := url.Parse(raw); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
		return CheckEvidence{URL: raw}, nil
	}
	inventory := "it has none yet"
	if len(names) > 0 {
		inventory = "its artifacts are " + strings.Join(names, ", ")
	}
	return CheckEvidence{}, fmt.Errorf("evidence %q is not a local file, an artifact on this seed, or an http(s) URL; %s", raw, inventory)
}

func ApplyCheckMove(tree PlotTree, id string, move CheckMove, artifacts []CheckEvidence, now time.Time) (Seed, Note, error) {
	seed, ok := tree.byID[id]
	if !ok {
		return Seed{}, Note{}, fmt.Errorf("no seed %s is planted here; `attn seed ls` lists the garden", id)
	}
	checkID := strings.TrimSpace(move.CheckID)
	if err := ValidateCheckID(checkID); err != nil {
		return Seed{}, Note{}, err
	}
	next := seed
	next.Checks = slices.Clone(seed.Checks)
	next.CheckResults = make(map[string]CheckResult, len(seed.CheckResults)+1)
	for k, v := range seed.CheckResults {
		next.CheckResults[k] = v
	}
	at := now.UTC().Format(time.RFC3339)
	var headline, detail string

	switch move.Verb {
	case CheckVerbAdd, CheckVerbEdit, CheckVerbRemove, CheckVerbRestore:
		var err error
		headline, detail, err = applyDefinitionMove(tree, &next, checkID, move)
		if err != nil {
			return Seed{}, Note{}, err
		}
	default:
		effective := tree.EffectiveChecks(id)
		index := slices.IndexFunc(effective, func(c EffectiveCheck) bool { return c.ID == checkID && !c.Removed })
		if index < 0 {
			missing := notEffective(seed.ID, checkID, effective)
			if move.Verb == CheckVerbReport {
				_, _, problems := buildCheckReport(seed.ID, checkID, move, artifacts, at)
				missing = JoinProblems(append([]error{missing}, problems...))
			}
			return Seed{}, Note{}, missing
		}
		result, key := effective[index].Result, checkID
		if from := effective[index].From; from != "" {
			key = sharedResultKey(from, checkID)
		}
		var err error
		headline, detail, result, err = applyResultMove(seed.ID, checkID, result, move, artifacts, at)
		if err != nil {
			return Seed{}, Note{}, err
		}
		if result.Report == nil && result.Verdict == nil {
			delete(next.CheckResults, key)
		} else {
			next.CheckResults[key] = result
		}
	}
	if len(next.CheckResults) == 0 {
		next.CheckResults = nil
	}
	body := headline
	if detail != "" {
		body += checkNoteSeparator + detail
	}
	if err := ValidateNote(body); err != nil {
		return Seed{}, Note{}, err
	}
	note := Note{
		Seed: seed.ID, Kind: NoteKindCheck, Body: body,
		AuthorSession: protocol.TrimID(move.Actor.Session), AuthorMember: strings.TrimSpace(move.Actor.Member),
	}
	return next, note, nil
}

const checkNoteSeparator = ": "

func CheckNoteHeadline(body string) string {
	headline, _, _ := strings.Cut(body, checkNoteSeparator)
	move, _, _ := strings.Cut(headline, ", ")
	return move
}

func applyDefinitionMove(tree PlotTree, next *Seed, checkID string, move CheckMove) (string, string, error) {
	text := strings.TrimSpace(move.Text)
	index := slices.IndexFunc(next.Checks, func(c Check) bool { return c.ID == checkID })
	switch move.Verb {
	case CheckVerbAdd:
		if index >= 0 {
			if next.Checks[index].Removed {
				return "", "", fmt.Errorf("%s was removed from %s, and a removed id is never reused so what cites it keeps its meaning; bring it back with: attn seed check restore %s %s, or leave the id out and attn picks the next free one, %s", checkID, next.ID, next.ID, checkID, tree.NextCheckID(next.ID))
			}
			return "", "", fmt.Errorf("%s already defines %s; change its statement with: attn seed check edit %s %s -m \"…\", or leave the id out and attn picks the next free one, %s", next.ID, checkID, next.ID, checkID, tree.NextCheckID(next.ID))
		}
		if err := tree.RefuseUsedCheckIDs(next.ID, []Check{{ID: checkID}}); err != nil {
			return "", "", err
		}
		if err := validateCheckText(checkID, text); err != nil {
			return "", "", err
		}
		if n := len(next.Checks) + 1; n > MaxChecksPerSeed {
			return "", "", fmt.Errorf("max_checks_per_seed=%d, and this would make %d on %s", MaxChecksPerSeed, n, next.ID)
		}
		next.Checks = append(next.Checks, Check{ID: checkID, Text: text, Shared: move.Shared})
		if move.Shared {
			return checkID + " added, shared by every seed below", text, nil
		}
		return checkID + " added", text, nil
	}
	if index < 0 {
		return "", "", notDefined(next.ID, checkID, next.Checks)
	}
	check := next.Checks[index]
	switch move.Verb {
	case CheckVerbEdit:
		if check.Removed {
			return "", "", fmt.Errorf("%s is removed from %s; restore it before editing it: attn seed check restore %s %s", checkID, next.ID, next.ID, checkID)
		}
		if err := validateCheckText(checkID, text); err != nil {
			return "", "", err
		}
		next.Checks[index].Text = text
		return checkID + " edited", fmt.Sprintf("%s (was: %s; any result answered the old wording)", text, check.Text), nil
	case CheckVerbRemove:
		if check.Removed {
			return "", "", fmt.Errorf("%s is already removed from %s; `attn seed check restore %s %s` brings it back", checkID, next.ID, next.ID, checkID)
		}
		next.Checks[index].Removed = true
		return checkID + " removed", "", nil
	case CheckVerbRestore:
		if !check.Removed {
			return "", "", fmt.Errorf("%s is not removed from %s, so there is nothing to restore", checkID, next.ID)
		}
		next.Checks[index].Removed = false
		return checkID + " restored", "", nil
	}
	return "", "", fmt.Errorf("%q is not a definition move", move.Verb)
}

func applyResultMove(seedID, checkID string, result CheckResult, move CheckMove, artifacts []CheckEvidence, at string) (string, string, CheckResult, error) {
	summary := strings.TrimSpace(move.Summary)
	actorSession := protocol.TrimID(move.Actor.Session)
	actorMember := strings.TrimSpace(move.Actor.Member)
	state := result.State()
	switch move.Verb {
	case CheckVerbReport:
		report, detail, problems := buildCheckReport(seedID, checkID, move, artifacts, at)
		if err := JoinProblems(problems); err != nil {
			return "", "", result, err
		}
		return checkID + " reported", detail, CheckResult{Report: report}, nil
	case CheckVerbConfirm, CheckVerbReject:
		confirmed := move.Verb == CheckVerbConfirm
		if !confirmed && summary == "" {
			return "", "", result, fmt.Errorf("a rejection says what would prove %s: attn seed check reject %s %s -m \"<what would prove it>\"", checkID, seedID, checkID)
		}
		if err := validateCheckLine(checkID, "reason", summary); err != nil {
			return "", "", result, err
		}
		result.Verdict = &CheckVerdict{Confirmed: confirmed, Reason: summary, BySession: actorSession, ByMember: actorMember, At: at}
		headline := checkID + " " + string(result.State())
		if result.Report == nil {
			headline += ", no report"
		}
		return headline, summary, result, nil
	case CheckVerbUncheck:
		if result.Verdict == nil {
			return "", "", result, fmt.Errorf("%s on %s is %s and has no verdict to clear", checkID, seedID, state)
		}
		result.Verdict = nil
		return checkID + " unchecked", summary, result, nil
	case CheckVerbReopen:
		if state == CheckOpen {
			return "", "", result, fmt.Errorf("%s on %s is already open: it has no report and no verdict", checkID, seedID)
		}
		return checkID + " reopened", summary, CheckResult{}, nil
	}
	return "", "", result, fmt.Errorf("%q is not a result move", move.Verb)
}

func buildCheckReport(seedID, checkID string, move CheckMove, artifacts []CheckEvidence, at string) (*CheckReport, string, []error) {
	var problems []error
	var text ReportText
	if strings.TrimSpace(move.Summary) == "" {
		problems = append(problems, fmt.Errorf("a report says in a line what proves %s: attn seed check report %s %s -m \"<summary line>\" --evidence <file|artifact|url>", checkID, seedID, checkID))
	} else if parsed, err := ParseReportText(move.Summary); err != nil {
		problems = append(problems, err)
	} else {
		text = parsed
	}
	if n := len(move.Evidence); n > MaxCheckEvidenceItems {
		problems = append(problems, fmt.Errorf("max_check_evidence_items=%d, asked for %d", MaxCheckEvidenceItems, n))
	}
	evidence := make([]CheckEvidence, 0, len(move.Evidence))
	for _, cited := range move.Evidence {
		parsed, err := ParseCheckEvidence(cited, artifacts)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		evidence = append(evidence, parsed)
	}
	if len(problems) > 0 {
		return nil, "", problems
	}
	report := &CheckReport{
		Summary: text.Summary, Evidence: evidence, Commit: strings.TrimSpace(move.Commit), Detail: text.Detail != "",
		BySession: protocol.TrimID(move.Actor.Session), ByMember: strings.TrimSpace(move.Actor.Member), At: at,
	}
	note := text.Summary
	if cited := evidenceLabels(evidence); cited != "" {
		note += " · " + cited
	}
	if report.Commit != "" {
		note += " · @" + shortCommit(report.Commit)
	}
	if text.Detail != "" {
		note += "\n\n" + text.Detail
	}
	return report, note, nil
}

func evidenceLabels(evidence []CheckEvidence) string {
	labels := make([]string, 0, len(evidence))
	for _, e := range evidence {
		labels = append(labels, e.Artifact+e.URL)
	}
	return strings.Join(labels, ", ")
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func notEffective(seedID, checkID string, effective []EffectiveCheck) error {
	ids := []string{}
	for _, check := range effective {
		if !check.Removed {
			ids = append(ids, check.ID)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("%s has no check %s; it has no checks at all. Define one with: attn seed check add %s -m \"<statement>\"", seedID, checkID, seedID)
	}
	return fmt.Errorf("%s has no check %s; its checks are %s", seedID, checkID, strings.Join(ids, ", "))
}

func notDefined(seedID, checkID string, checks []Check) error {
	ids := make([]string, 0, len(checks))
	for _, check := range checks {
		ids = append(ids, check.ID)
	}
	if len(ids) == 0 {
		return fmt.Errorf("%s defines no check %s; it defines none. An inherited check is changed on the plot that shares it", seedID, checkID)
	}
	return fmt.Errorf("%s defines no check %s; it defines %s. An inherited check is changed on the plot that shares it", seedID, checkID, strings.Join(ids, ", "))
}
