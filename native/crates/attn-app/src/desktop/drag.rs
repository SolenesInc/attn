//! Dragging a tile by its header, or a shelf column by its chip, onto a tile's edge, into a
//! tile's place, or beside a column on the shelf.

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{Drag, El, Phase, Tx, row, text};

use super::layout::{self, Edge, Node, ShelfDrop, TILE_MIN_HEIGHT, TILE_MIN_WIDTH};
use super::shelf::{PendingDrop, Shelf};
use super::{Desk, actions};
use crate::State;
use crate::shell::Holder;

/// How far the pointer travels from the press before a press becomes a drag.
const ACTIVATION: f32 = 4.0;
const BAND_MIN: f32 = 0.15;
const BAND_MAX: f32 = 0.5;
const SNAPS: [f32; 3] = [0.25, 1.0 / 3.0, 0.5];
const SNAP_TOLERANCE: f32 = 0.045;
/// How far from every edge a chip has to be over a tile to take its place.
const CENTRE: f32 = 0.25;

/// What a drag picks up.
#[derive(Clone, Debug)]
pub(crate) enum Source {
    /// A tile, by its header. An agent alone on its desktop has nowhere to go.
    Header {
        desktop: String,
        leaf: String,
        label: String,
        movable: bool,
    },
    /// A shelf column, by its chip: `leaf` is the tile it brings.
    Chip {
        desktop: String,
        column: String,
        leaf: String,
        label: String,
    },
}

impl Source {
    fn desktop(&self) -> &str {
        match self {
            Source::Header { desktop, .. } | Source::Chip { desktop, .. } => desktop,
        }
    }

    fn leaf(&self) -> &str {
        match self {
            Source::Header { leaf, .. } | Source::Chip { leaf, .. } => leaf,
        }
    }

    fn label(&self) -> &str {
        match self {
            Source::Header { label, .. } | Source::Chip { label, .. } => label,
        }
    }

    fn movable(&self) -> bool {
        match self {
            Source::Header { movable, .. } => *movable,
            Source::Chip { .. } => true,
        }
    }

    fn chip(&self) -> Option<&str> {
        match self {
            Source::Chip { column, .. } => Some(column),
            Source::Header { .. } => None,
        }
    }
}

/// A drag in flight.
#[derive(Clone, Debug)]
pub(crate) struct LeafDrag {
    pub source: Source,
    start: [f32; 2],
    at: [f32; 2],
    active: bool,
    target: Option<Target>,
}

#[derive(Clone, Debug, PartialEq)]
enum Target {
    /// Beside a column on the shelf, by its first leaf.
    Shelf {
        column: String,
        edge: Edge,
    },
    Tile(Dock),
}

/// A side of a tile the dragged tile docks against, taking `ratio` of the split it makes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Side {
    Left,
    Right,
    Top,
    Bottom,
}

impl Side {
    fn as_str(self) -> &'static str {
        match self {
            Side::Left => "left",
            Side::Right => "right",
            Side::Top => "top",
            Side::Bottom => "bottom",
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
struct Dock {
    anchor: String,
    side: Side,
    ratio: f32,
    /// Where the dragged tile would go, in window points.
    rect: [f32; 4],
    /// A chip over a tile's middle takes its place in the row.
    replaces: bool,
    /// Why a chip dropped on a tile's top or bottom lands beside it instead.
    limit: Option<String>,
}

impl LeafDrag {
    /// The shelf column the drag would land beside, and on which side.
    pub(crate) fn shelf_mark(&self) -> Option<(String, Edge)> {
        match &self.target {
            Some(Target::Shelf { column, edge }) => Some((column.clone(), *edge)),
            _ => None,
        }
    }

    /// The tile being dragged, while the press has become a drag.
    pub(crate) fn dragging(&self) -> Option<&str> {
        self.active.then(|| self.source.leaf())
    }
}

fn distance(a: [f32; 2], b: [f32; 2]) -> f32 {
    (a[0] - b[0]).hypot(a[1] - b[1])
}

/// Runs a press on a header or chip: a press that travels becomes a drag and drops where it
/// is let go; one that does not is a click.
pub(crate) fn gesture(state: &mut State, d: &mut Drag, tx: &mut Tx, source: &Source) {
    let at = d.window_point();
    match d.phase {
        Phase::Down => {
            state.window.desk.drag = Some(LeafDrag {
                source: source.clone(),
                start: at,
                at,
                active: false,
                target: None,
            });
            tx.live_only();
        }
        Phase::Move => {
            let Some(mut drag) = state.window.desk.drag.take() else {
                return;
            };
            drag.at = at;
            drag.active |= drag.source.movable() && distance(drag.start, at) >= ACTIVATION;
            if drag.active {
                drag.target = target(state, &drag);
            } else {
                tx.live_only();
            }
            state.window.desk.drag = Some(drag);
        }
        Phase::Up => {
            let Some(drag) = state.window.desk.drag.take() else {
                return;
            };
            if d.cancelled() {
                return;
            }
            let travelled = distance(drag.start, at) >= ACTIVATION;
            if drag.active || (travelled && drag.source.movable()) {
                let drag = LeafDrag {
                    at,
                    target: target(state, &LeafDrag { at, ..drag.clone() }),
                    ..drag
                };
                drop(state, &drag, tx);
            } else if !travelled {
                click(state, source, tx);
            }
        }
    }
}

fn click(state: &mut State, source: &Source, tx: &mut Tx) {
    match source {
        Source::Header { desktop, leaf, .. } => super::view::take_tile(
            state,
            &Holder {
                desktop: desktop.clone(),
                pane: leaf.clone(),
            },
            tx,
        ),
        Source::Chip { desktop, leaf, .. } => {
            state.window.desk.cap_open = None;
            super::shelf::bring(state, desktop, leaf, tx);
        }
    }
}

fn inside(rect: [f32; 4], [x, y]: [f32; 2]) -> bool {
    x >= rect[0] && x <= rect[0] + rect[2] && y >= rect[1] && y <= rect[1] + rect[3]
}

fn target(state: &State, drag: &LeafDrag) -> Option<Target> {
    match shelf_target(state, drag) {
        Some(shelf) => shelf,
        None => dock_target(state, drag).map(Target::Tile),
    }
}

/// Over the source desktop's shelf: Some(None) when over the dragged chip itself, which
/// takes the drop nowhere, and None when the pointer is not over the shelf.
fn shelf_target(state: &State, drag: &LeafDrag) -> Option<Option<Target>> {
    let shelf = Shelf::of(state).filter(|s| s.desktop == drag.source.desktop())?;
    let rects = &state.window.desk.rects;
    if !inside(*rects.get("shelf")?, drag.at) {
        return None;
    }
    let x = drag.at[0];
    let anchors: Vec<_> = shelf
        .anchors()
        .into_iter()
        .filter_map(|a| rects.get(&a.element).map(|r| (a, *r)))
        .collect();
    let own = drag.source.chip();
    if anchors
        .iter()
        .any(|(a, r)| Some(a.column.as_str()) == own && x >= r[0] && x <= r[0] + r[2])
    {
        return Some(None);
    }
    let away = |r: &[f32; 4]| {
        if x >= r[0] && x <= r[0] + r[2] {
            0.0
        } else {
            (x - r[0]).abs().min((x - r[0] - r[2]).abs())
        }
    };
    let nearest = anchors
        .iter()
        .filter(|(a, _)| Some(a.column.as_str()) != own)
        .min_by(|(_, a), (_, b)| away(a).total_cmp(&away(b)))?;
    let (anchor, rect) = nearest;
    let edge = anchor.edge.unwrap_or(if x < rect[0] + rect[2] / 2.0 {
        Edge::Left
    } else {
        Edge::Right
    });
    Some(Some(Target::Shelf {
        column: anchor.column.clone(),
        edge,
    }))
}

fn snap(fraction: f32) -> f32 {
    let clamped = fraction.clamp(BAND_MIN, BAND_MAX);
    SNAPS
        .into_iter()
        .find(|s| (clamped - s).abs() <= SNAP_TOLERANCE)
        .unwrap_or(clamped)
}

/// The band along `side` of `rect` that `ratio` of it takes.
fn band(rect: [f32; 4], side: Side, ratio: f32) -> [f32; 4] {
    let [x, y, w, h] = rect;
    match side {
        Side::Left => [x, y, w * ratio, h],
        Side::Right => [x + w * (1.0 - ratio), y, w * ratio, h],
        Side::Top => [x, y, w, h * ratio],
        Side::Bottom => [x, y + h * (1.0 - ratio), w, h * ratio],
    }
}

/// Over a tile of the current desktop: the side nearest the pointer, or for a chip over the
/// tile's middle, the tile's place.
fn dock_target(state: &State, drag: &LeafDrag) -> Option<Dock> {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let desktop = facts.current_desktop()?;
    let area = desk.area?;
    let (tree, plan) = desk.plan(facts, desktop)?;
    let view = desk.row(&desktop.id).view;
    let rendered = plan.rendered(&tree, &view, &desktop.active_pane_id);
    let (w, h) = (area.w, area.h);
    if w <= 0.0 || h <= 0.0 {
        return None;
    }
    let nx = (drag.at[0] - area.x) / w;
    let ny = (drag.at[1] - area.y) / h;
    let dragged = drag.source.leaf();
    let (anchor, bounds) = rendered.bounds().into_iter().find(|(id, r)| {
        *id != dragged
            && nx >= r.left
            && nx <= r.left + r.width
            && ny >= r.top
            && ny <= r.top + r.height
    })?;
    let rect = [
        area.x + bounds.left * w,
        area.y + bounds.top * h,
        bounds.width * w,
        bounds.height * h,
    ];
    let px = (nx - bounds.left) / bounds.width;
    let py = (ny - bounds.top) / bounds.height;
    let (side, dist) = [
        (Side::Left, px),
        (Side::Right, 1.0 - px),
        (Side::Top, py),
        (Side::Bottom, 1.0 - py),
    ]
    .into_iter()
    .fold((Side::Left, f32::INFINITY), |best, (s, d)| {
        if d < best.1 { (s, d) } else { best }
    });
    let chip = drag.source.chip();
    let source_shows = drag.source.desktop() == desktop.id;
    if chip.is_some() && source_shows && dist >= CENTRE {
        return Some(Dock {
            anchor: anchor.to_owned(),
            side: Side::Left,
            ratio: 0.5,
            rect,
            replaces: true,
            limit: None,
        });
    }
    let ratio = snap(dist);
    let dock = Dock {
        anchor: anchor.to_owned(),
        side,
        ratio,
        rect: band(rect, side, ratio),
        replaces: false,
        limit: None,
    };
    match chip {
        Some(_) if matches!(side, Side::Top | Side::Bottom) => {
            Some(fit_column(dock, rect, &tree, dragged))
        }
        _ => Some(dock),
    }
}

/// A chip's column stacked on a tile keeps every tile at the minimum height, or lands beside
/// the tile instead, saying why.
fn fit_column(dock: Dock, anchor_rect: [f32; 4], tree: &Node, leaf: &str) -> Dock {
    let row = layout::read_row(tree);
    let need = layout::column_index(&row, leaf)
        .map(|i| {
            let smallest = row[i]
                .node
                .bounds()
                .iter()
                .map(|(_, r)| r.height)
                .fold(1.0, f32::min);
            TILE_MIN_HEIGHT / smallest
        })
        .unwrap_or(TILE_MIN_HEIGHT);
    let anchor_px = anchor_rect[3];
    let needed = need + TILE_MIN_HEIGHT;
    if needed > anchor_px {
        return Dock {
            side: Side::Right,
            ratio: 0.5,
            rect: band(anchor_rect, Side::Right, 0.5),
            limit: Some(format!(
                "Stacking here needs {}px to keep every tile at the {}px minimum. This tile has {}px, so it moved beside instead.",
                needed.ceil(),
                TILE_MIN_HEIGHT,
                anchor_px.floor()
            )),
            ..dock
        };
    }
    let ratio = dock.ratio.max(need / anchor_px);
    Dock {
        ratio,
        rect: band(anchor_rect, dock.side, ratio),
        ..dock
    }
}

/// Where the column a chip leaves goes when the chip takes another tile's place in the row,
/// so the tile takes the chip's old place.
fn slot_return(tree: &Node, chip: &str, slot: &str) -> (Edge, Option<(String, Edge)>) {
    let row = layout::read_row(tree);
    let (Some(chip_at), Some(slot_at)) = (
        layout::column_index(&row, chip),
        layout::column_index(&row, slot),
    ) else {
        return (Edge::Right, None);
    };
    let from_left = chip_at < slot_at;
    let chip_edge = if from_left { Edge::Right } else { Edge::Left };
    let neighbour = if from_left {
        chip_at + 1
    } else {
        chip_at.wrapping_sub(1)
    };
    let back = (neighbour != slot_at).then(|| {
        (
            row[neighbour].first().to_owned(),
            if from_left { Edge::Left } else { Edge::Right },
        )
    });
    (chip_edge, back)
}

/// The note when two tiles at the narrowest width cannot show side by side, or None.
fn beside_limit(state: &State) -> Option<String> {
    let desk = &state.window.desk;
    let measures = desk.measures(&state.facts)?;
    let cols = Desk::narrowest_cols(&state.facts);
    let needed = 2.0 * measures.narrowest_px;
    if needed <= measures.area_px + 0.5 {
        return None;
    }
    let window_px = desk.window_w.unwrap_or(0.0);
    let sidebar = measures.area_px < window_px && needed <= window_px;
    let narrower =
        measures.narrowest_px > TILE_MIN_WIDTH && 2.0 * TILE_MIN_WIDTH <= measures.area_px;
    Some(format!(
        "2 tiles side by side at the narrowest width ({cols} cols, {}px) need {needed}px. Tiles have {}px here, so the dropped tile shows alone.{}{}",
        measures.narrowest_px,
        measures.area_px,
        if sidebar {
            " Closing the sidebar would make room."
        } else {
            ""
        },
        if narrower {
            " A narrower width can be set in Settings → Appearance."
        } else {
            ""
        },
    ))
}

fn drop(state: &mut State, drag: &LeafDrag, tx: &mut Tx) {
    let Some(target) = &drag.target else {
        return;
    };
    let source = drag.source.desktop().to_owned();
    let leaf = drag.source.leaf().to_owned();
    match (target, drag.source.chip()) {
        (Target::Shelf { column, edge }, Some(_)) => {
            super::shelf::send_move(
                state,
                &source,
                &source,
                &leaf,
                column,
                edge.as_str(),
                None,
                true,
                false,
            );
        }
        (Target::Shelf { column, edge }, None) => {
            let Some(desktop) = state.facts.desktops.iter().find(|d| d.id == source) else {
                return;
            };
            let column_leaves = Node::parse(&desktop.tree_json)
                .map(|t| {
                    let row = layout::read_row(&t);
                    layout::column_index(&row, column)
                        .map(|i| row[i].leaf_ids.clone())
                        .unwrap_or_default()
                })
                .unwrap_or_default();
            let active = desktop.active_pane_id.clone();
            let tree = desktop.tree_json.clone();
            let row = state.window.desk.row_mut(&source);
            row.held.retain(|id| !column_leaves.contains(id));
            row.drop = Some(PendingDrop {
                drop: ShelfDrop {
                    leaf: leaf.clone(),
                    anchor: column.clone(),
                    edge: *edge,
                    whole_column: false,
                },
                tree,
                active: leaf.clone(),
            });
            if let Some(note) = beside_limit(state) {
                state.window.notify(note, tx);
            }
            if active != leaf {
                actions::show_leaf(state, &source, &leaf);
            }
            super::shelf::send_move(
                state,
                &source,
                &source,
                &leaf,
                column,
                edge.as_str(),
                None,
                false,
                true,
            );
        }
        (Target::Tile(dock), Some(_)) if dock.replaces => {
            let Some(desktop) = state.facts.desktops.iter().find(|d| d.id == source) else {
                return;
            };
            let Some(tree) = Node::parse(&desktop.tree_json) else {
                return;
            };
            let (chip_edge, back) = slot_return(&tree, &leaf, &dock.anchor);
            let moves = if back.is_some() { 2 } else { 1 };
            let sent_on = desktop.tree_json.clone();
            let row = state.window.desk.row_mut(&source);
            if let Some(old) = row.swap.take() {
                row.held.retain(|id| *id != old.slot);
            }
            row.held.push(dock.anchor.clone());
            let until = match &back {
                Some((anchor, edge)) => ShelfDrop {
                    leaf: dock.anchor.clone(),
                    anchor: anchor.clone(),
                    edge: *edge,
                    whole_column: true,
                },
                None => ShelfDrop {
                    leaf: leaf.clone(),
                    anchor: dock.anchor.clone(),
                    edge: chip_edge,
                    whole_column: true,
                },
            };
            row.swap = Some(Swap {
                slot: dock.anchor.clone(),
                until,
                tree: sent_on,
                moves,
            });
            super::shelf::send_move(
                state,
                &source,
                &source,
                &leaf,
                &dock.anchor,
                chip_edge.as_str(),
                None,
                true,
                true,
            );
            if let Some((anchor, edge)) = back {
                super::shelf::send_move(
                    state,
                    &source,
                    &source,
                    &dock.anchor,
                    &anchor,
                    edge.as_str(),
                    None,
                    true,
                    false,
                );
            }
        }
        (Target::Tile(dock), chip) => {
            if let Some(limit) = &dock.limit {
                state.window.notify(limit.clone(), tx);
            }
            let Some(current) = state.facts.current_desktop().map(|d| d.id.clone()) else {
                return;
            };
            super::shelf::send_move(
                state,
                &source,
                &current,
                &leaf,
                &dock.anchor,
                dock.side.as_str(),
                Some(dock.ratio),
                chip.is_some(),
                true,
            );
        }
    }
}

/// What a drag shows over the window: the dragged tile's name by the pointer, and where it
/// would land on a tile.
pub(crate) fn overlay(state: &State, t: &Tokens) -> Vec<El> {
    let Some(drag) = state.window.desk.drag.as_ref().filter(|d| d.active) else {
        return vec![];
    };
    let ghost = row()
        .key("drag.ghost")
        .role("status")
        .label(drag.source.label().to_owned())
        .absolute(drag.at[0] + 12.0, drag.at[1] + 12.0)
        .px(space::S2)
        .py(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(
            text(drag.source.label().to_owned())
                .font(type_::LABEL)
                .color(t.text_secondary)
                .single_line(),
        );
    let mark = match &drag.target {
        Some(Target::Tile(dock)) => Some(
            rune::el()
                .key("drag.target")
                .role("group")
                .label(format!(
                    "{} {}",
                    dock.anchor,
                    if dock.replaces {
                        "place"
                    } else {
                        dock.side.as_str()
                    }
                ))
                .absolute(dock.rect[0], dock.rect[1])
                .w(dock.rect[2])
                .h(dock.rect[3])
                .radius(6.0)
                .fill(t.accent.alpha(0.14))
                .border(1.5, t.border_focus),
        ),
        _ => None,
    };
    mark.into_iter().chain([ghost]).collect()
}

/// A chip taking a tile's place: the tile stays held off screen until the last move lands.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Swap {
    pub slot: String,
    pub until: ShelfDrop,
    /// The row's tree when the moves were sent, and how many changes to it the moves make.
    pub tree: String,
    pub moves: u8,
}

/// Releases a swapped-out tile once its move landed, when a leaf it waits on went, or once
/// the row changed as often as the moves change it without the move landing.
pub(crate) fn settle_swap(row: &mut super::RowState, columns: &[layout::Column], tree: &str) {
    let Some(swap) = &mut row.swap else {
        return;
    };
    if swap.tree != tree {
        swap.tree = tree.to_owned();
        swap.moves = swap.moves.saturating_sub(1);
    }
    let gone = layout::column_index(columns, &swap.until.leaf).is_none()
        || layout::column_index(columns, &swap.until.anchor).is_none();
    if gone || swap.moves == 0 || super::shelf::beside(columns, &swap.until) {
        let slot = swap.slot.clone();
        row.held.retain(|id| *id != slot);
        row.swap = None;
    }
}

/// Ends a drag whose source is gone after the daemon's row changed: its tile left its
/// desktop, its chip left the shelf, or the window moved to another desktop. Its element no
/// longer hears the release.
pub(crate) fn settle_drag(state: &mut State) {
    let Some(drag) = &state.window.desk.drag else {
        return;
    };
    let facts = &state.facts;
    let source = &drag.source;
    let current = facts.current_desktop().filter(|d| d.id == source.desktop());
    let shown = current.is_some_and(|desktop| match source.chip() {
        Some(column) => Shelf::of(state).is_some_and(|shelf| {
            shelf
                .anchors()
                .iter()
                .any(|a| a.element == format!("shelf.chip.{column}"))
        }),
        None => state
            .window
            .desk
            .plan(facts, desktop)
            .is_some_and(|(tree, plan)| {
                let view = state.window.desk.row(&desktop.id).view;
                plan.rendered(&tree, &view, &desktop.active_pane_id)
                    .has_leaf(source.leaf())
            }),
    });
    if !shown {
        state.window.desk.drag = None;
    }
}
