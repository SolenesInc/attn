//! The Analytics page: its stats, the machine settings, the list and what the selected row holds.

use attn_protocol::{GetAnalyticsStatusResultMessage, Seed, Vent};
use attn_ui::Tokens;
use attn_ui::blocks::{
    Actions, Block, Facts, Group, Inline, Inspector, List, Notice, Prose, Section, SettingRow,
    inspector,
};
use attn_ui::keycap;
use attn_ui::system::{
    self, DotShape, DotSize, ListRow, Look, Tone, Type, chip_button, count_badge, dot, keys_hint,
    tag, words,
};
use rune::{El, Tx, el};
use rune_kit::{Toast, ToastRegion};

use super::items::{self, Item, SEND_SENT, VENT_TOOLS, VENTS, VIEWS};
use super::{Analytics, Confirm, Records, SEND_VENTS, TOOL_VENTS};
use crate::State;

pub(super) const PAGE: &str = "settings.analytics";
pub(super) const LIST: &str = "settings.analytics.list";

/// A vent longer than this shows its start until asked for all of it.
const LONG_VENT_CHARS: usize = 420;

pub(in crate::settings) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let page = &state.window.settings.analytics;
    let sharing = super::sharing(state);
    let items = super::listed(state);
    let index = super::current(&items, page.selected.as_deref());
    let current = items.get(index);
    let status = page.status.as_ref();
    let unreachable: Vec<&str> = state
        .facts
        .outposts
        .iter()
        .filter(|o| o.reach == "unreachable")
        .map(|o| o.name.as_str())
        .collect();
    let warning = status
        .filter(|_| sharing)
        .and_then(|s| s.ingest_off_reason.as_deref().or(s.last_refusal.as_deref()));
    let outposts_note = (!unreachable.is_empty()).then(|| {
        let held = if unreachable.len() == 1 {
            "it holds"
        } else {
            "they hold"
        };
        Prose::hint(
            "settings.analytics.outposts",
            format!(
                "{}: unreachable, so what {held} isn't shown here.",
                unreachable.join(", ")
            ),
        )
        .build(t)
    });
    let group = Group::new(PAGE, "Analytics records and vents")
        .keys(super::key)
        .push(stats(
            state,
            page,
            sharing,
            unreachable.len(),
            t,
            machine_toggle(page, t),
        ))
        .push_all(
            warning.map(|w| Notice::warn("settings.analytics.refusal", w.to_owned()).build(t)),
        )
        .push_all(outposts_note)
        .push_all(page.machine.then(|| machine(state, page, sharing, t)))
        .push(views(state, page.view, t))
        .push(inspector::beside_list(
            rows(state, page, &items, index, t),
            selected(state, page, current, sharing, t),
        ))
        .push(hints(page.view, sharing, t));
    vec![group.build(t)]
}

/// The last action's outcome over the dialog, with Undo when it can be taken back.
pub(in crate::settings) fn toast(state: &State, t: &Tokens) -> Option<El> {
    if state.window.settings.section != crate::settings::Section::Analytics {
        return None;
    }
    let said = state.window.settings.analytics.said.as_ref()?;
    let k = crate::settings::pages::kit(t);
    let mut toast = Toast::new("settings.analytics.toast", said.title.clone());
    if let Some(detail) = &said.detail {
        toast = toast.description(detail.clone());
    }
    if said.failed {
        toast = toast.lead(words("!", Type::Name, t.error));
    }
    if said.undo.is_some() {
        toast = toast.action(k, "Undo", false, |state: &mut State, tx: &mut Tx| {
            super::undo(state, tx);
        });
    }
    Some(ToastRegion::new().toast(toast.build(k)).build(k))
}

/// Sharing, the vents, what is kept here and the outposts, as tiles.
fn stats(
    state: &State,
    page: &Analytics,
    sharing: bool,
    unreachable: usize,
    t: &Tokens,
    toggle: El,
) -> Block {
    let status = page.status.as_ref();
    let vents = &state.facts.vents;
    let sent = vents
        .iter()
        .filter(|v| v.send.as_deref() == Some(SEND_SENT))
        .count();
    let refused = sharing && status.is_some_and(|s| s.last_refusal.is_some());
    let vents_value = if sharing {
        format!("{sent} sent · {} kept here", vents.len() - sent)
    } else {
        format!("{} kept here", vents.len())
    };
    let kept = match status {
        Some(s) => format!("7 days · {}", items::bytes(s.bytes)),
        None => "7 days".to_owned(),
    };
    let outposts = state.facts.outposts.len();
    let key = |id: &str| format!("settings.analytics.stat.{id}");
    let mut facts = Facts::new("settings.analytics.stats")
        .tiles()
        .code(key("sharing"), "Sharing", sharing_stat(sharing, status))
        .tone(if refused {
            Some(Tone::Warn)
        } else {
            (!sharing).then_some(Tone::Neutral)
        })
        .code(key("vents"), "Vents", vents_value)
        .tone(vents.is_empty().then_some(Tone::Neutral))
        .code(key("kept"), "Kept here", kept);
    if outposts > 0 {
        let value = if unreachable > 0 {
            format!("{unreachable} of {outposts} unreachable")
        } else {
            "Up to 5 min behind".to_owned()
        };
        facts = facts
            .code(key("outposts"), "Outposts", value)
            .tone((unreachable > 0).then_some(Tone::Warn));
    }
    facts.after(toggle).build(t)
}

/// The button that opens the machine settings and closes them again.
fn machine_toggle(page: &Analytics, t: &Tokens) -> El {
    let word = if page.machine {
        "Done"
    } else {
        "Machine settings"
    };
    El::from(
        system::Button::new(word)
            .expanded(page.machine)
            .trailing(keycap::Keys::ledged(&["m"]).build(t).a11y_hidden())
            .on_press(super::toggle_machine)
            .build(t)
            .key("settings.analytics.machine-toggle"),
    )
    .help(shortcut_tip(word, "m"))
    .shortcut_hint("M")
}

fn sharing_stat(sharing: bool, status: Option<&GetAnalyticsStatusResultMessage>) -> String {
    match status {
        _ if !sharing => "Off".to_owned(),
        None => "On".to_owned(),
        Some(s) if s.last_refusal.is_some() => {
            format!("Refused · {} waiting", items::count(s.waiting))
        }
        Some(s) => format!(
            "On · {}",
            items::sharing_line(s).replace("Last sent", "sent")
        ),
    }
}

fn slug(seeds: &[std::sync::Arc<Seed>], id: &str) -> String {
    seeds
        .iter()
        .find(|s| s.id == id)
        .map_or_else(|| id.to_owned(), |s| s.step_slug.clone())
}

fn row_label(state: &State, item: &Item) -> String {
    match item {
        Item::Vent { vent, done } => {
            let mut parts = vec![vent.context.session_label.clone()];
            if vent.kind == VENT_TOOLS {
                parts.push("tools".into());
            }
            parts.push(vent.text.clone());
            if *done {
                parts.push(match (&vent.planted_seed_id, &vent.withdrawn_at) {
                    (_, Some(_)) => "withdrawn".to_owned(),
                    (Some(seed), None) => slug(crate::garden::seeds(state), seed),
                    (None, None) => "dismissed".to_owned(),
                });
            }
            parts.push(items::age(&vent.created_at, state.window.now_ms));
            parts.join(" · ")
        }
        Item::Record(record) => format!(
            "{} · {} · {}",
            items::clock_time(&record.at),
            record.name,
            record.stream
        ),
        Item::Burst(records) => format!(
            "{} · {} ×{} · {}",
            items::clock_time(&records[0].at),
            records[0].name,
            records.len(),
            records[0].stream
        ),
    }
}

fn shortcut_tip(label: &str, key: &str) -> String {
    format!("{label}  {}", key.to_uppercase())
}

/// The lists to show, as tabs: Vents, All records and each kind of record.
fn views(state: &State, view: usize, t: &Tokens) -> Block {
    let fresh = state
        .facts
        .vents
        .iter()
        .filter(|v| !items::is_done(v))
        .count();
    let tabs = VIEWS.iter().enumerate().map(|(i, (id, name))| {
        let on = i == view;
        let count = (i == VENTS && fresh > 0).then_some(fresh);
        let face = Inline::tight()
            .child(words(
                *name,
                Type::Small,
                if on { t.text_primary } else { t.text_secondary },
            ))
            .children(count.map(|count| {
                count_badge(
                    count,
                    t.brand,
                    attn_ui::colors::mix_srgb(rune::Color::BLACK, t.brand, 0.11),
                )
            }));
        let label = match count {
            Some(count) => format!("{name} {count}"),
            None => (*name).to_owned(),
        };
        chip_button(format!("settings.analytics.views.{id}"), label, face, on, t)
            .role("tab")
            .focus_selected(on)
            .preserve_focus_on_press(true)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::switch_view(state, i, tx))
    });
    Block::control(
        "settings.analytics.views.slot",
        Inline::loose()
            .key("settings.analytics.views")
            .role("tablist")
            .label("Show")
            .focus_group()
            .children(tabs),
    )
}

/// The rows of the shown list; the vents fall under New and Done.
fn rows(state: &State, page: &Analytics, items: &[Item], index: usize, t: &Tokens) -> Block {
    let name = VIEWS[page.view].1;
    let row = |i: usize, item: &Item| {
        let key = item.key();
        let (title, line) = match item {
            Item::Vent { vent, done } => {
                let mut line = vec![vent.context.session_label.clone()];
                if vent.kind == VENT_TOOLS {
                    line.push("tools".into());
                }
                if *done {
                    line.push(match (&vent.planted_seed_id, &vent.withdrawn_at) {
                        (_, Some(_)) => "withdrawn".to_owned(),
                        (Some(seed), None) => slug(crate::garden::seeds(state), seed),
                        (None, None) => "dismissed".to_owned(),
                    });
                }
                line.push(items::age(&vent.created_at, state.window.now_ms));
                (vent.text.clone(), line.join(" · "))
            }
            Item::Record(record) => (
                record.name.clone(),
                format!("{} · {}", items::clock_time(&record.at), record.stream),
            ),
            Item::Burst(records) => (
                format!("{} ×{}", records[0].name, records.len()),
                format!(
                    "{} · {}",
                    items::clock_time(&records[0].at),
                    records[0].stream
                ),
            ),
        };
        ListRow::new(format!("item{i}"), row_label(state, item))
            .title(title)
            .subtitle(line, t)
            .in_view(i == index)
            .role("option")
            .unfocusable()
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick(state, key.clone(), tx))
            .build(t)
            .focus_selected(i == index)
    };
    if page.view == VENTS {
        let (new, done): (Vec<_>, Vec<_>) = items
            .iter()
            .enumerate()
            .partition(|(_, item)| !matches!(item, Item::Vent { done: true, .. }));
        let part = |key: &str, label: &'static str, rows: Vec<(usize, &Item)>| {
            (!rows.is_empty()).then(|| {
                List::new(format!("{LIST}.{key}"), label)
                    .heading()
                    .listbox()
                    .rows(rows.into_iter().map(|(i, item)| row(i, item)))
                    .build(t)
            })
        };
        return Group::<State>::new(LIST, name)
            .active((!items.is_empty()).then(|| format!("item{index}")))
            .push_all(part("new", "New", new))
            .push_all(part("done", "Done", done))
            .build(t);
    }
    let said = match &page.records {
        Some(r) if !r.loading && r.error.is_none() => "Nothing recorded yet.",
        Some(r) if !r.loading => "",
        _ => "Reading the record…",
    };
    Group::<State>::new(LIST, name)
        .active((!items.is_empty()).then(|| format!("item{index}")))
        .push(
            List::new(format!("{LIST}.rows"), name)
                .listbox()
                .rows(items.iter().enumerate().map(|(i, item)| row(i, item)))
                .empty(format!("{LIST}.empty"), said, None)
                .build(t),
        )
        .push_all(footer(page, items, t))
        .build(t)
}

/// What the record list holds and the buttons that read more of it.
fn footer(page: &Analytics, items: &[Item], t: &Tokens) -> Vec<Block> {
    let records = page.records.as_ref();
    let loading = records.is_none_or(|r| r.loading);
    let summary = match records {
        Some(Records {
            error: Some(error), ..
        }) => Some(Notice::warn("settings.analytics.footer.error", error.clone()).build(t)),
        Some(r) if !items.is_empty() => Some(
            Prose::hint(
                "settings.analytics.footer.count",
                format!("{} newest first", items::count(r.list.len() as i64)),
            )
            .build(t),
        ),
        _ => None,
    };
    let mut actions = Actions::new("settings.analytics.footer");
    if records.is_some_and(|r| r.more) {
        actions = actions.push(El::from(
            system::Button::new("Older")
                .look(Look::Quiet)
                .size(system::Size::Compact)
                .disabled(loading)
                .on_press_tx(super::older)
                .build(t)
                .key("settings.analytics.older"),
        ));
    }
    actions = actions.push(
        El::from(
            system::Button::new("Refresh")
                .look(Look::Quiet)
                .size(system::Size::Compact)
                .disabled(loading)
                .on_press_tx(super::refresh)
                .build(t)
                .key("settings.analytics.refresh"),
        )
        .help(shortcut_tip("Refresh", "r"))
        .shortcut_hint("R"),
    );
    summary.into_iter().chain([actions.build()]).collect()
}

/// What the selected row holds, in the column beside the list.
fn selected(
    state: &State,
    page: &Analytics,
    current: Option<&Item>,
    sharing: bool,
    t: &Tokens,
) -> El {
    let column = Inspector::side("settings.analytics.inspector", "Selected");
    match current {
        Some(Item::Vent { vent, .. }) => vent_detail(state, page, vent, sharing, column, t),
        Some(Item::Record(record)) => {
            record_detail(std::slice::from_ref(*record), false, column, t)
        }
        Some(Item::Burst(records)) => record_detail(records, true, column, t),
        None if page.view == VENTS => column
            .push(words("No vents yet", Type::Name, t.text_primary))
            .push(
                words(
                    "Agents launched since you turned this on can send one.",
                    Type::Small,
                    t.text_tertiary,
                )
                .wrap(),
            ),
        None => column,
    }
    .in_page(t)
}

/// A fact of the selected row: its label, and its value keyed `key` as the value alone.
fn fact(facts: Facts, key: &str, label: &'static str, value: String, t: &Tokens) -> Facts {
    let key = format!("settings.analytics.{key}");
    facts.item(
        format!("{key}.fact"),
        label,
        inspector::value(value, t).key(key),
    )
}

fn record_detail(
    records: &[items::Stored],
    burst: bool,
    column: Inspector,
    t: &Tokens,
) -> Inspector {
    let first = &records[0];
    let last = &records[records.len() - 1];
    let heading = if burst {
        format!("{} records in the same second", records.len())
    } else {
        "Record".to_owned()
    };
    let seq = if records.len() > 1 {
        format!("{}–{}", last.seq, first.seq)
    } else {
        first.seq.to_string()
    };
    let facts = Facts::new("settings.analytics.record.facts");
    let facts = fact(facts, "record.stream", "Stream", first.stream.clone(), t);
    let facts = fact(facts, "record.seq", "Seq", seq, t);
    let facts = fact(facts, "record.at", "At", items::when(&first.at), t);
    column
        .push(
            words(heading.clone(), Type::Name, t.text_primary)
                .key("settings.analytics.record.heading")
                .role("heading")
                .label(heading),
        )
        .block(facts.build(t))
        .push(words("Exactly as stored", Type::Name, t.text_primary))
        .push(
            Inline::tight()
                .key("settings.analytics.record.raw")
                .children(
                    records.iter().map(|r| {
                        words(r.raw.clone(), Type::Mono, t.text_secondary).wrap_anywhere()
                    }),
                ),
        )
}

fn vent_detail(
    state: &State,
    page: &Analytics,
    vent: &Vent,
    sharing: bool,
    column: Inspector,
    t: &Tokens,
) -> Inspector {
    let seeds = crate::garden::seeds(state);
    let withdrawn = vent.withdrawn_at.is_some();
    let outcome = if withdrawn {
        Some(("Withdrawn by agent".to_owned(), Tone::Neutral))
    } else if let Some(seed) = &vent.planted_seed_id {
        Some((format!("Planted · {}", slug(seeds, seed)), Tone::Good))
    } else if vent.dismissed_at.is_some() {
        Some(("Dismissed".to_owned(), Tone::Neutral))
    } else {
        None
    };
    let label = words(
        vent.context.session_label.clone(),
        Type::Name,
        t.text_primary,
    )
    .wrap();
    let tagged = vent.kind == VENT_TOOLS || outcome.is_some();
    let tags =
        Inline::tight()
            .children((vent.kind == VENT_TOOLS).then(|| {
                El::from(tag(
                    "settings.analytics.vent.tools",
                    "Tools",
                    Tone::Neutral,
                    t,
                ))
            }))
            .children(outcome.map(|(label, tone)| {
                El::from(tag("settings.analytics.vent.outcome", label, tone, t))
            }));
    let chars = vent.text.chars().count();
    let clamped = chars > LONG_VENT_CHARS && !page.expanded.contains(&vent.id);
    let shown: String = if clamped {
        vent.text
            .chars()
            .take(LONG_VENT_CHARS)
            .chain(['…'])
            .collect()
    } else {
        vent.text.clone()
    };
    let body = words(
        shown,
        Type::Body,
        if withdrawn {
            t.text_muted
        } else {
            t.text_primary
        },
    )
    .key("settings.analytics.vent.text")
    .wrap_anywhere();
    let more = clamped.then(|| {
        let id = vent.id.clone();
        El::from(
            system::Button::new(format!(
                "Show all {} characters",
                items::count(chars as i64)
            ))
            .look(Look::Quiet)
            .size(system::Size::Compact)
            .on_press(move |state: &mut State| {
                state.window.settings.analytics.expanded.insert(id.clone());
            })
            .build(t)
            .key("settings.analytics.vent.more"),
        )
    });
    let context = &vent.context;
    let model = items::model_label(context.model.as_deref(), context.model_requested.as_deref());
    let mut agent = [
        Some(crate::sidebar::model::harness_label(&context.harness)),
        context.harness_version.clone(),
    ]
    .into_iter()
    .flatten()
    .collect::<Vec<_>>()
    .join(" ");
    if !model.is_empty() {
        agent = format!("{agent} · {model}");
    }
    let experimental = if context.experimental.is_empty() {
        "none".to_owned()
    } else {
        context
            .experimental
            .iter()
            .map(|key| items::setting_name(key))
            .collect::<Vec<_>>()
            .join(", ")
    };
    let when = format!(
        "{} · {}",
        items::when(&vent.created_at),
        items::long_ago(&vent.created_at, state.window.now_ms)
    );
    let facts = Facts::new("settings.analytics.vent.facts");
    let facts = fact(facts, "vent.when", "When", when, t);
    let facts = fact(facts, "vent.agent", "Agent", agent, t);
    let facts = fact(
        facts,
        "vent.seed",
        "Stake seed",
        context
            .seed_id
            .as_deref()
            .map_or_else(|| "none".to_owned(), |id| slug(seeds, id)),
        t,
    );
    let facts = fact(
        facts,
        "vent.build",
        "Build",
        items::build_label(&context.build),
        t,
    );
    let facts = fact(facts, "vent.experimental", "Experimental", experimental, t);
    let mut column = column.push(label);
    if tagged {
        column = column.push(tags);
    }
    column = column.push(body);
    if let Some(more) = more {
        column = column.push(more);
    }
    column = column.block(facts.build(t));
    if !withdrawn {
        column = column.block(vent_actions(state, vent, t));
    }
    column.push(vent_where(vent, sharing, t))
}

fn action(label: &'static str, key: &'static str, t: &Tokens) -> El {
    El::from(
        system::Button::new(label)
            .size(system::Size::Compact)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::act_on_selected(state, key, tx);
            })
            .build(t)
            .key(format!("settings.analytics.vent.action.{key}")),
    )
    .help(shortcut_tip(label, key))
    .shortcut_hint(key.to_uppercase())
}

fn vent_actions(state: &State, vent: &Vent, t: &Tokens) -> Block {
    let file = if vent.kind == VENT_TOOLS {
        "File under attn"
    } else {
        "File under tools"
    };
    let actions = [
        super::is_live(state, vent).then(|| action("Open session", "o", t)),
        vent.context
            .transcript
            .is_some()
            .then(|| action("Transcript", "t", t)),
        vent.context
            .seed_id
            .is_some()
            .then(|| action("Stake seed", "g", t)),
        vent.planted_seed_id
            .is_none()
            .then(|| action("Plant seed", "p", t)),
        Some(action(
            if vent.dismissed_at.is_some() {
                "Restore"
            } else {
                "Dismiss"
            },
            "d",
            t,
        )),
        (vent.send.as_deref() != Some(SEND_SENT)).then(|| action(file, "s", t)),
    ];
    actions
        .into_iter()
        .flatten()
        .fold(
            Actions::new("settings.analytics.vent.actions"),
            Actions::push,
        )
        .build()
}

fn vent_where(vent: &Vent, sharing: bool, t: &Tokens) -> El {
    let to = items::destination(vent, sharing);
    Inline::tight()
        .key("settings.analytics.vent.where")
        .child(
            dot(
                if to.leaving { t.success } else { t.text_muted },
                DotShape::Filled,
                DotSize::Regular,
            )
            .a11y_hidden(),
        )
        .child(
            words(to.label, Type::Name, t.text_primary).key("settings.analytics.vent.destination"),
        )
        .children((!to.note.is_empty()).then(|| {
            words(format!("· {}", to.note), Type::Small, t.text_tertiary)
                .key("settings.analytics.vent.destination-note")
        }))
        .children(to.movable.then(|| {
            action(
                if vent.send.is_some() {
                    "Keep here"
                } else {
                    "Send"
                },
                "x",
                t,
            )
        }))
}

/// A machine setting: its title, what it says keyed `{key}.copy` (in the warning ink when it
/// warns), and its control.
fn machine_row(
    key: &str,
    title: &'static str,
    copy: Option<(String, bool)>,
    control: El,
    t: &Tokens,
) -> Block {
    let key = format!("settings.analytics.machine.{key}");
    let copy = copy.map(|(said, warns)| {
        words(
            said,
            Type::Small,
            if warns { t.warning } else { t.text_secondary },
        )
        .key(format!("{key}.copy"))
        .wrap()
    });
    SettingRow::new(key, title, "", control).note(copy).build(t)
}

fn confirming(yes: &'static str, on_yes: fn(&mut State, &mut Tx), t: &Tokens) -> El {
    Inline::tight()
        .child(
            system::Button::new(yes)
                .look(Look::Danger)
                .on_press_tx(on_yes)
                .build(t)
                .key("settings.analytics.confirm.yes")
                .into(),
        )
        .child(
            system::Button::new("Keep")
                .on_press(|state: &mut State| state.window.settings.analytics.confirm = None)
                .build(t)
                .key("settings.analytics.confirm.keep")
                .into(),
        )
}

fn asking(label: &'static str, confirm: Confirm, t: &Tokens) -> El {
    system::Button::new(label)
        .look(Look::Danger)
        .on_press(move |state: &mut State| state.window.settings.analytics.confirm = Some(confirm))
        .build(t)
        .into()
}

fn setting_switch(state: &State, key: &'static str, label: &'static str, t: &Tokens) -> El {
    let on = crate::setting::on(&state.facts.settings, key);
    system::switch(
        format!("settings.analytics.switch.{key}"),
        label,
        on,
        system::Beside::Nothing,
        false,
        t,
        move |state: &mut State, tx: &mut Tx| super::set(state, key, !on, tx),
    )
    .into()
}

fn machine(state: &State, page: &Analytics, sharing: bool, t: &Tokens) -> Block {
    let status = page.status.as_ref();
    let send_vents = crate::setting::on(&state.facts.settings, SEND_VENTS);
    let tool_vents = crate::setting::on(&state.facts.settings, TOOL_VENTS);
    let offering_delete = page.confirm == Some(Confirm::DeleteServerCopy);
    let share_copy = if offering_delete {
        Some((
            "Sharing is off. Delete what the server already holds?".to_owned(),
            false,
        ))
    } else if let Some(reason) = status.and_then(|s| s.ingest_off_reason.as_deref()) {
        Some((reason.to_owned(), true))
    } else if let Some(refusal) = status
        .filter(|_| sharing)
        .and_then(|s| s.last_refusal.as_deref())
    {
        Some((refusal.to_owned(), true))
    } else {
        status.map(|s| {
            (
                if sharing {
                    format!(
                        "{} · uploads go to {}",
                        items::sharing_line(s),
                        s.ingest_url
                    )
                } else {
                    format!(
                        "{} would be sent to {}.",
                        items::record_count(s.waiting),
                        s.ingest_url
                    )
                },
                false,
            )
        })
    };
    let share_control = if offering_delete {
        confirming("Delete from server", super::forget, t)
    } else {
        system::switch(
            "settings.analytics.switch.sharing",
            "Share with attn's maintainers",
            sharing,
            system::Beside::Nothing,
            false,
            t,
            super::toggle_sharing,
        )
        .into()
    };
    let kept = format!(
        "{}Dismissed and withdrawn vents are kept 30 days.",
        status
            .map(|s| format!(
                "{}, {}. ",
                items::record_count(s.records),
                items::bytes(s.bytes)
            ))
            .unwrap_or_default()
    );
    let forget = if page.confirm == Some(Confirm::Forget) {
        confirming("Delete from server", super::forget, t)
    } else {
        asking("Forget…", Confirm::Forget, t).key("settings.analytics.forget")
    };
    let clear_copy = format!(
        "{} and dismissed vents, and what each outpost holds. An unreachable outpost clears when it reconnects.",
        status.map_or_else(
            || "Deletes every record".to_owned(),
            |s| format!(
                "Deletes {} ({})",
                items::record_count(s.records),
                items::bytes(s.bytes)
            )
        )
    );
    let clear = if page.confirm == Some(Confirm::Clear) {
        confirming("Clear", super::clear, t)
    } else {
        asking("Clear…", Confirm::Clear, t).key("settings.analytics.clear")
    };
    Section::new("settings.analytics.machine", "Machine settings")
        .push(machine_row(
            "share",
            "Share with attn's maintainers",
            share_copy,
            share_control,
            t,
        ))
        .push_all((sharing || send_vents).then(|| {
            machine_row(
                "send-vents",
                "Send vents",
                Some((
                    if send_vents {
                        "New attn vents are sent with their text."
                    } else {
                        "Vents stay here unless you send one."
                    }
                    .to_owned(),
                    false,
                )),
                setting_switch(state, SEND_VENTS, "Send vents", t),
                t,
            )
        }))
        .push(machine_row(
            "tool-vents",
            "Tool vents",
            Some((
                if tool_vents {
                    "New sessions also vent about their other tools: skills, other CLIs, harness permissions. These never leave this machine."
                } else {
                    "Agents vent only about attn. Turn on to also hear about their other tools (skills, other CLIs, harness permissions), kept on this machine."
                }
                .to_owned(),
                false,
            )),
            setting_switch(state, TOOL_VENTS, "Tool vents", t),
            t,
        ))
        .push(machine_row("kept", "Kept for 7 days", Some((kept, false)), el(), t))
        .push(machine_row(
            "forget",
            "Forget this install",
            Some((
                "Deletes this install's data on the server and starts a new install id.".to_owned(),
                false,
            )),
            forget,
            t,
        ))
        .push(machine_row("clear", "Clear the local record", Some((clear_copy, false)), clear, t))
        .build(t)
}

fn hints(view: usize, sharing: bool, t: &Tokens) -> Block {
    let mut keys: Vec<(&[&str], &'static str)> =
        vec![(&["j", "k"], "move"), (&["[", "]"], "switch list")];
    if view != VENTS {
        keys.extend([
            (&["↵"][..], "expand a burst"),
            (&["c"], "copy JSON"),
            (&["r"], "refresh"),
        ]);
    } else {
        keys.extend([
            (&["o"][..], "open session"),
            (&["t"], "transcript"),
            (&["g"], "stake seed"),
            (&["p"], "plant"),
            (&["d"], "dismiss"),
            (&["s"], "attn or tools"),
            (&["u"], "undo"),
        ]);
        if sharing {
            keys.push((&["x"], "send or keep"));
        }
    }
    keys.push((&["m"], "machine settings"));
    Block::custom(
        "settings.analytics.keys.block",
        Inline::loose()
            .key("settings.analytics.keys")
            .role("group")
            .label("Keys")
            .children(
                keys.into_iter()
                    .map(|(keys, what)| keys_hint(keys.iter().copied(), what, t)),
            ),
    )
}
