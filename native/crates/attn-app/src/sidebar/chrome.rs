use std::collections::{HashMap, HashSet};

use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};
use rune_kit::Button;

use super::model::{desktop_title, ordered_desktops};
use super::queue::Bands;
use super::{actions, open_desktop};
use crate::State;
use crate::desktop::DesktopMark;
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
    /// An agent of this desktop has an open ask.
    pub asks: bool,
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
                asks: d.panes.iter().any(|p| {
                    facts
                        .sessions
                        .iter()
                        .find(|s| s.id == p.session_id)
                        .is_some_and(|s| !crate::asks::of_session(facts, s).is_empty())
                }),
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

/// How desktop `desktop`'s chip shows a dragged tile, where chips take one.
fn drop_on(state: &State, desktop: &str) -> Option<DesktopMark> {
    state.window.desk.drag.as_ref()?.desktop_mark(desktop)
}

pub(crate) fn chip(
    prefix: &str,
    chip: &Chip,
    mark: Mark,
    drop: Option<DesktopMark>,
    t: &Tokens,
) -> El {
    let desktop = chip.desktop.clone();
    let asks = if chip.asks { ", has open asks" } else { "" };
    let label = format!("{}{}{asks}", chip.title, waiting_words(chip.waiting, mark));
    let slot = chip.slot;
    let waiting = chip.waiting;
    let asks_mark = chip.asks;
    let tokens = *t;
    let el = Button::new(label)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
        .label_with(move |_, _| {
            row()
                .items_center()
                .child(
                    text(slot.to_string())
                        .font(type_::MONO_CAPTION)
                        .color(tokens.text_secondary),
                )
                .children(waiting_mark(waiting, mark, &tokens))
                .children(asks_mark.then(|| {
                    text("?")
                        .font(type_::MONO_CAPTION)
                        .color(tokens.accent)
                        .ml(2.0)
                }))
        })
        .build(attn_ui::kit(t))
        .key(format!("{prefix}.{}", chip.slot))
        .a11y_value(if chip.current { "current" } else { "" })
        .h(22.0)
        .min_w(22.0)
        .px(4.0)
        .radius(4.0)
        .fill(if chip.current {
            t.bg_selected
        } else {
            t.bg_button
        })
        .hover(t.bg_elevated);
    match drop {
        None => el,
        Some(DesktopMark::Source) => el.opacity(0.4),
        Some(DesktopMark::Accepts) => el.border(1.0, t.accent),
        Some(DesktopMark::Entering) => el.border(1.0, t.accent).fill(t.accent.alpha(0.18)),
    }
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
        let tokens = *t;
        Button::new(format!(
            "{count} more desktop{} without a shortcut{}",
            if count == 1 { "" } else { "s" },
            waiting_words(waiting, mark)
        ))
        .on_press_tx(|state: &mut State, tx: &mut Tx| {
            crate::dispatch(state, crate::Action::DesktopOverview, tx);
        })
        .label_with(move |_, _| {
            row()
                .items_center()
                .child(
                    text(format!("+{count}"))
                        .font(type_::MONO_CAPTION)
                        .color(tokens.text_secondary),
                )
                .children(waiting_mark(waiting, mark, &tokens))
        })
        .build(attn_ui::kit(t))
        .key(key.to_owned())
        .h(22.0)
        .px(4.0)
        .radius(4.0)
        .hover(t.bg_elevated)
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
        .children(
            chips
                .iter()
                .map(|c| chip("queue.desktop", c, Mark::Dot, drop_on(state, &c.desktop), t)),
        )
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
            GlyphButton::new(attn_ui::icon::HOME, "Home")
                .font(type_::BODY)
                .on_press(|state: &mut State| state.window.go_home())
                .build(attn_ui::kit(t))
                .key("rail.home")
                .a11y_value(if home { "current" } else { "" })
                .radius(4.0)
                .fill(if home { t.bg_selected } else { t.bg_panel })
                .hover(t.bg_elevated),
        )
        .child(
            GlyphButton::new("»", "Expand sidebar")
                .font(type_::BODY)
                .on_press(|state: &mut State| state.window.sidebar_hidden = false)
                .build(attn_ui::kit(t))
                .key("rail.expand")
                .radius(4.0)
                .hover(t.bg_elevated),
        )
        .children(
            chips
                .iter()
                .map(|c| chip("rail.desktop", c, Mark::Dot, None, t)),
        )
        .children(extras_chip("rail.desktop-extras", extras, Mark::Dot, t))
}

/// The title bar's own controls on the left: New agent, Commands, the flow switch and the
/// sidebar toggle.
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
        tool("titlebar.new", "New agent", "New", t, |state, tx| {
            crate::dispatch(state, crate::Action::SessionNew, tx);
        }),
        tool(
            "titlebar.garden",
            if crate::garden::mode(state) == crate::garden::Mode::Closed {
                "Show the garden"
            } else {
                "Hide the garden"
            },
            attn_ui::icon::SEEDLING,
            t,
            crate::garden::toggle_from_icon,
        ),
        tool(
            "titlebar.commands",
            "Commands",
            attn_ui::icon::BOLT,
            t,
            |state, tx| {
                crate::dispatch(state, crate::Action::UiCommandPalette, tx);
            },
        ),
        tool(
            "titlebar.flow",
            flow,
            if queue {
                attn_ui::icon::WINDOW
            } else {
                attn_ui::icon::LIST
            },
            t,
            actions::toggle_flow,
        ),
        tool(
            "titlebar.sidebar",
            sidebar,
            if collapsed { "»" } else { "«" },
            t,
            |state, _| state.window.sidebar_hidden = !state.window.sidebar_hidden,
        ),
    ]
}

fn tool(
    key: &str,
    label: &str,
    glyph: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    GlyphButton::new(glyph, label)
        .font(type_::BODY)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key.to_owned())
        .min_w(24.0)
        .radius(4.0)
        .hover(t.bg_elevated)
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
    let pill_ink = if waiting == 0 {
        t.text_tertiary
    } else {
        t.text_primary
    };
    let (chips, extras) = chips(state, Some(bands));
    row()
        .key("queue-bar")
        .role("group")
        .label("Queue bar")
        .h_full()
        .items_center()
        .gap(space::S2)
        .child(
            Button::new(pill.clone())
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    crate::dispatch(state, crate::Action::UiActionMenu, tx);
                })
                .label_with(move |_, _| text(pill).font(type_::LABEL).color(pill_ink))
                .build(attn_ui::kit(t))
                .key("queue-bar.pill")
                .a11y_value(waiting.to_string())
                .h(22.0)
                .px(space::S2)
                .radius(11.0)
                .fill(t.bg_button)
                .hover(t.bg_elevated),
        )
        .children(chips.iter().map(|c| {
            let drop = drop_on(state, &c.desktop);
            chip("queue-bar.desktop", c, Mark::Count, drop, t)
        }))
        .children(extras_chip(
            "queue-bar.desktop-extras",
            extras,
            Mark::Count,
            t,
        ))
}
