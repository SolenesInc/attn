//! The shelf: the columns of the current desktop's row that the screen does not show, as
//! chips in the title bar either side of a dot per column on screen.

use attn_protocol::{
    Desktop, DesktopMoveLeafMessage, DesktopSetWantedColsMessage, SetSettingMessage,
};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};

use super::layout::{
    self, Column, Edge, LeafKind, NarrowestStep, Node, RowInput, ShelfDrop, ShelfSend, View,
};
use super::{Desk, actions};
use crate::State;
use crate::action::Action;

const CHIP_PX: f32 = 112.0;
const LIVE_DOT_PX: f32 = 28.0;
const CAPS_PX: f32 = 104.0;
/// How long the width label shows after fewer or more tiles.
const WIDTH_LABEL_SECONDS: f64 = 1.5;

/// The width label fewer and more tiles show for a moment.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct WidthLabel {
    pub cleared: usize,
    pub until: f64,
}

/// One column of the row as its chip or dot shows it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Chip {
    /// The column's first leaf, which names the chip.
    key: String,
    /// The leaf the chip brings: the one this window used last.
    go: String,
    label: String,
    needs: bool,
}

fn needs_you(desktop: &Desktop, facts: &attn_client::Facts, leaf: &str) -> bool {
    let Some(pane) = super::view::pane_of(desktop, leaf) else {
        return false;
    };
    facts
        .sessions
        .iter()
        .find(|s| s.id == pane.session_id)
        .is_some_and(|s| matches!(s.state.as_str(), "waiting_input" | "pending_approval"))
}

/// A tile's name on the shelf: an agent by its session's name, a tile by what it holds.
fn title(desktop: &Desktop, facts: &attn_client::Facts, tree: &Node, leaf: &str) -> String {
    match tree.find_leaf(leaf) {
        Some(Node::Leaf(l)) => match &l.kind {
            LeafKind::Tile { kind, params, .. } => {
                crate::sidebar::tile_title(kind, params.as_deref().unwrap_or(""))
            }
            LeafKind::Pane => {
                let pane = super::view::pane_of(desktop, leaf);
                let session =
                    pane.and_then(|p| facts.sessions.iter().find(|s| s.id == p.session_id));
                session
                    .map(crate::sidebar::display_name)
                    .or_else(|| pane.map(|p| p.title.clone()).filter(|t| !t.is_empty()))
                    .unwrap_or_else(|| "Terminal".into())
            }
        },
        _ => "Terminal".into(),
    }
}

fn label(titles: &[String]) -> String {
    match titles {
        [one] => one.clone(),
        [a, b] => format!("{a} + {b}"),
        [first, rest @ ..] => format!("{first} + {}", rest.len()),
        [] => String::new(),
    }
}

fn chip(
    desktop: &Desktop,
    facts: &attn_client::Facts,
    tree: &Node,
    focus: &[String],
    column: &Column,
) -> Chip {
    let titles: Vec<String> = column
        .leaf_ids
        .iter()
        .map(|leaf| title(desktop, facts, tree, leaf))
        .collect();
    Chip {
        key: column.first().to_owned(),
        go: column.last_used(focus).to_owned(),
        label: label(&titles),
        needs: column
            .leaf_ids
            .iter()
            .any(|leaf| needs_you(desktop, facts, leaf)),
    }
}

/// How many chips show either side when the shelf is `available` points wide; the rest go
/// into the caps, the chips nearest the screen kept.
fn allot(available: f32, before: usize, after: usize, dots: usize) -> (usize, usize) {
    let without_caps = ((available - dots as f32 * LIVE_DOT_PX) / CHIP_PX).floor();
    if (before + after) as f32 <= without_caps {
        return (before, after);
    }
    let room = ((available - dots as f32 * LIVE_DOT_PX - CAPS_PX) / CHIP_PX)
        .floor()
        .max(0.0) as usize;
    let (mut left, mut right) = (0, 0);
    while left + right < room && (left < before || right < after) {
        if right < after && (right <= left || left >= before) {
            right += 1;
        } else {
            left += 1;
        }
    }
    (left, right)
}

/// The shelf of the current desktop, or None when nothing is off screen, the window shows
/// Home, or the active tile is in focus mode.
pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    if state.window.view != crate::shell::View::Desktop {
        return None;
    }
    let facts = &state.facts;
    let desk = &state.window.desk;
    let desktop = facts.current_desktop()?;
    let row_state = desk.row(&desktop.id);
    if matches!(row_state.view, View::Focused(_)) {
        return None;
    }
    let (tree, plan) = desk.plan(facts, desktop)?;
    if plan.before.is_empty() && plan.after.is_empty() {
        return None;
    }
    let focus = &row_state.focus_order;
    let chips = |columns: &[Column]| -> Vec<Chip> {
        columns
            .iter()
            .map(|c| chip(desktop, facts, &tree, focus, c))
            .collect()
    };
    let (before, screen, after) = (chips(&plan.before), chips(&plan.screen), chips(&plan.after));
    let available = desk.shelf_px.unwrap_or(f32::INFINITY);
    let (left, right) = allot(available, before.len(), after.len(), screen.len());
    let split = before.len() - left;
    let id = desktop.id.clone();
    let active = desktop.active_pane_id.clone();
    let dots = row()
        .key("shelf.live")
        .items_center()
        .gap(space::S1)
        .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
            state.window.desk.width_control = inside
        })
        .children(plan.screen.iter().zip(&screen).map(|(column, chip)| {
            let current = column.holds(&active);
            dot(chip, current, &id, t)
        }))
        .children(
            desk.width_control
                .then(|| width_control(plan.screen.len(), Desk::narrowest_cols(facts), t)),
        );
    let menu = |side: Edge, columns: &[Chip]| cap(side, columns, desk.cap_open == Some(side), t);
    Some(
        row()
            .key("shelf")
            .role("toolbar")
            .label("Shelf")
            .items_center()
            .gap(space::S1)
            .children(menu(Edge::Left, &before[..split]))
            .children(before[split..].iter().map(|c| chip_button(c, &id, t)))
            .child(dots)
            .children(after[..right].iter().map(|c| chip_button(c, &id, t)))
            .children(menu(Edge::Right, &after[right..])),
    )
}

/// The title bar's middle, which the shelf sits in and measures for its chips.
pub(crate) fn slot(state: &State, t: &Tokens) -> El {
    row()
        .key("shelf.slot")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .center()
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            if state.window.desk.shelf_px == Some(w) {
                tx.live_only();
                return;
            }
            state.window.desk.shelf_px = Some(w);
        })
        .children(view(state, t))
}

fn chip_button(chip: &Chip, desktop: &str, t: &Tokens) -> El {
    let (desktop, leaf) = (desktop.to_owned(), chip.go.clone());
    row()
        .key(format!("shelf.chip.{}", chip.key))
        .role("button")
        .label(chip.label.clone())
        .a11y_value(if chip.needs { "needs you" } else { "" })
        .w(CHIP_PX - space::S1)
        .min_w(0.0)
        .px(space::S2)
        .py(space::S1)
        .radius(4.0)
        .fill(t.bg_tile)
        .border(1.0, if chip.needs { t.accent } else { t.border })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            state.window.desk.cap_open = None;
            bring(state, &desktop, &leaf, tx);
        })
        .child(
            text(chip.label.clone())
                .font(type_::LABEL)
                .color(t.text_secondary)
                .single_line(),
        )
}

fn dot(chip: &Chip, current: bool, desktop: &str, t: &Tokens) -> El {
    let (desktop, leaf) = (desktop.to_owned(), chip.go.clone());
    row()
        .key(format!("shelf.dot.{}", chip.key))
        .role("button")
        .label(chip.label.clone())
        .a11y_value(if current { "current" } else { "" })
        .w(LIVE_DOT_PX - space::S1)
        .h(LIVE_DOT_PX - space::S1)
        .center()
        .on_press(move |state: &mut State| actions::show_leaf(state, &desktop, &leaf))
        .child(rune::el().w(8.0).h(8.0).radius(4.0).fill(if current {
            t.accent
        } else {
            t.text_tertiary
        }))
}

fn cap(side: Edge, chips: &[Chip], open: bool, t: &Tokens) -> Option<El> {
    if chips.is_empty() {
        return None;
    }
    let n = chips.len();
    let (name, caption) = match side {
        Edge::Left => ("before", format!("‹ {n}")),
        Edge::Right => ("after", format!("{n} ›")),
    };
    Some(
        row()
            .key(format!("shelf.cap.{name}"))
            .role("button")
            .label(format!("{n} more this way"))
            .px(space::S2)
            .py(space::S1)
            .radius(4.0)
            .border(
                1.0,
                if chips.iter().any(|c| c.needs) {
                    t.accent
                } else {
                    t.border
                },
            )
            .on_press(move |state: &mut State| {
                let desk = &mut state.window.desk;
                desk.cap_open = (desk.cap_open != Some(side)).then_some(side);
            })
            .a11y_value(if open { "open" } else { "" })
            .child(text(caption).font(type_::LABEL).color(t.text_secondary)),
    )
}

/// The open cap's menu of the chips it holds, under the title bar on its side.
pub(crate) fn cap_menu(state: &State, t: &Tokens) -> Option<El> {
    let desk = &state.window.desk;
    let side = desk.cap_open?;
    let facts = &state.facts;
    let desktop = facts.current_desktop()?;
    let (tree, plan) = desk.plan(facts, desktop)?;
    let focus = &desk.row(&desktop.id).focus_order;
    let chips = |columns: &[Column]| -> Vec<Chip> {
        columns
            .iter()
            .map(|c| chip(desktop, facts, &tree, focus, c))
            .collect()
    };
    let (before, after) = (chips(&plan.before), chips(&plan.after));
    let (left, right) = allot(
        desk.shelf_px.unwrap_or(f32::INFINITY),
        before.len(),
        after.len(),
        plan.screen.len(),
    );
    let (name, held) = match side {
        Edge::Left => ("before", &before[..before.len() - left]),
        Edge::Right => ("after", &after[right..]),
    };
    if held.is_empty() {
        return None;
    }
    let menu = col()
        .key(format!("shelf.menu.{name}"))
        .role("menu")
        .label(format!("{} more this way", held.len()))
        .padding(space::S1)
        .gap(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .children(held.iter().map(|c| chip_button(c, &desktop.id, t)));
    let place = row().w_full().padding(space::S2);
    let place = match side {
        Edge::Left => place,
        Edge::Right => place.justify_end(),
    };
    Some(
        rune::el()
            .key("shelf.menu.layer")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .on_press(|state: &mut State| state.window.desk.cap_open = None)
            .child(
                place
                    .absolute(0.0, crate::shell::TITLE_BAR_HEIGHT)
                    .child(menu),
            ),
    )
}

fn width_control(shown: usize, cols: u32, t: &Tokens) -> El {
    let button = |key: &str, label: &str, glyph: &str, action: Action| {
        row()
            .key(key.to_owned())
            .role("button")
            .label(label.to_owned())
            .px(space::S1)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                crate::dispatch::dispatch(state, action.clone(), tx)
            })
            .child(
                text(glyph.to_owned())
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
    };
    row()
        .key("shelf.width")
        .items_center()
        .gap(space::S1)
        .child(
            text(format!("{shown} · ≥{cols} cols"))
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .single_line(),
        )
        .child(button(
            "shelf.width.fewer",
            "Fewer, wider tiles",
            "−",
            Action::ShelfFewerTiles,
        ))
        .child(button(
            "shelf.width.more",
            "More, narrower tiles",
            "+",
            Action::ShelfMoreTiles,
        ))
}

/// The label fewer and more tiles show: how many tiles fit, how wide each is at least, and
/// the counts the steps reach.
pub(crate) fn width_label(state: &State, t: &Tokens) -> Option<El> {
    let desk = &state.window.desk;
    let label = desk.width_label.as_ref()?;
    let facts = &state.facts;
    let desktop = facts.current_desktop()?;
    let (tree, plan) = desk.plan(facts, desktop)?;
    let row_state = desk.row(&desktop.id);
    let cols = Desk::narrowest_cols(facts);
    let input = desk.row_input(facts, &tree, desktop, &row_state)?;
    let shown = plan.screen.len();
    let counts = layout::reachable_tile_counts(&input, cols);
    let cleared = match label.cleared {
        0 => None,
        1 => Some("Cleared the dragged width".to_owned()),
        n => Some(format!("Cleared {n} dragged widths")),
    };
    let steps = counts
        .iter()
        .map(|n| {
            if *n == shown {
                format!("[{n}]")
            } else {
                n.to_string()
            }
        })
        .collect::<Vec<_>>()
        .join(" ");
    let card = col()
        .key("shelf.width.label")
        .role("status")
        .label(format!("{shown} tile{}", if shown == 1 { "" } else { "s" }))
        .padding(space::S2)
        .gap(space::S1)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .child(
            text(format!("{shown} tile{}", if shown == 1 { "" } else { "s" }))
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .child(
            text(format!("at least {cols} cols each"))
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .children(cleared.map(|c| text(c).font(type_::LABEL).color(t.text_secondary)))
        .child(text(steps).font(type_::LABEL).color(t.text_tertiary));
    Some(
        rune::el()
            .key("shelf.width.layer")
            .absolute(0.0, crate::shell::TITLE_BAR_HEIGHT)
            .w_full()
            .child(
                row()
                    .w_full()
                    .justify_center()
                    .padding(space::S2)
                    .child(card),
            ),
    )
}

fn request_id() -> String {
    uuid::Uuid::new_v4().to_string()
}

fn move_leaf(
    state: &State,
    desktop: &str,
    leaf: &str,
    anchor: &str,
    edge: Edge,
    whole_column: bool,
    activate: bool,
) {
    let _ = state.daemon.send(&DesktopMoveLeafMessage {
        request_id: request_id(),
        source_desktop_id: desktop.to_owned(),
        target_desktop_id: Some(desktop.to_owned()),
        leaf_id: leaf.to_owned(),
        anchor_id: Some(anchor.to_owned()),
        edge: Some(edge.as_str().to_owned()),
        whole_column: Some(whole_column),
        activate: Some(activate),
        ..DesktopMoveLeafMessage::new()
    });
}

fn clear_widths(state: &mut State, desktop: &str, leaves: &[String]) {
    for leaf in leaves {
        let _ = state.daemon.send(&DesktopSetWantedColsMessage {
            request_id: request_id(),
            desktop_id: desktop.to_owned(),
            leaf_id: leaf.clone(),
            cols: 0,
            area_cols: 0,
            ..DesktopSetWantedColsMessage::new()
        });
    }
    let row = state.window.desk.row_mut(desktop);
    row.cleared.extend(leaves.iter().cloned());
}

/// Brings `leaf`'s column beside the active tile, or jumps to it where it is when it is on
/// screen already or the two cannot show side by side.
pub(crate) fn bring(state: &mut State, desktop_id: &str, leaf: &str, _tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.desktops.iter().find(|d| d.id == desktop_id) else {
        return;
    };
    let active = desktop.active_pane_id.clone();
    let jump = || actions::show_leaf(state, desktop_id, leaf);
    let Some((tree, plan)) = desk.plan(facts, desktop) else {
        return jump();
    };
    let Some(measures) = desk.measures(facts) else {
        return jump();
    };
    let on_screen = plan.screen.iter().any(|c| c.holds(leaf));
    let active_at = plan.screen.iter().position(|c| c.holds(&active));
    let row = layout::read_row(&tree);
    let (Some(active_at), Some(column), Some(active_column)) = (
        active_at,
        row.iter().find(|c| c.holds(leaf)),
        row.iter().find(|c| c.holds(&active)),
    ) else {
        return jump();
    };
    if on_screen
        || plan.screen.len() < 2
        || !layout::columns_fit_together(active_column, column, &measures)
    {
        return jump();
    }
    let sent_on = desktop.tree_json.clone();
    let active_then = active.clone();
    let edge = if active_at == plan.screen.len() - 1 {
        Edge::Left
    } else {
        Edge::Right
    };
    move_leaf(state, desktop_id, leaf, &active, edge, true, true);
    state.window.desk.row_mut(desktop_id).drop = Some(PendingDrop {
        drop: ShelfDrop {
            leaf: leaf.to_owned(),
            anchor: active,
            edge,
        },
        tree: sent_on,
        active: active_then,
    });
}

/// Carries the active tile's column one place along the row.
pub(crate) fn carry(state: &mut State, step: i32, tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.current_desktop() else {
        return;
    };
    let row_state = desk.row(&desktop.id);
    let active = desktop.active_pane_id.clone();
    if active.is_empty() || matches!(row_state.view, View::Focused(_)) {
        return;
    }
    let Some(tree) = Node::parse(&desktop.tree_json).and_then(|t| t.main()) else {
        return;
    };
    let row: Vec<Column> = layout::read_row(&tree)
        .into_iter()
        .filter(|c| c.holds(&active) || !c.leaf_ids.iter().any(|id| row_state.held.contains(id)))
        .collect();
    let Some(at) = layout::column_index(&row, &active) else {
        return;
    };
    let Some(neighbour) = at
        .checked_add_signed(step as isize)
        .and_then(|i| row.get(i))
    else {
        let end = if step < 0 { "start" } else { "end" };
        state
            .window
            .notify(format!("This tile is already at the {end} of the row."), tx);
        return;
    };
    let anchor = neighbour.first().to_owned();
    let desktop_id = desktop.id.clone();
    if let Some(first) = &row_state.first_column
        && step > 0
        && layout::column_index(&row, &first.leaf) == Some(at)
    {
        let index = first.index;
        state.window.desk.row_mut(&desktop_id).first_column = Some(layout::FirstColumn {
            leaf: anchor.clone(),
            index,
        });
    }
    let edge = if step < 0 { Edge::Left } else { Edge::Right };
    move_leaf(state, &desktop_id, &active, &anchor, edge, true, true);
}

/// Sends the active tile off screen to the shelf, showing the tile that takes its place.
pub(crate) fn send_to_shelf(state: &mut State, tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.current_desktop() else {
        return;
    };
    let row_state = desk.row(&desktop.id);
    if desktop.active_pane_id.is_empty() || matches!(row_state.view, View::Focused(_)) {
        return;
    }
    let Some((tree, plan)) = desk.plan(facts, desktop) else {
        return;
    };
    let Some(input) = desk.row_input(facts, &tree, desktop, &row_state) else {
        return;
    };
    let send = layout::plan_send_to_shelf(&input, &plan.screen, &row_state.focus_order);
    let id = desktop.id.clone();
    match send {
        ShelfSend::Alone => state
            .window
            .notify("This is the only tile on screen.".into(), tx),
        ShelfSend::Unstack { leaf, anchor, next } => {
            actions::show_leaf(state, &id, &next);
            move_leaf(state, &id, &leaf, &anchor, Edge::Right, false, false);
        }
        ShelfSend::Hold { leaf, next } => {
            actions::show_leaf(state, &id, &next);
            state.window.desk.row_mut(&id).held.push(leaf);
        }
        ShelfSend::Move {
            leaf,
            anchor,
            edge,
            next,
        } => {
            actions::show_leaf(state, &id, &next);
            move_leaf(state, &id, &leaf, &anchor, edge, true, false);
        }
    }
}

/// Fewer, wider tiles or more, narrower ones: the next narrowest width that changes how many
/// tiles fit, forgetting dragged widths when only they keep more off screen.
pub(crate) fn step_width(state: &mut State, fewer: bool, tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.current_desktop() else {
        return;
    };
    let row_state = desk.row(&desktop.id);
    let Some(tree) = Node::parse(&desktop.tree_json).and_then(|t| t.main()) else {
        return;
    };
    let Some(input) = desk.row_input(facts, &tree, desktop, &row_state) else {
        return;
    };
    let current = Desk::narrowest_cols(facts);
    let window_px = desk.window_w.unwrap_or(0.0);
    let (cols, clear) = match layout::step_narrowest_cols(&input, current, fewer, window_px) {
        NarrowestStep::Limit(message) => return state.window.notify(message, tx),
        NarrowestStep::Step(cols) => (cols, vec![]),
        NarrowestStep::ClearThenStep { cols, clear, .. } => (cols, clear),
    };
    let id = desktop.id.clone();
    clear_widths(state, &id, &clear);
    if cols != current {
        let _ = state.daemon.send(&SetSettingMessage {
            key: "narrowest_tile_cols".into(),
            value: cols.to_string(),
            ..SetSettingMessage::new()
        });
    }
    let until = tx.clock() + WIDTH_LABEL_SECONDS;
    state.window.desk.width_label = Some(WidthLabel {
        cleared: clear.len(),
        until,
    });
    tx.wake_at(until);
}

/// After the daemon's row changes: a brought column that landed sets where the screen starts
/// so its anchor keeps its place, forgetting dragged widths that keep the two apart, and
/// every other row keeps the start its screen settled on.
pub(crate) fn settle(state: &mut State) {
    let ids: Vec<String> = state.facts.desktops.iter().map(|d| d.id.clone()).collect();
    for id in ids {
        settle_desktop(state, &id);
    }
}

fn settle_desktop(state: &mut State, id: &str) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.desktops.iter().find(|d| d.id == *id) else {
        return;
    };
    let Some(tree) = Node::parse(&desktop.tree_json).and_then(|t| t.main()) else {
        return;
    };
    let row_state = desk.row(id);
    let Some(input) = desk.row_input(facts, &tree, desktop, &row_state) else {
        return;
    };
    let row = layout::read_row(&tree);
    let widened = |leaf: &String| {
        row.iter()
            .any(|c| c.first() == leaf && c.wanted_cols.is_some())
    };
    let cleared: Vec<String> = row_state
        .cleared
        .iter()
        .filter(|l| widened(l))
        .cloned()
        .collect();
    let plan = layout::resolve_row(&input);
    let settled = Settled {
        active: desktop.active_pane_id.clone(),
        order: tree.leaves().iter().map(|l| l.id.clone()).collect(),
    };
    let landed = row_state
        .drop
        .as_ref()
        .filter(|pending| dropped_beside(&row, &desktop.active_pane_id, &pending.drop));
    let reveal = landed
        .and_then(|pending| layout::reveal_drop(&input, &pending.drop, plan.first_column.index));
    let waiting = row_state.drop.clone().filter(|pending| {
        landed.is_none()
            && pending.tree == desktop.tree_json
            && pending.active == desktop.active_pane_id
    });
    let moved = row_state.settled.as_ref() != Some(&settled);
    let row_mut = state.window.desk.row_mut(id);
    row_mut.cleared = cleared;
    row_mut.drop = waiting;
    row_mut.settled = Some(settled);
    match reveal {
        Some((first, clear)) => {
            row_mut.first_column = Some(first);
            clear_widths(state, id, &clear);
        }
        None if moved || landed.is_some() => row_mut.first_column = Some(plan.first_column),
        None => {}
    }
}

/// What a row's screen start settles on: it settles again only when the active tile or the
/// order of the row's leaves changes, so a width or window change leaves it where it was.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Settled {
    active: String,
    order: Vec<String>,
}

/// A column brought beside the active tile, and the row and active tile as they were when the
/// window asked for it: once either changes without the column landing, the bring is over.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct PendingDrop {
    pub drop: ShelfDrop,
    pub tree: String,
    pub active: String,
}

fn dropped_beside(row: &[Column], active: &str, drop: &ShelfDrop) -> bool {
    let Some(at) = layout::column_index(row, &drop.anchor) else {
        return false;
    };
    let beside = match drop.edge {
        Edge::Left => at.checked_sub(1).and_then(|i| row.get(i)),
        Edge::Right => row.get(at + 1),
    };
    active == drop.leaf && beside.is_some_and(|c| c.holds(&drop.leaf))
}

impl Desk {
    /// The input the row's layout reads for `desktop`, once the tile area is laid out.
    pub(crate) fn row_input<'a>(
        &self,
        facts: &attn_client::Facts,
        tree: &'a Node,
        desktop: &'a Desktop,
        row: &'a super::RowState,
    ) -> Option<RowInput<'a>> {
        Some(RowInput {
            tree,
            active: &desktop.active_pane_id,
            first_column: row.first_column.as_ref(),
            held: &row.held,
            cleared: &row.cleared,
            measures: self.measures(facts)?,
        })
    }
}
