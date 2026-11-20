//! The controls that edit a setting. Each saves through the autosave, shows its Saved mark once
//! the value lands, and shows the daemon's refusal beside itself with Retry.

use std::cell::RefCell;

use attn_protocol::go_values::FEATURES;
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Field, Inline, Refusal, Stack, saved};
use attn_ui::keycap;
use attn_ui::system::{
    Button, Control, FieldState, Segment, Segmented, Type, arrows, line_box, number_box, switch,
    words,
};
use rune::{El, Tx};
use rune_kit::TextInput;

use super::autosave;
use crate::{State, setting};

/// A field that edits the setting `key` as a [`Field`] block: its label above, its Saved mark
/// at the end of the label's line, and its refusal under it.
pub(super) struct SettingField {
    key: String,
    label: String,
    placeholder: String,
    step: Option<f64>,
    hint: Option<String>,
    action: Option<El>,
}

impl SettingField {
    pub(super) fn new(key: impl Into<String>, label: impl Into<String>) -> SettingField {
        SettingField {
            key: key.into(),
            label: label.into(),
            placeholder: String::new(),
            step: None,
            hint: None,
            action: None,
        }
    }

    pub(super) fn placeholder(mut self, placeholder: impl Into<String>) -> Self {
        self.placeholder = placeholder.into();
        self
    }

    /// A number field whose arrows step it by `step`.
    pub(super) fn step(mut self, step: f64) -> Self {
        self.step = Some(step);
        self
    }

    pub(super) fn hint(mut self, hint: impl Into<String>) -> Self {
        self.hint = Some(hint.into());
        self
    }

    /// A button beside the field that fills it another way, such as Browse.
    pub(super) fn action(mut self, button: impl Into<El>) -> Self {
        self.action = Some(button.into());
        self
    }

    pub(super) fn build(self, state: &State, t: &Tokens) -> Block {
        let key = self.key.as_str();
        let input = setting_input(state, key, &self.label, &self.placeholder);
        let refused = refused(state, key);
        let control = match self.step {
            Some(step) => number_box(input.trailing(setting_arrows(key, step, t)), refused, t),
            None => line_box(input, refused, t),
        };
        let mut field = Field::new(super::field_key(key), self.label)
            .mark(saved_mark(state, key, t))
            .control(control)
            .refusal(refusal(state, key, t));
        if let Some(hint) = self.hint {
            field = field.hint(hint);
        }
        if let Some(action) = self.action {
            field = field.action(action);
        }
        field.build(t)
    }
}

/// The text input that edits the setting `key`: it saves on Enter and when the focus leaves it.
fn setting_input(state: &State, key: &str, label: &str, placeholder: &str) -> TextInput {
    let field = state.window.settings.autosave.field(key);
    let empty = rune::TextEdit::default();
    let (edit, submit, left) = (key.to_owned(), key.to_owned(), key.to_owned());
    TextInput::new(field.map_or(&empty, |f| &f.draft))
        .key(super::field_key(key))
        .label(label.to_owned())
        .placeholder(placeholder.to_owned())
        .on_edit(move |state: &mut State| state.window.settings.autosave.draft_mut(&edit))
        .on_submit(move |state: &mut State, tx: &mut Tx| autosave::commit(state, &submit, tx))
        .on_blur(move |state: &mut State, tx: &mut Tx| autosave::commit_on_leave(state, &left, tx))
}

fn refused(state: &State, key: &str) -> FieldState {
    let field = state.window.settings.autosave.field(key);
    FieldState::failed_if(field.is_some_and(|f| f.error.is_some()))
}

/// A number box that edits the setting `key` on its own, for a control beside a setting's
/// words: its Saved mark beside it, its refusal under it.
pub(super) fn number_field(
    state: &State,
    key: &'static str,
    label: &str,
    placeholder: &str,
    step: f64,
    t: &Tokens,
) -> El {
    let input =
        setting_input(state, key, label, placeholder).trailing(setting_arrows(key, step, t));
    Stack::tight()
        .items_end()
        .child(
            Inline::loose()
                .child(El::from(number_box(input, refused(state, key), t)))
                .children(saved_mark(state, key, t)),
        )
        .children(refusal(state, key, t))
}

/// The arrows that step the number setting `key` by `step` and save it.
pub(super) fn setting_arrows(key: &str, step: f64, t: &Tokens) -> El {
    let setting = key.to_owned();
    arrows(
        &super::field_key(key),
        t,
        move |state: &mut State, up, tx| {
            let next = stepped_value(state.window.settings.autosave.draft(&setting), step, up);
            autosave::apply(state, &setting, &next, tx);
        },
    )
}

/// The Saved mark a field shows once its value lands.
pub(super) fn saved_mark(state: &State, key: &str, t: &Tokens) -> Option<El> {
    state
        .window
        .settings
        .autosave
        .shows_saved_mark(key)
        .then(|| saved(format!("settings.saved.{key}"), t))
}

/// `typed` stepped once by `step`, up or down: a number field's arrows. An empty or unreadable
/// field steps from zero.
pub(super) fn stepped_value(typed: &str, step: f64, up: bool) -> String {
    let now = typed.trim().parse::<f64>().unwrap_or(0.0);
    let next = if up { now + step } else { now - step };
    if next.fract() == 0.0 {
        format!("{}", next as i64)
    } else {
        format!("{next}")
    }
}

/// The refusals the page draws beside their controls, by setting, with where a refused close
/// leaves the keyboard. Each drawing of the dialog sets them anew.
#[derive(Default)]
pub(crate) struct Anchors(RefCell<Vec<(String, String)>>);

impl Anchors {
    fn anchor(&self, key: &str, focus: String) {
        self.0.borrow_mut().push((key.to_owned(), focus));
    }

    pub(super) fn clear(&self) {
        self.0.borrow_mut().clear();
    }

    /// Where a refused close leaves the keyboard for `key`: beside its control when the page
    /// draws its refusal there, else the page's Retry.
    pub(super) fn focus(&self, key: &str) -> String {
        self.0
            .borrow()
            .iter()
            .find(|(anchored, _)| anchored == key)
            .map_or_else(|| retry_key(key), |(_, focus)| focus.clone())
    }

    fn has(&self, key: &str) -> bool {
        self.0.borrow().iter().any(|(anchored, _)| anchored == key)
    }
}

/// The refusals no control on the page shows beside itself, for the page's status line. Call
/// it once the page is drawn.
pub(super) fn unanchored_refusals(state: &State, t: &Tokens) -> Vec<El> {
    let dialog = &state.window.settings;
    dialog
        .autosave
        .refusals()
        .filter(|(key, _)| !dialog.anchors.has(key))
        .map(|(key, reason)| refusal_line(key.to_owned(), reason, t))
        .collect()
}

fn retry_key(key: &str) -> String {
    format!("settings.retry.{key}")
}

/// Why the daemon refused `key`'s value, with Retry, beside the field keyed for `key`, where a
/// refused close leaves the keyboard.
pub(super) fn refusal(state: &State, key: &str, t: &Tokens) -> Option<El> {
    refusal_beside(state, key, super::field_key(key), t)
}

/// Why the daemon refused `key`'s value, with Retry, beside the control that edits it, which
/// keeps the refusal off the page's status line. A refused close leaves the keyboard on `focus`.
pub(super) fn refusal_beside(state: &State, key: &str, focus: String, t: &Tokens) -> Option<El> {
    state.window.settings.anchors.anchor(key, focus);
    let reason = state
        .window
        .settings
        .autosave
        .field(key)?
        .error
        .as_deref()?;
    Some(refusal_line(key.to_owned(), reason, t))
}

fn refusal_line(key: String, reason: &str, t: &Tokens) -> El {
    let retried = key.clone();
    Refusal::new(format!("settings.refusal.{key}"), reason)
        .retry(
            Button::new("Retry")
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    autosave::commit(state, &retried, tx)
                })
                .build(t)
                .key(retry_key(&key))
                .label("Retry"),
        )
        .build(t)
}

/// The choice among `options` for the setting `key`, saved when one is picked.
pub(super) fn segmented(
    key: &str,
    label: &str,
    options: &'static [(&'static str, &'static str)],
    chosen: &str,
    t: &Tokens,
    choose: fn(&mut State, &'static str, &mut Tx),
) -> Control {
    Segmented::new(format!("settings.choice.{key}"), label)
        .options(options.iter().enumerate().map(|(i, &(value, name))| {
            Segment::new(format!("segment{i}"), name)
                .chosen(value == chosen)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| choose(state, value, tx))
        }))
        .build(t)
}

/// The switch in a feature page's head, which turns the feature on and off.
pub(super) fn feature_switch(
    state: &State,
    key: &'static str,
    show_state: bool,
    t: &Tokens,
) -> Control {
    let feature = feature(key);
    let on = setting::on(&state.facts.settings, key);
    switch(
        format!("settings.feature.{key}"),
        feature.name,
        on,
        show_state,
        false,
        t,
        move |state: &mut State, tx: &mut Tx| {
            autosave::apply(state, key, if on { "false" } else { "true" }, tx)
        },
    )
}

/// The declared feature `key`.
pub(super) fn feature(key: &str) -> &'static attn_protocol::go_values::Feature {
    FEATURES
        .iter()
        .find(|f| f.key == key)
        .expect("a settings page names a declared feature")
}

/// What a feature's switch also does and when a change starts, with the shortcut that opens
/// the feature where it has one.
pub(super) fn feature_note(state: &State, key: &str, t: &Tokens) -> Option<El> {
    let feature = feature(key);
    let shortcut = (key == "asks_enabled")
        .then(|| state.keymap.binding(&crate::Action::DockAsks))
        .flatten()
        .map(|binding| state.keymap.label(binding));
    let rest: Vec<&str> = [feature.note, feature.timing]
        .into_iter()
        .filter(|s| !s.is_empty())
        .collect();
    let said = |s: String| words(s, Type::Small, t.text_muted);
    let key = format!("settings.feature-note.{key}");
    match shortcut {
        Some(chord) => {
            let sentence = format!("Open it with {chord}.");
            let heard = [sentence.as_str()]
                .into_iter()
                .chain(rest.iter().copied())
                .collect::<Vec<_>>()
                .join(" ");
            Some(
                Stack::tight()
                    .key(key)
                    .label(heard)
                    .child(
                        Inline::tight()
                            .child(said("Open it with".into()).a11y_hidden())
                            .child(keycap::Keys::ledged(&[chord]).build(t).a11y_hidden()),
                    )
                    .children(
                        (!rest.is_empty()).then(|| said(rest.join(" ")).wrap().a11y_hidden()),
                    ),
            )
        }
        None if !rest.is_empty() => Some(said(rest.join(" ")).key(key).wrap()),
        None => None,
    }
}

/// The switch that turns the boolean setting `key` on and off, saved as soon as it flips.
pub(super) fn setting_switch(state: &State, key: &'static str, label: &str, t: &Tokens) -> Control {
    let on = setting::on(&state.facts.settings, key);
    switch(
        format!("settings.switch.{key}"),
        label,
        on,
        false,
        false,
        t,
        move |state: &mut State, tx: &mut Tx| {
            autosave::apply(state, key, if on { "false" } else { "true" }, tx)
        },
    )
}
