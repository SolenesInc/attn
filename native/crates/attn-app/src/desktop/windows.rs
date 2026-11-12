//! Tiles in windows of their own: a popped-out leaf leaves its desktop's columns for a window
//! where the daemon's `window` frame says.

use attn_protocol::{DesktopBringBackLeafMessage, DesktopPopOutLeafMessage, TileWindowFrame};

use super::layout::Node;
use crate::State;

/// The smallest window a tile pops out into, in points.
const MIN_WIDTH: f64 = 320.0;
const MIN_HEIGHT: f64 = 200.0;

/// Pops the current desktop's active tile out, or brings it back when it is out.
pub(crate) fn toggle(state: &mut State) {
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let (desktop, leaf) = (desktop.id.clone(), desktop.active_pane_id.clone());
    toggle_leaf(state, &desktop, &leaf);
}

pub(crate) fn toggle_leaf(state: &mut State, desktop: &str, leaf: &str) {
    if is_popped(state, desktop, leaf) {
        bring_back(state, desktop, leaf);
    } else {
        pop_out(state, desktop, leaf);
    }
}

fn is_popped(state: &State, desktop: &str, leaf: &str) -> bool {
    state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == desktop)
        .and_then(|d| Node::parse(&d.tree_json))
        .is_some_and(|tree| tree.popped().iter().any(|l| l.id == leaf))
}

/// Asks the daemon to pop `leaf` out into a window over where the tile shows now.
fn pop_out(state: &State, desktop: &str, leaf: &str) {
    let Some(&[x, y, w, h]) = state.window.desk.rects.get(&format!("tile.{leaf}")) else {
        return;
    };
    let (left, top) = crate::frame::main_content_origin();
    let _ = state.daemon.send(&DesktopPopOutLeafMessage {
        request_id: super::request_id(),
        desktop_id: desktop.to_owned(),
        leaf_id: leaf.to_owned(),
        frame: TileWindowFrame {
            x: left + f64::from(x),
            y: top + f64::from(y),
            width: f64::from(w).max(MIN_WIDTH),
            height: f64::from(h).max(MIN_HEIGHT),
        },
        ..DesktopPopOutLeafMessage::new()
    });
}

pub(crate) fn bring_back(state: &State, desktop: &str, leaf: &str) {
    let _ = state.daemon.send(&DesktopBringBackLeafMessage {
        request_id: super::request_id(),
        desktop_id: desktop.to_owned(),
        leaf_id: leaf.to_owned(),
        ..DesktopBringBackLeafMessage::new()
    });
}
