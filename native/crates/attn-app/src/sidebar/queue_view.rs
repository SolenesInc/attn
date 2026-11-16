use std::rc::Rc;

use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{El, GrowthSpec, ScrollSpec, Tx, col, memo, row, text};
use rune_kit::{Button, ButtonVariant, TextInput};

use super::model::{AgentRow, display_name, ordered_desktops};
use super::queue::{self, Bands, QueueRow};
use super::{ROW_HEIGHT, ROWS_BUILT, home_row, lead, open_agent};
use crate::State;
use crate::action::Action;

pub(crate) const FILTER_FIELD: &str = "queue.filter.field";
pub(crate) const LIST: &str = "sidebar.queue";

/// Where an agent's row sits in the queue sidebar, which sets the buttons it carries.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Band {
    Chief,
    Crew,
    Turn,
    Working,
    Snoozed,
}

impl Band {
    fn prefix(self) -> &'static str {
        match self {
            Band::Chief => "queue-chief",
            Band::Crew => "queue-crew",
            Band::Turn => "queue-turn",
            Band::Working => "queue-settled",
            Band::Snoozed => "queue-snoozed",
        }
    }
}

/// One agent as a queue row shows it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct QueueAgent {
    pub band: Band,
    pub agent: AgentRow,
    /// The desktop's slot, or `·` for a desktop without one.
    pub slot: String,
    pub desktop_title: String,
    pub age: String,
    pub snoozed_until: String,
    pub settle: bool,
    pub snooze: bool,
    pub unsnooze: bool,
    /// The member a crew row's session belongs to, with its name, for Ask to close.
    pub member: Option<(String, String)>,
    /// The row is the Chief's slot: Ask to close goes through the Chief's own request.
    pub chief: bool,
    /// The Chief was asked to close and has not answered yet.
    pub closing: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum QLine {
    Rule { key: &'static str, text: String },
    WaitingHead { waiting: usize, unreachable: usize },
    AgentsToggle { open: bool, more: usize },
    Counts(String),
    Filter,
    Empty(&'static str),
    Agent(Box<QueueAgent>),
    CrewRule,
    Member(super::crew::Resting),
    ChiefSetup(crate::chief::View),
}

impl QLine {
    pub fn key(&self) -> String {
        match self {
            QLine::Rule { key, .. } => format!("queue.rule.{key}"),
            QLine::WaitingHead { .. } => "queue.waiting-head".into(),
            QLine::AgentsToggle { .. } => "queue.agents-toggle".into(),
            QLine::Counts(_) => "queue.agents-counts".into(),
            QLine::Filter => "queue.filter".into(),
            QLine::Empty(key) => format!("queue.empty.{key}"),
            QLine::Agent(a) => format!("{}.{}", a.band.prefix(), a.agent.id),
            QLine::CrewRule => "queue.rule.crew".into(),
            QLine::Member(m) if m.chief => crate::chief::SLOT.into(),
            QLine::Member(m) => format!("{}.{}", super::crew::QUEUE_ROW, m.member),
            QLine::ChiefSetup(_) => crate::chief::SLOT.into(),
        }
    }
}

/// A queue row's key and the agent it shows.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct AgentLine {
    pub key: String,
    pub session: String,
}

/// The agent rows of the queue sidebar, top to bottom, as the keyboard walks them.
pub(crate) fn agent_rows(state: &State) -> Vec<AgentLine> {
    let bands = queue::bands(&state.facts, state.window.now_ms);
    agent_lines(&lines(state, &bands))
}

fn agent_lines(lines: &[QLine]) -> Vec<AgentLine> {
    lines
        .iter()
        .filter_map(|line| match line {
            QLine::Agent(a) => Some(AgentLine {
                key: line.key(),
                session: a.agent.id.clone(),
            }),
            _ => None,
        })
        .collect()
}

/// Where the row the arrows reached is now: the same row, or, once its agent moved to another
/// band, that agent's first row.
pub(crate) fn reached(rows: &[AgentLine], cursor: &AgentLine) -> Option<usize> {
    rows.iter()
        .position(|row| row == cursor)
        .or_else(|| rows.iter().position(|row| row.session == cursor.session))
}

/// The slot badge a queue row carries for its desktop: its number among the slotted
/// desktops, or `·` for a desktop without a shortcut.
fn slot_of(state: &State, desktop: Option<&str>) -> String {
    let Some(desktop) = desktop else {
        return String::new();
    };
    ordered_desktops(&state.facts.desktops)
        .into_iter()
        .find(|d| d.id == desktop)
        .and_then(|d| d.shortcut_slot.filter(|s| *s > 0))
        .map_or_else(|| "·".to_owned(), |slot| slot.to_string())
}

fn agent(
    state: &State,
    on_screen: &[String],
    band: Band,
    row: &QueueRow<'_>,
    now_ms: i64,
) -> Box<QueueAgent> {
    let session = row.session;
    let actions = queue::actions(&state.facts, session, now_ms);
    Box::new(QueueAgent {
        band,
        agent: {
            let row = AgentRow::of(
                session,
                &state.facts.outposts,
                state.window.on_screen(&state.facts).as_deref() == Some(session.id.as_str()),
            )
            .asking(&state.facts, session);
            if on_screen.contains(&session.id) {
                row
            } else {
                row.off_screen(session)
            }
        },
        slot: slot_of(state, row.desktop),
        desktop_title: row.desktop_title.clone(),
        age: if band == Band::Turn {
            queue::turn_age(session.turn_opened_at.as_deref(), now_ms)
        } else {
            String::new()
        },
        snoozed_until: if band == Band::Snoozed {
            queue::wake_time_short(session.turn_snoozed_until.as_deref(), now_ms)
        } else {
            String::new()
        },
        settle: band == Band::Turn && actions.settle,
        snooze: matches!(band, Band::Turn | Band::Working) && actions.snooze,
        unsnooze: band == Band::Snoozed && !row.offline,
        member: (band == Band::Crew)
            .then(|| session.crew_member.clone().filter(|m| !m.is_empty()))
            .flatten()
            .map(|m| {
                let name = super::crew::resting(state, &m).name;
                (m, name)
            }),
        chief: false,
        closing: false,
    })
}

/// The Chief's slot as the desktop sidebar draws it, above the crew between sessions.
pub(super) fn chief_slot(state: &State, t: &Tokens) -> Option<El> {
    let chief = crate::chief::view(state)?;
    let bands = queue::bands(&state.facts, state.window.now_ms);
    let on_screen = crate::desktop::on_screen_sessions(state);
    let line = chief_line(state, &on_screen, &bands, chief);
    let key = line.key();
    Some(match &line {
        QLine::Agent(agent) => queue_row(key, agent, false, super::logos(state), t),
        QLine::ChiefSetup(chief) => super::crew::chief_setup_row(key, chief, t),
        QLine::Member(member) => super::crew::row_view(key, member, ROW_HEIGHT, t),
        _ => return None,
    })
}

/// The Chief's slot: setup while it has none, its session's row while it has one, and a start
/// between sessions.
pub(super) fn chief_line(
    state: &State,
    on_screen: &[String],
    bands: &Bands<'_>,
    chief: crate::chief::View,
) -> QLine {
    use crate::chief::Phase;
    let rows = bands
        .chief
        .iter()
        .chain(&bands.crew)
        .chain(&bands.turns)
        .chain(&bands.settled)
        .chain(&bands.snoozed);
    let row = chief
        .session
        .as_deref()
        .and_then(|id| rows.into_iter().find(|r| r.session.id == id));
    match (chief.phase, row) {
        (Phase::Unconfigured | Phase::Configuring | Phase::Starting, _) => QLine::ChiefSetup(chief),
        (Phase::InSession | Phase::Closing, Some(row)) => {
            let mut line = agent(state, on_screen, Band::Chief, row, state.window.now_ms);
            line.agent.name.clone_from(&chief.name);
            line.member = (!chief.busy).then(|| (chief.key.clone(), chief.name.clone()));
            line.chief = true;
            line.closing = chief.phase == Phase::Closing;
            QLine::Agent(line)
        }
        _ => QLine::Member(super::crew::resting(state, &chief.key)),
    }
}

/// The queue sidebar's body, top to bottom: the crew, the waiting card and its turns, the
/// agent list's toggle, and with the list open its filter, working and snoozed agents.
pub fn lines(state: &State, bands: &Bands<'_>) -> Vec<QLine> {
    let now_ms = state.window.now_ms;
    let window = &state.window;
    let on_screen = crate::desktop::on_screen_sessions(state);
    let mut lines = Vec::new();
    let chief_slot = crate::chief::view(state);
    let chief = bands.chief.as_ref().filter(|_| chief_slot.is_none());
    let chief_member = match &chief_slot {
        Some(slot) => Some(slot.key.clone()),
        None => chief.and_then(|c| c.session.crew_member.clone()),
    };
    let mut members: Vec<(String, Option<&QueueRow<'_>>)> = Vec::new();
    for row in &bands.crew {
        let member = row.session.crew_member.clone().unwrap_or_default();
        if !members.iter().any(|(m, _)| *m == member) {
            members.push((member, Some(row)));
        }
    }
    for resting in crate::crew::members(state) {
        if !members.iter().any(|(m, _)| *m == resting.id) {
            members.push((resting.id.clone(), None));
        }
    }
    members.retain(|(m, _)| Some(m) != chief_member.as_ref());
    members.sort_by(|a, b| a.0.cmp(&b.0));
    if chief_slot.is_some() || chief.is_some() || !members.is_empty() {
        lines.push(QLine::CrewRule);
        if let Some(slot) = chief_slot {
            lines.push(chief_line(state, &on_screen, bands, slot));
        } else if let Some(chief) = chief {
            lines.push(QLine::Agent(agent(
                state,
                &on_screen,
                Band::Chief,
                chief,
                now_ms,
            )));
        }
        for (member, row) in members {
            lines.push(match row {
                Some(row) => QLine::Agent(agent(state, &on_screen, Band::Crew, row, now_ms)),
                None => QLine::Member(super::crew::resting(state, &member)),
            });
        }
    }
    let pinned: std::collections::HashSet<&str> =
        bands.crew.iter().map(|r| r.session.id.as_str()).collect();
    let (waiting, unreachable) = bands.owed();
    lines.push(QLine::WaitingHead {
        waiting,
        unreachable,
    });
    for row in &bands.turns {
        lines.push(QLine::Agent(agent(
            state,
            &on_screen,
            Band::Turn,
            row,
            now_ms,
        )));
    }
    let working: Vec<&QueueRow<'_>> = bands
        .settled
        .iter()
        .filter(|r| !pinned.contains(r.session.id.as_str()))
        .collect();
    let snoozed: Vec<&QueueRow<'_>> = bands
        .snoozed
        .iter()
        .filter(|r| !pinned.contains(r.session.id.as_str()))
        .collect();
    let more = working.len() + snoozed.len();
    lines.push(QLine::AgentsToggle {
        open: window.agent_list_open,
        more,
    });
    if !window.agent_list_open {
        let parts: Vec<String> = [(working.len(), "working"), (snoozed.len(), "snoozed")]
            .into_iter()
            .filter(|(n, _)| *n > 0)
            .map(|(n, label)| format!("{n} {label}"))
            .collect();
        if !parts.is_empty() {
            lines.push(QLine::Counts(parts.join("  ")));
        }
        return lines;
    }
    lines.push(QLine::Filter);
    let filter = window.agent_filter.text.to_lowercase();
    let matches = |r: &&QueueRow<'_>| {
        filter.is_empty() || display_name(r.session).to_lowercase().contains(&filter)
    };
    let working: Vec<_> = working.into_iter().filter(matches).collect();
    let snoozed: Vec<_> = snoozed.into_iter().filter(matches).collect();
    lines.push(QLine::Rule {
        key: "working",
        text: format!("Working {}", working.len()),
    });
    if working.is_empty() {
        lines.push(QLine::Empty("working"));
    }
    for row in working {
        lines.push(QLine::Agent(agent(
            state,
            &on_screen,
            Band::Working,
            row,
            now_ms,
        )));
    }
    if !snoozed.is_empty() {
        lines.push(QLine::Rule {
            key: "snoozed",
            text: format!("Snoozed {}", snoozed.len()),
        });
        for row in snoozed {
            lines.push(QLine::Agent(agent(
                state,
                &on_screen,
                Band::Snoozed,
                row,
                now_ms,
            )));
        }
    }
    lines
}

pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let window = &state.window;
    let reads = (
        std::sync::Arc::as_ptr(&facts.sessions) as usize,
        std::sync::Arc::as_ptr(&facts.desktops) as usize,
        std::sync::Arc::as_ptr(&facts.profiles) as usize,
        (
            std::sync::Arc::as_ptr(&facts.settings) as usize,
            std::sync::Arc::as_ptr(&facts.asks) as usize,
            std::sync::Arc::as_ptr(&facts.garden) as usize,
            std::sync::Arc::as_ptr(&facts.outposts) as usize,
            std::sync::Arc::as_ptr(&facts.crew) as usize,
            window.crew_rows.armed(),
            crate::chief::view(state),
        ),
        window.view,
        window.agent_list_open,
        (
            window.agent_filter.text.to_owned(),
            window.queue_cursor.clone(),
        ),
        window.now_ms,
        crate::desktop::on_screen_sessions(state),
        super::width::of(state).to_bits(),
        window.desk.drag.as_ref().and_then(|d| d.desktop_marks()),
        (
            *t,
            facts.notifications.unread_critical,
            facts.notifications.critical_title.clone(),
        ),
    );
    memo("sidebar", reads, || {
        let bands = queue::bands(facts, window.now_ms);
        let lines: Rc<[QLine]> = lines(state, &bands).into();
        let keys = lines.clone();
        let rows = agent_lines(&lines);
        let cursor: Option<String> = window
            .queue_cursor
            .as_ref()
            .and_then(|cursor| reached(&rows, cursor))
            .map(|at| rows[at].key.clone());
        let t = *t;
        col()
            .key("sidebar")
            .role("navigation")
            .label("Sidebar")
            .w(super::width::of(state))
            .h_full()
            .fill(t.bg_panel)
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
            .padding(space::S2)
            .gap(space::S1)
            .children(crate::notifications::critical_strip(
                &facts.notifications,
                &t,
            ))
            .child(home_row(
                window.view == crate::shell::View::Home,
                keys_of(state, &Action::SessionGoToDashboard),
                &t,
            ))
            .child(
                col()
                    .key("sidebar.scroll")
                    .w_full()
                    .grow(1.0)
                    .min_h(0.0)
                    .scroll(ScrollSpec {
                        stick_bottom: false,
                    })
                    .child(
                        col()
                            .key(LIST)
                            .role("list")
                            .label("Queue")
                            .w_full()
                            .growth(GrowthSpec::default())
                            .virtual_rows(
                                lines.len(),
                                ROW_HEIGHT,
                                move |_: &State, i| keys[i].key().into(),
                                move |state: &State, i| {
                                    let line_key = lines[i].key();
                                    let reached = cursor.as_deref() == Some(line_key.as_str());
                                    line(state, &lines[i], reached, &t)
                                },
                            )
                            .focusable(true)
                            .on_key(super::actions::list_key),
                    ),
            )
            .child(super::chrome::desktop_strip(state, &bands, &t))
    })
}

fn line(state: &State, line: &QLine, cursor: bool, t: &Tokens) -> El {
    let key = line.key();
    if let QLine::Filter = line {
        return filter(state, key, t);
    }
    let draining = match line {
        QLine::Agent(a) => state.window.countdowns.contains_key(&a.agent.id),
        _ => false,
    };
    let keys = match line {
        QLine::WaitingHead { .. } => keys_of(state, &Action::SessionJumpToWaiting),
        QLine::AgentsToggle { .. } => keys_of(state, &Action::SidebarAgentList),
        _ => None,
    };
    let logos = super::logos(state);
    memo(
        key.clone(),
        (line, draining, cursor, keys.clone(), logos, *t),
        || {
            ROWS_BUILT.with(|n| n.set(n.get() + 1));
            match line {
                QLine::Rule { text: label, .. } => rule(key, label, t),
                QLine::WaitingHead {
                    waiting,
                    unreachable,
                } => waiting_head(key, (*waiting, *unreachable), keys, t),
                QLine::AgentsToggle { open, more } => agents_toggle(key, *open, *more, keys, t),
                QLine::Counts(counts) => text(counts.clone())
                    .key(key)
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
                QLine::Empty(_) => text("Nobody else.")
                    .key(key)
                    .font(type_::LABEL)
                    .color(t.text_muted),
                QLine::Agent(agent) => {
                    let bar = super::settling_bar(state, &key, &agent.agent, t);
                    queue_row(key, agent, cursor, logos, t).children(bar)
                }
                QLine::CrewRule => crew_rule(key, t),
                QLine::Member(member) => super::crew::row_view(key, member, ROW_HEIGHT, t),
                QLine::ChiefSetup(chief) => super::crew::chief_setup_row(key, chief, t),
                QLine::Filter => unreachable!("the filter is built above"),
            }
        },
    )
}

pub(super) fn crew_rule(key: String, t: &Tokens) -> El {
    row()
        .key(key.clone())
        .role("heading")
        .label("Crew")
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S1)
        .child(text("Crew").font(type_::SECTION).color(t.text_tertiary))
        .child(rune::el().grow(1.0).h(1.0).fill(t.border_subtle))
        .child(
            Button::new("manage")
                .variant(ButtonVariant::GhostMuted)
                .control_size(rune_kit::ControlSize::Small)
                .on_press_tx(|state: &mut State, tx: &mut Tx| crate::crew::open(state, None, tx))
                .build(attn_ui::kit(t))
                .key(format!("{key}.manage"))
                .label("Manage crew"),
        )
}

fn rule(key: String, label: &str, t: &Tokens) -> El {
    row()
        .key(key)
        .role("heading")
        .label(label.to_owned())
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .child(
            text(label.to_owned())
                .font(type_::SECTION)
                .color(t.text_tertiary),
        )
}

fn keys_of(state: &State, action: &Action) -> Option<String> {
    state
        .keymap
        .binding(action)
        .map(|binding| state.keymap.label(binding))
}

/// What a section rule does, with its key when it has one.
fn says(does: &str, keys: Option<&String>) -> String {
    match keys {
        Some(keys) => format!("{does} ({keys})"),
        None => does.to_owned(),
    }
}

/// A section rule's line out to its key, as the Tauri app's `queue-rule-line` and `kbd`.
fn rule_tail(keys: Option<String>, t: &Tokens) -> [El; 2] {
    [
        rune::el().grow(1.0).h(1.0).fill(t.border_subtle),
        match keys {
            Some(keys) => text(keys)
                .font(KEYS_TYPE)
                .color(t.text_tertiary)
                .single_line(),
            None => rune::el(),
        },
    ]
}

const KEYS_TYPE: rune::TextStyle = rune::TextStyle::new(10.0, 400.0).mono();

/// What the queue owes, as the head and the collapsed bar say it: "2 waiting · 1 unreachable".
pub(crate) fn owed_label((waiting, unreachable): (usize, usize)) -> Option<String> {
    let parts: Vec<String> = [(waiting, "waiting"), (unreachable, "unreachable")]
        .into_iter()
        .filter(|(n, _)| *n > 0)
        .map(|(n, said)| format!("{n} {said}"))
        .collect();
    (!parts.is_empty()).then(|| parts.join(" · "))
}

fn waiting_head(
    key: String,
    (waiting, unreachable): (usize, usize),
    keys: Option<String>,
    t: &Tokens,
) -> El {
    let label = owed_label((waiting, unreachable)).unwrap_or_else(|| "Nothing owed".into());
    let ink = if waiting == 0 {
        t.text_muted
    } else {
        t.warning
    };
    let description = says("Open the oldest turn", keys.as_ref());
    let tokens = *t;
    Button::new(label.clone())
        .disabled(waiting == 0)
        .variant(ButtonVariant::Ghost)
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::actions::jump_to_waiting(state, tx))
        .label_with(move |_, _| {
            row()
                .w_full()
                .items_center()
                .gap(space::S1)
                .child(text(label).font(type_::SECTION).color(ink))
                .children(rule_tail(keys, &tokens))
        })
        .build(attn_ui::kit(t))
        .key(key)
        .description(description)
        .w_full()
        .h(ROW_HEIGHT)
        .px(0.0)
        .justify_start()
        .radius(4.0)
        .hover(t.bg_elevated)
}

fn agents_toggle(key: String, open: bool, more: usize, keys: Option<String>, t: &Tokens) -> El {
    let label = match more {
        0 => "No more agents".to_owned(),
        1 => "1 more agent".to_owned(),
        n => format!("{n} more agents"),
    };
    let tokens = *t;
    let description = says("Show every agent in this profile", keys.as_ref());
    Button::new(label.clone())
        .variant(ButtonVariant::Ghost)
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::actions::toggle_agent_list(state, tx))
        .label_with(move |_, _| {
            row()
                .w_full()
                .items_center()
                .gap(space::S1)
                .child(
                    text(if open {
                        attn_ui::icon::CARET_DOWN
                    } else {
                        attn_ui::icon::CARET_RIGHT
                    })
                    .font(type_::LABEL)
                    .color(tokens.text_tertiary),
                )
                .child(text(label).font(type_::SECTION).color(tokens.text_tertiary))
                .children(rule_tail(keys, &tokens))
        })
        .build(attn_ui::kit(t))
        .expanded(open)
        .key(key)
        .description(description)
        .w_full()
        .h(ROW_HEIGHT)
        .px(0.0)
        .justify_start()
        .radius(4.0)
        .hover(t.bg_elevated)
}

fn filter(state: &State, key: String, t: &Tokens) -> El {
    row()
        .key(key)
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S1)
        .child(
            text(attn_ui::icon::SEARCH)
                .font(type_::BODY)
                .color(t.text_tertiary),
        )
        .child(
            TextInput::new(&state.window.agent_filter)
                .key(FILTER_FIELD)
                .plain()
                .text_style(type_::BODY)
                .height(type_::BODY.line_px())
                .width(None)
                .label("Filter agents")
                .placeholder("filter agents")
                .on_edit(|state: &mut State| &mut state.window.agent_filter)
                .on_key(super::actions::filter_key)
                .build(attn_ui::kit(t))
                .grow(1.0),
        )
}

fn queue_row(key: String, a: &QueueAgent, cursor: bool, logos: bool, t: &Tokens) -> El {
    let id = a.agent.id.clone();
    let name = a.agent.shown_name();
    let trailing = if a.closing {
        "closing".to_owned()
    } else if !a.age.is_empty() {
        a.age.clone()
    } else {
        a.snoozed_until.clone()
    };
    let row = row()
        .key(key.clone())
        .role("listitem")
        .label(format!("Open {}", a.agent.name))
        .a11y_value(a.agent.state.wire())
        .help(a.agent.hover())
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S2)
        .px(space::S1)
        .radius(4.0)
        .focus_selected(cursor)
        .fill(super::row_fill(&a.agent, t))
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
        .child(lead(&key, &a.agent, logos, t))
        .child(
            text(name)
                .font(type_::BODY)
                .color(t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0),
        )
        .children(crate::asks::marker(&key, a.agent.asks, t))
        .children((!a.slot.is_empty()).then(|| {
            text(a.slot.clone())
                .key(format!("{key}.slot"))
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }))
        .children((!trailing.is_empty()).then(|| {
            text(trailing)
                .key(format!("{key}.age"))
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }))
        .hover_group()
        .children(row_controls(&key, a, t));
    if cursor {
        row.focus_ring(2.0, 0.0, t.border_focus)
    } else {
        row
    }
}

/// The buttons a queue row shows while it is hovered or holds focus.
fn row_controls(key: &str, a: &QueueAgent, t: &Tokens) -> Option<El> {
    let id = &a.agent.id;
    let name = &a.agent.name;
    let settle = a.settle.then(|| {
        let id = id.clone();
        control(
            key,
            "settle",
            format!("Settle {name}"),
            "✓",
            t,
            move |state, _| super::actions::settle(state, &id),
        )
    });
    let unsnooze = a.unsnooze.then(|| {
        let id = id.clone();
        control(
            key,
            "unsnooze",
            format!("Unsnooze {name}"),
            "↩",
            t,
            move |state, _| super::actions::wake(state, &id),
        )
    });
    let snooze = a.snooze.then(|| {
        let (id, name, under) = (id.clone(), name.clone(), format!("{key}.snooze"));
        control(
            key,
            "snooze",
            format!("Snooze {name}"),
            attn_ui::icon::MOON,
            t,
            move |state, tx| super::snooze::open(state, &id, name.clone(), Some(under.clone()), tx),
        )
    });
    let chief = a.chief;
    let ask_close = a.member.as_ref().map(|(member, name)| {
        let member = member.clone();
        control(
            key,
            "ask-close",
            format!("Ask {name} to close"),
            attn_ui::icon::MOON,
            t,
            move |state, tx| {
                if chief {
                    crate::chief::close(state, tx);
                } else {
                    crate::crew::ask_to_close(state, &member, tx);
                }
            },
        )
    });
    Some(
        row()
            .key(format!("{key}.controls"))
            .gap(2.0)
            .group_opacity(0.0, 1.0)
            .children(ask_close)
            .children(unsnooze)
            .children(snooze)
            .child(super::actions_button(key, &a.agent, t))
            .children(settle),
    )
}

pub(super) fn control(
    row_key: &str,
    name: &str,
    label: String,
    glyph: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    GlyphButton::new(glyph, label)
        .font(type_::LABEL)
        .side(20.0)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(format!("{row_key}.{name}"))
        .radius(4.0)
        .hover(t.bg_button)
}
