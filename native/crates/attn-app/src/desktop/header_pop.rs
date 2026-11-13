use rune::Tx;

use crate::State;

const OPEN_DELAY: f64 = 0.16;
const CLOSE_DELAY: f64 = 0.24;

/// What a header popover shows about its tile's agent.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum What {
    Usage,
}

impl What {
    fn part(self) -> &'static str {
        match self {
            What::Usage => "usage",
        }
    }

    fn shown_for(self, session: &attn_protocol::Session) -> bool {
        match self {
            What::Usage => super::usage::shown(session).is_some(),
        }
    }
}

/// A tile header's popover: the piece it hangs from, and whether a click or the Action menu
/// pinned it, which keeps it until Escape or a press outside it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Pop {
    pub(crate) leaf: String,
    pub(crate) session: String,
    pub(crate) what: What,
    pub(crate) pinned: bool,
}

impl Pop {
    pub(crate) fn anchor(&self) -> String {
        anchor(&self.leaf, self.what)
    }
}

pub(crate) fn anchor(leaf: &str, what: What) -> String {
    format!("tile.{leaf}.{}", what.part())
}

enum Timer {
    Open(Pop, f64),
    Close(f64),
}

/// The window's one header popover. The pointer opens it by resting on its piece and lets it
/// go by leaving both.
#[derive(Default)]
pub(crate) struct HeaderPop {
    pub(crate) open: Option<Pop>,
    timer: Option<Timer>,
}

pub(crate) fn hover(
    state: &mut State,
    leaf: &str,
    session: &str,
    what: What,
    inside: bool,
    tx: &mut Tx,
) {
    let pop = &mut state.window.desk.header_pop;
    if !inside {
        close_soon(pop, tx);
        return;
    }
    if pop
        .open
        .as_ref()
        .is_some_and(|p| p.leaf == leaf && p.what == what)
    {
        pop.timer = None;
        return;
    }
    if pop.open.as_ref().is_some_and(|p| p.pinned) {
        return;
    }
    let at = tx.clock() + OPEN_DELAY;
    pop.timer = Some(Timer::Open(
        Pop {
            leaf: leaf.to_owned(),
            session: session.to_owned(),
            what,
            pinned: false,
        },
        at,
    ));
    tx.wake_at(at);
}

/// The pointer moved onto the open popover, or off it.
pub(crate) fn hover_popover(state: &mut State, inside: bool, tx: &mut Tx) {
    let pop = &mut state.window.desk.header_pop;
    if inside {
        pop.timer = None;
    } else {
        close_soon(pop, tx);
    }
}

fn close_soon(pop: &mut HeaderPop, tx: &mut Tx) {
    pop.timer = None;
    if pop.open.as_ref().is_none_or(|p| p.pinned) {
        return;
    }
    let at = tx.clock() + CLOSE_DELAY;
    pop.timer = Some(Timer::Close(at));
    tx.wake_at(at);
}

/// A press on the piece pins its popover, and a second press closes it.
pub(crate) fn press(state: &mut State, leaf: &str, session: &str, what: What) {
    let pop = &mut state.window.desk.header_pop;
    pop.timer = None;
    let pinned_here = pop
        .open
        .as_ref()
        .is_some_and(|p| p.pinned && p.leaf == leaf && p.what == what);
    pop.open = (!pinned_here).then(|| Pop {
        leaf: leaf.to_owned(),
        session: session.to_owned(),
        what,
        pinned: true,
    });
}

/// Pins the popover of `what` on the tile on screen that shows `session`.
pub(crate) fn pin(state: &mut State, session: &str, what: What) {
    let leaf = state
        .facts
        .current_desktop()
        .and_then(|d| d.panes.iter().find(|p| p.session_id == session))
        .map(|p| p.pane_id.clone());
    if let Some(leaf) = leaf {
        let pop = &mut state.window.desk.header_pop;
        pop.timer = None;
        pop.open = Some(Pop {
            leaf,
            session: session.to_owned(),
            what,
            pinned: true,
        });
    }
}

pub(crate) fn close(state: &mut State) {
    let pop = &mut state.window.desk.header_pop;
    pop.timer = None;
    pop.open = None;
}

impl HeaderPop {
    /// Lets the popover go, and any it was about to open, once its tile leaves the current
    /// desktop or its agent stops having what it shows.
    pub(crate) fn follow(&mut self, facts: &attn_client::Facts) {
        let stands = |pop: &Pop| {
            facts
                .current_desktop()
                .is_some_and(|d| d.panes.iter().any(|p| p.pane_id == pop.leaf))
                && facts
                    .sessions
                    .iter()
                    .find(|s| s.id == pop.session)
                    .is_some_and(|s| pop.what.shown_for(s))
        };
        if self.open.as_ref().is_some_and(|p| !stands(p)) {
            self.open = None;
        }
        if matches!(&self.timer, Some(Timer::Open(pop, _)) if !stands(pop)) {
            self.timer = None;
        }
    }
}

/// Opens or closes the popover whose moment came. Returns whether it did.
pub(crate) fn expire(state: &mut State, now: f64) -> bool {
    let pop = &mut state.window.desk.header_pop;
    let due = match &pop.timer {
        Some(Timer::Open(_, at) | Timer::Close(at)) => *at <= now,
        None => false,
    };
    if !due {
        return false;
    }
    pop.open = match pop.timer.take() {
        Some(Timer::Open(open, _)) => Some(open),
        _ => None,
    };
    true
}
