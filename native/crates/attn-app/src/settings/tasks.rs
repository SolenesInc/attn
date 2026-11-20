//! Settings → Task runner: the daemon's durable background tasks, read when the page shows and
//! again each time the daemon says they changed, with Retry for a task that failed.

use std::collections::HashSet;

use attn_protocol::{Task, TaskListMessage, TaskListResultMessage, TaskRetryMessage};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Disclosure, Inline, List, Notice, Prose, Status};
use attn_ui::system::{Button, ListRow, Reveal, Size, Tone, Type, tag, words};
use rune::{El, Tx};

use super::Section;
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

pub(super) fn page(state: &State, now_ms: i64, t: &Tokens) -> Vec<Block> {
    let tasks = &state.window.settings.tasks;
    if let Some(error) = &tasks.error {
        return vec![
            Notice::error("settings.tasks.error", error.clone())
                .action(
                    Button::new("Try again")
                        .on_press_tx(read)
                        .build(t)
                        .key("settings.tasks.try-again"),
                )
                .build(t),
        ];
    }
    if tasks.list.is_empty() {
        return vec![if tasks.loading {
            Status::new("settings.tasks.empty", "Loading tasks…", true).build(t)
        } else {
            Prose::empty("settings.tasks.empty", "No background tasks.").build(t)
        }];
    }
    vec![
        tasks
            .list
            .iter()
            .map(|task| row_of(tasks, task, now_ms, t))
            .fold(
                List::new("settings.tasks", "Background tasks"),
                |list, (row, under)| list.row_with(row, under),
            )
            .build(t),
    ]
}

fn tone_of(state: &str) -> Tone {
    match state {
        "running" => Tone::Good,
        "failed" | "dead" => Tone::Bad,
        _ => Tone::Neutral,
    }
}

/// A task's row, with what went wrong and its diagnostic output under it.
fn row_of(tasks: &Tasks, task: &Task, now_ms: i64, t: &Tokens) -> (El, Vec<Block>) {
    let key = format!("settings.task.{}", task.id);
    let subject = format!("{}:{}", task.kind, task.subject);
    let retrying = tasks.retrying.contains(&task.id);
    let can_retry = matches!(task.state.as_str(), "failed" | "dead");
    let next = (!matches!(task.state.as_str(), "done" | "dead"))
        .then(|| next_attempt(&task.next_attempt_at, now_ms))
        .flatten();
    let tries = [
        Some(format!("attempts: {}", task.attempts)),
        next.map(|next| format!("next: {next}")),
    ]
    .into_iter()
    .flatten()
    .collect::<Vec<_>>()
    .join(" · ");
    let id = task.id.clone();
    let row = ListRow::new(key.clone(), subject.clone())
        .still()
        .wrap()
        .title_el(
            words(subject, Type::Mono, t.text_primary)
                .wrap_anywhere()
                .w_full()
                .min_w(0.0),
        )
        .subtitle_el(
            Inline::tight()
                .child(El::from(tag(
                    format!("{key}.state"),
                    &task.state,
                    tone_of(&task.state),
                    t,
                )))
                .child(words(tries, Type::Small, t.text_tertiary).wrap()),
        )
        .controls(
            can_retry.then(|| {
                Button::new(if retrying { "Retrying…" } else { "Retry" })
                    .size(Size::Compact)
                    .disabled(retrying)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| retry(state, id.clone(), tx))
                    .build(t)
                    .key(format!("{key}.retry"))
                    .label("Retry")
                    .into()
            }),
            Reveal::Always,
        )
        .build(t);
    let error = task
        .last_error
        .as_deref()
        .filter(|e| !e.is_empty())
        .map(|e| Notice::error(format!("{key}.error"), e.to_owned()).build(t));
    let diagnostic = task
        .last_diagnostic
        .as_deref()
        .filter(|d| !d.is_empty())
        .map(|d| {
            let id = task.id.clone();
            Disclosure::new(
                format!("{key}.diagnostics"),
                "Diagnostic output",
                tasks.diagnostics_shown.contains(&task.id),
                move |state: &mut State, tx: &mut Tx| {
                    tx.instant();
                    let shown = &mut state.window.settings.tasks.diagnostics_shown;
                    if !shown.remove(&id) {
                        shown.insert(id.clone());
                    }
                },
            )
            .push(Block::custom(
                format!("{key}.diagnostic.block"),
                words(d.to_owned(), Type::Mono, t.text_secondary)
                    .key(format!("{key}.diagnostic"))
                    .label(d.to_owned())
                    .wrap_anywhere(),
            ))
            .build(t)
        });
    (row, error.into_iter().chain(diagnostic).collect())
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
