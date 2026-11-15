//! The Analytics page: its stats, the machine settings, the list and what the selected row holds.

use attn_protocol::{GetAnalyticsStatusResultMessage, Seed, Vent};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, el, row, text};
use rune_kit::{
    Badge, BadgeVariant, Button, ButtonVariant, ControlSize, Keybind, Kit, List, ListItem,
    SegmentedControl, Toast, ToastRegion,
};

use super::items::{self, Item, SEND_SENT, VENT_TOOLS, VENTS, VIEWS};
use super::{Analytics, Confirm, Records, SEND_VENTS, TOOL_VENTS};
use crate::State;
use crate::settings::pages::{CAPTION, S4, switch};

pub(super) const PAGE: &str = "settings.analytics";
pub(super) const LIST: &str = "settings.analytics.list";

/// A vent longer than this shows its start until asked for all of it.
const LONG_VENT_CHARS: usize = 420;

pub(in crate::settings) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let k = attn_ui::kit(t);
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
    let root = col()
        .key(PAGE)
        .role("group")
        .label("Analytics records and vents")
        .focusable(true)
        .on_key(super::key)
        .w_full()
        .gap(space::S3)
        .child(strip(state, page, sharing, unreachable.len(), t, k))
        .children(warning.map(|w| {
            text(w.to_owned())
                .key("settings.analytics.refusal")
                .font(CAPTION)
                .color(t.warning)
                .wrap()
        }))
        .children((!unreachable.is_empty()).then(|| {
            let held = if unreachable.len() == 1 {
                "it holds"
            } else {
                "they hold"
            };
            text(format!(
                "{}: unreachable, so what {held} isn't shown here.",
                unreachable.join(", ")
            ))
            .key("settings.analytics.outposts")
            .font(CAPTION)
            .color(t.text_tertiary)
            .wrap()
        }))
        .children(page.machine.then(|| machine(state, page, sharing, t, k)))
        .child(views(state, page.view, k))
        .child(
            row()
                .w_full()
                .gap(S4)
                .items_start()
                .child(
                    col()
                        .w(360.0)
                        .gap(space::S2)
                        .child(list(state, page, &items, index, t, k))
                        .children((page.view != VENTS).then(|| footer(page, &items, t, k))),
                )
                .child(
                    col()
                        .key("settings.analytics.inspector")
                        .role("group")
                        .label("Selected")
                        .grow(1.0)
                        .min_w(0.0)
                        .gap(space::S2)
                        .padding(space::S3)
                        .radius(6.0)
                        .fill(t.bg_panel)
                        .border(1.0, t.border_subtle)
                        .children(inspector(state, page, current, sharing, t, k)),
                ),
        )
        .child(hints(page.view, sharing, t, k));
    vec![root]
}

/// The last action's outcome over the dialog, with Undo when it can be taken back.
pub(in crate::settings) fn toast(state: &State, t: &Tokens) -> Option<El> {
    if state.window.settings.section != crate::settings::Section::Analytics {
        return None;
    }
    let said = state.window.settings.analytics.said.as_ref()?;
    let k = attn_ui::kit(t);
    let mut toast = Toast::new("settings.analytics.toast", said.title.clone());
    if let Some(detail) = &said.detail {
        toast = toast.description(detail.clone());
    }
    if said.failed {
        toast = toast.lead(text("!").font(type_::LABEL).color(t.error));
    }
    if said.undo.is_some() {
        toast = toast.action(k, "Undo", false, |state: &mut State, tx: &mut Tx| {
            super::undo(state, tx);
        });
    }
    Some(ToastRegion::new().toast(toast.build(k)).build(k))
}

fn stat(id: &str, label: &str, value: String, tone: Option<rune::Color>, t: &Tokens) -> El {
    col()
        .gap(2.0)
        .child(text(label.to_owned()).font(CAPTION).color(t.text_tertiary))
        .child(
            text(value)
                .key(format!("settings.analytics.stat.{id}"))
                .font(type_::LABEL)
                .color(tone.unwrap_or(t.text_primary)),
        )
}

fn strip(
    state: &State,
    page: &Analytics,
    sharing: bool,
    unreachable: usize,
    t: &Tokens,
    k: &Kit,
) -> El {
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
    row()
        .w_full()
        .gap(20.0)
        .items_center()
        .child(stat(
            "sharing",
            "Sharing",
            sharing_stat(sharing, status),
            if refused {
                Some(t.warning)
            } else {
                (!sharing).then_some(t.text_muted)
            },
            t,
        ))
        .child(stat(
            "vents",
            "Vents",
            vents_value,
            vents.is_empty().then_some(t.text_muted),
            t,
        ))
        .child(stat("kept", "Kept here", kept, None, t))
        .children((outposts > 0).then(|| {
            let value = if unreachable > 0 {
                format!("{unreachable} of {outposts} unreachable")
            } else {
                "Up to 5 min behind".to_owned()
            };
            stat(
                "outposts",
                "Outposts",
                value,
                (unreachable > 0).then_some(t.warning),
                t,
            )
        }))
        .child(el().grow(1.0))
        .child(
            Button::new(if page.machine {
                "Done"
            } else {
                "Machine settings"
            })
            .expanded(page.machine)
            .shortcut("m")
            .on_press(super::toggle_machine)
            .build(k)
            .key("settings.analytics.machine-toggle"),
        )
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

fn views(state: &State, view: usize, k: &Kit) -> El {
    let fresh = state
        .facts
        .vents
        .iter()
        .filter(|v| !items::is_done(v))
        .count();
    let names: Vec<String> = VIEWS
        .iter()
        .enumerate()
        .map(|(i, (_, name))| match i {
            VENTS if fresh > 0 => format!("{name} {fresh}"),
            _ => (*name).to_owned(),
        })
        .collect();
    row().child(
        SegmentedControl::new(&names, Some(view))
            .label("Show")
            .fit()
            .on_pick(|state: &mut State, i: usize, tx: &mut Tx| super::switch_view(state, i, tx))
            .build(k)
            .key("settings.analytics.views"),
    )
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

fn list(state: &State, page: &Analytics, items: &[Item], index: usize, t: &Tokens, k: &Kit) -> El {
    let name = VIEWS[page.view].1;
    if items.is_empty() {
        let said = if page.view == VENTS {
            "No vents yet. Agents launched since you turned this on can send one."
        } else {
            match &page.records {
                Some(r) if !r.loading && r.error.is_none() => "Nothing recorded yet.",
                Some(r) if !r.loading => "",
                _ => "Reading the record…",
            }
        };
        return text(said)
            .key(LIST)
            .font(CAPTION)
            .color(t.text_tertiary)
            .wrap();
    }
    let mut list = List::new().active(Some(index));
    for item in items {
        let key = item.key();
        let mut row = ListItem::new(row_label(state, item))
            .on_select(move |state: &mut State, tx: &mut Tx| super::pick(state, key.clone(), tx));
        if let Item::Vent { done, .. } = item {
            row = row.group(if *done { "Done" } else { "New" });
        }
        list = list.item(row);
    }
    list.build(k).key(LIST).role("group").label(name)
}

fn footer(page: &Analytics, items: &[Item], t: &Tokens, k: &Kit) -> El {
    let records = page.records.as_ref();
    let loading = records.is_none_or(|r| r.loading);
    let summary = match records {
        Some(Records {
            error: Some(error), ..
        }) => text(error.clone()).font(CAPTION).color(t.warning).wrap(),
        Some(r) if !items.is_empty() => text(format!(
            "{} newest first",
            items::count(r.list.len() as i64)
        ))
        .font(CAPTION)
        .color(t.text_tertiary),
        _ => el(),
    };
    row()
        .key("settings.analytics.footer")
        .w_full()
        .gap(space::S2)
        .items_center()
        .child(summary.grow(1.0).min_w(0.0))
        .children(records.filter(|r| r.more).map(|_| {
            Button::new("Older")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .disabled(loading)
                .on_press_tx(super::older)
                .build(k)
                .key("settings.analytics.older")
        }))
        .child(
            Button::new("Refresh")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .disabled(loading)
                .shortcut("r")
                .on_press_tx(super::refresh)
                .build(k)
                .key("settings.analytics.refresh"),
        )
}

fn inspector(
    state: &State,
    page: &Analytics,
    current: Option<&Item>,
    sharing: bool,
    t: &Tokens,
    k: &Kit,
) -> Vec<El> {
    match current {
        Some(Item::Vent { vent, .. }) => vent_detail(state, page, vent, sharing, t, k),
        Some(Item::Record(record)) => record_detail(std::slice::from_ref(*record), false, t),
        Some(Item::Burst(records)) => record_detail(records, true, t),
        None if page.view == VENTS => vec![
            text("No vents yet")
                .font(type_::LABEL)
                .color(t.text_primary),
            text("Agents launched since you turned this on can send one.")
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap(),
        ],
        None => Vec::new(),
    }
}

fn context_row(key: &str, label: &str, value: String, t: &Tokens) -> El {
    row()
        .w_full()
        .gap(space::S3)
        .child(
            text(label.to_owned())
                .font(CAPTION)
                .color(t.text_tertiary)
                .w(96.0),
        )
        .child(
            text(value)
                .key(format!("settings.analytics.{key}"))
                .font(CAPTION)
                .color(t.text_primary)
                .wrap()
                .grow(1.0)
                .min_w(0.0),
        )
}

fn record_detail(records: &[items::Stored], burst: bool, t: &Tokens) -> Vec<El> {
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
    vec![
        text(heading)
            .key("settings.analytics.record.heading")
            .font(type_::LABEL)
            .color(t.text_primary),
        context_row("record.stream", "Stream", first.stream.clone(), t),
        context_row("record.seq", "Seq", seq, t),
        context_row("record.at", "At", items::when(&first.at), t),
        text("Exactly as stored")
            .font(type_::LABEL)
            .color(t.text_primary)
            .pt(space::S2),
        col()
            .key("settings.analytics.record.raw")
            .gap(space::S1)
            .children(records.iter().map(|r| {
                text(r.raw.clone())
                    .font(type_::MONO_CAPTION)
                    .color(t.text_secondary)
                    .wrap()
            })),
    ]
}

fn vent_detail(
    state: &State,
    page: &Analytics,
    vent: &Vent,
    sharing: bool,
    t: &Tokens,
    k: &Kit,
) -> Vec<El> {
    let seeds = crate::garden::seeds(state);
    let withdrawn = vent.withdrawn_at.is_some();
    let outcome = if withdrawn {
        Some(("Withdrawn by agent".to_owned(), BadgeVariant::Subtle))
    } else if let Some(seed) = &vent.planted_seed_id {
        Some((
            format!("Planted · {}", slug(seeds, seed)),
            BadgeVariant::Accent,
        ))
    } else if vent.dismissed_at.is_some() {
        Some(("Dismissed".to_owned(), BadgeVariant::Subtle))
    } else {
        None
    };
    let head = row()
        .gap(space::S2)
        .items_center()
        .child(
            text(vent.context.session_label.clone())
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .children((vent.kind == VENT_TOOLS).then(|| Badge::new("Tools").build(k)))
        .children(outcome.map(|(label, variant)| {
            Badge::new(label)
                .variant(variant)
                .build(k)
                .key("settings.analytics.vent.outcome")
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
    let body = text(shown)
        .key("settings.analytics.vent.text")
        .font(type_::BODY)
        .color(if withdrawn {
            t.text_muted
        } else {
            t.text_primary
        })
        .wrap();
    let more = clamped.then(|| {
        let id = vent.id.clone();
        Button::new(format!(
            "Show all {} characters",
            items::count(chars as i64)
        ))
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .on_press(move |state: &mut State| {
            state.window.settings.analytics.expanded.insert(id.clone());
        })
        .build(k)
        .key("settings.analytics.vent.more")
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
    let mut detail = vec![head, body];
    detail.extend(more);
    detail.extend([
        context_row("vent.when", "When", when, t),
        context_row("vent.agent", "Agent", agent, t),
        context_row(
            "vent.seed",
            "Stake seed",
            context
                .seed_id
                .as_deref()
                .map_or_else(|| "none".to_owned(), |id| slug(seeds, id)),
            t,
        ),
        context_row("vent.build", "Build", items::build_label(&context.build), t),
        context_row("vent.experimental", "Experimental", experimental, t),
    ]);
    if !withdrawn {
        detail.push(vent_actions(state, vent, k));
    }
    detail.push(vent_where(vent, sharing, t, k));
    detail
}

fn action(label: &str, key: &'static str, k: &Kit) -> El {
    Button::new(label)
        .control_size(ControlSize::Small)
        .shortcut(key)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::act_on_selected(state, key, tx);
        })
        .build(k)
        .key(format!("settings.analytics.vent.action.{key}"))
}

fn vent_actions(state: &State, vent: &Vent, k: &Kit) -> El {
    let file = if vent.kind == VENT_TOOLS {
        "File under attn"
    } else {
        "File under tools"
    };
    let actions = [
        super::is_live(state, vent).then(|| action("Open session", "o", k)),
        vent.context
            .transcript
            .is_some()
            .then(|| action("Transcript", "t", k)),
        vent.context
            .seed_id
            .is_some()
            .then(|| action("Stake seed", "g", k)),
        vent.planted_seed_id
            .is_none()
            .then(|| action("Plant seed", "p", k)),
        Some(action(
            if vent.dismissed_at.is_some() {
                "Restore"
            } else {
                "Dismiss"
            },
            "d",
            k,
        )),
        (vent.send.as_deref() != Some(SEND_SENT)).then(|| action(file, "s", k)),
    ];
    row()
        .key("settings.analytics.vent.actions")
        .gap(space::S2)
        .flex_wrap()
        .children(actions.into_iter().flatten())
}

fn vent_where(vent: &Vent, sharing: bool, t: &Tokens, k: &Kit) -> El {
    let to = items::destination(vent, sharing);
    row()
        .key("settings.analytics.vent.where")
        .gap(space::S2)
        .items_center()
        .child(
            el().size(8.0, 8.0)
                .radius(4.0)
                .fill(if to.leaving { t.success } else { t.text_muted })
                .a11y_hidden(),
        )
        .child(
            text(to.label)
                .key("settings.analytics.vent.destination")
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .children((!to.note.is_empty()).then(|| {
            text(format!("· {}", to.note))
                .key("settings.analytics.vent.destination-note")
                .font(CAPTION)
                .color(t.text_tertiary)
        }))
        .children(to.movable.then(|| {
            action(
                if vent.send.is_some() {
                    "Keep here"
                } else {
                    "Send"
                },
                "x",
                k,
            )
        }))
}

fn machine_row(key: &str, title: &str, copy: Option<El>, control: Option<El>, t: &Tokens) -> El {
    row()
        .key(format!("settings.analytics.machine.{key}"))
        .role("group")
        .label(title.to_owned())
        .w_full()
        .gap(S4)
        .items_center()
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(space::S1)
                .child(
                    text(title.to_owned())
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .children(copy.map(|c| c.key(format!("settings.analytics.machine.{key}.copy")))),
        )
        .children(control)
}

fn copy(said: String, t: &Tokens) -> El {
    text(said).font(CAPTION).color(t.text_tertiary).wrap()
}

fn warning(said: &str, t: &Tokens) -> El {
    text(said.to_owned()).font(CAPTION).color(t.warning).wrap()
}

fn confirming(yes: &str, on_yes: fn(&mut State, &mut Tx), k: &Kit) -> El {
    row()
        .gap(space::S2)
        .child(
            Button::new(yes)
                .variant(ButtonVariant::Danger)
                .on_press_tx(on_yes)
                .build(k)
                .key("settings.analytics.confirm.yes"),
        )
        .child(
            Button::new("Keep")
                .on_press(|state: &mut State| state.window.settings.analytics.confirm = None)
                .build(k)
                .key("settings.analytics.confirm.keep"),
        )
}

fn asking(label: &str, confirm: Confirm, k: &Kit) -> El {
    Button::new(label)
        .variant(ButtonVariant::Danger)
        .on_press(move |state: &mut State| state.window.settings.analytics.confirm = Some(confirm))
        .build(k)
}

fn setting_switch(state: &State, key: &'static str, label: &str, t: &Tokens) -> El {
    let on = crate::setting::on(&state.facts.settings, key);
    switch(
        format!("settings.analytics.switch.{key}"),
        label,
        on,
        false,
        t,
        move |state, tx| super::set(state, key, !on, tx),
    )
}

fn machine(state: &State, page: &Analytics, sharing: bool, t: &Tokens, k: &Kit) -> El {
    let status = page.status.as_ref();
    let send_vents = crate::setting::on(&state.facts.settings, SEND_VENTS);
    let tool_vents = crate::setting::on(&state.facts.settings, TOOL_VENTS);
    let offering_delete = page.confirm == Some(Confirm::DeleteServerCopy);
    let share_copy = if offering_delete {
        Some(copy(
            "Sharing is off. Delete what the server already holds?".into(),
            t,
        ))
    } else if let Some(reason) = status.and_then(|s| s.ingest_off_reason.as_deref()) {
        Some(warning(reason, t))
    } else if let Some(refusal) = status
        .filter(|_| sharing)
        .and_then(|s| s.last_refusal.as_deref())
    {
        Some(warning(refusal, t))
    } else {
        status.map(|s| {
            copy(
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
                t,
            )
        })
    };
    let share_control = if offering_delete {
        confirming("Delete from server", super::forget, k)
    } else {
        switch(
            "settings.analytics.switch.sharing".into(),
            "Share with attn's maintainers",
            sharing,
            false,
            t,
            super::toggle_sharing,
        )
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
        confirming("Delete from server", super::forget, k)
    } else {
        asking("Forget…", Confirm::Forget, k).key("settings.analytics.forget")
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
        confirming("Clear", super::clear, k)
    } else {
        asking("Clear…", Confirm::Clear, k).key("settings.analytics.clear")
    };
    col()
        .key("settings.analytics.machine")
        .role("group")
        .label("Machine settings")
        .w_full()
        .gap(space::S2)
        .child(machine_row(
            "share",
            "Share with attn's maintainers",
            share_copy,
            Some(share_control),
            t,
        ))
        .children((sharing || send_vents).then(|| {
            machine_row(
                "send-vents",
                "Send vents",
                Some(copy(
                    if send_vents {
                        "New attn vents are sent with their text."
                    } else {
                        "Vents stay here unless you send one."
                    }
                    .into(),
                    t,
                )),
                Some(setting_switch(state, SEND_VENTS, "Send vents", t)),
                t,
            )
        }))
        .child(machine_row(
            "tool-vents",
            "Tool vents",
            Some(copy(
                if tool_vents {
                    "New sessions also vent about their other tools: skills, other CLIs, harness permissions. These never leave this machine."
                } else {
                    "Agents vent only about attn. Turn on to also hear about their other tools (skills, other CLIs, harness permissions), kept on this machine."
                }
                .into(),
                t,
            )),
            Some(setting_switch(state, TOOL_VENTS, "Tool vents", t)),
            t,
        ))
        .child(machine_row("kept", "Kept for 7 days", Some(copy(kept, t)), None, t))
        .child(machine_row(
            "forget",
            "Forget this install",
            Some(copy(
                "Deletes this install's data on the server and starts a new install id.".into(),
                t,
            )),
            Some(forget),
            t,
        ))
        .child(machine_row(
            "clear",
            "Clear the local record",
            Some(copy(clear_copy, t)),
            Some(clear),
            t,
        ))
}

fn hints(view: usize, sharing: bool, t: &Tokens, k: &Kit) -> El {
    let mut keys: Vec<(&[&str], &str)> = vec![(&["j", "k"], "move"), (&["[", "]"], "switch list")];
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
    row()
        .key("settings.analytics.keys")
        .role("group")
        .label("Keys")
        .gap(space::S3)
        .flex_wrap()
        .children(keys.into_iter().map(|(keys, label)| {
            row()
                .gap(space::S1)
                .items_center()
                .child(Keybind::new(keys).build(k))
                .child(text(label).font(CAPTION).color(t.text_tertiary))
        }))
}
