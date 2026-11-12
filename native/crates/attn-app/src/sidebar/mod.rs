mod actions;
mod chrome;
pub(crate) mod model;
pub(crate) mod queue;
mod queue_view;
pub(crate) mod session_menu;
pub(crate) mod snooze;

use std::cell::Cell;
use std::rc::Rc;
use std::sync::atomic::{AtomicU64, Ordering};

use attn_protocol::DesktopShowSessionMessage;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, GrowthSpec, ScrollSpec, Tx, col, el, memo, row, text};

pub use model::AgentState;
use model::{AgentRow, DesktopHeader, Line, TileRow};
pub(crate) use model::{display_name, tile_title};

use crate::State;
use crate::shell::View;

pub(crate) const WIDTH: f32 = 240.0;
pub(crate) const ROW_HEIGHT: f32 = 28.0;

thread_local! {
    static ROWS_BUILT: Cell<usize> = const { Cell::new(0) };
}

pub(crate) use chrome::title_controls;

/// The window's sidebar for its flow: the queue sidebar in queue flow, the desktop sidebar
/// otherwise. Collapsed, desktop flow keeps the rail and queue flow keeps nothing here,
/// since the queue bar moves into the title bar.
pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    match (actions::queue_flow(state), state.window.sidebar_hidden) {
        (true, false) => Some(queue_view::view(state, t)),
        (true, true) => None,
        (false, false) => Some(desktop_sidebar(state, t)),
        (false, true) => Some(chrome::rail(state, t)),
    }
}

/// What the title bar's middle holds for the sidebar: the queue bar, in queue flow with the
/// sidebar collapsed.
pub(crate) fn title_middle(state: &State, t: &Tokens) -> Option<El> {
    (actions::queue_flow(state) && state.window.sidebar_hidden).then(|| {
        let bands = queue::bands(&state.facts, state.window.now_ms);
        chrome::queue_bar(state, &bands, t)
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
        Action::SidebarAgentList if queue && !state.window.sidebar_hidden => {
            actions::toggle_agent_list(state, tx)
        }
        Action::SidebarAgentList => crate::palette::open(state, crate::palette::Face::Agents, tx),
        Action::SessionSettle => actions::settle_on_screen(state, tx),
        Action::SessionPriority => actions::toggle_priority_on_screen(state),
        Action::SessionSnooze => actions::snooze_on_screen(state, tx),
        Action::SessionCancelCountdown => actions::cancel_countdown(state),
        Action::SessionPrev if queue => actions::step(state, -1, tx),
        Action::SessionNext if queue => actions::step(state, 1, tx),
        _ => return false,
    }
    true
}

pub(crate) use actions::{advance, wake};

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

fn request_id(cmd: &str) -> String {
    static NEXT: AtomicU64 = AtomicU64::new(1);
    format!("{cmd}-native-{}", NEXT.fetch_add(1, Ordering::Relaxed))
}

fn desktop_sidebar(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let home = state.window.view == View::Home;
    let on_screen = crate::desktop::on_screen_sessions(state);
    let reads = (
        std::sync::Arc::as_ptr(&facts.sessions) as usize,
        std::sync::Arc::as_ptr(&facts.desktops) as usize,
        std::sync::Arc::as_ptr(&facts.profiles) as usize,
        home,
        on_screen.clone(),
        *t,
    );
    memo("sidebar", reads, || {
        let lines: Rc<[Line]> = model::desktop_lines(facts, home, &on_screen).into();
        let keys = lines.clone();
        let t = *t;
        col()
            .key("sidebar")
            .role("navigation")
            .label("Sidebar")
            .w(WIDTH)
            .h_full()
            .fill(t.bg_panel)
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
            .padding(space::S2)
            .gap(space::S1)
            .child(home_row(home, &t))
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
                            .key("sidebar.desktops")
                            .role("list")
                            .label("Desktops")
                            .w_full()
                            .growth(GrowthSpec::default())
                            .virtual_rows(
                                lines.len(),
                                ROW_HEIGHT,
                                move |_: &State, i| keys[i].key().into(),
                                move |state: &State, i| line(state, &lines[i], &t),
                            ),
                    ),
            )
    })
}

pub(crate) fn home_row(home: bool, t: &Tokens) -> El {
    row()
        .key("sidebar.home")
        .role("button")
        .label("Home")
        .a11y_value(if home { "current" } else { "" })
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S2)
        .padding(space::S1)
        .radius(4.0)
        .fill(if home { t.bg_selected } else { t.bg_panel })
        .hover(t.bg_elevated)
        .on_press(|state: &mut State| state.window.go_home())
        .child(text("⌂").font(type_::BODY).color(t.text_secondary))
        .child(text("Home").font(type_::BODY).color(t.text_primary))
}

fn line(state: &State, line: &Line, t: &Tokens) -> El {
    let key = line.key();
    let draining = match line {
        Line::Agent { row, .. } => state.window.countdowns.contains_key(&row.id),
        _ => false,
    };
    memo(key.clone(), (line, draining, *t), || {
        ROWS_BUILT.with(|n| n.set(n.get() + 1));
        match line {
            Line::Desktop(desktop) => desktop_row(key, desktop, t),
            Line::Agent { row, .. } => {
                let bar = settling_bar(state, &key, row, t);
                let more = rune::row()
                    .key(format!("{key}.controls"))
                    .group_opacity(0.0, 1.0)
                    .child(actions_button(&key, row, t));
                agent_row(key, row, t)
                    .hover_group()
                    .child(more)
                    .children(bar)
            }
            Line::Tile(tile) => tile_row(key, tile, t),
        }
    })
}

fn desktop_row(key: String, desktop: &DesktopHeader, t: &Tokens) -> El {
    let id = desktop.id.clone();
    row()
        .key(key)
        .role("button")
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
}

fn agent_row(key: String, agent: &AgentRow, t: &Tokens) -> El {
    let id = agent.id.clone();
    let name = if agent.priority {
        format!("⚑{}", agent.name)
    } else {
        agent.name.clone()
    };
    row()
        .key(key.clone())
        .role("button")
        .label(format!("Open {}", agent.name))
        .a11y_value(agent.state.wire())
        .w_full()
        .h(ROW_HEIGHT)
        .items_center()
        .gap(space::S2)
        .pl(space::S3)
        .pr(space::S1)
        .radius(4.0)
        .fill(if agent.selected {
            t.bg_selected
        } else {
            t.bg_panel
        })
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
        .child(state_mark(&key, agent, t))
        .child(
            text(name)
                .font(type_::BODY)
                .color(t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0),
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
    )
    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
        session_menu::open(state, &id, name.clone(), under.clone(), tx)
    })
}

/// One auto-settle countdown's drain: full when first seen, empty at its deadline.
pub(crate) struct Countdown {
    fires_at: String,
    drain: rune::Tween<[f32; 2]>,
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
            let left_ms = queue::epoch_ms(&fires_at).map_or(0, |at| (at - now_ms).max(0));
            Countdown {
                drain: rune::Tween::linear(
                    [0.0, 1.0],
                    [1.0, 1.0],
                    std::time::Duration::from_millis(left_ms as u64),
                ),
                fires_at,
            }
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

/// The dot in each agent's colour of state, as the Tauri app's `.session-lead[data-state]`.
pub(crate) fn state_mark(row_key: &str, agent: &AgentRow, t: &Tokens) -> El {
    el().key(format!("{row_key}.state"))
        .role("img")
        .label(agent.lead())
        .a11y_value(agent.state.wire())
        .w(8.0)
        .h(8.0)
        .radius(4.0)
        .fill(state_color(agent.state, t))
}

pub(crate) fn state_color(state: AgentState, t: &Tokens) -> rune::Color {
    let hex = |s| rune::color::hex(s).expect("a state color is #rrggbb");
    match state {
        AgentState::Idle | AgentState::Recoverable => hex("#6b7280"),
        AgentState::Working => hex("#22c55e"),
        AgentState::WaitingInput => hex("#f59e0b"),
        AgentState::PendingApproval => hex("#eab308"),
        AgentState::Scheduled => hex("#0ea5e9"),
        AgentState::Unknown => hex("#a855f7"),
        AgentState::Launching => t.text_tertiary,
    }
}

fn tile_row(key: String, tile: &TileRow, t: &Tokens) -> El {
    let (desktop, leaf) = (tile.desktop.clone(), tile.id.clone());
    row()
        .key(key)
        .role("button")
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
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary),
        )
}

pub(crate) fn open_tile(state: &mut State, desktop: &str, tile: &str, tx: &mut Tx) {
    let action = crate::Action::OpenTile {
        desktop: desktop.into(),
        tile: tile.into(),
    };
    crate::dispatch(state, action, tx);
}

/// Shows the agent where it is, as a click on its row in the Tauri app does.
pub(crate) fn open_agent(state: &mut State, session: &str, _: &mut Tx) {
    state.window.view = View::Desktop;
    state.window.follow_next_turn = false;
    let _ = state.daemon.send(&DesktopShowSessionMessage {
        request_id: request_id("desktop_show_session"),
        session_id: session.into(),
        ..DesktopShowSessionMessage::new()
    });
}

pub(crate) fn open_desktop(state: &mut State, desktop: &str, tx: &mut Tx) {
    let action = crate::Action::GoToDesktop {
        desktop: desktop.into(),
    };
    crate::dispatch(state, action, tx);
}
