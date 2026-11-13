//! Settings → Task runner: the daemon's durable background tasks, read when the page shows and
//! again each time the daemon says they changed, with Retry for a task that failed.

use std::collections::HashSet;

use attn_protocol::{Task, TaskListMessage, TaskListResultMessage, TaskRetryMessage};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};
use rune_kit::{Badge, BadgeVariant, Button, Collapsible};

use super::Section;
use super::pages::CAPTION;
use crate::State;
use crate::requests::Answer;

#[derive(Default)]
pub(crate) struct Tasks {
    list: Vec<Task>,
    error: Option<String>,
    loading: bool,
    /// Rises with each read, so only the newest read's answer lands.
    epoch: u64,
    /// The `Facts::tasks_changed` the last read followed.
    seen: u64,
    retrying: HashSet<String>,
    diagnostics_shown: HashSet<String>,
}

fn shown(state: &State) -> bool {
    super::is_open(state) && state.window.settings.section == Section::BackgroundTasks
}

/// Reads the tasks when the page has just come into view.
pub(super) fn arrive(state: &mut State, tx: &mut Tx) {
    if shown(state) {
        read(state, tx);
    }
}

/// Reads the tasks again when the daemon says they changed while the page shows.
pub(super) fn follow(state: &mut State, tx: &mut Tx) {
    if shown(state) && state.window.settings.tasks.seen != state.facts.tasks_changed {
        read(state, tx);
    }
}

fn read(state: &mut State, tx: &mut Tx) {
    let tasks = &mut state.window.settings.tasks;
    tasks.epoch += 1;
    tasks.loading = true;
    tasks.seen = state.facts.tasks_changed;
    let epoch = tasks.epoch;
    state.ask(
        |request_id| TaskListMessage {
            request_id: Some(request_id),
            ..TaskListMessage::new()
        },
        move |state, answer, _tx: &mut Tx| {
            let tasks = &mut state.window.settings.tasks;
            if tasks.epoch != epoch {
                return;
            }
            tasks.loading = false;
            match answer.outcome() {
                Ok(reply) => {
                    tasks.list = reply
                        .parse::<TaskListResultMessage>()
                        .and_then(|r| r.tasks)
                        .unwrap_or_default();
                    tasks.error = None;
                }
                Err(why) => tasks.error = Some(why),
            }
        },
        tx,
    );
}

fn retry(state: &mut State, id: String, tx: &mut Tx) {
    state.window.settings.tasks.retrying.insert(id.clone());
    let task_id = id.clone();
    state.ask(
        |request_id| TaskRetryMessage {
            request_id: Some(request_id),
            task_id,
            ..TaskRetryMessage::new()
        },
        move |state, _answer: Answer, _tx: &mut Tx| {
            state.window.settings.tasks.retrying.remove(&id);
        },
        tx,
    );
}

pub(super) fn page(state: &State, now_ms: i64, t: &Tokens) -> Vec<El> {
    let k = attn_ui::kit(t);
    let tasks = &state.window.settings.tasks;
    if let Some(error) = &tasks.error {
        return vec![
            col()
                .key("settings.tasks.error")
                .role("alert")
                .label(error.clone())
                .gap(space::S2)
                .child(text(error.clone()).font(CAPTION).color(t.text_tertiary))
                .child(
                    Button::new("Try again")
                        .on_press_tx(read)
                        .build(k)
                        .key("settings.tasks.try-again"),
                ),
        ];
    }
    if tasks.list.is_empty() {
        let said = if tasks.loading {
            "Loading tasks…"
        } else {
            "No background tasks."
        };
        return vec![
            text(said)
                .key("settings.tasks.empty")
                .font(CAPTION)
                .color(t.text_tertiary),
        ];
    }
    vec![
        col()
            .key("settings.tasks")
            .role("list")
            .label("Background tasks")
            .w_full()
            .gap(space::S2)
            .children(
                tasks
                    .list
                    .iter()
                    .map(|task| row_of(tasks, task, now_ms, t, k)),
            ),
    ]
}

fn row_of(tasks: &Tasks, task: &Task, now_ms: i64, t: &Tokens, k: &rune_kit::Kit) -> El {
    let id = task.id.clone();
    let subject = format!("{}:{}", task.kind, task.subject);
    let badge = Badge::new(task.state.clone())
        .variant(match task.state.as_str() {
            "running" => BadgeVariant::Accent,
            "queued" => BadgeVariant::Subtle,
            _ => BadgeVariant::Neutral,
        })
        .build(k);
    let retrying = tasks.retrying.contains(&task.id);
    let can_retry = matches!(task.state.as_str(), "failed" | "dead");
    let head = row()
        .w_full()
        .gap(space::S2)
        .items_center()
        .child(badge)
        .child(
            text(subject.clone())
                .font(type_::MONO_CAPTION)
                .color(t.text_primary)
                .truncate()
                .grow(1.0)
                .min_w(0.0),
        )
        .children(can_retry.then(|| {
            let id = id.clone();
            Button::new(if retrying { "Retrying…" } else { "Retry" })
                .disabled(retrying)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| retry(state, id.clone(), tx))
                .build(k)
                .key(format!("settings.task.{}.retry", task.id))
                .label("Retry")
        }));
    let next = (!matches!(task.state.as_str(), "done" | "dead"))
        .then(|| next_attempt(&task.next_attempt_at, now_ms))
        .flatten();
    let meta = row()
        .gap(space::S3)
        .child(
            text(format!("attempts: {}", task.attempts))
                .font(CAPTION)
                .color(t.text_tertiary),
        )
        .children(next.map(|next| {
            text(format!("next: {next}"))
                .font(CAPTION)
                .color(t.text_tertiary)
        }));
    let error = task
        .last_error
        .as_deref()
        .filter(|e| !e.is_empty())
        .map(|e| {
            text(e.to_owned())
                .key(format!("settings.task.{}.error", task.id))
                .font(CAPTION)
                .color(t.error)
                .wrap()
        });
    let diagnostic = task
        .last_diagnostic
        .as_deref()
        .filter(|d| !d.is_empty())
        .map(|d| {
            let open = tasks.diagnostics_shown.contains(&task.id);
            let id = id.clone();
            Collapsible::new("Diagnostic output", open)
                .ghost()
                .child(
                    text(d.to_owned())
                        .key(format!("settings.task.{}.diagnostic", task.id))
                        .font(type_::MONO_CAPTION)
                        .color(t.text_secondary)
                        .wrap(),
                )
                .on_toggle(move |state: &mut State| {
                    let shown = &mut state.window.settings.tasks.diagnostics_shown;
                    if !shown.remove(&id) {
                        shown.insert(id.clone());
                    }
                })
                .build(k)
                .key(format!("settings.task.{}.diagnostics", task.id))
        });
    col()
        .key(format!("settings.task.{}", task.id))
        .role("listitem")
        .label(subject)
        .w_full()
        .gap(space::S1)
        .padding(space::S3)
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(head)
        .child(meta)
        .children(error)
        .children(diagnostic)
}

/// When a task next runs, said as the Tauri app says it: "now" within five seconds, else "in 3m"
/// or "40s ago"; nothing for a time that does not parse or is the zero time.
fn next_attempt(at: &str, now_ms: i64) -> Option<String> {
    let at_ms = crate::sidebar::queue::epoch_ms(at)?;
    let year: i64 = at.split('-').next()?.parse().ok()?;
    if year <= 1 {
        return None;
    }
    let delta = ((at_ms - now_ms) as f64 / 1000.0).round() as i64;
    let abs = delta.abs();
    if abs < 5 {
        return Some("now".into());
    }
    let unit = if abs < 60 {
        format!("{abs}s")
    } else if abs < 3600 {
        format!("{}m", (abs as f64 / 60.0).round() as i64)
    } else {
        format!("{}h", (abs as f64 / 3600.0).round() as i64)
    };
    Some(if delta >= 0 {
        format!("in {unit}")
    } else {
        format!("{unit} ago")
    })
}
