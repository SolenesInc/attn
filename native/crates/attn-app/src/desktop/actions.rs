use attn_client::Facts;
use attn_protocol::{
    Desktop, DesktopCloseMessage, DesktopCloseTileMessage, DesktopCreateMessage,
    DesktopMoveLeafMessage, DesktopRemoveLeafMessage, DesktopSetCurrentMessage,
    DesktopShowLeafMessage, SessionPlacement, SpawnSessionMessage,
};
use rune::Tx;

use super::layout::{self, Direction, LeafKind, Node, Toward, View};
use super::{Visit, leaf_ids};
use crate::State;
use crate::action::Action;
use crate::requests::Refused;

/// Runs a desktop or tile action; false when `action` is not one.
pub(crate) fn run(state: &mut State, action: &Action, tx: &mut Tx) -> bool {
    use Action::*;
    match action {
        GoToDesktop { desktop } => set_current(state, desktop, Refused::Quiet, tx),
        OpenTile { desktop, tile } => show_leaf(state, desktop, tile, tx),
        ClearDraggedWidths { desktop } => super::shelf::clear_dragged_widths(state, desktop, tx),
        MoveWithDelegates { desktop } => move_with_delegates(state, desktop, tx),
        DesktopSelect1 | DesktopSelect2 | DesktopSelect3 | DesktopSelect4 | DesktopSelect5
        | DesktopSelect6 | DesktopSelect7 | DesktopSelect8 | DesktopSelect9 => {
            select_slot(state, slot_of(action), tx)
        }
        DesktopSend1 | DesktopSend2 | DesktopSend3 | DesktopSend4 | DesktopSend5 | DesktopSend6
        | DesktopSend7 | DesktopSend8 | DesktopSend9 => {
            send_to_slot(state, slot_of(action), true, tx)
        }
        DesktopSendStay1 | DesktopSendStay2 | DesktopSendStay3 | DesktopSendStay4
        | DesktopSendStay5 | DesktopSendStay6 | DesktopSendStay7 | DesktopSendStay8
        | DesktopSendStay9 => send_to_slot(state, slot_of(action), false, tx),
        SessionPrev => step_desktop(state, -1, tx),
        SessionNext => step_desktop(state, 1, tx),
        TerminalFocusLeft => focus_toward(state, Toward::Left, tx),
        TerminalFocusRight => focus_toward(state, Toward::Right, tx),
        TerminalFocusUp => focus_toward(state, Toward::Up, tx),
        TerminalFocusDown => focus_toward(state, Toward::Down, tx),
        TerminalSplitVertical => split(state, Direction::Vertical, tx),
        TerminalSplitHorizontal => split(state, Direction::Horizontal, tx),
        TerminalToggleZoom => toggle_view(state, false, tx),
        TerminalToggleMaximize => toggle_view(state, true, tx),
        SessionClose | TerminalClose => close(state, tx),
        SessionHistoryBack => walk_history(state, -1, tx),
        SessionHistoryForward => walk_history(state, 1, tx),
        ShelfCarryLeft => super::shelf::carry(state, -1, tx),
        ShelfCarryRight => super::shelf::carry(state, 1, tx),
        ShelfSendToShelf => super::shelf::send_to_shelf(state, tx),
        ShelfFewerTiles => super::shelf::step_width(state, true, tx),
        ShelfMoreTiles => super::shelf::step_width(state, false, tx),
        DesktopOverview => super::overview::toggle(state, tx),
        TilePopOut => super::windows::toggle(state, tx),
        TerminalFind => find(state, tx),
        TerminalSendAnnotations => {
            crate::annotations::draft::send_key(state, tx);
            return true;
        }
        _ => return false,
    }
    if goes_to_a_desktop(action) {
        state.window.show_desktop(tx);
    }
    true
}

/// Opens find in the terminal holding the keyboard.
fn find(state: &mut State, tx: &mut Tx) {
    if let Some(id) = state.window.keyboard_terminal(&state.facts) {
        state.terminals.open_find(&id, tx);
    }
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

fn profile(state: &State) -> Option<String> {
    let selected = &state.facts.profiles.selected;
    (!selected.is_empty()).then(|| selected.clone())
}

/// The id the daemon gives the desktop on a shortcut slot.
fn slot_desktop_id(profile: &str, slot: u32) -> String {
    format!("{profile}/desktop_{slot}")
}

fn set_current(state: &mut State, desktop: &str, refused: Refused, tx: &mut Tx) {
    let Some(profile_id) = profile(state) else {
        return;
    };
    let desktop_id = desktop.to_owned();
    state.act(
        |request_id| DesktopSetCurrentMessage {
            request_id,
            profile_id,
            desktop_id,
            ..DesktopSetCurrentMessage::new()
        },
        refused,
        |_, _, _| {},
        tx,
    );
}

/// Goes to the desktop on `slot`, creating it when missing. The current desktop's own digit
/// goes back to the desktop shown before it.
fn select_slot(state: &mut State, slot: u32, tx: &mut Tx) {
    let Some(profile_id) = profile(state) else {
        return;
    };
    let facts = &state.facts;
    let Some(target) = facts
        .desktops
        .iter()
        .find(|d| d.shortcut_slot == Some(slot as i64))
    else {
        let creating = state.requests.waits_for(|command| {
            command["cmd"] == "desktop_create" && command["shortcut_slot"] == slot
        });
        if !creating {
            state.act(
                |request_id| DesktopCreateMessage {
                    request_id,
                    profile_id,
                    shortcut_slot: Some(slot as i64),
                    ..DesktopCreateMessage::new()
                },
                Refused::Bare,
                |_, _, _| {},
                tx,
            );
        }
        return;
    };
    let current = facts.current_desktop().map(|d| d.id.as_str());
    let previous = state.window.desk.previous_desktop.as_deref();
    let to = match previous {
        Some(previous) if current == Some(&target.id) => previous.to_owned(),
        _ => target.id.clone(),
    };
    set_current(state, &to, Refused::Bare, tx);
}

/// Moves the active tile to the desktop on `slot`, creating it when missing, and shows it
/// there when `follow`.
fn send_to_slot(state: &mut State, slot: u32, follow: bool, tx: &mut Tx) {
    let Some(profile_id) = profile(state) else {
        return;
    };
    let Some(source) = state.facts.current_desktop() else {
        return;
    };
    let leaf = state.window.desk.acting_leaf(source);
    if source.shortcut_slot == Some(slot as i64) || leaf.is_empty() {
        return;
    }
    let source_desktop_id = source.id.clone();
    let target = slot_desktop_id(&profile_id, slot);
    state.act(
        |request_id| DesktopMoveLeafMessage {
            request_id,
            source_desktop_id,
            target_shortcut_slot: Some(slot as i64),
            leaf_id: leaf,
            ..DesktopMoveLeafMessage::new()
        },
        Refused::Bare,
        move |state, _, tx| {
            if follow {
                set_current(state, &target, Refused::Bare, tx);
            }
        },
        tx,
    );
}

fn ordered_desktops(state: &State) -> Vec<&Desktop> {
    let mut desktops: Vec<&Desktop> = state.facts.desktops.iter().collect();
    desktops.sort_by(|a, b| a.order_key.cmp(&b.order_key));
    desktops
}

/// Moves the active tile and its delegates to `desktop`, without following them.
fn move_with_delegates(state: &mut State, desktop: &str, tx: &mut Tx) {
    let Some(source) = state.facts.current_desktop() else {
        return;
    };
    let leaf = state.window.desk.acting_leaf(source);
    if leaf.is_empty() || source.id == desktop {
        return;
    }
    let source_desktop_id = source.id.clone();
    let target_desktop_id = Some(desktop.to_owned());
    state.act(
        |request_id| DesktopMoveLeafMessage {
            request_id,
            source_desktop_id,
            target_desktop_id,
            leaf_id: leaf,
            with_delegates: Some(true),
            ..DesktopMoveLeafMessage::new()
        },
        Refused::Prefixed("Could not move that pane"),
        |_, _, _| {},
        tx,
    );
}

/// Steps to the next or previous desktop in the profile's order, round the end.
fn step_desktop(state: &mut State, step: i32, tx: &mut Tx) {
    let desktops = ordered_desktops(state);
    let Some(current) = state.facts.current_desktop() else {
        return;
    };
    let n = desktops.len() as i32;
    let Some(at) = desktops.iter().position(|d| d.id == current.id) else {
        return;
    };
    let to = desktops[((at as i32 + step + n) % n) as usize].id.clone();
    set_current(state, &to, Refused::Bare, tx);
}

/// Shows the oldest document that arrived unseen on the current desktop. Returns whether
/// there was one.
pub(crate) fn show_unseen(state: &mut State, tx: &mut Tx) -> bool {
    let Some(desktop) = state.facts.current_desktop().map(|d| d.id.clone()) else {
        return false;
    };
    let Some(leaf) = state.window.desk.row(&desktop).unseen.first().cloned() else {
        return false;
    };
    state.window.show_desktop(tx);
    state.window.follow_next_turn = false;
    show_leaf(state, &desktop, &leaf, tx);
    true
}

/// Shows `leaf` on `desktop` as the active tile, as the person picked it, and gives it the
/// keyboard.
pub(crate) fn show_leaf(state: &mut State, desktop: &str, leaf: &str, tx: &mut Tx) {
    give_keyboard(state, desktop, leaf, tx);
    if state.window.desk.pick(desktop, leaf) {
        tx.instant();
    }
    let (desktop_id, leaf_id) = (desktop.to_owned(), leaf.to_owned());
    state.act(
        |request_id| DesktopShowLeafMessage {
            request_id,
            desktop_id,
            leaf_id,
            ..DesktopShowLeafMessage::new()
        },
        Refused::Prefixed("Could not show that leaf"),
        |_, _, _| {},
        tx,
    );
}

/// A tile the window took: it holds the keyboard and becomes the active tile of the current
/// desktop.
pub(crate) fn activate(state: &mut State, desktop: &str, leaf: &str, tx: &mut Tx) {
    let current = state.facts.current_desktop();
    let already = current.is_some_and(|d| d.id == desktop && d.active_pane_id == leaf);
    if already {
        give_keyboard(state, desktop, leaf, tx);
    } else {
        show_leaf(state, desktop, leaf, tx);
    }
}

pub(crate) fn give_keyboard(state: &mut State, desktop: &str, leaf: &str, tx: &mut Tx) {
    state.window.take_keyboard(desktop, leaf, tx);
    let keyboard = state.window.keyboard_terminal(&state.facts);
    state.terminals.focus(keyboard, &state.daemon);
}

/// Moves the active tile to its neighbour on screen: along the row by columns for left and
/// right, by geometry within a column and for up and down. Past the edge it leaves for the
/// neighbouring desktop.
fn focus_toward(state: &mut State, toward: Toward, tx: &mut Tx) {
    let edge = match toward {
        Toward::Left | Toward::Up => -1,
        Toward::Right | Toward::Down => 1,
    };
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let desk = &state.window.desk;
    let Some((tree, plan)) = desk.plan(&state.facts, desktop) else {
        return step_desktop(state, edge, tx);
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
    let desktop = desktop.id.clone();
    match next {
        Some(leaf) => show_leaf(state, &desktop, &leaf, tx),
        None => step_desktop(state, edge, tx),
    }
}

/// Opens a shell beside or below the active terminal tile, in its session's folder. A
/// column already as tall as its tiles allow gets the shell beside instead.
fn split(state: &mut State, direction: Direction, tx: &mut Tx) {
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let leaf = state.window.desk.acting_leaf(desktop);
    let desktop = desktop.id.clone();
    split_at(state, &desktop, &leaf, direction, tx);
}

/// Opens a shell beside the terminal `leaf` on `desktop`, in its directory.
pub(super) fn split_at(
    state: &mut State,
    desktop: &str,
    leaf: &str,
    direction: Direction,
    tx: &mut Tx,
) {
    let Some(desktop) = state.facts.desktops.iter().find(|d| d.id == desktop) else {
        return;
    };
    let Some(pane) = super::view::pane_of(desktop, leaf) else {
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
    let spawn = SpawnSessionMessage {
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
    };
    state.act_as(id.clone(), &spawn, Refused::Bare, |_, _, _| {}, tx);
    state.window.desk.split = Some(id);
}

/// The person asked to see `leaf` alone: it takes the keyboard and becomes the active tile,
/// shown alone, with zoom off. Showing a tile alone and returning it land at once, as in the
/// Tauri app.
pub(super) fn focus_leaf(state: &mut State, desktop: &str, leaf: &str, tx: &mut Tx) {
    activate(state, desktop, leaf, tx);
    state.window.desk.row_mut(desktop).view = View::Focused(leaf.to_owned());
    tx.instant();
}

/// The tile `desktop` shows alone goes back to its row.
pub(super) fn return_to_row(state: &mut State, desktop: &str, tx: &mut Tx) {
    state.window.desk.row_mut(desktop).view = View::Normal;
    tx.instant();
}

/// Zoom gives the active tile most of the room and follows the active tile; focus shows the
/// active tile alone. Each turns the other off.
fn toggle_view(state: &mut State, focus: bool, tx: &mut Tx) {
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let active = desktop.active_pane_id.clone();
    if active.is_empty() {
        return;
    }
    let row = state.window.desk.row_mut(&desktop.id);
    let was_alone = matches!(row.view, View::Focused(_));
    row.view = match (&row.view, focus) {
        (View::Focused(_), true) | (View::Zoomed, false) => View::Normal,
        (_, true) => View::Focused(active),
        (_, false) => View::Zoomed,
    };
    if was_alone != matches!(row.view, View::Focused(_)) {
        tx.instant();
    }
}

/// Escape ends a drag, closes a shelf cap's menu, or takes the current desktop out of focus
/// mode, before anything else hears it; false when there is none of those to end.
pub(crate) fn escape(state: &mut State, tx: &mut Tx) -> bool {
    if super::peek::close(state) {
        return true;
    }
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
    tx.instant();
    true
}

/// Closes the active tile, or the one shown alone: a document or browser tile leaves the
/// desktop, a terminal tile ends its terminal, and on an empty desktop the desktop closes.
fn close(state: &mut State, tx: &mut Tx) {
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let desktop_id = desktop.id.clone();
    let Some(tree) = Node::parse(&desktop.tree_json) else {
        let expected_revision = desktop.revision;
        state.act(
            |request_id| DesktopCloseMessage {
                request_id,
                desktop_id,
                expected_revision,
                ..DesktopCloseMessage::new()
            },
            Refused::Bare,
            |_, _, _| {},
            tx,
        );
        return;
    };
    let Some(Node::Leaf(leaf)) = tree.find_leaf(&state.window.desk.acting_leaf(desktop)) else {
        return;
    };
    let (leaf_id, kind) = (leaf.id.clone(), leaf.kind.clone());
    let refused = Refused::Prefixed("Could not close that tile");
    match kind {
        LeafKind::Tile { .. } => state.act(
            |request_id| DesktopRemoveLeafMessage {
                request_id,
                desktop_id,
                leaf_id,
                ..DesktopRemoveLeafMessage::new()
            },
            refused,
            |_, _, _| {},
            tx,
        ),
        LeafKind::Pane => state.act(
            |request_id| DesktopCloseTileMessage {
                request_id,
                desktop_id,
                tile_id: leaf_id,
                ..DesktopCloseTileMessage::new()
            },
            refused,
            |_, _, _| {},
            tx,
        ),
    }
}

/// Walks back or forward through the tiles this window showed, skipping ones that are gone,
/// and following a tile to the desktop it moved to.
fn walk_history(state: &mut State, step: i32, tx: &mut Tx) {
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
    show_leaf(state, &desktop, &leaf, tx);
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
