use std::collections::{HashMap, HashSet};

use attn_ui::Tokens;
use attn_ui::blocks::{Inline, Rail};
use attn_ui::system::{self, IconSize, ListRow, Type, words};
use rune::{El, Tx};
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

/// Where a queue chip sits, which sets its look: the queue sidebar's strip or the queue bar.
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
        false,
        system::Size::Compact,
        t,
    )
    .help(named)
    .a11y_value(chip_value(chip.current, chip.empty))
    .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
    .children(waiting_mark(
        &format!("{prefix}.{slot}"),
        chip.waiting,
        look,
        t,
    ));
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

/// The turns waiting on a chip's desktops, over its top right corner: a dot on the strip, their
/// number on the bar.
fn waiting_mark(chip: &str, waiting: usize, look: Look, t: &Tokens) -> Option<El> {
    (waiting > 0).then(|| match look {
        Look::Strip => waiting_dot(t),
        Look::Bar => system::count_badge(waiting, t.warning, attn_ui::ink_on(t.warning, t))
            .key(format!("{chip}.waiting"))
            .role("none")
            .pin(None, Some(-BADGE_RISE), Some(-BADGE_OVERHANG), None)
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
            false,
            system::Size::Compact,
            t,
        )
        .on_press_tx(|state: &mut State, tx: &mut Tx| {
            crate::dispatch(state, crate::Action::DesktopOverview, tx);
        })
        .children(waiting_mark(key, waiting, look, t))
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
        system::Size::Small,
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
    let button = system::chip_button(
        format!("queue.desktop.{slot}"),
        label,
        face,
        false,
        system::Size::Small,
        t,
    )
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
            false,
            system::Size::Small,
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

/// The desktop sidebar collapsed: Home, a divider, a chip for each user desktop by its number,
/// and a single compact Automations chip when unslotted automation-only desktops hold live runs.
/// The title bar's toggle brings the sidebar back.
pub(crate) fn rail(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let by_id: HashMap<&str, &attn_protocol::Session> =
        facts.sessions.iter().map(|s| (s.id.as_str(), s)).collect();
    let crew_launch = super::model::crew_launch_desktops(facts);
    let desktop_by_id: HashMap<&str, &attn_client::Desktop> =
        facts.desktops.iter().map(|d| (d.id.as_str(), d)).collect();
    let chips: Vec<Chip> = chips(state, wanting_on(state))
        .into_iter()
        .filter(|c| {
            let Some(desktop) = desktop_by_id.get(c.desktop.as_str()) else {
                return true;
            };
            if super::model::is_automation_desktop(desktop, &by_id) {
                return false;
            }
            if !c.current
                && (super::model::is_empty_ephemeral_desktop(desktop)
                    || super::model::is_empty_crew_launch_desktop(desktop, &crew_launch))
            {
                return false;
            }
            true
        })
        .collect();
    let automations_chip = rail_automations_chip(state, &by_id, t);
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
        .children(automations_chip.map(|chip| {
            Rail::column()
                .items_center()
                .child(Rail::rule(t))
                .child(chip)
        }))
}

fn rail_automations_chip(
    state: &State,
    by_id: &HashMap<&str, &attn_protocol::Session>,
    t: &Tokens,
) -> Option<El> {
    let facts = &state.facts;
    let home = state.window.view == View::Home;
    let current_id = facts.current_desktop().map(|d| d.id.as_str());
    let mut runs = 0usize;
    let mut needing = 0usize;
    let mut current = false;
    let mut seen: HashSet<&str> = HashSet::new();
    for desktop in ordered_desktops(&facts.desktops) {
        if !super::model::is_automation_desktop(desktop, by_id) {
            continue;
        }
        if !home && current_id == Some(desktop.id.as_str()) {
            current = true;
        }
        for pane in &desktop.panes {
            let Some(session) = by_id.get(pane.session_id.as_str()).copied() else {
                continue;
            };
            if !seen.insert(session.id.as_str()) {
                continue;
            }
            runs += 1;
            if crate::automations::runs::needs_you(session, state.window.now_ms) {
                needing += 1;
            }
        }
    }
    if runs == 0 {
        return None;
    }
    let ink = if current {
        t.text_primary
    } else {
        t.text_tertiary
    };
    let face = system::chip_edge(words(attn_ui::glyph::BOLT, Type::Tag, ink), t);
    let face = if current {
        face.fill(t.bg_selected).border(1.0, t.border_hover)
    } else {
        face
    }
    .children((needing > 0).then(|| waiting_dot(t)));
    let runs_word = if runs == 1 {
        "1 run".to_owned()
    } else {
        format!("{runs} runs")
    };
    let label = if needing > 0 {
        format!(
            "Automations · {runs_word}, {needing} {} you",
            if needing == 1 { "needs" } else { "need" }
        )
    } else {
        format!("Automations · {runs_word}")
    };
    Some(
        system::chip_button(
            "rail.automations",
            label.clone(),
            face,
            false,
            system::Size::Small,
            t,
        )
        .help(label)
        .a11y_value(if current { "current" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            if needing > 0 {
                crate::automations::runs::next_run(state, tx);
            } else {
                crate::automations::toggle(state, tx);
            }
        }),
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
        false,
        system::Size::Small,
        t,
    )
    .help(named)
    .a11y_value(chip_value(chip.current, chip.empty))
    .on_press_tx(move |state: &mut State, tx: &mut Tx| open_desktop(state, &desktop, tx))
}

/// Queue flow with the sidebar collapsed: the title bar's middle as the queue bar, with the
/// waiting pill, the critical notification, the tile shelf, the runs chip and the desktop chips.
/// The pill holds a dot, the count, then the agents whose turns wait.
pub(crate) fn queue_bar(state: &State, bands: &Bands<'_>, shelf: El, t: &Tokens) -> El {
    let owed = bands.owed();
    let crumbs: Vec<String> = bands
        .turns
        .iter()
        .take(3)
        .map(|r| super::model::display_name(r.session, &state.facts.harnesses))
        .collect();
    let more = bands.turns.len().saturating_sub(crumbs.len());
    use super::crowding::{GivenUp, Part};
    let given_up = super::crowding::given_up(state);
    let count = super::queue_view::owed_label(owed).unwrap_or_else(|| "Nothing owed".into());
    let mut said = count.clone();
    if !crumbs.is_empty() {
        said.push_str(" · ");
        said.push_str(&crumbs.join(" › "));
    }
    if more > 0 {
        said.push_str(&format!(" +{more}"));
    }
    let mut trail = String::new();
    for (n, crumb) in crumbs.iter().enumerate() {
        trail.push_str(if n == 0 { "· " } else { " › " });
        trail.push_str(crumb);
    }
    if more > 0 {
        trail.push_str(&format!(" +{more}"));
    }
    if given_up >= GivenUp::Crumbs {
        trail.clear();
    }
    let crumbs_shown = !trail.is_empty();
    let waiting = system::dot(
        if owed.0 > 0 { t.warning } else { t.text_dimmed },
        system::DotShape::Filled,
        system::DotSize::Regular,
    );
    let after = Inline::joined()
        .shrink()
        .children(crumbs_shown.then(|| {
            words(trail, Type::Small, t.text_secondary)
                .single_line()
                .truncate()
                .shrink()
        }))
        .children((given_up < GivenUp::PillWord).then(|| {
            super::crowding::part(
                system::glyph(attn_ui::glyph::CARET_DOWN, IconSize::Small, t.text_dimmed),
                Part::Caret,
            )
        }));
    let after = super::crowding::part(after, Part::Trail);
    let shown = if given_up < GivenUp::PillWord {
        count
    } else {
        owed.0.to_string()
    };
    let pill = El::from(
        system::Button::new(shown)
            .size(system::Size::Compact)
            .leading(waiting)
            .whole()
            .trailing(after)
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                super::bar_peek::dismiss(state);
                crate::dispatch(state, crate::Action::UiActionMenu, tx);
            })
            .build(t),
    )
    .key("queue-bar.pill")
    .label(said)
    .a11y_value(owed.0.to_string())
    .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
        super::bar_peek::anchor_hovered(state, super::bar_peek::Anchor::Waiting, inside)
    })
    .max_w(PILL_MAX_W);
    let pill = if crumbs_shown {
        pill.shrink().min_w(PILL_MIN_W)
    } else {
        pill.shrink_first()
    };
    let crowded = given_up >= GivenUp::Desktops;
    let bar = Inline::joined()
        .key("queue-bar")
        .role("group")
        .label("Queue bar")
        .h_full()
        .child(pill)
        .children(
            (given_up < GivenUp::Strip)
                .then(|| crate::notifications::critical_strip(&state.facts.notifications, t))
                .flatten()
                .map(|strip| {
                    super::crowding::part(
                        strip
                            .shrink()
                            .min_w(CRITICAL_STRIP_MIN_W)
                            .max_w(CRITICAL_STRIP_MAX_W),
                        Part::Strip,
                    )
                }),
        )
        .children((given_up < GivenUp::Shelf).then(|| super::crowding::part(shelf, Part::Shelf)))
        .children(
            (!crowded)
                .then(|| runs_chip(state, t))
                .flatten()
                .map(|runs| super::crowding::part(runs, Part::Runs)),
        )
        .children((given_up < GivenUp::Chips).then(|| {
            let gathered = gathered_width(state, bands);
            super::crowding::part(desktops(state, bands, crowded, t), Part::Chips { gathered })
        }));
    super::crowding::filled(state, bar)
}

/// The queue bar's desktop chips.
fn desktops(state: &State, bands: &Bands<'_>, crowded: bool, t: &Tokens) -> El {
    let (chips, extras) = slotted(chips(state, turns_on(bands)), crowded);
    Inline::joined()
        .key("queue-bar.desktops")
        .role("group")
        .label("Desktops")
        .h_full()
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
        .child(rune::el().w(BADGE_OVERHANG))
}

/// How wide the queue bar's desktop chips stand once a crowded bar gathers them.
fn gathered_width(state: &State, bands: &Bands<'_>) -> f32 {
    let (kept, extras) = slotted(chips(state, turns_on(bands)), true);
    let size = system::Size::Compact;
    let widths: Vec<f32> = kept
        .iter()
        .map(|c| system::chip_width(&c.slot.unwrap_or_default().to_string(), size))
        .chain((extras.count > 0).then(|| system::chip_width(&format!("+{}", extras.count), size)))
        .chain([BADGE_OVERHANG])
        .collect();
    widths.iter().sum::<f32>() + Inline::joined_gap() * (widths.len() - 1) as f32
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
        .size(system::Size::Compact)
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

/// The queue bar pill's narrowest while it shows crumbs, and its widest: the crumbs cut before
/// the pill crowds the desktops. Without crumbs it holds its dot, its count whole and its caret.
const PILL_MIN_W: f32 = 124.0;
const PILL_MAX_W: f32 = 640.0;

/// The narrowest the critical strip stands in the bar: its mark and about ten characters of its
/// sentence; past that it leaves the bar rather than cut its sentence to nothing.
const CRITICAL_STRIP_MIN_W: f32 = 96.0;

/// The widest the critical strip stands in the bar; it shrinks with the waiting pill when the
/// bar is tight.
const CRITICAL_STRIP_MAX_W: f32 = 260.0;

/// How far the bar's waiting count hangs past its chip's corner; the bar keeps as much room
/// after its last chip, so the count never lies over the toolbar.
const BADGE_OVERHANG: f32 = 6.0;

/// How far the bar's waiting count rises above its chip: hugging the chip's corner, and never
/// past the bar's top edge.
const BADGE_RISE: f32 =
    ((crate::shell::TITLE_BAR_HEIGHT - system::Size::Compact.height()) / 2.0).min(4.0);
