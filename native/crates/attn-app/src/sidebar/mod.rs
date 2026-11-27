pub(crate) mod actions;
pub(crate) mod bar_peek;
mod chips;
pub(crate) mod crew;
pub(crate) mod crowding;
pub(crate) mod dock;
pub(crate) mod model;
pub(crate) mod queue;
mod queue_view;
pub(crate) mod rename;
pub(crate) mod reorder;
pub(crate) mod session_menu;
pub(crate) mod snooze;
pub(crate) mod width;

use std::cell::Cell;

use attn_protocol::DesktopShowSessionMessage;
use attn_ui::Tokens;
use attn_ui::blocks::{self, Inline, MemberState};
use attn_ui::system::{self, DotShape, DotSize, GroupHead, IconSize, ListRow, Type, words};
use rune::{El, ScrollSpec, Tx, col, memo, row};
use rune_kit::ScrollBar;

pub(crate) use model::{AgentRow, Line, display_name, tile_icon, tile_title};
pub use model::{AgentState, SESSION_EMOJIS};
use model::{DesktopHeader, TileRow};

use crate::State;
use crate::action::Action;
use crate::desktop::DesktopMark;
use crate::desktop::header_pop::{Marked, Shown};
use crate::shell::View;

pub(crate) const HARNESS_LOGOS: &str = "sidebar_harness_logos_enabled";

thread_local! {
    static ROWS_BUILT: Cell<usize> = const { Cell::new(0) };
}

pub(crate) use queue_view::AgentLine;

/// The window's sidebar for its flow: the queue sidebar in queue flow, the desktop sidebar
/// otherwise. Collapsed, desktop flow keeps the rail and queue flow keeps nothing here,
/// since the queue bar moves into the title bar. An agent shown alone hides it in either flow.
pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    if crate::desktop::shows_an_agent_alone(state) {
        return None;
    }
    let sidebar = match (actions::queue_flow(state), state.window.sidebar_hidden) {
        (true, false) => queue_view::view(state, t),
        (true, true) => return None,
        (false, false) => desktop_sidebar(state, t),
        (false, true) => return Some(chips::rail(state, t)),
    };
    Some(
        row()
            .key("sidebar.frame")
            .h_full()
            .child(sidebar)
            .child(width::handle(t))
            .children(width::label_view(state, t)),
    )
}

/// What the title bar's middle holds around the tile shelf: the queue bar, in queue flow with
/// the sidebar collapsed, or only the shelf.
pub(crate) fn title_middle(state: &State, shelf: El, t: &Tokens) -> (El, crowding::GivenUp) {
    if actions::queue_flow(state) && state.window.sidebar_hidden {
        let bands = queue::bands(&state.facts, state.window.now_ms);
        chips::queue_bar(state, &bands, shelf, t)
    } else {
        let held = crowding::Contents::new(state, None);
        let given_up = crowding::given_up(state, &held);
        let shown = given_up < crowding::GivenUp::Shelf;
        let middle = rune::row()
            .items_center()
            .h_full()
            .children(shown.then(|| crowding::part(shelf, crowding::Part::Shelf)));
        (crowding::filled(middle, held, given_up), given_up)
    }
}

/// Runs the actions the sidebar, the queue and home own; false for any other.
pub(crate) fn dispatch(state: &mut State, action: &crate::Action, tx: &mut Tx) -> bool {
    use crate::Action;
    let queue = actions::queue_flow(state);
    match action {
        Action::SessionGoToDashboard => state.window.go_home(),
        Action::OpenAgent { session } => open_agent(state, session, tx),
        Action::SessionJumpToWaiting => actions::jump_to_waiting(state, tx),
        Action::SidebarAgentList
            if queue
                && !state.window.sidebar_hidden
                && !crate::desktop::shows_an_agent_alone(state) =>
        {
            actions::toggle_agent_list(state, tx)
        }
        Action::SidebarAgentList => crate::palette::open(state, crate::palette::Face::Agents, tx),
        Action::SessionSettle => actions::settle_key(state, tx),
        Action::RenameSession { session } => {
            let under = rename::Opener::Tile(tile_header_of(state, session));
            rename::session(state, session, under, tx)
        }
        Action::ReloadSession { session } => session_menu::reload_session(state, session, tx),
        Action::WakeSession { session } => actions::wake(state, session),
        Action::SessionPriority => actions::priority_key(state, tx),
        Action::SessionSnooze => actions::snooze_key(state, tx),
        Action::SessionCancelCountdown => actions::cancel_countdown(state),
        Action::SidebarWider => width::step_by_key(state, true, tx),
        Action::SidebarNarrower => width::step_by_key(state, false, tx),
        Action::SessionPrev if queue => actions::step(state, -1, tx),
        Action::SessionNext if queue => actions::step(state, 1, tx),
        _ => return false,
    }
    true
}

pub(crate) use actions::{advance, closing, queue_flow, settle, step, toggle_priority, wake};
pub(crate) use queue_view::{agent_rows as queue_rows, automations as queue_automations};
pub(crate) const DESKTOP_LIST: &str = "sidebar.desktops";
pub(crate) const AUTOMATIONS_LIST: &str = "sidebar.automations";

/// The next wall-clock moment a turn age or automation snooze the window shows reads differently.
pub(crate) fn next_change(state: &State, now_ms: i64) -> Option<i64> {
    if !actions::queue_flow(state) {
        let by_id: std::collections::HashMap<&str, &attn_protocol::Session> = state
            .facts
            .sessions
            .iter()
            .map(|s| (s.id.as_str(), s))
            .collect();
        return state
            .facts
            .sessions
            .iter()
            .filter(|s| {
                s.profile_id == state.facts.profiles.selected
                    && model::is_automation_session(s, &by_id)
            })
            .filter_map(|s| queue::epoch_ms(s.turn_snoozed_until.as_deref()?))
            .filter(|&at| at > now_ms)
            .min();
    }
    let age = queue::bands(&state.facts, now_ms)
        .turns
        .iter()
        .filter_map(|row| queue::next_age_change(row.session.turn_opened_at.as_deref(), now_ms))
        .min();
    let run_snooze = state
        .facts
        .sessions
        .iter()
        .filter(|s| s.profile_id == state.facts.profiles.selected && s.automation.is_some())
        .filter_map(|s| queue::epoch_ms(s.turn_snoozed_until.as_deref()?))
        .filter(|&at| at > now_ms)
        .min();
    match (age, run_snooze) {
        (Some(a), Some(b)) => Some(a.min(b)),
        (a, b) => a.or(b),
    }
}

/// How many sidebar rows this thread has built, memo misses only: a row whose model did not
/// change is reused and not counted.
pub fn rows_built() -> usize {
    ROWS_BUILT.with(Cell::get)
}

/// The element whose desktops take a dragged tile: the title bar, which holds the queue
/// bar's chips, in queue flow with the sidebar collapsed, the sidebar otherwise. The rail
/// takes none.
pub(crate) fn drop_scope(state: &State) -> &'static str {
    if actions::queue_flow(state) && state.window.sidebar_hidden {
        "titlebar"
    } else {
        "sidebar"
    }
}

/// The desktop a line or chip at element path `path` shows.
pub(crate) fn desktop_at(state: &State, path: &str) -> Option<String> {
    let slot = path.split('/').find_map(|segment| {
        ["queue.desktop.", "queue-bar.desktop."]
            .iter()
            .find_map(|prefix| segment.strip_prefix(prefix)?.parse::<i64>().ok())
    });
    if let Some(slot) = slot {
        let desktops = &state.facts.desktops;
        return desktops
            .iter()
            .find(|d| d.shortcut_slot == Some(slot))
            .map(|d| d.id.clone());
    }
    if path.split('/').any(|segment| segment == AUTOMATIONS_LIST) {
        return automation_row_at(state, path).map(|run| run.desktop);
    }
    let path = format!("/{path}/");
    lines(state)
        .into_iter()
        .find(|l| {
            path.contains(&format!("/{}/", l.key()))
                || path.contains(&format!("/sidebar.group.{}/", l.desktop()))
        })
        .map(|l| l.desktop().to_owned())
}

/// Whether the sidebar draws the agent row keyed `key` now.
pub(crate) fn draws_row(state: &State, key: &str) -> bool {
    if state.window.sidebar_hidden {
        return false;
    }
    if actions::queue_flow(state) {
        queue_view::agent_rows(state).iter().any(|r| r.key == key)
    } else {
        if lines(state).iter().any(|l| l.key() == key) {
            return true;
        }
        let section = automations(state);
        section
            .active
            .iter()
            .chain(
                state
                    .window
                    .automations_finished_open
                    .then_some(section.finished.iter())
                    .into_iter()
                    .flatten(),
            )
            .any(|run| format!("sidebar.session.{}", run.row.id) == key)
    }
}

/// The desktop sidebar's lines, in the order it shows them.
pub(crate) fn lines(state: &State) -> Vec<Line> {
    let home = state.window.view == View::Home;
    let on_screen = crate::desktop::on_screen_sessions(state);
    model::desktop_lines(
        &state.facts,
        Some(&state.window.garden),
        home,
        &on_screen,
        &crew::desktop_entries(state),
    )
}

/// The desktop sidebar's unified Automations section.
pub(crate) fn automations(state: &State) -> model::AutomationSection {
    let home = state.window.view == View::Home;
    let on_screen = crate::desktop::on_screen_sessions(state);
    model::desktop_automations(&state.facts, home, &on_screen, state.window.now_ms)
}

fn desktop_sidebar(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let home = state.window.view == View::Home;
    let on_screen = crate::desktop::on_screen_sessions(state);
    let drag_marks = state
        .window
        .desk
        .drag
        .as_ref()
        .and_then(|d| d.desktop_marks());
    let new_desktop = state
        .window
        .desk
        .drag
        .as_ref()
        .and_then(|d| d.new_desktop());
    let dock = dock::of(state);
    let home_keys = crate::palette::label_of(state, &Action::SessionGoToDashboard);
    let overview_keys = crate::palette::label_of(state, &Action::DesktopOverview);
    let next_run_keys = crate::palette::label_of(state, &Action::SessionNextRun);
    let by_id: std::collections::HashMap<&str, &attn_protocol::Session> =
        facts.sessions.iter().map(|s| (s.id.as_str(), s)).collect();
    let snoozed_automations = facts
        .sessions
        .iter()
        .filter(|s| {
            s.profile_id == facts.profiles.selected
                && model::is_automation_session(s, &by_id)
                && queue::is_snoozed(s.turn_snoozed_until.as_deref(), state.window.now_ms)
        })
        .count();
    let chief = crate::chief::view(state);
    let active_session = state.window.on_screen(facts);
    let marked = Marked::of(state);
    let logos_on = logos(state);
    let mut countdowns: Vec<(String, String)> = state
        .window
        .countdowns
        .iter()
        .map(|(id, c)| (id.clone(), c.fires_at.clone()))
        .collect();
    countdowns.sort();
    let waiting = crate::cold::waiting(facts);
    let reads = (
        (
            std::sync::Arc::as_ptr(&facts.sessions) as usize,
            std::sync::Arc::as_ptr(&facts.desktops) as usize,
            std::sync::Arc::as_ptr(&facts.profiles) as usize,
            std::sync::Arc::as_ptr(&facts.crew) as usize,
            facts.tile_contents.revision,
        ),
        (
            std::sync::Arc::as_ptr(&facts.asks) as usize,
            std::sync::Arc::as_ptr(&facts.garden) as usize,
            state.window.garden.revision,
            std::sync::Arc::as_ptr(&facts.outposts) as usize,
            std::sync::Arc::as_ptr(&facts.harnesses) as usize,
            std::sync::Arc::as_ptr(&state.keymap) as usize,
            logos_on,
            state.logos.revision(),
        ),
        (
            home,
            marked.clone(),
            countdowns,
            state.window.automations_finished_open,
            snoozed_automations,
        ),
        (on_screen.clone(), active_session),
        width::of(state).to_bits(),
        (
            state.window.reorder.clone(),
            drag_marks,
            new_desktop,
            (
                dock.clone(),
                home_keys.clone(),
                overview_keys.clone(),
                next_run_keys.clone(),
            ),
            (chief, state.window.crew_rows.armed()),
        ),
        (
            *t,
            facts.notifications.unread_critical,
            facts.notifications.critical_title.clone(),
            waiting.clone(),
        ),
    );
    memo("sidebar", reads, || {
        let crew_entries = crew::desktop_entries(state);
        let has_crew = !crew_entries.is_empty();
        let lines = model::desktop_lines(
            facts,
            Some(&state.window.garden),
            home,
            &on_screen,
            &crew_entries,
        );
        let automations = model::desktop_automations(facts, home, &on_screen, state.window.now_ms);
        let has_automations = !automations.active.is_empty() || !automations.finished.is_empty();
        let t = *t;
        col()
            .key("sidebar")
            .role("navigation")
            .label("Sidebar")
            .w(width::of(state))
            .h_full()
            .fill(t.bg_panel)
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
            .children(crate::notifications::critical_strip(
                &facts.notifications,
                &t,
            ))
            .child(home_row(home, home_keys.clone(), &t))
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
                            .child(queue_view::crew_rule("sidebar.rule.crew".into(), &t))
                            .child(crew::pill_bar(state, &crew_entries, logos_on, &t))
                    }))
                    .children(has_crew.then(|| {
                        rune::el()
                            .key("sidebar.rule.desktops.gap")
                            .w_full()
                            .h(ListRow::section_gap())
                            .a11y_hidden()
                    }))
                    .child(desktops_rule(overview_keys.clone(), &t))
                    .child(
                        rune::el()
                            .key("sidebar.desktops.gap")
                            .w_full()
                            .h(ListRow::well_pad_y())
                            .a11y_hidden(),
                    )
                    .child(
                        col()
                            .key(DESKTOP_LIST)
                            .role("list")
                            .label("Desktops")
                            .w_full()
                            .children(desktop_groups(state, &lines, &marked, logos_on, &t)),
                    )
                    .children(waiting.clone().map(|waiting| {
                        ListRow::wrapped_note(
                            "sidebar.waiting-for-home",
                            waiting,
                            attn_ui::system::Type::Small,
                            t.text_secondary,
                            &t,
                        )
                    }))
                    .children(has_automations.then(|| {
                        automations_section(
                            state,
                            &automations,
                            next_run_keys.clone(),
                            &marked,
                            logos_on,
                            &t,
                        )
                    }))
                    .children(new_desktop.map(|over| new_desktop_drop(over, &t)))
                    .scroll_bar_styled(attn_ui::scroll_bar(&t)),
            )
            .child(dock::view(&dock, &t))
    })
}

/// The Home row, under the title bar.
pub(crate) fn home_row(home: bool, keys: Option<String>, t: &Tokens) -> El {
    ListRow::column().child(
        nav_row("sidebar.home", &attn_ui::icon::HOME, "Home", keys, home, t)
            .a11y_value(if home { "current" } else { "" })
            .on_press(|state: &mut State| state.window.go_home()),
    )
}

fn desktops_rule(overview_keys: Option<String>, t: &Tokens) -> El {
    GroupHead::new("sidebar.rule.desktops", "Desktops")
        .heading()
        .section("Desktops")
        .action(
            "sidebar.overview",
            "overview",
            "Desktop overview",
            |state: &mut State, tx: &mut Tx| {
                crate::desktop::run(state, &Action::DesktopOverview, tx);
            },
        )
        .action_keys(overview_keys)
        .build(t)
}

fn automations_section(
    state: &State,
    section: &model::AutomationSection,
    next_run_keys: Option<String>,
    marked: &Marked,
    logos_on: bool,
    t: &Tokens,
) -> El {
    let logo_rev = if logos_on { state.logos.revision() } else { 0 };
    let build = RowBuild {
        marked,
        logos_on,
        logo_rev,
        in_current: false,
    };
    let finished_open = state.window.automations_finished_open;
    let finished_count = section.finished.len();
    col()
        .key("sidebar.automations.section")
        .w_full()
        .child(
            rune::el()
                .key("sidebar.rule.automations.gap")
                .w_full()
                .h(ListRow::section_gap())
                .a11y_hidden(),
        )
        .child(automations_rule(section.needing, next_run_keys, t))
        .child(
            col()
                .key(AUTOMATIONS_LIST)
                .role("list")
                .label("Automations")
                .w_full()
                .children(
                    section
                        .active
                        .iter()
                        .map(|run| automation_run_row(state, run, &build, t)),
                )
                .children(
                    (finished_count > 0)
                        .then(|| finished_runs_toggle(finished_count, finished_open, t)),
                )
                .children(
                    finished_open
                        .then(|| {
                            section
                                .finished
                                .iter()
                                .map(|run| automation_run_row(state, run, &build, t))
                        })
                        .into_iter()
                        .flatten(),
                ),
        )
}

fn automations_rule(needing: usize, next_run_keys: Option<String>, t: &Tokens) -> El {
    let mut head = GroupHead::new("sidebar.rule.automations", "Automations")
        .heading()
        .section("Automations")
        .action(
            "sidebar.automations.all",
            "all",
            "Open Automations",
            |state: &mut State, tx: &mut Tx| crate::automations::open(state, tx),
        );
    if needing > 0 {
        head = head.aside(automations_needing_pill(
            "sidebar.automations.needing".into(),
            needing,
            next_run_keys,
            next_automation_needing,
            t,
        ));
    }
    head.build(t)
}

pub(super) fn automations_needing_pill(
    key: String,
    needing: usize,
    next_run_keys: Option<String>,
    on_press: fn(&mut State, &mut Tx),
    t: &Tokens,
) -> El {
    let label = format!("{needing} waiting");
    let help = match &next_run_keys {
        Some(keys) => format!("Open the next automation run needing you ({keys})"),
        None => "Open the next automation run needing you".to_owned(),
    };
    let ground = attn_ui::colors::mix_srgb(t.bg_panel, t.warning, 0.14);
    let ink = attn_ui::colors::moved_until_it_reads(
        t.warning,
        t.text_primary,
        ground,
        attn_ui::colors::READS,
    );
    let face = Inline::joined()
        .child(system::dot(ink, DotShape::Filled, DotSize::Small))
        .child(words(label.clone(), Type::Small, ink).single_line());
    system::pill_button(
        key,
        format!("{label} — open next"),
        face,
        t.warning,
        system::Size::Small,
        t,
    )
    .help(help)
    .on_press_tx(on_press)
}

/// Opens the next automation run or automation delegate needing the user in desktop flow:
/// first walks needing rows in the unified Automations section (oldest first), then any needing
/// direct automation runs on numbered or mixed desktops so none are starved.
pub(crate) fn next_automation_needing(state: &mut State, tx: &mut Tx) {
    let now_ms = state.wall_ms();
    let section = model::desktop_automations(
        &state.facts,
        state.window.view == View::Home,
        &crate::desktop::on_screen_sessions(state),
        now_ms,
    );
    let by_id: std::collections::HashMap<&str, &attn_protocol::Session> = state
        .facts
        .sessions
        .iter()
        .map(|s| (s.id.as_str(), s))
        .collect();
    let mut batch: Vec<&str> = Vec::new();
    let mut seen: std::collections::HashSet<&str> = std::collections::HashSet::new();
    for run in &section.active {
        let Some(session) = by_id.get(run.row.id.as_str()) else {
            continue;
        };
        if crate::automations::runs::needs_you(session, &state.facts, now_ms)
            && seen.insert(run.row.id.as_str())
        {
            batch.push(run.row.id.as_str());
        }
    }
    for group in crate::automations::runs::on_desktops(&state.facts, now_ms) {
        for run in group.needing_you {
            if seen.insert(run.id.as_str()) {
                batch.push(run.id.as_str());
            }
        }
    }
    if batch.is_empty() {
        return;
    }
    let current = state.window.on_screen(&state.facts);
    let at = current
        .as_deref()
        .and_then(|id| batch.iter().position(|&run| run == id))
        .map_or(0, |i| (i + 1) % batch.len());
    let next = batch[at].to_owned();
    open_agent(state, &next, tx);
}

pub(super) fn finished_runs_toggle(count: usize, open: bool, t: &Tokens) -> El {
    let label = if count == 1 {
        "1 finished run".to_owned()
    } else {
        format!("{count} finished runs")
    };
    let caret = if open {
        attn_ui::glyph::CARET_DOWN
    } else {
        attn_ui::glyph::CARET_RIGHT
    };
    ListRow::new("sidebar.automations.finished", label.clone())
        .lead(system::glyph(caret, IconSize::Small, t.text_tertiary))
        .title_el(
            words(label, Type::Small, t.text_tertiary)
                .single_line()
                .truncate()
                .shrink(),
        )
        .build(t)
        .expanded(open)
        .on_press(|state: &mut State| {
            state.window.automations_finished_open = !state.window.automations_finished_open;
        })
}

fn automation_run_row(
    state: &State,
    run: &model::AutomationRunLine,
    build: &RowBuild<'_>,
    t: &Tokens,
) -> El {
    let key = format!("sidebar.session.{}", run.row.id);
    let shown = build.marked.shown(&run.row.id);
    let draining = state.window.countdowns.contains_key(&run.row.id);
    let logos_on = build.logos_on;
    let logo_rev = build.logo_rev;
    let mark = state
        .window
        .desk
        .drag
        .as_ref()
        .and_then(|drag| drag.desktop_mark(&run.desktop));
    memo(
        key.clone(),
        (run, draining, logos_on, logo_rev, shown, mark, *t),
        || {
            ROWS_BUILT.with(|n| n.set(n.get() + 1));
            let bar = settling_bar(state, &key, &run.row, t);
            let tile = Some((run.desktop.clone(), run.leaf.clone()));
            let controls = run
                .row
                .settle_run
                .then(|| settle_button(&key, &run.row))
                .into_iter()
                .chain([actions_button(&key, &run.row, tile)]);
            let row = agent_row(
                state,
                key.clone(),
                &run.row,
                false,
                shown,
                logos_on,
                controls.collect(),
                t,
            )
            .children(bar);
            drop_mark(row, mark, false, t)
        },
    )
}

struct RowBuild<'a> {
    marked: &'a Marked,
    logos_on: bool,
    logo_rev: u64,
    in_current: bool,
}

fn desktop_groups(
    state: &State,
    lines: &[Line],
    marked: &Marked,
    logos_on: bool,
    t: &Tokens,
) -> Vec<El> {
    let seam_target = reorder::seam_target(state, lines);
    let logo_rev = if logos_on { state.logos.revision() } else { 0 };
    let mut seen_sessions: std::collections::HashMap<&str, usize> =
        std::collections::HashMap::new();
    let mut groups = Vec::new();
    let mut folded_empty: Vec<&DesktopHeader> = Vec::new();
    let mut i = 0;
    while i < lines.len() {
        let Line::Desktop(desktop) = &lines[i] else {
            i += 1;
            continue;
        };
        let start = i;
        i += 1;
        while i < lines.len() && !matches!(lines[i], Line::Desktop(_)) {
            i += 1;
        }
        if !reorder::stacked(desktop) {
            folded_empty.push(desktop);
            continue;
        }
        let first = groups.is_empty();
        let key = format!("sidebar.group.{}", desktop.id);
        let group = if desktop.current {
            ListRow::well(key, first, t)
        } else {
            ListRow::group(key, first)
        };
        let build = RowBuild {
            marked,
            logos_on,
            logo_rev,
            in_current: desktop.current,
        };
        let rows: Vec<El> = (start..i)
            .map(|at| {
                let occurrence = match &lines[at] {
                    Line::Agent { row, .. } => {
                        let count = seen_sessions.entry(row.id.as_str()).or_insert(0);
                        let current = *count;
                        *count += 1;
                        current
                    }
                    _ => 0,
                };
                let seam = seam_target
                    .filter(|(target, _)| *target == at)
                    .map(|(_, seam)| seam);
                line(state, &lines[at], occurrence, seam, &build, t)
            })
            .collect();
        groups.push(group.children(rows));
    }
    if !folded_empty.is_empty() {
        let strip = ListRow::pill_strip("sidebar.empty_desktops")
            .role("list")
            .label("Empty desktops")
            .children(
                folded_empty
                    .into_iter()
                    .map(|desktop| empty_desktop_pill(state, desktop, t)),
            );
        groups.push(ListRow::group("sidebar.empty_desktops.group", groups.is_empty()).child(strip));
    }
    groups
}

fn empty_desktop_pill(state: &State, desktop: &DesktopHeader, t: &Tokens) -> El {
    let key = format!("sidebar.desktop.{}", desktop.id);
    let id = desktop.id.clone();
    let keys = desktop
        .slot
        .and_then(Action::desktop_select)
        .and_then(|select| crate::palette::label_of(state, &select));
    let mark = state
        .window
        .desk
        .drag
        .as_ref()
        .and_then(|d| d.desktop_mark(&desktop.id));
    let help = match &keys {
        Some(keys) => format!("{} ({keys})", desktop.title),
        None => desktop.title.clone(),
    };
    let pill = system::pill_surface(
        key,
        format!("Open {}", desktop.title),
        system::PillLook::Dashed,
        t,
    )
    .role("button")
    .focusable(true)
    .a11y_value("Empty desktop")
    .help(help)
    .children(desktop.slot.map(|slot| {
        words(slot.to_string(), Type::Tag, t.text_tertiary)
            .single_line()
            .a11y_hidden()
    }))
    .children((desktop.named || desktop.slot.is_none()).then(|| {
        words(desktop.title.clone(), Type::Small, t.text_tertiary)
            .single_line()
            .truncate()
            .shrink()
            .min_w(0.0)
            .a11y_hidden()
    }))
    .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &id, tx));
    let pill = match keys {
        Some(keys) => pill.description(keys),
        None => pill,
    };
    pill_drop_mark(pill, mark, t)
}

fn pill_drop_mark(pill: El, mark: Option<DesktopMark>, t: &Tokens) -> El {
    match mark {
        None => pill,
        Some(DesktopMark::Source) => pill.opacity(0.45),
        Some(DesktopMark::Accepts) => pill
            .fill(t.accent.alpha(0.07))
            .border(1.0, t.accent.alpha(0.45)),
        Some(DesktopMark::Target) => pill
            .border_dash(0.0, 0.0)
            .fill(t.accent.alpha(0.14))
            .border(1.0, t.accent),
    }
}

/// A row that goes somewhere rather than to an agent, with the keys that go there too:
/// secondary until it is where the window is.
fn nav_row(
    key: &str,
    glyph: impl Into<attn_ui::Glyph>,
    label: &str,
    keys: Option<String>,
    selected: bool,
    t: &Tokens,
) -> El {
    let ink = if selected {
        t.text_primary
    } else {
        t.text_secondary
    };
    let row = ListRow::new(key, label)
        .lead(system::glyph(glyph, IconSize::Regular, ink))
        .quiet_title(label.to_owned())
        .keys(keys.clone(), t)
        .current(selected)
        .build(t);
    match keys {
        Some(keys) => row.description(keys),
        None => row,
    }
}

fn line(
    state: &State,
    line: &Line,
    occurrence: usize,
    seam: Option<reorder::Seam>,
    build: &RowBuild<'_>,
    t: &Tokens,
) -> El {
    let key = line.key();
    let unique_key = if occurrence == 0 {
        key.clone()
    } else {
        format!("{key}#{}", occurrence + 1)
    };
    let shown = match line {
        Line::Agent { row, .. } => build.marked.shown(&row.id),
        _ => Shown::default(),
    };
    let draining = match line {
        Line::Agent { row, .. } => state.window.countdowns.contains_key(&row.id),
        _ => false,
    };
    let mark = state
        .window
        .desk
        .drag
        .as_ref()
        .and_then(|d| d.desktop_mark(line.desktop()));
    let keys = match line {
        Line::Desktop(desktop) => desktop
            .slot
            .and_then(Action::desktop_select)
            .and_then(|select| crate::palette::label_of(state, &select)),
        _ => None,
    };
    let in_current = build.in_current;
    let logos_on = build.logos_on;
    let logo_rev = build.logo_rev;
    memo(
        unique_key.clone(),
        (
            line,
            in_current,
            draining,
            seam,
            mark,
            logos_on,
            logo_rev,
            keys.clone(),
            shown,
            *t,
        ),
        || {
            ROWS_BUILT.with(|n| n.set(n.get() + 1));
            let row = match line {
                Line::Desktop(desktop) => desktop_row(key.clone(), desktop, keys.clone(), t),
                Line::Agent { desktop, leaf, row } => {
                    let bar = settling_bar(state, &key, row, t);
                    let tile = Some((desktop.clone(), leaf.clone()));
                    let controls = row
                        .settle_run
                        .then(|| settle_button(&key, row))
                        .into_iter()
                        .chain([actions_button(&key, row, tile)]);
                    agent_row(
                        state,
                        key.clone(),
                        row,
                        in_current,
                        shown,
                        logos_on,
                        controls.collect(),
                        t,
                    )
                    .children(bar)
                }
                Line::Tile(tile) => tile_row(key.clone(), tile, in_current, t),
            };
            let row = if occurrence > 0 {
                row.key(unique_key.clone())
            } else {
                row
            };
            drop_mark(row, mark, matches!(line, Line::Desktop(_)), t).children(seam.map(|seam| {
                let edge = match seam {
                    reorder::Seam::Above => (Some(0.0), None),
                    reorder::Seam::Below => (None, Some(0.0)),
                };
                rune::el()
                    .key(format!("{key}.seam"))
                    .pin(Some(0.0), edge.0, Some(0.0), edge.1)
                    .h(EDGE_LINE)
                    .fill(t.accent)
            }))
        },
    )
}

/// A line as its desktop shows to a dragged tile: muted on the desktop the tile comes from,
/// the heading outlined on the others, and every line of the one it lands on tinted behind
/// one accent bar.
fn drop_mark(line: El, mark: Option<DesktopMark>, heading: bool, t: &Tokens) -> El {
    match mark {
        None => line,
        Some(DesktopMark::Source) => line.opacity(0.45),
        Some(DesktopMark::Accepts) if heading => line
            .fill(t.accent.alpha(0.07))
            .border(1.0, t.accent.alpha(0.32)),
        Some(DesktopMark::Accepts) => line,
        Some(DesktopMark::Target) => line
            .fill(t.accent.alpha(0.14))
            .border_sides([0.0, 0.0, 0.0, 3.0], t.accent),
    }
}

/// Where a dragged tile drops to make a new desktop for it, shown only while a tile is
/// dragged.
fn new_desktop_drop(over: bool, t: &Tokens) -> El {
    system::drop_zone("sidebar.new_desktop", "New desktop", over, t)
}

/// How tall line `line` stands in the desktop sidebar, since a virtual list stacks its rows by
/// their height.
pub(super) fn line_height(line: &Line) -> f32 {
    match line {
        Line::Desktop(_) => GroupHead::height(),
        _ => ListRow::height(),
    }
}

/// A desktop's heading: its slot chip when it has one, its name, and the keys that go to it,
/// with its rename and close controls on hover.
fn desktop_row(key: String, desktop: &DesktopHeader, keys: Option<String>, t: &Tokens) -> El {
    let id = desktop.id.clone();
    let dragged = desktop.id.clone();
    let surface = if desktop.current {
        ListRow::well_surface(t)
    } else {
        t.bg_panel
    };
    let mut head = GroupHead::new(key.clone(), format!("Open {}", desktop.title))
        .on(surface)
        .current(desktop.current)
        .keys(keys.clone())
        .controls([
            rename_desktop_button(&key, desktop),
            close_desktop_button(&key, desktop),
        ]);
    if let Some(slot) = desktop.slot {
        head = head.mark(system::number_chip(slot, desktop.current, desktop.empty, t));
        if desktop.named {
            head = head.name(desktop.title.clone());
        }
    } else {
        head = head.name(desktop.title.clone());
    }
    let head = head
        .build(t)
        .a11y_value(match (desktop.current, desktop.empty) {
            (true, true) => "current, Empty desktop",
            (true, false) => "current",
            (false, true) => "Empty desktop",
            (false, false) => "",
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &id, tx))
        .on_drag(move |state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            reorder::drag(state, &dragged, d, tx)
        });
    match keys {
        Some(keys) => head.description(keys),
        None => head,
    }
}

/// A line along a row's edge, the seam a dragged row drops at or an agent's settling bar: two
/// points, the thinnest that still reads over the row's hover fill.
const EDGE_LINE: f32 = 2.0;

/// How much of an agent's name stays before the marks after it give way: about six
/// characters of body text, enough to tell agents apart.
const NAME_MIN_W: f32 = 64.0;

/// Three-tier title hierarchy: active or needs-you in bold primary, working in regular primary,
/// and idle/settled quieted to secondary inside the current desktop or tertiary outside it.
pub(crate) fn title_style(agent: &AgentRow, in_current: bool, t: &Tokens) -> (Type, rune::Color) {
    let needs_you = matches!(
        agent.state,
        AgentState::WaitingInput | AgentState::PendingApproval
    ) || agent.asks > 0;
    if agent.selected || needs_you {
        (Type::Name, t.text_primary)
    } else if agent.state == AgentState::Working {
        (Type::Body, t.text_primary)
    } else if in_current {
        (Type::Body, t.text_secondary)
    } else {
        (Type::Body, t.text_tertiary)
    }
}

/// An agent's row, its hover controls laid over its end.
#[allow(clippy::too_many_arguments)]
fn agent_row(
    state: &State,
    key: String,
    agent: &AgentRow,
    in_current: bool,
    shown: Shown,
    logos: bool,
    controls: Vec<system::IconButton>,
    t: &Tokens,
) -> El {
    let id = agent.id.clone();
    let surface = if in_current {
        ListRow::well_surface(t)
    } else {
        t.bg_panel
    };
    let ground = if agent.selected {
        if in_current {
            t.bg_selected
        } else {
            t.bg_elevated
        }
    } else {
        surface
    };
    let (title_kind, title_ink) = title_style(agent, in_current, t);
    let name = words(agent.name.clone(), title_kind, title_ink)
        .key(format!("{key}.name"))
        .single_line()
        .truncate()
        .shrink()
        .min_w(NAME_MIN_W);
    let mut row = ListRow::new(key.clone(), format!("Open {}", agent.name))
        .on(surface)
        .lead(lead(state, &key, agent, logos, ground, t))
        .title_el(name)
        .in_view(agent.selected)
        .tint(row_tint(agent, shown, t))
        .hover_controls(controls);
    if let Some(pr) = &agent.pr {
        row = row.badge(crate::desktop::provenance::badge_view(&key, pr, t));
    }
    if let Some(chain) = &agent.chain {
        row = crate::desktop::chain::on_row(row, &key, &agent.id, chain, t);
    }
    if let Some(run) = &agent.run {
        row = row.badge(crate::desktop::provenance::run_view(&key, run, t));
    }
    if let Some(asks) = crate::asks::marker(&key, agent.asks, t) {
        row = row.badge(asks);
    }
    if agent.state == AgentState::Recoverable {
        row = row.badge(
            words("recoverable", Type::Mono, t.text_tertiary).key(format!("{key}.recoverable")),
        );
    }
    let row = row
        .build(t)
        .a11y_value(agent.state.wire())
        .help(agent.hover())
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
        .on_drag(|state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            crate::desktop::drag::gesture(state, d, tx, crate::desktop::drag::Owner::Sidebar)
        });
    let row = outlined(row, shown, t);
    match &agent.chain {
        Some(_) => crate::desktop::chain::row_hovers(row, &key, &agent.id),
        None => row,
    }
}

/// An agent's row takes the error tint while its machine is unreachable, and the accent's while
/// the open delegation chain picks it.
fn row_tint(agent: &AgentRow, shown: Shown, t: &Tokens) -> Option<rune::Color> {
    if shown.picked {
        Some(t.accent)
    } else {
        agent.offline.then_some(t.error)
    }
}

/// An agent's row outlined while the open delegation chain or pull requests are the ones its agent
/// opened.
fn outlined(row: El, shown: Shown, t: &Tokens) -> El {
    if shown.opened {
        row.border(1.0, t.accent)
    } else {
        row
    }
}

/// The line an agent's name sits on, led by the brand's flag when the agent has priority.
pub(crate) fn flagged(row_key: &str, agent: &AgentRow, t: &Tokens) -> El {
    Inline::joined()
        .key(format!("{row_key}.identity"))
        .children(agent.priority.then(|| priority_flag(row_key, t)))
}

pub(crate) fn priority_flag(row_key: &str, t: &Tokens) -> El {
    system::icon(&attn_ui::icon::FLAG, IconSize::Regular, t.brand)
        .key(format!("{row_key}.priority"))
        .role("img")
        .label("Priority")
}

fn rename_desktop_button(row_key: &str, desktop: &DesktopHeader) -> system::IconButton {
    let (id, under) = (desktop.id.clone(), format!("{row_key}.rename"));
    queue_view::control(
        row_key,
        "rename",
        format!("Rename {}", desktop.title),
        attn_ui::glyph::PENCIL,
        move |state, tx| rename::desktop(state, &id, rename::Opener::Control(under.clone()), tx),
    )
}

fn settle_button(row_key: &str, agent: &AgentRow) -> system::IconButton {
    let id = agent.id.clone();
    queue_view::control(
        row_key,
        "settle",
        format!("Settle {}", agent.name),
        "✓",
        move |state, _| actions::settle(state, &id),
    )
}

/// The × button that closes a desktop, asking first when it holds tiles.
fn close_desktop_button(row_key: &str, desktop: &DesktopHeader) -> system::IconButton {
    let id = desktop.id.clone();
    queue_view::control(
        row_key,
        "close",
        format!("Close {}", desktop.title),
        "×",
        move |state, tx| {
            crate::dispatch::dispatch(
                state,
                Action::CloseDesktop {
                    desktop: id.clone(),
                },
                tx,
            );
        },
    )
}

/// The ••• button that opens an agent's actions menu under itself, for the tile its row shows
/// or, with none, the agent's own tile.
pub(crate) fn actions_button(
    row_key: &str,
    agent: &AgentRow,
    tile: Option<(String, String)>,
) -> system::IconButton {
    let (id, name, under) = (
        agent.id.clone(),
        agent.name.clone(),
        format!("{row_key}.actions"),
    );
    queue_view::control(
        row_key,
        "actions",
        format!("Actions for {}", agent.name),
        "•••",
        move |state, tx| {
            session_menu::open(state, &id, name.clone(), under.clone(), tile.clone(), tx)
        },
    )
}

/// One auto-settle countdown's drain: full when first seen, empty at its deadline.
pub(crate) struct Countdown {
    pub(crate) fires_at: String,
    pub(crate) drain: rune::Tween<[f32; 2]>,
}

impl Countdown {
    /// The drain of a countdown that fires at `fires_at`, first seen at `now_ms`.
    pub(crate) fn seen(fires_at: String, now_ms: i64) -> Countdown {
        let left_ms = queue::epoch_ms(&fires_at).map_or(0, |at| (at - now_ms).max(0));
        Countdown {
            drain: rune::Tween::linear(
                [0.0, 1.0],
                [1.0, 1.0],
                std::time::Duration::from_millis(left_ms as u64),
            ),
            fires_at,
        }
    }
}

/// Keeps a drain for each countdown whose agent is off screen, started from the time left
/// when it left the screen or began, so the bar empties at the deadline. True when one began
/// or ended.
pub(crate) fn follow_countdowns(state: &mut State, now_ms: i64) -> bool {
    let counting = |s: &&attn_protocol::Session| {
        matches!(model::Settling::of(s), model::Settling::Counting(_))
    };
    if state.window.countdowns.is_empty() && !state.facts.sessions.iter().any(|s| counting(&s)) {
        return false;
    }
    let on_screen = crate::desktop::on_screen_sessions(state);
    let off_screen: Vec<(String, String)> = state
        .facts
        .sessions
        .iter()
        .filter(|s| counting(s) && !on_screen.contains(&s.id))
        .filter_map(|s| Some((s.id.clone(), s.auto_settle_fires_at.clone()?)))
        .collect();
    let countdowns = &mut state.window.countdowns;
    let before = countdowns.len();
    countdowns.retain(|id, c| {
        off_screen
            .iter()
            .any(|(i, at)| i == id && *at == c.fires_at)
    });
    let mut changed = countdowns.len() != before;
    for (id, fires_at) in off_screen {
        countdowns.entry(id).or_insert_with(|| {
            changed = true;
            Countdown::seen(fires_at, now_ms)
        });
    }
    changed
}

/// The bar under a row whose agent's tile is off screen while its turn auto-settles: it
/// drains to the deadline, or stands full while the countdown is held.
pub(crate) fn settling_bar(
    state: &State,
    row_key: &str,
    agent: &AgentRow,
    t: &Tokens,
) -> Option<El> {
    let (label, drain) = match &agent.settling {
        model::Settling::Off => return None,
        model::Settling::Held => ("Settling paused", None),
        model::Settling::Counting(_) => ("Settling", state.window.countdowns.get(&agent.id)),
    };
    let line = rune::Shape::new().view_box(1.0, 1.0).line(
        [0.0, 0.5],
        [1.0, 0.5],
        2.0,
        rune::LineCap::Butt,
        if drain.is_some() {
            t.accent
        } else {
            t.text_muted
        },
    );
    let bar = rune::canvas(line)
        .key(format!("{row_key}.settling"))
        .role("img")
        .label(label)
        .pin(Some(0.0), None, Some(0.0), Some(0.0))
        .h(EDGE_LINE);
    Some(match drain {
        Some(c) => bar.live_trim(&c.drain),
        None => bar,
    })
}

pub(crate) fn logos(state: &State) -> bool {
    crate::setting::on(&state.facts.settings, HARNESS_LOGOS)
}

fn member_state(agent: &AgentRow) -> Option<MemberState> {
    if crate::harness::catalog::normalize(&agent.agent) == crate::harness::catalog::TERMINAL {
        return None;
    }
    match agent.state {
        AgentState::Launching => None,
        AgentState::Working => Some(MemberState::Working),
        AgentState::WaitingInput => Some(MemberState::Waiting),
        AgentState::PendingApproval => Some(MemberState::Approval),
        AgentState::Scheduled => Some(MemberState::Scheduled),
        AgentState::Idle | AgentState::Recoverable => Some(MemberState::Idle),
        AgentState::Unknown => Some(MemberState::Unknown),
    }
}

pub(crate) fn lead(
    state: &State,
    row_key: &str,
    agent: &AgentRow,
    logos: bool,
    ground: rune::Color,
    t: &Tokens,
) -> El {
    if logos && agent.state == AgentState::Launching && !agent.offline {
        return rune::row()
            .key(format!("{row_key}.state"))
            .role("img")
            .label(agent.lead())
            .a11y_value(agent.state.wire())
            .center()
            .child(
                system::glyph(
                    model::session_emoji(&agent.id),
                    IconSize::Regular,
                    t.text_primary,
                )
                .key(format!("{row_key}.emoji")),
            );
    }
    if !logos {
        return rune::row()
            .key(format!("{row_key}.lead"))
            .center()
            .child(state_mark(row_key, agent, DotSize::Regular, t));
    }
    rune::row()
        .key(format!("{row_key}.state"))
        .role("img")
        .label(agent.lead())
        .a11y_value(agent.state.wire())
        .center()
        .child(blocks::state_mark(
            crate::harness::logo::of(state, &agent.agent),
            member_state(agent),
            ground,
            t,
        ))
}

/// The dot in each agent's colour of state at `size`, or the struck-through ring of an agent
/// whose machine is unreachable, at the regular size whatever `size` is.
pub(crate) fn state_mark(row_key: &str, agent: &AgentRow, size: DotSize, t: &Tokens) -> El {
    let ink = state_color(agent.state, t);
    let mark = rune::row()
        .key(format!("{row_key}.state"))
        .role("img")
        .label(agent.lead())
        .a11y_value(agent.state.wire());
    if !agent.offline {
        return mark.child(system::dot(ink, DotShape::Filled, size));
    }
    mark.child(system::struck_dot(ink, t.text_secondary))
}

pub(crate) fn state_color(state: AgentState, t: &Tokens) -> rune::Color {
    match state {
        AgentState::Idle | AgentState::Recoverable => t.state_idle,
        AgentState::Working => t.state_working,
        AgentState::WaitingInput => t.state_waiting,
        AgentState::PendingApproval => t.state_approval,
        AgentState::Scheduled => t.state_scheduled,
        AgentState::Unknown => t.state_unknown,
        AgentState::Launching => t.text_tertiary,
    }
}

fn tile_row(key: String, tile: &TileRow, in_current: bool, t: &Tokens) -> El {
    let (desktop, leaf) = (tile.desktop.clone(), tile.id.clone());
    let surface = if in_current {
        ListRow::well_surface(t)
    } else {
        t.bg_panel
    };
    let ground = if tile.selected {
        if in_current {
            t.bg_selected
        } else {
            t.bg_elevated
        }
    } else {
        surface
    };
    let (title_kind, title_ink) = if tile.selected {
        (Type::Name, t.text_primary)
    } else if in_current {
        (Type::Body, t.text_secondary)
    } else {
        (Type::Body, t.text_tertiary)
    };
    let controls = [move_tile_button(&key, tile)]
        .into_iter()
        .chain((tile.kind == crate::browser::KIND).then(|| reload_tile_button(&key, tile)))
        .chain([close_tile_button(&key, tile)]);
    ListRow::new(key.clone(), format!("Open {}", tile.title))
        .on(surface)
        .lead(system::icon(
            tile.icon(),
            IconSize::Regular,
            t.text_tertiary,
        ))
        .title_el(
            words(tile.title.clone(), title_kind, title_ink)
                .single_line()
                .truncate()
                .shrink(),
        )
        .aside(system::quiet_tag(
            format!("{key}.kind"),
            tile.word(),
            ground,
            t,
        ))
        .hover_controls(controls)
        .in_view(tile.selected)
        .build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_tile(state, &desktop, &leaf, tx))
        .on_drag(|state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            crate::desktop::drag::gesture(state, d, tx, crate::desktop::drag::Owner::Sidebar)
        })
}

fn move_tile_button(row_key: &str, tile: &TileRow) -> system::IconButton {
    let (desktop, leaf) = (tile.desktop.clone(), tile.id.clone());
    queue_view::control(
        row_key,
        "move",
        format!("Move {} to desktop", tile.title),
        &attn_ui::icon::OVERVIEW,
        move |state, tx| {
            crate::palette::open_move_to_desktop(state, desktop.clone(), leaf.clone(), tx)
        },
    )
}

fn close_tile_button(row_key: &str, tile: &TileRow) -> system::IconButton {
    let (desktop, leaf) = (tile.desktop.clone(), tile.id.clone());
    queue_view::control(
        row_key,
        "close",
        format!("Close {}", tile.title),
        "×",
        move |state, tx| crate::desktop::remove_tile(state, desktop.clone(), leaf.clone(), tx),
    )
}

/// The browser row's button that loads its page again, as the tile header's does.
fn reload_tile_button(row: &str, tile: &TileRow) -> system::IconButton {
    let page = attn_client::TileKey {
        desktop: tile.desktop.clone(),
        tile: tile.id.clone(),
    };
    system::icon_button(
        format!("{row}.reload"),
        format!("Reload {}", tile.title),
        attn_ui::glyph::RELOAD,
        IconSize::Small,
    )
    .on_press_tx(move |_: &mut State, _: &mut Tx| crate::browser::reload(&page))
}

/// The header of the tile showing `session` on the current desktop.
fn tile_header_of(state: &State, session: &str) -> String {
    let pane = state
        .facts
        .current_desktop()
        .and_then(|d| d.panes.iter().find(|p| p.session_id == session))
        .map(|p| p.pane_id.clone())
        .unwrap_or_default();
    format!("tile.{pane}.header")
}

pub(crate) fn open_tile(state: &mut State, desktop: &str, tile: &str, tx: &mut Tx) {
    let action = crate::Action::OpenTile {
        desktop: desktop.into(),
        tile: tile.into(),
    };
    crate::dispatch(state, action, tx);
}

/// Shows the agent where it is and gives its terminal the keyboard.
pub(crate) fn open_agent(state: &mut State, session: &str, tx: &mut Tx) {
    let tiled = pane_of(state, session);
    if let Some((desktop, leaf)) = &tiled {
        crate::desktop::give_keyboard(state, desktop, leaf, tx);
    }
    show_agent(state, session, tx);
    if tiled.is_none() {
        crate::desktop::arrival::hand_keyboard(state);
    }
}

/// Shows the agent where it is and leaves the keyboard where it was. A show the daemon refuses
/// says why and, when no later show was asked for, undoes what the window did ahead of it: the
/// window goes back home when it came from there, and the keyboard back to the agent on screen
/// when it went to this one.
pub(crate) fn show_agent(state: &mut State, session: &str, tx: &mut Tx) {
    let shown = pane_of(state, session);
    if let Some((desktop, leaf)) = &shown
        && state.window.desk.pick(desktop, leaf)
    {
        tx.instant();
    }
    let from_home = state.window.view == View::Home;
    state.window.shows_asked += 1;
    let this_show = state.window.shows_asked;
    state.window.show_desktop(tx);
    state.window.follow_next_turn = false;
    if let Some((desktop, leaf)) = &shown
        && crate::desktop::show_here(state, desktop, leaf, tx)
    {
        return;
    }
    let awaited = crate::desktop::arrival::Shown::Session(session.to_owned());
    crate::desktop::arrival::expect(state, awaited.clone(), true, tx);
    let session_id = session.to_owned();
    state.ask(
        |request_id| DesktopShowSessionMessage {
            request_id,
            session_id,
            ..DesktopShowSessionMessage::new()
        },
        move |state: &mut State, answer, tx: &mut Tx| {
            let Err(reason) = answer.outcome() else {
                return;
            };
            state
                .window
                .notify(format!("Could not show that agent: {reason}"), tx);
            crate::desktop::arrival::forget(state, &awaited);
            if state.window.shows_asked != this_show {
                return;
            }
            if from_home && state.window.view == View::Desktop {
                state.window.go_home();
            }
            let keyboard_went_there = shown.is_some_and(|(desktop, pane)| {
                state
                    .window
                    .keyboard
                    .as_ref()
                    .is_some_and(|k| k.desktop == desktop && k.pane == pane)
            });
            if keyboard_went_there {
                keyboard_to_screen(state, tx);
            }
        },
        tx,
    );
}

/// What dragging a row at element path `path` in the sidebar picks up: its pane or tile, on the
/// desktop it shows on.
pub(crate) fn row_source_at(state: &State, path: &str) -> Option<crate::desktop::drag::Source> {
    let path = format!("/{path}/");
    let all = lines(state);
    let mut seen_sessions: std::collections::HashMap<&str, usize> =
        std::collections::HashMap::new();
    if let Some(found) = all.iter().find_map(|line| {
        let occurrence = match line {
            Line::Agent { row, .. } => {
                let count = seen_sessions.entry(row.id.as_str()).or_insert(0);
                let current = *count;
                *count += 1;
                current
            }
            _ => 0,
        };
        let key = line.key();
        let unique = if occurrence == 0 {
            key
        } else {
            format!("{key}#{}", occurrence + 1)
        };
        if !path.contains(&format!("/{unique}/")) {
            return None;
        }
        match line {
            Line::Agent { desktop, leaf, row } => Some(crate::desktop::drag::Source::Row {
                desktop: desktop.clone(),
                leaf: leaf.clone(),
                label: row.name.clone(),
            }),
            Line::Tile(tile) => Some(crate::desktop::drag::Source::Row {
                desktop: tile.desktop.clone(),
                leaf: tile.id.clone(),
                label: tile.title.clone(),
            }),
            Line::Desktop(_) => None,
        }
    }) {
        return Some(found);
    }
    automation_row_at(state, &path).map(|run| crate::desktop::drag::Source::Row {
        desktop: run.desktop,
        leaf: run.leaf,
        label: run.row.name,
    })
}

fn automation_row_at(state: &State, path: &str) -> Option<model::AutomationRunLine> {
    let session = path
        .split('/')
        .find_map(|segment| segment.strip_prefix("sidebar.session."))?;
    let section = automations(state);
    section
        .active
        .into_iter()
        .chain(
            state
                .window
                .automations_finished_open
                .then_some(section.finished)
                .into_iter()
                .flatten(),
        )
        .find(|run| run.row.id == session)
}

pub(super) fn pane_of(state: &State, session: &str) -> Option<(String, String)> {
    state.facts.desktops.iter().find_map(|d| {
        let pane = d.panes.iter().find(|p| p.session_id == session)?;
        Some((d.id.clone(), pane.pane_id.clone()))
    })
}

/// When an agent shows alone, the sidebar it hides lets go: its agent list closes, and the
/// keyboard it held goes to the agent.
pub(crate) fn give_way_to_an_agent_alone(state: &mut State, tx: &mut Tx) {
    if !crate::desktop::shows_an_agent_alone(state) {
        return;
    }
    let window = &mut state.window;
    let held =
        window.agent_list_open || window.agent_filter.focused || window.queue_cursor.is_some();
    window.agent_list_open = false;
    window.agent_filter = rune::TextEdit::default();
    window.queue_cursor = None;
    if held {
        keyboard_to_screen(state, tx);
    }
}

/// Gives the keyboard back to the agent on screen, or lets go of it at home.
pub(crate) fn keyboard_to_screen(state: &mut State, tx: &mut Tx) {
    let pane = state
        .window
        .on_screen(&state.facts)
        .and_then(|session| pane_of(state, &session));
    match pane {
        Some((desktop, leaf)) => crate::desktop::give_keyboard(state, &desktop, &leaf, tx),
        None => tx.blur(),
    }
}

pub(crate) fn open_desktop(state: &mut State, desktop: &str, tx: &mut Tx) {
    let action = crate::Action::GoToDesktop {
        desktop: desktop.into(),
    };
    crate::dispatch(state, action, tx);
}
