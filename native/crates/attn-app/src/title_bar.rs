use attn_ui::Tokens;
use attn_ui::system::{self, IconSize};
use rune::{El, Tx};

use crate::State;
use crate::sidebar::actions;

/// The title bar's tools on the left: New agent, Commands, the flow switch, the sidebar toggle
/// and the notifications bell, each framed.
pub(crate) fn title_tools(state: &State, t: &Tokens) -> Vec<El> {
    let queue = actions::queue_flow(state);
    let flow = if queue {
        "Switch to desktop flow"
    } else {
        "Switch to queue flow"
    };
    let collapsed = state.window.sidebar_hidden;
    let sidebar = if collapsed {
        "Show sidebar"
    } else {
        "Collapse sidebar"
    };
    vec![
        system::Button::new("New")
            .look(system::Look::Brand)
            .size(system::Size::Compact)
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                crate::dispatch(state, crate::Action::SessionNew, tx);
            })
            .build(t)
            .key("titlebar.new")
            .label("New agent")
            .into(),
        tool(
            "titlebar.commands",
            "Commands",
            &attn_ui::icon::COMMANDS,
            |state, tx| {
                crate::dispatch(state, crate::Action::UiCommandPalette, tx);
            },
        )
        .ink(t.brand)
        .edged()
        .build(t)
        .into(),
        tool(
            "titlebar.flow",
            flow,
            if queue {
                &attn_ui::icon::DESKTOP_FLOW
            } else {
                &attn_ui::icon::QUEUE_FLOW
            },
            actions::toggle_flow,
        )
        .edged()
        .build(t)
        .into(),
        tool(
            "titlebar.sidebar",
            sidebar,
            if collapsed {
                &attn_ui::icon::EXPAND
            } else {
                &attn_ui::icon::COLLAPSE
            },
            |state, _| state.window.sidebar_hidden = !state.window.sidebar_hidden,
        )
        .edged()
        .build(t)
        .into(),
        crate::notifications::title_button(state, t),
    ]
}

/// The title bar's panel buttons on the right: the garden, the notebook, the ledger,
/// automations and asks, unframed.
pub(crate) fn title_panels(state: &State, t: &Tokens) -> Vec<El> {
    let garden_open = crate::garden::mode(state) != crate::garden::Mode::Closed;
    let automations_open = crate::automations::is_open(state);
    [
        (
            tool(
                "titlebar.garden",
                if garden_open {
                    "Hide the garden"
                } else {
                    "Show the garden"
                },
                &attn_ui::icon::GARDEN,
                crate::garden::toggle_from_icon,
            ),
            garden_open,
        ),
        (
            tool(
                "titlebar.notebook",
                &format!(
                    "Open Notebook{}",
                    keys_of(state, &crate::Action::NotebookOpenFullscreen)
                ),
                &attn_ui::icon::NOTEBOOK,
                |state, tx| {
                    crate::dispatch(state, crate::Action::NotebookOpenFullscreen, tx);
                },
            ),
            state.window.layer == Some(crate::shell::Layer::Notebook),
        ),
        (
            tool(
                "titlebar.ledger",
                &format!(
                    "Open Ledger{}",
                    keys_of(state, &crate::Action::SessionsOpen)
                ),
                &attn_ui::icon::LEDGER,
                crate::ledger::open_last,
            ),
            crate::ledger::is_open(state),
        ),
        (
            tool(
                "titlebar.automations",
                if automations_open {
                    "Hide Automations"
                } else {
                    "Show Automations"
                },
                &attn_ui::icon::AUTOMATIONS,
                crate::automations::toggle,
            ),
            automations_open,
        ),
    ]
    .into_iter()
    .map(|(button, open)| opens(button.selected(open).build(t).into(), open))
    .chain(crate::asks::title_button(state, t))
    .collect()
}

fn keys_of(state: &State, action: &crate::Action) -> String {
    state
        .keymap
        .binding(action)
        .map(|b| format!(" ({})", state.keymap.label(b)))
        .unwrap_or_default()
}

/// A title bar button for a panel, checked while the panel is `open`.
pub(crate) fn opens(button: El, open: bool) -> El {
    button.checked(open)
}

fn tool(
    key: &str,
    label: &str,
    glyph: impl Into<attn_ui::Glyph>,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> system::IconButton {
    system::icon_button(key, label, glyph, IconSize::Large).on_press_tx(press)
}
