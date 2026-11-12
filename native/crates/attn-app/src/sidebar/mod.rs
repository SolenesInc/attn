mod model;

use std::cell::Cell;
use std::rc::Rc;
use std::sync::atomic::{AtomicU64, Ordering};

use attn_protocol::{DesktopSetCurrentMessage, DesktopShowLeafMessage, DesktopShowSessionMessage};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, GrowthSpec, ScrollSpec, Tx, col, el, memo, row, text};

pub use model::AgentState;
use model::{AgentRow, DesktopHeader, Line, TileRow};

use crate::State;
use crate::shell::View;

pub(crate) const WIDTH: f32 = 240.0;
const ROW_HEIGHT: f32 = 28.0;

thread_local! {
    static ROWS_BUILT: Cell<usize> = const { Cell::new(0) };
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

pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let home = state.window.view == View::Home;
    let reads = (
        std::sync::Arc::as_ptr(&facts.sessions) as usize,
        std::sync::Arc::as_ptr(&facts.desktops) as usize,
        std::sync::Arc::as_ptr(&facts.profiles) as usize,
        home,
        *t,
    );
    memo("sidebar", reads, || {
        let lines: Rc<[Line]> = model::desktop_lines(facts, home).into();
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
                                move |_: &State, i| line(&lines[i], &t),
                            ),
                    ),
            )
    })
}

fn home_row(home: bool, t: &Tokens) -> El {
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

fn line(line: &Line, t: &Tokens) -> El {
    let key = line.key();
    memo(key.clone(), (line, *t), || {
        ROWS_BUILT.with(|n| n.set(n.get() + 1));
        match line {
            Line::Desktop(desktop) => desktop_row(key, desktop, t),
            Line::Agent { row, .. } => agent_row(key, row, t),
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

fn open_tile(state: &mut State, desktop: &str, leaf: &str, _: &mut Tx) {
    state.window.view = View::Desktop;
    let _ = state.daemon.send(&DesktopShowLeafMessage {
        request_id: request_id("desktop_show_leaf"),
        desktop_id: desktop.into(),
        leaf_id: leaf.into(),
        ..DesktopShowLeafMessage::new()
    });
}

/// Shows the agent where it is, as a click on its row in the Tauri app does.
pub(crate) fn open_agent(state: &mut State, session: &str, _: &mut Tx) {
    state.window.view = View::Desktop;
    let _ = state.daemon.send(&DesktopShowSessionMessage {
        request_id: request_id("desktop_show_session"),
        session_id: session.into(),
        ..DesktopShowSessionMessage::new()
    });
}

fn open_desktop(state: &mut State, desktop: &str, _: &mut Tx) {
    state.window.view = View::Desktop;
    let _ = state.daemon.send(&DesktopSetCurrentMessage {
        request_id: request_id("desktop_set_current"),
        desktop_id: desktop.into(),
        profile_id: state.facts.profiles.selected.clone(),
        ..DesktopSetCurrentMessage::new()
    });
}
