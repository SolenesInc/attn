use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{Color, El, TextStyle, Tx, col, el, row, text};
use rune_kit::{ScrollBar, TextInput};

use super::pages::{CAPTION, S4};
use super::sections::{self, Section};
use super::{
    agents, analytics, appearance, background, bus, delegation, desktop, hygiene, models, outposts,
    pages, phones, plugins, route, senders, tasks, updates,
};
use crate::State;
use crate::setting;

const SEARCH_FIELD: &str = "settings.search.field";

/// The Tauri app's `.settings-modal`: as large as the window allows up to 1380 by 880 points,
/// 20 points in from its edges.
const MAX_WIDTH: f32 = 1380.0;
const MAX_HEIGHT: f32 = 880.0;
const INSET: f32 = 20.0;
const HEADER_HEIGHT: f32 = 58.0;
const NAV_WIDTH: f32 = 258.0;
const CONTROL_HEIGHT: f32 = 34.0;
const SEARCH_WIDTH: f32 = 280.0;

/// The settings modal over the whole window, while it is open.
pub(crate) fn dialog(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    if !super::is_open(state) {
        return;
    }
    let k = attn_ui::kit(t);
    let content = row()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .fill(t.bg_panel)
        .child(nav(state, t))
        .child(body(state, t));
    let panel = col()
        .key("settings-dialog")
        .role("dialog")
        .label("Settings")
        .description("local daemon")
        .modal_tx(true, super::close)
        .autofocus()
        .w_full()
        .max_w(MAX_WIDTH)
        .h_full()
        .max_h(MAX_HEIGHT)
        .radius(8.0)
        .clip_rounded(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_hover)
        .shadow(28.0, 80.0, t.shadow.alpha(0.48))
        .token_motion(k.motion.overlay)
        .child(header(state, t))
        .child(content)
        .children(analytics::toast(state, t));
    layers.push(
        el().key("settings-scrim")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .fill(t.bg_overlay)
            .token_motion(k.motion.overlay)
            .a11y_hidden(),
    );
    layers.push(
        el().key("settings-layer")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .padding(INSET)
            .center()
            .child(panel),
    );
    delegation::overlay(state, t, layers);
    route::overlay(state, t, layers);
    agents::overlay(state, t, layers);
}

/// The Tauri app's `.settings-header`: the title on the left, the search field and the close
/// button on the right, all on one line.
fn header(state: &State, t: &Tokens) -> El {
    let search = TextInput::new(&state.window.settings.search)
        .key(SEARCH_FIELD)
        .label("Search settings")
        .placeholder("Search settings")
        .on_edit(|state: &mut State| &mut state.window.settings.search)
        .text_style(CAPTION)
        .plain()
        .width(None)
        .height(CONTROL_HEIGHT - 2.0)
        .build(attn_ui::kit(t));
    let search = row()
        .w(SEARCH_WIDTH)
        .h(CONTROL_HEIGHT)
        .px(10.0)
        .items_center()
        .radius(6.0)
        .fill(t.bg_input)
        .border(1.0, t.border)
        .focus_within_ring(1.0, -1.0, t.border_focus)
        .child(search);
    let close = row()
        .key("settings.close")
        .role("button")
        .label("Close")
        .focusable(true)
        .size(CONTROL_HEIGHT, CONTROL_HEIGHT)
        .center()
        .radius(6.0)
        .fill(t.bg_button)
        .border(1.0, t.border_hover)
        .hover(t.bg_elevated)
        .child(
            text("x")
                .font(TextStyle::new(13.0, 700.0))
                .color(t.text_secondary)
                .a11y_hidden(),
        )
        .on_press_tx(super::close);
    row()
        .key("settings.header")
        .w_full()
        .h(HEADER_HEIGHT)
        .pl(22.0)
        .pr(18.0)
        .gap(16.0)
        .items_center()
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
        .child(title(t).grow(1.0).min_w(0.0))
        .child(row().gap(10.0).items_center().child(search).child(close))
}

fn nav(state: &State, t: &Tokens) -> El {
    let dialog = &state.window.settings;
    let groups = sections::filtered(&dialog.search.text);
    let nav = col()
        .key("settings.nav")
        .role("navigation")
        .label("Settings sections")
        .w(NAV_WIDTH)
        .h_full()
        .gap(20.0)
        .py(18.0)
        .px(12.0)
        .fill(t.bg_app)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    if groups.is_empty() {
        return nav
            .child(
                text("No matching settings.")
                    .font(CAPTION)
                    .color(t.text_tertiary),
            )
            .scroll_bar_styled(attn_ui::scroll_bar(t).track_offset(0.0, 1.0));
    }
    nav.children(groups.into_iter().map(|(label, items)| {
        col()
            .child(
                type_::upper(label, type_::BOLD_CAPS)
                    .color(t.text_dimmed)
                    .px(10.0)
                    .pb(8.0),
            )
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
                    .min_h(38.0)
                    .px(10.0)
                    .py(8.0)
                    .gap(10.0)
                    .items_center()
                    .radius(6.0)
                    .fill(if active {
                        t.warning.alpha(0.12)
                    } else {
                        Color::TRANSPARENT
                    })
                    .border_sides([0.0, 0.0, 0.0, if active { 3.0 } else { 0.0 }], t.warning)
                    .hover(if active {
                        t.warning.alpha(0.12)
                    } else {
                        t.bg_elevated
                    })
                    .child(
                        text(item.label)
                            .font(CAPTION)
                            .color(if active {
                                t.text_primary
                            } else {
                                t.text_secondary
                            })
                            .grow(1.0),
                    )
                    .children(off.then(|| {
                        text("off")
                            .key(format!("settings.nav-off.{}", section.id()))
                            .font(CAPTION)
                            .color(t.text_tertiary)
                    }))
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        super::select(state, section, tx)
                    })
            }))
    }))
    .scroll_bar_styled(attn_ui::scroll_bar(t).track_offset(0.0, 1.0))
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
        .scroll_bar_styled(attn_ui::scroll_bar(t))
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
        Section::Outposts => outposts::page(state, t),
        Section::Phones => phones::page(state, t),
        Section::Senders => senders::page(state, t),
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

/// The dialog's title as the Tauri app's `.settings-title`: the name in uppercase monospace,
/// then the daemon it edits after a rule.
fn title(t: &Tokens) -> El {
    row()
        .items_center()
        .gap(16.0)
        .child(type_::upper("Settings", type_::BOLD_CAPS).color(t.text_primary))
        .child(
            text("local daemon")
                .font(CAPTION)
                .color(t.text_tertiary)
                .pl(16.0)
                .border_sides([0.0, 0.0, 0.0, 1.0], t.border),
        )
}
