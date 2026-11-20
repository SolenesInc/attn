use std::collections::{HashMap, HashSet};

use attn_ui::Tokens;
use attn_ui::blocks::{Inline, Rail};
use attn_ui::system::{self, IconSize, ListRow, Type, words};
use rune::{El, Tx, row};
use rune_kit::ScrollBar;

use super::model::{desktop_number, desktop_title, ordered_desktops};
use super::open_desktop;
use super::queue::Bands;
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
    /// They include slotted desktops a crowded bar has no room for.
    pub crowded: bool,
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
/// that have none, as the queue's chips show them. A crowded bar slots only the selected
/// desktop.
fn slotted(chips: Vec<Chip>, crowded: bool) -> (Vec<Chip>, Extras) {
    let (mut slotted, rest): (Vec<Chip>, Vec<Chip>) = chips
        .into_iter()
        .partition(|c| c.slot.is_some() && (!crowded || c.selected));
    slotted.sort_by_key(|c| c.slot);
    let extras = Extras {
        count: rest.len(),
        waiting: rest.iter().map(|c| c.waiting).sum(),
        selected: rest.iter().any(|c| c.selected),
        crowded,
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
    let face = system::chip_button(
        format!("{prefix}.{slot}"),
        label,
        system::number_chip(slot, chip.current, chip.empty, t),
        chip.selected,
        t,
    )
    .help(named)
    .a11y_value(chip_value(chip.current, chip.empty))
    .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
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
        Look::Strip => waiting_dot(t),
        Look::Bar => system::count_badge(waiting, t.warning, attn_ui::warning_ink(t))
            .pin(None, Some(-BADGE_OVERHANG), Some(-BADGE_OVERHANG), None)
            .inert(),
    })
}

fn extras_chip(key: &str, extras: Extras, look: Look, t: &Tokens) -> Option<El> {
    let Extras {
        count,
        waiting,
        selected,
        crowded,
    } = extras;
    (count > 0).then(|| {
        let ink = if selected {
            t.text_primary
        } else {
            t.text_tertiary
        };
        system::chip_button(
            key,
            format!(
                "{count} more desktop{}{}{}",
                if count == 1 { "" } else { "s" },
                if crowded { "" } else { " without a shortcut" },
                waiting_words(waiting, look)
            ),
            system::chip_edge(words(format!("+{count}"), Type::Tag, ink), t),
            selected,
            t,
        )
        .on_press_tx(|state: &mut State, tx: &mut Tx| {
            crate::dispatch(state, crate::Action::DesktopOverview, tx);
        })
        .children(waiting_mark(waiting, look, t))
    })
}

/// The strip's chip that opens the desktop overview.
fn overview_chip(keys: Option<String>, t: &Tokens) -> El {
    let label = match &keys {
        Some(keys) => format!("Desktop overview ({keys})"),
        None => "Desktop overview".to_owned(),
    };
    system::chip_button(
        "queue.desktop-overview",
        label,
        system::chip_edge(
            system::icon(&attn_ui::icon::OVERVIEW, IconSize::Small, t.text_dimmed),
            t,
        ),
        false,
        t,
    )
    .on_press_tx(|state: &mut State, tx: &mut Tx| {
        crate::dispatch(state, crate::Action::DesktopOverview, tx);
    })
}

/// A slotted desktop's chip on the queue sidebar's strip: its slot in a number chip, with a
/// dot over its corner while turns wait on it.
fn strip_chip(chip: &Chip, drop: Option<DesktopMark>, t: &Tokens) -> El {
    let desktop = chip.desktop.clone();
    let (named, label) = said(chip, Look::Strip);
    let slot = chip.slot.unwrap_or_default();
    let face = system::number_chip(slot, chip.selected, chip.empty, t)
        .children((chip.waiting > 0).then(|| waiting_dot(t)));
    let button = system::chip_button(format!("queue.desktop.{slot}"), label, face, false, t)
        .help(named)
        .a11y_value(chip_value(chip.current, chip.empty))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx));
    dropped(button, drop, t)
}

/// The strip's chip for the desktops without a slot, as their count.
fn strip_extras(extras: Extras, t: &Tokens) -> Option<El> {
    let Extras {
        count,
        waiting,
        selected,
        crowded,
    } = extras;
    (count > 0).then(|| {
        let ink = if selected {
            t.text_primary
        } else {
            t.text_tertiary
        };
        let face = system::chip_edge(words(format!("+{count}"), Type::Tag, ink), t)
            .children((waiting > 0).then(|| waiting_dot(t)));
        system::chip_button(
            "queue.desktop-extras",
            format!(
                "{count} more desktop{}{}{}",
                if count == 1 { "" } else { "s" },
                if crowded { "" } else { " without a shortcut" },
                waiting_words(waiting, Look::Strip)
            ),
            face,
            selected,
            t,
        )
        .on_press_tx(|state: &mut State, tx: &mut Tx| {
            crate::dispatch(state, crate::Action::DesktopOverview, tx);
        })
    })
}

/// A dot over a desktop chip's corner while an agent on it wants the user.
fn waiting_dot(t: &Tokens) -> El {
    system::ringed_dot(t.warning, t.bg_panel).pin(
        None,
        Some(-DOT_OVERHANG),
        Some(-DOT_OVERHANG),
        None,
    )
}

/// How far a desktop chip's waiting dot hangs past its corner: half the dot.
const DOT_OVERHANG: f32 = system::RINGED_DOT / 2.0;

/// The queue sidebar's foot: a chip per slotted desktop, the others as one, the overview's
/// chip, and the window's desktop by name with the overview's keys.
pub(crate) fn desktop_strip(state: &State, bands: &Bands<'_>, t: &Tokens) -> El {
    let all = chips(state, turns_on(bands));
    let current = all.iter().find(|c| c.selected).map(|c| c.title.clone());
    let (chips, extras) = slotted(all, false);
    let overview_keys = crate::palette::label_of(state, &crate::Action::DesktopOverview);
    ListRow::column()
        .key("queue.strip")
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border)
        .child(
            Inline::tight()
                .key("queue.desktops")
                .role("group")
                .label("Desktops")
                .w_full()
                .children(
                    chips
                        .iter()
                        .map(|c| strip_chip(c, drop_on(state, &c.desktop), t)),
                )
                .children(strip_extras(extras, t))
                .child(overview_chip(overview_keys.clone(), t)),
        )
        .child(
            ListRow::note(
                "queue.desktop-current",
                current.unwrap_or_default(),
                Type::Mono,
                t.text_tertiary,
                overview_keys,
                t,
            )
            .role("note"),
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

/// The desktop sidebar collapsed: Home, a divider, and a chip for every desktop by its number.
/// The title bar's toggle brings the sidebar back.
pub(crate) fn rail(state: &State, t: &Tokens) -> El {
    let chips = chips(state, wanting_on(state));
    let home = state.window.view == View::Home;
    let home_button =
        system::icon_button("rail.home", "Home", &attn_ui::icon::HOME, IconSize::Regular)
            .selected(home)
            .ink(if home { t.text_primary } else { t.text_dimmed })
            .on_press(|state: &mut State| state.window.go_home())
            .build(t);
    Rail::column()
        .key("rail")
        .role("navigation")
        .label("Rail")
        .h_full()
        .fill(t.bg_panel)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
        .child(El::from(home_button).a11y_value(if home { "current" } else { "" }))
        .child(Rail::rule(t))
        .child(
            Rail::column()
                .key("rail.desktops")
                .role("group")
                .label("Desktops")
                .grow(1.0)
                .min_h(0.0)
                .items_center()
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .children(chips.iter().map(|c| rail_chip(c, t)))
                .scroll_bar_styled(attn_ui::scroll_bar(t)),
        )
}

/// A desktop on the rail: its number chip, with a dot while an agent on it wants the user.
fn rail_chip(chip: &Chip, t: &Tokens) -> El {
    let desktop = chip.desktop.clone();
    let (named, label) = said(chip, Look::Strip);
    let face = system::number_chip(chip.number, chip.current, chip.empty, t)
        .children((chip.waiting > 0).then(|| waiting_dot(t)));
    system::chip_button(
        format!("rail.desktop.{}", chip.number),
        label,
        face,
        chip.current,
        t,
    )
    .help(named)
    .a11y_value(chip_value(chip.current, chip.empty))
    .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
}

/// Queue flow with the sidebar collapsed: the title bar's middle as the Tauri app's `.queue-bar`,
/// with the waiting pill, the critical notification, the tile shelf, the runs chip and the
/// desktop chips. The pill is the Tauri app's `.queue-bar-pill`: a dot, the count, then the
/// agents whose turns wait.
pub(crate) fn queue_bar(state: &State, bands: &Bands<'_>, shelf: El, t: &Tokens) -> El {
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
    let sep = |glyph: &str| words(glyph.to_owned(), Type::Label, t.text_dimmed);
    let mut trail = Vec::new();
    for (n, crumb) in crumbs.into_iter().enumerate() {
        trail.push(sep(if n == 0 { "·" } else { "›" }));
        trail.push(
            words(crumb, Type::Label, t.text_secondary)
                .single_line()
                .truncate()
                .shrink(),
        );
    }
    if more > 0 {
        trail.push(words(format!("+{more}"), Type::Label, t.text_tertiary));
    }
    let waiting = system::dot(
        if owed.0 > 0 { t.warning } else { t.text_dimmed },
        system::DotShape::Filled,
        system::DotSize::Regular,
    );
    let content = Inline::line()
        .child(waiting)
        .child(words(count, Type::Name, t.text_primary).single_line())
        .children(trail);
    let pill = El::from(
        system::Trigger::new("queue-bar.pill", said, content)
            .compact()
            .open(crate::palette::shows_agents(state))
            .value(owed.0.to_string())
            .build(t, |state: &mut State, tx: &mut Tx| {
                super::bar_peek::dismiss(state);
                crate::dispatch(state, crate::Action::UiActionMenu, tx);
            }),
    )
    .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
        super::bar_peek::anchor_hovered(state, super::bar_peek::Anchor::Waiting, inside)
    })
    .shrink()
    .min_w(PILL_MIN_W)
    .max_w(PILL_MAX_W);
    let crowded = super::crowding::crowded(state);
    row()
        .key("queue-bar")
        .role("group")
        .label("Queue bar")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .items_center()
        .gap(6.0)
        .on_measure(super::crowding::bar_laid_out)
        .child(pill)
        .children(
            crate::notifications::critical_strip(&state.facts.notifications, t).map(|s| s.w(240.0)),
        )
        .child(shelf)
        .children((!crowded).then(|| runs_chip(state, t)).flatten())
        .child(desktops(state, bands, crowded, t))
}

/// The queue bar's desktop chips, as the Tauri app's `.queue-bar-desktops`.
fn desktops(state: &State, bands: &Bands<'_>, crowded: bool, t: &Tokens) -> El {
    let (chips, extras) = slotted(chips(state, turns_on(bands)), crowded);
    row()
        .key("queue-bar.desktops")
        .role("group")
        .label("Desktops")
        .h_full()
        .items_center()
        .gap(4.0)
        .on_measure(move |state: &mut State, rect| {
            super::crowding::chips_laid_out(state, rect, crowded)
        })
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
    let button = system::Button::new(format!("{} {runs}", attn_ui::glyph::STAR_OPEN))
        .size(system::Size::Small)
        .on_press_tx(|state: &mut State, tx: &mut Tx| {
            super::bar_peek::dismiss(state);
            crate::automations::runs::next_run(state, tx)
        });
    let button = match needing {
        0 => button,
        n => button.trailing(words(n.to_string(), Type::Tag, t.warning)),
    };
    Some(
        El::from(button.build(t))
            .key(super::bar_peek::RUNS)
            .label(format!("{runs} runs"))
            .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
                super::bar_peek::anchor_hovered(state, super::bar_peek::Anchor::Runs, inside)
            })
            .a11y_value(format!("{runs} runs, {needing} need you"))
            .description(says.join(" · ")),
    )
}

/// The queue bar pill's narrowest and widest, as the Tauri app's `.queue-bar-pill`: the count
/// whole, and the crumbs cut before the pill crowds the desktops.
const PILL_MIN_W: f32 = 124.0;
const PILL_MAX_W: f32 = 640.0;

/// How far the bar's waiting count hangs past its chip's corner, as the Tauri app's
/// `.queue-bar-desktop-waiting` at -6.
const BADGE_OVERHANG: f32 = 6.0;
