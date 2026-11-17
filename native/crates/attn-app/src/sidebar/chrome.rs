use std::collections::{HashMap, HashSet};

use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens};
use rune::tok::space;
use rune::{El, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant};

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
    /// Nothing on the desktop, neither agents nor documents.
    pub empty: bool,
    /// The keys that select the desktop.
    pub keys: Option<String>,
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
/// that have none. `waiting_on` names a desktop once for each turn waiting on it.
pub fn chips<'a>(
    state: &'a State,
    waiting_on: impl IntoIterator<Item = &'a str>,
) -> (Vec<Chip>, Extras) {
    let facts = &state.facts;
    let current = (state.window.view == View::Desktop)
        .then(|| facts.current_desktop().map(|d| d.id.clone()))
        .flatten();
    let mut waiting: HashMap<&str, usize> = HashMap::new();
    for desktop in waiting_on {
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
                empty: crate::desktop::holds_nothing(d),
                keys: crate::Action::desktop_select(slot)
                    .and_then(|a| crate::palette::label_of(state, &a)),
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
    let named = match &chip.keys {
        Some(keys) => format!("{} ({keys})", chip.title),
        None => chip.title.clone(),
    };
    let label = format!("{named}{}{asks}", waiting_words(chip.waiting, mark));
    let slot = chip.slot;
    let waiting = chip.waiting;
    let asks_mark = chip.asks;
    let number = match (chip.current, chip.empty) {
        (true, _) => attn_ui::brand_ink(t),
        (false, true) => t.text_tertiary,
        (false, false) => t.text_secondary,
    };
    let tokens = *t;
    let el = Button::new(label)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
        .label_with(move |_, _| {
            row()
                .items_center()
                .child(
                    text(slot.to_string())
                        .font(type_::MONO_CAPTION)
                        .color(number),
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
        .help(named)
        .a11y_value(match (chip.current, chip.empty) {
            (true, true) => "current, Empty desktop",
            (true, false) => "current",
            (false, true) => "Empty desktop",
            (false, false) => "",
        })
        .h(22.0)
        .min_w(22.0)
        .px(4.0)
        .radius(4.0)
        .fill(if chip.current { t.brand } else { t.bg_button })
        .hover(t.bg_elevated);
    match drop {
        None => el,
        Some(DesktopMark::Source) => el.opacity(0.4),
        Some(DesktopMark::Accepts) => el.border(1.0, t.accent),
        Some(DesktopMark::Target) => el.border(1.0, t.accent).fill(t.accent.alpha(0.18)),
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
        Mark::Count => row()
            .min_w(15.0)
            .h(15.0)
            .px(4.0)
            .center()
            .radius(999.0)
            .fill(t.warning)
            .ml(3.0)
            .child(text(waiting.to_string()).font(COUNT_TYPE).color(t.bg_app)),
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
    let (chips, extras) = chips(state, turns_on(bands));
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

fn turns_on<'a>(bands: &Bands<'a>) -> Vec<&'a str> {
    bands
        .turns
        .iter()
        .filter(|r| !r.offline)
        .filter_map(|r| r.desktop)
        .collect()
}

/// The desktops of the agents that want the user, one entry per agent.
fn wanting_on(state: &State) -> Vec<&str> {
    let wanting: HashSet<&str> = super::queue::wanting(&state.facts)
        .iter()
        .map(|s| s.id.as_str())
        .collect();
    state
        .facts
        .desktops
        .iter()
        .flat_map(|d| {
            d.panes
                .iter()
                .filter(|p| wanting.contains(p.session_id.as_str()))
                .map(|_| d.id.as_str())
        })
        .collect()
}

/// The desktop sidebar collapsed: Home, the way back out, and the desktops' chips.
pub(crate) fn rail(state: &State, t: &Tokens) -> El {
    let (chips, extras) = chips(state, wanting_on(state));
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
            GlyphButton::new(&attn_ui::icon::HOME, "Home")
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
        .child(
            col()
                .key("rail.desktops")
                .role("group")
                .label("Desktops")
                .items_center()
                .gap(space::S1)
                .children(
                    chips
                        .iter()
                        .map(|c| chip("rail.desktop", c, Mark::Dot, None, t)),
                )
                .children(extras_chip("rail.desktop-extras", extras, Mark::Dot, t)),
        )
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
        attn_ui::brand_tinted(
            tool_button(
                "titlebar.new",
                GlyphButton::new("New", "New agent").variant(ButtonVariant::Ghost),
                t,
                |state, tx| {
                    crate::dispatch(state, crate::Action::SessionNew, tx);
                },
            )
            .px(space::S2),
            t,
        ),
        tool(
            "titlebar.garden",
            if crate::garden::mode(state) == crate::garden::Mode::Closed {
                "Show the garden"
            } else {
                "Hide the garden"
            },
            &attn_ui::icon::GARDEN,
            t,
            crate::garden::toggle_from_icon,
        ),
        ledger_button(state, t),
        tool(
            "titlebar.automations",
            if crate::automations::is_open(state) {
                "Hide Automations"
            } else {
                "Show Automations"
            },
            &attn_ui::icon::AUTOMATIONS,
            t,
            crate::automations::toggle,
        ),
        tool_button(
            "titlebar.commands",
            GlyphButton::new(&attn_ui::icon::COMMANDS, "Commands").color(t.brand),
            t,
            |state, tx| {
                crate::dispatch(state, crate::Action::UiCommandPalette, tx);
            },
        ),
        tool(
            "titlebar.flow",
            flow,
            if queue {
                &attn_ui::icon::DESKTOP_FLOW
            } else {
                &attn_ui::icon::QUEUE_FLOW
            },
            t,
            actions::toggle_flow,
        ),
        tool(
            "titlebar.sidebar",
            sidebar,
            if collapsed {
                &attn_ui::icon::EXPAND
            } else {
                &attn_ui::icon::COLLAPSE
            },
            t,
            |state, _| state.window.sidebar_hidden = !state.window.sidebar_hidden,
        ),
    ]
}

fn ledger_button(state: &State, t: &Tokens) -> El {
    let keys = state
        .keymap
        .binding(&crate::Action::SessionsOpen)
        .map(|b| format!(" ({})", state.keymap.label(b)))
        .unwrap_or_default();
    tool(
        "titlebar.ledger",
        &format!("Open Ledger{keys}"),
        &attn_ui::icon::LEDGER,
        t,
        crate::ledger::open_last,
    )
}

fn tool(
    key: &str,
    label: &str,
    glyph: impl Into<attn_ui::Glyph>,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    tool_button(key, GlyphButton::new(glyph, label), t, press)
}

fn tool_button(
    key: &str,
    button: GlyphButton,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    button
        .font(type_::BODY)
        .icon_size(13.0)
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
    let owed = bands.owed();
    let crumbs: Vec<String> = bands
        .turns
        .iter()
        .take(3)
        .map(|r| super::model::display_name(r.session))
        .collect();
    let more = bands.turns.len().saturating_sub(crumbs.len());
    let mut pill = super::queue_view::owed_label(owed).unwrap_or_else(|| "Nothing owed".into());
    if !crumbs.is_empty() {
        pill.push_str(" · ");
        pill.push_str(&crumbs.join(" › "));
    }
    if more > 0 {
        pill.push_str(&format!(" +{more}"));
    }
    let pill_ink = if owed.0 == 0 {
        t.text_tertiary
    } else {
        t.text_primary
    };
    let (chips, extras) = chips(state, turns_on(bands));
    row()
        .key("queue-bar")
        .role("group")
        .label("Queue bar")
        .shrink()
        .h_full()
        .items_center()
        .gap(space::S2)
        .child(
            Button::new(pill.clone())
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    super::bar_peek::dismiss(state);
                    crate::dispatch(state, crate::Action::UiActionMenu, tx);
                })
                .label_with(move |_, _| {
                    text(pill)
                        .font(type_::LABEL)
                        .color(pill_ink)
                        .single_line()
                        .truncate()
                        .shrink()
                })
                .build(attn_ui::kit(t))
                .key("queue-bar.pill")
                .a11y_value(owed.0.to_string())
                .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
                    super::bar_peek::anchor_hovered(state, super::bar_peek::Anchor::Waiting, inside)
                })
                .shrink()
                .min_w(124.0)
                .max_w(640.0)
                .h(22.0)
                .px(space::S2)
                .radius(11.0)
                .fill(t.bg_button)
                .hover(t.bg_elevated),
        )
        .children(runs_chip(state, t))
        .child(
            row()
                .key("queue-bar.desktops")
                .role("group")
                .label("Desktops")
                .h_full()
                .items_center()
                .gap(space::S2)
                .children(chips.iter().map(|c| {
                    let drop = drop_on(state, &c.desktop);
                    chip("queue-bar.desktop", c, Mark::Count, drop, t)
                }))
                .children(extras_chip(
                    "queue-bar.desktop-extras",
                    extras,
                    Mark::Count,
                    t,
                )),
        )
}

/// The queue bar's count of automation runs and of those that need the user; pressing it walks
/// them as ⌘⇧J does.
fn runs_chip(state: &State, t: &Tokens) -> Option<El> {
    let groups = crate::automations::runs::on_desktops(&state.facts, state.window.now_ms);
    let runs: usize = groups.iter().map(|g| g.runs.len()).sum();
    let needing: usize = groups.iter().map(|g| g.needing_you.len()).sum();
    if runs == 0 && needing == 0 {
        return None;
    }
    let plural = |n: usize, noun: &str| format!("{n} {noun}{}", if n == 1 { "" } else { "s" });
    let mut says = vec![plural(groups.len(), "automation"), plural(runs, "run")];
    if needing > 0 {
        says.push(format!("{needing} still need you"));
    }
    says.push("runs stay out of the queue".into());
    let keys = state
        .keymap
        .binding(&crate::Action::SessionNextRun)
        .map(|b| state.keymap.label(b));
    says.push(match keys {
        Some(keys) => format!("click or {keys} to walk them"),
        None => "click to walk them".into(),
    });
    let tokens = *t;
    Some(
        Button::new(format!("{runs} runs"))
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                super::bar_peek::dismiss(state);
                crate::automations::runs::next_run(state, tx)
            })
            .label_with(move |_, _| {
                row()
                    .items_center()
                    .gap(space::S1)
                    .child(
                        text(format!("{} {runs}", attn_ui::glyph::STAR_OPEN))
                            .font(type_::LABEL)
                            .color(tokens.text_secondary)
                            .single_line(),
                    )
                    .children((needing > 0).then(|| {
                        text(needing.to_string())
                            .font(COUNT_TYPE)
                            .color(tokens.warning)
                    }))
            })
            .build(attn_ui::kit(t))
            .key(super::bar_peek::RUNS)
            .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
                super::bar_peek::anchor_hovered(state, super::bar_peek::Anchor::Runs, inside)
            })
            .a11y_value(format!("{runs} runs, {needing} need you"))
            .description(says.join(" · "))
            .h(22.0)
            .px(space::S2)
            .radius(11.0)
            .fill(t.bg_button)
            .hover(t.bg_elevated),
    )
}

const COUNT_TYPE: rune::TextStyle = rune::TextStyle::new(9.5, 600.0).mono();
