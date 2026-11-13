//! Dragging a tile by its header, or a shelf column by its chip, onto a tile's edge, into a
//! tile's place, beside a column on the shelf, or onto a desktop in the sidebar.

use attn_protocol::{DesktopMoveLeafMessage, ProfileActionResultMessage};
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
/// How long a drag rests on a sidebar desktop before the window switches to it.
const HOVER_SWITCH: f64 = 0.32;
/// The share of the desktop a tile dropped on a sidebar desktop takes, at its left.
const SIDEBAR_SHARE: f64 = 0.32;

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

/// The element that runs a drag. It stays on screen for as long as the drag lasts, even when
/// the drag switches desktops, so it hears the drag to its end.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Owner {
    /// The tile area: headers.
    Tiles,
    /// The shelf in the title bar: its chips.
    Shelf,
    /// An open cap's menu: the chips it holds.
    Menu,
}

impl Owner {
    fn element(self) -> &'static str {
        match self {
            Owner::Tiles => "tiles",
            Owner::Shelf => "shelf",
            Owner::Menu => "shelf.menu.layer",
        }
    }

    /// What a press at `path` in this owner picks up.
    fn source_at(self, state: &State, path: &str) -> Option<Source> {
        path.split('/').rev().find_map(|segment| match self {
            Owner::Tiles => segment
                .strip_prefix("tile.")
                .and_then(|s| s.strip_suffix(".header"))
                .and_then(|leaf| super::view::header_source(state, leaf)),
            Owner::Shelf | Owner::Menu => segment
                .strip_prefix("shelf.chip.")
                .and_then(|column| Shelf::of(state)?.chip_source(column)),
        })
    }
}

/// A drag in flight.
#[derive(Clone, Debug)]
pub(crate) struct LeafDrag {
    pub owner: Owner,
    pub source: Source,
    start: [f32; 2],
    at: [f32; 2],
    active: bool,
    target: Option<Target>,
    /// The sidebar desktop the drag rests on, and when the window switches to it.
    entering: Option<(String, f64)>,
}

#[derive(Clone, Debug, PartialEq)]
enum Target {
    /// Beside a column on the shelf, by its first leaf.
    Shelf {
        column: String,
        edge: Edge,
    },
    Tile(Dock),
    /// Another desktop, in the sidebar.
    Desktop(String),
    /// The sidebar's drop for a new desktop.
    NewDesktop,
}

/// How a sidebar desktop shows while a tile is dragged.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum DesktopMark {
    /// The desktop the drag started on, which takes no drop.
    Source,
    Accepts,
    /// The drag rests on it, and the window switches to it soon.
    Entering,
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

    /// How sidebar desktop `desktop` shows, while the press has become a drag.
    pub(crate) fn desktop_mark(&self, desktop: &str) -> Option<DesktopMark> {
        if !self.active {
            None
        } else if desktop == self.source.desktop() {
            Some(DesktopMark::Source)
        } else if self.entering.as_ref().is_some_and(|(on, _)| on == desktop) {
            Some(DesktopMark::Entering)
        } else {
            Some(DesktopMark::Accepts)
        }
    }

    /// What sidebar desktops show of the drag, for a view to read: the desktop it started on
    /// and the one it rests on, while the press has become a drag.
    pub(crate) fn desktop_marks(&self) -> Option<(String, Option<String>)> {
        self.active.then(|| {
            let entering = self.entering.as_ref().map(|(on, _)| on.clone());
            (self.source.desktop().to_owned(), entering)
        })
    }

    /// Whether the sidebar shows its drop for a new desktop, and whether the drag is over it.
    pub(crate) fn new_desktop(&self) -> Option<bool> {
        self.active.then(|| self.target == Some(Target::NewDesktop))
    }

    /// The tile being dragged, while the press has become a drag.
    pub(crate) fn dragging(&self) -> Option<&str> {
        self.active.then(|| self.source.leaf())
    }
}

fn distance(a: [f32; 2], b: [f32; 2]) -> f32 {
    (a[0] - b[0]).hypot(a[1] - b[1])
}

/// Runs a press on `owner`: on a header or chip, a press that travels becomes a drag and
/// drops where it is let go, and one that does not is a click.
pub(crate) fn gesture(state: &mut State, d: &mut Drag, tx: &mut Tx, owner: Owner) {
    let at = d.window_point();
    match d.phase {
        Phase::Down => {
            state.window.desk.drag = None;
            super::peek::dismiss(state);
            tx.live_only();
            tx.element_at_in(
                owner.element(),
                at[0],
                at[1],
                move |state: &mut State, picked, _: &mut Tx| {
                    let source = picked.and_then(|p| owner.source_at(state, p.path()));
                    state.window.desk.drag = source.map(|source| LeafDrag {
                        owner,
                        source,
                        start: at,
                        at,
                        active: false,
                        target: None,
                        entering: None,
                    });
                },
            );
        }
        Phase::Move => {
            let Some(mut drag) = state.window.desk.drag.take() else {
                return;
            };
            drag.at = at;
            drag.active |= drag.source.movable() && distance(drag.start, at) >= ACTIVATION;
            if drag.active {
                drag.target = target(state, &drag);
                tx.element_at_in(
                    crate::sidebar::drop_scope(state),
                    at[0],
                    at[1],
                    over_sidebar,
                );
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
                let target = target(state, &LeafDrag { at, ..drag.clone() });
                let drag = LeafDrag { at, target, ..drag };
                let with_delegates = tx.mods().alt;
                tx.element_at_in(
                    crate::sidebar::drop_scope(state),
                    at[0],
                    at[1],
                    move |state: &mut State, picked, tx: &mut Tx| {
                        let drag = match picked.and_then(|p| sidebar_target(state, &drag, p.path()))
                        {
                            Some(target) => LeafDrag {
                                target: Some(target),
                                ..drag
                            },
                            None => drag,
                        };
                        drop(state, &drag, with_delegates, tx);
                    },
                );
            } else if !travelled {
                click(state, &drag.source, tx);
            }
        }
    }
}

/// The sidebar or queue bar target at `path`: a desktop other than the one the drag started
/// on, or the drop for a new desktop.
fn sidebar_target(state: &State, drag: &LeafDrag, path: &str) -> Option<Target> {
    if path.split('/').any(|s| s == "sidebar.new_desktop") {
        return Some(Target::NewDesktop);
    }
    let desktop = crate::sidebar::desktop_at(state, path)?;
    (desktop != drag.source.desktop()).then_some(Target::Desktop(desktop))
}

/// Takes the sidebar's part of a drag's move: what the pointer is over there, and the switch
/// to a desktop the drag rests on.
fn over_sidebar(state: &mut State, picked: Option<rune::Picked>, tx: &mut Tx) {
    let Some(drag) = &state.window.desk.drag else {
        return;
    };
    let target = picked.and_then(|p| sidebar_target(state, drag, p.path()));
    let current = state.facts.current_desktop().map(|d| d.id.as_str());
    let entering = match &target {
        Some(Target::Desktop(id)) if current == Some(id.as_str()) => None,
        Some(Target::Desktop(id)) => match &drag.entering {
            Some((on, until)) if on == id => Some((on.clone(), *until)),
            _ => {
                let until = tx.clock() + HOVER_SWITCH;
                tx.wake_at(until);
                Some((id.clone(), until))
            }
        },
        _ => None,
    };
    let Some(drag) = &mut state.window.desk.drag else {
        return;
    };
    if target.is_some() {
        drag.target = target;
    }
    drag.entering = entering;
}

/// Switches to the sidebar desktop a drag rested on long enough. Returns whether it did.
pub(crate) fn expire(state: &mut State, tx: &mut Tx) -> bool {
    let now = tx.clock();
    let Some(drag) = &mut state.window.desk.drag else {
        return false;
    };
    let Some((desktop, _)) = drag.entering.take_if(|(_, until)| now >= *until) else {
        return false;
    };
    crate::sidebar::open_desktop(state, &desktop, tx);
    true
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
            let source = facts
                .desktops
                .iter()
                .find(|d| d.id == drag.source.desktop())
                .and_then(|d| Node::parse(&d.tree_json));
            Some(fit_column(dock, rect, source.as_ref()?, dragged))
        }
        _ => Some(dock),
    }
}

/// A chip's column stacked on a tile keeps every tile at the minimum height, or lands beside
/// the tile instead, saying why. `source` is the tree of the desktop the chip comes from.
fn fit_column(dock: Dock, anchor_rect: [f32; 4], source: &Node, leaf: &str) -> Dock {
    let row = layout::read_row(source);
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

/// Drops `drag` on its target. Onto a desktop in the sidebar, `with_delegates` takes the
/// agent's delegates along.
fn drop(state: &mut State, drag: &LeafDrag, with_delegates: bool, tx: &mut Tx) {
    let Some(target) = &drag.target else {
        return;
    };
    let source = drag.source.desktop().to_owned();
    let leaf = drag.source.leaf().to_owned();
    match (target, drag.source.chip()) {
        (Target::Desktop(desktop), chip) => {
            crate::sidebar::open_desktop(state, desktop, tx);
            let to = Some(desktop.clone());
            to_desktop(
                state,
                &source,
                to,
                &leaf,
                chip.is_some(),
                with_delegates,
                tx,
            );
        }
        (Target::NewDesktop, chip) => {
            to_desktop(
                state,
                &source,
                None,
                &leaf,
                chip.is_some(),
                with_delegates,
                tx,
            );
        }
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
                "reorder the shelf",
                tx,
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
            if active == leaf {
                actions::give_keyboard(state, &source, &leaf, tx);
            } else {
                actions::show_leaf(state, &source, &leaf, tx);
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
                "move that tile",
                tx,
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
                chip: leaf.clone(),
                slot: dock.anchor.clone(),
                until,
                tree: sent_on,
                moves,
            });
            actions::give_keyboard(state, &source, &leaf, tx);
            let (from, slot) = (source.clone(), dock.anchor.clone());
            super::shelf::send_move_then(
                state,
                (&source, &source, &leaf, &dock.anchor, chip_edge.as_str()),
                None,
                true,
                true,
                "put that tile in the slot",
                move |state: &mut State, moved, tx: &mut Tx| {
                    if moved && let Some((anchor, edge)) = back {
                        super::shelf::send_move(
                            state,
                            &from,
                            &from,
                            &slot,
                            &anchor,
                            edge.as_str(),
                            None,
                            true,
                            false,
                            "put that tile in the slot",
                            tx,
                        );
                    }
                },
                tx,
            );
        }
        (Target::Tile(dock), chip) => {
            if let Some(limit) = &dock.limit {
                state.window.notify(limit.clone(), tx);
            }
            let Some(current) = state.facts.current_desktop().map(|d| d.id.clone()) else {
                return;
            };
            let already_there = current == source;
            if already_there {
                actions::give_keyboard(state, &current, &leaf, tx);
            }
            let (landed_on, landed) = (current.clone(), leaf.clone());
            super::shelf::send_move_then(
                state,
                (&source, &current, &leaf, &dock.anchor, dock.side.as_str()),
                Some(dock.ratio),
                chip.is_some(),
                true,
                "move that tile",
                move |state: &mut State, moved, tx: &mut Tx| {
                    if moved && !already_there {
                        actions::give_keyboard(state, &landed_on, &landed, tx);
                    }
                },
                tx,
            );
        }
    }
}

/// Moves `leaf` to the left of desktop `target`, or of a new desktop, as a drop on the
/// sidebar does, then gives `leaf` the keyboard on `target` or shows the new desktop.
fn to_desktop(
    state: &mut State,
    source: &str,
    target: Option<String>,
    leaf: &str,
    whole_column: bool,
    with_delegates: bool,
    tx: &mut Tx,
) {
    let new = target.is_none();
    let (from, landing, moved) = (source.to_owned(), target.clone(), leaf.to_owned());
    state.ask(
        |id| DesktopMoveLeafMessage {
            request_id: id,
            source_desktop_id: source.to_owned(),
            target_desktop_id: target,
            leaf_id: leaf.to_owned(),
            edge: Some(Edge::Left.as_str().to_owned()),
            leaf_share: Some(SIDEBAR_SHARE),
            whole_column: whole_column.then_some(true),
            with_delegates: with_delegates.then_some(true),
            ..DesktopMoveLeafMessage::new()
        },
        move |state: &mut State, answer, tx: &mut Tx| match answer.outcome() {
            Ok(reply) => match landing {
                Some(desktop) => actions::give_keyboard(state, &desktop, &moved, tx),
                None => {
                    let made = reply
                        .parse::<ProfileActionResultMessage>()
                        .and_then(|r| r.desktops?.into_iter().find(|d| d.id != from));
                    if let Some(desktop) = made {
                        crate::sidebar::open_desktop(state, &desktop.id, tx);
                    }
                }
            },
            Err(error) => {
                let what = if new {
                    "Could not move that pane to a new desktop"
                } else {
                    "Could not move that pane"
                };
                state.window.notify(format!("{what}: {error}"), tx);
            }
        },
        tx,
    );
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
    /// The chip's tile, which goes into the slot.
    pub chip: String,
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

/// Ends a drag whose tile left the desktop it was dragged from, or whose window left the
/// desktops for Home.
pub(crate) fn settle_drag(state: &mut State) {
    let Some(drag) = &state.window.desk.drag else {
        return;
    };
    let source = &drag.source;
    let there = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == source.desktop())
        .and_then(|d| Node::parse(&d.tree_json))
        .is_some_and(|tree| tree.has_leaf(source.leaf()));
    if !there || state.window.view != crate::shell::View::Desktop {
        state.window.desk.drag = None;
    }
}
