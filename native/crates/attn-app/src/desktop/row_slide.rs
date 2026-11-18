//! The row slide of the Tauri app's `useRowMotion`: when the screen moves along the same row,
//! tiles leaving the screen travel out with it and tiles joining it travel in, rather than
//! shrinking and growing in place.

use std::hash::{Hash, Hasher};

use attn_client::layout::RowPlan;
use rune::{El, Spring, SpringPair, Transition, Tx};

use crate::State;

use super::layout::Placement;

/// The row the tile area last laid out: which columns the row holds, where the screen starts
/// in it, how wide the area is, and where each tile on screen lies.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct ShownRow {
    row: String,
    first_column: usize,
    width: f32,
    leaves: Vec<(String, [f32; 4])>,
}

impl ShownRow {
    pub(crate) fn of(plan: &RowPlan, placed: &Placement, width: f32) -> ShownRow {
        let row = plan
            .before
            .iter()
            .chain(&plan.screen)
            .chain(&plan.after)
            .map(|column| column.leaf_ids.join("+"))
            .collect::<Vec<_>>()
            .join("|");
        ShownRow {
            row,
            first_column: plan.first_column.index,
            width,
            leaves: placed.leaves.clone(),
        }
    }

    pub(crate) fn shows(&self, leaf: &str) -> bool {
        self.leaves.iter().any(|(id, _)| id == leaf)
    }

    /// How far, in points, the screen's contents move going from `self` to `next`: as far as
    /// the first tile on both screens moves, or the area's width toward the side the screen
    /// moved from when none stays. Nothing when the row's columns or the area's width changed.
    pub(crate) fn slide_to(&self, next: &ShownRow) -> f32 {
        if self.row != next.row || self.width != next.width {
            return 0.0;
        }
        let stays = next.leaves.iter().find_map(|(id, [x, ..])| {
            let (_, [was, ..]) = self.leaves.iter().find(|(earlier, _)| earlier == id)?;
            Some(x - was)
        });
        stays.unwrap_or_else(|| match self.first_column.cmp(&next.first_column) {
            std::cmp::Ordering::Less => -next.width,
            std::cmp::Ordering::Equal => 0.0,
            std::cmp::Ordering::Greater => next.width,
        })
    }

    /// An empty element keyed by this row, whose first layout records it as the row the tile
    /// area shows: each row laid out mounts its own, since the area itself keeps its place.
    pub(super) fn marker(self) -> El {
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        self.hash(&mut hasher);
        rune::el()
            .key(format!("tiles.row.{:016x}", hasher.finish()))
            .w(0.0)
            .h(0.0)
            .transition(Transition::none())
            .on_layout_tx(move |state: &mut State, _, tx: &mut Tx| {
                let desk = &mut state.window.desk;
                if desk.shown_row.as_ref() == Some(&self) {
                    tx.live_only();
                    return;
                }
                desk.shown_row = Some(self.clone());
            })
    }
}

impl Hash for ShownRow {
    fn hash<H: Hasher>(&self, state: &mut H) {
        self.row.hash(state);
        self.first_column.hash(state);
        self.width.to_bits().hash(state);
        for (id, rect) in &self.leaves {
            id.hash(state);
            rect.map(f32::to_bits).hash(state);
        }
    }
}

/// How a tile leaves a screen sliding by `slide`: out with the row, whole, on the row's spring.
pub(super) fn leaving(slide: f32, spring: Spring) -> Transition {
    Transition::offset(slide, 0.0)
        .leaves_opaque()
        .springs(SpringPair {
            enter: spring,
            exit: spring,
        })
}

/// How a tile joins a screen sliding by `slide`: in from where the row brings it, whole, on the
/// row's spring; it leaves later as `exit` says.
pub(super) fn joining(slide: f32, spring: Spring, exit: Transition) -> Transition {
    Transition::asymmetric(Transition::offset(-slide, 0.0), exit).springs(SpringPair {
        enter: spring,
        exit: exit.springs.exit,
    })
}
