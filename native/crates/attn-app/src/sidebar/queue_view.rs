use std::collections::HashSet;

use attn_ui::Tokens;
use attn_ui::blocks::Inline;
use attn_ui::system::{self, GroupHead, IconSize, ListRow, Type, words};
use rune::{El, ScrollSpec, Tx, col, memo, row};
use rune_kit::ScrollBar;
use rune_kit::TextInput;

use super::model::{AgentRow, AgentState, display_name, ordered_desktops};
use super::queue::{self, Bands, QueueRow};
use super::{ROWS_BUILT, home_row, lead, open_agent};
use crate::State;
use crate::action::Action;
use crate::desktop::header_pop::{Marked, Shown};

pub(crate) const FILTER_FIELD: &str = "queue.filter.field";
pub(crate) const LIST: &str = "sidebar.queue";
const RUNS: &str = "queue.runs";

/// Where an agent's row sits in the queue sidebar, which sets the buttons it carries.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Band {
    Turn,
    Working,
    Snoozed,
    Run,
}

impl Band {
    fn prefix(self) -> &'static str {
        match self {
            Band::Turn => "queue-turn",
            Band::Working => "queue-settled",
            Band::Snoozed => "queue-snoozed",
            Band::Run => "queue-run",
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

impl QueueAgent {
    pub(crate) fn key(&self) -> String {
        format!("{}.{}", self.band.prefix(), self.agent.id)
    }
}

/// A queue row's key and the agent it shows.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct AgentLine {
    pub key: String,
    pub session: String,
}

/// The flat Automations section in the queue sidebar: direct automation runs across desktops,
/// split into active rows shown by default and finished off-screen runs folded behind a toggle.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct QueueAutomations {
    pub active: Vec<QueueAgent>,
    pub finished: Vec<QueueAgent>,
    /// Direct runs currently needing the user (including superseded ones), matching ⌘⇧J.
    pub needing: usize,
}

impl QueueAutomations {
    fn is_empty(&self) -> bool {
        self.active.is_empty() && self.finished.is_empty() && self.needing == 0
    }
}

/// Direct automation runs on the selected profile's desktops, flattened into active and finished
/// lists matching desktop flow's Automations section. Delegates stay in the main queue.
pub(crate) fn automations(state: &State) -> QueueAutomations {
    let on_screen = crate::desktop::on_screen_sessions(state);
    queue_automations(state, &on_screen, state.window.now_ms)
}

fn queue_automations(state: &State, on_screen: &[String], now_ms: i64) -> QueueAutomations {
    let groups = crate::automations::runs::on_desktops(&state.facts, now_ms);
    if groups.is_empty() {
        return QueueAutomations::default();
    }
    let places = queue::Places::of(&state.facts);
    let needing: usize = groups.iter().map(|g| g.needing_you.len()).sum();
    let mut seen: HashSet<&str> = HashSet::new();
    let mut active: Vec<(u8, Option<&str>, QueueAgent)> = Vec::new();
    let mut finished: Vec<(&str, QueueAgent)> = Vec::new();

    for group in &groups {
        for run in &group.runs {
            if !seen.insert(run.id.as_str()) {
                continue;
            }
            let desktop = places.desktop_of.get(run.id.as_str()).copied();
            let qrow = QueueRow {
                session: run,
                desktop,
                desktop_title: String::new(),
                offline: crate::outpost::offline(run, &state.facts.outposts),
            };
            let qa = *agent(state, on_screen, Band::Run, &qrow, now_ms);
            let needs = crate::automations::runs::needs_you(run, &state.facts.outposts, now_ms);
            let snoozed = queue::is_snoozed(run.turn_snoozed_until.as_deref(), now_ms);
            let wants_attention = needs || (!snoozed && qa.agent.asks > 0);
            let live = matches!(
                qa.agent.state,
                AgentState::Launching
                    | AgentState::Working
                    | AgentState::Scheduled
                    | AgentState::Recoverable
            );
            if wants_attention {
                active.push((0, run.turn_opened_at.as_deref(), qa));
            } else if live {
                active.push((1, None, qa));
            } else if qa.agent.selected || on_screen.contains(&run.id) {
                active.push((2, None, qa));
            } else {
                finished.push((run.state_since.as_str(), qa));
            }
        }
    }

    active.sort_by_key(|(phase, opened_at, _)| {
        (
            *phase,
            crate::automations::runs::turn_opened_order(*opened_at),
        )
    });
    finished.sort_by(|a, b| b.0.cmp(a.0).then_with(|| a.1.agent.id.cmp(&b.1.agent.id)));

    QueueAutomations {
        active: active.into_iter().map(|(_, _, qa)| qa).collect(),
        finished: finished.into_iter().map(|(_, qa)| qa).collect(),
        needing,
    }
}

struct QueueSections {
    waiting: usize,
    unreachable: usize,
    turns: Vec<QueueAgent>,
    more: usize,
    counts_summary: String,
    working: Vec<QueueAgent>,
    snoozed: Vec<QueueAgent>,
    automations: QueueAutomations,
}

fn sections(state: &State, bands: &Bands<'_>) -> QueueSections {
    let now_ms = state.window.now_ms;
    let window = &state.window;
    let on_screen = crate::desktop::on_screen_sessions(state);
    let pinned: HashSet<&str> = bands.crew.iter().map(|r| r.session.id.as_str()).collect();
    let (waiting, unreachable) = bands.owed();
    let turns = bands
        .turns
        .iter()
        .map(|row| *agent(state, &on_screen, Band::Turn, row, now_ms))
        .collect();
    let all_working: Vec<&QueueRow<'_>> = bands
        .settled
        .iter()
        .filter(|r| !pinned.contains(r.session.id.as_str()))
        .collect();
    let all_snoozed: Vec<&QueueRow<'_>> = bands
        .snoozed
        .iter()
        .filter(|r| !pinned.contains(r.session.id.as_str()))
        .collect();
    let more = all_working.len() + all_snoozed.len();
    let counts_summary = [
        (all_working.len(), "working"),
        (all_snoozed.len(), "snoozed"),
    ]
    .into_iter()
    .filter(|(n, _)| *n > 0)
    .map(|(n, label)| format!("{n} {label}"))
    .collect::<Vec<_>>()
    .join(" · ");

    let (working, snoozed) = if window.agent_list_open {
        let filter = window.agent_filter.text.to_lowercase();
        let matches = |r: &&QueueRow<'_>| {
            filter.is_empty()
                || display_name(r.session, &state.facts.harnesses)
                    .to_lowercase()
                    .contains(&filter)
        };
        let working = all_working
            .into_iter()
            .filter(matches)
            .map(|row| *agent(state, &on_screen, Band::Working, row, now_ms))
            .collect();
        let snoozed = all_snoozed
            .into_iter()
            .filter(matches)
            .map(|row| *agent(state, &on_screen, Band::Snoozed, row, now_ms))
            .collect();
        (working, snoozed)
    } else {
        (Vec::new(), Vec::new())
    };

    let automations = queue_automations(state, &on_screen, now_ms);
    QueueSections {
        waiting,
        unreachable,
        turns,
        more,
        counts_summary,
        working,
        snoozed,
        automations,
    }
}

fn section_agent_lines(sections: &QueueSections, finished_open: bool) -> Vec<AgentLine> {
    let to_line = |a: &QueueAgent| AgentLine {
        key: a.key(),
        session: a.agent.id.clone(),
    };
    sections
        .turns
        .iter()
        .chain(&sections.working)
        .chain(&sections.snoozed)
        .chain(&sections.automations.active)
        .chain(
            finished_open
                .then_some(sections.automations.finished.iter())
                .into_iter()
                .flatten(),
        )
        .map(to_line)
        .collect()
}

/// The agent rows of the queue sidebar, top to bottom, as the keyboard walks them.
pub(crate) fn agent_rows(state: &State) -> Vec<AgentLine> {
    let bands = queue::bands(&state.facts, state.window.now_ms);
    let data = sections(state, &bands);
    section_agent_lines(&data, state.window.automations_finished_open)
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
                &state.facts.harnesses,
                &state.facts.outposts,
                state.window.on_screen(&state.facts).as_deref() == Some(session.id.as_str()),
            )
            .asking(&state.facts, session)
            .chained(session, &state.facts);
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
        settle: matches!(band, Band::Turn | Band::Run) && actions.settle,
        snooze: matches!(band, Band::Turn | Band::Working) && actions.snooze,
        unsnooze: band == Band::Snoozed && !row.offline,
    })
}

pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let window = &state.window;
    let marked = Marked::of(state);
    let logos_on = super::logos(state);
    let mut countdowns: Vec<(String, String)> = window
        .countdowns
        .iter()
        .map(|(id, c)| (id.clone(), c.fires_at.clone()))
        .collect();
    countdowns.sort();
    let reads = (
        (
            std::sync::Arc::as_ptr(&facts.sessions) as usize,
            std::sync::Arc::as_ptr(&facts.desktops) as usize,
            std::sync::Arc::as_ptr(&facts.profiles) as usize,
            std::sync::Arc::as_ptr(&facts.settings) as usize,
            std::sync::Arc::as_ptr(&facts.asks) as usize,
        ),
        (
            std::sync::Arc::as_ptr(&facts.garden) as usize,
            std::sync::Arc::as_ptr(&facts.outposts) as usize,
            std::sync::Arc::as_ptr(&facts.crew) as usize,
            std::sync::Arc::as_ptr(&facts.harnesses) as usize,
            std::sync::Arc::as_ptr(&state.keymap) as usize,
            logos_on,
            state.logos.revision(),
            window.crew_rows.armed(),
            crate::chief::view(state),
        ),
        (
            window.view,
            window.agent_list_open,
            window.automations_finished_open,
        ),
        (
            &window.agent_filter,
            window.queue_cursor.clone(),
            countdowns,
        ),
        window.now_ms,
        (crate::desktop::on_screen_sessions(state), marked.clone()),
        super::width::of(state).to_bits(),
        window.desk.drag.as_ref().and_then(|d| d.desktop_marks()),
        (
            *t,
            facts.notifications.unread_critical,
            facts.notifications.critical_title.clone(),
        ),
    );
    memo("sidebar", reads, || {
        let crew_entries = super::crew::desktop_entries(state);
        let has_crew = !crew_entries.is_empty();
        let bands = queue::bands(facts, window.now_ms);
        let data = sections(state, &bands);
        let rows = section_agent_lines(&data, window.automations_finished_open);
        let cursor: Option<String> = window
            .queue_cursor
            .as_ref()
            .and_then(|cursor| reached(&rows, cursor))
            .map(|at| rows[at].key.clone());
        let jump_keys = keys_of(state, &Action::SessionJumpToWaiting);
        let list_keys = keys_of(state, &Action::SidebarAgentList);
        let next_run_keys = keys_of(state, &Action::SessionNextRun);
        let logo_rev = if logos_on { state.logos.revision() } else { 0 };
        let t = *t;

        let row_el = |qa: &QueueAgent, in_well: bool| {
            memo_queue_row(
                state,
                qa,
                in_well,
                cursor.as_deref() == Some(qa.key().as_str()),
                marked.shown(&qa.agent.id),
                logos_on,
                logo_rev,
                &t,
            )
        };

        let has_owed = data.waiting > 0 || data.unreachable > 0;
        let waiting_block = if has_owed {
            ListRow::well("queue.waiting-well", true, &t)
                .child(waiting_head(
                    "queue.waiting-head".into(),
                    (data.waiting, data.unreachable),
                    jump_keys,
                    true,
                    &t,
                ))
                .children(data.turns.iter().map(|qa| row_el(qa, true)))
        } else {
            waiting_head("queue.waiting-head".into(), (0, 0), jump_keys, false, &t)
        };

        let mut queue_children: Vec<El> = vec![waiting_block];
        queue_children.push(gap("queue.agents-toggle.gap"));
        queue_children.push(agents_toggle(
            "queue.agents-toggle".into(),
            window.agent_list_open,
            data.more,
            &data.counts_summary,
            list_keys,
            &t,
        ));
        if window.agent_list_open {
            queue_children.push(
                rune::el()
                    .key("queue.filter.gap")
                    .w_full()
                    .h(ListRow::well_pad_y())
                    .a11y_hidden(),
            );
            queue_children.push(filter(state, "queue.filter".into(), &t));
            queue_children.push(gap("queue.rule.working.gap"));
            queue_children.push(rule(
                "queue.rule.working".into(),
                &format!("Working {}", data.working.len()),
                &t,
            ));
            if data.working.is_empty() {
                queue_children.push(ListRow::note(
                    "queue.empty.working",
                    "Nobody else.",
                    Type::Small,
                    t.text_muted,
                    None,
                    &t,
                ));
            } else {
                queue_children.extend(data.working.iter().map(|qa| row_el(qa, false)));
            }
            if !data.snoozed.is_empty() {
                queue_children.push(gap("queue.rule.snoozed.gap"));
                queue_children.push(rule(
                    "queue.rule.snoozed".into(),
                    &format!("Snoozed {}", data.snoozed.len()),
                    &t,
                ));
                queue_children.extend(data.snoozed.iter().map(|qa| row_el(qa, false)));
            }
        }
        if !data.automations.is_empty() {
            let finished_count = data.automations.finished.len();
            let finished_open = window.automations_finished_open;
            queue_children.push(gap("queue.runs.gap"));
            queue_children.push(runs_head(
                RUNS.into(),
                data.automations.needing,
                next_run_keys,
                &t,
            ));
            queue_children.extend(data.automations.active.iter().map(|qa| row_el(qa, false)));
            if finished_count > 0 {
                queue_children.push(super::finished_runs_toggle(
                    finished_count,
                    finished_open,
                    &t,
                ));
            }
            if finished_open {
                queue_children.extend(data.automations.finished.iter().map(|qa| row_el(qa, false)));
            }
        }

        let list = col()
            .key(LIST)
            .role("list")
            .label("Queue")
            .w_full()
            .focusable(true)
            .on_key(super::actions::list_key)
            .children(queue_children);
        let list = match cursor {
            Some(key) => list.active_descendant(key),
            None => list,
        };
        col()
            .key("sidebar")
            .role("navigation")
            .label("Sidebar")
            .w(super::width::of(state))
            .h_full()
            .fill(t.bg_panel)
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
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
                ListRow::column()
                    .key("sidebar.scroll")
                    .grow(1.0)
                    .min_h(0.0)
                    .scroll(ScrollSpec {
                        stick_bottom: false,
                    })
                    .children(has_crew.then(|| {
                        col()
                            .key("sidebar.crew.section")
                            .w_full()
                            .child(crew_rule("sidebar.rule.crew".into(), &t))
                            .child(super::crew::pill_bar(state, &crew_entries, logos_on, &t))
                    }))
                    .children(has_crew.then(|| gap("sidebar.rule.queue.gap")))
                    .child(list)
                    .scroll_bar_styled(attn_ui::scroll_bar(&t)),
            )
            .child(super::chips::desktop_strip(state, &bands, &t))
    })
}

fn gap(key: &str) -> El {
    rune::el()
        .key(key)
        .w_full()
        .h(ListRow::section_gap())
        .a11y_hidden()
}

#[allow(clippy::too_many_arguments)]
fn memo_queue_row(
    state: &State,
    a: &QueueAgent,
    in_well: bool,
    cursor: bool,
    shown: Shown,
    logos: bool,
    logo_rev: u64,
    t: &Tokens,
) -> El {
    let key = a.key();
    let draining = state.window.countdowns.contains_key(&a.agent.id);
    memo(
        key.clone(),
        (a, in_well, draining, cursor, shown, logos, logo_rev, *t),
        || {
            ROWS_BUILT.with(|n| n.set(n.get() + 1));
            let bar = super::settling_bar(state, &key, &a.agent, t);
            queue_row(state, key.clone(), a, in_well, cursor, shown, logos, t).children(bar)
        },
    )
}

pub(super) fn crew_rule(key: String, t: &Tokens) -> El {
    let manage = format!("{key}.manage");
    GroupHead::new(key, "Crew")
        .heading()
        .section("Crew")
        .action(
            manage,
            "manage",
            "Manage crew",
            |state: &mut State, tx: &mut Tx| crate::crew::open(state, None, tx),
        )
        .build(t)
}

fn rule(key: String, label: &str, t: &Tokens) -> El {
    GroupHead::new(key, label)
        .heading()
        .section(label.to_owned())
        .build(t)
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
    in_well: bool,
    t: &Tokens,
) -> El {
    let label = owed_label((waiting, unreachable)).unwrap_or_else(|| "Nothing owed".into());
    let description = says("Open the oldest turn", keys.as_ref());
    let surface = if in_well {
        ListRow::well_surface(t)
    } else {
        t.bg_panel
    };
    GroupHead::new(key, label)
        .on(surface)
        .name_el(owed_words((waiting, unreachable), t))
        .keys(keys)
        .disabled(waiting == 0)
        .build(t)
        .description(description)
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::actions::jump_to_waiting(state, tx))
}

/// What the queue owes: the number of waiting turns in bold warning, the words in primary, and
/// an unreachable count in tertiary.
fn owed_words((waiting, unreachable): (usize, usize), t: &Tokens) -> El {
    if waiting == 0 && unreachable == 0 {
        return words("Nothing owed", Type::Body, t.text_tertiary);
    }
    let mut parts = Vec::new();
    if waiting > 0 {
        parts.push(words(waiting.to_string(), Type::Heading, t.warning));
        parts.push(words("waiting", Type::Name, t.text_primary));
    }
    if unreachable > 0 {
        if waiting > 0 {
            parts.push(words("·", Type::Name, t.text_tertiary));
        }
        parts.push(words(
            format!("{unreachable} unreachable"),
            Type::Name,
            t.text_tertiary,
        ));
    }
    Inline::joined().children(parts)
}

fn agents_toggle(
    key: String,
    open: bool,
    more: usize,
    counts_summary: &str,
    keys: Option<String>,
    t: &Tokens,
) -> El {
    let label = match more {
        0 => "No more agents".to_owned(),
        1 => "1 more agent".to_owned(),
        n => format!("{n} more agents"),
    };
    let section = if more == 0 {
        "No more agents".to_owned()
    } else {
        format!("{more} more")
    };
    let description = says("Show every agent in this profile", keys.as_ref());
    let mut head = GroupHead::new(key, label)
        .open(open)
        .section(section)
        .keys(keys);
    if !open && !counts_summary.is_empty() {
        head = head.aside(
            words(counts_summary.to_owned(), Type::Small, t.text_tertiary)
                .key("queue.agents-counts")
                .single_line()
                .truncate()
                .shrink()
                .min_w(0.0),
        );
    }
    head.build(t)
        .description(description)
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::actions::toggle_agent_list(state, tx))
}

/// The Automations section heading in queue flow: matches desktop flow's header with an inline
/// waiting pill (`⌘⇧J`) and `all` action to open the Automations drawer.
fn runs_head(key: String, needing: usize, keys: Option<String>, t: &Tokens) -> El {
    let mut head = GroupHead::new(key, "Automations")
        .heading()
        .section("Automations")
        .action(
            format!("{RUNS}.all"),
            "all",
            "Open Automations",
            |state: &mut State, tx: &mut Tx| crate::automations::open(state, tx),
        );
    if needing > 0 {
        head = head.aside(super::automations_needing_pill(
            format!("{RUNS}.needing"),
            needing,
            keys,
            crate::automations::runs::next_run,
            t,
        ));
    }
    head.build(t).help("Automation runs never join the queue")
}

/// The field that narrows the agent list as it is typed into.
fn filter(state: &State, key: String, t: &Tokens) -> El {
    let input = TextInput::new(&state.window.agent_filter)
        .key(FILTER_FIELD)
        .label("Filter agents")
        .placeholder("filter agents")
        .on_edit(|state: &mut State| &mut state.window.agent_filter)
        .on_key(super::actions::filter_key);
    row()
        .key(key)
        .w_full()
        .h(ListRow::height())
        .items_center()
        .child(El::from(system::search_field(input, t)))
}

#[allow(clippy::too_many_arguments)]
fn queue_row(
    state: &State,
    key: String,
    a: &QueueAgent,
    in_well: bool,
    cursor: bool,
    shown: Shown,
    logos: bool,
    t: &Tokens,
) -> El {
    let id = a.agent.id.clone();
    let surface = if in_well {
        ListRow::well_surface(t)
    } else {
        t.bg_panel
    };
    let ground = if a.agent.selected {
        if in_well {
            t.bg_selected
        } else {
            t.bg_elevated
        }
    } else {
        surface
    };
    let trailing = if !a.age.is_empty() {
        a.age.clone()
    } else {
        a.snoozed_until.clone()
    };
    let (title_kind, title_ink) = if a.band == Band::Turn {
        let needs_you = matches!(
            a.agent.state,
            AgentState::WaitingInput | AgentState::PendingApproval
        ) || a.agent.asks > 0;
        if a.agent.selected || needs_you {
            (Type::Name, t.text_primary)
        } else {
            (Type::Body, t.text_primary)
        }
    } else {
        super::title_style(&a.agent, false, t)
    };
    let name = super::flagged(&key, &a.agent, t).shrink().child(
        words(a.agent.name.clone(), title_kind, title_ink)
            .single_line()
            .truncate()
            .shrink(),
    );
    let mut row = ListRow::new(key.clone(), format!("Open {}", a.agent.name))
        .on(surface)
        .role("listitem")
        .unfocusable()
        .cursor(cursor)
        .lead(badged_lead(state, &key, a, logos, ground, t))
        .title_el(name)
        .in_view(a.agent.selected)
        .tint(super::row_tint(&a.agent, shown, t))
        .hover_controls(row_controls(&key, a));
    if let Some(chain) = &a.agent.chain {
        row = crate::desktop::chain::on_row(row, &key, &a.agent.id, chain, t);
    }
    if a.band == Band::Run
        && let Some(run) = &a.agent.run
    {
        row = row.badge(crate::desktop::provenance::run_view(&key, run, t));
    }
    if let Some(asks) = crate::asks::marker(&key, a.agent.asks, t) {
        row = row.aside(asks);
    }
    if !trailing.is_empty() {
        row = row.aside(words(trailing, Type::Mono, t.text_dimmed).key(format!("{key}.age")));
    }
    let row = row
        .build(t)
        .a11y_value(a.agent.state.wire())
        .help(a.agent.hover())
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx));
    let row = super::outlined(row, shown, t);
    match &a.agent.chain {
        Some(_) => crate::desktop::chain::row_hovers(row, &key, &a.agent.id),
        None => row,
    }
}

/// A queue row's lead with its desktop's slot tucked under its lower right corner: it takes no
/// room in the row.
fn badged_lead(
    state: &State,
    key: &str,
    a: &QueueAgent,
    logos: bool,
    ground: rune::Color,
    t: &Tokens,
) -> El {
    row()
        .center()
        .child(lead(state, key, &a.agent, logos, ground, t))
        .children((!a.slot.is_empty() && a.band != Band::Run).then(|| {
            system::slot_badge(format!("{key}.slot"), a.slot.clone(), ground, t).pin(
                None,
                None,
                Some(-SLOT_OVERHANG),
                Some(-SLOT_OVERHANG),
            )
        }))
}

/// How far the desktop's slot hangs past its lead's corner.
const SLOT_OVERHANG: f32 = 4.0;

/// The buttons a queue row shows while it is hovered or holds focus.
fn row_controls(key: &str, a: &QueueAgent) -> Vec<system::IconButton> {
    let id = &a.agent.id;
    let name = &a.agent.name;
    let settle = a.settle.then(|| {
        let id = id.clone();
        control(
            key,
            "settle",
            format!("Settle {name}"),
            "✓",
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
            move |state, _| super::actions::wake(state, &id),
        )
    });
    let snooze = a.snooze.then(|| {
        let (id, name, under) = (id.clone(), name.clone(), format!("{key}.snooze"));
        control(
            key,
            "snooze",
            format!("Snooze {name}"),
            attn_ui::glyph::MOON,
            move |state, tx| super::snooze::open(state, &id, name.clone(), Some(under.clone()), tx),
        )
    });
    unsnooze
        .into_iter()
        .chain(snooze)
        .chain([super::actions_button(key, &a.agent, None)])
        .chain(settle)
        .collect()
}

pub(super) fn control(
    row_key: &str,
    name: &str,
    label: String,
    glyph: impl Into<attn_ui::Glyph>,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> system::IconButton {
    system::icon_button(format!("{row_key}.{name}"), label, glyph, IconSize::Small)
        .on_press_tx(press)
}
