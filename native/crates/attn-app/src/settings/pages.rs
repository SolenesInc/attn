//! The controls the settings pages share, and the pages with little of their own.

use std::cell::RefCell;

use attn_protocol::go_values::FEATURES;
use attn_ui::Tokens;
use attn_ui::keycap;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, TextStyle, Tx, col, row, text};
use rune_kit::{Button, TextInput};

use super::autosave;
use crate::State;

/// The small print under a setting, and a block's gap.
pub(super) const CAPTION: rune::TextStyle = rune::TextStyle::new(12.0, 400.0);
pub(super) const S4: f32 = 16.0;
use crate::setting;

/// A block's heading, as the Tauri app's `.settings-block h3`.
pub(super) const BLOCK_HEADING: rune::TextStyle = rune::TextStyle::new(13.0, 700.0);
/// A row card's title, as the Tauri app's `.settings-row-title`.
const ROW_TITLE: rune::TextStyle = rune::TextStyle::new(13.0, 750.0);

/// A titled block of a page, as the Tauri app's `.settings-block`: its heading and what it is
/// for, then its controls.
pub(super) fn block(key: &str, title: &str, description: &str, t: &Tokens, body: Vec<El>) -> El {
    col()
        .key(format!("settings.block.{key}"))
        .role("group")
        .label(title.to_owned())
        .w_full()
        .gap(12.0)
        .pb(22.0)
        .child(
            col()
                .w_full()
                .gap(8.0)
                .child(
                    text(title.to_owned())
                        .font(BLOCK_HEADING)
                        .color(t.text_primary),
                )
                .children((!description.is_empty()).then(|| {
                    text(description.to_owned())
                        .font(type_::BODY)
                        .color(t.text_tertiary)
                        .wrap()
                })),
        )
        .child(col().w_full().gap(10.0).children(body))
}

/// A block that follows another on its page, set off by a rule as the Tauri app's
/// `.settings-block ~ .settings-block`.
pub(super) fn after_block(block: El, t: &Tokens) -> El {
    block.pt(20.0).border_sides([1.0, 0.0, 0.0, 0.0], t.border)
}

/// Blocks one after another, each after the first set off by a rule.
pub(super) fn blocks(blocks: Vec<El>, t: &Tokens) -> Vec<El> {
    blocks
        .into_iter()
        .enumerate()
        .map(|(i, block)| if i == 0 { block } else { after_block(block, t) })
        .collect()
}

/// The small print under a row card's title, as the Tauri app's `.settings-row-copy`.
pub(super) fn row_copy(copy: impl Into<String>, t: &Tokens) -> El {
    text(copy.into())
        .font(CAPTION)
        .color(t.text_secondary)
        .wrap()
}

/// A row card's title and its small print.
pub(super) fn row_words(title: &str, copy: &str, t: &Tokens) -> El {
    col()
        .grow(1.0)
        .min_w(0.0)
        .gap(4.0)
        .child(text(title.to_owned()).font(ROW_TITLE).color(t.text_primary))
        .children((!copy.is_empty()).then(|| row_copy(copy, t)))
}

/// The Tauri app's `.settings-row-card`: one setting on the page's raised surface, its name
/// and explanation on the left, its control on the right.
pub(super) fn row_card(title: &str, copy: &str, t: &Tokens, control: El) -> El {
    card_surface(row(), t)
        .w_full()
        .min_h(56.0)
        .gap(S4)
        .items_center()
        .py(12.0)
        .px(14.0)
        .child(row_words(title, copy, t))
        .child(control)
}

/// The raised surface of a settings card, as the Tauri app's row, plugin and price cards.
pub(super) fn card_surface(el: El, t: &Tokens) -> El {
    el.radius(7.0).fill(t.bg_elevated).border(1.0, t.border)
}

/// One setting as a plain row on the page, as the Tauri app's `.settings-default-row`: its
/// words, from [`row_words`], on the left and its control on the right.
pub(super) fn plain_row(words: El, control: El) -> El {
    row()
        .w_full()
        .gap(24.0)
        .items_center()
        .py(8.0)
        .child(words)
        .child(control)
}

pub(super) fn button(
    key: String,
    label: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    named_button(key, label, label, t, press)
}

/// A button that shows `shown` and is announced as `label`.
pub(super) fn named_button(
    key: String,
    label: &str,
    shown: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Action::new(shown)
        .on_press_tx(press)
        .build(t)
        .key(key)
        .label(label.to_owned())
}

/// The modifiers of the Tauri app's `.settings-action`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(super) enum Look {
    #[default]
    Plain,
    /// `.primary`: the page's one main action, filled `warning`.
    Primary,
    /// `.quiet`: no fill or edge until the pointer is over it.
    Quiet,
    /// `.danger`: an action that removes or turns something off.
    Danger,
    /// Delegation's `.quiet.danger`: a quiet action in `error`.
    QuietDanger,
}

/// The Tauri app's `.settings-action`, the button every settings page acts with: 38 points
/// tall, a `border_hover` edge, a `bg_button` fill and a bold label in `text_secondary`.
pub(super) struct Action {
    shown: String,
    look: Look,
    disabled: bool,
    height: f32,
    ink: Option<rune::Color>,
    button: Button,
}

/// A settings action's label and a switch's state word: native's 12 points, at the Tauri
/// app's weight.
const ACTION_LABEL: rune::TextStyle = rune::TextStyle::new(12.0, 700.0);

impl Action {
    pub(super) fn new(shown: impl Into<String>) -> Action {
        let shown = shown.into();
        Action {
            button: Button::new(shown.clone()),
            shown,
            look: Look::Plain,
            disabled: false,
            height: 38.0,
            ink: None,
        }
    }

    /// The button's height, 38 points unless set.
    pub(super) fn height(mut self, height: f32) -> Self {
        self.height = height;
        self
    }

    /// The label's color, the look's own unless set.
    pub(super) fn ink(mut self, ink: rune::Color) -> Self {
        self.ink = Some(ink);
        self
    }

    pub(super) fn look(mut self, look: Look) -> Self {
        self.look = look;
        self
    }

    pub(super) fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self.button = self.button.disabled(disabled);
        self
    }

    pub(super) fn help(mut self, help: impl Into<String>) -> Self {
        self.button = self.button.help(help);
        self
    }

    pub(super) fn on_press(mut self, press: impl Fn(&mut State) + 'static) -> Self {
        self.button = self.button.on_press(press);
        self
    }

    pub(super) fn on_press_tx(mut self, press: impl Fn(&mut State, &mut Tx) + 'static) -> Self {
        self.button = self.button.on_press_tx(press);
        self
    }

    pub(super) fn build(self, t: &Tokens) -> El {
        let white = t.highlight;
        let (fill, edge, ink, hover) = match self.look {
            Look::Plain => (t.bg_button, t.border_hover, t.text_secondary, t.bg_panel),
            Look::Primary => (
                t.warning,
                t.warning.alpha(0.8),
                attn_ui::warning_ink(t),
                attn_ui::colors::mix_srgb(t.warning, white, 0.3),
            ),
            Look::Quiet => (
                rune::Color::TRANSPARENT,
                rune::Color::TRANSPARENT,
                t.text_secondary,
                t.bg_button,
            ),
            Look::QuietDanger => (
                rune::Color::TRANSPARENT,
                rune::Color::TRANSPARENT,
                t.error,
                t.bg_button,
            ),
            Look::Danger => (
                t.bg_button,
                t.error.alpha(0.38),
                attn_ui::colors::mix_srgb(t.error, white, 0.24),
                t.bg_panel,
            ),
        };
        let ink = self.ink.unwrap_or(ink);
        let shown = self.shown;
        let button = self
            .button
            .variant(rune_kit::ButtonVariant::Ghost)
            .label_with(move |_, _| text(shown).font(ACTION_LABEL).color(ink))
            .build(kit(t))
            .h(self.height)
            .px(14.0)
            .radius(6.0)
            .fill(fill)
            .border(1.0, edge);
        if self.disabled {
            button
        } else {
            button.hover(hover)
        }
    }
}

/// The kit the settings pages build with: the Tauri app's settings paint primary actions in
/// `warning`, where the rest of the app paints them in `brand`.
pub(super) fn kit(t: &Tokens) -> &'static rune_kit::Kit {
    attn_ui::kit(&Tokens {
        brand: t.warning,
        ..*t
    })
}

/// A switch that reads On or Off when `show_state` asks for it, drawn as the Tauri app's
/// `.settings-switch`: a 36 by 20 track, `warning` when on.
pub(super) fn switch(
    key: String,
    label: &str,
    on: bool,
    show_state: bool,
    t: &Tokens,
    toggle: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let (track, edge, knob) = if on {
        (t.warning, t.warning, attn_ui::warning_ink(t))
    } else {
        (t.bg_button, t.border_hover, t.text_tertiary)
    };
    let knob = rune::el()
        .key("thumb")
        .size(14.0, 14.0)
        .radius(7.0)
        .absolute(if on { 19.0 } else { 3.0 }, 3.0)
        .fill(knob);
    let track = rune::el()
        .key("control")
        .size(36.0, 20.0)
        .radius(10.0)
        .fill(track)
        .border(1.0, edge)
        .child(knob)
        .a11y_hidden();
    row()
        .key(key)
        .role("switch")
        .label(label.to_owned())
        .checked(on)
        .focusable(true)
        .focus_ring(2.0, 2.0, t.warning)
        .gap(10.0)
        .items_center()
        .child(track)
        .children(show_state.then(|| {
            text(if on { "On" } else { "Off" })
                .font(ACTION_LABEL)
                .color(t.text_secondary)
                .a11y_hidden()
        }))
        .on_press_tx(toggle)
}

/// The track of the Tauri app's `.settings-segmented`, as wide as its options.
pub(super) fn segment_track(key: impl Into<String>, label: &str, t: &Tokens) -> El {
    row()
        .key(key.into())
        .role("radiogroup")
        .label(label.to_owned())
        .focus_group()
        .gap(6.0)
        .padding(5.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
}

/// One option of a segment track; the chosen one is `warning`, as `.settings-segmented-option`.
pub(super) fn segment(key: String, name: &str, chosen: bool, t: &Tokens) -> El {
    let (fill, ink) = if chosen {
        (t.warning, attn_ui::warning_ink(t))
    } else {
        (rune::Color::TRANSPARENT, t.text_secondary)
    };
    let mut option = row()
        .key(key)
        .role("radio")
        .label(name.to_owned())
        .checked(chosen)
        .focusable(true)
        .focus_selected(chosen)
        .focus_ring(2.0, 1.0, t.warning)
        .h(32.0)
        .px(12.0)
        .center()
        .radius(6.0)
        .fill(fill)
        .child(
            text(name.to_owned())
                .font(rune::TextStyle {
                    weight: 700.0,
                    ..type_::BODY
                })
                .color(ink)
                .a11y_hidden(),
        );
    if chosen {
        option = option.border(1.0, t.warning.alpha(0.8));
    } else {
        option = option.hover(t.bg_button);
    }
    option
}

/// The Tauri app's `.settings-input`: 38 points tall, monospace, on `bg_input`, with its
/// placeholder in `text_dimmed`.
pub(super) fn input(field: TextInput, t: &Tokens) -> El {
    let dimmed_placeholder = attn_ui::kit(&Tokens {
        brand: t.warning,
        text_tertiary: t.text_dimmed,
        ..*t
    });
    field
        .text_style(dimmed_placeholder.ty.ui_compact.mono())
        .height(38.0)
        .build(dimmed_placeholder)
        .fill(t.bg_input)
        .pl(10.0)
}

/// The width of a field that holds a number.
pub(super) const NUMBER_FIELD: f32 = 160.0;

/// A field `width` points wide that edits the number setting `key`, with arrows that step it
/// by `step`: it saves on Enter, when the focus leaves it and on each step, and shows its Saved
/// mark beside it and its refusal under it.
pub(super) fn number_field(
    state: &State,
    key: &'static str,
    label: &str,
    placeholder: &str,
    width: f32,
    step: f64,
    t: &Tokens,
) -> El {
    let field = state.window.settings.autosave.field(key);
    let empty = rune::TextEdit::default();
    let draft = TextInput::new(field.map_or(&empty, |f| &f.draft))
        .key(super::field_key(key))
        .label(label.to_owned())
        .placeholder(placeholder.to_owned())
        .invalid(field.is_some_and(|f| f.error.is_some()))
        .on_edit(move |state: &mut State| state.window.settings.autosave.draft_mut(key))
        .on_submit(move |state: &mut State, tx: &mut Tx| autosave::commit(state, key, tx))
        .trailing(setting_arrows(key, step, t))
        .width(Some(width));
    col()
        .items_end()
        .gap(space::S1)
        .child(
            row()
                .gap(space::S2)
                .items_center()
                .child(input(draft, t))
                .children(saved_mark(state, key, t)),
        )
        .children(refusal(state, key, t))
}

/// The arrows that step the number setting `key` by `step` and save it.
pub(super) fn setting_arrows(key: &str, step: f64, t: &Tokens) -> El {
    let setting = key.to_owned();
    arrows(&super::field_key(key), t, move |state, up, tx| {
        let next = stepped_value(state.window.settings.autosave.draft(&setting), step, up);
        autosave::apply(state, &setting, &next, tx);
    })
}

/// The Saved mark a field shows beside itself once its value lands.
fn saved_mark(state: &State, key: &str, t: &Tokens) -> Option<El> {
    state
        .window
        .settings
        .autosave
        .shows_saved_mark(key)
        .then(|| {
            text("Saved")
                .key(format!("settings.saved.{key}"))
                .role("status")
                .label("Saved")
                .font(CAPTION)
                .color(t.success)
        })
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

/// A field's name above it, as the Tauri app's `.settings-label`.
pub(super) fn field_label(said: &str, t: &Tokens) -> El {
    type_::upper(said, type_::BOLD_CAPS).color(t.text_dimmed)
}

/// A field that edits the setting `key` and fills its row, as the Tauri app's
/// `.settings-input { width: 100% }`: it saves on Enter and when the focus leaves it, steps by
/// `step` with up and down arrows when set, and shows its Saved mark, then `beside`, to its right
/// and its refusal under it.
pub(super) fn wide_setting_field(
    state: &State,
    key: &'static str,
    label: &str,
    placeholder: &str,
    step: Option<f64>,
    beside: Option<El>,
    t: &Tokens,
) -> El {
    let field = state.window.settings.autosave.field(key);
    let empty = rune::TextEdit::default();
    let mut draft = TextInput::new(field.map_or(&empty, |f| &f.draft))
        .key(super::field_key(key))
        .label(label.to_owned())
        .placeholder(placeholder.to_owned())
        .invalid(field.is_some_and(|f| f.error.is_some()))
        .on_edit(move |state: &mut State| state.window.settings.autosave.draft_mut(key))
        .on_submit(move |state: &mut State, tx: &mut Tx| autosave::commit(state, key, tx))
        .width(None);
    if let Some(step) = step {
        draft = draft.trailing(setting_arrows(key, step, t));
    }
    col()
        .w_full()
        .gap(7.0)
        .child(
            row()
                .w_full()
                .gap(8.0)
                .items_center()
                .child(col().grow(1.0).min_w(0.0).child(input(draft, t)))
                .children(saved_mark(state, key, t))
                .children(beside),
        )
        .children(refusal(state, key, t))
}

/// The up and down arrows a number input shows at its right edge, keyed `{key}.up` and
/// `{key}.down`; `step` runs with `true` for up.
pub(super) fn arrows(
    key: &str,
    t: &Tokens,
    step: impl Fn(&mut State, bool, &mut Tx) + Clone + 'static,
) -> El {
    let glyph = &kit(t).glyph;
    let arrow = |up: bool| {
        let step = step.clone();
        row()
            .key(format!("{key}.{}", if up { "up" } else { "down" }))
            .role("button")
            .label(if up { "Increase" } else { "Decrease" })
            .w(14.0)
            .h(9.0)
            .center()
            .child(
                rune::icon(
                    if up {
                        glyph.chevron_up
                    } else {
                        glyph.chevron_down
                    },
                    8.0,
                )
                .color(t.text_secondary)
                .a11y_hidden(),
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| step(state, up, tx))
    };
    col()
        .h(19.0)
        .w(14.0)
        .mr(4.0)
        .radius(3.0)
        .border(1.0, t.border)
        .child(arrow(true))
        .child(arrow(false))
}

/// The refusals the page draws beside their controls, by setting, with where a refused close
/// leaves the keyboard: Tauri's anchored refusals. Each drawing of the dialog sets them anew.
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
    /// draws its refusal there, else the footer's Retry.
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

/// The refusals no control on the page shows beside itself, for the dialog's footer. Call it
/// once the page is drawn.
pub(super) fn unanchored_refusals(state: &State, t: &Tokens) -> Vec<El> {
    let dialog = &state.window.settings;
    dialog
        .autosave
        .refusals()
        .filter(|(key, _)| !dialog.anchors.has(key))
        .map(|(key, reason)| refusal_row(key.to_owned(), reason, t))
        .collect()
}

fn retry_key(key: &str) -> String {
    format!("settings.retry.{key}")
}

/// Why the daemon refused `key`'s value, with Retry, beside the control that edits it: the
/// field keyed for `key`, where a refused close leaves the keyboard.
pub(super) fn refusal(state: &State, key: &str, t: &Tokens) -> Option<El> {
    refusal_beside(state, key, super::field_key(key), t)
}

/// Why the daemon refused `key`'s value, with Retry, beside the control that edits it, which
/// keeps the refusal out of the dialog's footer. A refused close leaves the keyboard on
/// `focus`.
pub(super) fn refusal_beside(state: &State, key: &str, focus: String, t: &Tokens) -> Option<El> {
    state.window.settings.anchors.anchor(key, focus);
    let reason = state
        .window
        .settings
        .autosave
        .field(key)?
        .error
        .as_deref()?;
    Some(refusal_row(key.to_owned(), reason, t))
}

fn refusal_row(key: String, reason: &str, t: &Tokens) -> El {
    let retried = key.clone();
    row()
        .key(format!("settings.refusal.{key}"))
        .role("alert")
        .label(reason.to_owned())
        .gap(space::S2)
        .items_center()
        .child(text(reason.to_owned()).font(CAPTION).color(t.error).wrap())
        .child(button(retry_key(&key), "Retry", t, move |state, tx| {
            autosave::commit(state, &retried, tx)
        }))
}

/// The choice among `options` for the setting `key`, saved when one is picked.
pub(super) fn segmented(
    key: &str,
    label: &str,
    options: &'static [(&'static str, &'static str)],
    chosen: &str,
    t: &Tokens,
    choose: fn(&mut State, &'static str, &mut Tx),
) -> El {
    let track = segment_track(format!("settings.choice.{key}"), label, t).children(
        options.iter().enumerate().map(|(i, &(value, name))| {
            segment(format!("segment{i}"), name, value == chosen, t)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| choose(state, value, tx))
        }),
    );
    row().child(track)
}

/// The switch in a feature page's head, which turns the feature on and off.
pub(super) fn feature_switch(state: &State, key: &'static str, show_state: bool, t: &Tokens) -> El {
    let feature = feature(key);
    let on = setting::on(&state.facts.settings, key);
    switch(
        format!("settings.feature.{key}"),
        feature.name,
        on,
        show_state,
        t,
        move |state, tx| autosave::apply(state, key, if on { "false" } else { "true" }, tx),
    )
}

/// The declared feature `key`.
pub(super) fn feature(key: &str) -> &'static attn_protocol::go_values::Feature {
    FEATURES
        .iter()
        .find(|f| f.key == key)
        .expect("a settings page names a declared feature")
}

/// What a feature's switch also does and when a change starts, as the Tauri app's
/// `.settings-flag-note`: small print in `text_muted` under the feature's summary.
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
    let words = |s: String| text(s).font(CAPTION).color(t.text_muted);
    let key = format!("settings.feature-note.{key}");
    match shortcut {
        Some(chord) => {
            let sentence = format!("Open it with {chord}.");
            let said = [sentence.as_str()]
                .into_iter()
                .chain(rest.iter().copied())
                .collect::<Vec<_>>()
                .join(" ");
            Some(
                row()
                    .key(key)
                    .label(said)
                    .flex_wrap()
                    .items_center()
                    .gaps(2.0, 4.0)
                    .child(words("Open it with".into()).a11y_hidden())
                    .child(keycap::Keys::ledged(&[chord]).build(t).a11y_hidden())
                    .child(words(".".into()).ml(-4.0).a11y_hidden())
                    .children(
                        (!rest.is_empty())
                            .then(|| words(rest.join(" ")).wrap().shrink().a11y_hidden()),
                    ),
            )
        }
        None if !rest.is_empty() => Some(words(rest.join(" ")).key(key).wrap()),
        None => None,
    }
}

/// An experimental feature's name, as the Tauri app's `.settings-flag-name`.
const FLAG_NAME: rune::TextStyle = rune::TextStyle::new(14.0, 600.0);

pub(super) fn experimental(state: &State, t: &Tokens) -> Vec<El> {
    let experimental: Vec<_> = FEATURES
        .iter()
        .filter(|f| f.stage == "experimental")
        .collect();
    let mut groups: Vec<&str> = Vec::new();
    for feature in &experimental {
        if !groups.contains(&feature.group) {
            groups.push(feature.group);
        }
    }
    groups
        .into_iter()
        .enumerate()
        .map(|(i, group)| {
            let features: Vec<_> = experimental.iter().filter(|f| f.group == group).collect();
            let on = features
                .iter()
                .filter(|f| setting::on(&state.facts.settings, f.key))
                .count();
            let count = format!("{on} of {} on", features.len());
            let gutter = col()
                .w(132.0)
                .gap(6.0)
                .pt(12.0)
                .child(type_::upper(group, type_::BOLD_CAPS).color(t.text_tertiary))
                .child(
                    row()
                        .label(count.clone())
                        .child(
                            text(on.to_string())
                                .font(TextStyle {
                                    weight: 500.0,
                                    ..type_::MONO_CAPTION
                                })
                                .color(t.warning)
                                .a11y_hidden(),
                        )
                        .child(
                            text(format!(" of {} on", features.len()))
                                .font(type_::MONO_CAPTION)
                                .color(t.text_dimmed)
                                .a11y_hidden(),
                        ),
                );
            let group_row = row()
                .key(format!("settings.feature-group.{group}"))
                .role("group")
                .label(format!("{group}, {count}"))
                .w_full()
                .gap(20.0)
                .pb(14.0)
                .child(gutter)
                .child(
                    col()
                        .grow(1.0)
                        .min_w(0.0)
                        .children(features.iter().map(|feature| flag(state, feature, t))),
                );
            if i == 0 {
                group_row
            } else {
                group_row
                    .pt(18.0)
                    .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
            }
        })
        .collect()
}

/// One experimental feature, as the Tauri app's `.settings-flag`: a flat row whose name and
/// summary stay quiet while it is off, washed in amber from its left edge while it is on.
fn flag(state: &State, feature: &attn_protocol::go_values::Feature, t: &Tokens) -> El {
    let on = setting::on(&state.facts.settings, feature.key);
    let (name_ink, summary_ink) = if on {
        (t.text_primary, t.text_secondary)
    } else {
        (t.text_secondary, t.text_tertiary)
    };
    let words = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(4.0)
        .child(text(feature.name).font(FLAG_NAME).color(name_ink))
        .child(
            text(feature.summary)
                .font(type_::BODY)
                .color(summary_ink)
                .wrap(),
        )
        .children(feature_note(state, feature.key, t));
    let row = row()
        .key(format!("settings.flag.{}", feature.key))
        .w_full()
        .gap(24.0)
        .items_start()
        .pt(10.0)
        .pb(11.0)
        .px(12.0)
        .child(words)
        .child(feature_switch(state, feature.key, false, t).mt(1.0));
    if on {
        row.linear_gradient(
            std::f32::consts::FRAC_PI_2,
            [
                (0.0, attn_ui::colors::mix_srgb(t.bg_panel, t.warning, 0.07)),
                (0.45, t.bg_panel),
            ],
        )
    } else {
        row
    }
}

pub(super) fn github_hosts(state: &State, t: &Tokens) -> Vec<El> {
    let github = &state.facts.github;
    let body = if let Some(reason) = github.polling_off_reason.as_deref() {
        vec![
            empty(reason.to_owned(), t)
                .key("settings.github.polling-off")
                .wrap(),
        ]
    } else if github.hosts.is_empty() {
        vec![empty("No authenticated hosts detected.", t)]
    } else {
        vec![
            row()
                .key("settings.github.hosts")
                .role("list")
                .label("GitHub hosts")
                .gap(space::S2)
                .children(github.hosts.iter().map(|host| {
                    text(host.clone())
                        .role("listitem")
                        .label(host.clone())
                        .font(type_::MONO_CAPTION)
                        .color(t.text_primary)
                        .px(space::S2)
                        .py(space::S1)
                        .radius(4.0)
                        .fill(t.bg_button)
                })),
        ]
    };
    let hint = github
        .polling_off_reason
        .is_none()
        .then(|| hint("Add hosts with `gh auth login --hostname <host>`.", t));
    vec![block(
        "github",
        "GitHub Hosts",
        "Authenticated hosts used by PR actions, review lookup, and repository metadata.",
        t,
        body.into_iter().chain(hint).collect(),
    )]
}

/// A line that says a list or a page has nothing in it, as the Tauri app's `.settings-empty`.
pub(super) fn empty(said: impl Into<String>, t: &Tokens) -> El {
    text(said.into()).font(type_::BODY).color(t.text_tertiary)
}

/// A hint beside a control, as the Tauri app's `.settings-hint`.
pub(super) fn hint(said: impl Into<String>, t: &Tokens) -> El {
    text(said.into()).font(CAPTION).color(t.text_muted)
}

/// A button that reads Enable or Disable and is announced as the switch `name`, as the Tauri
/// app's `.settings-action[role=switch]`.
pub(super) fn toggle(
    key: &str,
    name: &str,
    on: bool,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Action::new(if on { "Disable" } else { "Enable" })
        .disabled(disabled)
        .on_press_tx(press)
        .build(t)
        .key(key.to_owned())
        .role("switch")
        .label(name.to_owned())
        .checked(on)
}

/// A toggle that turns the boolean setting `key` on and off, saved as soon as it flips.
pub(super) fn setting_switch(state: &State, key: &'static str, label: &str, t: &Tokens) -> El {
    let on = setting::on(&state.facts.settings, key);
    toggle(
        &format!("settings.switch.{key}"),
        label,
        on,
        false,
        t,
        move |state, tx| autosave::apply(state, key, if on { "false" } else { "true" }, tx),
    )
}

/// How a pill reads: good news, something to look at, a problem, or a plain count.
#[derive(Clone, Copy)]
pub(super) enum Tone {
    Good,
    Warn,
    Bad,
    Plain,
}

/// A small status marker: a page's state beside its heading, or a plugin's.
pub(super) fn pill(key: String, label: String, tone: Tone, t: &Tokens) -> El {
    let (ink, fill, edge) = match tone {
        Tone::Plain => (t.text_secondary, t.bg_elevated, t.border),
        Tone::Good => tinted(
            t.success,
            attn_ui::colors::mix_srgb(t.text_primary, t.success, 0.73),
        ),
        Tone::Warn => tinted(t.warning, t.warning),
        Tone::Bad => tinted(t.error, t.error),
    };
    type_::upper(&label, type_::BADGE)
        .key(key)
        .role("status")
        .color(ink)
        .px(space::S2)
        .py(3.0)
        .radius(999.0)
        .fill(fill)
        .border(1.0, edge)
}

/// A pill's ink, fill and border on `tone`, as the Tauri app's `.settings-pill.good`.
fn tinted(tone: rune::Color, ink: rune::Color) -> (rune::Color, rune::Color, rune::Color) {
    (ink, tone.alpha(0.11), tone.alpha(0.42))
}

/// A warning line, as the Tauri pages show a plugin's trouble.
pub(super) fn warning(key: String, message: String, t: &Tokens) -> El {
    text(message.clone())
        .key(key)
        .role("alert")
        .label(message)
        .font(CAPTION)
        .color(t.warning)
        .wrap()
}
