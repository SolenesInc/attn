use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};
use rune_kit::{Dialog, TextInput};

use super::pages::{CAPTION, S4};
use super::sections::{self, Section};
use super::{
    agents, analytics, appearance, background, bus, delegation, desktop, hygiene, models, pages,
    plugins, route, tasks, updates,
};
use crate::State;
use crate::setting;

const SEARCH_FIELD: &str = "settings.search.field";

/// The settings dialog over the window, while it is open.
pub(crate) fn dialog(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let k = attn_ui::kit(t);
    let search = TextInput::new(&state.window.settings.search)
        .key(SEARCH_FIELD)
        .label("Search settings")
        .placeholder("Search settings")
        .on_edit(|state: &mut State| &mut state.window.settings.search)
        .width(Some(220.0))
        .build(k);
    let content = row()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border)
        .child(nav(state, t))
        .child(body(state, t));
    let mut dialog = Dialog::new("settings", "Settings")
        .description("local daemon")
        .open(super::is_open(state))
        .width(960.0)
        .max_height(720.0)
        .header(search)
        .on_close_tx(super::close)
        .child(content);
    if let Some(toast) = analytics::toast(state, t) {
        dialog = dialog.child(toast);
    }
    dialog.build(k, layers);
    if super::is_open(state) {
        delegation::overlay(state, t, layers);
        route::overlay(state, t, layers);
        agents::overlay(state, t, layers);
    }
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
    state.window.settings.anchors.clear();
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
        )
        .child(
            row()
                .key("settings.pills")
                .gap(space::S2)
                .items_center()
                .children(pills(state, section, t)),
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
    let refusals = pages::unanchored_refusals(state, t);
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
        Section::Analytics => analytics::page(state, t),
        Section::BackgroundAgents => background::page(state, t),
        Section::Delegation => delegation::page(state, t),
        Section::Agents => agents::page(state, t),
        Section::Models => models::page(state, t),
        Section::Updates => updates::page(state, t),
        Section::Desktop => desktop::page(state, t),
        Section::Hygiene => hygiene::page(state, t),
        Section::Plugins => plugins::page(state, t),
        other => pages::not_yet(other.item().label, t),
    }
}

/// The page's state beside its heading, for the pages that show one.
fn pills(state: &State, section: Section, t: &Tokens) -> Vec<El> {
    match section {
        Section::Desktop => desktop::pills(state, t),
        Section::Hygiene => hygiene::pills(state, t),
        Section::Plugins => plugins::pills(state, t),
        Section::Delegation => delegation::pill(state, t).into_iter().collect(),
        _ => Vec::new(),
    }
}
