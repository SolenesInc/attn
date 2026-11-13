use attn_ui::Tokens;
use rune::{Color, El, TextStyle, Tx, text};
use rune_kit::{Button, ButtonVariant};

use crate::State;

/// What a header popover shows about its tile's agent.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum What {
    Usage,
    /// The details of the session's pull requests.
    Details,
    /// The delegation chain the session sits in.
    Chain,
    /// The agent's open asks.
    Ask,
}

impl What {
    fn part(self) -> &'static str {
        match self {
            What::Usage => "usage",
            What::Details => "details",
            What::Chain => "role",
            What::Ask => "ask",
        }
    }

    fn shown_for(self, session: &attn_protocol::Session, facts: &attn_client::Facts) -> bool {
        match self {
            What::Usage => super::usage::shown(session).is_some(),
            What::Details => super::provenance::pull_request(session).is_some(),
            What::Chain => super::chain::shown(session, &facts.sessions),
            What::Ask => !crate::asks::of_session(facts, session).is_empty(),
        }
    }

    /// The row a popover listing several things picks first.
    fn first_pick(self, session: &str, sessions: &[attn_protocol::Session]) -> usize {
        match self {
            What::Chain => super::chain::current_row(session, sessions),
            What::Usage | What::Details | What::Ask => 0,
        }
    }

    /// Scrolls a popover listing several things to the row it picks.
    fn reveal(self, pop: &Pop, picked: usize, tx: &mut Tx) {
        if self == What::Chain {
            super::chain::reveal(pop, picked, tx);
        }
    }

    /// How long the pointer rests before the popover opens, and how long it may be away
    /// before the popover closes, in seconds.
    fn delays(self) -> (f64, f64) {
        match self {
            What::Usage | What::Ask => (0.16, 0.24),
            What::Details | What::Chain => (0.0, 0.12),
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

    /// The popover's own key.
    pub(crate) fn surface(&self) -> String {
        format!("{}.popover", self.anchor())
    }
}

pub(crate) fn anchor(leaf: &str, what: What) -> String {
    format!("tile.{leaf}.{}", what.part())
}

enum Timer {
    Open(Pop, usize, f64),
    Close(f64),
}

/// The window's one header popover. The pointer opens it by resting on its piece and lets it
/// go by leaving both.
#[derive(Default)]
pub(crate) struct HeaderPop {
    pub(crate) open: Option<Pop>,
    timer: Option<Timer>,
    /// The row a popover listing several things has picked.
    pub(crate) picked: usize,
}

/// `piece` opens the popover of `what` when the pointer moves onto it, as the shelf's peek does,
/// so a popover closed under a still pointer stays closed, and lets it go once the pointer
/// leaves.
/// A header piece that reads as text in `font` and `ink` and is pressed as a button named `label`.
pub(crate) fn link(
    label: String,
    face: String,
    font: TextStyle,
    ink: Color,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(label)
        .variant(ButtonVariant::GhostMuted)
        .on_press_tx(press)
        .label_with(move |_, _| text(face).font(font).color(ink).single_line())
        .build(attn_ui::kit(t))
        .h(font.line_px())
        .px(0.0)
}

pub(crate) fn hovers(piece: El, leaf: &str, session: &str, what: What) -> El {
    let (leaf, session) = (leaf.to_owned(), session.to_owned());
    piece
        .on_pointer_move_local(move |state: &mut State, _, tx: &mut Tx| {
            rest(state, &leaf, &session, what, tx)
        })
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            if !inside {
                close_soon(&mut state.window.desk.header_pop, tx);
            }
        })
}

fn rest(state: &mut State, leaf: &str, session: &str, what: What, tx: &mut Tx) {
    let first = what.first_pick(session, &state.facts.sessions);
    let pop = &mut state.window.desk.header_pop;
    let here = |p: &Pop| p.leaf == leaf && p.what == what;
    if pop.open.as_ref().is_some_and(here) {
        if matches!(pop.timer, Some(Timer::Close(_))) {
            pop.timer = None;
        } else {
            tx.live_only();
        }
        return;
    }
    if pop.open.as_ref().is_some_and(|p| p.pinned)
        || matches!(&pop.timer, Some(Timer::Open(p, ..)) if here(p))
    {
        tx.live_only();
        return;
    }
    let opening = Pop {
        leaf: leaf.to_owned(),
        session: session.to_owned(),
        what,
        pinned: false,
    };
    match what.delays().0 {
        0.0 => {
            pop.timer = None;
            pop.picked = first;
            what.reveal(&opening, first, tx);
            pop.open = Some(opening);
        }
        delay => {
            let at = tx.clock() + delay;
            pop.timer = Some(Timer::Open(opening, first, at));
            tx.wake_at(at);
        }
    }
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
    let Some(open) = pop.open.as_ref().filter(|p| !p.pinned) else {
        return;
    };
    let at = tx.clock() + open.what.delays().1;
    pop.timer = Some(Timer::Close(at));
    tx.wake_at(at);
}

/// A press on the piece pins its popover, which takes the keyboard, and a second press closes
/// it.
pub(crate) fn press(state: &mut State, leaf: &str, session: &str, what: What, tx: &mut Tx) {
    let pinned_here = state
        .window
        .desk
        .header_pop
        .open
        .as_ref()
        .is_some_and(|p| p.pinned && p.leaf == leaf && p.what == what);
    if pinned_here {
        close(state);
    } else {
        pinned(state, leaf, session, what, tx);
    }
}

fn pinned(state: &mut State, leaf: &str, session: &str, what: What, tx: &mut Tx) {
    let first = what.first_pick(session, &state.facts.sessions);
    let pop = &mut state.window.desk.header_pop;
    pop.timer = None;
    pop.picked = first;
    let open = Pop {
        leaf: leaf.to_owned(),
        session: session.to_owned(),
        what,
        pinned: true,
    };
    tx.focus(open.surface(), true);
    what.reveal(&open, first, tx);
    pop.open = Some(open);
}

/// Pins the popover of `what` on the tile on screen that shows `session`.
pub(crate) fn pin(state: &mut State, session: &str, what: What, tx: &mut Tx) {
    let leaf = state
        .facts
        .current_desktop()
        .and_then(|d| d.panes.iter().find(|p| p.session_id == session))
        .map(|p| p.pane_id.clone());
    if let Some(leaf) = leaf {
        pinned(state, &leaf, session, what, tx);
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
                    .is_some_and(|s| pop.what.shown_for(s, facts))
        };
        if self.open.as_ref().is_some_and(|p| !stands(p)) {
            self.open = None;
        }
        if matches!(&self.timer, Some(Timer::Open(pop, ..)) if !stands(pop)) {
            self.timer = None;
        }
    }
}

/// Opens or closes the popover whose moment came. Returns whether it did.
pub(crate) fn expire(state: &mut State, now: f64) -> bool {
    let pop = &mut state.window.desk.header_pop;
    let due = match &pop.timer {
        Some(Timer::Open(.., at) | Timer::Close(at)) => *at <= now,
        None => false,
    };
    if !due {
        return false;
    }
    pop.open = match pop.timer.take() {
        Some(Timer::Open(open, first, _)) => {
            pop.picked = first;
            Some(open)
        }
        _ => None,
    };
    true
}
