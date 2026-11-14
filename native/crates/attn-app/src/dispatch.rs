use attn_protocol::SetSettingMessage;
use rune::Tx;

use crate::State;
use crate::action::Action;
use crate::palette::Face;
use crate::picker::{self, Direction};
use crate::shell::{Layer, Palette};

/// What became of an action.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Outcome {
    Ran,
    /// The native app runs it, but not now: no asks drawer without asks.
    Unavailable,
    /// The native app does not run it yet.
    Pending,
}

/// The one path every intent takes, from the native menu or the palette. Whether the native
/// app ran it; one it does not run yet says so in a notice.
pub fn dispatch(state: &mut State, action: Action, tx: &mut Tx) -> bool {
    match run(state, action.clone(), tx) {
        Outcome::Ran => true,
        Outcome::Unavailable => false,
        Outcome::Pending => {
            state
                .window
                .notify(format!("Not in the native app yet: {}", action.label()), tx);
            false
        }
    }
}

/// [`dispatch`] with no notice, for keys: a key whose action did not run goes on, as an
/// unhandled key does in the Tauri app.
pub(crate) fn run(state: &mut State, action: Action, tx: &mut Tx) -> Outcome {
    if state.window.tile.is_some() {
        return if crate::desktop::forward_to_main(state, action) {
            Outcome::Ran
        } else {
            Outcome::Unavailable
        };
    }
    if crate::palette::queue_action(state, &action, tx)
        || crate::sidebar::dispatch(state, &action, tx)
        || crate::desktop::run(state, &action, tx)
        || crate::settings::dispatch(state, &action, tx)
    {
        return Outcome::Ran;
    }
    let window = &mut state.window;
    match action {
        Action::SessionToggleSidebar => window.sidebar_hidden = !window.sidebar_hidden,
        Action::DockAttention => crate::attention::toggle(state, tx),
        Action::OpenAttention => crate::attention::open(state, tx),
        Action::OpenWhatsNew => crate::whats_new::open(state, tx),
        Action::SessionRefreshPrs => crate::attention::refresh(state, tx),
        Action::DockAsks if crate::asks::available(&state.facts) => crate::asks::toggle(state, tx),
        Action::DockAsks => return Outcome::Unavailable,
        Action::ShowAsks { ask } if crate::asks::available(&state.facts) => {
            crate::asks::show(state, ask.as_deref(), tx)
        }
        Action::ShowAsks { .. } => return Outcome::Unavailable,
        Action::BoardOpen => crate::garden::toggle_frame(state, tx),
        Action::UiActionMenu => crate::palette::open(state, Face::Agents, tx),
        Action::UiCommandPalette => crate::palette::open(state, Face::Commands, tx),
        Action::FileOpen if window.layer == Some(Layer::Palette(Palette::Files)) => {
            crate::opener::close(state, tx)
        }
        Action::FileOpen => crate::opener::open(state, tx),
        Action::SessionsOpen => crate::ledger::toggle(state, tx),
        Action::OpenWorktrees => crate::ledger::open(state, crate::ledger::Tab::Worktrees, tx),
        Action::ToggleNotifications => crate::notifications::toggle(state, tx),
        Action::StartUpdate => crate::update_run::start(state, None, tx),
        Action::UiShowShortcuts => window.toggle_layer(Layer::Shortcuts, tx),
        Action::OpenShortcutEditor => crate::shortcut_editor::open(state, tx),
        Action::ProfileSwitch => crate::profile_switcher::toggle(state, tx),
        Action::SwitchToProfile { profile } => crate::profile_switcher::select(state, &profile, tx),
        Action::AppQuit => tx.quit(),
        Action::SetSetting { key, value } => {
            let _ = state.daemon.send(&SetSettingMessage {
                key,
                value,
                ..SetSettingMessage::new()
            });
        }
        Action::SessionNew => picker::new_session(state, Direction::Vertical, None, tx),
        Action::SessionNewHorizontal => picker::new_session(state, Direction::Horizontal, None, tx),
        Action::SessionNewTerminal => {
            picker::new_session(state, Direction::Vertical, Some(picker::TERMINAL), tx)
        }
        _ => return Outcome::Pending,
    }
    Outcome::Ran
}
