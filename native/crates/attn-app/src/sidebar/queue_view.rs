use std::rc::Rc;

use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{El, GrowthSpec, ScrollSpec, Tx, col, memo, row, text, text_input};
use rune_kit::{Button, ButtonVariant};

use super::model::{AgentRow, display_name, ordered_desktops};
use super::queue::{self, Bands, QueueRow};
use super::{ROW_HEIGHT, ROWS_BUILT, home_row, open_agent, state_mark};
use crate::State;

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
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum QLine {
    Rule { key: &'static str, text: String },
    WaitingHead { waiting: usize },
    AgentsToggle { open: bool, more: usize },
    Counts(String),
    Filter,
    Empty(&'static str),
    Agent(QueueAgent),
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
) -> QueueAgent {
    let session = row.session;
    let actions = queue::actions(&state.facts, session, now_ms);
    QueueAgent {
        band,
        agent: {
            let row = AgentRow::of(
                session,
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
        unsnooze: band == Band::Snoozed,
    }
}

/// The queue sidebar's body, top to bottom: the crew, the waiting card and its turns, the
/// agent list's toggle, and with the list open its filter, working and snoozed agents.
pub fn lines(state: &State, bands: &Bands<'_>) -> Vec<QLine> {
    let now_ms = state.window.now_ms;
    let window = &state.window;
    let on_screen = crate::desktop::on_screen_sessions(state);
    let mut lines = Vec::new();
    let chief = bands.chief.as_ref();
    if chief.is_some() || !bands.crew.is_empty() {
        lines.push(QLine::Rule {
            key: "crew",
            text: "Crew".into(),
        });
        if let Some(chief) = chief {
            lines.push(QLine::Agent(agent(
                state,
                &on_screen,
                Band::Chief,
                chief,
                now_ms,
            )));
        }
        let chief_member = chief.and_then(|c| c.session.crew_member.clone());
        let mut seen = std::collections::HashSet::new();
        for row in &bands.crew {
            let member = row.session.crew_member.clone().unwrap_or_default();
            if Some(&member) == chief_member.as_ref() || !seen.insert(member) {
                continue;
            }
            lines.push(QLine::Agent(agent(
                state,
                &on_screen,
                Band::Crew,
                row,
                now_ms,
            )));
        }
    }
    let pinned: std::collections::HashSet<&str> =
        bands.crew.iter().map(|r| r.session.id.as_str()).collect();
    lines.push(QLine::WaitingHead {
        waiting: bands.turns.len(),
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
            .child(home_row(window.view == crate::shell::View::Home, &t))
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
    memo(key.clone(), (line, draining, cursor, *t), || {
        ROWS_BUILT.with(|n| n.set(n.get() + 1));
        match line {
            QLine::Rule { text: label, .. } => rule(key, label, t),
            QLine::WaitingHead { waiting } => waiting_head(key, *waiting, t),
            QLine::AgentsToggle { open, more } => agents_toggle(key, *open, *more, t),
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
                queue_row(key, agent, cursor, t).children(bar)
            }
            QLine::Filter => unreachable!("the filter is built above"),
        }
    })
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

fn waiting_head(key: String, waiting: usize, t: &Tokens) -> El {
    let label = if waiting == 0 {
        "Nothing owed".to_owned()
    } else {
        format!("{waiting} waiting")
    };
    let ink = if waiting == 0 {
        t.text_muted
    } else {
        t.warning
    };
    Button::new(label.clone())
        .disabled(waiting == 0)
        .variant(ButtonVariant::Ghost)
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::actions::jump_to_waiting(state, tx))
        .label_with(move |_, _| text(label).font(type_::SECTION).color(ink))
        .build(attn_ui::kit(t))
        .key(key)
        .w_full()
        .h(ROW_HEIGHT)
        .px(0.0)
        .justify_start()
        .radius(4.0)
        .hover(t.bg_elevated)
}

fn agents_toggle(key: String, open: bool, more: usize, t: &Tokens) -> El {
    let label = match more {
        0 => "No more agents".to_owned(),
        1 => "1 more agent".to_owned(),
        n => format!("{n} more agents"),
    };
    let tokens = *t;
    Button::new(label.clone())
        .expanded(open)
        .variant(ButtonVariant::Ghost)
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::actions::toggle_agent_list(state, tx))
        .label_with(move |_, _| {
            row()
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
        })
        .build(attn_ui::kit(t))
        .key(key)
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
            text_input(&state.window.agent_filter, type_::BODY)
                .key(FILTER_FIELD)
                .single_line()
                .label("Filter agents")
                .placeholder("filter agents", t.text_muted)
                .on_edit(|state: &mut State| &mut state.window.agent_filter)
                .on_input_key(super::actions::filter_key)
                .grow(1.0)
                .color(t.text_primary),
        )
}

fn queue_row(key: String, a: &QueueAgent, cursor: bool, t: &Tokens) -> El {
    let id = a.agent.id.clone();
    let name = a.agent.shown_name();
    let trailing = if !a.age.is_empty() {
        a.age.clone()
    } else {
        a.snoozed_until.clone()
    };
    let row = row()
        .key(key.clone())
        .role("listitem")
        .label(format!("Open {}", a.agent.name))
        .a11y_value(a.agent.state.wire())
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S2)
        .px(space::S1)
        .radius(4.0)
        .focus_selected(cursor)
        .fill(if a.agent.selected {
            t.bg_selected
        } else {
            t.bg_panel
        })
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
        .child(state_mark(&key, &a.agent, t))
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
            move |state, tx| {
                super::snooze::open(state, &id, name.clone(), Some(under.clone()), true, tx)
            },
        )
    });
    Some(
        row()
            .key(format!("{key}.controls"))
            .gap(2.0)
            .group_opacity(0.0, 1.0)
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
