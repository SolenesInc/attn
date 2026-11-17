//! The automations drawer's and form's own type and buttons, as the Tauri app's
//! `AutomationsPanel.css` and `AutomationForm.css` draw them, at the native app's text sizes.

use attn_ui::Tokens;
use rune::{Color, El, TextStyle, text};
use rune_kit::{Button, ButtonVariant, ControlSize};

/// The Tauri app's `--font-size-xs` text: buttons, summaries, hints and field labels.
pub(crate) const SMALL: TextStyle = TextStyle::new(12.0, 400.0);

/// The Tauri app's `--font-size-sm` text: the panel's own words, run dates.
pub(crate) const REGULAR: TextStyle = TextStyle::new(13.0, 400.0);

/// The drawer's heading, `.automations-panel__kicker`.
pub(crate) const HEADING: TextStyle = TextStyle::new(24.0, 600.0);

/// The Tauri app's bordered buttons: `.automations-panel__new`, `__run-now`, `__edit`, and the
/// form's `__browse`, `__cancel`, `__save` and `__delete-button`.
#[derive(Clone, Copy)]
pub(crate) enum Look {
    Neutral,
    Outline,
    Save,
    Danger,
    Armed,
}

impl Look {
    fn paint(self, t: &Tokens) -> (Color, Color, Color) {
        match self {
            Look::Neutral => (t.bg_button, t.border, t.text_primary),
            Look::Outline => (Color::TRANSPARENT, t.border, t.text_secondary),
            Look::Save => (t.bg_button, t.brand, t.text_primary),
            Look::Danger => (t.bg_button, t.border, t.error),
            Look::Armed => (t.error, t.error, t.bg_elevated),
        }
    }
}

/// `label` as a button in `look`; `make` adds what it does.
pub(crate) fn button(
    label: &str,
    look: Look,
    t: &Tokens,
    make: impl FnOnce(Button) -> Button,
) -> El {
    let (fill, edge, ink) = look.paint(t);
    let words = label.to_owned();
    make(
        Button::new(label)
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .label_with(move |_, _| text(words).font(SMALL).color(ink)),
    )
    .build(attn_ui::kit(t))
    .radius(6.0)
    .fill(fill)
    .border(1.0, edge)
}

/// `words` as the Tauri app's quiet text buttons, such as the runs' Close: no frame, nothing
/// around the words.
pub(crate) fn quiet(words: &str, ink: Color, t: &Tokens) -> El {
    text(words.to_owned())
        .role("button")
        .label(words.to_owned())
        .focusable(true)
        .focus_ring(2.0, 2.0, t.brand)
        .font(SMALL)
        .color(ink)
}
