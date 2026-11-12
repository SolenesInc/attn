use std::collections::{HashMap, HashSet};

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
    /// The turns waiting on agents of this desktop.
    pub waiting: usize,
}

/// The desktops without a shortcut slot, as one chip.
pub struct Extras {
    pub count: usize,
    pub waiting: usize,
}

/// The chips of the desktops that have a shortcut slot, in slot order, and the desktops
/// that have none.
pub fn chips(state: &State, bands: Option<&Bands<'_>>) -> (Vec<Chip>, Extras) {
    let facts = &state.facts;
    let current = (state.window.view == View::Desktop)
        .then(|| facts.current_desktop().map(|d| d.id.clone()))
        .flatten();
    let mut waiting: HashMap<&str, usize> = HashMap::new();
    for desktop in bands
        .iter()
        .flat_map(|b| b.turns.iter().filter_map(|r| r.desktop))
    {
        *waiting.entry(desktop).or_default() += 1;
    }
    let mut chips: Vec<Chip> = ordered_desktops(&facts.desktops)
        .into_iter()
        .filter_map(|d| {
            let slot = d.shortcut_slot.filter(|s| *s > 0)?;
            Some(Chip {
                desktop: d.id.clone(),
                slot,
                title: desktop_title(d, &facts.desktops),
                current: current.as_deref() == Some(d.id.as_str()),
                waiting: waiting.get(d.id.as_str()).copied().unwrap_or(0),
            })
        })
        .collect();
    chips.sort_by_key(|c| c.slot);
    let slotted: HashSet<&str> = chips.iter().map(|c| c.desktop.as_str()).collect();
    let extras = Extras {
        count: facts.desktops.len() - chips.len(),
        waiting: facts
            .desktops
            .iter()
            .filter(|d| !slotted.contains(d.id.as_str()))
            .map(|d| waiting.get(d.id.as_str()).copied().unwrap_or(0))
            .sum(),
    };
    (chips, extras)
}

/// How a chip shows the turns waiting on its desktops: the queue sidebar's strip with a dot,
/// the queue bar with their number.
#[derive(Clone, Copy)]
pub(crate) enum Mark {
    Dot,
    Count,
}

pub(crate) fn chip(prefix: &str, chip: &Chip, mark: Mark, t: &Tokens) -> El {
    let desktop = chip.desktop.clone();
    let label = format!("{}{}", chip.title, waiting_words(chip.waiting, mark));
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
                .color(t.text_secondary),
        )
        .children(waiting_mark(chip.waiting, mark, t))
}

fn waiting_words(waiting: usize, mark: Mark) -> String {
    match (waiting, mark) {
        (0, _) => String::new(),
        (_, Mark::Dot) => ", has turns waiting".into(),
        (1, Mark::Count) => ", 1 turn waiting".into(),
        (n, Mark::Count) => format!(", {n} turns waiting"),
    }
}

fn waiting_mark(waiting: usize, mark: Mark, t: &Tokens) -> Option<El> {
    (waiting > 0).then(|| match mark {
        Mark::Dot => rune::el()
            .size(5.0, 5.0)
            .radius(2.5)
            .fill(t.warning)
            .ml(2.0),
        Mark::Count => text(waiting.to_string())
            .font(type_::MONO_CAPTION)
            .color(t.warning)
            .ml(2.0),
    })
}

fn extras_chip(key: &str, extras: Extras, mark: Mark, t: &Tokens) -> Option<El> {
    let Extras { count, waiting } = extras;
    (count > 0).then(|| {
        row()
            .key(key.to_owned())
            .role("button")
            .label(format!(
                "{count} more desktop{} without a shortcut{}",
                if count == 1 { "" } else { "s" },
                waiting_words(waiting, mark)
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
                text(format!("+{count}"))
                    .font(type_::MONO_CAPTION)
                    .color(t.text_secondary),
            )
            .children(waiting_mark(waiting, mark, t))
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
        .children(chips.iter().map(|c| chip("queue.desktop", c, Mark::Dot, t)))
        .children(extras_chip("queue.desktop-extras", extras, Mark::Dot, t))
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
        .children(chips.iter().map(|c| chip("rail.desktop", c, Mark::Dot, t)))
        .children(extras_chip("rail.desktop-extras", extras, Mark::Dot, t))
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
        .children(
            chips
                .iter()
                .map(|c| chip("queue-bar.desktop", c, Mark::Count, t)),
        )
        .children(extras_chip(
            "queue-bar.desktop-extras",
            extras,
            Mark::Count,
            t,
        ))
}
