use attn_client::Facts;
use attn_protocol::{
    Desktop, DesktopCloseMessage, DesktopCloseTileMessage, DesktopCreateMessage,
    DesktopMoveLeafMessage, DesktopRemoveLeafMessage, DesktopSetCurrentMessage,
    DesktopShowLeafMessage, DesktopShowSessionMessage, SessionPlacement, SpawnSessionMessage,
};
use rune::Tx;

use super::layout::{self, Direction, LeafKind, Node, Toward, View};
use super::{Visit, leaf_ids};
use crate::State;
use crate::action::Action;

/// Runs a desktop or tile action; false when `action` is not one.
pub(crate) fn run(state: &mut State, action: &Action, tx: &mut Tx) -> bool {
    use Action::*;
    match action {
        GoToDesktop { desktop } => set_current(state, desktop),
        OpenTile { desktop, tile } => show_leaf(state, desktop, tile),
        MoveWithDelegates { desktop } => move_with_delegates(state, desktop),
        DesktopSelect1 | DesktopSelect2 | DesktopSelect3 | DesktopSelect4 | DesktopSelect5
        | DesktopSelect6 | DesktopSelect7 | DesktopSelect8 | DesktopSelect9 => {
            select_slot(state, slot_of(action))
        }
        DesktopSend1 | DesktopSend2 | DesktopSend3 | DesktopSend4 | DesktopSend5 | DesktopSend6
        | DesktopSend7 | DesktopSend8 | DesktopSend9 => send_to_slot(state, slot_of(action), true),
        DesktopSendStay1 | DesktopSendStay2 | DesktopSendStay3 | DesktopSendStay4
        | DesktopSendStay5 | DesktopSendStay6 | DesktopSendStay7 | DesktopSendStay8
        | DesktopSendStay9 => send_to_slot(state, slot_of(action), false),
        SessionPrev => step_desktop(state, -1),
        SessionNext => step_desktop(state, 1),
        TerminalFocusLeft => focus_toward(state, Toward::Left),
        TerminalFocusRight => focus_toward(state, Toward::Right),
        TerminalFocusUp => focus_toward(state, Toward::Up),
        TerminalFocusDown => focus_toward(state, Toward::Down),
        TerminalSplitVertical => split(state, Direction::Vertical, tx),
        TerminalSplitHorizontal => split(state, Direction::Horizontal, tx),
        TerminalToggleZoom => toggle_view(state, false),
        TerminalToggleMaximize => toggle_view(state, true),
        SessionClose | TerminalClose => close(state),
        SessionHistoryBack => walk_history(state, -1),
        SessionHistoryForward => walk_history(state, 1),
        ShelfCarryLeft => super::shelf::carry(state, -1, tx),
        ShelfCarryRight => super::shelf::carry(state, 1, tx),
        ShelfSendToShelf => super::shelf::send_to_shelf(state, tx),
        ShelfFewerTiles => super::shelf::step_width(state, true, tx),
        ShelfMoreTiles => super::shelf::step_width(state, false, tx),
        _ => return false,
    }
    if goes_to_a_desktop(action) {
        state.window.view = crate::shell::View::Desktop;
    }
    true
}

/// The actions that take the window to a desktop or a tile, which leave home.
fn goes_to_a_desktop(action: &Action) -> bool {
    use Action::*;
    matches!(
        action,
        GoToDesktop { .. }
            | OpenTile { .. }
            | DesktopSelect1
            | DesktopSelect2
            | DesktopSelect3
            | DesktopSelect4
            | DesktopSelect5
            | DesktopSelect6
            | DesktopSelect7
            | DesktopSelect8
            | DesktopSelect9
            | DesktopSend1
            | DesktopSend2
            | DesktopSend3
            | DesktopSend4
            | DesktopSend5
            | DesktopSend6
            | DesktopSend7
            | DesktopSend8
            | DesktopSend9
            | SessionPrev
            | SessionNext
            | SessionHistoryBack
            | SessionHistoryForward
    )
}

fn slot_of(action: &Action) -> u32 {
    let id = action.id();
    id[id.len() - 1..]
        .parse()
        .expect("a desktop action's id ends in its slot")
}

fn request_id() -> String {
    uuid::Uuid::new_v4().to_string()
}

fn profile(state: &State) -> Option<String> {
    let selected = &state.facts.profiles.selected;
    (!selected.is_empty()).then(|| selected.clone())
}

/// The id the daemon gives the desktop on a shortcut slot.
fn slot_desktop_id(profile: &str, slot: u32) -> String {
    format!("{profile}/desktop_{slot}")
}

fn set_current(state: &State, desktop: &str) {
    let Some(profile_id) = profile(state) else {
        return;
    };
    let _ = state.daemon.send(&DesktopSetCurrentMessage {
        request_id: request_id(),
        profile_id,
        desktop_id: desktop.to_owned(),
        ..DesktopSetCurrentMessage::new()
    });
}

/// Goes to the desktop on `slot`, creating it when missing. The current desktop's own digit
/// goes back to the desktop shown before it.
fn select_slot(state: &mut State, slot: u32) {
    let Some(profile_id) = profile(state) else {
        return;
    };
    let facts = &state.facts;
    let Some(target) = facts
        .desktops
        .iter()
        .find(|d| d.shortcut_slot == Some(slot as i64))
    else {
        let _ = state.daemon.send(&DesktopCreateMessage {
            request_id: request_id(),
            profile_id: profile_id.clone(),
            shortcut_slot: Some(slot as i64),
            ..DesktopCreateMessage::new()
        });
        set_current(state, &slot_desktop_id(&profile_id, slot));
        return;
    };
    let current = facts.current_desktop().map(|d| d.id.as_str());
    let previous = state.window.desk.previous_desktop.as_deref();
    let to = match previous {
        Some(previous) if current == Some(&target.id) => previous.to_owned(),
        _ => target.id.clone(),
    };
    set_current(state, &to);
}

/// Moves the active tile to the desktop on `slot`, creating it when missing, and shows it
/// there when `follow`.
fn send_to_slot(state: &mut State, slot: u32, follow: bool) {
    let Some(profile_id) = profile(state) else {
        return;
    };
    let Some(source) = state.facts.current_desktop() else {
        return;
    };
    if source.shortcut_slot == Some(slot as i64) || source.active_pane_id.is_empty() {
        return;
    }
    let _ = state.daemon.send(&DesktopMoveLeafMessage {
        request_id: request_id(),
        source_desktop_id: source.id.clone(),
        target_shortcut_slot: Some(slot as i64),
        leaf_id: source.active_pane_id.clone(),
        ..DesktopMoveLeafMessage::new()
    });
    if follow {
        set_current(state, &slot_desktop_id(&profile_id, slot));
    }
}

fn ordered_desktops(state: &State) -> Vec<&Desktop> {
    let mut desktops: Vec<&Desktop> = state.facts.desktops.iter().collect();
    desktops.sort_by(|a, b| a.order_key.cmp(&b.order_key));
    desktops
}

/// Moves the active tile and its delegates to `desktop`, without following them.
fn move_with_delegates(state: &State, desktop: &str) {
    let Some(source) = state.facts.current_desktop() else {
        return;
    };
    if source.active_pane_id.is_empty() || source.id == desktop {
        return;
    }
    let _ = state.daemon.send(&DesktopMoveLeafMessage {
        request_id: request_id(),
        source_desktop_id: source.id.clone(),
        target_desktop_id: Some(desktop.to_owned()),
        leaf_id: source.active_pane_id.clone(),
        with_delegates: Some(true),
        ..DesktopMoveLeafMessage::new()
    });
}

/// Steps to the next or previous desktop in the profile's order, round the end.
fn step_desktop(state: &mut State, step: i32) {
    let desktops = ordered_desktops(state);
    let Some(current) = state.facts.current_desktop() else {
        return;
    };
    let n = desktops.len() as i32;
    let Some(at) = desktops.iter().position(|d| d.id == current.id) else {
        return;
    };
    let to = desktops[((at as i32 + step + n) % n) as usize].id.clone();
    set_current(state, &to);
}

/// Shows `leaf` on `desktop` as the active tile.
pub(crate) fn show_leaf(state: &State, desktop: &str, leaf: &str) {
    let _ = state.daemon.send(&DesktopShowLeafMessage {
        request_id: request_id(),
        desktop_id: desktop.to_owned(),
        leaf_id: leaf.to_owned(),
        ..DesktopShowLeafMessage::new()
    });
}

/// A tile the window took: it becomes the active tile of the current desktop.
pub(crate) fn activate(state: &State, desktop: &str, leaf: &str) {
    let current = state.facts.current_desktop();
    let already = current.is_some_and(|d| d.id == desktop && d.active_pane_id == leaf);
    if !already {
        show_leaf(state, desktop, leaf);
    }
}

/// Moves the active tile to its neighbour on screen: along the row by columns for left and
/// right, by geometry within a column and for up and down. Past the edge it leaves for the
/// neighbouring desktop.
fn focus_toward(state: &mut State, toward: Toward) {
    let edge = match toward {
        Toward::Left | Toward::Up => -1,
        Toward::Right | Toward::Down => 1,
    };
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let desk = &state.window.desk;
    let Some((tree, plan)) = desk.plan(&state.facts, desktop) else {
        return step_desktop(state, edge);
    };
    let active = desktop.active_pane_id.as_str();
    let row_state = desk.row(&desktop.id);
    let sideways = matches!(toward, Toward::Left | Toward::Right);
    let next = if sideways && !matches!(row_state.view, View::Focused(_)) {
        let row: Vec<_> = plan
            .before
            .iter()
            .chain(&plan.screen)
            .chain(&plan.after)
            .filter(|c| c.holds(active) || !c.leaf_ids.iter().any(|id| row_state.held.contains(id)))
            .collect();
        let at = row.iter().position(|c| c.holds(active));
        at.and_then(|at| row[at].node.leaf_toward(active, toward))
            .or_else(|| {
                let at = at? as i32 + edge;
                let column = row.get(usize::try_from(at).ok()?)?;
                Some(column.last_used(&row_state.focus_order).to_owned())
            })
    } else {
        plan.rendered(&tree, &row_state.view, active)
            .leaf_toward(active, toward)
    };
    match next {
        Some(leaf) => show_leaf(state, &desktop.id, &leaf),
        None => step_desktop(state, edge),
    }
}

/// Opens a shell beside or below the active terminal tile, in its session's folder. A
/// column already as tall as its tiles allow gets the shell beside instead.
fn split(state: &mut State, direction: Direction, tx: &mut Tx) {
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let Some(pane) = super::view::pane_of(desktop, &desktop.active_pane_id) else {
        return;
    };
    let Some(base) = state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == pane.session_id)
    else {
        return;
    };
    let mut direction = direction;
    if direction == Direction::Horizontal
        && let (Some(tree), Some(area)) = (Node::parse(&desktop.tree_json), state.window.desk.area)
        && let Some(message) = layout::stack_limit_message(&tree, &pane.pane_id, area.h)
    {
        state.window.notify(message, tx);
        direction = Direction::Vertical;
    }
    let Some(profile_id) = profile(state) else {
        return;
    };
    let id = uuid::Uuid::new_v4().to_string();
    let _ = state.daemon.send(&SpawnSessionMessage {
        id: id.clone(),
        cwd: base.directory.clone(),
        host: base.host.clone().filter(|h| !h.is_empty()),
        placement: Some(SessionPlacement {
            desktop_id: Some(desktop.id.clone()),
            direction: Some(
                match direction {
                    Direction::Vertical => "vertical",
                    Direction::Horizontal => "horizontal",
                }
                .into(),
            ),
            anchor_pane_id: Some(pane.pane_id.clone()),
        }),
        agent: "shell".into(),
        cols: 80,
        rows: 24,
        spawned_from: Some(base.id.clone()),
        profile_id,
        ..SpawnSessionMessage::new()
    });
    let _ = state.daemon.send(&DesktopShowSessionMessage {
        request_id: request_id(),
        session_id: id,
        ..DesktopShowSessionMessage::new()
    });
}

/// Zoom gives the active tile most of the room and follows the active tile; focus shows the
/// active tile alone. Each turns the other off.
fn toggle_view(state: &mut State, focus: bool) {
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let active = desktop.active_pane_id.clone();
    if active.is_empty() {
        return;
    }
    let row = state.window.desk.row_mut(&desktop.id);
    row.view = match (&row.view, focus) {
        (View::Focused(_), true) | (View::Zoomed, false) => View::Normal,
        (_, true) => View::Focused(active),
        (_, false) => View::Zoomed,
    };
}

/// Escape ends a drag, closes a shelf cap's menu, or takes the current desktop out of focus
/// mode, before anything else hears it; false when there is none of those to end.
pub(crate) fn escape(state: &mut State) -> bool {
    let Some(desktop) = state.facts.current_desktop() else {
        return false;
    };
    if state.window.desk.drag.take().is_some() || state.window.desk.cap_open.take().is_some() {
        return true;
    }
    let row = state.window.desk.row_mut(&desktop.id);
    if !matches!(row.view, View::Focused(_)) {
        return false;
    }
    row.view = View::Normal;
    true
}

/// Closes the active tile: a document or browser tile leaves the desktop, a terminal tile
/// ends its terminal, and on an empty desktop the desktop closes.
fn close(state: &mut State) {
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let tree = Node::parse(&desktop.tree_json);
    let Some(tree) = tree else {
        let _ = state.daemon.send(&DesktopCloseMessage {
            request_id: request_id(),
            desktop_id: desktop.id.clone(),
            expected_revision: desktop.revision,
            ..DesktopCloseMessage::new()
        });
        return;
    };
    let Some(Node::Leaf(leaf)) = tree.find_leaf(&desktop.active_pane_id) else {
        return;
    };
    match leaf.kind {
        LeafKind::Tile { .. } => {
            let _ = state.daemon.send(&DesktopRemoveLeafMessage {
                request_id: request_id(),
                desktop_id: desktop.id.clone(),
                leaf_id: leaf.id.clone(),
                ..DesktopRemoveLeafMessage::new()
            });
        }
        LeafKind::Pane => {
            let _ = state.daemon.send(&DesktopCloseTileMessage {
                request_id: request_id(),
                desktop_id: desktop.id.clone(),
                tile_id: leaf.id.clone(),
                ..DesktopCloseTileMessage::new()
            });
        }
    }
}

/// Walks back or forward through the tiles this window showed, skipping ones that are gone,
/// and following a tile to the desktop it moved to.
fn walk_history(state: &mut State, step: i32) {
    let facts = &state.facts;
    let history = &mut state.window.desk.history;
    let mut kept: Vec<Visit> = Vec::new();
    let mut shown = 0;
    for (i, mut visit) in history.entries.drain(..).enumerate() {
        if follow_visit(facts, &mut visit) && kept.last() != Some(&visit) {
            kept.push(visit);
        }
        if i == history.at {
            shown = kept.len().saturating_sub(1);
        }
    }
    history.entries = kept;
    history.at = shown;
    let len = history.entries.len();
    let Some(at) = shown
        .checked_add_signed(step as isize)
        .filter(|at| *at < len)
    else {
        return;
    };
    history.at = at;
    let Visit { desktop, leaf } = history.entries[at].clone();
    show_leaf(state, &desktop, &leaf);
}

/// Keeps a visit whose tile is still on its desktop, follows one that moved to one other
/// desktop, and drops the rest.
fn follow_visit(facts: &Facts, visit: &mut Visit) -> bool {
    let holds = |d: &&Desktop| leaf_ids(d).contains(&visit.leaf);
    if facts
        .desktops
        .iter()
        .find(|d| d.id == visit.desktop)
        .is_some_and(|d| holds(&d))
    {
        return true;
    }
    let mut holders = facts.desktops.iter().filter(holds);
    match (holders.next(), holders.next()) {
        (Some(desktop), None) => {
            visit.desktop = desktop.id.clone();
            true
        }
        _ => false,
    }
}
