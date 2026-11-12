use attn_protocol::{Desktop, DesktopPane};
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
        ratio_bits(&desk.ratios),
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
        Some(_) if desk.area.is_none() => col(),
        Some(desktop) => match desk.plan(facts, desktop) {
            None => empty(t, "Empty desktop"),
            Some((tree, plan)) => {
                let view = desk.row(&desktop.id).view;
                let rendered = plan.rendered(&tree, &view, &desktop.active_pane_id);
                let dragging = desk.drag.as_ref().and_then(|d| d.dragging());
                let cx = TileCx {
                    desktop,
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
            let pointing_link = cx.terminals.pointing_link() == Some(pane.runtime_id.as_str());
            terminal_slot(
                &pane.runtime_id,
                holder.clone(),
                caret,
                pointing_link,
                (t.dark, cx.scale),
            )
        }
        (LeafKind::Pane, _) => takes(empty(t, "Starting…"), &holder),
        (LeafKind::Tile { kind, .. }, _) => takes(empty(t, tile_kind_label(kind)), &holder),
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
    el.on_press_tx(move |state: &mut State, tx: &mut Tx| take_tile(state, &holder, tx))
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

/// A tile's header, which the tile area drags the tile by.
fn header(leaf: &Leaf, title: &str, cx: &TileCx) -> El {
    let t = cx.t;
    row()
        .key(format!("tile.{}.header", leaf.id))
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
                    take_tile(state, &tile, tx);
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
                        _ => {}
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
pub(crate) fn take_tile(state: &mut State, tile: &Holder, tx: &mut Tx) {
    state.window.take_keyboard(&tile.desktop, &tile.pane, tx);
    let keyboard = state.window.keyboard_terminal(&state.facts);
    state.terminals.focus(keyboard, &state.daemon);
    super::actions::activate(state, &tile.desktop, &tile.pane);
}

fn report_mods(mods: Mods) -> u8 {
    mouse::modifiers(mods.shift, mods.alt, mods.ctrl)
}
