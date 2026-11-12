//! Desktops and their tiles: the row the daemon arranges, which part of it this window shows,
//! and what the window does to it.

mod actions;
mod drag;
pub mod layout;
mod shelf;
mod view;

use std::collections::HashMap;

use attn_client::Facts;
use attn_protocol::Desktop;

pub(crate) use actions::{escape, run};
pub(crate) use drag::overlay as drag_overlay;
pub(crate) use shelf::{cap_menu, settle, slot as shelf_slot, width_label};
pub(crate) use view::{area, take_tile};

use layout::{DEFAULT_NARROWEST_COLS, FirstColumn, Measures, Node, RowInput, RowPlan, View};

/// How this window shows each desktop's row. The daemon owns the row, the active tile and
/// the current desktop; this is what one window adds: where its screen sits along each row,
/// how its active tile shows, and the order it used tiles in.
#[derive(Default)]
pub struct Desk {
    /// The tile area in points and a terminal cell's width, once laid out.
    pub(crate) area: Option<Area>,
    pub(crate) rows: HashMap<String, RowState>,
    /// Tiles this window showed, oldest first, for ⌘[ and ⌘].
    pub(crate) history: History,
    /// The desktop shown before the current one, where the current desktop's digit goes.
    pub(crate) previous_desktop: Option<String>,
    shown: Option<(String, String)>,
    /// The title bar's room for the shelf, in points.
    pub(crate) shelf_px: Option<f32>,
    pub(crate) window_w: Option<f32>,
    /// The pointer is over the shelf's dots, which show the width control.
    pub(crate) width_control: bool,
    /// The shelf cap whose menu is open.
    pub(crate) cap_open: Option<layout::Edge>,
    pub(crate) width_label: Option<shelf::WidthLabel>,
    /// A tile or shelf column being dragged.
    pub(crate) drag: Option<drag::LeafDrag>,
    /// Where shelf chips, caps and the shelf are in the window, by element key, for drops.
    pub(crate) rects: HashMap<String, [f32; 4]>,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub(crate) struct Area {
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
    pub px_per_col: f32,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct RowState {
    pub first_column: Option<FirstColumn>,
    pub view: View,
    /// Leaves sent to the shelf while they would have fit.
    pub held: Vec<String>,
    /// Leaves in the order this window last used them, newest first.
    pub focus_order: Vec<String>,
    /// Columns, by first leaf, whose dragged width this window asked the daemon to forget.
    pub cleared: Vec<String>,
    /// A column brought beside the active tile that the daemon has not yet moved.
    pub drop: Option<shelf::PendingDrop>,
    pub settled: Option<shelf::Settled>,
    /// A tile a chip took the place of, held off screen until the moves land.
    pub swap: Option<drag::Swap>,
}

/// A walk back and forward through the tiles a window showed, as a browser's history.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct History {
    pub entries: Vec<Visit>,
    /// The entry the window shows; entries after it are forward.
    pub at: usize,
}

/// The Tauri app's bound on a window's tile history.
const HISTORY_LIMIT: usize = 1024;

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Visit {
    pub desktop: String,
    pub leaf: String,
}

impl History {
    /// Records that the window shows `visit`, dropping the forward entries when it differs
    /// from the entry it shows.
    pub fn visit(&mut self, visit: Visit) {
        if self.entries.get(self.at) == Some(&visit) {
            return;
        }
        if !self.entries.is_empty() {
            self.entries.truncate(self.at + 1);
        }
        self.entries.push(visit);
        if self.entries.len() > HISTORY_LIMIT {
            self.entries.remove(0);
        }
        self.at = self.entries.len() - 1;
    }
}

impl Desk {
    pub(crate) fn row(&self, desktop: &str) -> RowState {
        self.rows.get(desktop).cloned().unwrap_or_default()
    }

    pub(crate) fn row_mut(&mut self, desktop: &str) -> &mut RowState {
        self.rows.entry(desktop.to_owned()).or_default()
    }

    /// Keeps the window's record of each desktop in step with the daemon's: the active tile
    /// heads the focus order, and leaves that went drop out of it.
    pub(crate) fn follow(&mut self, facts: &Facts) {
        self.rows
            .retain(|id, _| facts.desktops.iter().any(|d| &d.id == id));
        for desktop in facts.desktops.iter() {
            let leaves = leaf_ids(desktop);
            let row = self.row_mut(&desktop.id);
            row.focus_order.retain(|id| leaves.contains(id));
            row.held.retain(|id| leaves.contains(id));
            if let View::Focused(leaf) = &row.view
                && *leaf != desktop.active_pane_id
            {
                row.view = View::Normal;
            }
            let active = &desktop.active_pane_id;
            if leaves.contains(active) && row.focus_order.first() != Some(active) {
                row.held.retain(|id| id != active);
                row.focus_order.retain(|id| id != active);
                row.focus_order.insert(0, active.clone());
            }
        }
        self.follow_current(facts);
        if let Some(desktop) = facts.current_desktop()
            && !desktop.active_pane_id.is_empty()
        {
            self.history.visit(Visit {
                desktop: desktop.id.clone(),
                leaf: desktop.active_pane_id.clone(),
            });
        }
    }

    /// The current desktop the window last saw becomes the previous desktop when the profile
    /// moves to another one; a previous desktop that went, or that is current again, is gone.
    fn follow_current(&mut self, facts: &Facts) {
        let profile = facts.profiles.selected.clone();
        let current = facts.current_desktop().map(|d| d.id.clone());
        let shown = current.clone().map(|c| (profile.clone(), c));
        if let (Some((was_profile, was)), Some((_, now))) = (&self.shown, &shown)
            && was != now
        {
            self.previous_desktop = (*was_profile == profile).then(|| was.clone());
        }
        if self.shown.as_ref().is_some_and(|(p, _)| *p != profile) {
            self.previous_desktop = None;
        }
        self.previous_desktop.take_if(|previous| {
            current.as_ref() == Some(previous) || !facts.desktops.iter().any(|d| d.id == *previous)
        });
        if shown.is_some() && shown != self.shown {
            self.cap_open = None;
        }
        if shown.is_some() {
            self.shown = shown;
        }
    }

    /// The narrowest tile width the settings ask for, in terminal columns.
    pub(crate) fn narrowest_cols(facts: &Facts) -> u32 {
        facts
            .settings
            .get("narrowest_tile_cols")
            .and_then(|v| v.parse().ok())
            .filter(|cols| *cols > 0)
            .unwrap_or(DEFAULT_NARROWEST_COLS)
    }

    pub(crate) fn measures(&self, facts: &Facts) -> Option<Measures> {
        let area = self.area?;
        Some(Measures::new(
            area.w,
            Self::narrowest_cols(facts),
            area.px_per_col,
        ))
    }

    /// What this window's screen shows of `desktop`'s row; None for an empty desktop.
    pub(crate) fn plan(&self, facts: &Facts, desktop: &Desktop) -> Option<(Node, RowPlan)> {
        let tree = Node::parse(&desktop.tree_json)?.main()?;
        let row = self.row(&desktop.id);
        let measures = self.measures(facts).unwrap_or(Measures {
            area_px: f32::INFINITY,
            narrowest_px: 0.0,
            px_per_col: 1.0,
        });
        let plan = layout::resolve_row(&RowInput {
            tree: &tree,
            active: &desktop.active_pane_id,
            first_column: row.first_column.as_ref(),
            held: &row.held,
            cleared: &row.cleared,
            measures,
        });
        Some((tree, plan))
    }
}

pub(crate) fn leaf_ids(desktop: &Desktop) -> Vec<String> {
    Node::parse(&desktop.tree_json)
        .map(|tree| tree.leaves().into_iter().map(|l| l.id.clone()).collect())
        .unwrap_or_default()
}

/// The agents whose tiles this window shows: none at home, else the current desktop's
/// columns on screen, or its one focused tile.
pub(crate) fn on_screen_sessions(state: &crate::State) -> Vec<String> {
    if state.window.view == crate::shell::View::Home {
        return Vec::new();
    }
    let Some(desktop) = state.facts.current_desktop() else {
        return Vec::new();
    };
    let leaves: Vec<String> = match state.window.desk.row(&desktop.id).view {
        View::Focused(leaf) => vec![leaf],
        _ => state
            .window
            .desk
            .plan(&state.facts, desktop)
            .map(|(_, plan)| plan.screen.into_iter().flat_map(|c| c.leaf_ids).collect())
            .unwrap_or_default(),
    };
    desktop
        .panes
        .iter()
        .filter(|p| leaves.contains(&p.pane_id))
        .map(|p| p.session_id.clone())
        .collect()
}
