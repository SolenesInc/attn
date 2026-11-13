use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, el, row, text, text_input};
use rune_kit::{IconButton, IconButtonVariant};

use super::pages::{CAPTION, S4};
use super::sections::{self, Section};
use super::{appearance, bus, pages, tasks};
use crate::State;
use crate::keys::FIELD_KEY_SUFFIX;
use crate::setting;

const SEARCH_FIELD: &str = "settings.search.field";

/// The settings dialog over the window, while it is open.
pub(crate) fn dialog(state: &State, t: &Tokens) -> Option<El> {
    if !super::is_open(state) {
        return None;
    }
    debug_assert!(SEARCH_FIELD.ends_with(FIELD_KEY_SUFFIX));
    let panel = col()
        .key("settings.dialog")
        .role("dialog")
        .label("Settings")
        .modal_tx(true, super::close)
        .w_full()
        .max_w(960.0)
        .h_full()
        .max_h(720.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(header(state, t))
        .child(
            row()
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .child(nav(state, t))
                .child(body(state, t)),
        );
    Some(
        el().key("settings")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .child(
                el().key("settings.backdrop")
                    .role("button")
                    .label("Dismiss settings")
                    .absolute(0.0, 0.0)
                    .w_full()
                    .h_full()
                    .fill(t.bg_overlay)
                    .on_press_tx(super::close),
            )
            .child(
                col()
                    .w_full()
                    .h_full()
                    .padding(space::S6)
                    .center()
                    .child(panel),
            ),
    )
}

fn header(state: &State, t: &Tokens) -> El {
    let kit = attn_ui::kit(t);
    let search = text_input(&state.window.settings.search, type_::BODY)
        .key(SEARCH_FIELD)
        .single_line()
        .label("Search settings")
        .placeholder("Search settings", t.text_muted)
        .on_edit(|state: &mut State| &mut state.window.settings.search)
        .w(220.0)
        .px(space::S2)
        .py(space::S1)
        .radius(4.0)
        .fill(t.bg_input)
        .border(1.0, t.border)
        .color(t.text_primary);
    row()
        .key("settings.header")
        .w_full()
        .items_center()
        .gap(space::S3)
        .px(S4)
        .py(space::S3)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
        .child(text("Settings").font(type_::TITLE).color(t.text_primary))
        .child(
            text("local daemon")
                .font(CAPTION)
                .color(t.text_tertiary)
                .grow(1.0),
        )
        .child(search)
        .child(
            IconButton::new(kit.glyph.close)
                .variant(IconButtonVariant::Ghost)
                .label("Close settings")
                .on_press_tx(super::close)
                .build(kit)
                .key("settings.close"),
        )
}

fn nav(state: &State, t: &Tokens) -> El {
    let dialog = &state.window.settings;
    let groups = sections::filtered(&dialog.search.text);
    let nav = col()
        .key("settings.nav")
        .role("navigation")
        .label("Settings sections")
        .w(220.0)
        .h_full()
        .gap(space::S3)
        .padding(space::S3)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    if groups.is_empty() {
        return nav.child(
            text("No matching settings.")
                .font(CAPTION)
                .color(t.text_tertiary),
        );
    }
    nav.children(groups.into_iter().map(|(label, items)| {
        col()
            .gap(space::S1)
            .child(text(label).font(type_::SECTION).color(t.text_tertiary))
            .children(items.into_iter().map(|item| {
                let section = item.section;
                let active = dialog.section == section;
                let off = item
                    .feature
                    .is_some_and(|key| !setting::on(&state.facts.settings, key));
                row()
                    .key(format!("settings.nav.{}", section.id()))
                    .role("button")
                    .label(item.label)
                    .checked(active)
                    .focusable(true)
                    .w_full()
                    .h(28.0)
                    .px(space::S2)
                    .items_center()
                    .radius(4.0)
                    .fill(if active { t.bg_selected } else { t.bg_elevated })
                    .hover(t.bg_selected)
                    .child(
                        text(item.label)
                            .font(type_::BODY)
                            .color(t.text_primary)
                            .grow(1.0),
                    )
                    .children(off.then(|| {
                        text("off")
                            .key(format!("settings.nav-off.{}", section.id()))
                            .font(CAPTION)
                            .color(t.text_muted)
                    }))
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        super::select(state, section, tx)
                    })
            }))
    }))
}

fn body(state: &State, t: &Tokens) -> El {
    let section = state.window.settings.section;
    let item = section.item();
    let lead: Vec<El> = match item.feature {
        Some(key) => pages::feature_lines(state, key, t),
        None if !item.description.is_empty() => vec![
            text(item.description)
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        ],
        None => Vec::new(),
    };
    let head = row()
        .w_full()
        .gap(S4)
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(space::S1)
                .child(
                    text(item.label)
                        .key("settings.heading")
                        .role("heading")
                        .label(item.label)
                        .font(type_::TITLE)
                        .color(t.text_primary),
                )
                .children(lead),
        )
        .children(
            item.feature
                .map(|key| pages::feature_switch(state, key, true, t)),
        );
    let off = item
        .feature
        .is_some_and(|key| !setting::on(&state.facts.settings, key));
    let mut content = col()
        .key(format!("settings.section.{}", section.id()))
        .role("group")
        .label(item.label)
        .w_full()
        .gap(space::S2)
        .children(page(state, section, t));
    if off {
        content = content.opacity(0.45).inert();
    }
    col()
        .key("settings.body")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .gap(space::S3)
        .padding(S4)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .child(head)
        .child(status(state, t))
        .child(content)
}

/// Saving…, Saved, or the refusals no field shows beside itself.
fn status(state: &State, t: &Tokens) -> El {
    let saves = &state.window.settings.autosave;
    let beside_a_field = |key: &str| super::TEXT_FIELDS.iter().any(|(k, _)| *k == key);
    let refusals: Vec<El> = saves
        .refusals()
        .filter(|(key, _)| !beside_a_field(key))
        .map(|(key, reason)| pages::refusal_row(key.to_owned(), reason, t))
        .collect();
    let said = if saves.saving() {
        Some("Saving…")
    } else if saves.saved() {
        Some("Saved")
    } else {
        None
    };
    row()
        .key("settings.autosave")
        .role("status")
        .label(said.unwrap_or_default())
        .gap(space::S2)
        .items_center()
        .children(said.map(|s| text(s).font(CAPTION).color(t.text_tertiary)))
        .children(refusals)
}

fn page(state: &State, section: Section, t: &Tokens) -> Vec<El> {
    match section {
        Section::General => appearance::page(state, t),
        Section::Connectivity => pages::github_hosts(state, t),
        Section::Experimental => pages::experimental(state, t),
        Section::BackgroundTasks => tasks::page(state, state.window.now_ms, t),
        Section::EventBus => bus::page(state, state.window.now_ms, t),
        other => pages::not_yet(other.item().label, t),
    }
}
