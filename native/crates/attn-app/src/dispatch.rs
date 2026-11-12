use rune::Tx;

use crate::State;
use crate::action::Action;
use crate::palette::Face;
use crate::picker::{self, Direction};
use crate::shell::{Drawer, Layer, Palette};

/// The one path every intent takes, from a key, the native menu or the palette. Whether the
/// native app ran it.
pub fn dispatch(state: &mut State, action: Action, tx: &mut Tx) -> bool {
    if crate::sidebar::dispatch(state, &action, tx) || crate::desktop::run(state, &action, tx) {
        return true;
    }
    let window = &mut state.window;
    match action {
        Action::SessionToggleSidebar => window.sidebar_hidden = !window.sidebar_hidden,
        Action::DockAttention => window.toggle_drawer(Drawer::Attention),
        Action::DockAsks => window.toggle_drawer(Drawer::Asks),
        Action::BoardOpen => window.toggle_drawer(Drawer::Garden),
        Action::UiActionMenu => crate::palette::open(state, Face::Agents, tx),
        Action::UiCommandPalette => crate::palette::open(state, Face::Commands, tx),
        Action::FileOpen => window.toggle_layer(Layer::Palette(Palette::Files), tx),
        Action::SessionsOpen => window.toggle_layer(Layer::Palette(Palette::Sessions), tx),
        Action::UiOpenSettings => window.toggle_layer(Layer::Settings, tx),
        Action::UiShowShortcuts => window.toggle_layer(Layer::Shortcuts, tx),
        Action::AppQuit => tx.quit(),
        Action::SessionNew => picker::new_session(state, Direction::Vertical, None, tx),
        Action::SessionNewHorizontal => picker::new_session(state, Direction::Horizontal, None, tx),
        Action::SessionNewTerminal => {
            picker::new_session(state, Direction::Vertical, Some(picker::TERMINAL), tx)
        }
        _ => {
            window.notify(format!("Not in the native app yet: {}", action.label()), tx);
            return false;
        }
    }
    true
}
