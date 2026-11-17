//! The automations drawer: each definition with what triggers it, its switch, Run now and Edit,
//! what went wrong with its latest run or guard check, and the runs of the one selected, each
//! opening its session. The form in `form` creates and edits a definition in the drawer's place.

mod drawer;
pub(crate) mod form;
pub(crate) mod runs;
pub(crate) mod schedule;

use std::collections::{HashMap, HashSet};

use attn_protocol::{
    AutomationDefinitionSummary, AutomationDefinitionsGetMessage,
    AutomationDefinitionsResultMessage, AutomationRunFix, AutomationRunMessage,
    AutomationRunSummary, AutomationRunsGetMessage, AutomationRunsResultMessage,
    AutomationSetEnabledMessage,
};
use rune::{TextEdit, Tx};

use crate::State;
use crate::requests::Answer;
use crate::shell::Drawer;

pub(crate) use drawer::drawer;

pub(crate) const DRAWER: &str = "automations";
/// How long a Run now waits for the daemon before it says the run is still in flight, as the
/// Tauri app's automation requests do.
const RUN_ANSWER_SECONDS: f64 = 30.0;
pub(crate) const STILL_IN_FLIGHT: &str = "Run request is still in flight — it will appear in run history; clicking again retries the same run.";
const MANUAL: &str = "manual:";

/// What the drawer holds while the window lives, open or not. Everything but the form belongs
/// to `profile`, and goes when the window shows another.
#[derive(Default)]
pub struct Automations {
    profile: String,
    /// The identity of the drawer's time on `profile`: a reply asked for in another one
    /// changes nothing.
    tenure: u64,
    /// The last identity handed out, to a tenure, a Run now or a form's opening or read.
    issued: u64,
    pub(crate) definitions: Vec<AutomationDefinitionSummary>,
    /// The daemon answered a list since the drawer opened.
    pub(crate) loaded: bool,
    pub(crate) error: Option<String>,
    /// Wall-clock milliseconds when the latest list arrived, which guard checks are aged from:
    /// they read again when the list does, not as time passes.
    pub(crate) listed_ms: i64,
    /// The `automations_changed` count the drawer last listed at.
    seen_changes: u64,
    pub(crate) selected: Option<i64>,
    /// Runs by definition, newest first; a definition with no entry has not been read yet.
    pub(crate) runs: HashMap<i64, Vec<AutomationRunSummary>>,
    pub(crate) runs_error: Option<String>,
    pub(crate) toggling: HashSet<i64>,
    pub(crate) toggle_errors: HashMap<i64, String>,
    /// The Run now waiting for the daemon, by definition.
    pub(crate) running: HashMap<i64, Running>,
    pub(crate) run_errors: HashMap<i64, String>,
    /// The request id of each definition's manual run not yet known delivered or failed. The
    /// daemon keeps it as the run's dedup key, so a retry of the same click must reuse it.
    pub(crate) run_keys: HashMap<i64, String>,
    pub(crate) pull_request: Option<PullRequestPrompt>,
    pub(crate) form: Option<form::Form>,
}

impl Automations {
    pub(crate) fn issue(&mut self) -> u64 {
        self.issued += 1;
        self.issued
    }
}

pub(crate) struct Running {
    attempt: u64,
    until: f64,
}

/// Run now's question for an automation that works in a pull request's worktree.
pub(crate) struct PullRequestPrompt {
    pub(crate) definition: i64,
    pub(crate) url: TextEdit,
    pub(crate) missing: bool,
}

pub(crate) fn is_open(state: &State) -> bool {
    state.window.shows(Drawer::Automations)
}

pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if is_open(state) {
        close(state, tx);
    } else {
        open(state, tx);
    }
}

pub(crate) fn open(state: &mut State, tx: &mut Tx) {
    hold_selected_profile(state);
    state.window.open_drawer(Drawer::Automations);
    let automations = &mut state.window.automations;
    automations.loaded = false;
    automations.seen_changes = state.facts.automations_changed;
    list(state, tx);
    if let Some(id) = state.window.automations.selected {
        read_runs(state, id, tx);
    }
    tx.focus(DRAWER, false);
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    state.window.close_drawer(Drawer::Automations);
    let automations = &mut state.window.automations;
    automations.form = None;
    automations.pull_request = None;
    tx.blur();
}

/// Lists the definitions again, and the selected one's runs, when the daemon says they changed
/// or the window shows another profile while the drawer is open; the open form keeps what is
/// typed in it.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let switched = hold_selected_profile(state);
    let changes = state.facts.automations_changed;
    if !is_open(state) || (!switched && state.window.automations.seen_changes == changes) {
        return;
    }
    state.window.automations.seen_changes = changes;
    list(state, tx);
    if let Some(id) = state.window.automations.selected {
        read_runs(state, id, tx);
    }
}

/// Starts the drawer over on the profile the window shows, when it held another. Returns whether
/// it did.
fn hold_selected_profile(state: &mut State) -> bool {
    let selected = &state.facts.profiles.selected;
    let automations = &mut state.window.automations;
    if &automations.profile == selected {
        return false;
    }
    let mut fresh = Automations {
        profile: selected.clone(),
        issued: automations.issued,
        form: automations.form.take(),
        ..Automations::default()
    };
    fresh.tenure = fresh.issue();
    *automations = fresh;
    true
}

/// Sends a request of the drawer's, whose answer `then` takes only while the drawer is still on
/// the profile it was sent for.
fn ask_in_tenure(
    state: &mut State,
    id: String,
    command: &impl serde::Serialize,
    then: impl FnOnce(&mut State, Answer, &mut Tx) + 'static,
    tx: &mut Tx,
) {
    let tenure = state.window.automations.tenure;
    state.ask_as(
        id,
        command,
        move |state, answer, tx| {
            if state.window.automations.tenure == tenure {
                then(state, answer, tx);
            }
        },
        tx,
    );
}

/// The list goes out under a `request_id` that carries its command, as the Tauri app's does, so
/// the daemon's log names what asked.
fn list(state: &mut State, tx: &mut Tx) {
    let id = format!("automation_definitions_get:{}", state.next_request_id());
    let message = AutomationDefinitionsGetMessage {
        request_id: Some(id.clone()),
        ..AutomationDefinitionsGetMessage::new()
    };
    ask_in_tenure(
        state,
        id,
        &message,
        |state, answer, tx| {
            let now_ms = state.wall_ms(tx);
            let automations = &mut state.window.automations;
            automations.loaded = true;
            match answer.outcome() {
                Ok(reply) => {
                    let listed: Option<AutomationDefinitionsResultMessage> = reply.parse();
                    automations.definitions =
                        listed.and_then(|l| l.definitions).unwrap_or_default();
                    automations.error = None;
                    automations.listed_ms = now_ms;
                    let latest: Vec<(i64, Vec<AutomationRunSummary>)> = state
                        .window
                        .automations
                        .definitions
                        .iter()
                        .map(|d| (d.id, d.last_run.iter().cloned().collect()))
                        .collect();
                    for (id, runs) in latest {
                        reconcile_run_key(&mut state.window.automations, id, &runs);
                    }
                }
                Err(reason) => automations.error = Some(reason),
            }
        },
        tx,
    );
}

fn read_runs(state: &mut State, definition: i64, tx: &mut Tx) {
    let id = state.next_request_id();
    ask_in_tenure(
        state,
        id.clone(),
        &AutomationRunsGetMessage {
            request_id: Some(id),
            definition_id: definition,
            ..AutomationRunsGetMessage::new()
        },
        move |state, answer, _| {
            let automations = &mut state.window.automations;
            match answer.outcome() {
                Ok(reply) => {
                    let read: Option<AutomationRunsResultMessage> = reply.parse();
                    let runs = read.and_then(|r| r.runs).unwrap_or_default();
                    reconcile_run_key(automations, definition, &runs);
                    automations.runs.insert(definition, runs);
                    automations.runs_error = None;
                }
                Err(reason) => automations.runs_error = Some(reason),
            }
        },
        tx,
    );
}

/// Forgets a manual run's key once history shows that run delivered or failed; with no key,
/// takes the newest manual run history shows pending, so a click after a relaunch retries it.
fn reconcile_run_key(
    automations: &mut Automations,
    definition: i64,
    runs: &[AutomationRunSummary],
) {
    if let Some(key) = automations.run_keys.get(&definition) {
        let occurrence = format!("{MANUAL}{key}");
        let finished = runs.iter().any(|run| {
            run.occurrence_key.as_deref() == Some(occurrence.as_str())
                && matches!(run.state.as_str(), "delivered" | "failed")
        });
        if finished {
            automations.run_keys.remove(&definition);
        }
        return;
    }
    let pending = runs.iter().find_map(|run| {
        (run.state == "pending")
            .then(|| run.occurrence_key.as_deref()?.strip_prefix(MANUAL))
            .flatten()
    });
    if let Some(key) = pending {
        automations.run_keys.insert(definition, key.to_owned());
    }
}

pub(crate) fn select(state: &mut State, definition: i64, tx: &mut Tx) {
    let automations = &mut state.window.automations;
    if automations.selected == Some(definition) {
        automations.selected = None;
        return;
    }
    automations.selected = Some(definition);
    read_runs(state, definition, tx);
}

pub(crate) fn unselect(state: &mut State) {
    state.window.automations.selected = None;
}

pub(crate) fn set_enabled(state: &mut State, definition: i64, enabled: bool, tx: &mut Tx) {
    let automations = &mut state.window.automations;
    if !automations.toggling.insert(definition) {
        return;
    }
    automations.toggle_errors.remove(&definition);
    let id = state.next_request_id();
    ask_in_tenure(
        state,
        id.clone(),
        &AutomationSetEnabledMessage {
            request_id: Some(id),
            definition_id: definition,
            enabled,
            ..AutomationSetEnabledMessage::new()
        },
        move |state, answer, _| {
            let automations = &mut state.window.automations;
            automations.toggling.remove(&definition);
            match answer.outcome() {
                Ok(_) => {
                    if let Some(d) = automations
                        .definitions
                        .iter_mut()
                        .find(|d| d.id == definition)
                    {
                        d.enabled = enabled;
                    }
                }
                Err(reason) => {
                    automations.toggle_errors.insert(definition, reason);
                }
            }
        },
        tx,
    );
}

/// Run now: an automation that works in a pull request's worktree asks for the pull request
/// first; any other starts at once.
pub(crate) fn run_now(state: &mut State, definition: i64, tx: &mut Tx) {
    let worktree =
        state.window.automations.definitions.iter().any(|d| {
            d.id == definition && d.location_type.as_deref() == Some("repository_worktree")
        });
    if worktree {
        state.window.automations.pull_request = Some(PullRequestPrompt {
            definition,
            url: TextEdit::default(),
            missing: false,
        });
        tx.focus(drawer::pull_request_field(definition), false);
    } else {
        run(state, definition, None, tx);
    }
}

pub(crate) fn run_pull_request(state: &mut State, tx: &mut Tx) {
    let Some(prompt) = state.window.automations.pull_request.as_mut() else {
        return;
    };
    let url = prompt.url.text.trim().to_owned();
    if url.is_empty() {
        prompt.missing = true;
        return;
    }
    let definition = prompt.definition;
    run(state, definition, Some(url), tx);
}

pub(crate) fn cancel_pull_request(state: &mut State) {
    state.window.automations.pull_request = None;
}

fn run(state: &mut State, definition: i64, pr_url: Option<String>, tx: &mut Tx) {
    let automations = &mut state.window.automations;
    if automations.running.contains_key(&definition) {
        return;
    }
    let key = automations
        .run_keys
        .entry(definition)
        .or_insert_with(|| uuid::Uuid::new_v4().to_string())
        .clone();
    let attempt = automations.issue();
    let until = tx.clock() + RUN_ANSWER_SECONDS;
    automations
        .running
        .insert(definition, Running { attempt, until });
    automations.run_errors.remove(&definition);
    tx.wake_at(until);
    let message = AutomationRunMessage {
        request_id: key.clone(),
        definition_id: definition,
        pr_url,
        ..AutomationRunMessage::new()
    };
    ask_in_tenure(
        state,
        key,
        &message,
        move |state, answer: Answer, _| {
            let automations = &mut state.window.automations;
            if automations.running.get(&definition).map(|r| r.attempt) != Some(attempt) {
                return;
            }
            automations.running.remove(&definition);
            automations.run_keys.remove(&definition);
            match answer.outcome() {
                Ok(_) => {
                    if automations
                        .pull_request
                        .as_ref()
                        .is_some_and(|p| p.definition == definition)
                    {
                        automations.pull_request = None;
                    }
                }
                Err(reason) => {
                    automations.run_errors.insert(definition, reason);
                }
            }
        },
        tx,
    );
}

/// Opens the harness the failed run names at the folder it names, so the user can trust it.
pub(crate) fn fix(state: &mut State, fix: AutomationRunFix, tx: &mut Tx) {
    crate::picker::spawn(
        state,
        crate::picker::Spawn {
            path: fix.directory,
            agent: fix.agent,
            yolo: false,
            desktop: None,
            anchor: None,
            direction: crate::picker::Direction::Vertical,
            label: None,
            host: None,
        },
        tx,
    );
}

/// Shows the session a run started, as a click on its row in the sidebar does.
pub(crate) fn open_run(state: &mut State, session: &str, tx: &mut Tx) {
    crate::sidebar::open_agent(state, session, tx);
}

pub(crate) fn new_form(state: &mut State, tx: &mut Tx) {
    form::open(state, None, tx);
}

pub(crate) fn edit(state: &mut State, definition: i64, tx: &mut Tx) {
    form::open(state, Some(definition), tx);
}

/// Says a Run now is still in flight once the daemon has kept it waiting too long; a click
/// after that retries the same run.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    let automations = &mut state.window.automations;
    let late: Vec<i64> = automations
        .running
        .iter()
        .filter(|(_, r)| now >= r.until)
        .map(|(id, _)| *id)
        .collect();
    for id in &late {
        automations.running.remove(id);
        automations
            .run_errors
            .insert(*id, STILL_IN_FLIGHT.to_owned());
    }
    let followed = form::follow_focus(state, tx);
    !late.is_empty() || followed
}

pub(crate) fn trigger_label(definition: &AutomationDefinitionSummary) -> String {
    match definition.trigger_type.as_str() {
        "manual" => "Manual".into(),
        "github_review_requested" => "GitHub".into(),
        "scheduled" => match (&definition.schedule_cron, &definition.schedule_time_zone) {
            (Some(cron), Some(zone)) if !cron.is_empty() && !zone.is_empty() => {
                format!("Scheduled — {cron} ({zone})")
            }
            (Some(cron), _) if !cron.is_empty() => format!("Scheduled — {cron}"),
            _ => "Scheduled".into(),
        },
        other => other.to_owned(),
    }
}

/// The slice the automation server reads as `automations`: whether the drawer shows, its
/// definitions and the selected one's runs, and the form's state when it shows.
pub(crate) fn automation_state(state: &State) -> serde_json::Value {
    let automations = &state.window.automations;
    serde_json::json!({
        "present": is_open(state),
        "loaded": automations.loaded,
        "error": automations.error,
        "definitions": automations.definitions.iter().map(|d| serde_json::json!({
            "id": d.id,
            "name": d.name,
            "enabled": d.enabled,
            "trigger": trigger_label(d),
            "canRunNow": !automations.running.contains_key(&d.id),
            "lastRun": d.last_run,
            "guardCheck": d.guard_check,
            "launchDesktop": d.launch_desktop.as_ref().and_then(|l| l.label.clone()),
        })).collect::<Vec<_>>(),
        "selected": automations.selected,
        "runs": automations.selected.and_then(|id| automations.runs.get(&id)),
        "runErrors": automations.run_errors,
        "toggleErrors": automations.toggle_errors,
        "form": automations.form.as_ref().map(|f| f.automation_state()),
    })
}
