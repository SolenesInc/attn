use std::collections::hash_map::Entry;

use attn_client::{Commands, Facts};
use attn_protocol::{GetScreenSnapshotMessage, GetScreenSnapshotResultMessage};
use attn_term::{Grid, Vt};
use attn_ui::colors::TerminalColors;

use super::{Terminals, base64_decode, clamp, themed};

/// What the daemon made of a terminal's screen for this window to draw small.
pub(super) enum Snapshot {
    Asked,
    Shown(Box<Grid>),
    Refused,
}

/// The mark a terminal's miniature carries, apart from its tile's.
pub(crate) fn miniature_mark(runtime_id: &str) -> u32 {
    super::mark(&format!("miniature.{runtime_id}"))
}

impl Terminals {
    /// Forgets the snapshots the daemon answered with, so the next `ask_snapshots` asks anew.
    pub(crate) fn forget_snapshots(&mut self) {
        self.snapshots
            .retain(|_, snapshot| matches!(snapshot, Snapshot::Asked));
    }

    /// Forgets the snapshots of the terminals that went.
    pub(crate) fn follow_snapshots(&mut self, facts: &Facts) {
        if self.snapshots.is_empty() {
            return;
        }
        self.snapshots.retain(|id, _| {
            facts
                .desktops
                .iter()
                .any(|desktop| desktop.panes.iter().any(|pane| pane.runtime_id == *id))
        });
    }

    /// Asks the daemon for the screen of each of `ids` that this window does not draw live.
    pub(crate) fn ask_snapshots<'a>(
        &mut self,
        ids: impl Iterator<Item = &'a str>,
        daemon: &Commands,
    ) {
        for id in ids.filter(|id| !id.is_empty()) {
            if !self.up.home || self.live_grid(id).is_some() {
                continue;
            }
            let Entry::Vacant(unasked) = self.snapshots.entry(id.to_owned()) else {
                continue;
            };
            let asked = daemon.send(&GetScreenSnapshotMessage {
                id: id.to_owned(),
                ..GetScreenSnapshotMessage::new()
            });
            if asked.is_ok() {
                unasked.insert(Snapshot::Asked);
            }
        }
    }

    pub(super) fn snapshot_arrived(&mut self, result: &GetScreenSnapshotResultMessage) {
        let Some(snapshot) = self.snapshots.get_mut(&result.id) else {
            return;
        };
        *snapshot = replayed(result, &self.theme)
            .map_or(Snapshot::Refused, |grid| Snapshot::Shown(Box::new(grid)));
    }

    /// Whether the window has a picture of terminal `id` to draw small.
    pub(crate) fn pictured(&self, id: &str) -> bool {
        self.picture(id).is_some()
    }

    /// The picture of each of `ids` that has one, by the mark of its miniature.
    pub(crate) fn miniatures<'a>(
        &'a self,
        ids: &'a [String],
    ) -> impl Iterator<Item = (u32, &'a Grid)> {
        ids.iter()
            .filter_map(|id| Some((miniature_mark(id), self.picture(id)?)))
    }

    fn live_grid(&self, id: &str) -> Option<&Grid> {
        let term = self.panes.get(id)?;
        term.tile.is_some().then_some(&term.grid)
    }

    fn picture(&self, id: &str) -> Option<&Grid> {
        let last_shown = || {
            let held = self.panes.get(id).filter(|t| t.holding_hidden_screen);
            held.map(|t| &t.grid).or(self.hidden_screens.get(id))
        };
        self.live_grid(id)
            .or(match self.snapshots.get(id) {
                Some(Snapshot::Shown(grid)) => Some(grid),
                _ => last_shown(),
            })
            .filter(|grid| grid.cols > 0)
    }
}

/// The screen the daemon's replay draws; None when it has none to draw.
fn replayed(result: &GetScreenSnapshotResultMessage, theme: &TerminalColors) -> Option<Grid> {
    let replay = result
        .screen_snapshot
        .as_deref()
        .filter(|s| !s.is_empty())?;
    let (cols, rows) = (result.screen_cols?, result.screen_rows?);
    if !result.success || cols < 1 || rows < 1 {
        return None;
    }
    let mut vt = themed(Vt::new(clamp(cols), clamp(rows)), theme);
    vt.write(&base64_decode(replay)?);
    let mut grid = Grid::default();
    vt.sync(&mut grid);
    Some(grid)
}
