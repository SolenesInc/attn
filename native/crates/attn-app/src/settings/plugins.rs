//! Settings → Plugins: install a plugin from a folder or a Git repository, remove one, and set
//! each one's provider priority. The list is read when the dialog opens and after each change,
//! and follows every list the daemon pushes.

use std::collections::BTreeMap;

use attn_client::{LIST_PLUGINS, plugin_action};
use attn_protocol::{
    InstallPluginMessage, ListPluginsMessage, PluginInfo, PluginsUpdatedMessage,
    RemovePluginMessage, SetPluginPriorityMessage,
};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Facts, Field, Inline, List, Notice, Prose, Status};
use rune::{El, FileDialogOptions, TextEdit, Tx};
use rune_kit::TextInput;

use attn_ui::system::{
    Button, FieldState, ListRow, Look, Reveal, Said, Size, Tone, Type, line_box, number_box, tag,
    words,
};

use crate::State;
use crate::requests::Answer;

const SAVED_MARK_SECONDS: f64 = 1.6;
const INSTALL: &str = "install";
const PRIORITY_PREFIX: &str = "settings.plugin.";
const PRIORITY_SUFFIX: &str = ".priority";

#[derive(Default)]
pub(crate) struct Plugins {
    source: TextEdit,
    /// Each plugin's priority as typed, seeded from each list the page reads.
    priorities: BTreeMap<String, TextEdit>,
    loading: bool,
    error: Option<String>,
    /// What is mid-flight: an install, or the plugin a removal or priority change is for.
    busy: Option<String>,
    /// The change that finishes once the list it read again lands, and whose priority it set.
    finishing: Option<Option<String>>,
    /// Each plugin whose priority just saved, and until when its Saved mark shows.
    saved: BTreeMap<String, f64>,
}

fn panel(state: &mut State) -> &mut Plugins {
    &mut state.window.settings.plugins
}

/// Clears the source and reads the list, as the Tauri dialog does each time it opens.
pub(super) fn open(state: &mut State, tx: &mut Tx) {
    panel(state).source = TextEdit::default();
    refresh(state, tx);
}

fn refresh(state: &mut State, tx: &mut Tx) {
    let plugins = panel(state);
    plugins.loading = true;
    plugins.error = None;
    if state
        .requests
        .waits_for(|command| command["cmd"] == LIST_PLUGINS)
    {
        return;
    }
    state.ask_as(LIST_PLUGINS.into(), &ListPluginsMessage::new(), listed, tx);
}

fn listed(state: &mut State, answer: Answer, tx: &mut Tx) {
    let list = match &answer {
        Answer::Reply(reply) if reply.event == "plugins_updated" => {
            reply.parse::<PluginsUpdatedMessage>()
        }
        _ => None,
    };
    let now = tx.clock();
    let plugins = panel(state);
    plugins.loading = false;
    match list {
        Some(list) => {
            plugins.priorities = list
                .plugins
                .iter()
                .map(|p| (p.name.clone(), TextEdit::new(p.priority.to_string())))
                .collect();
        }
        None => {
            plugins.error = Some(
                answer
                    .outcome()
                    .err()
                    .unwrap_or_else(|| "Failed to load plugins".into()),
            );
        }
    }
    if let Some(saved) = plugins.finishing.take() {
        plugins.busy = None;
        if let Some(name) = saved {
            plugins.saved.insert(name, now + SAVED_MARK_SECONDS);
            tx.wake_at(now + SAVED_MARK_SECONDS);
        }
    }
}

/// Sends a plugin command for `busy`; once the daemon did it, reads the list again and
/// finishes when that lands. A refusal shows above the list.
fn run<M: serde::Serialize>(
    state: &mut State,
    busy: String,
    action: &str,
    command: M,
    saved: Option<String>,
    tx: &mut Tx,
) {
    let plugins = panel(state);
    plugins.error = None;
    plugins.busy = Some(busy);
    let installing = action == INSTALL;
    state.ask_as(
        plugin_action(action),
        &command,
        move |state, answer, tx| match answer.outcome() {
            Ok(_) => {
                let plugins = panel(state);
                if installing {
                    plugins.source = TextEdit::default();
                }
                plugins.finishing = Some(saved);
                refresh(state, tx);
            }
            Err(why) => {
                let plugins = panel(state);
                plugins.busy = None;
                plugins.error = Some(why);
            }
        },
        tx,
    );
}

fn install(state: &mut State, tx: &mut Tx) {
    let source = panel(state).source.text.trim().to_owned();
    if source.is_empty() {
        panel(state).error = Some("Plugin source is required".into());
        return;
    }
    let command = InstallPluginMessage {
        source,
        ..InstallPluginMessage::new()
    };
    run(state, INSTALL.into(), INSTALL, command, None, tx);
}

fn remove(state: &mut State, name: &str, tx: &mut Tx) {
    let command = RemovePluginMessage {
        name: name.to_owned(),
        ..RemovePluginMessage::new()
    };
    run(state, name.to_owned(), "remove", command, None, tx);
}

/// A priority as JavaScript's `Number` reads the typed text, so an empty field is 0.
fn parse_priority(raw: &str) -> Option<i64> {
    if raw.is_empty() {
        return Some(0);
    }
    raw.parse::<f64>()
        .ok()
        .filter(|n| n.is_finite() && n.fract() == 0.0)
        .map(|n| n as i64)
}

fn commit_priority(state: &mut State, name: &str, tx: &mut Tx) {
    let Some(current) = state
        .facts
        .plugins
        .plugins
        .iter()
        .find(|p| p.name == name)
        .map(|p| p.priority)
    else {
        return;
    };
    let plugins = panel(state);
    let raw = plugins
        .priorities
        .get(name)
        .map_or_else(|| current.to_string(), |d| d.text.trim().to_owned());
    if raw == current.to_string() {
        return;
    }
    let Some(priority) = parse_priority(&raw) else {
        plugins.error = Some("Plugin priority must be an integer".into());
        return;
    };
    let command = SetPluginPriorityMessage {
        name: name.to_owned(),
        priority,
        ..SetPluginPriorityMessage::new()
    };
    run(
        state,
        name.to_owned(),
        "set_priority",
        command,
        Some(name.to_owned()),
        tx,
    );
}

fn browse(tx: &mut Tx) {
    let options = FileDialogOptions {
        title: "Select Plugin Directory".into(),
        ..FileDialogOptions::default()
    };
    tx.open_folder(options, |state: &mut State, chosen, tx| match chosen {
        Ok(Some(folder)) => panel(state).source = TextEdit::new(folder.to_string_lossy()),
        Ok(None) => {}
        Err(why) => state
            .window
            .notify(format!("Could not ask for a folder: {why}"), tx),
    });
}

fn priority_key(name: &str) -> String {
    format!("{PRIORITY_PREFIX}{name}{PRIORITY_SUFFIX}")
}

/// Before each frame: lets Saved marks go; whether one went.
pub(super) fn poll(state: &mut State, now: f64) -> bool {
    let plugins = panel(state);
    let marks = plugins.saved.len();
    plugins.saved.retain(|_, until| now < *until);
    plugins.saved.len() != marks
}

/// The state a plugin's runtime is in, as the Tauri page words it.
fn runtime(plugin: &PluginInfo) -> &str {
    if !plugin.runtime_state.is_empty() {
        return &plugin.runtime_state;
    }
    match plugin.runtime_phase.as_deref() {
        Some(phase) if !phase.is_empty() => phase,
        _ if plugin.connected => "connected",
        _ if plugin.running => "starting",
        _ => "stopped",
    }
}

fn runtime_tone(phase: &str) -> Tone {
    match phase {
        "connected" => Tone::Good,
        "starting" | "backoff" => Tone::Warn,
        "parked" => Tone::Bad,
        _ => Tone::Neutral,
    }
}

fn health_tone(health: &str) -> Tone {
    match health {
        "healthy" => Tone::Good,
        "unhealthy" => Tone::Bad,
        _ => Tone::Neutral,
    }
}

/// A plugin's row, and what is under it: what went wrong, where it lives and its priority.
fn card(state: &State, plugin: &PluginInfo, t: &Tokens) -> (El, Vec<Block>) {
    let plugins = &state.window.settings.plugins;
    let name = plugin.name.clone();
    let busy = plugins.busy.is_some();
    let id = |part: &str| format!("settings.plugin.{name}.{part}");
    let phase = runtime(plugin).to_owned();
    let health = plugin
        .health_status
        .clone()
        .filter(|h| !h.is_empty())
        .unwrap_or_else(|| "unknown".into());
    let fallback = plugin.priority.to_string();
    let typed = plugins.priorities.get(&name);
    let shown = TextEdit::new(fallback.clone());
    let editing = name.clone();
    let priority = TextInput::new(typed.unwrap_or(&shown))
        .key(priority_key(&name))
        .label(format!("{name} priority"))
        .on_edit(move |state: &mut State| {
            panel(state)
                .priorities
                .entry(editing.clone())
                .or_insert_with(|| TextEdit::new(fallback.clone()))
        })
        .on_submit({
            let name = name.clone();
            move |state: &mut State, tx: &mut Tx| commit_priority(state, &name, tx)
        })
        .on_blur({
            let name = name.clone();
            move |state: &mut State, tx: &mut Tx| commit_priority(state, &name, tx)
        });
    let priority = number_box(priority, FieldState::disabled_if(busy), t);
    let saved = plugins.saved.contains_key(&name).then(|| {
        words("Saved", Type::Small, t.success)
            .key(id("saved"))
            .role("status")
            .label("Saved")
    });
    let removing = name.clone();
    let mut under: Vec<Block> = Vec::new();
    if let Some(description) = plugin.description.as_ref().filter(|d| !d.is_empty()) {
        under.push(Prose::hint(id("description"), description.clone()).build(t));
    }
    if let Some(message) = plugin.health_message.as_ref().filter(|m| !m.is_empty()) {
        under.push(Notice::warn(id("healthcheck"), format!("Healthcheck: {message}")).build(t));
    }
    if let Some(exit) = plugin.last_exit.as_ref().filter(|e| !e.is_empty()) {
        under.push(Notice::warn(id("last-exit"), format!("Last exit: {exit}")).build(t));
    }
    let mut facts = Facts::new(id("details")).code(id("path"), "Path", plugin.dir.clone());
    if let Some(target) = plugin.link_target.as_ref().filter(|l| !l.is_empty()) {
        facts = facts.code(id("linked-to"), "Linked to", target.clone());
    }
    if let Some(attempt) = plugin.restart_attempt {
        facts = facts.code(
            id("restart-attempt"),
            "Restart attempt",
            attempt.to_string(),
        );
    }
    if let Some(next) = plugin.next_restart_at.as_ref().filter(|n| !n.is_empty()) {
        facts = facts.code(id("next-restart"), "Next restart", next.clone());
    }
    under.push(facts.build(t));
    under.push(
        Field::new(priority_key(&name), "Priority")
            .control(priority)
            .mark(saved)
            .build(t),
    );
    if plugins.busy.as_deref() == Some(name.as_str()) {
        under.push(Status::new(id("busy"), format!("Updating {name}..."), true).build(t));
    }
    let tagged =
        |key: String, said: Said, tone: Tone| El::from(tag(key, said, tone, t)).role("status");
    let tags = Inline::tight()
        .child(tagged(
            id("version"),
            format!("v{}", plugin.version).into(),
            Tone::Neutral,
        ))
        .children(
            plugin
                .link_target
                .as_ref()
                .filter(|l| !l.is_empty())
                .map(|_| tagged(id("linked"), "Linked".into(), Tone::Neutral)),
        )
        .child(tagged(
            id("runtime"),
            phase.clone().into(),
            runtime_tone(&phase),
        ))
        .child(tagged(
            id("health"),
            health.clone().into(),
            health_tone(&health),
        ));
    let row = ListRow::new(format!("settings.plugin.{name}"), name.clone())
        .still()
        .wrap()
        .title(name.clone())
        .subtitle_el(tags)
        .controls(
            [Button::new("Remove")
                .look(Look::Danger)
                .size(Size::Compact)
                .disabled(busy || !plugin.can_uninstall)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| remove(state, &removing, tx))
                .build(t)
                .key(id("remove"))
                .into()],
            Reveal::Always,
        )
        .build(t);
    (row, under)
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let plugins = &state.window.settings.plugins;
    let listed = &state.facts.plugins;
    let busy = plugins.busy.is_some();
    let mut page = vec![
        Field::new("settings.plugins.source", "Plugin source")
            .control(line_box(
                TextInput::new(&plugins.source)
                    .key("settings.plugins.source")
                    .label("Plugin source")
                    .placeholder(
                        "git@host:team/my-attn-plugin.git or /Users/you/src/my-attn-plugin",
                    )
                    .on_edit(|state: &mut State| &mut panel(state).source),
                FieldState::disabled_if(busy),
                t,
            ))
            .action(
                Button::new("Browse")
                    .disabled(busy)
                    .on_press_tx(|_: &mut State, tx: &mut Tx| browse(tx))
                    .build(t)
                    .key("settings.plugins.browse"),
            )
            .build(t),
        Actions::new("settings.plugins.actions")
            .push(
                Button::new("Install Plugin")
                    .look(Look::Primary)
                    .disabled(busy)
                    .on_press_tx(install)
                    .build(t)
                    .key("settings.plugins.install"),
            )
            .build(),
    ];
    if let Some(error) = &plugins.error {
        page.push(Notice::warn("settings.plugins.error", error.clone()).build(t));
    }
    page.extend(listed.issues.iter().map(|issue| {
        Notice::warn(
            format!("settings.plugins.issue.{}", issue.path),
            format!("{}: {}", issue.path, issue.error),
        )
        .build(t)
    }));
    if plugins.loading {
        page.push(Status::new("settings.plugins.empty", "Loading plugins...", true).build(t));
    } else if listed.plugins.is_empty() {
        page.push(Prose::empty("settings.plugins.empty", "No plugins installed.").build(t));
    } else {
        page.push(
            listed
                .plugins
                .iter()
                .map(|plugin| card(state, plugin, t))
                .fold(
                    List::new("settings.plugins.list", "Plugins"),
                    |list, (row, under)| list.row_with(row, under),
                )
                .build(t),
        );
    }
    page
}

/// Whether any plugin has a problem, and how many of them run.
pub(super) fn pills(state: &State, t: &Tokens) -> Vec<El> {
    let listed = &state.facts.plugins;
    let problems = listed.issues.len()
        + listed
            .plugins
            .iter()
            .filter(|p| p.health_status.as_deref() == Some("unhealthy"))
            .count();
    let running = listed
        .plugins
        .iter()
        .filter(|p| p.connected || p.running)
        .count();
    let health = match problems {
        0 => El::from(tag(
            "settings.pill.plugins.health",
            "healthy",
            Tone::Good,
            t,
        ))
        .role("status"),
        n => El::from(tag(
            "settings.pill.plugins.health",
            format!("{n} issue{}", if n == 1 { "" } else { "s" }),
            Tone::Bad,
            t,
        ))
        .role("status"),
    };
    vec![
        health,
        El::from(tag(
            "settings.pill.plugins.running",
            format!("{running}/{} running", listed.plugins.len()),
            Tone::Neutral,
            t,
        ))
        .role("status"),
    ]
}
