package daemon

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/whatsnew"
)

// dataSetFacts is what the app holds for one profile, as the daemon sends it:
// the values today's Home and the data sets are both shaped from.
type dataSetFacts struct {
	profileID        string
	sessions         []protocol.Session
	desktops         []protocol.Desktop
	settings         map[string]string
	harnesses        []protocol.Harness
	outposts         []protocol.OutpostInfo
	crew             []protocol.CrewMember
	asks             []protocol.Ask
	seeds            []protocol.Seed
	prs              []protocol.PR
	repos            []protocol.RepoState
	authors          []protocol.AuthorState
	pollingOff       string
	rateLimitedUntil string
}

func (f *dataSetFacts) on(s *setting.Setting) bool {
	return setting.ParseBool(s.WithDefault(f.settings[s.Key]))
}

// shapeDataSet shapes the data set name from f at now, and says the next
// instant the set reads differently while nothing else changes, or the zero
// time when it never does.
func shapeDataSet(name string, f *dataSetFacts, now time.Time) (any, time.Time) {
	switch name {
	case "sessions":
		return shapeSessions(f, now)
	case "asks":
		return shapeAsks(f, now)
	case "seeds":
		return shapeSeeds(f), time.Time{}
	case "pull-requests":
		return shapePullRequests(f, now)
	case "app":
		return shapeApp(), time.Time{}
	}
	return nil, time.Time{}
}

type sessionsSet struct {
	Commands   []string     `json:"commands"`
	Empty      bool         `json:"empty"`
	AllSettled string       `json:"all_settled,omitempty"`
	Asks       []askRow     `json:"asks"`
	Turns      []sessionRow `json:"turns"`
	// Unreachable counts the owed turns on machines the home cannot reach,
	// which the count of turns that need the user leaves out.
	Unreachable int            `json:"unreachable"`
	Settled     bool           `json:"settled"`
	Groups      []sessionGroup `json:"groups"`
	Snoozed     []sessionRow   `json:"snoozed"`
}

type sessionGroup struct {
	Key   string       `json:"key"`
	Label string       `json:"label"`
	Rows  []sessionRow `json:"rows"`
}

type sessionRow struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Harness        string   `json:"harness"`
	State          string   `json:"state"`
	UnknownBecause string   `json:"unknown_because,omitempty"`
	Priority       bool     `json:"priority"`
	Offline        bool     `json:"offline"`
	Chief          bool     `json:"chief"`
	Activity       string   `json:"activity,omitempty"`
	ActivityAt     string   `json:"activity_at,omitempty"`
	ActivityStale  bool     `json:"activity_stale,omitempty"`
	TurnOpenedAt   string   `json:"turn_opened_at,omitempty"`
	SnoozedUntil   string   `json:"snoozed_until,omitempty"`
	Commands       []string `json:"commands"`
}

var sessionStateGroups = []struct {
	state protocol.SessionState
	key   string
	label string
}{
	{protocol.SessionStateWaitingInput, "waiting", "Waiting for input"},
	{protocol.SessionStatePendingApproval, "pending", "Pending approval"},
	{protocol.SessionStateLaunching, "launching", "Launching"},
	{protocol.SessionStateWorking, "working", "Working"},
	{protocol.SessionStateScheduled, "scheduled", "Scheduled"},
	{protocol.SessionStateIdle, "idle", "Idle"},
	{protocol.SessionStateRecoverable, "recoverable", "Recoverable"},
	{protocol.SessionStateUnknown, "unknown", "Unknown / error"},
}

var stillRunning = []struct {
	state protocol.SessionState
	said  string
}{
	{protocol.SessionStateWorking, "working"},
	{protocol.SessionStateLaunching, "launching"},
	{protocol.SessionStateScheduled, "scheduled"},
	{protocol.SessionStateRecoverable, "recoverable"},
}

// groupedState is the state group a session's state falls in: a state no
// group names is unknown.
func groupedState(state protocol.SessionState) protocol.SessionState {
	for _, group := range sessionStateGroups {
		if group.state == state {
			return state
		}
	}
	return protocol.SessionStateUnknown
}

type placedSession struct {
	session *protocol.Session
	desktop string
}

// placedSessions is the profile's agents in place order: each desktop's in
// desktop order, then, when the queue shows hidden sessions, the hidden ones
// on no desktop. Shells are left out.
func placedSessions(f *dataSetFacts) ([]placedSession, map[protocol.SessionID]string) {
	desktops := slices.Clone(f.desktops)
	slices.SortStableFunc(desktops, func(a, b protocol.Desktop) int { return cmp.Compare(a.OrderKey, b.OrderKey) })
	desktopOf := map[protocol.SessionID]string{}
	for _, desktop := range desktops {
		for _, pane := range desktop.Panes {
			desktopOf[pane.SessionID] = desktop.ID
		}
	}
	var placed []placedSession
	for _, desktop := range desktops {
		for i := range f.sessions {
			if desktopOf[f.sessions[i].ID] == desktop.ID {
				placed = append(placed, placedSession{session: &f.sessions[i], desktop: desktop.ID})
			}
		}
	}
	if f.on(setting.QueueModeEnabled) && f.on(setting.QueueShowHiddenSessions) {
		for i := range f.sessions {
			s := &f.sessions[i]
			if _, onDesktop := desktopOf[s.ID]; protocol.Deref(s.Hidden) && s.ProfileID == f.profileID && !onDesktop {
				placed = append(placed, placedSession{session: s})
			}
		}
	}
	placed = slices.DeleteFunc(placed, func(p placedSession) bool { return protocol.Deref(p.session.CommandTerminal) })
	return placed, desktopOf
}

type queueBands struct {
	chief   bool
	turns   []placedSession
	settled []placedSession
	snoozed []placedSession
}

// bands sorts the placed agents into the queue: automation runs, crew when
// the crew does not queue, and an agent's child on the agent's desktop take
// no band.
func bands(f *dataSetFacts, placed []placedSession, desktopOf map[protocol.SessionID]string, now time.Time) queueBands {
	crewQueues := f.on(setting.QueueCrewEnabled)
	var b queueBands
	for _, p := range placed {
		s := p.session
		b.chief = b.chief || protocol.Deref(s.ChiefOfStaff)
		if s.Automation != nil {
			continue
		}
		if protocol.Deref(s.CrewMember) != "" && !crewQueues {
			continue
		}
		if parent := s.ParentSessionID; parent != nil && p.desktop != "" && desktopOf[*parent] == p.desktop {
			continue
		}
		switch {
		case snoozedAt(s, now):
			b.snoozed = append(b.snoozed, p)
		case protocol.Deref(s.TurnOwed):
			b.turns = append(b.turns, p)
		default:
			b.settled = append(b.settled, p)
		}
	}
	slices.SortStableFunc(b.turns, func(a, c placedSession) int {
		return cmp.Or(compareBool(f.offline(a.session), f.offline(c.session)), compareTurnOrder(a.session, c.session))
	})
	return b
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}

func compareTurnOrder(a, b *protocol.Session) int {
	return cmp.Or(
		compareBool(!protocol.Deref(a.Priority), !protocol.Deref(b.Priority)),
		cmp.Compare(protocol.Deref(a.TurnOpenedAt), protocol.Deref(b.TurnOpenedAt)),
		cmp.Compare(a.ID, b.ID),
	)
}

func compareWakeOrder(a, b *protocol.Session) int {
	return cmp.Or(cmp.Compare(protocol.Deref(a.TurnSnoozedUntil), protocol.Deref(b.TurnSnoozedUntil)), cmp.Compare(a.ID, b.ID))
}

func snoozedAt(s *protocol.Session, now time.Time) bool {
	until, ok := parseInstant(protocol.Deref(s.TurnSnoozedUntil))
	return ok && until.After(now)
}

func parseInstant(instant string) (time.Time, bool) {
	at, err := time.Parse(time.RFC3339Nano, instant)
	return at, err == nil
}

// offline is whether the session runs on a machine the home cannot reach: an
// outpost no row names, or one whose reach is neither reachable nor connecting.
func (f *dataSetFacts) offline(s *protocol.Session) bool {
	host := protocol.Deref(s.Host)
	if host == "" {
		return false
	}
	for _, outpost := range f.outposts {
		if outpost.ID == host {
			return outpost.Reach != protocol.OutpostReachReachable && outpost.Reach != protocol.OutpostReachConnecting
		}
	}
	return true
}

// shapeSessions shapes the Sessions view's set. In queue flow the owed turns
// come first, oldest first, and the rest sit by state under Settled;
// otherwise every agent sits by state. Snoozed agents sit apart in either.
func shapeSessions(f *dataSetFacts, now time.Time) (sessionsSet, time.Time) {
	placed, desktopOf := placedSessions(f)
	queueFlow := f.on(setting.QueueModeEnabled)
	staleAfter := activityStaleAfter(f)
	var next time.Time
	soonest := func(at time.Time) {
		if at.After(now) && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	row := func(s *protocol.Session) sessionRow {
		r := sessionRow{
			ID:       string(s.ID),
			Name:     f.displayName(s),
			Harness:  f.harnessName(s.Agent),
			State:    string(s.State),
			Priority: protocol.Deref(s.Priority),
			Offline:  f.offline(s),
			Chief:    protocol.Deref(s.ChiefOfStaff),
			Commands: []string{"open"},
		}
		if s.State == protocol.SessionStateUnknown {
			r.UnknownBecause = unknownBecause(protocol.Deref(s.StateReason))
		}
		if line := strings.TrimSpace(protocol.Deref(s.Activity)); line != "" {
			r.Activity, r.ActivityAt = line, protocol.Deref(s.ActivityAt)
			if at, ok := parseInstant(r.ActivityAt); ok {
				stale := at.Add(staleAfter)
				r.ActivityStale = now.After(stale)
				soonest(stale.Add(time.Millisecond))
			}
		}
		if until, ok := parseInstant(protocol.Deref(s.TurnSnoozedUntil)); ok {
			soonest(until.Add(time.Millisecond))
		}
		return r
	}

	var turns, snoozed []*protocol.Session
	var b queueBands
	if queueFlow {
		b = bands(f, placed, desktopOf, now)
		for _, p := range b.turns {
			turns = append(turns, p.session)
		}
		for _, p := range b.snoozed {
			snoozed = append(snoozed, p.session)
		}
		for _, p := range placed {
			if protocol.Deref(p.session.ChiefOfStaff) {
				if !slices.Contains(snoozed, p.session) {
					snoozed = append(snoozed, p.session)
				}
				break
			}
		}
	} else {
		for _, p := range placed {
			snoozed = append(snoozed, p.session)
		}
	}
	snoozed = slices.DeleteFunc(snoozed, func(s *protocol.Session) bool { return !snoozedAt(s, now) })
	slices.SortStableFunc(snoozed, compareWakeOrder)
	var settled []*protocol.Session
	for _, p := range placed {
		if !slices.Contains(turns, p.session) && !slices.Contains(snoozed, p.session) {
			settled = append(settled, p.session)
		}
	}

	set := sessionsSet{Commands: []string{"new-session"}, Empty: len(placed) == 0, Asks: []askRow{}, Turns: []sessionRow{}, Groups: []sessionGroup{}, Snoozed: []sessionRow{}}
	for _, group := range sessionStateGroups {
		rows := []sessionRow{}
		for _, s := range settled {
			if groupedState(s.State) == group.state {
				rows = append(rows, row(s))
			}
		}
		if len(rows) > 0 {
			set.Groups = append(set.Groups, sessionGroup{Key: group.key, Label: group.label, Rows: rows})
		}
	}
	for _, s := range turns {
		r := row(s)
		r.TurnOpenedAt = protocol.Deref(s.TurnOpenedAt)
		if r.Offline {
			set.Unreachable++
		}
		set.Turns = append(set.Turns, r)
	}
	for _, s := range snoozed {
		r := row(s)
		r.SnoozedUntil = protocol.Deref(s.TurnSnoozedUntil)
		if !r.Offline {
			r.Commands = append(r.Commands, "unsnooze")
		}
		set.Snoozed = append(set.Snoozed, r)
	}
	set.Settled = queueFlow && len(set.Groups) > 0
	if queueFlow && len(b.turns) == 0 && (b.chief || len(b.settled) > 0 || len(b.snoozed) > 0) {
		set.AllSettled = stillRunningSaid(settled)
		set.Commands = append(set.Commands, "follow")
	}
	asks, asksNext := liveAsks(f, now)
	set.Asks = asks.Open
	if !asksNext.IsZero() {
		soonest(asksNext)
	}
	return set, next
}

func stillRunningSaid(settled []*protocol.Session) string {
	var counts []string
	for _, running := range stillRunning {
		n := 0
		for _, s := range settled {
			if groupedState(s.State) == running.state {
				n++
			}
		}
		if n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, running.said))
		}
	}
	if len(counts) == 0 {
		return "Nothing is running."
	}
	return strings.Join(counts, " · ")
}

func unknownBecause(reason string) string {
	switch reason {
	case "stuck":
		return "Stuck — the agent has stopped reporting anything at all"
	case "no_evidence":
		return "No signal from this agent yet"
	}
	return ""
}

// activityStaleAfter is how long an activity line reads as current: three
// times the longer of the generator's intervals.
func activityStaleAfter(f *dataSetFacts) time.Duration {
	intervals, err := parseActivityIntervals(f.settings[setting.ActivityIntervals.Key])
	if err != nil {
		intervals = activityIntervals{Watching: defaultActivityWatchingSeconds, Present: defaultActivityPresentSeconds}
	}
	seconds := max(intervals.Watching, intervals.Present, activityIntervalMinSeconds)
	return time.Duration(seconds) * 3 * time.Second
}

// displayName is the name a row shows: the session's label, its title, or its
// harness's name.
func (f *dataSetFacts) displayName(s *protocol.Session) string {
	if s.Label != "" {
		return s.Label
	}
	if title := protocol.Deref(s.Title); title != "" {
		return title
	}
	return f.harnessName(s.Agent)
}

func (f *dataSetFacts) harnessName(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return "Unknown harness"
	}
	for _, h := range f.harnesses {
		if strings.ToLower(strings.TrimSpace(h.ID)) == id {
			if name := strings.TrimSpace(h.Name); name != "" {
				return name
			}
		}
	}
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, word := range words {
		words[i] = capitalized(word)
	}
	return strings.Join(words, " ")
}

func capitalized(word string) string {
	first, size := utf8.DecodeRuneInString(word)
	if size == 0 {
		return word
	}
	return string(unicode.ToUpper(first)) + word[size:]
}

type asksSet struct {
	Commands []string `json:"commands"`
	Open     []askRow `json:"open"`
	Snoozed  []askRow `json:"snoozed"`
	Answered []askRow `json:"answered"`
}

type askRow struct {
	ID           string   `json:"id"`
	From         string   `json:"from"`
	Subject      string   `json:"subject"`
	Session      string   `json:"session,omitempty"`
	ToChief      bool     `json:"to_chief"`
	Questions    int      `json:"questions"`
	CreatedAt    string   `json:"created_at"`
	SnoozedUntil string   `json:"snoozed_until,omitempty"`
	AnsweredAt   string   `json:"answered_at,omitempty"`
	Commands     []string `json:"commands"`
}

func shapeAsks(f *dataSetFacts, now time.Time) (asksSet, time.Time) {
	return liveAsks(f, now)
}

// liveAsks lists the open asks, those of agents the user owes a turn first,
// oldest turn first; a snoozed agent's asks apart, by when they wake; and the
// asks answered in the last day, newest first.
func liveAsks(f *dataSetFacts, now time.Time) (asksSet, time.Time) {
	set := asksSet{Commands: []string{}, Open: []askRow{}, Snoozed: []askRow{}, Answered: []askRow{}}
	var next time.Time
	var open []*protocol.Ask
	for i := range f.asks {
		if f.asks[i].Reply == nil {
			open = append(open, &f.asks[i])
		}
	}
	waiting := func(a *protocol.Ask) (string, bool) {
		holder := f.askHolder(a)
		if holder == nil || !protocol.Deref(holder.TurnOwed) {
			return "", false
		}
		return cmp.Or(protocol.Deref(holder.TurnOpenedAt), a.CreatedAt), true
	}
	slices.SortStableFunc(open, func(a, b *protocol.Ask) int {
		wa, aWaits := waiting(a)
		wb, bWaits := waiting(b)
		return cmp.Or(compareBool(bWaits, aWaits), cmp.Compare(wa, wb), cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	for _, a := range open {
		row := f.askRow(a)
		if holder := f.askHolder(a); holder != nil && snoozedAt(holder, now) {
			row.SnoozedUntil = protocol.Deref(holder.TurnSnoozedUntil)
			if until, ok := parseInstant(row.SnoozedUntil); ok && (next.IsZero() || until.Before(next)) {
				next = until.Add(time.Millisecond)
			}
			set.Snoozed = append(set.Snoozed, row)
			continue
		}
		set.Open = append(set.Open, row)
	}
	slices.SortStableFunc(set.Snoozed, func(a, b askRow) int { return cmp.Compare(a.SnoozedUntil, b.SnoozedUntil) })
	var answered []*protocol.Ask
	for i := range f.asks {
		if f.asks[i].Reply != nil {
			answered = append(answered, &f.asks[i])
		}
	}
	slices.SortStableFunc(answered, func(a, b *protocol.Ask) int {
		return cmp.Or(cmp.Compare(b.Reply.AnsweredAt, a.Reply.AnsweredAt), cmp.Compare(a.ID, b.ID))
	})
	for _, a := range answered {
		row := f.askRow(a)
		row.AnsweredAt = a.Reply.AnsweredAt
		set.Answered = append(set.Answered, row)
	}
	return set, next
}

func (f *dataSetFacts) askRow(a *protocol.Ask) askRow {
	holder := f.askHolder(a)
	row := askRow{
		ID:        a.ID,
		From:      f.askFrom(a, holder),
		Subject:   a.Subject,
		ToChief:   protocol.Deref(a.Stranded) && f.chief() != nil,
		CreatedAt: a.CreatedAt,
		Commands:  []string{"open"},
	}
	if holder != nil {
		row.Session = string(holder.ID)
	}
	for _, part := range a.Parts {
		if part.Question != nil {
			row.Questions++
		}
	}
	return row
}

// askHolder is the agent an ask waits on: whoever the garden says tends its
// seed, its crew member's session, or the session that asked. A seed the
// garden does not hold is tended by the session that names it as its stake.
func (f *dataSetFacts) askHolder(a *protocol.Ask) *protocol.Session {
	for i := range f.sessions {
		if f.askBelongsTo(a, &f.sessions[i]) {
			return &f.sessions[i]
		}
	}
	return nil
}

func (f *dataSetFacts) askBelongsTo(a *protocol.Ask, s *protocol.Session) bool {
	if seedID := protocol.Deref(a.SeedID); seedID != "" {
		seed := f.seed(seedID)
		if seed == nil {
			return protocol.Deref(s.SeedID) == seedID
		}
		if seed.Tender == nil {
			return false
		}
		member := protocol.Deref(s.CrewMember)
		return (member != "" && string(seed.Tender.Ref) == "member:"+member) || protocol.Deref(seed.Tender.SessionID) == s.ID
	}
	if member := protocol.Deref(a.CrewMember); member != "" {
		return protocol.Deref(s.CrewMember) == member
	}
	return a.SessionID == s.ID
}

func (f *dataSetFacts) seed(id string) *protocol.Seed {
	for i := range f.seeds {
		if f.seeds[i].ID == id {
			return &f.seeds[i]
		}
	}
	return nil
}

func (f *dataSetFacts) chief() *protocol.CrewMember {
	for i := range f.crew {
		if protocol.Deref(f.crew[i].Chief) && f.crew[i].ProfileID == f.profileID {
			return &f.crew[i]
		}
	}
	return nil
}

// askFrom names who asked, as the asks sheet does.
func (f *dataSetFacts) askFrom(a *protocol.Ask, holder *protocol.Session) string {
	if holder != nil && protocol.Deref(holder.ChiefOfStaff) {
		return "Chief"
	}
	if member := protocol.Deref(a.CrewMember); member != "" {
		name := crewDisplayName(member)
		if holder == nil {
			return name + " (between sessions)"
		}
		return name
	}
	if holder == nil {
		if tender := f.standbyTender(a); tender != nil {
			name := tender.Label
			if name == "" {
				name = f.harnessName(tender.Agent)
			}
			return name + " · on standby"
		}
	}
	switch seedID := protocol.Deref(a.SeedID); {
	case holder != nil:
		return f.displayName(holder)
	case seedID != "":
		return "waiting for the next tender of " + cmp.Or(protocol.Deref(a.SeedTitle), seedID)
	}
	return "Closed agent"
}

func (f *dataSetFacts) standbyTender(a *protocol.Ask) *protocol.StandbyTender {
	seed := f.seed(protocol.Deref(a.SeedID))
	if seed == nil || seed.Standby == nil {
		return nil
	}
	return seed.Standby.Tender
}

func crewDisplayName(id string) string {
	id = strings.TrimSpace(id)
	if id == "attn" {
		return id
	}
	return capitalized(id)
}

type seedsSet struct {
	Commands []string    `json:"commands"`
	Groups   []seedGroup `json:"groups"`
}

type seedGroup struct {
	Key   string    `json:"key"`
	Label string    `json:"label"`
	Rows  []seedRow `json:"rows"`
}

type seedRow struct {
	ID       string   `json:"id"`
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Ready    bool     `json:"ready"`
	Pulse    string   `json:"pulse,omitempty"`
	PulseAt  string   `json:"pulse_at,omitempty"`
	Bed      string   `json:"bed,omitempty"`
	Tender   string   `json:"tender,omitempty"`
	PartOf   string   `json:"part_of,omitempty"`
	Since    string   `json:"since"`
	Commands []string `json:"commands"`
}

var seedGroups = []struct{ key, label string }{
	{"growing", "Growing"},
	{"ready", "Ready"},
	{"planted", "Planted"},
	{"parked", "Parked"},
}

// shapeSeeds groups the open seeds the garden keeps live: growing, ready to
// tend, planted and waiting, parked. Templates are not work and stay out.
func shapeSeeds(f *dataSetFacts) seedsSet {
	set := seedsSet{Commands: []string{}, Groups: []seedGroup{}}
	for _, group := range seedGroups {
		rows := []seedRow{}
		for _, seed := range f.seeds {
			if seed.Template || seedGroupOf(seed) != group.key {
				continue
			}
			row := seedRow{
				ID:       seed.ID,
				Slug:     seed.StepSlug,
				Title:    seed.Title,
				Status:   seed.Status,
				Ready:    seed.Ready,
				Pulse:    protocol.Deref(seed.Pulse),
				PulseAt:  protocol.Deref(seed.PulseAt),
				Bed:      protocol.Deref(seed.Bed),
				Since:    seed.StateChangedAt,
				Commands: []string{"open"},
			}
			if seed.Tender != nil {
				row.Tender = seed.Tender.Name
			}
			for _, edge := range seed.Edges {
				if edge.Kind == "part-of" {
					row.PartOf = edge.To
				}
			}
			rows = append(rows, row)
		}
		if len(rows) > 0 {
			set.Groups = append(set.Groups, seedGroup{Key: group.key, Label: group.label, Rows: rows})
		}
	}
	return set
}

func seedGroupOf(seed protocol.Seed) string {
	switch {
	case seed.Status == "planted" && seed.Ready:
		return "ready"
	case seed.Status == "planted", seed.Status == "growing", seed.Status == "parked":
		return seed.Status
	}
	return ""
}

type pullRequestsSet struct {
	Commands []string `json:"commands"`
	// PollingOff says why attn does not poll GitHub, in place of every list.
	PollingOff       string   `json:"polling_off,omitempty"`
	RateLimitedUntil string   `json:"rate_limited_until,omitempty"`
	Empty            string   `json:"empty,omitempty"`
	Needing          int      `json:"needing"`
	Yours            []prRow  `json:"yours"`
	Review           []prRepo `json:"review"`
	ReviewCount      int      `json:"review_count"`
}

type prRepo struct {
	Repo     string   `json:"repo"`
	Name     string   `json:"name"`
	Count    int      `json:"count"`
	Rows     []prRow  `json:"rows"`
	Commands []string `json:"commands"`
}

type prRow struct {
	ID       string   `json:"id"`
	Repo     string   `json:"repo"`
	Number   int      `json:"number"`
	Title    string   `json:"title"`
	URL      string   `json:"url"`
	Author   string   `json:"author"`
	Heading  string   `json:"heading"`
	Host     string   `json:"host,omitempty"`
	Role     string   `json:"role"`
	RoleSaid string   `json:"role_said"`
	Updated  bool     `json:"updated"`
	Approved bool     `json:"approved"`
	Settled  bool     `json:"settled"`
	CI       string   `json:"ci,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Commands []string `json:"commands"`
}

// shapePullRequests shapes the Pull requests view's set: the user's own pull
// requests, then the review requests by repository, none of them muted, by
// itself, its repository or its author.
func shapePullRequests(f *dataSetFacts, now time.Time) (pullRequestsSet, time.Time) {
	set := pullRequestsSet{Commands: []string{"refresh"}, Yours: []prRow{}, Review: []prRepo{}}
	var next time.Time
	if until, ok := parseInstant(f.rateLimitedUntil); ok && until.After(now) {
		set.RateLimitedUntil, next = f.rateLimitedUntil, until.Add(time.Millisecond)
	}
	if f.pollingOff != "" {
		set.PollingOff, set.Empty = f.pollingOff, f.pollingOff
		return set, next
	}
	mutedRepo := map[string]bool{}
	for _, r := range f.repos {
		mutedRepo[r.Repo] = mutedRepo[r.Repo] || r.Muted
	}
	mutedAuthor := map[string]bool{}
	for _, a := range f.authors {
		mutedAuthor[a.Author] = mutedAuthor[a.Author] || a.Muted
	}
	var active []*protocol.PR
	for i := range f.prs {
		pr := &f.prs[i]
		if !pr.Muted && !mutedRepo[pr.Repo] && !mutedAuthor[pr.Author] {
			active = append(active, pr)
		}
	}
	if len(active) == 0 {
		set.Empty = "No PRs need attention"
		return set, next
	}
	hosts := map[string]map[string]bool{}
	for _, pr := range active {
		if pr.Host == "" {
			continue
		}
		if hosts[pr.Repo] == nil {
			hosts[pr.Repo] = map[string]bool{}
		}
		hosts[pr.Repo][pr.Host] = true
	}
	row := func(pr *protocol.PR, heading string, yours bool) prRow {
		r := prRow{
			ID:       pr.ID,
			Repo:     pr.Repo,
			Number:   pr.Number,
			Title:    pr.Title,
			URL:      pr.URL,
			Author:   pr.Author,
			Heading:  heading,
			Role:     prRole(pr),
			Updated:  pr.HasNewChanges,
			Approved: pr.ApprovedByMe,
			Settled:  pr.ApprovedByMe && !pr.HasNewChanges,
			Commands: []string{"open", "new-session", "choose-harness", "approve", "merge", "mute"},
		}
		r.RoleSaid = prRoleSaid[r.Role]
		if len(hosts[pr.Repo]) > 1 {
			r.Host = pr.Host
		}
		if ci := protocol.Deref(pr.CIStatus); ci != "" && ci != "none" {
			r.CI = ci
		}
		if yours && pr.Reason != "" {
			r.Reason = strings.ReplaceAll(pr.Reason, "_", " ")
		}
		if pr.Author != "" {
			r.Commands = append(r.Commands, "mute-author")
		}
		return r
	}
	for _, pr := range active {
		if !pr.ApprovedByMe || pr.HasNewChanges {
			set.Needing++
		}
		if pr.Role == protocol.PRRoleAuthor {
			set.Yours = append(set.Yours, row(pr, fmt.Sprintf("%s #%d", repoName(pr.Repo), pr.Number), true))
			continue
		}
		at := slices.IndexFunc(set.Review, func(r prRepo) bool { return r.Repo == pr.Repo })
		if at < 0 {
			at = len(set.Review)
			set.Review = append(set.Review, prRepo{Repo: pr.Repo, Name: repoName(pr.Repo), Rows: []prRow{}, Commands: []string{"mute-repo"}})
		}
		set.Review[at].Rows = append(set.Review[at].Rows, row(pr, fmt.Sprintf("#%d", pr.Number), false))
		set.Review[at].Count++
		set.ReviewCount++
	}
	return set, next
}

var prRoleSaid = map[string]string{
	"reviewer":     "Review requested",
	"bot-reviewer": "Review requested by a bot",
	"author":       "Yours",
}

func prRole(pr *protocol.PR) string {
	switch {
	case pr.Role == protocol.PRRoleAuthor:
		return "author"
	case strings.Contains(strings.ToLower(pr.Author), "bot"):
		return "bot-reviewer"
	}
	return "reviewer"
}

func repoName(repo string) string {
	return repo[strings.LastIndexByte(repo, '/')+1:]
}

type appSet struct {
	Commands []string `json:"commands"`
	Name     string   `json:"name"`
	Tagline  string   `json:"tagline"`
	Version  string   `json:"version"`
	WhatsNew whatsNew `json:"whats_new"`
}

type whatsNew struct {
	Release string `json:"release"`
	Line    string `json:"line"`
}

func shapeApp() appSet {
	return appSet{
		Commands: []string{"settings", "whats-new"},
		Name:     "attn",
		Tagline:  "attention hub",
		Version:  buildinfo.Version,
		WhatsNew: whatsNew{Release: whatsnew.Release, Line: whatsnew.Banner},
	}
}
