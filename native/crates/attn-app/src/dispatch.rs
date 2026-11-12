use attn_protocol::RecordAnalyticsMessage;
use rune::Tx;

use crate::State;
use crate::action::Action;
use crate::shell::{Drawer, Layer, Palette};

/// The one path every intent takes, from a key, the native menu or the palette.
pub fn dispatch(state: &mut State, action: Action, tx: &mut Tx) {
    let window = &mut state.window;
    match action {
        Action::SessionToggleSidebar => window.sidebar_hidden = !window.sidebar_hidden,
        Action::DockAttention => window.toggle_drawer(Drawer::Attention),
        Action::DockAsks => window.toggle_drawer(Drawer::Asks),
        Action::BoardOpen => window.toggle_drawer(Drawer::Garden),
        Action::UiActionMenu => window.toggle_layer(Layer::Palette(Palette::Agents), tx),
        Action::UiCommandPalette => window.toggle_layer(Layer::Palette(Palette::Commands), tx),
        Action::FileOpen => window.toggle_layer(Layer::Palette(Palette::Files), tx),
        Action::SessionsOpen => window.toggle_layer(Layer::Palette(Palette::Sessions), tx),
        Action::UiOpenSettings => window.toggle_layer(Layer::Settings, tx),
        Action::UiShowShortcuts => window.toggle_layer(Layer::Shortcuts, tx),
        Action::AppQuit => tx.quit(),
        _ => {
            window.notify(format!("Not in the native app yet: {}", action.label()), tx);
            return;
        }
    }
    record(state, action);
}

fn record(state: &State, action: Action) {
    if state
        .facts
        .settings
        .get("analytics.enabled")
        .map(String::as_str)
        != Some("true")
    {
        return;
    }
    let _ = state.daemon.send(&RecordAnalyticsMessage {
        stream: "app".into(),
        name: "shortcut".into(),
        props: Some(
            [("id".to_owned(), serde_json::Value::from(action.id()))]
                .into_iter()
                .collect(),
        ),
        ..RecordAnalyticsMessage::new()
    });
}
