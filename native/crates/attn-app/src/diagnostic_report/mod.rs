//! "Create diagnostic report": one file in the user's Downloads with what it takes to trace a
//! stuck terminal, and the output of the panes the user ticks.

mod report;
mod save;

use std::collections::BTreeSet;
use std::path::PathBuf;

use attn_protocol::SupportSnapshotMessage;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, FileDialogOptions, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, Checkbox, ControlSize, Dialog};
use serde_json::{Value, json};

use crate::State;
use crate::requests::Answer;
use crate::shell::{Layer, View};
use crate::sidebar::model::desktop_title;
use report::{Capture, Output, Pane};

const DIALOG: &str = "diagnostic-report";
const SAVE: &str = "diagnostic-report.save";
const SNAPSHOT_DEADLINE_S: f64 = 3.0;

/// The open dialog: what the window showed when the user asked, the panes they ticked, and
/// the daemon's evidence as it arrives.
pub(crate) struct Draft {
    capture: Capture,
    selected: BTreeSet<String>,
    daemon: Snapshot,
    deadline: f64,
    saving: bool,
    error: Option<String>,
}

enum Snapshot {
    Waiting,
    Taken(Value),
    Missing,
}

pub(crate) fn open(state: &mut State, tx: &mut Tx) {
    let capture = capture(state, tx);
    let selected = capture
        .affected_pane
        .iter()
        .filter(|id| {
            capture
                .panes
                .iter()
                .any(|p| &p.pane_id == *id && p.available)
        })
        .cloned()
        .collect();
    let runtime_ids: BTreeSet<String> =
        capture.panes.iter().map(|p| p.runtime_id.clone()).collect();
    state.window.diagnostic_report = Some(Draft {
        capture,
        selected,
        daemon: Snapshot::Waiting,
        deadline: tx.clock() + SNAPSHOT_DEADLINE_S,
        saving: false,
        error: None,
    });
    tx.wake_at(tx.clock() + SNAPSHOT_DEADLINE_S);
    state.ask(
        |request_id| SupportSnapshotMessage {
            cmd: "support_snapshot".into(),
            request_id,
            runtime_ids: Some(runtime_ids.into_iter().collect()),
        },
        took_snapshot,
        tx,
    );
    state.window.layer = Some(Layer::DiagnosticReport);
    tx.focus(SAVE, false);
}

fn took_snapshot(state: &mut State, answer: Answer, tx: &mut Tx) {
    let Some(draft) = &mut state.window.diagnostic_report else {
        return;
    };
    if !matches!(draft.daemon, Snapshot::Waiting) {
        return;
    }
    draft.daemon = match answer {
        Answer::Reply(reply) if reply.event == "support_snapshot_result" => {
            Snapshot::Taken(reply.body)
        }
        _ => Snapshot::Missing,
    };
    if draft.saving {
        write(state, tx);
    }
}

/// Gives up on the daemon's evidence once its deadline passes, and writes a report the user
/// is waiting on without it.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    let Some(draft) = &mut state.window.diagnostic_report else {
        return false;
    };
    if !matches!(draft.daemon, Snapshot::Waiting) || now < draft.deadline {
        return false;
    }
    draft.daemon = Snapshot::Missing;
    if draft.saving {
        write(state, tx);
    }
    true
}

fn capture(state: &State, tx: &Tx) -> Capture {
    let facts = &state.facts;
    let desktops = &facts.desktops;
    let label = |session_id: &str| {
        facts
            .sessions
            .iter()
            .find(|s| s.id == session_id)
            .map(|s| s.label.clone())
            .filter(|l| !l.is_empty())
    };
    let panes: Vec<Pane> = desktops
        .iter()
        .flat_map(|desktop| {
            desktop
                .panes
                .iter()
                .filter(|pane| pane.kind == "agent")
                .map(|pane| {
                    let title = if pane.title.is_empty() {
                        pane.pane_id.clone()
                    } else {
                        pane.title.clone()
                    };
                    Pane {
                        pane_id: pane.pane_id.clone(),
                        runtime_id: pane.runtime_id.clone(),
                        session_id: pane.session_id.clone(),
                        session_label: label(&pane.session_id).unwrap_or_else(|| title.clone()),
                        title,
                        desktop_id: desktop.id.clone(),
                        desktop_label: desktop_title(desktop, desktops),
                        available: state.terminals.has_output(&pane.runtime_id),
                    }
                })
        })
        .collect();
    let holder = state
        .window
        .keyboard
        .as_ref()
        .filter(|_| state.window.view == View::Desktop);
    let active_session = holder.and_then(|h| {
        desktops
            .iter()
            .find(|d| d.id == h.desktop)?
            .panes
            .iter()
            .find(|p| p.pane_id == h.pane)
            .map(|p| p.session_id.clone())
    });
    let desktop_of = |session_id: &str| {
        desktops
            .iter()
            .find(|d| d.panes.iter().any(|p| p.session_id == session_id))
            .map(|d| d.id.clone())
            .unwrap_or_default()
    };
    let sessions = facts
        .sessions
        .iter()
        .map(|s| {
            json!({
                "id": s.id,
                "label": s.label,
                "state": s.state,
                "agent": s.agent,
                "cwd": report::shortened(&s.directory),
                "desktopId": desktop_of(&s.id),
                "active": Some(&s.id) == active_session.as_ref(),
            })
        })
        .collect();
    let desktop_snapshots = desktops
        .iter()
        .map(|desktop| {
            let active_session = desktop
                .panes
                .iter()
                .find(|p| p.pane_id == desktop.active_pane_id)
                .map(|p| p.session_id.as_str());
            let directory = facts
                .sessions
                .iter()
                .find(|s| Some(s.id.as_str()) == active_session)
                .map(|s| report::shortened(&s.directory))
                .unwrap_or_default();
            json!({
                "id": desktop.id,
                "label": desktop_title(desktop, desktops),
                "directory": directory,
                "layout": serde_json::from_str::<Value>(&desktop.tree_json).unwrap_or(Value::Null),
            })
        })
        .collect();
    Capture {
        origin: json!({
            "capturedAtUnixMs": state.wall_ms(tx),
            "view": match state.window.view { View::Home => "home", View::Desktop => "desktop" },
            "activeSessionId": active_session,
            "activePaneId": holder.map(|h| h.pane.clone()),
        }),
        affected_pane: holder.map(|h| h.pane.clone()),
        panes,
        sessions,
        desktops: desktop_snapshots,
        settings: report::chosen_settings(&facts.settings),
    }
}

fn close(state: &mut State, tx: &mut Tx) {
    if state
        .window
        .diagnostic_report
        .as_ref()
        .is_some_and(|d| d.saving)
    {
        return;
    }
    state.window.diagnostic_report = None;
    if state.window.layer == Some(Layer::DiagnosticReport) {
        state.window.layer = None;
        tx.blur();
    }
}

fn start_saving(state: &mut State, tx: &mut Tx) {
    let Some(draft) = &mut state.window.diagnostic_report else {
        return;
    };
    if draft.saving {
        return;
    }
    draft.saving = true;
    draft.error = None;
    if matches!(draft.daemon, Snapshot::Waiting) && tx.clock() < draft.deadline {
        return;
    }
    write(state, tx);
}

fn write(state: &mut State, tx: &mut Tx) {
    let created_at = state.wall_ms(tx);
    let Some(draft) = state.window.diagnostic_report.take() else {
        return;
    };
    let selected: Vec<String> = draft.selected.iter().cloned().collect();
    let terminals = &mut state.terminals;
    let outputs: Vec<(String, Output)> = draft
        .capture
        .panes
        .iter()
        .filter(|p| draft.selected.contains(&p.pane_id))
        .map(|p| {
            let output = match terminals.text(&p.runtime_id) {
                Some(text) => Output::Text(text),
                None => Output::Gone,
            };
            (p.pane_id.clone(), output)
        })
        .collect();
    let daemon = match &draft.daemon {
        Snapshot::Taken(snapshot) => Some(snapshot),
        Snapshot::Waiting | Snapshot::Missing => None,
    };
    let built = report::build(
        &draft.capture,
        &selected,
        daemon,
        |pane| match outputs.iter().find(|(id, _)| *id == pane.pane_id) {
            Some((_, Output::Text(text))) => Output::Text(text.clone()),
            _ => Output::Gone,
        },
        created_at,
        uuid::Uuid::new_v4().to_string(),
    );
    state.window.diagnostic_report = Some(draft);
    let contents = match built {
        Ok(contents) => contents,
        Err(why) => return failed(state, why),
    };
    let name = save::file_name(created_at);
    let folder = save::downloads(&crate::frame::folders());
    let saved = match &folder {
        Some(folder) => save::write_new(folder, &name, &contents).map_err(|e| e.to_string()),
        None => Err("this machine names no Downloads folder".into()),
    };
    match saved {
        Ok(path) => saved_to(state, path, tx),
        Err(why) => ask_where(name, folder, contents, why, tx),
    }
}

fn ask_where(
    name: String,
    folder: Option<PathBuf>,
    contents: String,
    automatic: String,
    tx: &mut Tx,
) {
    let options = FileDialogOptions {
        title: "Save diagnostic report".into(),
        directory: folder,
        file_name: Some(name),
        ..FileDialogOptions::default()
    }
    .filter("attn diagnostic report", &["json"]);
    tx.save_file(options, move |state: &mut State, chosen, tx| match chosen {
        Ok(Some(path)) => match std::fs::write(&path, &contents) {
            Ok(()) => saved_to(state, path, tx),
            Err(why) => failed(state, why.to_string()),
        },
        Ok(None) => failed(state, automatic),
        Err(why) => failed(state, why.to_string()),
    });
}

fn saved_to(state: &mut State, path: PathBuf, tx: &mut Tx) {
    if let Some(draft) = &mut state.window.diagnostic_report {
        draft.saving = false;
    }
    close(state, tx);
    let _ = tx.reveal_path(&path.to_string_lossy());
}

fn failed(state: &mut State, why: String) {
    if let Some(draft) = &mut state.window.diagnostic_report {
        draft.saving = false;
        draft.error = Some(why);
    }
}

fn toggle(state: &mut State, pane: &str) {
    let Some(draft) = &mut state.window.diagnostic_report else {
        return;
    };
    if !draft.selected.remove(pane) {
        draft.selected.insert(pane.to_owned());
    }
}

pub(crate) fn dialog(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let Some(draft) = &state.window.diagnostic_report else {
        return;
    };
    let k = attn_ui::kit(t);
    let saving = draft.saving;
    let mut groups: Vec<(&str, &str, Vec<&Pane>)> = Vec::new();
    for pane in &draft.capture.panes {
        match groups.iter_mut().find(|(id, _, _)| *id == pane.desktop_id) {
            Some((_, _, panes)) => panes.push(pane),
            None => groups.push((&pane.desktop_id, &pane.desktop_label, vec![pane])),
        }
    }
    let output_heading = row()
        .w_full()
        .items_center()
        .gap(space::S3)
        .child(
            col()
                .grow(1.0)
                .gap(space::S1)
                .child(
                    text("Recent terminal output")
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .child(
                    text("Optional. Output may contain private text or secrets.")
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                ),
        )
        .children((!draft.selected.is_empty()).then(|| {
            Button::new("Clear")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .disabled(saving)
                .on_press(|state: &mut State| {
                    if let Some(draft) = &mut state.window.diagnostic_report {
                        draft.selected.clear();
                    }
                })
                .build(k)
                .key("diagnostic-report.clear")
        }));
    let choices = if groups.is_empty() {
        col().child(
            text("No mounted terminal panes are available.")
                .key("diagnostic-report.empty")
                .font(type_::BODY)
                .color(t.text_secondary),
        )
    } else {
        col()
            .gap(space::S3)
            .children(groups.into_iter().map(|(id, label, panes)| {
                col()
                    .key(format!("diagnostic-report.desktop.{id}"))
                    .role("group")
                    .label(label.to_owned())
                    .gap(space::S1)
                    .child(
                        text(label.to_owned())
                            .font(type_::LABEL)
                            .color(t.text_tertiary),
                    )
                    .children(panes.into_iter().map(|pane| {
                        let id = pane.pane_id.clone();
                        let description = if pane.available {
                            pane.session_label.clone()
                        } else {
                            format!("{}, not currently mounted", pane.session_label)
                        };
                        Checkbox::new(draft.selected.contains(&pane.pane_id))
                            .label(pane.title.clone())
                            .description(description)
                            .disabled(saving || !pane.available)
                            .on_toggle(move |state: &mut State| toggle(state, &id))
                            .build(k)
                            .key(format!("diagnostic-report.pane.{}", pane.pane_id))
                    }))
            }))
    };
    let footer = row()
        .w_full()
        .gap(space::S2)
        .child(rune::el().grow(1.0))
        .child(
            Button::new("Cancel")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .disabled(saving)
                .on_press_tx(close)
                .build(k)
                .key("diagnostic-report.cancel"),
        )
        .child(
            Button::new(if saving { "Saving…" } else { "Save report" })
                .variant(ButtonVariant::Submit)
                .control_size(ControlSize::Small)
                .disabled(saving)
                .on_press_tx(start_saving)
                .build(k)
                .key(SAVE)
                .default_button(),
        );
    let body = col()
        .key("diagnostic-report.body")
        .w_full()
        .gap(space::S3)
        .padding(space::S6)
        .pt(0.0)
        .child(
            text("Save one file with the evidence needed to trace a stuck terminal.")
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        )
        .child(
            col()
                .key("diagnostic-report.included")
                .gap(space::S1)
                .child(text("Included automatically").font(type_::LABEL).color(t.text_primary))
                .child(
                    text("App and daemon versions, session and pane metadata, shortened working directories, and the daemon's terminal health.")
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .wrap(),
                )
                .child(
                    text("Environment variables, credentials, clipboard data, raw prompt bodies, raw terminal bytes, logs, and error text stay out.")
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .wrap(),
                ),
        )
        .child(output_heading)
        .child(choices)
        .children(draft.error.as_ref().map(|why| {
            text(why.clone())
                .key("diagnostic-report.error")
                .role("alert")
                .font(type_::BODY)
                .color(t.error)
                .wrap()
        }))
        .child(footer);
    Dialog::new(DIALOG, "Create diagnostic report")
        .open(true)
        .fit()
        .width(520.0)
        .click_outside(!saving)
        .on_close_tx(close)
        .child(body)
        .build(k, layers);
}
