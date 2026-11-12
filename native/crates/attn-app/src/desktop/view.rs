use attn_protocol::{Desktop, DesktopPane};
use attn_term::mouse;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{Drag, El, Mods, PointerButton, Tx, col, el, memo, row, text};

use super::Area;
use super::layout::{Direction, Leaf, LeafKind, Node};
use crate::State;
use crate::shell::Holder;
use crate::terminal::Terminals;

const GAP: f32 = 6.0;

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
        row_state,
        area_bits,
        dragging.clone(),
        *t,
        scale.to_bits(),
    );
    let launcher = desktop
        .filter(|d| d.panes.is_empty())
        .and_then(|_| crate::picker::inline(state, t));
    let content = launcher.unwrap_or_else(|| {
        memo("tiles.area", reads, || {
            col().w_full().h_full().min_w(0.0).min_h(0.0).child(screen(
                desktop,
                desk,
                facts,
                holder.as_ref(),
                &state.terminals,
                t,
                scale,
            ))
        })
    });
    col()
        .key("tiles")
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
        })
        .child(content)
}

fn screen(
    desktop: Option<&Desktop>,
    desk: &super::Desk,
    facts: &attn_client::Facts,
    holder: Option<&Holder>,
    terminals: &Terminals,
    t: &Tokens,
    scale: f32,
) -> El {
    match desktop {
        None => empty(t, "No desktop"),
        Some(desktop) => match desk.plan(facts, desktop) {
            None => empty(t, "Empty desktop"),
            Some((tree, plan)) => {
                let view = desk.row(&desktop.id).view;
                let rendered = plan.rendered(&tree, &view, &desktop.active_pane_id);
                let dragging = desk.drag.as_ref().and_then(|d| d.dragging());
                let cx = TileCx {
                    desktop,
                    facts,
                    tree: &tree,
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
    facts: &'a attn_client::Facts,
    tree: &'a Node,
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
            parent
                .key(format!("split.{}", split.id))
                .w_full()
                .h_full()
                .min_w(0.0)
                .min_h(0.0)
                .gap(GAP)
                .child(share(node(a, cx), split.ratio))
                .child(share(node(b, cx), 1.0 - split.ratio))
        }
    }
}

fn share(child: El, ratio: f32) -> El {
    col()
        .grow(ratio.max(0.001))
        .min_w(0.0)
        .min_h(0.0)
        .child(child)
}

/// The name a leaf goes by in its header and to assistive tech.
pub(crate) fn leaf_title(desktop: &Desktop, leaf: &Leaf) -> String {
    match &leaf.kind {
        LeafKind::Pane => pane_of(desktop, &leaf.id)
            .map(|p| p.title.clone())
            .filter(|t| !t.is_empty())
            .unwrap_or_else(|| "Terminal".into()),
        LeafKind::Tile { kind, .. } => tile_kind_label(kind).into(),
    }
}

fn tile_kind_label(kind: &str) -> &str {
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
    let title = leaf_title(desktop, leaf);
    let holder = Holder {
        desktop: desktop.id.clone(),
        pane: leaf.id.clone(),
    };
    let body = match (&leaf.kind, pane_of(desktop, &leaf.id)) {
        (LeafKind::Pane, Some(pane)) if !pane.runtime_id.is_empty() => {
            let caret = held
                .then(|| ime_caret(cx.terminals, &pane.runtime_id, t.dark))
                .flatten();
            terminal_slot(&pane.runtime_id, holder.clone(), caret, t.dark, cx.scale)
        }
        (LeafKind::Pane, _) => empty(t, "Starting…"),
        (LeafKind::Tile { kind, .. }, _) => empty(t, tile_kind_label(kind)),
    };
    col()
        .key(format!("tile.{}", leaf.id))
        .w_full()
        .h_full()
        .min_w(0.0)
        .min_h(0.0)
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
        .on_press_tx({
            let holder = holder.clone();
            move |state: &mut State, tx: &mut Tx| take_tile(state, &holder, tx)
        })
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

/// A tile's header, which drags the tile.
fn header(leaf: &Leaf, title: &str, cx: &TileCx) -> El {
    let t = cx.t;
    let source = super::drag::Source::Header {
        desktop: cx.desktop.id.clone(),
        leaf: leaf.id.clone(),
        label: super::shelf::title(cx.desktop, cx.facts, cx.tree, &leaf.id),
        movable: matches!(leaf.kind, LeafKind::Tile { .. }) || cx.tree.leaves().len() > 1,
    };
    let header = row().key(format!("tile.{}.header", leaf.id)).on_drag(
        move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
            super::drag::gesture(state, d, tx, &source)
        },
    );
    header
        .w_full()
        .items_center()
        .px(space::S2)
        .py(space::S1)
        .child(
            text(title.to_owned())
                .font(type_::LABEL)
                .color(t.text_secondary)
                .single_line()
                .grow(1.0),
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
fn terminal_slot(runtime_id: &str, tile: Holder, caret: Option<El>, dark: bool, scale: f32) -> El {
    let id = runtime_id.to_owned();
    let (pointing, pressing, wheeling) = (id.clone(), id.clone(), id.clone());
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
            let mods = report_mods(tx.mods());
            let now = tx.clock();
            let held = tx.pointer_held();
            state
                .terminals
                .pointer_moved(&pointing, (p.x, p.y), held, mods, now, &state.daemon);
            tx.live_only();
        })
        .pointer_buttons([PointerButton::Middle, PointerButton::Secondary])
        .on_drag(move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
            let mods = report_mods(tx.mods());
            match d.phase {
                rune::Phase::Down => {
                    take_tile(state, &tile, tx);
                    let button = match tx.button() {
                        PointerButton::Middle => mouse::MIDDLE,
                        PointerButton::Secondary => mouse::SECONDARY,
                        _ => mouse::PRIMARY,
                    };
                    state.terminals.pointer_pressed(
                        &pressing,
                        button,
                        (d.x, d.y),
                        mods,
                        &state.daemon,
                    );
                }
                rune::Phase::Move => tx.live_only(),
                rune::Phase::Up if d.cancelled() => {
                    state
                        .terminals
                        .pointer_released(&pressing, None, 0, &state.daemon);
                    tx.live_only();
                }
                rune::Phase::Up => {
                    state.terminals.pointer_released(
                        &pressing,
                        Some((d.x, d.y)),
                        mods,
                        &state.daemon,
                    );
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
pub(crate) fn take_tile(state: &mut State, tile: &Holder, tx: &mut Tx) {
    state.window.take_keyboard(&tile.desktop, &tile.pane, tx);
    let keyboard = state.window.keyboard_terminal(&state.facts);
    state.terminals.focus(keyboard, &state.daemon);
    super::actions::activate(state, &tile.desktop, &tile.pane);
}

fn report_mods(mods: Mods) -> u8 {
    mouse::modifiers(mods.shift, mods.alt, mods.ctrl)
}
