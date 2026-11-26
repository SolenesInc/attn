//! Crew members between sessions as sidebar rows: a start that asks for a second click before
//! it wakes the member, and the member's menu.

use attn_protocol::CrewMember;
use attn_ui::Tokens;
use attn_ui::blocks::Inline;
use attn_ui::system::{self, IconSize, ListRow, PillLook, Reveal, Type, words};
use rune::{El, Tx};
use rune_kit::{MenuItem, Placement};

use super::model::AgentRow;
use crate::State;

/// How long an armed start waits for its second click: a tripwire about ten times the few
/// hundred milliseconds a confirming click takes.
pub(crate) const ARM_SECONDS: f64 = 4.0;

/// How long a confirmed start shows its flare, during which further clicks do nothing.
const BREAK_SECONDS: f64 = 0.62;

const MENU: &str = "crew-member-menu";

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Phase {
    Armed,
    Breaking,
}

struct Arm {
    member: String,
    phase: Phase,
    until: f64,
    focus: Option<String>,
}

struct Menu {
    member: String,
    under: String,
}

/// The sidebar's crew rows: the one start armed, and the member menu open.
#[derive(Default)]
pub(crate) struct Rows {
    arm: Option<Arm>,
    menu: Option<Menu>,
}

impl Rows {
    /// The member whose start is armed or breaking, and which.
    pub(crate) fn armed(&self) -> Option<(String, Phase)> {
        self.arm.as_ref().map(|arm| (arm.member.clone(), arm.phase))
    }
}

/// A member between sessions, as its row shows it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Resting {
    pub(crate) member: String,
    pub(crate) name: String,
    pub(crate) phase: Option<Phase>,
    /// The Chief, whose start takes one click.
    pub(crate) chief: bool,
    /// The harness it starts on, while the sidebar shows harness logos.
    pub(crate) harness: Option<String>,
}

pub(crate) fn resting(state: &State, member: &str) -> Resting {
    let found = crate::crew::members(state)
        .into_iter()
        .find(|m| m.id == member);
    let name = found
        .map(|m| m.name.clone())
        .filter(|n| !n.is_empty())
        .unwrap_or_else(|| crate::asks::crew_display_name(member));
    let harness =
        super::logos(state).then(|| found.map(|m| m.resolved_agent.clone()).unwrap_or_default());
    let phase = state
        .window
        .crew_rows
        .arm
        .as_ref()
        .filter(|arm| arm.member == member)
        .map(|arm| arm.phase);
    Resting {
        member: member.to_owned(),
        name,
        phase,
        chief: crate::chief::member(state).is_some_and(|c| c.id == member),
        harness,
    }
}

fn idle_lead(state: &State, harness: Option<&str>, t: &Tokens) -> El {
    match harness {
        Some(harness) => system::icon(
            crate::harness::logo::of(state, harness),
            IconSize::Regular,
            t.state_idle,
        ),
        None => system::dot(
            t.state_idle,
            system::DotShape::Filled,
            system::DotSize::Regular,
        ),
    }
}

/// A crew member running in a live session, shown as a compact pill in desktop flow.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct ActivePill {
    pub(crate) key: String,
    pub(crate) closing: bool,
    pub(crate) agent: AgentRow,
}

/// One entry in the desktop sidebar's compact crew pill bar.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) enum DesktopEntry {
    ChiefSetup(crate::chief::View),
    Active(Box<ActivePill>),
    Resting(Resting),
}

/// The live sessions currently represented as active pills in the desktop sidebar's Crew bar.
pub(crate) fn shown_crew_sessions(facts: &attn_client::Facts) -> std::collections::HashSet<&str> {
    let profile = &facts.profiles.selected;
    let live_session = |id: &str| {
        facts
            .sessions
            .iter()
            .find(|s| s.id == id && &s.profile_id == profile)
            .map(|s| s.id.as_str())
    };
    let mut shown = std::collections::HashSet::new();
    let mut covered_members = std::collections::HashSet::new();
    let mut chief_covered = false;

    if let Some(chief) = crate::chief::of(facts) {
        chief_covered = true;
        covered_members.insert(chief.id.as_str());
        let configured = chief.agent.as_deref().is_some_and(|a| !a.is_empty());
        if configured
            && let Some(id) = chief
                .binding_session
                .as_deref()
                .filter(|s| !s.is_empty())
                .and_then(live_session)
        {
            shown.insert(id);
        }
    }

    for member in facts
        .crew
        .iter()
        .filter(|m| &m.profile_id == profile && !m.retired)
    {
        if !covered_members.insert(member.id.as_str()) {
            continue;
        }
        if let Some(id) = member
            .binding_session
            .as_deref()
            .filter(|s| !s.is_empty())
            .and_then(live_session)
        {
            shown.insert(id);
        }
    }

    for session in facts.sessions.iter() {
        if &session.profile_id != profile || shown.contains(session.id.as_str()) {
            continue;
        }
        if session.chief_of_staff == Some(true) && chief_covered {
            continue;
        }
        if session
            .crew_member
            .as_deref()
            .is_some_and(|m| covered_members.contains(m))
        {
            continue;
        }
        if session.chief_of_staff == Some(true)
            || session
                .crew_member
                .as_deref()
                .is_some_and(|c| !c.is_empty())
        {
            shown.insert(session.id.as_str());
        }
    }

    shown
}

fn resting_of(
    member: &CrewMember,
    is_chief: bool,
    logos_on: bool,
    armed: Option<(&str, Phase)>,
) -> Resting {
    let name = if member.name.is_empty() {
        crate::asks::crew_display_name(&member.id)
    } else {
        member.name.clone()
    };
    let phase = armed
        .filter(|(id, _)| *id == member.id)
        .map(|(_, phase)| phase);
    Resting {
        member: member.id.clone(),
        name,
        phase,
        chief: is_chief,
        harness: logos_on.then(|| member.resolved_agent.clone()),
    }
}

/// The selected profile's crew members in pill-bar order: Chief first, then the roster by key,
/// then any live crew session whose member left the roster.
pub(crate) fn desktop_entries(state: &State) -> Vec<DesktopEntry> {
    let facts = &state.facts;
    let profile = &facts.profiles.selected;
    let on_screen = state.window.on_screen(facts);
    let logos_on = super::logos(state);
    let armed = state
        .window
        .crew_rows
        .arm
        .as_ref()
        .map(|a| (a.member.as_str(), a.phase));
    let session_by_id = |id: &str| {
        facts
            .sessions
            .iter()
            .find(|s| s.id == id && &s.profile_id == profile)
    };
    let active_pill =
        |key: String, name: Option<&str>, closing: bool, session: &attn_protocol::Session| {
            let selected = on_screen.as_deref() == Some(session.id.as_str());
            let mut agent = AgentRow::of(session, &facts.harnesses, &facts.outposts, selected)
                .asking(facts, session);
            if let Some(name) = name.filter(|n| !n.is_empty()) {
                agent.name = name.to_owned();
            }
            DesktopEntry::Active(Box::new(ActivePill {
                key,
                closing,
                agent,
            }))
        };

    let mut entries = Vec::new();
    let mut covered_sessions = std::collections::HashSet::new();
    let mut covered_members = std::collections::HashSet::new();
    let mut chief_covered = false;

    if let Some(chief) = crate::chief::view(state) {
        use crate::chief::Phase as ChiefPhase;
        chief_covered = true;
        covered_members.insert(chief.key.clone());
        let live = chief.session.as_deref().and_then(session_by_id);
        match (chief.phase, live) {
            (ChiefPhase::Unconfigured | ChiefPhase::Configuring | ChiefPhase::Starting, _) => {
                entries.push(DesktopEntry::ChiefSetup(chief));
            }
            (ChiefPhase::InSession | ChiefPhase::Closing, Some(session)) => {
                covered_sessions.insert(session.id.as_str());
                entries.push(active_pill(
                    format!("{CLASSIC_ROW}.{}", chief.key),
                    Some(&chief.name),
                    chief.phase == ChiefPhase::Closing,
                    session,
                ));
            }
            _ => {
                if let Some(member) = crate::chief::member(state) {
                    entries.push(DesktopEntry::Resting(resting_of(
                        member, true, logos_on, armed,
                    )));
                }
            }
        }
    }

    let mut members: Vec<&CrewMember> = crate::crew::members(state)
        .into_iter()
        .filter(|m| !covered_members.contains(&m.id))
        .collect();
    members.sort_by(|a, b| a.id.cmp(&b.id));
    for member in members {
        covered_members.insert(member.id.clone());
        let live = member
            .binding_session
            .as_deref()
            .filter(|s| !s.is_empty())
            .and_then(session_by_id);
        match live {
            Some(session) => {
                covered_sessions.insert(session.id.as_str());
                let name = (!member.name.is_empty()).then_some(member.name.as_str());
                entries.push(active_pill(
                    format!("{CLASSIC_ROW}.{}", member.id),
                    name,
                    false,
                    session,
                ));
            }
            None => entries.push(DesktopEntry::Resting(resting_of(
                member, false, logos_on, armed,
            ))),
        }
    }

    for session in facts.sessions.iter() {
        if &session.profile_id != profile || covered_sessions.contains(session.id.as_str()) {
            continue;
        }
        if session.chief_of_staff == Some(true) && chief_covered {
            continue;
        }
        if session
            .crew_member
            .as_deref()
            .is_some_and(|m| covered_members.contains(m))
        {
            continue;
        }
        let is_crew = session.chief_of_staff == Some(true)
            || session
                .crew_member
                .as_deref()
                .is_some_and(|c| !c.is_empty());
        if !is_crew {
            continue;
        }
        covered_sessions.insert(session.id.as_str());
        let display = session
            .crew_member
            .as_deref()
            .filter(|c| !c.is_empty())
            .map(crate::asks::crew_display_name);
        entries.push(active_pill(
            format!("{CLASSIC_ROW}.session.{}", session.id),
            display.as_deref(),
            false,
            session,
        ));
    }

    entries
}

/// The compact wrapping crew pill bar for desktop flow.
pub(crate) fn pill_bar(state: &State, entries: &[DesktopEntry], logos: bool, t: &Tokens) -> El {
    ListRow::pill_strip(CLASSIC_ROW)
        .role("list")
        .label("Crew")
        .children(entries.iter().map(|entry| match entry {
            DesktopEntry::ChiefSetup(chief) => chief_setup_pill(state, chief, logos, t),
            DesktopEntry::Active(pill) => active_pill_view(state, pill, logos, t),
            DesktopEntry::Resting(member) => resting_pill_view(member, t),
        }))
}

fn chief_setup_pill(state: &State, chief: &crate::chief::View, logos: bool, t: &Tokens) -> El {
    use crate::chief::{Phase as ChiefPhase, SLOT};
    let cue = if chief.phase == ChiefPhase::Unconfigured {
        "Set up"
    } else {
        "starting…"
    };
    let phase = match chief.phase {
        ChiefPhase::Unconfigured => "unconfigured",
        ChiefPhase::Configuring => "configuring",
        _ => "starting",
    };
    let ground = PillLook::Elevated.ground(t);
    let cue_ink = attn_ui::colors::moved_until_it_reads(
        t.brand,
        t.text_primary,
        ground,
        attn_ui::colors::READS,
    );
    let pill = system::pill_surface(
        SLOT,
        format!("Set up {}", chief.name),
        PillLook::Elevated,
        t,
    )
    .role("button")
    .focusable(!chief.busy)
    .a11y_value(phase)
    .child(idle_lead(
        state,
        logos.then_some(chief.agent.as_deref().unwrap_or_default()),
        t,
    ))
    .child(
        words(chief.name.clone(), Type::Control, t.text_primary)
            .key(format!("{SLOT}.name"))
            .single_line()
            .truncate()
            .shrink()
            .min_w(0.0),
    )
    .child(
        words(cue, Type::Control, cue_ink)
            .key(format!("{SLOT}.pill"))
            .single_line(),
    );
    if chief.busy {
        return pill;
    }
    pill.on_press_tx(crate::chief::edit).on_key(
        |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            if ev.key == rune::Key::Enter {
                crate::chief::edit(state, tx);
                return true;
            }
            false
        },
    )
}

fn active_pill_view(state: &State, pill: &ActivePill, logos: bool, t: &Tokens) -> El {
    let key = &pill.key;
    let look = if pill.agent.selected {
        PillLook::Current
    } else {
        PillLook::Elevated
    };
    let ground = look.ground(t);
    let (title_kind, title_ink) = super::title_style(&pill.agent, true, t);
    let title_kind = if title_kind == Type::Name {
        Type::Control
    } else {
        Type::Small
    };
    let session_id = pill.agent.id.clone();
    let actions = {
        let under = format!("{key}.actions");
        let label = format!("Actions for {}", pill.agent.name);
        let id = pill.agent.id.clone();
        let name = pill.agent.name.clone();
        El::from(
            system::icon_button(under.clone(), label, "•••", IconSize::Small)
                .size(system::Size::Small)
                .ink(t.text_tertiary)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::session_menu::open(state, &id, name.clone(), under.clone(), None, tx)
                })
                .build(t),
        )
    };
    system::pill_surface(key.clone(), format!("Open {}", pill.agent.name), look, t)
        .role("button")
        .focusable(true)
        .a11y_value(pill.agent.state.wire())
        .help(pill.agent.hover())
        .child(super::lead(state, key, &pill.agent, logos, ground, t))
        .child(
            words(pill.agent.name.clone(), title_kind, title_ink)
                .key(format!("{key}.name"))
                .single_line()
                .truncate()
                .shrink()
                .min_w(0.0),
        )
        .children(crate::asks::marker(key, pill.agent.asks, t))
        .children(pill.closing.then(|| {
            words("closing…", Type::Mono, t.text_tertiary)
                .key(format!("{key}.closing"))
                .single_line()
        }))
        .child(actions)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::open_agent(state, &session_id, tx)
        })
}

fn resting_pill_view(m: &Resting, t: &Tokens) -> El {
    let key = if m.chief {
        crate::chief::SLOT.to_owned()
    } else {
        format!("{CLASSIC_ROW}.{}", m.member)
    };
    let armed = m.phase == Some(Phase::Armed);
    let start_label = if armed {
        format!("Start {} — click again to confirm", m.name)
    } else {
        format!("Start {}", m.name)
    };
    let look = if armed {
        PillLook::Armed
    } else {
        PillLook::Quiet
    };
    let ink = if armed {
        t.text_primary
    } else if m.chief {
        t.text_secondary
    } else {
        t.text_tertiary
    };
    let armed_ink = attn_ui::colors::moved_until_it_reads(
        t.warning,
        t.text_primary,
        look.ground(t),
        attn_ui::colors::READS,
    );
    let sun = {
        let (member, row_key) = (m.member.clone(), key.clone());
        let sun = system::icon_button(
            format!("{key}.start"),
            start_label.clone(),
            &attn_ui::icon::CREW_SUN,
            IconSize::Small,
        )
        .size(system::Size::Small);
        let sun = match m.phase {
            None => sun.ink(t.text_dimmed),
            Some(_) => sun.ink(armed_ink).selected(true),
        };
        El::from(
            sun.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                trigger(state, &row_key, &member, tx)
            })
            .build(t),
        )
    };
    let (member, pressed, outside) = (m.member.clone(), key.clone(), m.member.clone());
    let mut pill = system::pill_surface(key.clone(), start_label, look, t)
        .role("listitem")
        .a11y_value(if armed { "armed" } else { "between sessions" })
        .child(
            words(m.name.clone(), Type::Small, ink)
                .key(format!("{key}.name"))
                .single_line()
                .truncate()
                .shrink()
                .min_w(0.0),
        )
        .children(armed.then(|| {
            words("confirm", Type::Tag, armed_ink)
                .key(format!("{key}.confirm"))
                .single_line()
        }))
        .child(sun)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| trigger(state, &pressed, &member, tx));
    if armed {
        pill = pill.on_press_outside(move |state: &mut State, _: &mut Tx| {
            if state
                .window
                .crew_rows
                .arm
                .as_ref()
                .is_some_and(|a| a.member == outside)
            {
                disarm(state);
            }
        });
    }
    pill
}

/// A press on `member`'s start: the first arms it, the second wakes the member, and one
/// during the flare does nothing.
pub(crate) fn trigger(state: &mut State, row_key: &str, member: &str, tx: &mut Tx) {
    let now = tx.clock();
    let one_click = crate::chief::member(state).is_some_and(|c| c.id == member);
    let armed = match &state.window.crew_rows.arm {
        Some(arm) if arm.member == member => arm.phase,
        _ if one_click => Phase::Armed,
        _ => {
            state.window.crew_rows.arm = Some(Arm {
                member: member.to_owned(),
                phase: Phase::Armed,
                until: now + ARM_SECONDS,
                focus: tx
                    .focused()
                    .map(str::to_owned)
                    .filter(|f| !f.starts_with(row_key)),
            });
            tx.wake_at(now + ARM_SECONDS);
            return;
        }
    };
    if armed == Phase::Breaking {
        return;
    }
    state.window.crew_rows.arm = Some(Arm {
        member: member.to_owned(),
        phase: Phase::Breaking,
        until: now + BREAK_SECONDS,
        focus: None,
    });
    tx.wake_at(now + BREAK_SECONDS);
    if one_click {
        crate::chief::start(state, tx);
    } else {
        crate::crew::start(state, member, tx);
    }
}

/// Stands an armed start down; whether one was armed.
pub(crate) fn disarm(state: &mut State) -> bool {
    let armed = state
        .window
        .crew_rows
        .arm
        .as_ref()
        .is_some_and(|arm| arm.phase == Phase::Armed);
    if armed {
        state.window.crew_rows.arm = None;
    }
    armed
}

/// Ends an arm or a flare whose time ran out, and an arm whose focus went to another row;
/// whether one ended.
pub(crate) fn poll(state: &mut State, now: f64, row_prefixes: &[String], tx: &Tx) -> bool {
    let Some(arm) = &state.window.crew_rows.arm else {
        return false;
    };
    let focused = tx.focused();
    let wandered = arm.phase == Phase::Armed
        && focused != arm.focus.as_deref()
        && !focused.is_some_and(|f| {
            row_prefixes.iter().any(|p| {
                let row = format!("{p}{}", arm.member);
                f == row || f.starts_with(&format!("{row}."))
            })
        });
    if now >= arm.until || wandered {
        state.window.crew_rows.arm = None;
        return true;
    }
    false
}

/// The keys of the rows a member's start sits in, before the member's key.
pub(crate) fn row_prefixes() -> [String; 2] {
    [format!("{QUEUE_ROW}."), format!("{CLASSIC_ROW}.")]
}

pub(crate) const QUEUE_ROW: &str = "queue-crew.member";
pub(crate) const CLASSIC_ROW: &str = "sidebar.crew";

/// A member between sessions: pressing the row or its sun starts the member in two clicks.
pub(crate) fn row_view(state: &State, key: String, m: &Resting, t: &Tokens) -> El {
    let armed = m.phase == Some(Phase::Armed);
    let start_label = if armed {
        format!("Start {} — click again to confirm", m.name)
    } else {
        format!("Start {}", m.name)
    };
    let (member, pressed, outside) = (m.member.clone(), key.clone(), m.member.clone());
    let sun = {
        let (member, row_key) = (m.member.clone(), key.clone());
        let sun = system::icon_button(
            format!("{key}.start"),
            start_label.clone(),
            &attn_ui::icon::CREW_SUN,
            IconSize::Small,
        );
        let sun = match m.phase {
            None => sun.ink(t.text_dimmed),
            Some(_) => sun.ink(t.warning).selected(true),
        };
        El::from(
            sun.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                trigger(state, &row_key, &member, tx)
            })
            .build(t),
        )
    };
    let actions = {
        let (member, under) = (m.member.clone(), format!("{key}.actions"));
        super::queue_view::control(
            &key,
            "actions",
            format!("Actions for {}", m.name),
            "•••",
            move |state, tx| open_menu(state, &member, &under, tx),
        )
    };
    let ink = if m.chief {
        t.text_primary
    } else {
        t.text_tertiary
    };
    let name = words(m.name.clone(), Type::Body, ink)
        .key(format!("{key}.name"))
        .single_line();
    let between = m.chief.then(|| {
        Inline::joined()
            .grow(1.0)
            .justify_end()
            .child(
                system::icon(&attn_ui::icon::CHIEF, IconSize::Small, t.text_tertiary)
                    .key(format!("{key}.chief"))
                    .help(m.name.clone()),
            )
            .child(
                words("between sessions", Type::Mono, t.text_tertiary)
                    .key(format!("{key}.age"))
                    .single_line()
                    .truncate()
                    .shrink(),
            )
    });
    let row = ListRow::new(key.clone(), start_label)
        .role("listitem")
        .unfocusable()
        .lead(idle_lead(state, m.harness.as_deref(), t))
        .title_el(
            Inline::joined()
                .grow(1.0)
                .child(if m.chief {
                    name.truncate()
                } else {
                    name.truncate().shrink()
                })
                .children(between),
        );
    let row = if armed {
        let confirm = words("confirm", Type::Label, t.warning).key(format!("{key}.confirm"));
        row.controls([confirm, actions.build(t).into(), sun], Reveal::Always)
    } else {
        row.hover_controls([actions])
            .controls([sun], Reveal::Always)
    };
    let mut row = row
        .build(t)
        .a11y_value(if armed { "armed" } else { "between sessions" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| trigger(state, &pressed, &member, tx));
    if armed {
        row = row.on_press_outside(move |state: &mut State, _: &mut Tx| {
            if state
                .window
                .crew_rows
                .arm
                .as_ref()
                .is_some_and(|a| a.member == outside)
            {
                disarm(state);
            }
        });
    }
    row
}

/// The Chief's slot before it has a session: Set up opens its setup, and the slot says when
/// the Chief is starting.
pub(crate) fn chief_setup_row(
    state: &State,
    key: String,
    chief: &crate::chief::View,
    logos: bool,
    t: &Tokens,
) -> El {
    use crate::chief::Phase;
    let pill = if chief.phase == Phase::Unconfigured {
        "Set up ⏎"
    } else {
        "starting…"
    };
    let phase = match chief.phase {
        Phase::Unconfigured => "unconfigured",
        Phase::Configuring => "configuring",
        _ => "starting",
    };
    let row = ListRow::new(key.clone(), format!("Set up {}", chief.name))
        .lead(idle_lead(
            state,
            logos.then_some(chief.agent.as_deref().unwrap_or_default()),
            t,
        ))
        .title(chief.name.clone())
        .aside(system::pill(format!("{key}.pill"), pill, t))
        .disabled(chief.busy)
        .build(t)
        .a11y_value(phase);
    if chief.busy {
        return row;
    }
    row.on_press_tx(crate::chief::edit).on_key(
        |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            if ev.key == rune::Key::Enter {
                crate::chief::edit(state, tx);
                return true;
            }
            false
        },
    )
}

fn open_menu(state: &mut State, member: &str, under: &str, tx: &mut Tx) {
    state.window.crew_rows.menu = Some(Menu {
        member: member.to_owned(),
        under: under.to_owned(),
    });
    let chief = crate::chief::member(state).is_some_and(|c| c.id == member);
    tx.focus(
        format!("{MENU}.{}", if chief { "route" } else { "details" }),
        true,
    );
}

/// Opens Manage crew on `member`.
pub(crate) fn details(state: &mut State, member: &str, opener: Option<String>, tx: &mut Tx) {
    crate::crew::open(state, Some(member.to_owned()), tx);
    state.window.crew.opener = opener;
}

/// Opens Manage crew on `member`'s charter.
pub(crate) fn charter(state: &mut State, member: &str, opener: Option<String>, tx: &mut Tx) {
    crate::crew::open_on(
        state,
        Some(member.to_owned()),
        crate::crew::Tab::Charter,
        tx,
    );
    state.window.crew.opener = opener;
}

/// The member menu, over the window.
pub(crate) fn menu_view(state: &State, t: &Tokens) -> Vec<El> {
    let Some(menu) = &state.window.crew_rows.menu else {
        return Vec::new();
    };
    let member = crate::crew::members(state)
        .into_iter()
        .find(|m| m.id == menu.member);
    let name = member
        .map(|m| m.name.clone())
        .filter(|n| !n.is_empty())
        .unwrap_or_else(|| crate::asks::crew_display_name(&menu.member));
    let desktop = member.and_then(|m| m.launch_desktop.as_ref()?.label.clone());
    let chief = crate::chief::view(state).filter(|c| c.key == menu.member);
    let id = menu.member.clone();
    let mut kit_menu = attn_ui::system::row_menu(MENU)
        .anchor(menu.under.clone())
        .placement(Placement::BottomEnd)
        .min_width(200.0)
        .label(format!("Actions for {name}"))
        .open(true)
        .on_close(|state: &mut State| state.window.crew_rows.menu = None);
    if let Some(desktop) = desktop {
        kit_menu = kit_menu.group_label(desktop);
    }
    let is_chief = chief.is_some();
    if is_chief {
        kit_menu = kit_menu.item(
            MenuItem::new("Harness and model…")
                .key(format!("{MENU}.route"))
                .on_select_tx(|state: &mut State, tx: &mut Tx| {
                    state.window.crew_rows.menu = None;
                    crate::chief::edit(state, tx);
                }),
        );
    }
    kit_menu = kit_menu.item(
        MenuItem::new(if is_chief {
            "Edit charter"
        } else {
            "Member details"
        })
        .key(format!("{MENU}.details"))
        .lead(system::icon(
            &attn_ui::icon::CHIEF,
            IconSize::Small,
            t.text_tertiary,
        ))
        .on_select_tx(move |state: &mut State, tx: &mut Tx| {
            let opener = state.window.crew_rows.menu.take().map(|m| m.under);
            if is_chief {
                charter(state, &id, opener, tx);
            } else {
                details(state, &id, opener, tx);
            }
        }),
    );
    if let Some(chief) = chief.filter(|c| c.agent.is_some()) {
        kit_menu = kit_menu.item(
            MenuItem::new(format!("Start {}", chief.name))
                .key(format!("{MENU}.start"))
                .on_select_tx(|state: &mut State, tx: &mut Tx| {
                    state.window.crew_rows.menu = None;
                    crate::chief::start(state, tx);
                }),
        );
    }
    let mut layer = Vec::new();
    kit_menu.build(attn_ui::kit(t), &mut layer);
    layer
}
