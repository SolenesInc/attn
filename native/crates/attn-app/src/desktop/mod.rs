//! Desktops and their tiles: the row the daemon arranges, which part of it this window shows,
//! and what the window does to it.

mod actions;
mod annotation_popup;
mod divider;
mod drag;
mod filter_panel;
mod find_bar;
pub mod layout;
mod overview;
mod peek;
mod shelf;
mod terminal_menu;
mod view;
mod windows;

use std::collections::HashMap;

use attn_client::Facts;
use attn_protocol::Desktop;
use rune::Tx;

use crate::State;

pub(crate) use actions::{escape, give_keyboard, run, show_unseen};
pub(crate) use drag::{DesktopMark, overlay as drag_overlay};
pub(crate) use overview::view as overview;
pub(crate) use peek::view as peek;
pub(crate) use shelf::{cap_menu, settle, slot as shelf_slot, width_label};
pub(crate) use view::{area, take_tile};
pub use windows::{
    Deliver, Opened, Opener, Placement, Setup, TileWindow, on_close as tile_window_closed,
    on_quit as tile_window_quits, tile_window_leaf,
};
pub(crate) use windows::{
    follow as follow_windows, follow_title as follow_window_title, forward as forward_to_main,
    on_focus as window_focus, view as windows_view,
};

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
    /// A column divider being dragged.
    pub(crate) column_drag: Option<divider::ColumnDrag>,
    /// A split divider being dragged.
    pub(crate) ratio_drag: Option<divider::RatioDrag>,
    /// Where each divider on screen is in the window, and which way it splits, by split id.
    pub(crate) dividers: HashMap<String, ([f32; 4], layout::Direction)>,
    /// Split ratios this window shows ahead of the daemon, by split id.
    pub(crate) ratios: HashMap<String, divider::RatioOverride>,
    /// Chips that flash for a moment: a tile that arrived or went off screen.
    pub(crate) flashes: Vec<shelf::Flash>,
    /// A tile or shelf column being dragged.
    pub(crate) drag: Option<drag::LeafDrag>,
    /// Where shelf chips, caps and the shelf are in the window, by element key, for drops.
    pub(crate) rects: HashMap<String, [f32; 4]>,
    pub(crate) peeks: peek::Peeks,
    /// The desktop the overview's keys act on.
    pub(crate) overview_focus: Option<String>,
    /// The drain under each tile header whose agent's turn counts down to settling, by session.
    pub(crate) settling: HashMap<String, crate::sidebar::Countdown>,
    pub(crate) windows: windows::Pool,
    /// The session a split here spawned, whose tile takes the keyboard when the daemon places
    /// it.
    pub(crate) split: Option<String>,
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
    pub pending_widths: Vec<(String, u32)>,
    /// A column brought beside the active tile that the daemon has not yet moved.
    pub drop: Option<shelf::PendingDrop>,
    pub settled: Option<shelf::Settled>,
    /// A tile a chip took the place of, held off screen until the moves land.
    pub swap: Option<drag::Swap>,
    /// Documents that arrived off screen, not yet seen.
    pub unseen: Vec<String>,
    /// Where the screen goes back to when a document that arrived leaves the row, by leaf.
    pub returns: Vec<(String, shelf::Return)>,
    /// Whether this window has shown the desktop, so its screen is one someone saw.
    pub shown: bool,
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

/// Lets the moments the desktops keep lapse when their time is up: chips' flashes, the rest a
/// drag takes on a sidebar desktop before the window switches to it, and the peek's delays and
/// reads. Returns whether anything changed.
pub(crate) fn expire(state: &mut State, tx: &mut Tx) -> bool {
    let switched = drag::expire(state, tx) | peek::expire(state, tx);
    let now = tx.clock();
    let flashes = &mut state.window.desk.flashes;
    let before = flashes.len();
    flashes.retain(|f| f.until > now);
    switched || flashes.len() != before
}

impl Desk {
    pub(crate) fn row(&self, desktop: &str) -> RowState {
        self.rows.get(desktop).cloned().unwrap_or_default()
    }

    /// The tile the person acts on with the active tile's keys: the one `desktop` shows alone,
    /// or else its active tile.
    pub(crate) fn acting_leaf(&self, desktop: &Desktop) -> String {
        match self.row(&desktop.id).view {
            View::Focused(leaf) => leaf,
            _ => desktop.active_pane_id.clone(),
        }
    }

    /// The person picked `leaf` on `desktop`: a tile shown alone there gives way to it.
    /// Whether one did.
    pub(crate) fn pick(&mut self, desktop: &str, leaf: &str) -> bool {
        let row = self.row_mut(desktop);
        let gives_way = matches!(&row.view, View::Focused(focused) if focused != leaf);
        if gives_way {
            row.view = View::Normal;
        }
        gives_way
    }

    /// Where the tile area lays out `rendered`, the screen it shows, from the area's top left
    /// corner: each split at the ratio this window shows.
    pub(crate) fn placement(&self, rendered: &Node) -> layout::Placement {
        let (w, h) = self.area.map_or((0.0, 0.0), |a| (a.w, a.h));
        layout::place(rendered, (w, h), view::GAP, &|split| {
            self.ratios.get(&split.id).map_or(split.ratio, |o| o.ratio)
        })
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
                && !leaves.contains(leaf)
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
    /// moves to another one, and stops showing a tile alone; a previous desktop that went, or
    /// that is current again, is gone.
    fn follow_current(&mut self, facts: &Facts) {
        let profile = facts.profiles.selected.clone();
        let current = facts.current_desktop().map(|d| d.id.clone());
        let shown = current.clone().map(|c| (profile.clone(), c));
        if let (Some((was_profile, was)), Some((_, now))) = (&self.shown, &shown)
            && was != now
        {
            self.previous_desktop = (*was_profile == profile).then(|| was.clone());
            if let Some(left) = self.rows.get_mut(was)
                && matches!(left.view, View::Focused(_))
            {
                left.view = View::Normal;
            }
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
            pending: &row.pending_widths,
            measures,
        });
        Some((tree, plan))
    }
}

/// Keeps a drain for each agent on screen whose turn counts down to settling. True when one
/// began or ended.
pub(crate) fn follow_settling(state: &mut State, now_ms: i64) -> bool {
    let settling = &state.window.desk.settling;
    let any = state.facts.sessions.iter().any(|s| {
        s.auto_settle_fires_at
            .as_ref()
            .is_some_and(|at| !at.is_empty())
    });
    if settling.is_empty() && !any {
        return false;
    }
    let on_screen = on_screen_sessions(state);
    let counting: Vec<(String, String)> = state
        .facts
        .sessions
        .iter()
        .filter(|s| on_screen.contains(&s.id))
        .filter_map(|s| Some((s.id.clone(), s.auto_settle_fires_at.clone()?)))
        .filter(|(_, at)| !at.is_empty())
        .collect();
    let settling = &mut state.window.desk.settling;
    let before = settling.len();
    settling.retain(|id, c| counting.iter().any(|(i, at)| i == id && *at == c.fires_at));
    let mut changed = settling.len() != before;
    for (id, fires_at) in counting {
        settling.entry(id).or_insert_with(|| {
            changed = true;
            crate::sidebar::Countdown::seen(fires_at, now_ms)
        });
    }
    changed
}

/// A desktop with no tiles of any kind, neither agents nor documents: the launcher's place.
pub(crate) fn holds_nothing(desktop: &Desktop) -> bool {
    desktop.panes.is_empty() && leaf_ids(desktop).is_empty()
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

fn request_id() -> String {
    uuid::Uuid::new_v4().to_string()
}

/// The tile the current desktop shows alone.
pub(crate) fn shown_alone(state: &State) -> Option<String> {
    let desktop = state.facts.current_desktop()?;
    match &state.window.desk.row(&desktop.id).view {
        View::Focused(leaf) => Some(leaf.clone()),
        _ => None,
    }
}

/// Whether `next` changes the tiles of the desktop the window shows, which then move as the
/// Tauri app moves them: on the same desktop, with no tile shown alone and no divider dragged.
pub(crate) fn moves_tiles(state: &State, next: &Facts) -> bool {
    let (Some(was), Some(is)) = (state.facts.current_desktop(), next.current_desktop()) else {
        return false;
    };
    let desk = &state.window.desk;
    was.id == is.id
        && (was.tree_json != is.tree_json || was.active_pane_id != is.active_pane_id)
        && state.window.view == crate::shell::View::Desktop
        && !matches!(desk.row(&is.id).view, View::Focused(_))
        && desk.column_drag.is_none()
        && desk.ratio_drag.is_none()
}
