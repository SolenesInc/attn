//! Dragging the divider between two tiles: between screen columns it sets their widths,
//! anywhere else the share the split gives its first tile.

use attn_protocol::{DesktopSetSplitRatioMessage, DesktopSetWantedColsMessage};
use rune::{Drag, Phase, Tx};

use super::layout::{self, ColumnDragStart, Direction, FirstColumn, Node};
use crate::State;

/// How far the pointer travels across a column divider before the press becomes a drag.
const ACTIVATION: f32 = 4.0;
/// How close the daemon's ratio has to be to the dragged one to count as the same.
const RATIO_SAME: f32 = 0.005;

/// A column divider being dragged, and what the row was when it started, to put back on a
/// cancel.
#[derive(Clone, Debug)]
pub(crate) struct ColumnDrag {
    pub desktop: String,
    divider: usize,
    start: ColumnDragStart,
    start_x: f32,
    pending: Vec<(String, u32)>,
    first_column: Option<FirstColumn>,
    active: bool,
}

/// A split's ratio this window shows ahead of the daemon: while its divider is dragged, and
/// after, until the daemon's ratio is the same or changes from `base`.
#[derive(Clone, Copy, Debug, PartialEq)]
pub(crate) struct RatioOverride {
    pub ratio: f32,
    base: f32,
    dragging: bool,
}

/// The screen column a `row:<first leaf>` split divides from the next, or None for a split
/// inside a column.
fn column_divider(state: &State, desktop: &str, split: &str) -> Option<(usize, ColumnDragStart)> {
    let leaf = split.strip_prefix("row:")?;
    let facts = &state.facts;
    let d = facts.desktops.iter().find(|d| d.id == desktop)?;
    let (_, plan) = state.window.desk.plan(facts, d)?;
    let at = plan.screen[..plan.screen.len().saturating_sub(1)]
        .iter()
        .position(|c| c.first() == leaf)?;
    Some((
        at,
        ColumnDragStart {
            screen: plan.screen,
            widths: plan.widths,
            first_column: plan.first_column,
        },
    ))
}

/// A split divider being dragged.
#[derive(Clone, Debug)]
pub(crate) struct RatioDrag {
    desktop: String,
    split: String,
    direction: Direction,
}

/// How far either side of a divider a press still takes it, as the Tauri app's 9 point strip.
const HIT_SLOP: f32 = 1.5;

/// The divider under `at` on the current desktop's screen.
fn hit(state: &State, [x, y]: [f32; 2]) -> Option<(String, String, Direction)> {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let desktop = facts.current_desktop()?;
    let (tree, plan) = desk.plan(facts, desktop)?;
    let view = desk.row(&desktop.id).view;
    if view != layout::View::Normal {
        return None;
    }
    let rendered = plan.rendered(&tree, &view, &desktop.active_pane_id);
    desk.dividers.iter().find_map(|(split, (r, direction))| {
        let inside = x >= r[0] - HIT_SLOP
            && x <= r[0] + r[2] + HIT_SLOP
            && y >= r[1] - HIT_SLOP
            && y <= r[1] + r[3] + HIT_SLOP;
        (inside && rendered.split_ratio(split).is_some())
            .then(|| (desktop.id.clone(), split.clone(), *direction))
    })
}

/// Runs a press in the tile area: a divider's or a header's. The area, which stays put
/// whatever the screen shows, follows the drag to its end.
pub(crate) fn area_gesture(state: &mut State, d: &mut Drag, tx: &mut Tx) {
    if d.phase == Phase::Down {
        let Some((desktop, split, direction)) = hit(state, d.window_point()) else {
            super::drag::gesture(state, d, tx, super::drag::Owner::Tiles);
            return;
        };
        match column_divider(state, &desktop, &split) {
            Some((divider, start)) => {
                let row = state.window.desk.row(&desktop);
                state.window.desk.column_drag = Some(ColumnDrag {
                    desktop,
                    divider,
                    start,
                    start_x: d.window_point()[0],
                    pending: row.pending_widths,
                    first_column: row.first_column,
                    active: false,
                });
                tx.live_only();
            }
            None => {
                let drag = RatioDrag {
                    desktop,
                    split,
                    direction,
                };
                ratio_gesture(state, d, tx, &drag);
                state.window.desk.ratio_drag = Some(drag);
            }
        }
        return;
    }
    if state.window.desk.column_drag.is_some() {
        column_gesture(state, d, tx);
    } else if let Some(drag) = state.window.desk.ratio_drag.clone() {
        ratio_gesture(state, d, tx, &drag);
        if d.phase == Phase::Up {
            state.window.desk.ratio_drag = None;
        }
    } else {
        super::drag::gesture(state, d, tx, super::drag::Owner::Tiles);
    }
}

fn column_gesture(state: &mut State, d: &mut Drag, tx: &mut Tx) {
    let x = d.window_point()[0];
    match d.phase {
        Phase::Down => {}
        Phase::Move => {
            let Some(mut drag) = state.window.desk.column_drag.take() else {
                return;
            };
            drag.active |= (x - drag.start_x).abs() >= ACTIVATION;
            if drag.active {
                follow_column(state, &drag, x);
            } else {
                tx.live_only();
            }
            state.window.desk.column_drag = Some(drag);
        }
        Phase::Up => {
            let Some(drag) = state.window.desk.column_drag.take() else {
                return;
            };
            if d.cancelled() {
                if drag.active {
                    let row = state.window.desk.row_mut(&drag.desktop);
                    row.pending_widths = drag.pending;
                    row.first_column = drag.first_column;
                }
                return;
            }
            if !drag.active {
                if d.clicks == 2 {
                    clear_beside(state, &drag);
                }
                return;
            }
            let wanted = follow_column(state, &drag, x);
            let area_cols = state
                .window
                .desk
                .area
                .map_or(0, |a| (a.w / a.px_per_col).floor() as i64);
            for (leaf, cols) in wanted {
                let _ = state.daemon.send(&DesktopSetWantedColsMessage {
                    request_id: super::request_id(),
                    desktop_id: drag.desktop.clone(),
                    leaf_id: leaf,
                    cols: cols as i64,
                    area_cols: if cols > 0 { area_cols } else { 0 },
                    ..DesktopSetWantedColsMessage::new()
                });
            }
        }
    }
}

/// Lays the row out as the divider at `x` asks, returning the widths it asks for.
fn follow_column(state: &mut State, drag: &ColumnDrag, x: f32) -> Vec<(String, u32)> {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let (Some(desktop), Some(measures)) = (
        facts.desktops.iter().find(|d| d.id == drag.desktop),
        desk.measures(facts),
    ) else {
        return vec![];
    };
    let result = layout::apply_column_drag(
        &drag.start,
        drag.divider,
        x - drag.start_x,
        &desktop.active_pane_id,
        &measures,
    );
    let row = state.window.desk.row_mut(&drag.desktop);
    row.pending_widths = drag
        .pending
        .iter()
        .cloned()
        .chain(result.wanted.iter().cloned())
        .collect();
    row.first_column = Some(result.first_column);
    result.wanted
}

/// A double click on a column divider forgets the dragged widths of the columns either side.
fn clear_beside(state: &mut State, drag: &ColumnDrag) {
    let leaves: Vec<String> = drag.start.screen[drag.divider..=drag.divider + 1]
        .iter()
        .filter(|c| c.wanted_cols.is_some())
        .map(|c| c.first().to_owned())
        .collect();
    super::shelf::clear_widths(state, &drag.desktop, &leaves);
}

fn ratio_gesture(state: &mut State, d: &mut Drag, tx: &mut Tx, drag: &RatioDrag) {
    let (desktop, split, direction) = (drag.desktop.as_str(), drag.split.as_str(), drag.direction);
    let Some(rect) = state
        .window
        .desk
        .rects
        .get(&format!("split.{split}"))
        .copied()
    else {
        if d.phase == Phase::Up {
            state.window.desk.ratios.remove(split);
        }
        return;
    };
    let base = daemon_ratio(state, desktop, split).unwrap_or(0.5);
    let [px, py] = d.window_point();
    let (offset, span) = match direction {
        Direction::Vertical => (px - rect[0], rect[2]),
        Direction::Horizontal => (py - rect[1], rect[3]),
    };
    let (min, max) = layout::ratio_bounds(direction, span);
    let ratio = if span > 0.0 {
        (offset / span).clamp(min, max)
    } else {
        0.5
    };
    let overrides = &mut state.window.desk.ratios;
    match d.phase {
        Phase::Down => tx.live_only(),
        Phase::Move => {
            overrides.insert(
                split.to_owned(),
                RatioOverride {
                    ratio,
                    base,
                    dragging: true,
                },
            );
        }
        Phase::Up if d.cancelled() => {
            overrides.remove(split);
        }
        Phase::Up => {
            overrides.insert(
                split.to_owned(),
                RatioOverride {
                    ratio,
                    base,
                    dragging: false,
                },
            );
            let _ = state.daemon.send(&DesktopSetSplitRatioMessage {
                request_id: super::request_id(),
                desktop_id: desktop.to_owned(),
                split_id: split.to_owned(),
                ratio: ratio as f64,
                ..DesktopSetSplitRatioMessage::new()
            });
        }
    }
}

fn daemon_ratio(state: &State, desktop: &str, split: &str) -> Option<f32> {
    let d = state.facts.desktops.iter().find(|d| d.id == desktop)?;
    Node::parse(&d.tree_json)?.split_ratio(split)
}

/// Forgets each ratio override the daemon caught up with or changed under, unless its divider
/// is still held.
pub(crate) fn settle_ratios(state: &mut State) {
    let trees: Vec<Node> = state
        .facts
        .desktops
        .iter()
        .filter_map(|d| Node::parse(&d.tree_json))
        .collect();
    state.window.desk.ratios.retain(|split, o| {
        if o.dragging {
            return true;
        }
        match trees.iter().find_map(|t| t.split_ratio(split)) {
            None => false,
            Some(now) => (now - o.ratio).abs() > RATIO_SAME && (now - o.base).abs() <= RATIO_SAME,
        }
    });
}
