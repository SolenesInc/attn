//! The harness menu: the harnesses the daemon offers, to start something with when the user
//! has no last pick or wants another. Choosing one makes it the last pick and starts with it.

use attn_protocol::{Harness, PR};
use attn_ui::Tokens;
use rune::{El, Key, KeyEvent, Tx};
use rune_kit::MenuItem;

use crate::State;

const MENU: &str = "harness-menu";

/// What the chosen harness starts.
pub(crate) enum Start {
    CodeNavSetup { tile: String },
    Worktree(Box<PR>),
}

/// An open menu: what it starts, and the key of what it opened beside.
pub(crate) struct Open {
    start: Start,
    under: String,
}

fn offered(harnesses: &[Harness]) -> impl Iterator<Item = &Harness> {
    harnesses.iter().filter(|h| h.offered)
}

/// The harness the user last picked, while the daemon offers it.
pub(crate) fn last_pick(
    settings: &std::collections::BTreeMap<String, String>,
    harnesses: &[Harness],
) -> Option<String> {
    let picked = settings
        .get(crate::picker::SAVED_AGENT)?
        .trim()
        .to_lowercase();
    offered(harnesses).any(|h| h.id == picked).then_some(picked)
}

fn item_key(agent: &str) -> String {
    format!("{MENU}.{agent}")
}

/// Opens the menu beside `under` with the last pick, or else the first harness, focused.
pub(crate) fn open(state: &mut State, start: Start, under: String, tx: &mut Tx) {
    let facts = &state.facts;
    let focused = last_pick(&facts.settings, &facts.harnesses)
        .or_else(|| offered(&facts.harnesses).next().map(|h| h.id.clone()));
    if let Some(agent) = focused {
        tx.focus(item_key(&agent), true);
    }
    state.window.harness_menu = Some(Open { start, under });
}

fn choose(state: &mut State, agent: &str, tx: &mut Tx) {
    let Some(open) = state.window.harness_menu.take() else {
        return;
    };
    crate::picker::set_setting(state, crate::picker::SAVED_AGENT, agent);
    match open.start {
        Start::CodeNavSetup { tile } => {
            crate::review::setup::set_up(state, &tile, agent.to_owned(), tx)
        }
        Start::Worktree(pr) => {
            crate::picker::pull_request::open_in_worktree(state, &pr, agent.to_owned(), tx)
        }
    }
}

/// 1 to 9 choose the harness at that place in the menu.
fn digit(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    let Key::Text(typed) = &ev.key else {
        return false;
    };
    let Some(place) = typed.parse::<usize>().ok().filter(|n| (1..=9).contains(n)) else {
        return false;
    };
    let Some(agent) = offered(&state.facts.harnesses)
        .nth(place - 1)
        .map(|h| h.id.clone())
    else {
        return false;
    };
    choose(state, &agent, tx);
    true
}

pub(crate) fn view(state: &State, t: &Tokens) -> Vec<El> {
    let Some(open) = &state.window.harness_menu else {
        return Vec::new();
    };
    let facts = &state.facts;
    let picked = last_pick(&facts.settings, &facts.harnesses);
    let mut menu = attn_ui::system::row_menu(MENU)
        .min_width(216.0)
        .label("Choose a harness")
        .open(true)
        .on_close(|state: &mut State| state.window.harness_menu = None)
        .anchor(open.under.clone());
    let mut any = false;
    for (place, harness) in offered(&facts.harnesses).enumerate() {
        any = true;
        let agent = harness.id.clone();
        let mut item = MenuItem::new(harness.name.clone())
            .key(item_key(&harness.id))
            .checked(picked.as_deref() == Some(harness.id.as_str()))
            .on_select_tx(move |state: &mut State, tx: &mut Tx| choose(state, &agent, tx));
        if place < 9 {
            item = item.shortcut((place + 1).to_string());
        }
        menu = menu.item(item);
    }
    menu = menu.note(if any {
        "↑ ↓ choose · 1–9 pick · ↵ start · esc cancel"
    } else {
        "No harness is offered yet"
    });
    let mut layer = Vec::new();
    menu.build(attn_ui::kit(t), &mut layer);
    if let Some(surface) = layer.pop() {
        layer.push(surface.on_key(digit));
    }
    layer
}
