pub(crate) mod actions;
pub(crate) mod bar_peek;
mod chrome;
pub(crate) mod crew;
mod dock;
mod harness_icon;
pub(crate) mod model;
pub(crate) mod queue;
mod queue_view;
pub(crate) mod rename;
pub(crate) mod reorder;
pub(crate) mod session_menu;
pub(crate) mod snooze;
pub(crate) mod width;

use std::cell::Cell;
use std::rc::Rc;

use attn_protocol::DesktopShowSessionMessage;
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, GrowthSpec, ScrollSpec, Tx, col, el, memo, row, text};

pub use model::AgentState;
pub(crate) use model::{AgentRow, Line, display_name, tile_title};
use model::{DesktopHeader, TileRow};

use crate::State;
use crate::action::Action;
use crate::desktop::DesktopMark;
use crate::shell::View;

pub(crate) const ROW_HEIGHT: f32 = 28.0;
pub(crate) const HARNESS_LOGOS: &str = "sidebar_harness_logos_enabled";

thread_local! {
    static ROWS_BUILT: Cell<usize> = const { Cell::new(0) };
}

pub(crate) use chrome::title_controls;
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
        (false, true) => return Some(chrome::rail(state, t)),
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

/// What the title bar's middle holds for the sidebar: the queue bar, in queue flow with the
/// sidebar collapsed.
pub(crate) fn title_middle(state: &State, t: &Tokens) -> Option<El> {
    (actions::queue_flow(state) && state.window.sidebar_hidden).then(|| {
        let bands = queue::bands(&state.facts, state.window.now_ms);
        row()
            .shrink()
            .h_full()
            .items_center()
            .gap(space::S2)
            .children(
                crate::notifications::critical_strip(&state.facts.notifications, t)
                    .map(|s| s.w(240.0)),
            )
            .child(chrome::queue_bar(state, &bands, t))
    })
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
            let under = tile_header_of(state, session);
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
pub(crate) use queue_view::{LIST as QUEUE_LIST, agent_rows as queue_rows};
pub(crate) const DESKTOP_LIST: &str = "sidebar.desktops";

/// The next wall-clock moment a turn age the window shows reads differently.
pub(crate) fn next_change(state: &State, now_ms: i64) -> Option<i64> {
    if !actions::queue_flow(state) {
        return None;
    }
    queue::bands(&state.facts, now_ms)
        .turns
        .iter()
        .filter_map(|row| queue::next_age_change(row.session.turn_opened_at.as_deref(), now_ms))
        .min()
}

/// How many sidebar rows this thread has built, memo misses only: a row whose model did not
/// change is reused and not counted.
pub fn rows_built() -> usize {
    ROWS_BUILT.with(Cell::get)
}

/// The element whose desktops take a dragged tile: the queue bar in queue flow with the
/// sidebar collapsed, the sidebar otherwise. The rail takes none.
pub(crate) fn drop_scope(state: &State) -> &'static str {
    if actions::queue_flow(state) && state.window.sidebar_hidden {
        "queue-bar"
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
    let path = format!("/{path}/");
    lines(state)
        .into_iter()
        .find(|l| path.contains(&format!("/{}/", l.key())))
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
        lines(state).iter().any(|l| l.key() == key)
    }
}

/// The desktop sidebar's lines, in the order it shows them.
pub(crate) fn lines(state: &State) -> Vec<Line> {
    let home = state.window.view == View::Home;
    let on_screen = crate::desktop::on_screen_sessions(state);
    model::desktop_lines(&state.facts, home, &on_screen)
}

fn desktop_sidebar(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let home = state.window.view == View::Home;
    let on_screen = crate::desktop::on_screen_sessions(state);
    let new_desktop = state
        .window
        .desk
        .drag
        .as_ref()
        .and_then(|d| d.new_desktop());
    let dock = dock::of(state);
    let home_keys = crate::palette::label_of(state, &Action::SessionGoToDashboard);
    let overview_keys = crate::palette::label_of(state, &Action::DesktopOverview);
    let has_crew = !crate::crew::members(state).is_empty();
    let chief = crate::chief::view(state);
    let resting: Vec<crew::Resting> = crew::between_sessions(state)
        .into_iter()
        .filter(|m| chief.as_ref().is_none_or(|c| c.key != m.id))
        .map(|m| crew::resting(state, &m.id))
        .collect();
    let reads = (
        std::sync::Arc::as_ptr(&facts.sessions) as usize,
        std::sync::Arc::as_ptr(&facts.desktops) as usize,
        std::sync::Arc::as_ptr(&facts.profiles) as usize,
        facts.tile_contents.revision,
        std::sync::Arc::as_ptr(&facts.asks) as usize,
        std::sync::Arc::as_ptr(&facts.garden) as usize,
        std::sync::Arc::as_ptr(&facts.outposts) as usize,
        home,
        on_screen.clone(),
        width::of(state).to_bits(),
        (
            state.window.reorder.clone(),
            new_desktop,
            (dock.clone(), home_keys.clone(), overview_keys.clone()),
            (
                resting.clone(),
                has_crew,
                chief.clone(),
                state.window.crew_rows.armed(),
            ),
        ),
        (
            *t,
            facts.notifications.unread_critical,
            facts.notifications.critical_title.clone(),
        ),
    );
    memo("sidebar", reads, || {
        let lines: Rc<[Line]> = model::desktop_lines(facts, home, &on_screen).into();
        let keys = lines.clone();
        let t = *t;
        col()
            .key("sidebar")
            .role("navigation")
            .label("Sidebar")
            .w(width::of(state))
            .h_full()
            .fill(t.bg_panel)
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
            .padding(space::S2)
            .gap(space::S1)
            .children(crate::notifications::critical_strip(
                &facts.notifications,
                &t,
            ))
            .child(home_row(home, home_keys.clone(), &t))
            .child(
                col()
                    .key("sidebar.scroll")
                    .w_full()
                    .grow(1.0)
                    .min_h(0.0)
                    .scroll(ScrollSpec {
                        stick_bottom: false,
                    })
                    .child(overview_row(overview_keys.clone(), &t))
                    .children(has_crew.then(|| {
                        col()
                            .key(crew::CLASSIC_ROW)
                            .role("list")
                            .label("Crew between sessions")
                            .w_full()
                            .child(queue_view::crew_rule("sidebar.rule.crew".into(), &t))
                            .children(queue_view::chief_slot(state, &t))
                            .children(resting.iter().map(|m| {
                                crew::row_view(
                                    format!("{}.{}", crew::CLASSIC_ROW, m.member),
                                    m,
                                    ROW_HEIGHT,
                                    &t,
                                )
                            }))
                    }))
                    .child(
                        col()
                            .key(DESKTOP_LIST)
                            .role("list")
                            .label("Desktops")
                            .w_full()
                            .growth(GrowthSpec::default())
                            .virtual_rows(
                                lines.len(),
                                ROW_HEIGHT,
                                move |_: &State, i| keys[i].key().into(),
                                move |state: &State, i| line(state, &lines, i, &t),
                            ),
                    )
                    .children(new_desktop.map(|over| new_desktop_drop(over, &t))),
            )
            .child(dock::view(&dock, &t))
    })
}

pub(crate) fn home_row(home: bool, keys: Option<String>, t: &Tokens) -> El {
    nav_row("sidebar.home", attn_ui::icon::HOME, "Home", keys, t)
        .a11y_value(if home { "current" } else { "" })
        .fill(if home { t.bg_selected } else { t.bg_panel })
        .on_press(|state: &mut State| state.window.go_home())
}

fn overview_row(keys: Option<String>, t: &Tokens) -> El {
    nav_row(
        "sidebar.overview",
        attn_ui::icon::GRID,
        "Desktop overview",
        keys,
        t,
    )
    .fill(t.bg_panel)
    .on_press_tx(|state: &mut State, tx: &mut Tx| {
        crate::desktop::run(state, &Action::DesktopOverview, tx);
    })
}

/// A row that goes somewhere rather than to an agent, with the keys that go there too, as the
/// Tauri app's `sidebar-nav-row`.
fn nav_row(key: &str, glyph: &str, label: &str, keys: Option<String>, t: &Tokens) -> El {
    let row = row()
        .key(key)
        .role("button")
        .focusable(true)
        .label(label)
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S2)
        .padding(space::S1)
        .radius(4.0)
        .hover(t.bg_elevated)
        .child(
            text(glyph)
                .font(type_::BODY)
                .color(t.text_secondary)
                .w(14.0),
        )
        .child(
            text(label)
                .font(type_::BODY)
                .color(t.text_primary)
                .grow(1.0)
                .single_line()
                .truncate(),
        );
    match keys {
        Some(keys) => row
            .description(keys.clone())
            .child(text(keys).font(type_::MONO_CAPTION).color(t.text_tertiary)),
        None => row,
    }
}

fn line(state: &State, lines: &[Line], index: usize, t: &Tokens) -> El {
    let line = &lines[index];
    let key = line.key();
    let draining = match line {
        Line::Agent { row, .. } => state.window.countdowns.contains_key(&row.id),
        _ => false,
    };
    let seam = reorder::seam_on(state, lines, index);
    let mark = state
        .window
        .desk
        .drag
        .as_ref()
        .and_then(|d| d.desktop_mark(line.desktop()));
    let logos = logos(state);
    let keys = match line {
        Line::Desktop(desktop) => Action::desktop_select(desktop.number)
            .and_then(|select| crate::palette::label_of(state, &select)),
        _ => None,
    };
    memo(
        key.clone(),
        (line, draining, seam, mark, logos, keys.clone(), *t),
        || {
            ROWS_BUILT.with(|n| n.set(n.get() + 1));
            let row = match line {
                Line::Desktop(desktop) => {
                    let close = overlaid_controls(&key, keys.is_some(), t)
                        .child(rename_desktop_button(&key, desktop, t))
                        .child(close_desktop_button(&key, desktop, t));
                    desktop_row(key.clone(), desktop, keys.clone(), t)
                        .hover_group()
                        .child(close)
                }
                Line::Agent { row, .. } => {
                    let bar = settling_bar(state, &key, row, t);
                    let more = rune::row()
                        .key(format!("{key}.controls"))
                        .group_opacity(0.0, 1.0)
                        .gap(space::S1)
                        .items_center()
                        .children(
                            row.pr
                                .as_ref()
                                .map(|pr| crate::desktop::provenance::badge_view(&key, pr, t)),
                        )
                        .children(row.settle_run.then(|| settle_button(&key, row, t)))
                        .child(actions_button(&key, row, t));
                    agent_row(key.clone(), row, logos, t)
                        .hover_group()
                        .child(more)
                        .children(bar)
                }
                Line::Tile(tile) => tile_row(key.clone(), tile, t),
            };
            drop_mark(row, mark, matches!(line, Line::Desktop(_)), t).children(seam.map(|seam| {
                rune::el()
                    .key(format!("{key}.seam"))
                    .absolute(
                        0.0,
                        if seam == reorder::Seam::Above {
                            0.0
                        } else {
                            ROW_HEIGHT - 2.0
                        },
                    )
                    .w_full()
                    .h(2.0)
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
            .radius(0.0)
            .fill(t.accent.alpha(0.14))
            .border_sides([0.0, 0.0, 0.0, 3.0], t.accent),
    }
}

/// Where a dragged tile drops to make a new desktop for it, shown only while a tile is
/// dragged.
fn new_desktop_drop(over: bool, t: &Tokens) -> El {
    row()
        .key("sidebar.new_desktop")
        .role("button")
        .focusable(true)
        .label("New desktop")
        .a11y_value(if over { "drop here" } else { "" })
        .w_full()
        .center()
        .gap(6.0)
        .px(10.0)
        .py(12.0)
        .radius(8.0)
        .fill(t.accent.alpha(if over { 0.14 } else { 0.04 }))
        .border(1.5, t.accent.alpha(if over { 1.0 } else { 0.5 }))
        .child(text("+").font(type_::MONO_CAPTION).color(t.accent))
        .child(
            text("New desktop")
                .font(type_::MONO_CAPTION)
                .color(t.accent),
        )
}

fn desktop_row(key: String, desktop: &DesktopHeader, keys: Option<String>, t: &Tokens) -> El {
    let id = desktop.id.clone();
    let dragged = desktop.id.clone();
    let row = match &keys {
        Some(keys) => row().description(keys.clone()),
        None => row(),
    };
    row.key(key.clone())
        .role("button")
        .focusable(true)
        .label(format!("Open {}", desktop.title))
        .a11y_value(match (desktop.current, desktop.empty) {
            (true, true) => "current, Empty desktop",
            (true, false) => "current",
            (false, true) => "Empty desktop",
            (false, false) => "",
        })
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S2)
        .padding(space::S1)
        .radius(4.0)
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &id, tx))
        .on_drag(move |state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            reorder::drag(state, &dragged, d, tx)
        })
        .child(
            text(desktop.number.to_string())
                .font(type_::MONO_CAPTION)
                .color(if desktop.current {
                    t.text_primary
                } else {
                    t.text_tertiary
                })
                .fill(if desktop.current {
                    t.bg_selected
                } else {
                    t.bg_button
                })
                .radius(3.0)
                .px(4.0),
        )
        .child(
            text(desktop.title.clone())
                .font(type_::SECTION)
                .color(if desktop.empty {
                    t.text_muted
                } else {
                    t.text_tertiary
                })
                .single_line()
                .truncate()
                .grow(1.0),
        )
        .children(keys.map(|keys| {
            text(keys)
                .key(format!("{key}.keys"))
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }))
}

const NAME_MIN_W: f32 = 64.0;

fn agent_row(key: String, agent: &AgentRow, logos: bool, t: &Tokens) -> El {
    let id = agent.id.clone();
    let name = agent.shown_name();
    let row = row()
        .key(key.clone())
        .role("button")
        .focusable(true)
        .label(format!("Open {}", agent.name))
        .a11y_value(agent.state.wire())
        .help(agent.hover())
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S2)
        .pl(space::S3)
        .pr(space::S1)
        .radius(4.0)
        .fill(row_fill(agent, t))
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
        .on_drag(|state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            crate::desktop::drag::gesture(state, d, tx, crate::desktop::drag::Owner::Sidebar)
        })
        .child(lead(&key, agent, logos, t))
        .child(
            text(name)
                .key(format!("{key}.name"))
                .font(type_::BODY)
                .color(t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0)
                .shrink()
                .min_w(NAME_MIN_W),
        )
        .children(
            agent
                .run
                .as_ref()
                .map(|run| crate::desktop::provenance::run_view(&key, run, t)),
        )
        .children((agent.state == AgentState::Recoverable).then(|| {
            text("recoverable")
                .key(format!("{key}.recoverable"))
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }))
        .children(
            agent
                .chief
                .then(|| mark(&key, "chief", attn_ui::icon::BOLT, "Chief of staff", t)),
        )
        .children(
            agent
                .delegated
                .then(|| mark(&key, "delegated", "↳", "Delegated from chief of staff", t)),
        )
        .children(
            agent
                .chain
                .as_ref()
                .map(|c| crate::desktop::chain::chip(&key, &agent.id, c, t)),
        )
        .children(crate::asks::marker(&key, agent.asks, t));
    match &agent.chain {
        Some(_) => crate::desktop::chain::row_hovers(row, &key, &agent.id),
        None => row,
    }
}

fn rename_desktop_button(row_key: &str, desktop: &DesktopHeader, t: &Tokens) -> El {
    let (id, under) = (desktop.id.clone(), format!("{row_key}.rename"));
    queue_view::control(
        row_key,
        "rename",
        format!("Rename {}", desktop.title),
        attn_ui::icon::PENCIL,
        t,
        move |state, tx| rename::desktop(state, &id, under.clone(), tx),
    )
}

fn mark(row_key: &str, name: &str, glyph: &str, label: &str, t: &Tokens) -> El {
    text(glyph)
        .key(format!("{row_key}.{name}"))
        .role("img")
        .label(label)
        .font(type_::MONO_CAPTION)
        .color(t.accent)
        .px(5.0)
        .radius(9.0)
        .fill(t.accent.alpha(0.14))
}

fn settle_button(row_key: &str, agent: &AgentRow, t: &Tokens) -> El {
    let id = agent.id.clone();
    queue_view::control(
        row_key,
        "settle",
        format!("Settle {}", agent.name),
        "✓",
        t,
        move |state, _| actions::settle(state, &id),
    )
}

/// The × button that closes a desktop, asking first when it holds tiles.
fn close_desktop_button(row_key: &str, desktop: &DesktopHeader, t: &Tokens) -> El {
    let id = desktop.id.clone();
    queue_view::control(
        row_key,
        "close",
        format!("Close {}", desktop.title),
        "×",
        t,
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

/// The ••• button that opens an agent's actions menu under itself.
pub(crate) fn actions_button(row_key: &str, agent: &AgentRow, t: &Tokens) -> El {
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
        t,
        move |state, tx| session_menu::open(state, &id, name.clone(), under.clone(), tx),
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
        .absolute(0.0, ROW_HEIGHT - 2.0)
        .w_full()
        .h(2.0);
    Some(match drain {
        Some(c) => bar.live_trim(&c.drain),
        None => bar,
    })
}

pub(crate) fn logos(state: &State) -> bool {
    crate::setting::on(&state.facts.settings, HARNESS_LOGOS)
}

/// An agent row's ground, tinted while its machine is unreachable.
pub(crate) fn row_fill(agent: &AgentRow, t: &Tokens) -> rune::Color {
    match (agent.offline, agent.selected) {
        (true, false) => t.error.alpha(0.07),
        (true, true) => t.error.alpha(0.12),
        (false, true) => t.bg_selected,
        (false, false) => t.bg_panel,
    }
}

pub(crate) fn lead(row_key: &str, agent: &AgentRow, logos: bool, t: &Tokens) -> El {
    if !logos || agent.state == AgentState::Launching || agent.offline {
        return state_mark(row_key, agent, t);
    }
    rune::icon(harness_icon::of(&agent.harness), 14.0)
        .key(format!("{row_key}.state"))
        .role("img")
        .label(agent.lead())
        .a11y_value(agent.state.wire())
        .color(state_color(agent.state, t))
}

/// The dot in each agent's colour of state, as the Tauri app's `.session-lead[data-state]`.
pub(crate) fn state_mark(row_key: &str, agent: &AgentRow, t: &Tokens) -> El {
    let mark = el()
        .key(format!("{row_key}.state"))
        .role("img")
        .label(agent.lead())
        .a11y_value(agent.state.wire())
        .w(8.0)
        .h(8.0)
        .radius(4.0);
    if !agent.offline {
        return mark.fill(state_color(agent.state, t));
    }
    mark.border(2.0, state_color(agent.state, t)).child(
        el().absolute(-2.0, 3.25)
            .w(12.0)
            .h(1.5)
            .fill(t.text_secondary)
            .rotate(-std::f32::consts::FRAC_PI_4)
            .inert(),
    )
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

fn tile_row(key: String, tile: &TileRow, t: &Tokens) -> El {
    let (desktop, leaf) = (tile.desktop.clone(), tile.id.clone());
    row()
        .key(key.clone())
        .role("button")
        .focusable(true)
        .label(format!("Open {}", tile.title))
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S2)
        .pl(space::S3)
        .pr(space::S1)
        .radius(4.0)
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_tile(state, &desktop, &leaf, tx))
        .child(
            text(tile.title.clone())
                .font(type_::BODY)
                .color(t.text_secondary)
                .single_line()
                .truncate()
                .grow(1.0),
        )
        .child(
            text(tile.word())
                .font(type_::BADGE)
                .color(t.text_dimmed)
                .border(1.0, t.border)
                .radius(3.0)
                .px(3.0),
        )
        .hover_group()
        .child(
            overlaid_controls(&key, false, t)
                .children(
                    (tile.kind == crate::browser::KIND).then(|| reload_tile_button(&key, tile, t)),
                )
                .child(close_tile_button(&key, tile, t)),
        )
}

/// A row's hover controls, laid over its right end as the Tauri app's `.session-actions`
/// and `.desktop-actions` are, so the row keeps its trailing hint or tag where it would be
/// without them, and only hides what they cover.
fn overlaid_controls(row_key: &str, beside_keys: bool, t: &Tokens) -> El {
    rune::row()
        .key(format!("{row_key}.controls"))
        .group_opacity(0.0, 1.0)
        .pin(
            None,
            Some(0.0),
            Some(if beside_keys { 28.0 } else { 4.0 }),
            Some(0.0),
        )
        .items_center()
        .gap(2.0)
        .pl(6.0)
        .fill(t.bg_elevated)
}

fn close_tile_button(row_key: &str, tile: &TileRow, t: &Tokens) -> El {
    let (desktop, leaf) = (tile.desktop.clone(), tile.id.clone());
    queue_view::control(
        row_key,
        "close",
        format!("Close {}", tile.title),
        "×",
        t,
        move |state, tx| crate::desktop::remove_tile(state, desktop.clone(), leaf.clone(), tx),
    )
}

/// The browser row's button that loads its page again, as the tile header's does.
fn reload_tile_button(row: &str, tile: &TileRow, t: &Tokens) -> El {
    let page = attn_client::TileKey {
        desktop: tile.desktop.clone(),
        tile: tile.id.clone(),
    };
    attn_ui::GlyphButton::new("↻", format!("Reload {}", tile.title))
        .variant(rune_kit::ButtonVariant::GhostFaint)
        .font(type_::LABEL)
        .side(type_::LABEL.line_px())
        .on_press_tx(move |_: &mut State, _: &mut Tx| crate::browser::reload(&page))
        .build(attn_ui::kit(t))
        .key(format!("{row}.reload"))
        .radius(4.0)
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

/// Shows the agent where it is and gives its terminal the keyboard, as a click on its row in
/// the Tauri app does.
pub(crate) fn open_agent(state: &mut State, session: &str, tx: &mut Tx) {
    if let Some(run) = state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == session)
        .and_then(|s| s.automation.as_ref())
    {
        state.window.run_groups_open.insert(run.definition_id);
    }
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

/// What dragging agent `session`'s row picks up: its pane, on the desktop it shows on.
pub(crate) fn row_source(state: &State, session: &str) -> Option<crate::desktop::drag::Source> {
    let (desktop, leaf) = pane_of(state, session)?;
    let found = state.facts.sessions.iter().find(|s| s.id == session)?;
    Some(crate::desktop::drag::Source::Row {
        desktop,
        leaf,
        label: model::display_name(found),
    })
}

fn pane_of(state: &State, session: &str) -> Option<(String, String)> {
    state.facts.desktops.iter().find_map(|d| {
        let pane = d.panes.iter().find(|p| p.session_id == session)?;
        Some((d.id.clone(), pane.pane_id.clone()))
    })
}

/// When an agent shows alone, the sidebar it hides lets go: its agent list closes, and the
/// keyboard it held goes to the agent, as in the Tauri app.
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
