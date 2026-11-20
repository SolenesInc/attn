use rune::{El, row};

use super::door;

/// Which side of a button meets the button it is joined to.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Join {
    /// The button after it.
    Next,
    /// The button before it.
    Previous,
}

/// An action and the button that opens a menu of its other ways, side by side as one control:
/// `action` built [`Join::Next`], then `more` built [`Join::Previous`], sharing one edge.
pub fn split(action: impl Into<El>, more: impl Into<El>) -> El {
    row()
        .items_center()
        .min_w(0.0)
        .shrink()
        .child(action.into())
        .child(door::over_previous_edge(more.into()))
}
