//! The ring a tile shows for a moment when it arrives on screen, as the Tauri app's arrival
//! pulse: the tile a show the person asked for lands on, or a tile that joins the shown
//! desktop while no show is on its way.

use attn_client::Facts;
use attn_ui::Tokens;
use rune::{Beat, Easing, El, Sequence, Tx, el};

use crate::State;
use crate::shell::View;

/// How long a ring fades, as the Tauri app's `leaf-arrival-pulse`.
const RING_SECONDS: f64 = 0.55;
/// The ring's width and strength, as the Tauri app's inset accent shadow.
const RING_PX: f32 = 1.5;
const RING_ALPHA: f32 = 0.7;
const FADE: &[Beat] = &[
    Beat::new(0.0).opacity(1.0),
    Beat::new(RING_SECONDS)
        .opacity(0.0)
        .easing(Easing::EASE_OUT),
];

/// What a show this window asked for brings on screen.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Shown {
    Session(String),
    Leaf { desktop: String, leaf: String },
}

/// A show this window asked for that has not landed on screen yet. An `announced` one rings
/// its tile when it lands; a quiet one for a session that just started only keeps its tile
/// from ringing as it joins, and is done once the session has a tile.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Awaited {
    shown: Shown,
    announced: bool,
}

/// A tile ringing until `until`.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Ring {
    desktop: String,
    leaf: String,
    until: f64,
}

/// The tiles the window shows, to tell the ones that join it.
pub(crate) struct OnScreen {
    desktop: String,
    leaves: Vec<String>,
}

/// Waits for `shown` to land, ringing its tile then if `announced`.
pub(crate) fn expect(state: &mut State, shown: Shown, announced: bool, tx: &mut Tx) {
    state.window.desk.awaited = Some(Awaited { shown, announced });
    land(state, tx);
}

/// Stops waiting for `shown`, which the daemon refused or put out of sight.
pub(crate) fn forget(state: &mut State, shown: &Shown) {
    let awaited = &mut state.window.desk.awaited;
    if awaited.as_ref().is_some_and(|a| a.shown == *shown) {
        *awaited = None;
    }
}

/// The tiles the window shows before `facts` change.
pub(crate) fn on_screen(state: &State) -> Option<OnScreen> {
    if state.window.view != View::Desktop {
        return None;
    }
    let desktop = state.facts.current_desktop()?;
    Some(OnScreen {
        desktop: desktop.id.clone(),
        leaves: leaves(&state.facts, &desktop.id),
    })
}

/// Follows new facts: rings the tile an awaited show lands on and the tiles that joined the
/// desktop the window kept showing, and lets go of the rings of a desktop out of sight.
pub(crate) fn follow(state: &mut State, before: Option<OnScreen>, tx: &mut Tx) {
    let now = on_screen(state);
    let desk = &mut state.window.desk;
    desk.rings
        .retain(|r| now.as_ref().is_some_and(|n| n.desktop == r.desktop));
    if let (Some(before), Some(now)) = (before, &now)
        && before.desktop == now.desktop
        && desk.awaited.is_none()
    {
        let joined = now.leaves.iter().filter(|l| !before.leaves.contains(l));
        let until = tx.clock() + RING_SECONDS;
        for leaf in joined.cloned().collect::<Vec<_>>() {
            ring(desk, &now.desktop, leaf, until, tx);
        }
    }
    land(state, tx);
}

/// Lets rings whose time is up go. Whether any did.
pub(crate) fn expire(state: &mut State, now: f64) -> bool {
    let rings = &mut state.window.desk.rings;
    let before = rings.len();
    rings.retain(|r| r.until > now);
    rings.len() != before
}

fn land(state: &mut State, tx: &mut Tx) {
    let Some(awaited) = state.window.desk.awaited.clone() else {
        return;
    };
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let leaf = desktop.active_pane_id.clone();
    if let (false, Shown::Session(session)) = (awaited.announced, &awaited.shown)
        && state
            .facts
            .desktops
            .iter()
            .any(|d| d.panes.iter().any(|p| p.session_id == *session))
    {
        state.window.desk.awaited = None;
        return;
    }
    let landed = state.window.view == View::Desktop
        && match &awaited.shown {
            Shown::Session(session) => desktop
                .panes
                .iter()
                .any(|p| p.pane_id == leaf && p.session_id == *session),
            Shown::Leaf {
                desktop: d,
                leaf: l,
            } => *d == desktop.id && *l == leaf,
        };
    if !landed {
        return;
    }
    let id = desktop.id.clone();
    let desk = &mut state.window.desk;
    desk.awaited = None;
    if awaited.announced {
        desk.rings.clear();
        ring(desk, &id, leaf, tx.clock() + RING_SECONDS, tx);
    }
}

fn ring(desk: &mut super::Desk, desktop: &str, leaf: String, until: f64, tx: &mut Tx) {
    desk.rings.retain(|r| r.leaf != leaf);
    desk.rings.push(Ring {
        desktop: desktop.to_owned(),
        leaf,
        until,
    });
    tx.wake_at(until);
}

fn leaves(facts: &Facts, desktop: &str) -> Vec<String> {
    facts
        .desktops
        .iter()
        .find(|d| d.id == desktop)
        .map(|d| {
            let tree = super::layout::Node::parse(&d.tree_json);
            tree.map(|t| t.leaves().iter().map(|l| l.id.clone()).collect())
                .unwrap_or_default()
        })
        .unwrap_or_default()
}

/// What the drawn rings depend on, for the tile area's memo.
pub(crate) fn keys(rings: &[Ring]) -> Vec<(String, u64)> {
    rings
        .iter()
        .map(|r| (r.leaf.clone(), r.until.to_bits()))
        .collect()
}

/// The ring over tile `leaf` while it rings, keyed by when it started so a ring that starts
/// again plays from the start.
pub(crate) fn overlay(rings: &[Ring], leaf: &str, t: &Tokens) -> Option<El> {
    let ring = rings.iter().find(|r| r.leaf == leaf)?;
    Some(
        el().key(format!("tile.{leaf}.arrival.{}", ring.until.to_bits()))
            .pin(Some(0.0), Some(0.0), Some(0.0), Some(0.0))
            .paint_order(2)
            .border(RING_PX, t.brand.alpha(RING_ALPHA))
            .transition(rune::Transition::none())
            .sequence(Sequence::new(FADE)),
    )
}
