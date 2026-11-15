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
use rune::tok::{space, type_};
use rune::{El, FileDialogOptions, TextEdit, Tx, col, row, text};
use rune_kit::{Button, TextInput};

use super::pages::{CAPTION, NUMBER_FIELD, Tone, pill, warning};
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
    /// The plugin whose priority field the focus was in at the last frame.
    editing: Option<String>,
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

/// Before each frame: commits the priority field the focus just left, and lets Saved marks
/// go; whether one went.
pub(super) fn poll(state: &mut State, focused: Option<&str>, now: f64, tx: &mut Tx) -> bool {
    let editing = focused
        .and_then(|key| {
            key.strip_prefix(PRIORITY_PREFIX)?
                .strip_suffix(PRIORITY_SUFFIX)
        })
        .map(str::to_owned);
    let plugins = panel(state);
    let left = plugins
        .editing
        .clone()
        .filter(|was| Some(was) != editing.as_ref());
    plugins.editing = editing;
    let marks = plugins.saved.len();
    plugins.saved.retain(|_, until| now < *until);
    let went = plugins.saved.len() != marks;
    if let Some(name) = left {
        commit_priority(state, &name, tx);
    }
    went
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
        _ => Tone::Plain,
    }
}

fn health_tone(health: &str) -> Tone {
    match health {
        "healthy" => Tone::Good,
        "unhealthy" => Tone::Bad,
        _ => Tone::Plain,
    }
}

fn meta(key: String, label: &str, value: String, t: &Tokens) -> El {
    row()
        .key(key)
        .label(format!("{label} {value}"))
        .gap(space::S3)
        .child(
            text(label.to_owned())
                .font(CAPTION)
                .color(t.text_tertiary)
                .w(110.0),
        )
        .child(
            text(value)
                .font(type_::MONO_CAPTION)
                .color(t.text_primary)
                .wrap()
                .grow(1.0)
                .min_w(0.0),
        )
}

fn card(state: &State, plugin: &PluginInfo, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
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
        .disabled(busy)
        .width(Some(NUMBER_FIELD / 2.0))
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
        .build(k);
    let saved = plugins.saved.contains_key(&name).then(|| {
        text("Saved")
            .key(id("saved"))
            .role("status")
            .label("Saved")
            .font(CAPTION)
            .color(t.success)
    });
    let removing = name.clone();
    let mut details: Vec<El> = Vec::new();
    if let Some(description) = plugin.description.as_ref().filter(|d| !d.is_empty()) {
        details.push(
            text(description.clone())
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap(),
        );
    }
    if let Some(message) = plugin.health_message.as_ref().filter(|m| !m.is_empty()) {
        details.push(warning(
            id("healthcheck"),
            format!("Healthcheck: {message}"),
            t,
        ));
    }
    if let Some(exit) = plugin.last_exit.as_ref().filter(|e| !e.is_empty()) {
        details.push(warning(id("last-exit"), format!("Last exit: {exit}"), t));
    }
    details.push(meta(id("path"), "Path", plugin.dir.clone(), t));
    if let Some(target) = plugin.link_target.as_ref().filter(|l| !l.is_empty()) {
        details.push(meta(id("linked-to"), "Linked to", target.clone(), t));
    }
    if let Some(attempt) = plugin.restart_attempt {
        details.push(meta(
            id("restart-attempt"),
            "Restart attempt",
            attempt.to_string(),
            t,
        ));
    }
    if let Some(next) = plugin.next_restart_at.as_ref().filter(|n| !n.is_empty()) {
        details.push(meta(id("next-restart"), "Next restart", next.clone(), t));
    }
    details.push(
        row()
            .gap(space::S3)
            .items_center()
            .child(
                text("Priority")
                    .font(CAPTION)
                    .color(t.text_tertiary)
                    .w(110.0),
            )
            .child(priority)
            .children(saved),
    );
    if plugins.busy.as_deref() == Some(name.as_str()) {
        details.push(
            text(format!("Updating {name}..."))
                .key(id("busy"))
                .font(CAPTION)
                .color(t.text_tertiary),
        );
    }
    let link = plugin
        .link_target
        .as_ref()
        .filter(|l| !l.is_empty())
        .map(|_| pill(id("linked"), "Linked".into(), Tone::Plain, t));
    col()
        .key(format!("settings.plugin.{name}"))
        .role("group")
        .label(name.clone())
        .w_full()
        .gap(space::S2)
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            row()
                .w_full()
                .gap(space::S2)
                .items_center()
                .child(text(name.clone()).font(type_::LABEL).color(t.text_primary))
                .child(pill(
                    id("version"),
                    format!("v{}", plugin.version),
                    Tone::Plain,
                    t,
                ))
                .children(link)
                .child(pill(id("runtime"), phase.clone(), runtime_tone(&phase), t))
                .child(pill(id("health"), health.clone(), health_tone(&health), t))
                .child(row().grow(1.0))
                .child(
                    Button::new("Remove")
                        .disabled(busy || !plugin.can_uninstall)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            remove(state, &removing, tx)
                        })
                        .build(k)
                        .key(id("remove")),
                ),
        )
        .children(details)
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let k = attn_ui::kit(t);
    let plugins = &state.window.settings.plugins;
    let listed = &state.facts.plugins;
    let busy = plugins.busy.is_some();
    let form = row()
        .w_full()
        .gap(space::S2)
        .items_center()
        .child(
            TextInput::new(&plugins.source)
                .key("settings.plugins.source")
                .label("Plugin source")
                .placeholder("git@host:team/my-attn-plugin.git or /Users/you/src/my-attn-plugin")
                .disabled(busy)
                .on_edit(|state: &mut State| &mut panel(state).source)
                .width(None)
                .build(k)
                .grow(1.0),
        )
        .child(
            Button::new("Browse")
                .disabled(busy)
                .on_press_tx(|_: &mut State, tx: &mut Tx| browse(tx))
                .build(k)
                .key("settings.plugins.browse"),
        )
        .child(
            Button::new("Install Plugin")
                .disabled(busy)
                .on_press_tx(install)
                .build(k)
                .key("settings.plugins.install"),
        );
    let mut page = vec![form];
    if let Some(error) = &plugins.error {
        page.push(warning("settings.plugins.error".into(), error.clone(), t));
    }
    page.extend(listed.issues.iter().map(|issue| {
        warning(
            format!("settings.plugins.issue.{}", issue.path),
            format!("{}: {}", issue.path, issue.error),
            t,
        )
    }));
    let empty = |message: &str| {
        text(message.to_owned())
            .key("settings.plugins.empty")
            .label(message.to_owned())
            .font(type_::BODY)
            .color(t.text_tertiary)
    };
    if plugins.loading {
        page.push(empty("Loading plugins..."));
    } else if listed.plugins.is_empty() {
        page.push(empty("No plugins installed."));
    } else {
        page.push(
            col()
                .key("settings.plugins.list")
                .w_full()
                .gap(space::S2)
                .children(listed.plugins.iter().map(|plugin| card(state, plugin, t))),
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
        0 => pill(
            "settings.pill.plugins.health".into(),
            "healthy".into(),
            Tone::Good,
            t,
        ),
        n => pill(
            "settings.pill.plugins.health".into(),
            format!("{n} issue{}", if n == 1 { "" } else { "s" }),
            Tone::Bad,
            t,
        ),
    };
    vec![
        health,
        pill(
            "settings.pill.plugins.running".into(),
            format!("{running}/{} running", listed.plugins.len()),
            Tone::Plain,
            t,
        ),
    ]
}
