//! The controls the settings pages share, and the pages with little of their own.

use attn_protocol::go_values::FEATURES;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text, text_input};
use rune_kit::{Button, ControlSize, SegmentedControl, Switch};

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
        .build(attn_ui::kit(t))
        .key(key)
        .label(label.to_owned())
}

/// A switch that reads On or Off when `show_state` asks for it.
pub(super) fn switch(
    key: String,
    label: &str,
    on: bool,
    show_state: bool,
    t: &Tokens,
    toggle: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let switch = Switch::new(on)
        .label(label)
        .hide_label()
        .control_size(ControlSize::Mini)
        .on_toggle_tx(toggle)
        .build(attn_ui::kit(t))
        .key(key);
    row()
        .gap(space::S2)
        .items_center()
        .child(switch)
        .children(show_state.then(|| {
            text(if on { "On" } else { "Off" })
                .font(type_::LABEL)
                .color(t.text_secondary)
                .a11y_hidden()
        }))
}

/// A field that edits the setting `key`: it saves on Enter and when the focus leaves it, and
/// shows its Saved mark and its refusal beside it.
pub(super) fn setting_field(
    state: &State,
    key: &'static str,
    label: &str,
    placeholder: &str,
    t: &Tokens,
) -> El {
    let field_key = super::field_key(key);
    let draft = state.window.settings.autosave.field(key).map(|f| &f.draft);
    let empty = rune::TextEdit::default();
    let input = text_input(draft.unwrap_or(&empty), type_::BODY)
        .key(field_key)
        .single_line()
        .label(label.to_owned())
        .placeholder(placeholder.to_owned(), t.text_muted)
        .on_edit(move |state: &mut State| state.window.settings.autosave.draft_mut(key))
        .on_submit(move |state: &mut State, tx: &mut Tx| autosave::commit(state, key, tx))
        .w(160.0)
        .px(space::S2)
        .py(space::S1)
        .radius(4.0)
        .fill(t.bg_input)
        .border(1.0, t.border)
        .color(t.text_primary);
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

/// Why the daemon refused `key`'s value, with Retry, beside the control that edits it.
pub(super) fn refusal(state: &State, key: &'static str, t: &Tokens) -> Option<El> {
    let reason = state
        .window
        .settings
        .autosave
        .field(key)?
        .error
        .as_deref()?;
    Some(refusal_row(key.to_owned(), reason, t))
}

pub(super) fn refusal_row(key: String, reason: &str, t: &Tokens) -> El {
    let retry_key = key.clone();
    row()
        .key(format!("settings.refusal.{key}"))
        .role("alert")
        .label(reason.to_owned())
        .gap(space::S2)
        .items_center()
        .child(text(reason.to_owned()).font(CAPTION).color(t.error).wrap())
        .child(button(
            format!("settings.retry.{key}"),
            "Retry",
            t,
            move |state, tx| autosave::commit(state, &retry_key, tx),
        ))
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
    let names: Vec<&str> = options.iter().map(|&(_, name)| name).collect();
    let selected = options.iter().position(|&(value, _)| value == chosen);
    let control = SegmentedControl::new(&names, selected)
        .label(label)
        .fit()
        .on_pick(move |state: &mut State, i: usize, tx: &mut Tx| choose(state, options[i].0, tx))
        .build(attn_ui::kit(t))
        .key(format!("settings.choice.{key}"));
    row().child(control)
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
        .map(|binding| format!("Open it with {}.", state.keymap.label(binding)));
    let note: Vec<&str> = shortcut
        .as_deref()
        .into_iter()
        .chain([feature.note, feature.timing])
        .filter(|s| !s.is_empty())
        .collect();
    let mut lines = vec![
        text(feature.summary)
            .font(type_::BODY)
            .color(t.text_secondary)
            .wrap(),
    ];
    if !note.is_empty() {
        lines.push(
            text(note.join(" "))
                .key(format!("settings.feature-note.{key}"))
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap(),
        );
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
                        .child(text(group).font(type_::SECTION).color(t.text_tertiary))
                        .child(text(count).font(CAPTION).color(t.text_muted)),
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

/// A page another part of attn has not brought to the native app yet.
pub(super) fn not_yet(label: &str, t: &Tokens) -> Vec<El> {
    vec![
        text(format!("{label} is not in the native app yet."))
            .font(type_::BODY)
            .color(t.text_secondary),
    ]
}
