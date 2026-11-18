//! Whether the queue bar's desktop chips overflow the bar, as the Tauri app's
//! `useBarCrowded`. A crowded bar shows only the window's desktop's chip, gathers the others
//! into the extras chip, and leaves out the runs chip. It stays crowded while what the bar holds
//! is unchanged and the bar is narrower than the width the chips last needed. The bar and its
//! chips are measured before the frame paints, as the hook's layout effect measures them, so
//! a bar that changes shows its chips crowded or whole from its first frame.

use crate::State;

/// What the bar holds that changes the room its chips need.
#[derive(Clone, Debug, PartialEq)]
struct Contents {
    desktop: Option<String>,
    desktops: usize,
    runs: bool,
    critical: bool,
}

fn contents(state: &State) -> Contents {
    let facts = &state.facts;
    let runs = crate::automations::runs::on_desktops(facts, state.window.now_ms)
        .iter()
        .any(|g| !g.runs.is_empty() || !g.needing_you.is_empty());
    Contents {
        desktop: facts.current_desktop().map(|d| d.id.clone()),
        desktops: facts.desktops.len(),
        runs,
        critical: facts.notifications.unread_critical > 0,
    }
}

#[derive(Clone, Debug, PartialEq)]
struct Crowding {
    fits_from: f32,
    contents: Contents,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct Room {
    bar: Option<[f32; 4]>,
    /// Where every chip ends, from the last layout that showed them all.
    chips_right: Option<f32>,
    crowding: Option<Crowding>,
}

/// The bar shows only the window's desktop's chip and the extras chip.
pub(crate) fn crowded(state: &State) -> bool {
    let room = &state.window.queue_bar_room;
    match (&room.crowding, room.bar) {
        (Some(crowding), Some([_, _, w, _])) => {
            w < crowding.fits_from && crowding.contents == contents(state)
        }
        _ => false,
    }
}

/// Where the queue bar is laid out.
pub(crate) fn bar_laid_out(state: &mut State, rect: [f32; 4]) {
    state.window.queue_bar_room.bar = Some(rect);
    measure(state);
}

/// Where the queue bar's desktop chips are laid out, when the view showed them `crowded`.
/// A layout reports the bar before the chips inside it, and the view runs again only after
/// both, so a bar that moved with stale chips is measured again with the chips' new place.
pub(crate) fn chips_laid_out(state: &mut State, [x, _, w, _]: [f32; 4], crowded: bool) {
    state.window.queue_bar_room.chips_right = (!crowded).then_some(x + w);
    measure(state);
}

fn measure(state: &mut State) {
    let contents = contents(state);
    let room = &mut state.window.queue_bar_room;
    let (Some([x, _, w, _]), Some(chips_right)) = (room.bar, room.chips_right) else {
        return;
    };
    let overflow = chips_right - (x + w);
    room.crowding = (overflow > 0.5).then_some(Crowding {
        fits_from: w + overflow,
        contents,
    });
}
