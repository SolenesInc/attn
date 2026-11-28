//! Tells the daemon what the person sees of the main window, and that they gave the app an
//! input; the link reports it as the daemon's `set_client_presence` asks.

use attn_client::{Connection, Sight};
use rune::Tx;

use crate::State;

/// Says what the main window shows, once the daemon has greeted it; the link reports it when it
/// changed or is news to this connection.
pub(crate) fn follow(state: &State) {
    if state.window.tile.is_some() || state.facts.connection != Connection::Connected {
        return;
    }
    state.daemon.see(Sight {
        visible: !state.hidden,
        home: crate::home::shown(state),
    });
}

pub(crate) fn visibility(state: &mut State, visible: bool, _: &mut Tx) {
    state.hidden = !visible;
    follow(state);
}

pub(crate) fn input(state: &mut State, _: &mut Tx) {
    state.daemon.heard_input();
}
