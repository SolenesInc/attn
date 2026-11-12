use std::collections::HashSet;

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};

use super::model::{desktop_title, ordered_desktops};
use super::queue::Bands;
use super::{actions, open_desktop};
use crate::State;
use crate::shell::View;

/// A desktop's chip: its slot, marked when it shows and when a turn waits on it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Chip {
    pub desktop: String,
    pub slot: i64,
    pub title: String,
    pub current: bool,
    pub waiting: bool,
}

/// The chips of the desktops that have a shortcut slot, in slot order, and how many
/// desktops have none.
pub fn chips(state: &State, bands: Option<&Bands<'_>>) -> (Vec<Chip>, usize) {
    let facts = &state.facts;
    let current = (state.window.view == View::Desktop)
        .then(|| facts.current_desktop().map(|d| d.id.clone()))
        .flatten();
    let waiting: HashSet<&str> = bands
        .map(|b| b.turns.iter().filter_map(|r| r.desktop).collect())
        .unwrap_or_default();
    let mut chips: Vec<Chip> = ordered_desktops(&facts.desktops)
        .into_iter()
        .filter_map(|d| {
            let slot = d.shortcut_slot.filter(|s| *s > 0)?;
            Some(Chip {
                desktop: d.id.clone(),
                slot,
                title: desktop_title(d, &facts.desktops),
                current: current.as_deref() == Some(d.id.as_str()),
                waiting: waiting.contains(d.id.as_str()),
            })
        })
        .collect();
    chips.sort_by_key(|c| c.slot);
    let extras = facts.desktops.len() - chips.len();
    (chips, extras)
}

pub(crate) fn chip(prefix: &str, chip: &Chip, t: &Tokens) -> El {
    let desktop = chip.desktop.clone();
    let label = if chip.waiting {
        format!("{}, has turns waiting", chip.title)
    } else {
        chip.title.clone()
    };
    row()
        .key(format!("{prefix}.{}", chip.slot))
        .role("button")
        .label(label)
        .a11y_value(if chip.current { "current" } else { "" })
        .h(22.0)
        .min_w(22.0)
        .px(4.0)
        .center()
        .radius(4.0)
        .fill(if chip.current {
            t.bg_selected
        } else {
            t.bg_button
        })
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
        .child(
            text(chip.slot.to_string())
                .font(type_::MONO_CAPTION)
                .color(if chip.waiting {
                    t.warning
                } else {
                    t.text_secondary
                }),
        )
}

fn extras_chip(key: &str, extras: usize, t: &Tokens) -> Option<El> {
    (extras > 0).then(|| {
        row()
            .key(key.to_owned())
            .role("button")
            .label(format!(
                "{extras} more desktop{} without a shortcut",
                if extras == 1 { "" } else { "s" }
            ))
            .h(22.0)
            .px(4.0)
            .center()
            .radius(4.0)
            .fill(t.bg_button)
            .hover(t.bg_elevated)
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                crate::dispatch(state, crate::Action::DesktopOverview, tx);
            })
            .child(
                text(format!("+{extras}"))
                    .font(type_::MONO_CAPTION)
                    .color(t.text_secondary),
            )
    })
}

/// The queue sidebar's foot: a chip per slotted desktop.
pub(crate) fn desktop_strip(state: &State, bands: &Bands<'_>, t: &Tokens) -> El {
    let (chips, extras) = chips(state, Some(bands));
    row()
        .key("queue.desktops")
        .role("group")
        .label("Desktops")
        .w_full()
        .gap(space::S1)
        .children(chips.iter().map(|c| chip("queue.desktop", c, t)))
        .children(extras_chip("queue.desktop-extras", extras, t))
}

/// The desktop sidebar collapsed: Home, the way back out, and the desktops' chips.
pub(crate) fn rail(state: &State, t: &Tokens) -> El {
    let (chips, extras) = chips(state, None);
    let home = state.window.view == View::Home;
    col()
        .key("rail")
        .role("navigation")
        .label("Rail")
        .w(40.0)
        .h_full()
        .items_center()
        .gap(space::S1)
        .py(space::S2)
        .fill(t.bg_panel)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
        .child(
            row()
                .key("rail.home")
                .role("button")
                .label("Home")
                .a11y_value(if home { "current" } else { "" })
                .h(22.0)
                .min_w(22.0)
                .center()
                .radius(4.0)
                .fill(if home { t.bg_selected } else { t.bg_panel })
                .hover(t.bg_elevated)
                .on_press(|state: &mut State| state.window.go_home())
                .child(
                    text(attn_ui::icon::HOME)
                        .font(type_::BODY)
                        .color(t.text_secondary),
                ),
        )
        .child(
            row()
                .key("rail.expand")
                .role("button")
                .label("Expand sidebar")
                .h(22.0)
                .min_w(22.0)
                .center()
                .radius(4.0)
                .hover(t.bg_elevated)
                .on_press(|state: &mut State| state.window.sidebar_hidden = false)
                .child(text("»").font(type_::BODY).color(t.text_secondary)),
        )
        .children(chips.iter().map(|c| chip("rail.desktop", c, t)))
        .children(extras_chip("rail.desktop-extras", extras, t))
}

/// The title bar's own controls on the left: the flow switch and the sidebar toggle.
pub(crate) fn title_controls(state: &State, t: &Tokens) -> Vec<El> {
    let queue = actions::queue_flow(state);
    let flow = if queue {
        "Switch to desktop flow"
    } else {
        "Switch to queue flow"
    };
    let collapsed = state.window.sidebar_hidden;
    let sidebar = if collapsed {
        "Show sidebar"
    } else {
        "Collapse sidebar"
    };
    vec![
        tool(
            "titlebar.flow",
            flow,
            if queue {
                attn_ui::icon::WINDOW
            } else {
                attn_ui::icon::LIST
            },
            t,
        )
        .on_press_tx(actions::toggle_flow),
        tool(
            "titlebar.sidebar",
            sidebar,
            if collapsed { "»" } else { "«" },
            t,
        )
        .on_press(|state: &mut State| state.window.sidebar_hidden = !state.window.sidebar_hidden),
    ]
}

fn tool(key: &str, label: &str, glyph: &str, t: &Tokens) -> El {
    row()
        .key(key.to_owned())
        .role("button")
        .label(label.to_owned())
        .h(22.0)
        .min_w(24.0)
        .center()
        .radius(4.0)
        .hover(t.bg_elevated)
        .child(
            text(glyph.to_owned())
                .font(type_::BODY)
                .color(t.text_secondary),
        )
}

/// Queue flow with the sidebar collapsed: the waiting pill, then the desktops' chips, in
/// the title bar.
pub(crate) fn queue_bar(state: &State, bands: &Bands<'_>, t: &Tokens) -> El {
    let waiting = bands.turns.len();
    let crumbs: Vec<String> = bands
        .turns
        .iter()
        .take(3)
        .map(|r| super::model::display_name(r.session))
        .collect();
    let more = waiting.saturating_sub(crumbs.len());
    let mut pill = if waiting == 0 {
        "Nothing owed".to_owned()
    } else {
        format!("{waiting} waiting")
    };
    if !crumbs.is_empty() {
        pill.push_str(" · ");
        pill.push_str(&crumbs.join(" › "));
    }
    if more > 0 {
        pill.push_str(&format!(" +{more}"));
    }
    let (chips, extras) = chips(state, Some(bands));
    row()
        .key("queue-bar")
        .role("group")
        .label("Queue bar")
        .h_full()
        .items_center()
        .gap(space::S2)
        .child(
            row()
                .key("queue-bar.pill")
                .role("button")
                .label(pill.clone())
                .a11y_value(waiting.to_string())
                .h(22.0)
                .px(space::S2)
                .items_center()
                .radius(11.0)
                .fill(t.bg_button)
                .hover(t.bg_elevated)
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    crate::dispatch(state, crate::Action::UiActionMenu, tx);
                })
                .child(text(pill).font(type_::LABEL).color(if waiting == 0 {
                    t.text_tertiary
                } else {
                    t.text_primary
                })),
        )
        .children(chips.iter().map(|c| chip("queue-bar.desktop", c, t)))
        .children(extras_chip("queue-bar.desktop-extras", extras, t))
}
