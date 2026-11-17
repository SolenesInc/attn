//! The controls the settings pages share, and the pages with little of their own.

use std::cell::RefCell;

use attn_protocol::go_values::FEATURES;
use attn_ui::Tokens;
use attn_ui::keycap;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, Tx, col, row, text};
use rune_kit::{Button, TextInput};

use super::autosave;
use crate::State;

/// The small print under a setting, and a block's gap.
pub(super) const CAPTION: rune::TextStyle = rune::TextStyle::new(12.0, 400.0);
pub(super) const S4: f32 = 16.0;
use crate::setting;

/// A titled block of a page: its title and what it is for, then its controls.
pub(super) fn block(key: &str, title: &str, description: &str, t: &Tokens, body: Vec<El>) -> El {
    col()
        .key(format!("settings.block.{key}"))
        .role("group")
        .label(title.to_owned())
        .w_full()
        .gap(space::S2)
        .pb(S4)
        .child(
            text(title.to_owned())
                .font(type_::SECTION)
                .color(t.text_primary),
        )
        .children((!description.is_empty()).then(|| {
            text(description.to_owned())
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap()
        }))
        .child(col().w_full().gap(space::S2).children(body))
}

/// One setting in a block: its name and explanation on the left, its control on the right.
pub(super) fn row_card(title: &str, copy: &str, t: &Tokens, control: El) -> El {
    row()
        .w_full()
        .gap(S4)
        .items_center()
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(space::S1)
                .child(
                    text(title.to_owned())
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .children((!copy.is_empty()).then(|| {
                    text(copy.to_owned())
                        .font(CAPTION)
                        .color(t.text_tertiary)
                        .wrap()
                })),
        )
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
    Button::new(shown)
        .on_press_tx(press)
        .build(crate::settings::pages::kit(t))
        .key(key)
        .label(label.to_owned())
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
                .font(type_::LABEL)
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

/// The width of a field that holds a number.
pub(super) const NUMBER_FIELD: f32 = 160.0;
/// The width of a field that holds a path or a command.
pub(super) const PATH_FIELD: f32 = 320.0;

/// A field that edits the setting `key`: it saves on Enter and when the focus leaves it, and
/// shows its Saved mark and its refusal beside it.
pub(super) fn setting_field(
    state: &State,
    key: &'static str,
    label: &str,
    placeholder: &str,
    width: f32,
    t: &Tokens,
) -> El {
    let field_key = super::field_key(key);
    let field = state.window.settings.autosave.field(key);
    let empty = rune::TextEdit::default();
    let input = TextInput::new(field.map_or(&empty, |f| &f.draft))
        .key(field_key)
        .label(label.to_owned())
        .placeholder(placeholder.to_owned())
        .text_style(attn_ui::kit(t).ty.ui_compact.mono())
        .invalid(field.is_some_and(|f| f.error.is_some()))
        .on_edit(move |state: &mut State| state.window.settings.autosave.draft_mut(key))
        .on_submit(move |state: &mut State, tx: &mut Tx| autosave::commit(state, key, tx))
        .width(Some(width))
        .build(crate::settings::pages::kit(t));
    let saved = state
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
        });
    col()
        .items_end()
        .gap(space::S1)
        .child(
            row()
                .gap(space::S2)
                .items_center()
                .child(input)
                .children(saved),
        )
        .children(refusal(state, key, t))
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
    let feature = FEATURES
        .iter()
        .find(|f| f.key == key)
        .expect("a feature page names a declared feature");
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

/// A feature's summary line, then what its switch also does and when a change starts.
pub(super) fn feature_lines(state: &State, key: &str, t: &Tokens) -> Vec<El> {
    let Some(feature) = FEATURES.iter().find(|f| f.key == key) else {
        return Vec::new();
    };
    let shortcut = (key == "asks_enabled")
        .then(|| state.keymap.binding(&crate::Action::DockAsks))
        .flatten()
        .map(|binding| state.keymap.label(binding));
    let rest: Vec<&str> = [feature.note, feature.timing]
        .into_iter()
        .filter(|s| !s.is_empty())
        .collect();
    let mut lines = vec![
        text(feature.summary)
            .font(type_::BODY)
            .color(t.text_secondary)
            .wrap(),
    ];
    let words = |s: String| text(s).font(CAPTION).color(t.text_tertiary);
    let key = format!("settings.feature-note.{key}");
    match shortcut {
        Some(chord) => {
            let sentence = format!("Open it with {chord}.");
            let said = [sentence.as_str()]
                .into_iter()
                .chain(rest.iter().copied())
                .collect::<Vec<_>>()
                .join(" ");
            lines.push(
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
            );
        }
        None if !rest.is_empty() => lines.push(words(rest.join(" ")).key(key).wrap()),
        None => {}
    }
    lines
}

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
        .map(|group| {
            let features: Vec<_> = experimental.iter().filter(|f| f.group == group).collect();
            let on = features
                .iter()
                .filter(|f| setting::on(&state.facts.settings, f.key))
                .count();
            let count = format!("{on} of {} on", features.len());
            row()
                .key(format!("settings.feature-group.{group}"))
                .role("group")
                .label(format!("{group}, {count}"))
                .w_full()
                .gap(S4)
                .pb(S4)
                .child(
                    col()
                        .w(140.0)
                        .gap(space::S1)
                        .child(type_::upper(group, type_::BOLD_CAPS).color(t.text_tertiary))
                        .child(text(count).font(type_::MONO_CAPTION).color(t.text_dimmed)),
                )
                .child(
                    col()
                        .grow(1.0)
                        .min_w(0.0)
                        .gap(space::S2)
                        .children(features.iter().map(|feature| {
                            row()
                                .w_full()
                                .gap(space::S3)
                                .items_center()
                                .padding(space::S3)
                                .radius(6.0)
                                .fill(t.bg_panel)
                                .border(1.0, t.border_subtle)
                                .child(
                                    col()
                                        .grow(1.0)
                                        .min_w(0.0)
                                        .gap(space::S1)
                                        .child(
                                            text(feature.name)
                                                .font(type_::LABEL)
                                                .color(t.text_primary),
                                        )
                                        .children(feature_lines(state, feature.key, t)),
                                )
                                .child(feature_switch(state, feature.key, false, t))
                        })),
                )
        })
        .collect()
}

pub(super) fn github_hosts(state: &State, t: &Tokens) -> Vec<El> {
    let github = &state.facts.github;
    let body = if let Some(reason) = github.polling_off_reason.as_deref() {
        vec![
            text(reason.to_owned())
                .key("settings.github.polling-off")
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        ]
    } else if github.hosts.is_empty() {
        vec![
            text("No authenticated hosts detected.")
                .font(type_::BODY)
                .color(t.text_secondary),
        ]
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
    let hint = github.polling_off_reason.is_none().then(|| {
        text("Add hosts with `gh auth login --hostname <host>`.")
            .font(CAPTION)
            .color(t.text_tertiary)
    });
    vec![block(
        "github",
        "GitHub Hosts",
        "Authenticated hosts used by PR actions, review lookup, and repository metadata.",
        t,
        body.into_iter().chain(hint).collect(),
    )]
}

/// A switch that turns the boolean setting `key` on and off, saved as soon as it flips.
pub(super) fn setting_switch(state: &State, key: &'static str, label: &str, t: &Tokens) -> El {
    let on = setting::on(&state.facts.settings, key);
    switch(
        format!("settings.switch.{key}"),
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
        Tone::Good => tinted(t.success, t.success.mix(t.text_primary, 0.27)),
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
