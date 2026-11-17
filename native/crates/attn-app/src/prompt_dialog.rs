//! The Tauri app's `ModalDialog` prompts, `.mp-dialog`: a question in
//! [`DialogFrame::prompt`](attn_ui::DialogFrame::prompt) that Cancel and one action answer.

use attn_ui::Tokens;
use rune::{El, row};
use rune_kit::{Button, ButtonVariant};

/// `.mp-button`: 7 points above and below 13 point text, inside a 1 point edge.
const BUTTON_HEIGHT: f32 = 31.0;

/// `button` as a `.mp-button`, or as `.mp-button.primary` when `primary`, keyed `key`.
pub(crate) fn button(button: Button, primary: bool, key: impl Into<String>, t: &Tokens) -> El {
    let variant = if primary {
        ButtonVariant::Submit
    } else {
        ButtonVariant::Neutral
    };
    button
        .variant(variant)
        .build(attn_ui::kit(t))
        .key(key.into())
        .h(BUTTON_HEIGHT)
        .px(12.0)
}

/// The prompts' `.mp-dialog-actions`: Cancel, then the action, at the bottom right.
pub(crate) fn actions(cancel: El, action: El) -> El {
    row()
        .w_full()
        .gap(10.0)
        .px(24.0)
        .pb(22.0)
        .justify_end()
        .child(cancel)
        .child(action)
}
