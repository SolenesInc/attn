package pty

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

// Claude has changed which glyphs it animates with, so the busy rule is the SHAPE of the
// title: ✳ is the resting symbol, any other prefixed symbol is a spinner frame.

const (
	// At codex's ~10Hz every frame would evict every other kind of evidence from
	// the observation ring; 1s matches claude's natural repaint rate.
	heartbeatKeepalive = time.Second

	claimBusy         = "busy"
	claimNotBusy      = "not_busy"
	claimApproval     = "approval"
	claimUnclassified = "unclassified"
)

const (
	oscCodeTitleAndIcon = 0
	oscCodeTitle        = 2
)

type titleClassifier func(title string) (claim string, summary string, ok bool)

type harnessSignalPolicy struct {
	classifyTitle titleClassifier
}

type harnessSignalObserver struct {
	policy    harnessSignalPolicy
	scanner   oscScanner
	lastClaim string
	lastEmit  time.Time
}

func newHarnessSignalObserver(kind agentdriver.HarnessSignalKind) *harnessSignalObserver {
	switch kind {
	case agentdriver.HarnessSignalsClaude:
		return &harnessSignalObserver{policy: harnessSignalPolicy{
			classifyTitle: classifyClaudeTitle,
		}}
	case agentdriver.HarnessSignalsCodex:
		return &harnessSignalObserver{policy: harnessSignalPolicy{
			classifyTitle: classifyCodexTitle,
		}}
	default:
		return nil
	}
}

func (o *harnessSignalObserver) Observe(chunk []byte, now time.Time) []Observation {
	if o == nil || len(chunk) == 0 {
		return nil
	}
	var out []Observation
	o.scanner.Feed(chunk, func(code int, payload string) {
		switch code {
		case oscCodeTitleAndIcon, oscCodeTitle:
			if obs, ok := o.observeTitle(payload, now); ok {
				out = append(out, obs)
			}
		}
	})
	return out
}

func (o *harnessSignalObserver) observeTitle(title string, now time.Time) (Observation, bool) {
	if o.policy.classifyTitle == nil {
		return Observation{}, false
	}
	claim, summary, ok := o.policy.classifyTitle(title)
	if !ok {
		return Observation{}, false
	}
	if claim == o.lastClaim && now.Sub(o.lastEmit) < heartbeatKeepalive {
		return Observation{}, false
	}
	o.lastClaim = claim
	o.lastEmit = now
	return newObservation(SourceHeartbeat, claim, summary, now), true
}

func classifyClaudeTitle(title string) (string, string, bool) {
	first, ok := firstRune(title)
	if !ok {
		return "", "", false
	}
	switch {
	case first == '✳':
		return claimNotBusy, stripLevelGlyph(title), true
	case isStatusGlyph(first):
		return claimBusy, stripLevelGlyph(title), true
	default:
		return claimUnclassified, strings.TrimSpace(title), true
	}
}

// Measured on codex 0.145.0 through a real PTY with --ask-for-approval untrusted. codex
// has no notification escape and no approval hook, so a reword silently drops the signal.
const codexApprovalMarker = "Action Required"

// Codex has no idle glyph, so an overwritten title reads as not-busy: a live
// capture measured a competing repaint moving accuracy by 0.2pp.
func classifyCodexTitle(title string) (string, string, bool) {
	first, ok := firstRune(title)
	if !ok {
		return "", "", false
	}
	if isBrailleSpinner(first) {
		return claimBusy, stripLevelGlyph(title), true
	}
	// Codex leaves the marker words after the prompt is answered and flips
	// only the glyph — the marker alone would re-arm the approval forever.
	if strings.Contains(title, codexApprovalMarker) {
		if codexApprovalPending(title) {
			return claimApproval, codexTitleSummary(title), true
		}
		return claimNotBusy, codexTitleSummary(title), true
	}
	return claimNotBusy, stripLevelGlyph(title), true
}

// The glyph before the marker: "." while waiting, "!" once answered (measured
// on codex 0.145.0); anything else reads as still waiting.
func codexApprovalPending(title string) bool {
	prefix, _, found := strings.Cut(title, codexApprovalMarker)
	if !found {
		return false
	}
	return strings.Contains(prefix, ".")
}

func codexTitleSummary(title string) string {
	if _, rest, found := strings.Cut(title, "|"); found {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(title)
}

func stripLevelGlyph(title string) string {
	trimmed := strings.TrimSpace(title)
	first, size := utf8.DecodeRuneInString(trimmed)
	if isStatusGlyph(first) {
		return strings.TrimSpace(trimmed[size:])
	}
	return trimmed
}

func isBrailleSpinner(r rune) bool {
	return r >= 0x2800 && r <= 0x28FF
}

func isStatusGlyph(r rune) bool {
	return unicode.IsSymbol(r)
}

func firstRune(s string) (rune, bool) {
	trimmed := strings.TrimLeft(s, " \t")
	if trimmed == "" {
		return 0, false
	}
	r, size := utf8.DecodeRuneInString(trimmed)
	if r == utf8.RuneError && size <= 1 {
		return 0, false
	}
	return r, true
}
