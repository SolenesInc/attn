package workflow

import (
	"strconv"
	"strings"
)

type segKind int

const (
	// segPhase records a phase boundary as a *sequence number*, never the title:
	// a rename must not invalidate cached calls.
	segPhase segKind = iota
	segParallelSlot
	segPipelineItem
	segStage
	segCallsite
)

func (k segKind) prefix() string {
	switch k {
	case segPhase:
		return "ph"
	case segParallelSlot:
		return "ps"
	case segPipelineItem:
		return "pi"
	case segStage:
		return "st"
	case segCallsite:
		return "cs"
	default:
		return "??"
	}
}

type segment struct {
	kind  segKind
	index int
	site  string
}

func (s segment) String() string {
	if s.kind == segCallsite {
		return "cs@" + s.site + "#" + strconv.Itoa(s.index)
	}
	return s.kind.prefix() + strconv.Itoa(s.index)
}

type OrdinalPath struct {
	segs []segment
}

func (p OrdinalPath) String() string {
	if len(p.segs) == 0 {
		return ""
	}
	parts := make([]string, len(p.segs))
	for i, s := range p.segs {
		parts[i] = s.String()
	}
	return strings.Join(parts, "/")
}

func (p OrdinalPath) clone() OrdinalPath {
	cp := make([]segment, len(p.segs))
	copy(cp, p.segs)
	return OrdinalPath{segs: cp}
}

// pathStack is the engine-owned, loop-goroutine-only structural-path context,
// mutated and read synchronously. NOT stored in goja.
type pathStack struct {
	segs []segment

	callCounter map[string]int

	phaseSeq   int
	phaseTitle string
}

func newPathStack() *pathStack {
	return &pathStack{callCounter: map[string]int{}}
}

// prefix delegates to OrdinalPath.String() so the encoding has a single
// authority — a divergence silently corrupts journal cache identity.
func (ps *pathStack) prefix() string {
	return OrdinalPath{segs: ps.segs}.String()
}

type pushPop func()

func (ps *pathStack) push(kind segKind, index int) pushPop {
	savedLen := len(ps.segs)
	savedCounters := make(map[string]int, len(ps.callCounter))
	for k, v := range ps.callCounter {
		savedCounters[k] = v
	}
	ps.segs = append(ps.segs, segment{kind: kind, index: index})
	return func() {
		ps.segs = ps.segs[:savedLen]
		ps.callCounter = savedCounters
	}
}

func (ps *pathStack) replace(newSegs []segment) pushPop {
	savedSegs := ps.segs
	savedCounters := ps.callCounter
	cp := make([]segment, len(newSegs))
	copy(cp, newSegs)
	ps.segs = cp
	ps.callCounter = map[string]int{}
	return func() {
		ps.segs = savedSegs
		ps.callCounter = savedCounters
	}
}

func (ps *pathStack) snapshot() []segment {
	cp := make([]segment, len(ps.segs))
	copy(cp, ps.segs)
	return cp
}

type stackState struct {
	segs       []segment
	counters   map[string]int
	phaseSeq   int
	phaseTitle string
}

func (ps *pathStack) captureState() stackState {
	segs := make([]segment, len(ps.segs))
	copy(segs, ps.segs)
	counters := make(map[string]int, len(ps.callCounter))
	for k, v := range ps.callCounter {
		counters[k] = v
	}
	return stackState{segs: segs, counters: counters, phaseSeq: ps.phaseSeq, phaseTitle: ps.phaseTitle}
}

// restoreState deep-copies so a restored continuation cannot mutate another
// continuation's snapshot.
func (ps *pathStack) restoreState(s stackState) {
	segs := make([]segment, len(s.segs))
	copy(segs, s.segs)
	counters := make(map[string]int, len(s.counters))
	for k, v := range s.counters {
		counters[k] = v
	}
	ps.segs = segs
	ps.callCounter = counters
	ps.phaseSeq = s.phaseSeq
	ps.phaseTitle = s.phaseTitle
}

func (ps *pathStack) ordinalFor(site string) OrdinalPath {
	key := ps.prefix() + "|" + site
	counter := ps.callCounter[key]
	ps.callCounter[key] = counter + 1

	segs := make([]segment, 0, len(ps.segs)+1)
	segs = append(segs, ps.segs...)
	segs = append(segs, segment{kind: segCallsite, index: counter, site: site})
	return OrdinalPath{segs: segs}
}

func (ps *pathStack) setPhase(title string) {
	ps.phaseSeq++
	seq := ps.phaseSeq
	ps.phaseTitle = title
	if len(ps.segs) > 0 && ps.segs[0].kind == segPhase {
		ps.segs[0].index = seq
		return
	}
	ps.segs = append([]segment{{kind: segPhase, index: seq}}, ps.segs...)
}

func (ps *pathStack) currentPhase() string {
	return ps.phaseTitle
}
