//! The overview: the current desktop's whole row drawn small over the tile area, with a strip per
//! other desktop, to walk with a highlight of its own and land on a tile.

use std::collections::HashSet;

use attn_client::Desktop;
use attn_protocol::{DesktopCreateMessage, DesktopMoveLeafMessage, ProfileActionResultMessage};
use rune::{Key, KeyEvent, Mods, Platform, Tx};

use super::layout::{self, Column, Toward};
use crate::State;
use crate::action::Action;
use crate::requests::Refused;
use crate::shell::Layer;

pub(crate) const LAYER: &str = "overview";
const HOLD_OPENS_AFTER: f64 = 0.2;

/// The open overview. It counts only while `Layer::Overview` is the window's layer, and the next
/// opening replaces it.
#[derive(Clone, Debug)]
pub struct Overview {
    /// Where the window was when the overview opened.
    from: Spot,
    /// The highlight, which no tile, row or history follows until it lands.
    at: Spot,
    /// The leaves the highlight used last, newest first, after the window's own order.
    order: Vec<String>,
    /// The shown row's leaves by column as they last stood, to find a closed tile's neighbour.
    seen: Vec<Vec<String>>,
    mode: Mode,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Mode {
    Held { chord: Mods },
    Kept,
}

#[derive(Clone, Debug)]
pub(crate) struct Hold {
    chord: Mods,
    from: Spot,
    at: Spot,
    opens_at: f64,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Spot {
    pub desktop: String,
    /// None on an empty desktop.
    pub leaf: Option<String>,
}

impl Overview {
    pub(crate) fn at(&self) -> &Spot {
        &self.at
    }

    pub(super) fn held_by(&self) -> Option<Mods> {
        match self.mode {
            Mode::Held { chord } => Some(chord),
            Mode::Kept => None,
        }
    }
}

/// The overview, while its layer is the window's.
pub(crate) fn open(state: &State) -> Option<&Overview> {
    (state.window.layer == Some(Layer::Overview))
        .then_some(state.window.overview.as_ref())
        .flatten()
}

pub(super) fn ordered(state: &State) -> Vec<&Desktop> {
    let mut desktops: Vec<&Desktop> = state.facts.desktops.iter().collect();
    desktops.sort_by(|a, b| a.order_key.cmp(&b.order_key));
    desktops
}

pub(super) fn desktop_of<'s>(state: &'s State, id: &str) -> Option<&'s Desktop> {
    state.facts.desktops.iter().find(|d| d.id == id)
}

/// Every column of `desktop`'s row in the order its entries show them, and which of them are on
/// screen.
pub(super) fn row_of(state: &State, desktop: &Desktop) -> (Vec<Column>, std::ops::Range<usize>) {
    let Some((_, plan)) = state.window.desk.plan(&state.facts, desktop) else {
        return (Vec::new(), 0..0);
    };
    let on_screen = plan.before.len()..plan.before.len() + plan.screen.len();
    let columns = plan
        .before
        .into_iter()
        .chain(plan.screen)
        .chain(plan.after)
        .collect();
    (columns, on_screen)
}

fn leaves_of(state: &State, desktop: &str) -> Vec<Vec<String>> {
    desktop_of(state, desktop).map_or_else(Vec::new, |d| {
        row_of(state, d).0.into_iter().map(|c| c.leaf_ids).collect()
    })
}

/// Where the highlight starts on `desktop`: the tile its keys act on, else its row's first.
fn spot_on(state: &State, desktop: &str) -> Spot {
    let columns = leaves_of(state, desktop);
    let acting = desktop_of(state, desktop).map(|d| state.window.desk.acting_leaf(d));
    let leaf = acting
        .filter(|leaf| columns.iter().flatten().any(|l| l == leaf))
        .or_else(|| columns.into_iter().flatten().next());
    Spot {
        desktop: desktop.to_owned(),
        leaf,
    }
}

/// Opens the overview on the current desktop, or closes it.
pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if open(state).is_some() {
        dismiss(state, tx);
    } else {
        show(state, tx);
    }
}

/// Opens the overview on the current desktop.
pub(crate) fn show(state: &mut State, tx: &mut Tx) {
    if open(state).is_some() {
        return;
    }
    let current = state.facts.current_desktop().map(|d| d.id.clone());
    let Some(desktop) = current.or_else(|| ordered(state).first().map(|d| d.id.clone())) else {
        return;
    };
    let from = spot_on(state, &desktop);
    open_at(state, from.clone(), from, Mode::Kept);
    tx.focus(LAYER, false);
}

fn open_at(state: &mut State, from: Spot, at: Spot, mode: Mode) {
    let desktop = at.desktop.clone();
    state.window.overview = Some(Overview {
        from,
        at,
        order: state.window.desk.row(&desktop).focus_order,
        seen: leaves_of(state, &desktop),
        mode,
    });
    state.window.layer = Some(Layer::Overview);
    state.terminals.forget_snapshots();
    ask_snapshots(state, &desktop);
}

pub(crate) fn chord(state: &mut State, toward: Toward, tx: &mut Tx) {
    if let Some(hold) = state.window.desk.hold.take()
        && open_held_if_in_front(state, hold)
    {
        step(state, toward, tx);
        return;
    }
    let chord = tx.mods();
    let arms = chord != Mods::default() && !tx.key_repeat() && open(state).is_none();
    let from = state.facts.current_desktop().map(|desktop| Spot {
        desktop: desktop.id.clone(),
        leaf: Some(state.window.desk.acting_leaf(desktop)),
    });
    let stepped = super::actions::focus_toward(state, toward, tx);
    if let (true, Some(from), Some(leaf)) = (arms, from, stepped) {
        let at = Spot {
            desktop: from.desktop.clone(),
            leaf: Some(leaf),
        };
        let opens_at = tx.clock() + HOLD_OPENS_AFTER;
        state.window.desk.hold = Some(Hold {
            chord,
            from,
            at,
            opens_at,
        });
        tx.wake_at(opens_at);
    }
}

pub(super) fn expire(state: &mut State, now: f64) -> bool {
    let hold = &mut state.window.desk.hold;
    match hold.take_if(|hold| now >= hold.opens_at) {
        Some(hold) => open_held_if_in_front(state, hold),
        None => false,
    }
}

fn open_held_if_in_front(state: &mut State, hold: Hold) -> bool {
    let desktop = state.facts.current_desktop().map(|d| d.id.clone());
    let there = desktop.map(|desktop| spot_on(state, &desktop));
    if there.as_ref() != Some(&hold.at) || !state.window.tiles_in_front() {
        return false;
    }
    let leaf = hold.at.leaf.clone();
    let mode = Mode::Held { chord: hold.chord };
    open_at(state, hold.from, hold.at, mode);
    if let Some(leaf) = leaf {
        highlight(state, &leaf);
    }
    true
}

pub(crate) fn on_modifiers(state: &mut State, mods: Mods, tx: &mut Tx) -> bool {
    let let_go = |chord: Mods| !crate::keys::covers(mods, chord);
    state.window.desk.hold.take_if(|hold| let_go(hold.chord));
    let lands = open(state)
        .filter(|overview| overview.held_by().is_some_and(let_go))
        .map(|overview| overview.at.clone());
    let landed = lands.is_some();
    if let Some(at) = lands {
        land(state, at, tx);
    }
    landed
}

pub(super) fn dismiss(state: &mut State, tx: &mut Tx) {
    let back = open(state)
        .filter(|overview| overview.held_by().is_some())
        .map(|overview| overview.from.clone());
    match back {
        Some(from) => land(state, from, tx),
        None => close(state, tx),
    }
}

/// Closes the overview and leaves the window as it was.
fn close(state: &mut State, tx: &mut Tx) {
    if state.window.close_overview() {
        tx.blur();
    }
    crate::picker::refocus_inline(state, tx);
}

fn ask_snapshots(state: &mut State, desktop: &str) {
    let runtimes: Vec<String> = desktop_of(state, desktop).map_or_else(Vec::new, |d| {
        d.panes.iter().map(|p| p.runtime_id.clone()).collect()
    });
    state
        .terminals
        .ask_snapshots(runtimes.iter().map(String::as_str), &state.daemon);
}

/// Moves the highlight as `layout::step` moves the active tile, over the plain columns. Past the
/// end of a column a kept overview previews the desktop above or below and a held one stays; past
/// an end of the row both stay.
pub(super) fn step(state: &mut State, toward: Toward, tx: &mut Tx) -> Option<String> {
    let overview = open(state)?;
    let desktop = desktop_of(state, &overview.at.desktop)?;
    let (columns, _) = row_of(state, desktop);
    let row: Vec<&Column> = columns.iter().collect();
    let next = overview
        .at
        .leaf
        .as_deref()
        .and_then(|leaf| layout::step(&row, leaf, toward, &overview.order));
    let beyond = match toward {
        _ if overview.held_by().is_some() => 0,
        Toward::Up => -1,
        Toward::Down => 1,
        Toward::Left | Toward::Right => 0,
    };
    match &next {
        Some(leaf) => highlight(state, leaf),
        None if beyond != 0 => {
            let desktops = ordered(state);
            let shown = &open(state)?.at.desktop;
            let to = desktops
                .iter()
                .position(|d| d.id == *shown)
                .and_then(|i| i.checked_add_signed(beyond))
                .and_then(|i| desktops.get(i))
                .map(|d| d.id.clone());
            if let Some(to) = to {
                preview(state, &to, tx);
            }
        }
        None => {}
    }
    next
}

fn highlight(state: &mut State, leaf: &str) {
    if let Some(overview) = &mut state.window.overview {
        overview.order.retain(|l| l != leaf);
        overview.order.insert(0, leaf.to_owned());
        overview.at.leaf = Some(leaf.to_owned());
    }
}

/// Shows `desktop`'s row in the overview.
pub(super) fn preview(state: &mut State, desktop: &str, _: &mut Tx) {
    let at = spot_on(state, desktop);
    let used = state.window.desk.row(desktop).focus_order;
    let seen = leaves_of(state, desktop);
    let Some(overview) = &mut state.window.overview else {
        return;
    };
    let unused: Vec<String> = used
        .into_iter()
        .filter(|leaf| !overview.order.contains(leaf))
        .collect();
    overview.order.extend(unused);
    overview.at = at;
    overview.seen = seen;
    super::peek::leave_row(state);
    ask_snapshots(state, desktop);
}

/// Closes the overview and shows `spot`'s tile as a click on it does, or its empty desktop.
pub(super) fn land(state: &mut State, spot: Spot, tx: &mut Tx) {
    close(state, tx);
    match spot.leaf {
        Some(tile) => {
            let desktop = spot.desktop;
            crate::dispatch(state, Action::OpenTile { desktop, tile }, tx);
        }
        None => crate::sidebar::open_desktop(state, &spot.desktop, tx),
    }
}

/// The tile the user came from, when the shown desktop can take it.
fn sendable(overview: &Overview) -> Option<&str> {
    (overview.from.desktop != overview.at.desktop)
        .then_some(overview.from.leaf.as_deref())
        .flatten()
}

/// Sends the tile the user came from to the desktop the overview shows.
fn send(state: &mut State, tx: &mut Tx) {
    let Some(overview) = open(state) else {
        return;
    };
    let Some(leaf) = sendable(overview).map(str::to_owned) else {
        return;
    };
    let message = DesktopMoveLeafMessage {
        source_desktop_id: overview.from.desktop.clone(),
        target_desktop_id: Some(overview.at.desktop.clone()),
        leaf_id: leaf,
        ..DesktopMoveLeafMessage::new()
    };
    close(state, tx);
    state.act(
        move |id| DesktopMoveLeafMessage {
            request_id: id,
            ..message
        },
        Refused::Prefixed("Could not move that pane"),
        |_, _, _| {},
        tx,
    );
}

/// Makes a desktop and switches to it once the daemon made it.
pub(crate) fn create(state: &mut State, tx: &mut Tx) {
    let profile = state.facts.profiles.selected.clone();
    if profile.is_empty() {
        return;
    }
    close(state, tx);
    state.act(
        |id| DesktopCreateMessage {
            request_id: id,
            profile_id: profile,
            ..DesktopCreateMessage::new()
        },
        Refused::Prefixed("Could not create a desktop"),
        |state: &mut State, reply, tx: &mut Tx| {
            let made = reply
                .parse::<ProfileActionResultMessage>()
                .and_then(|r| r.desktops?.into_iter().next());
            if let Some(desktop) = made {
                crate::sidebar::open_desktop(state, &desktop.id, tx);
            }
        },
        tx,
    );
}

pub(crate) fn held_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let cancels = ev.key == Key::Escape
        && Platform::current() != Platform::Mac
        && open(state).is_some_and(|overview| overview.held_by().is_some());
    if cancels {
        dismiss(state, tx);
    }
    cancels
}

/// A key the open overview takes: the arrows walk, Return lands, Shift-Return sends the tile the
/// user came from to the shown desktop, a digit goes to its desktop and Escape closes. Escape on
/// Linux reaches a held overview too, with its chord down.
pub(crate) fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if held_key(state, ev, tx) {
        return true;
    }
    let Some(overview) = open(state) else {
        return false;
    };
    if ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    let toward = match ev.key {
        Key::Left => Some(Toward::Left),
        Key::Right => Some(Toward::Right),
        Key::Up => Some(Toward::Up),
        Key::Down => Some(Toward::Down),
        _ => None,
    };
    if let Some(toward) = toward {
        step(state, toward, tx);
        return true;
    }
    match &ev.key {
        Key::Escape => dismiss(state, tx),
        Key::Enter if ev.mods.shift => send(state, tx),
        Key::Enter => {
            let at = overview.at.clone();
            land(state, at, tx);
        }
        Key::Text(typed) => {
            let Some(slot) = typed.parse::<i64>().ok().filter(|n| (1..=9).contains(n)) else {
                return false;
            };
            let on_slot = |d: &&Desktop| d.shortcut_slot == Some(slot);
            let Some(desktop) = state.facts.desktops.iter().find(on_slot) else {
                return true;
            };
            let desktop = desktop.id.clone();
            close(state, tx);
            crate::sidebar::open_desktop(state, &desktop, tx);
        }
        _ => return false,
    }
    true
}

/// Keeps the open overview in step with the daemon's desktops, and lets go of a closed one.
pub(crate) fn follow(state: &mut State) {
    if open(state).is_none() {
        state.window.overview = None;
        state.terminals.forget_snapshots();
        return;
    }
    state.terminals.follow_snapshots(&state.facts);
    let Some(mut overview) = state.window.overview.take() else {
        return;
    };
    if desktop_of(state, &overview.from.desktop).is_none() {
        let current = state.facts.current_desktop().map(|d| d.id.clone());
        let Some(home) = current.or_else(|| ordered(state).first().map(|d| d.id.clone())) else {
            state.window.close_overview();
            return;
        };
        overview.from = spot_on(state, &home);
    }
    let from_row = leaves_of(state, &overview.from.desktop);
    overview
        .from
        .leaf
        .take_if(|leaf| !from_row.iter().flatten().any(|l| l == leaf));
    if desktop_of(state, &overview.at.desktop).is_none() {
        overview.at = spot_on(state, &overview.from.desktop);
        overview.seen = leaves_of(state, &overview.at.desktop);
    }
    let now = leaves_of(state, &overview.at.desktop);
    let held = now
        .iter()
        .flatten()
        .any(|l| Some(l) == overview.at.leaf.as_ref());
    if !held {
        overview.at.leaf = overview
            .at
            .leaf
            .as_deref()
            .and_then(|gone| neighbour(&overview.seen, &now, gone, &overview.order))
            .or_else(|| spot_on(state, &overview.at.desktop).leaf);
    }
    overview.seen = now;
    state.window.overview = Some(overview);
}

/// The leaf of row `now` nearest where `gone` stood in row `seen`: the next in its column, else
/// the one used last of the nearest column, the one after before the one before.
fn neighbour(
    seen: &[Vec<String>],
    now: &[Vec<String>],
    gone: &str,
    order: &[String],
) -> Option<String> {
    let left: HashSet<&String> = now.iter().flatten().collect();
    let kept = |leaf: &&String| left.contains(*leaf);
    let at = seen.iter().position(|c| c.iter().any(|l| l == gone))?;
    let within = seen[at].iter().position(|l| l == gone)?;
    let (above, below) = seen[at].split_at(within);
    let in_column = below.iter().find(kept).or(above.iter().rev().find(kept));
    let last_used = |column: &Vec<String>| {
        let mut leaves = column.iter().filter(kept);
        let first = leaves.next()?;
        Some(
            order
                .iter()
                .find(|l| *l == first || leaves.clone().any(|o| o == *l))
                .unwrap_or(first)
                .clone(),
        )
    };
    in_column.cloned().or_else(|| {
        (1..seen.len()).find_map(|far| {
            let after = seen.get(at + far).and_then(last_used);
            after.or_else(|| seen.get(at.checked_sub(far)?).and_then(last_used))
        })
    })
}
