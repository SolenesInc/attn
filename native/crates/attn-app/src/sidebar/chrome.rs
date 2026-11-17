use std::collections::{HashMap, HashSet};

use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens};
use rune::tok::space;
use rune::{El, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant};

use super::model::{desktop_number, desktop_title, ordered_desktops};
use super::queue::Bands;
use super::{actions, open_desktop};
use crate::State;
use crate::desktop::DesktopMark;
use crate::shell::View;

/// A desktop's chip: its number or slot, marked when it shows and when a turn waits on it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Chip {
    pub desktop: String,
    /// The desktop's shortcut slot, which the queue's chips go by.
    pub slot: Option<i64>,
    /// The desktop's number, which the rail goes by.
    pub number: i64,
    pub title: String,
    /// The desktop shows in the window.
    pub current: bool,
    /// The desktop the window shows or last showed, at Home too.
    pub selected: bool,
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
    /// The window's desktop is one of them.
    pub selected: bool,
}

/// A chip for every desktop, in the sidebar's order. `waiting_on` names a desktop once for
/// each turn waiting on it.
pub fn chips<'a>(state: &'a State, waiting_on: impl IntoIterator<Item = &'a str>) -> Vec<Chip> {
    let facts = &state.facts;
    let selected = facts.current_desktop().map(|d| d.id.clone());
    let home = state.window.view != View::Desktop;
    let mut waiting: HashMap<&str, usize> = HashMap::new();
    for desktop in waiting_on {
        *waiting.entry(desktop).or_default() += 1;
    }
    ordered_desktops(&facts.desktops)
        .into_iter()
        .map(|d| {
            let slot = d.shortcut_slot.filter(|s| *s > 0);
            let is_selected = selected.as_deref() == Some(d.id.as_str());
            Chip {
                desktop: d.id.clone(),
                slot,
                number: desktop_number(d, &facts.desktops),
                title: desktop_title(d, &facts.desktops),
                current: is_selected && !home,
                selected: is_selected,
                empty: crate::desktop::holds_nothing(d),
                keys: slot
                    .and_then(crate::Action::desktop_select)
                    .and_then(|a| crate::palette::label_of(state, &a)),
                waiting: waiting.get(d.id.as_str()).copied().unwrap_or(0),
                asks: d.panes.iter().any(|p| {
                    facts
                        .sessions
                        .iter()
                        .find(|s| s.id == p.session_id)
                        .is_some_and(|s| !crate::asks::of_session(facts, s).is_empty())
                }),
            }
        })
        .collect()
}

/// The chips of the desktops that have a shortcut slot, in slot order, and the desktops
/// that have none, as the queue's chips show them.
fn slotted(chips: Vec<Chip>) -> (Vec<Chip>, Extras) {
    let (mut slotted, rest): (Vec<Chip>, Vec<Chip>) =
        chips.into_iter().partition(|c| c.slot.is_some());
    slotted.sort_by_key(|c| c.slot);
    let extras = Extras {
        count: rest.len(),
        waiting: rest.iter().map(|c| c.waiting).sum(),
        selected: rest.iter().any(|c| c.selected),
    };
    (slotted, extras)
}

/// Where a queue chip sits, which sets its look: the queue sidebar's strip, as the Tauri app's
/// `.queue-desktop-chip`, or the queue bar, as its `.queue-bar-desktop`.
#[derive(Clone, Copy, PartialEq)]
pub(crate) enum Look {
    Strip,
    Bar,
}

/// How desktop `desktop`'s chip shows a dragged tile, where chips take one.
fn drop_on(state: &State, desktop: &str) -> Option<DesktopMark> {
    state.window.desk.drag.as_ref()?.desktop_mark(desktop)
}

fn said(chip: &Chip, look: Look) -> (String, String) {
    let asks = if chip.asks { ", has open asks" } else { "" };
    let named = match &chip.keys {
        Some(keys) => format!("{} ({keys})", chip.title),
        None => chip.title.clone(),
    };
    let label = format!("{named}{}{asks}", waiting_words(chip.waiting, look));
    (named, label)
}

fn chip_value(current: bool, empty: bool) -> &'static str {
    match (current, empty) {
        (true, true) => "current, Empty desktop",
        (true, false) => "current",
        (false, true) => "Empty desktop",
        (false, false) => "",
    }
}

/// A slotted desktop's chip in the queue's strip or bar: its slot, framed, with the turns
/// waiting on it hung off its top right corner so the chip keeps its size.
pub(crate) fn chip(
    prefix: &str,
    chip: &Chip,
    look: Look,
    drop: Option<DesktopMark>,
    t: &Tokens,
) -> El {
    let desktop = chip.desktop.clone();
    let (named, label) = said(chip, look);
    let slot = chip.slot.unwrap_or_default();
    let ink = match (chip.selected, chip.empty) {
        (true, _) => t.text_primary,
        (false, true) => t.text_dimmed,
        (false, false) => t.text_secondary,
    };
    let face = queue_chip_face(look, chip.selected, !chip.empty, t)
        .key(format!("{prefix}.{slot}"))
        .role("button")
        .focusable(true)
        .label(label)
        .help(named)
        .a11y_value(chip_value(chip.current, chip.empty))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
        .child(
            text(slot.to_string())
                .font(digits(look, chip.selected))
                .color(ink),
        )
        .children(waiting_mark(chip.waiting, look, t));
    dropped(face, drop, t)
}

fn dropped(face: El, drop: Option<DesktopMark>, t: &Tokens) -> El {
    match drop {
        None => face,
        Some(DesktopMark::Source) => face.opacity(0.4),
        Some(DesktopMark::Accepts) => face.border(1.0, t.brand).border_dash(2.0, 2.0),
        Some(DesktopMark::Target) => face.border(1.0, t.brand).fill(t.brand.alpha(0.18)),
    }
}

/// The frame of a queue chip, before its content.
fn queue_chip_face(look: Look, selected: bool, has_panes: bool, t: &Tokens) -> El {
    let face = row().center();
    match look {
        Look::Strip => {
            let face = face
                .min_w(15.0)
                .h(18.0)
                .radius(4.0)
                .border(1.0, t.border)
                .hover(t.bg_elevated);
            match (selected, has_panes) {
                (true, _) => face.fill(t.bg_elevated).border(1.0, t.border_focus),
                (false, true) => face.fill(super::wash(t, 0.03)),
                (false, false) => face,
            }
        }
        Look::Bar => {
            let face = face
                .min_w(24.0)
                .h(24.0)
                .radius(6.0)
                .border(1.0, rune::Color::TRANSPARENT)
                .hover(t.bg_elevated);
            match (selected, has_panes) {
                (true, _) => face
                    .fill(t.accent.alpha(0.22))
                    .border(1.0, t.accent.alpha(0.5)),
                (false, true) => face.fill(super::wash(t, 0.04)),
                (false, false) => face,
            }
        }
    }
}

fn digits(look: Look, selected: bool) -> rune::TextStyle {
    match look {
        Look::Strip => rune::TextStyle::new(10.5, if selected { 600.0 } else { 400.0 }).mono(),
        Look::Bar => rune::TextStyle::new(11.5, 600.0).mono(),
    }
}

fn waiting_words(waiting: usize, look: Look) -> String {
    match (waiting, look) {
        (0, _) => String::new(),
        (_, Look::Strip) => ", has turns waiting".into(),
        (1, Look::Bar) => ", 1 turn waiting".into(),
        (n, Look::Bar) => format!(", {n} turns waiting"),
    }
}

/// The turns waiting on a chip's desktops, over its top right corner: a dot on the strip, as
/// the Tauri app's `.queue-desktop-chip-waiting`, their number on the bar, as its
/// `.queue-bar-desktop-waiting`.
fn waiting_mark(waiting: usize, look: Look, t: &Tokens) -> Option<El> {
    (waiting > 0).then(|| match look {
        Look::Strip => rune::el()
            .pin(None, Some(-3.0), Some(-3.0), None)
            .size(6.0, 6.0)
            .radius(3.0)
            .fill(t.warning)
            .border(1.0, t.bg_panel)
            .inert(),
        Look::Bar => row()
            .pin(None, Some(-6.0), Some(-6.0), None)
            .min_w(15.0)
            .h(15.0)
            .px(4.0)
            .center()
            .radius(999.0)
            .fill(t.warning)
            .inert()
            .child(
                text(waiting.to_string())
                    .font(COUNT_TYPE)
                    .color(attn_ui::warning_ink(t)),
            ),
    })
}

fn extras_chip(key: &str, extras: Extras, look: Look, t: &Tokens) -> Option<El> {
    let Extras {
        count,
        waiting,
        selected,
    } = extras;
    (count > 0).then(|| {
        queue_chip_face(look, selected, false, t)
            .key(key.to_owned())
            .role("button")
            .focusable(true)
            .label(format!(
                "{count} more desktop{} without a shortcut{}",
                if count == 1 { "" } else { "s" },
                waiting_words(waiting, look)
            ))
            .px(if look == Look::Strip { 5.0 } else { 6.0 })
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                crate::dispatch(state, crate::Action::DesktopOverview, tx);
            })
            .child(
                text(format!("+{count}"))
                    .font(match look {
                        Look::Strip => digits(look, selected),
                        Look::Bar => rune::TextStyle::new(11.5, 500.0).mono(),
                    })
                    .color(if selected {
                        t.text_primary
                    } else {
                        t.text_tertiary
                    }),
            )
            .children(waiting_mark(waiting, look, t))
    })
}

/// The strip's chip that opens the desktop overview, as the Tauri app's compact
/// `SidebarDesktopOverview`.
fn overview_chip(keys: Option<String>, t: &Tokens) -> El {
    let label = match &keys {
        Some(keys) => format!("Desktop overview ({keys})"),
        None => "Desktop overview".to_owned(),
    };
    queue_chip_face(Look::Strip, false, false, t)
        .key("queue.desktop-overview")
        .role("button")
        .focusable(true)
        .label(label)
        .on_press_tx(|state: &mut State, tx: &mut Tx| {
            crate::dispatch(state, crate::Action::DesktopOverview, tx);
        })
        .child(rune::icon(&attn_ui::icon::OVERVIEW, 10.0).color(t.text_dimmed))
}

/// The queue sidebar's foot, as the Tauri app's `.queue-desktop-strip`: a chip per slotted
/// desktop, the others as one, the overview's chip, and the window's desktop by name with the
/// overview's keys.
pub(crate) fn desktop_strip(state: &State, bands: &Bands<'_>, t: &Tokens) -> El {
    let all = chips(state, turns_on(bands));
    let current = all.iter().find(|c| c.selected).map(|c| c.title.clone());
    let (chips, extras) = slotted(all);
    let overview_keys = crate::palette::label_of(state, &crate::Action::DesktopOverview);
    col()
        .key("queue.strip")
        .w_full()
        .gap(6.0)
        .pt(8.0)
        .px(6.0)
        .pb(6.0)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border)
        .child(
            row()
                .key("queue.desktops")
                .role("group")
                .label("Desktops")
                .w_full()
                .flex_wrap()
                .gap(2.0)
                .children(chips.iter().map(|c| {
                    chip(
                        "queue.desktop",
                        c,
                        Look::Strip,
                        drop_on(state, &c.desktop),
                        t,
                    )
                }))
                .children(extras_chip("queue.desktop-extras", extras, Look::Strip, t))
                .child(overview_chip(overview_keys.clone(), t)),
        )
        .child(
            row()
                .key("queue.desktop-current")
                .w_full()
                .items_center()
                .gap(6.0)
                .child(
                    text(current.unwrap_or_default())
                        .font(type_::MONO_CAPTION)
                        .color(t.text_tertiary)
                        .single_line()
                        .truncate()
                        .shrink()
                        .grow(1.0),
                )
                .children(overview_keys.map(|keys| {
                    text(keys)
                        .font(rune::TextStyle::new(10.0, 400.0).mono())
                        .color(t.text_dimmed)
                })),
        )
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

/// The desktop sidebar collapsed, as the Tauri app's `.icon-rail`: Home, a divider, and a
/// chip for every desktop by its number. The title bar's toggle brings the sidebar back.
pub(crate) fn rail(state: &State, t: &Tokens) -> El {
    let chips = chips(state, wanting_on(state));
    let home = state.window.view == View::Home;
    col()
        .key("rail")
        .role("navigation")
        .label("Rail")
        .w(44.0)
        .h_full()
        .items_center()
        .pt(12.0)
        .px(7.0)
        .fill(t.bg_panel)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
        .child(
            rail_button(home, t)
                .key("rail.home")
                .role("button")
                .focusable(true)
                .label("Home")
                .a11y_value(if home { "current" } else { "" })
                .on_press(|state: &mut State| state.window.go_home())
                .child(rune::icon(&attn_ui::icon::HOME, 16.0).color(if home {
                    t.text_primary
                } else {
                    t.text_dimmed
                })),
        )
        .child(el().w(24.0).h(1.0).my(6.0).fill(t.border))
        .child(
            col()
                .key("rail.desktops")
                .role("group")
                .label("Desktops")
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .items_center()
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .children(chips.iter().map(|c| rail_chip(c, t))),
        )
}

/// A button of the rail, as the Tauri app's `.icon-btn`: 30 points square, filled while it is
/// where the window is.
fn rail_button(active: bool, t: &Tokens) -> El {
    row()
        .size(30.0, 30.0)
        .mb(4.0)
        .center()
        .radius(7.0)
        .fill(if active {
            t.bg_elevated
        } else {
            rune::Color::TRANSPARENT
        })
        .hover(t.bg_elevated)
}

/// A desktop on the rail: its number chip, with a dot while an agent on it wants the user.
fn rail_chip(chip: &Chip, t: &Tokens) -> El {
    let desktop = chip.desktop.clone();
    let (named, label) = said(chip, Look::Strip);
    rail_button(chip.current, t)
        .key(format!("rail.desktop.{}", chip.number))
        .role("button")
        .focusable(true)
        .label(label)
        .help(named)
        .a11y_value(chip_value(chip.current, chip.empty))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
        .child(
            super::desktop_number(chip.number, chip.current, chip.empty, t).children(
                (chip.waiting > 0).then(|| {
                    el().pin(None, Some(-4.0), Some(-4.0), None)
                        .size(8.0, 8.0)
                        .radius(4.0)
                        .fill(t.state_waiting)
                        .inert()
                }),
            ),
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

/// Queue flow with the sidebar collapsed: the waiting pill in the title bar, as the Tauri
/// app's `.queue-bar-pill`: a dot, the count, then the agents whose turns wait.
pub(crate) fn queue_bar(state: &State, bands: &Bands<'_>, t: &Tokens) -> El {
    let owed = bands.owed();
    let crumbs: Vec<String> = bands
        .turns
        .iter()
        .take(3)
        .map(|r| super::model::display_name(r.session))
        .collect();
    let more = bands.turns.len().saturating_sub(crumbs.len());
    let count = super::queue_view::owed_label(owed).unwrap_or_else(|| "Nothing owed".into());
    let mut said = count.clone();
    if !crumbs.is_empty() {
        said.push_str(" · ");
        said.push_str(&crumbs.join(" › "));
    }
    if more > 0 {
        said.push_str(&format!(" +{more}"));
    }
    let sep = |glyph: &str| {
        text(glyph.to_owned())
            .font(type_::LABEL)
            .color(t.text_dimmed)
    };
    let mut trail = Vec::new();
    for (n, crumb) in crumbs.into_iter().enumerate() {
        trail.push(sep(if n == 0 { "·" } else { "›" }));
        trail.push(
            text(crumb)
                .font(type_::LABEL)
                .color(t.text_secondary)
                .single_line()
                .truncate()
                .shrink(),
        );
    }
    if more > 0 {
        trail.push(
            text(format!("+{more}"))
                .font(type_::LABEL)
                .color(t.text_tertiary),
        );
    }
    let pill = row()
        .key("queue-bar.pill")
        .role("button")
        .focusable(true)
        .label(said)
        .a11y_value(owed.0.to_string())
        .on_press_tx(|state: &mut State, tx: &mut Tx| {
            super::bar_peek::dismiss(state);
            crate::dispatch(state, crate::Action::UiActionMenu, tx);
        })
        .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
            super::bar_peek::anchor_hovered(state, super::bar_peek::Anchor::Waiting, inside)
        })
        .shrink()
        .min_w(124.0)
        .max_w(640.0)
        .h(24.0)
        .px(12.0)
        .gap(8.0)
        .items_center()
        .radius(999.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(
            row()
                .items_center()
                .gap(6.0)
                .child(el().size(7.0, 7.0).radius(3.5).fill(if owed.0 > 0 {
                    t.warning
                } else {
                    t.text_dimmed
                }))
                .child(
                    text(count)
                        .font(rune::TextStyle {
                            weight: 600.0,
                            ..type_::LABEL
                        })
                        .color(t.text_primary)
                        .single_line(),
                ),
        )
        .children(trail)
        .child(
            text(attn_ui::glyph::CARET_DOWN)
                .font(rune::TextStyle::new(9.0, 400.0))
                .color(t.text_dimmed),
        );
    row()
        .key("queue-bar")
        .role("group")
        .label("Queue bar")
        .shrink()
        .h_full()
        .items_center()
        .gap(6.0)
        .child(pill)
        .children(runs_chip(state, t))
}

/// The queue bar's desktop chips, which the title bar holds after the tile shelf, as the
/// Tauri app's `.queue-bar-desktops`.
pub(crate) fn queue_bar_desktops(state: &State, bands: &Bands<'_>, t: &Tokens) -> El {
    let (chips, extras) = slotted(chips(state, turns_on(bands)));
    row()
        .key("queue-bar.desktops")
        .role("group")
        .label("Desktops")
        .h_full()
        .items_center()
        .gap(4.0)
        .children(chips.iter().map(|c| {
            let drop = drop_on(state, &c.desktop);
            chip("queue-bar.desktop", c, Look::Bar, drop, t)
        }))
        .children(extras_chip(
            "queue-bar.desktop-extras",
            extras,
            Look::Bar,
            t,
        ))
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
