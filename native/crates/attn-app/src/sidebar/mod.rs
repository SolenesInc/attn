pub(crate) mod actions;
pub(crate) mod bar_peek;
mod chips;
pub(crate) mod crew;
pub(crate) mod crowding;
pub(crate) mod dock;
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
use attn_ui::blocks::Inline;
use attn_ui::system::{self, DotShape, DotSize, GroupHead, IconSize, ListRow, Tone, Type, words};
use rune::{El, GrowthSpec, ScrollSpec, Tx, col, memo, row};
use rune_kit::ScrollBar;

pub(crate) use model::{AgentRow, Line, display_name, tile_title};
pub use model::{AgentState, SESSION_EMOJIS};
use model::{DesktopHeader, TileRow};

use crate::State;
use crate::action::Action;
use crate::desktop::DesktopMark;
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
pub(crate) fn title_middle(state: &State, shelf: El, t: &Tokens) -> El {
    if actions::queue_flow(state) && state.window.sidebar_hidden {
        let bands = queue::bands(&state.facts, state.window.now_ms);
        chips::queue_bar(state, &bands, shelf, t)
    } else {
        shelf
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
                            .key(crew::CLASSIC_ROW)
                            .role("list")
                            .label("Crew between sessions")
                            .w_full()
                            .child(queue_view::crew_rule("sidebar.rule.crew".into(), &t))
                            .children(queue_view::chief_slot(state, &t))
                            .children(resting.iter().map(|m| {
                                crew::row_view(format!("{}.{}", crew::CLASSIC_ROW, m.member), m, &t)
                            }))
                    }))
                    .child(overview_row(overview_keys.clone(), &t))
                    .child(
                        col()
                            .key(DESKTOP_LIST)
                            .role("list")
                            .label("Desktops")
                            .w_full()
                            .growth(GrowthSpec::default())
                            .virtual_rows(
                                lines.len(),
                                ListRow::height(),
                                move |_: &State, i| keys[i].key().into(),
                                move |state: &State, i| line(state, &lines, i, &t),
                            ),
                    )
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

fn overview_row(keys: Option<String>, t: &Tokens) -> El {
    nav_row(
        "sidebar.overview",
        &attn_ui::icon::OVERVIEW,
        "Desktop overview",
        keys,
        false,
        t,
    )
    .on_press_tx(|state: &mut State, tx: &mut Tx| {
        crate::desktop::run(state, &Action::DesktopOverview, tx);
    })
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
                Line::Desktop(desktop) => desktop_row(key.clone(), desktop, keys.clone(), t),
                Line::Agent { row, .. } => {
                    let bar = settling_bar(state, &key, row, t);
                    let controls = row
                        .settle_run
                        .then(|| settle_button(&key, row))
                        .into_iter()
                        .chain([actions_button(&key, row)]);
                    agent_row(key.clone(), row, logos, controls.collect(), t).children(bar)
                }
                Line::Tile(tile) => tile_row(key.clone(), tile, t),
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

/// A desktop's heading: its number, its name when it has one, a hairline, and the keys that go
/// to it, with its rename and close controls on hover.
fn desktop_row(key: String, desktop: &DesktopHeader, keys: Option<String>, t: &Tokens) -> El {
    let id = desktop.id.clone();
    let dragged = desktop.id.clone();
    let head = GroupHead::new(key.clone(), format!("Open {}", desktop.title))
        .mark(system::number_chip(
            desktop.number,
            desktop.current,
            desktop.empty,
            t,
        ))
        .keys(keys.clone())
        .controls([
            rename_desktop_button(&key, desktop),
            close_desktop_button(&key, desktop),
        ]);
    let head = if desktop.named {
        head.name(desktop.title.clone())
    } else {
        head
    };
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

/// An agent's row, its hover controls laid over its end.
fn agent_row(
    key: String,
    agent: &AgentRow,
    logos: bool,
    controls: Vec<system::IconButton>,
    t: &Tokens,
) -> El {
    let id = agent.id.clone();
    let name = flagged(&key, agent, t).shrink().min_w(NAME_MIN_W).child(
        words(agent.name.clone(), Type::Body, t.text_primary)
            .key(format!("{key}.name"))
            .single_line()
            .truncate()
            .shrink(),
    );
    let mut row = ListRow::new(key.clone(), format!("Open {}", agent.name))
        .lead(lead(&key, agent, logos, t))
        .title_el(name)
        .in_view(agent.selected)
        .tint(agent.offline.then_some(t.error))
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
    if agent.chief {
        row = row.badge(mark(
            &key,
            "chief",
            &attn_ui::icon::CHIEF,
            "Chief of staff",
            t,
        ));
    }
    if agent.delegated {
        row = row.badge(mark(
            &key,
            "delegated",
            "↳",
            "Delegated from chief of staff",
            t,
        ));
    }
    let row = row
        .build(t)
        .a11y_value(agent.state.wire())
        .help(agent.hover())
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
        .on_drag(|state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            crate::desktop::drag::gesture(state, d, tx, crate::desktop::drag::Owner::Sidebar)
        });
    match &agent.chain {
        Some(_) => crate::desktop::chain::row_hovers(row, &key, &agent.id),
        None => row,
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

fn mark(
    row_key: &str,
    name: &str,
    glyph: impl Into<attn_ui::Glyph>,
    label: &str,
    t: &Tokens,
) -> El {
    system::mark_chip(glyph, t.accent, t)
        .key(format!("{row_key}.{name}"))
        .role("img")
        .label(label)
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

/// The ••• button that opens an agent's actions menu under itself.
pub(crate) fn actions_button(row_key: &str, agent: &AgentRow) -> system::IconButton {
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

pub(crate) fn lead(row_key: &str, agent: &AgentRow, logos: bool, t: &Tokens) -> El {
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
    system::icon(
        harness_icon::of(&agent.harness),
        IconSize::Regular,
        state_color(agent.state, t),
    )
    .key(format!("{row_key}.state"))
    .role("img")
    .label(agent.lead())
    .a11y_value(agent.state.wire())
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

fn tile_row(key: String, tile: &TileRow, t: &Tokens) -> El {
    let (desktop, leaf) = (tile.desktop.clone(), tile.id.clone());
    let reload = (tile.kind == crate::browser::KIND).then(|| reload_tile_button(&key, tile));
    ListRow::new(key.clone(), format!("Open {}", tile.title))
        .lead(system::icon(
            tile.icon(),
            IconSize::Regular,
            t.text_tertiary,
        ))
        .title(tile.title.clone())
        .aside(system::tag(
            format!("{key}.kind"),
            tile.word(),
            Tone::Neutral,
            t,
        ))
        .hover_controls(reload.into_iter().chain([close_tile_button(&key, tile)]))
        .in_view(tile.selected)
        .build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_tile(state, &desktop, &leaf, tx))
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
