use attn_protocol::{Desktop, DesktopPane, Session};
use attn_term::mouse;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{Drag, El, Mods, PointerButton, Tx, col, el, memo, row, text};

use super::Area;
use super::layout::{Direction, Leaf, LeafKind, Node};
use crate::State;
use crate::shell::Holder;
use crate::terminal::{Release, Terminals, accelerator};

const GAP: f32 = 6.0;
const HEADER_PX: f32 = 2.0 * space::S1 + 18.0;
const SUBLINE_PX: f32 = 15.0;
const AGENT_HEADER_PX: f32 = HEADER_PX + SUBLINE_PX;
const SUBLINE_TYPE: rune::TextStyle = rune::TextStyle::new(11.0, 400.0);
const SETTLING_TRACK_PX: f32 = 2.0;

/// What an agent's header says about its turn settling on its own.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Settle {
    /// Settles when its countdown runs out, the deadline named.
    Counting(String),
    /// The countdown stands still until the host comes back.
    Held,
    /// The person kept the next turn: it will not settle on its own.
    Kept,
}

impl Settle {
    fn of(session: &attn_protocol::Session) -> Option<Settle> {
        match &session.auto_settle_fires_at {
            Some(at) if !at.is_empty() => Some(Settle::Counting(at.clone())),
            _ if session.auto_settle_held == Some(true) => Some(Settle::Held),
            _ if session.auto_settle_dismiss_armed == Some(true) => Some(Settle::Kept),
            _ => None,
        }
    }
}

/// The tile area: the current desktop's screen of its row, each tile with its header.
pub(crate) fn area(state: &State, t: &Tokens, scale: f32) -> El {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let desktop = facts.current_desktop();
    let holder = state.window.keyboard.clone();
    let composing = state.window.keyboard_terminal(facts).and_then(|id| {
        let terminals = &state.terminals;
        let font = terminals
            .cell_font(&id)
            .map(|(size, h)| [size, h].map(f32::to_bits));
        Some((terminals.preedit(&id)?.to_owned(), font))
    });
    let row_state = desktop.map(|d| desk.row(&d.id));
    let area_bits = desk
        .area
        .map(|a| [a.w, a.h, a.px_per_col].map(f32::to_bits));
    let dragging = desk
        .drag
        .as_ref()
        .and_then(|d| d.dragging())
        .map(str::to_owned);
    let reads = (
        version(&facts.desktops),
        version(&facts.profiles),
        version(&facts.settings),
        holder.clone(),
        composing,
        state.terminals.pointing_link().map(str::to_owned),
        row_state,
        area_bits,
        dragging.clone(),
        (ratio_bits(&desk.ratios), desk.peeks.unsent()),
        *t,
        scale.to_bits(),
    );
    let settles = desktop.map_or_else(Vec::new, |d| settles(d, &facts.sessions));
    let keys = header_keys(state);
    let names: Vec<crate::sidebar::AgentRow> = desktop
        .map(|d| {
            d.panes
                .iter()
                .filter_map(|p| facts.sessions.iter().find(|s| s.id == p.session_id))
                .map(|s| crate::sidebar::AgentRow::of(s, false))
                .collect()
        })
        .unwrap_or_default();
    let reads = (reads, settles.clone(), keys.clone(), names);
    let launcher = desktop
        .filter(|d| d.panes.is_empty())
        .and_then(|_| crate::picker::inline(state, t));
    let content = launcher.unwrap_or_else(|| {
        memo("tiles.area", reads, || {
            col().w_full().h_full().min_w(0.0).min_h(0.0).child(screen(
                desktop,
                desk,
                facts,
                (&settles, &keys),
                holder.as_ref(),
                &state.terminals,
                (t, scale),
            ))
        })
    });
    col()
        .key("tiles")
        .on_drag(super::divider::area_gesture)
        .role("main")
        .label("Tiles")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .padding(GAP)
        .on_layout_tx(|state: &mut State, [x, y, w, h], tx: &mut Tx| {
            let px_per_col = state.terminals.metrics().cell_w;
            let area = Some(Area {
                x: x + GAP,
                y: y + GAP,
                w: w - 2.0 * GAP,
                h: h - 2.0 * GAP,
                px_per_col,
            });
            if state.window.desk.area == area {
                tx.live_only();
                return;
            }
            state.window.desk.area = area;
            super::settle(state, tx);
        })
        .child(content)
}

fn settles(desktop: &Desktop, sessions: &[Session]) -> Vec<(String, Settle)> {
    desktop
        .panes
        .iter()
        .filter_map(|p| {
            let session = sessions.iter().find(|s| s.id == p.session_id)?;
            Some((p.pane_id.clone(), Settle::of(session)?))
        })
        .collect()
}

/// The keys a tile header names beside the buttons that do the same.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
struct HeaderKeys {
    keep: Option<String>,
    pop_out: Option<String>,
    focus: Option<String>,
}

fn header_keys(state: &State) -> HeaderKeys {
    let key_of = |action: &crate::action::Action| {
        state.keymap.binding(action).map(|b| state.keymap.label(b))
    };
    HeaderKeys {
        keep: key_of(&crate::action::Action::SessionCancelCountdown),
        pop_out: key_of(&crate::action::Action::TilePopOut),
        focus: key_of(&crate::action::Action::TerminalToggleMaximize),
    }
}

fn with_key(label: &str, key: Option<&String>) -> String {
    match key {
        Some(key) => format!("{label} ({key})"),
        None => label.to_owned(),
    }
}

/// `leaf` alone, filling a window of its own.
pub(super) fn lone(state: &State, desktop: &Desktop, leaf: &Leaf, t: &Tokens, scale: f32) -> El {
    let settles = settles(desktop, &state.facts.sessions);
    let keys = header_keys(state);
    let unsent = state.window.desk.peeks.unsent();
    let cx = TileCx {
        desktop,
        sessions: &state.facts.sessions,
        unsent: &unsent,
        settles: &settles,
        keys: &keys,
        focused: None,
        drains: &state.window.desk.settling,
        ratios: &state.window.desk.ratios,
        dividers: false,
        dragging: None,
        holder: state.window.keyboard.as_ref(),
        terminals: &state.terminals,
        t,
        scale,
    };
    tile(leaf, &cx)
}

fn screen(
    desktop: Option<&Desktop>,
    desk: &super::Desk,
    facts: &attn_client::Facts,
    (settles, keys): (&[(String, Settle)], &HeaderKeys),
    holder: Option<&Holder>,
    terminals: &Terminals,
    (t, scale): (&Tokens, f32),
) -> El {
    match desktop {
        None => empty(t, "No desktop"),
        Some(_) if desk.area.is_none() => col(),
        Some(desktop) => match desk.plan(facts, desktop) {
            None => empty(t, "Empty desktop"),
            Some((tree, plan)) => {
                let view = desk.row(&desktop.id).view;
                let rendered = plan.rendered(&tree, &view, &desktop.active_pane_id);
                let dragging = desk.drag.as_ref().and_then(|d| d.dragging());
                let unsent = desk.peeks.unsent();
                let cx = TileCx {
                    desktop,
                    sessions: &facts.sessions,
                    unsent: &unsent,
                    settles,
                    keys,
                    focused: match &view {
                        super::layout::View::Focused(leaf) => Some(leaf.as_str()),
                        _ => None,
                    },
                    drains: &desk.settling,
                    ratios: &desk.ratios,
                    dividers: view == super::layout::View::Normal,
                    dragging,
                    holder,
                    terminals,
                    t,
                    scale,
                };
                node(&rendered, &cx)
            }
        },
    }
}

fn ratio_bits(
    ratios: &std::collections::HashMap<String, super::divider::RatioOverride>,
) -> Vec<(String, u32)> {
    let mut bits: Vec<(String, u32)> = ratios
        .iter()
        .map(|(k, o)| (k.clone(), o.ratio.to_bits()))
        .collect();
    bits.sort();
    bits
}

fn version<T>(arc: &std::sync::Arc<T>) -> usize {
    std::sync::Arc::as_ptr(arc) as usize
}

fn empty(t: &Tokens, message: &str) -> El {
    col()
        .w_full()
        .h_full()
        .center()
        .child(text(message).font(type_::BODY).color(t.text_tertiary))
}

struct TileCx<'a> {
    desktop: &'a Desktop,
    sessions: &'a [Session],
    /// Replies a peek could not send, by terminal.
    unsent: &'a [(String, String)],
    /// How each agent's turn settles, by leaf.
    settles: &'a [(String, Settle)],
    keys: &'a HeaderKeys,
    /// The leaf this desktop shows alone.
    focused: Option<&'a str>,
    drains: &'a std::collections::HashMap<String, crate::sidebar::Countdown>,
    /// Splits this window shows at its own ratio, ahead of the daemon.
    ratios: &'a std::collections::HashMap<String, super::divider::RatioOverride>,
    /// Dividers drag only in the normal view.
    dividers: bool,
    dragging: Option<&'a str>,
    holder: Option<&'a Holder>,
    terminals: &'a Terminals,
    t: &'a Tokens,
    scale: f32,
}

fn node(n: &Node, cx: &TileCx) -> El {
    match n {
        Node::Leaf(leaf) => tile(leaf, cx),
        Node::Split(split) => {
            let [a, b] = &*split.children;
            let parent = match split.direction {
                Direction::Vertical => row(),
                Direction::Horizontal => col(),
            };
            let ratio = cx.ratios.get(&split.id).map_or(split.ratio, |o| o.ratio);
            let key = format!("split.{}", split.id);
            let recorded = key.clone();
            parent
                .key(key)
                .w_full()
                .h_full()
                .min_w(0.0)
                .min_h(0.0)
                .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
                    let rects = &mut state.window.desk.rects;
                    if rects.get(&recorded) == Some(&rect) {
                        tx.live_only();
                        return;
                    }
                    rects.insert(recorded.clone(), rect);
                })
                .child(share(node(a, cx), ratio))
                .child(divider(split.id.clone(), split.direction, cx))
                .child(share(node(b, cx), 1.0 - ratio))
        }
    }
}

/// The gap between a split's two sides, which drags the split's ratio, or between screen
/// columns their widths. The tile area runs the drag, so it outlives the divider moving.
fn divider(split: String, direction: Direction, cx: &TileCx) -> El {
    let key = format!("divider.{split}");
    let bar = match direction {
        Direction::Vertical => rune::el().w(GAP).h_full(),
        Direction::Horizontal => rune::el().h(GAP).w_full(),
    };
    if !cx.dividers {
        return bar;
    }
    bar.key(key)
        .role("separator")
        .cursor(match direction {
            Direction::Vertical => rune::Cursor::ResizeColumn,
            Direction::Horizontal => rune::Cursor::ResizeRow,
        })
        .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
            let dividers = &mut state.window.desk.dividers;
            if dividers.get(&split) == Some(&(rect, direction)) {
                tx.live_only();
                return;
            }
            dividers.insert(split.clone(), (rect, direction));
        })
}

pub(super) fn share(child: El, ratio: f32) -> El {
    col()
        .grow(ratio.max(0.001))
        .min_w(0.0)
        .min_h(0.0)
        .child(child)
}

/// The name a leaf goes by in its header, on the shelf and to assistive tech: an agent by its
/// session's name, a tile by what it holds.
pub(crate) fn leaf_title(desktop: &Desktop, sessions: &[Session], leaf: &Leaf) -> String {
    match &leaf.kind {
        LeafKind::Tile { kind, params, .. } => {
            crate::sidebar::tile_title(kind, params.as_deref().unwrap_or(""))
        }
        LeafKind::Pane => {
            let pane = pane_of(desktop, &leaf.id);
            pane.and_then(|p| sessions.iter().find(|s| s.id == p.session_id))
                .map(crate::sidebar::display_name)
                .or_else(|| pane.map(|p| p.title.clone()).filter(|t| !t.is_empty()))
                .unwrap_or_else(|| "Terminal".into())
        }
    }
}

pub(crate) fn tile_kind_label(kind: &str) -> &str {
    match kind {
        "markdown" => "Markdown",
        "browser" => "Browser",
        "seed" => "Seed",
        "notebook" => "Editor",
        other => other,
    }
}

pub(crate) fn pane_of<'a>(desktop: &'a Desktop, id: &str) -> Option<&'a DesktopPane> {
    desktop.panes.iter().find(|p| p.pane_id == id)
}

fn tile(leaf: &Leaf, cx: &TileCx) -> El {
    let t = cx.t;
    let desktop = cx.desktop;
    let active = leaf.id == desktop.active_pane_id;
    let held = cx.holder.is_some_and(|h| h.pane == leaf.id);
    let title = leaf_title(desktop, cx.sessions, leaf);
    let holder = Holder {
        desktop: desktop.id.clone(),
        pane: leaf.id.clone(),
    };
    let body = match (&leaf.kind, pane_of(desktop, &leaf.id)) {
        (LeafKind::Pane, Some(pane)) if !pane.runtime_id.is_empty() => {
            let caret = held
                .then(|| ime_caret(cx.terminals, &pane.runtime_id, t.dark))
                .flatten();
            let pointing_link = cx.terminals.pointing_link() == Some(pane.runtime_id.as_str());
            let slot = terminal_slot(
                &pane.runtime_id,
                holder.clone(),
                caret,
                pointing_link,
                (t.dark, cx.scale),
            );
            match cx.unsent.iter().find(|(r, _)| *r == pane.runtime_id) {
                Some((runtime, reply)) => col()
                    .w_full()
                    .grow(1.0)
                    .min_h(0.0)
                    .child(slot)
                    .child(super::peek::unsent_strip(runtime, reply, t)),
                None => slot,
            }
        }
        (LeafKind::Pane, _) => takes(empty(t, "Starting…"), &holder),
        (LeafKind::Tile { kind, .. }, _) => takes(empty(t, tile_kind_label(kind)), &holder),
    };
    let recorded = format!("tile.{}", leaf.id);
    col()
        .key(recorded.clone())
        .w_full()
        .h_full()
        .min_w(0.0)
        .min_h(0.0)
        .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
            let rects = &mut state.window.desk.rects;
            if rects.get(&recorded) == Some(&rect) {
                tx.live_only();
                return;
            }
            rects.insert(recorded.clone(), rect);
        })
        .radius(6.0)
        .fill(t.bg_tile)
        .border(
            1.0,
            if held {
                t.accent
            } else if active {
                t.border_focus
            } else {
                t.border
            },
        )
        .role("group")
        .label(title.clone())
        .opacity(if cx.dragging == Some(leaf.id.as_str()) {
            0.5
        } else {
            1.0
        })
        .child(header(leaf, &title, cx))
        .child(body)
}

/// `el`, where a press takes the tile into `holder`.
fn takes(el: El, holder: &Holder) -> El {
    let holder = holder.clone();
    el.on_press_tx(move |state: &mut State, tx: &mut Tx| press_tile(state, &holder, tx))
}

/// What dragging the header of the current desktop's tile `leaf` picks up. An agent alone on
/// its desktop has nowhere to go.
pub(crate) fn header_source(state: &State, leaf: &str) -> Option<super::drag::Source> {
    let facts = &state.facts;
    let desktop = facts.current_desktop()?;
    let (tree, _) = state.window.desk.plan(facts, desktop)?;
    let kind = &tree.leaves().into_iter().find(|l| l.id == leaf)?.kind;
    Some(super::drag::Source::Header {
        desktop: desktop.id.clone(),
        leaf: leaf.to_owned(),
        label: super::shelf::title(desktop, facts, &tree, leaf),
        movable: matches!(kind, LeafKind::Tile { .. }) || tree.leaves().len() > 1,
    })
}

/// A tile's header, which the tile area drags the tile by. An agent's header carries a second
/// line under its name.
fn header(leaf: &Leaf, title: &str, cx: &TileCx) -> El {
    let t = cx.t;
    let key = format!("tile.{}", leaf.id);
    let settle = cx
        .settles
        .iter()
        .find(|(l, _)| *l == leaf.id)
        .map(|(_, s)| s);
    let pane = pane_of(cx.desktop, &leaf.id);
    let session = pane.and_then(|p| cx.sessions.iter().find(|s| s.id == p.session_id));
    let focused = cx.focused == Some(leaf.id.as_str());
    let popped = leaf.window.is_some();
    let actions = row()
        .key(format!("{key}.actions"))
        .pin(None, Some(0.0), Some(0.0), Some(0.0))
        .items_center()
        .gap(2.0)
        .pl(space::S1)
        .fill(t.bg_tile)
        .group_opacity(0.0, 1.0)
        .children(session.map(|s| rename_button(&key, s, title, t)))
        .children(
            (pane.is_some() && !focused && !popped)
                .then(|| focus_button(&key, &cx.desktop.id, &leaf.id, title, cx)),
        );
    let headline =
        row()
            .w_full()
            .h(HEADER_PX)
            .items_center()
            .gap(space::S2)
            .px(space::S2)
            .children(session.map(|s| {
                crate::sidebar::state_mark(&key, &crate::sidebar::AgentRow::of(s, false), t)
            }))
            .child(
                row()
                    .grow(1.0)
                    .min_w(0.0)
                    .h_full()
                    .items_center()
                    .child(
                        text(title.to_owned())
                            .font(type_::LABEL)
                            .color(t.text_secondary)
                            .single_line()
                            .truncate()
                            .grow(1.0),
                    )
                    .child(actions),
            );
    let headline = match (settle, pane) {
        (Some(settle), Some(pane)) => headline.child(settling_chip(
            &leaf.id,
            settle,
            &pane.session_id,
            cx.keys.keep.as_deref(),
        )),
        _ => headline,
    };
    let pops_out = matches!(leaf.kind, LeafKind::Tile { .. });
    let headline = headline
        .children(pops_out.then(|| pop_out_button(&cx.desktop.id, &leaf.id, popped, cx)))
        .children(focused.then(|| return_to_row(&key, &cx.desktop.id, cx)));
    let height = if pane.is_some() {
        AGENT_HEADER_PX
    } else {
        HEADER_PX
    };
    let track = match (settle, pane) {
        (Some(settle), Some(pane)) => {
            settling_track(&leaf.id, settle, cx.drains.get(&pane.session_id), height)
        }
        _ => None,
    };
    col()
        .key(format!("{key}.header"))
        .w_full()
        .h(height)
        .hover_group()
        .child(headline)
        .children(pane.map(|pane| subline(&key, &pane.session_id, t)))
        .children(track)
}

/// The agent header's second line: the session's short id, which copies the whole id.
fn subline(key: &str, session: &str, t: &Tokens) -> El {
    let short: String = session.chars().take(8).collect();
    let id = session.to_owned();
    row()
        .key(format!("{key}.subline"))
        .w_full()
        .h(SUBLINE_PX)
        .items_center()
        .gap(10.0)
        .pl(24.0)
        .pr(10.0)
        .child(
            text(short.clone())
                .key(format!("{key}.id"))
                .role("button")
                .label(format!("Copy session id {short}"))
                .font(SUBLINE_TYPE)
                .color(t.text_tertiary)
                .on_press_tx(move |_: &mut State, tx: &mut Tx| tx.copy(id.clone())),
        )
}

/// The header's pencil, which renames the agent's session.
fn rename_button(key: &str, session: &Session, title: &str, t: &Tokens) -> El {
    let (id, under) = (session.id.clone(), format!("{key}.rename"));
    header_button(
        under.clone(),
        format!("Rename session {title}"),
        attn_ui::icon::PENCIL,
        t,
    )
    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
        crate::sidebar::session_menu::rename_session(state, &id, under.clone(), tx)
    })
}

/// The header's button that shows this tile alone on its desktop.
fn focus_button(key: &str, desktop: &str, leaf: &str, title: &str, cx: &TileCx) -> El {
    let (desktop, leaf) = (desktop.to_owned(), leaf.to_owned());
    header_button(
        format!("{key}.focus"),
        with_key(&format!("Focus agent {title}"), cx.keys.focus.as_ref()),
        attn_ui::icon::EXPAND,
        cx.t,
    )
    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
        super::actions::focus_leaf(state, &desktop, &leaf, tx)
    })
}

/// The header's button that puts the tile shown alone back in its row.
fn return_to_row(key: &str, desktop: &str, cx: &TileCx) -> El {
    let desktop = desktop.to_owned();
    header_button(
        format!("{key}.return"),
        with_key("Return to row", cx.keys.focus.as_ref()),
        attn_ui::icon::COMPRESS,
        cx.t,
    )
    .on_press(move |state: &mut State| super::actions::return_to_row(state, &desktop))
}

fn header_button(key: String, label: String, glyph: &'static str, t: &Tokens) -> El {
    text(glyph)
        .key(key)
        .role("button")
        .label(label)
        .font(type_::LABEL)
        .color(t.text_secondary)
        .hover(t.bg_elevated)
        .radius(4.0)
        .px(space::S1)
}

/// The header's button that pops the tile out into its own window, or brings it back.
fn pop_out_button(desktop: &str, leaf: &str, popped: bool, cx: &TileCx) -> El {
    let (label, glyph) = if popped {
        ("Bring back tile", attn_ui::icon::BRING_BACK)
    } else {
        ("Pop out tile", attn_ui::icon::POP_OUT)
    };
    let (desktop, leaf_id) = (desktop.to_owned(), leaf.to_owned());
    text(glyph)
        .key(format!("tile.{leaf}.popout"))
        .role("button")
        .label(with_key(label, cx.keys.pop_out.as_ref()))
        .font(type_::LABEL)
        .color(cx.t.text_secondary)
        .px(space::S1)
        .on_press(move |state: &mut State| super::windows::toggle_leaf(state, &desktop, &leaf_id))
}

/// The settling violet, which the Tauri app keeps out of its theme so packaged apps match.
fn settling(alpha: f32) -> rune::Color {
    rune::color::hex("#a78bfa")
        .expect("the settling accent is #rrggbb")
        .alpha(alpha)
}

/// The header's mark of a turn that settles on its own, or that the person kept; pressing it
/// keeps the turn, or undoes keeping it.
fn settling_chip(leaf: &str, settle: &Settle, session: &str, keep_key: Option<&str>) -> El {
    let (label, words, verb, fill, edge, ink) = match settle {
        Settle::Counting(_) => ("Keep this turn", "SETTLING…", "keep", 0.16, 0.55, "#e2d9fd"),
        Settle::Held => (
            "Keep this turn",
            "SETTLING PAUSED",
            "keep",
            0.10,
            0.35,
            "#c8bcf0",
        ),
        Settle::Kept => (
            "Turn kept; undo to let it auto-settle again",
            "TURN KEPT",
            "undo",
            0.09,
            0.40,
            "#c8bcf0",
        ),
    };
    let ink = rune::color::hex(ink).expect("a settling ink is #rrggbb");
    let mark = el().w(6.0).h(6.0).fill(settling(1.0));
    let mark = match settle {
        Settle::Counting(_) => mark.radius(3.0),
        Settle::Held => mark.radius(3.0).opacity(0.55),
        Settle::Kept => mark.radius(1.0).opacity(0.7),
    };
    let font = rune::TextStyle {
        size: 10.0,
        weight: 600.0,
        ..type_::LABEL
    };
    let session = session.to_owned();
    row()
        .key(format!("tile.{leaf}.settling"))
        .role("button")
        .label(label)
        .items_center()
        .gap(7.0)
        .px(9.0)
        .py(2.0)
        .radius(5.0)
        .fill(settling(fill))
        .border(1.0, settling(edge))
        .on_press(move |state: &mut State| {
            let _ = state.daemon.send(&attn_protocol::CancelCountdownMessage {
                session_id: session.clone(),
                ..attn_protocol::CancelCountdownMessage::new()
            });
        })
        .child(mark)
        .child(text(words).font(font).color(ink).single_line())
        .children(keep_key.map(|key| {
            row()
                .items_center()
                .gap(4.0)
                .opacity(0.8)
                .child(
                    row().px(4.0).py(2.0).radius(3.0).border(1.0, ink).child(
                        text(key.to_owned())
                            .font(rune::TextStyle {
                                weight: 700.0,
                                ..font
                            })
                            .color(ink),
                    ),
                )
                .child(text(verb).font(font).color(ink))
        }))
}

/// The hairline along the header's foot that drains to the countdown's deadline, or stands
/// dimmed while the countdown is held.
fn settling_track(
    leaf: &str,
    settle: &Settle,
    drain: Option<&crate::sidebar::Countdown>,
    header: f32,
) -> Option<El> {
    if *settle == Settle::Kept {
        return None;
    }
    let line = rune::Shape::new().view_box(1.0, 1.0).line(
        [0.0, 0.5],
        [1.0, 0.5],
        1.0,
        rune::LineCap::Butt,
        settling(1.0),
    );
    let fill = rune::canvas(line)
        .key(format!("tile.{leaf}.settling.track"))
        .w_full()
        .h(SETTLING_TRACK_PX);
    let fill = match (settle, drain) {
        (Settle::Counting(_), Some(c)) => fill.live_trim(&c.drain),
        _ => fill.opacity(0.45),
    };
    Some(
        el().absolute(0.0, header - SETTLING_TRACK_PX)
            .w_full()
            .h(SETTLING_TRACK_PX)
            .fill(settling(0.16))
            .child(fill),
    )
}

/// The input method's caret in the terminal holding the keyboard: the cell text goes to
/// next, where the candidate window opens, showing what the input method composes so far.
fn ime_caret(terminals: &Terminals, runtime_id: &str, dark: bool) -> Option<El> {
    let caret = el()
        .key(format!("terminal.{runtime_id}.caret"))
        .absolute(0.0, 0.0)
        .live_rect(terminals.caret(runtime_id)?)
        .ime_cursor();
    let (Some(preedit), Some((size, h))) = (
        terminals.preedit(runtime_id),
        terminals.cell_font(runtime_id),
    ) else {
        return Some(caret);
    };
    let theme = attn_term::Theme::of(dark);
    let color =
        |rgb| rune::color::hex(&attn_term::Theme::hex(rgb)).expect("a theme color is #rrggbb");
    let font = rune::TextStyle {
        size,
        line_height: h,
        family: rune::Family::Mono,
        ..type_::BODY
    };
    Some(
        caret.child(
            text(preedit)
                .key(format!("terminal.{runtime_id}.preedit"))
                .absolute(0.0, 0.0)
                .fill(color(theme.bg))
                .font(font)
                .color(color(theme.fg))
                .single_line()
                .decorations(vec![rune::Span {
                    start: 0,
                    end: preedit.len() as u32,
                    underline: Some(color(theme.fg)),
                    ..rune::Span::default()
                }]),
        ),
    )
}

/// Where a terminal draws: the renderer finds it by its mark, and its laid-out size is the
/// size the terminal asks its PTY for. While it holds the keyboard it holds the input method's
/// `caret`.
fn terminal_slot(
    runtime_id: &str,
    tile: Holder,
    caret: Option<El>,
    pointing_link: bool,
    (dark, scale): (bool, f32),
) -> El {
    let id = runtime_id.to_owned();
    let (pointing, pressing, wheeling, leaving) = (id.clone(), id.clone(), id.clone(), id.clone());
    let bg = attn_term::Theme::hex(attn_term::Theme::of(dark).bg);
    let slot = el()
        .key(format!("terminal.{runtime_id}"))
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .fill(rune::color::hex(&bg).expect("a theme color is #rrggbb"))
        .effect(crate::terminal::mark(runtime_id))
        .on_layout_tx(move |state: &mut State, [_, _, w, h], tx: &mut Tx| {
            state
                .terminals
                .laid_out(&id, (w, h), scale, tx, &state.daemon);
            tx.live_only();
        })
        .on_pointer_move_local(move |state: &mut State, p, tx: &mut Tx| {
            let mods = (report_mods(tx.mods()), accelerator(tx.mods()));
            let now = tx.clock();
            let held = tx.pointer_held();
            let pointing_changed = state.terminals.pointer_moved(
                &pointing,
                (p.x, p.y),
                held,
                mods,
                now,
                &state.daemon,
            );
            if !pointing_changed {
                tx.live_only();
            }
        })
        .on_hover_change(move |state: &mut State, inside, tx: &mut Tx| {
            if inside || !state.terminals.pointer_left(&leaving) {
                tx.live_only();
            }
        })
        .cursor(if pointing_link {
            rune::Cursor::Pointer
        } else {
            rune::Cursor::Text
        })
        .pointer_buttons([PointerButton::Middle, PointerButton::Secondary])
        .on_drag(move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
            let mods = report_mods(tx.mods());
            match d.phase {
                rune::Phase::Down => {
                    press_tile(state, &tile, tx);
                    let button = match tx.button() {
                        PointerButton::Middle => mouse::MIDDLE,
                        PointerButton::Secondary => mouse::SECONDARY,
                        _ => mouse::PRIMARY,
                    };
                    let opens = button == mouse::PRIMARY
                        && accelerator(tx.mods())
                        && state.terminals.presses_link(&pressing, (d.x, d.y));
                    let reported = !opens
                        && state.terminals.pointer_pressed(
                            &pressing,
                            button,
                            (d.x, d.y),
                            mods,
                            &state.daemon,
                        );
                    if !reported
                        && button == mouse::PRIMARY
                        && let Some(text) =
                            state
                                .terminals
                                .select_press(&pressing, (d.x, d.y), d.clicks)
                    {
                        tx.copy(text);
                    }
                }
                rune::Phase::Move => {
                    state.terminals.select_drag(&pressing, (d.x, d.y));
                    tx.live_only();
                }
                rune::Phase::Up => {
                    let at = (!d.cancelled()).then_some((d.x, d.y));
                    let mods = if d.cancelled() { 0 } else { mods };
                    state
                        .terminals
                        .pointer_released(&pressing, at, mods, &state.daemon);
                    match state.terminals.select_release(&pressing) {
                        Release::Copied(text) => tx.copy(text),
                        Release::Click if !d.cancelled() && accelerator(tx.mods()) => {
                            state.terminals.open_link_at(&pressing, (d.x, d.y), tx);
                        }
                        Release::Click if !d.cancelled() => {
                            state.terminals.click(&pressing, (d.x, d.y));
                        }
                        _ => {}
                    }
                    if !d.cancelled() && tx.button() == PointerButton::Secondary {
                        super::terminal_menu::open(
                            state,
                            &pressing,
                            (d.x, d.y),
                            d.window_point(),
                            tx,
                        );
                    }
                    tx.live_only();
                }
            }
        })
        .on_wheel(move |state: &mut State, e, tx: &mut Tx| {
            tx.live_only();
            state
                .terminals
                .wheel(&wheeling, e.dy, (e.x, e.y), &state.daemon)
        });
    slot.children(caret)
}

/// The window's keyboard goes to `tile`, which becomes the desktop's active tile.
/// A press on a tile closes the peek and takes the tile into `tile`.
fn press_tile(state: &mut State, tile: &Holder, tx: &mut Tx) {
    super::peek::dismiss(state);
    take_tile(state, tile, tx);
}

pub(crate) fn take_tile(state: &mut State, tile: &Holder, tx: &mut Tx) {
    state.window.take_keyboard(&tile.desktop, &tile.pane, tx);
    let keyboard = state.window.keyboard_terminal(&state.facts);
    state.terminals.focus(keyboard, &state.daemon);
    super::actions::activate(state, &tile.desktop, &tile.pane);
}

fn report_mods(mods: Mods) -> u8 {
    mouse::modifiers(mods.shift, mods.alt, mods.ctrl)
}
