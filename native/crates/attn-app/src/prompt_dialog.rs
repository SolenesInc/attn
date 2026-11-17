//! The Tauri app's `ModalDialog` prompts, `.mp-dialog`: a question in
//! [`DialogFrame::prompt`](attn_ui::DialogFrame::prompt) that Cancel and one action answer.

use attn_ui::Tokens;
use rune::{El, TextStyle, row, text};
use rune_kit::{Button, ButtonVariant};

/// `.mp-button`: 7 points above and below 13 point text, inside a 1 point edge.
const BUTTON_HEIGHT: f32 = 31.0;

/// A `.mp-button` saying `words`, or a `.mp-button.primary` when `primary`, keyed `key`;
/// `wire` adds what pressing it does.
pub(crate) fn button(
    words: impl Into<String>,
    primary: bool,
    key: impl Into<String>,
    t: &Tokens,
    wire: impl FnOnce(Button) -> Button,
) -> El {
    let words = words.into();
    let (variant, weight) = if primary {
        (ButtonVariant::Submit, 650.0)
    } else {
        (ButtonVariant::Neutral, 400.0)
    };
    let label = words.clone();
    let button = wire(
        Button::new(words)
            .variant(variant)
            .label_with(move |font, ink| text(label).font(TextStyle { weight, ..font }).color(ink)),
    )
    .build(attn_ui::kit(t))
    .key(key.into())
    .h(BUTTON_HEIGHT)
    .px(12.0);
    if primary {
        button
    } else {
        button.border(1.0, t.border_hover)
    }
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
