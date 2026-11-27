use attn_ui::Tokens;
use attn_ui::blocks::{Block, Dialog, DialogSize, Head, Inline, Page};
use attn_ui::system::{GroupHead, ListRow, Type, search_field, words};
use rune::{El, Tx, col, row};
use rune_kit::{ScrollBar, TextInput};

use super::sections::{self, Section};
use super::{
    agents, analytics, appearance, background, bus, controls, delegation, desktop, experimental,
    github, hygiene, models, outposts, phones, plugins, route, senders, tasks, updates,
};
use crate::State;
use crate::setting;

const SEARCH_FIELD: &str = "settings.search.field";

/// The most of the dialog the navigation takes: in the smallest window at 150% text the dialog
/// is 493 wide and the navigation keeps 164, where "Files and locations" fits on one line.
const NAV_SHARE: f32 = 1.0 / 3.0;
const SEARCH_WIDTH: f32 = 280.0;
/// The narrowest the search field gets before the title gives way: its placeholder, "Search
/// settings", measures 92 in Body, inside its insets and with room to type after it.
const SEARCH_FLOOR: f32 = 160.0;

/// The settings modal over the whole window, while it is open: its navigation beside the page.
pub(crate) fn dialog(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    if !super::is_open(state) {
        return;
    }
    let content = row()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .fill(t.bg_panel)
        .child(nav(state, t))
        .child(body(state, t));
    Dialog::new("settings", "Settings")
        .lead(
            crate::machine::Machines::of(&state.facts)
                .away(None)
                .map_or_else(
                    || "local daemon".to_owned(),
                    |home| format!("home on {home}"),
                ),
        )
        .aside(search(state, t))
        .size(DialogSize::Workspace)
        .autofocus()
        .on_close_tx(super::close)
        .list(content)
        .over(analytics::toast(state, t))
        .build(t, layers);
    delegation::overlay(state, t, layers);
    route::overlay(state, t, layers);
    agents::overlay(state, t, layers);
}

/// The field that narrows the navigation to the sections whose words it holds.
fn search(state: &State, t: &Tokens) -> El {
    let input = TextInput::new(&state.window.settings.search)
        .key(SEARCH_FIELD)
        .label("Search settings")
        .placeholder("Search settings")
        .on_edit(|state: &mut State| &mut state.window.settings.search);
    El::from(search_field(input, t))
        .w(SEARCH_WIDTH)
        .min_w(SEARCH_FLOOR)
        .shrink()
}

fn nav(state: &State, t: &Tokens) -> El {
    let dialog = &state.window.settings;
    let groups = sections::filtered(&dialog.search.text, &state.facts.harnesses);
    let nav = col()
        .key("settings.nav")
        .role("navigation")
        .label("Settings sections")
        .w(Dialog::NAVIGATION)
        .max_w_frac(NAV_SHARE)
        .h_full()
        .fill(t.bg_app)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    let bar = attn_ui::scroll_bar(t).track_offset(0.0, 1.0);
    if groups.is_empty() {
        return nav
            .child(ListRow::column().child(ListRow::note(
                "settings.nav-empty",
                "No matching settings.",
                Type::Small,
                t.text_tertiary,
                None,
                t,
            )))
            .scroll_bar_styled(bar);
    }
    let rows = groups.into_iter().flat_map(|(label, items)| {
        let head = GroupHead::new(format!("settings.group.{}", label.to_lowercase()), label)
            .section(label)
            .heading()
            .build(t);
        std::iter::once(head).chain(items.into_iter().map(|item| {
            let section = item.section;
            let active = dialog.section == section;
            let off = item
                .feature
                .is_some_and(|key| !setting::on(&state.facts.settings, key));
            let row = ListRow::new(format!("settings.nav.{}", section.id()), item.label)
                .title(item.label)
                .in_view(active)
                .checked(active)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::select(state, section, tx)
                });
            let row = if off {
                row.aside(
                    words("off", Type::Small, t.text_tertiary)
                        .key(format!("settings.nav-off.{}", section.id())),
                )
            } else {
                row
            };
            row.build(t)
        }))
    });
    nav.child(ListRow::column().children(rows.collect::<Vec<_>>()))
        .scroll_bar_styled(bar)
}

fn body(state: &State, t: &Tokens) -> El {
    state.window.settings.anchors.clear();
    let section = state.window.settings.section;
    let item = section.item();
    let summary = match item.feature {
        Some(key) => controls::feature(key).summary,
        None => item.description,
    };
    let mut head = Head::new("settings.heading", item.label).lead(summary);
    if let Some(note) = item
        .feature
        .and_then(|key| controls::feature_note(state, key, t))
    {
        head = head.note(note);
    }
    if let Some(key) = item.feature {
        head = head.aside(controls::feature_switch(
            state,
            key,
            attn_ui::system::Beside::State,
            t,
        ));
    }
    let pills = pills(state, section, t);
    if !pills.is_empty() {
        head = head.aside(Inline::loose().key("settings.pills").children(pills));
    }
    let off = item
        .feature
        .is_some_and(|key| !setting::on(&state.facts.settings, key));
    let blocks = page(state, section, t);
    let (said, parts) = status(state, t);
    Page::new()
        .head(head)
        .status("settings.autosave", said, parts)
        .push_all(blocks)
        .group(format!("settings.section.{}", section.id()), item.label)
        .dimmed(off)
        .build(super::BODY, t)
}

/// Saving…, Saved, or the refusals no field shows beside itself: what the page says, and
/// what it shows.
fn status(state: &State, t: &Tokens) -> (&'static str, Vec<El>) {
    let saves = &state.window.settings.autosave;
    let said = if saves.saving() {
        Some("Saving…")
    } else if saves.saved() {
        Some("Saved")
    } else {
        None
    };
    let parts = said
        .map(|s| words(s, Type::Small, t.text_tertiary))
        .into_iter()
        .chain(controls::unanchored_refusals(state, t))
        .collect();
    (said.unwrap_or_default(), parts)
}

fn page(state: &State, section: Section, t: &Tokens) -> Vec<Block> {
    match section {
        Section::General => appearance::page(state, t),
        Section::Connectivity => github::page(state, t),
        Section::Experimental => experimental::page(state, t),
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
