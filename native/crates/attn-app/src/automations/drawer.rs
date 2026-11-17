use attn_protocol::{AutomationDefinitionSummary, AutomationRunSummary};
use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Switch, TextInput};

use super::{Automations, DRAWER, form};
use crate::State;

const MIN_WIDTH: f32 = 420.0;
const MAX_WIDTH: f32 = 640.0;

pub(crate) fn pull_request_field(definition: i64) -> String {
    format!("automations.pr-url.{definition}")
}

/// The drawer's width: 42% of the window, between 420 and 640 points, as the Tauri app's
/// `clamp(420px, 42vw, 640px)`.
fn width(state: &State) -> f32 {
    (state.window.width.window * 0.42).clamp(MIN_WIDTH, MAX_WIDTH)
}

pub(crate) fn drawer(state: &State, t: &Tokens) -> El {
    let automations = &state.window.automations;
    let body = match &automations.form {
        Some(form) => form::view(state, form, t),
        None => list(automations, t),
    };
    col()
        .key(DRAWER)
        .role("region")
        .label("Automations")
        .focusable(true)
        .on_key(drawer_key)
        .w(width(state))
        .h_full()
        .min_h(0.0)
        .fill(t.bg_panel)
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
        .child(body)
}

fn drawer_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key == Key::Escape {
        crate::shell::escape_drawers(state, tx);
        return true;
    }
    false
}

fn new_button(key: &str, t: &Tokens) -> El {
    Button::new("New automation")
        .variant(ButtonVariant::Submit)
        .control_size(ControlSize::Small)
        .on_press_tx(super::new_form)
        .build(attn_ui::kit(t))
        .key(key.to_owned())
}

fn list(automations: &Automations, t: &Tokens) -> El {
    let header = row()
        .key("automations.header")
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S3)
        .py(space::S2)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            text("Automations")
                .font(type_::SECTION)
                .color(t.text_primary)
                .grow(1.0),
        )
        .child(new_button("automations.new", t))
        .child(
            GlyphButton::new(icon::CLOSE, "Close automations")
                .on_press_tx(super::close)
                .build(attn_ui::kit(t))
                .key("automations.close"),
        );
    let mut body = col()
        .key("automations.body")
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .gap(space::S3)
        .padding(space::S3)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    if let Some(error) = &automations.error {
        body = body.child(error_text("automations.error", error, t));
    }
    let empty =
        automations.loaded && automations.error.is_none() && automations.definitions.is_empty();
    if empty {
        body = body.child(
            col()
                .key("automations.empty")
                .gap(space::S2)
                .child(
                    text("No automations yet.")
                        .font(type_::BODY)
                        .color(t.text_secondary),
                )
                .child(new_button("automations.new-empty", t)),
        );
    } else {
        body = body.child(
            col()
                .key("automations.list")
                .role("list")
                .label("Automations")
                .w_full()
                .gap(space::S2)
                .children(
                    automations
                        .definitions
                        .iter()
                        .map(|d| definition_row(automations, d, t)),
                ),
        );
    }
    if let Some(selected) = automations
        .selected
        .and_then(|id| automations.definitions.iter().find(|d| d.id == id))
    {
        body = body.child(runs(automations, selected, t));
    }
    col()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(header)
        .child(body)
}

fn error_text(key: &str, error: &str, t: &Tokens) -> El {
    text(error.to_owned())
        .key(key.to_owned())
        .role("alert")
        .font(type_::LABEL)
        .color(t.error)
        .wrap()
}

fn quiet_text(words: String, t: &Tokens) -> El {
    text(words)
        .font(type_::LABEL)
        .color(t.text_secondary)
        .wrap()
}

fn definition_row(
    automations: &Automations,
    definition: &AutomationDefinitionSummary,
    t: &Tokens,
) -> El {
    let k = attn_ui::kit(t);
    let id = definition.id;
    let latest = definition.last_run.as_ref();
    let guard = definition.guard_check.as_ref();
    let failed =
        latest.is_some_and(|r| r.state == "failed") || guard.is_some_and(|g| g.outcome == "failed");
    let selected = automations.selected == Some(id);
    let mut summary = row()
        .w_full()
        .gap(space::S2)
        .items_center()
        .child(
            text(definition.name.clone())
                .font(type_::BODY)
                .color(t.text_primary)
                .truncate(),
        )
        .child(
            text(super::trigger_label(definition))
                .font(type_::LABEL)
                .color(t.text_secondary)
                .truncate(),
        );
    if let Some(label) = definition
        .launch_desktop
        .as_ref()
        .and_then(|l| l.label.clone())
    {
        summary = summary.child(
            text(label)
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .truncate(),
        );
    }
    let badge = failed.then(|| {
        text("Failed")
            .key(format!("automations.failure.{id}"))
            .role("status")
            .label("Failed")
            .font(type_::LABEL)
            .color(t.error)
    });
    let trigger = super::trigger_label(definition);
    let select = Button::new(definition.name.clone())
        .variant(if selected {
            ButtonVariant::Neutral
        } else {
            ButtonVariant::GhostMuted
        })
        .control_size(ControlSize::Small)
        .label_with(move |_, _| summary)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::select(state, id, tx))
        .build(k)
        .key(format!("automations.select.{id}"))
        .description(trigger)
        .checked(selected)
        .grow(1.0)
        .min_w(0.0);
    let enabled = definition.enabled;
    let toggle = Switch::new(enabled)
        .label(if enabled { "Enabled" } else { "Disabled" })
        .control_size(ControlSize::Mini)
        .disabled(automations.toggling.contains(&id))
        .on_toggle_tx(move |state: &mut State, tx: &mut Tx| {
            super::set_enabled(state, id, !enabled, tx);
        })
        .build(k)
        .key(format!("automations.toggle.{id}"));
    let run_now = Button::new("Run now")
        .control_size(ControlSize::Small)
        .disabled(automations.running.contains_key(&id))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run_now(state, id, tx))
        .build(k)
        .key(format!("automations.run-now.{id}"));
    let edit = Button::new("Edit")
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::edit(state, id, tx))
        .build(k)
        .key(format!("automations.edit.{id}"));
    let mut item = col()
        .key(format!("automations.row.{id}"))
        .role("listitem")
        .label(definition.name.clone())
        .w_full()
        .gap(space::S1)
        .padding(space::S2)
        .radius(6.0)
        .fill(if selected {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .child(
            row()
                .w_full()
                .gap(space::S2)
                .items_center()
                .child(select)
                .children(badge)
                .child(toggle)
                .child(run_now)
                .child(edit),
        );
    if let Some(prompt) = automations
        .pull_request
        .as_ref()
        .filter(|p| p.definition == id)
    {
        item = item.child(pull_request_prompt(automations, prompt, t));
    }
    if let Some(error) = automations.toggle_errors.get(&id) {
        item = item.child(error_text(
            &format!("automations.toggle-error.{id}"),
            error,
            t,
        ));
    }
    if let Some(error) = automations.run_errors.get(&id) {
        item = item.child(error_text(&format!("automations.run-error.{id}"), error, t));
    }
    if let Some(guard) = guard {
        let checks = if guard.checks_since_run == 1 {
            "check"
        } else {
            "checks"
        };
        let ago = crate::settings::long_ago(&guard.checked_at, automations.listed_ms);
        item = item.child(
            quiet_text(
                format!(
                    "Guard: {} · checked {ago} · {} {checks} since the last run",
                    guard.outcome, guard.checks_since_run
                ),
                t,
            )
            .key(format!("automations.guard.{id}")),
        );
        if guard.outcome == "failed"
            && let Some(detail) = guard.detail.as_ref().filter(|d| !d.is_empty())
        {
            item = item.child(error_text(
                &format!("automations.guard-error.{id}"),
                detail,
                t,
            ));
        }
    }
    if let Some(run) = latest.filter(|r| r.state == "failed") {
        if let Some(error) = run.last_error.as_ref().filter(|e| !e.is_empty()) {
            item = item.child(error_text(
                &format!("automations.last-error.{id}"),
                error,
                t,
            ));
        }
        if let Some(fix) = &run.fix {
            let fix = fix.clone();
            let harness = crate::picker::agents::label(&fix.agent);
            item = item.child(
                Button::new(format!("Trust folder in {harness}"))
                    .variant(ButtonVariant::Outline)
                    .control_size(ControlSize::Small)
                    .help(fix.directory.clone())
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        super::fix(state, fix.clone(), tx);
                    })
                    .build(k)
                    .key(format!("automations.fix.{id}")),
            );
        }
    }
    item
}

/// The prompt's URL, or the drawer's spare once the prompt is gone, so the field's last edit as
/// focus leaves it has somewhere to land.
fn pull_request_url(state: &mut State) -> &mut TextEdit {
    let automations = &mut state.window.automations;
    match automations.pull_request.as_mut() {
        Some(prompt) => &mut prompt.url,
        None => &mut automations.spare,
    }
}

fn pull_request_prompt(
    automations: &Automations,
    prompt: &super::PullRequestPrompt,
    t: &Tokens,
) -> El {
    let k = attn_ui::kit(t);
    let id = prompt.definition;
    let field = TextInput::new(&prompt.url)
        .key(pull_request_field(id))
        .label("Pull request URL")
        .placeholder("https://github.com/owner/repo/pull/42")
        .on_edit(pull_request_url)
        .on_change(|state: &mut State, _: &mut Tx| {
            if let Some(prompt) = state.window.automations.pull_request.as_mut() {
                prompt.missing = false;
            }
        })
        .on_submit(super::run_pull_request)
        .on_key(|state: &mut State, ev: &KeyEvent, _: &mut Tx| {
            if ev.key != Key::Escape {
                return false;
            }
            super::cancel_pull_request(state);
            true
        })
        .build(k)
        .grow(1.0);
    let mut prompt_el = col()
        .key(format!("automations.pr.{id}"))
        .w_full()
        .gap(space::S1)
        .child(
            row()
                .w_full()
                .gap(space::S2)
                .items_center()
                .child(field)
                .child(
                    Button::new("Run")
                        .variant(ButtonVariant::Submit)
                        .control_size(ControlSize::Small)
                        .disabled(automations.running.contains_key(&id))
                        .on_press_tx(super::run_pull_request)
                        .build(k)
                        .key(format!("automations.pr-run.{id}")),
                )
                .child(
                    Button::new("Cancel")
                        .variant(ButtonVariant::GhostMuted)
                        .control_size(ControlSize::Small)
                        .on_press(super::cancel_pull_request)
                        .build(k)
                        .key(format!("automations.pr-cancel.{id}")),
                ),
        );
    if prompt.missing {
        prompt_el = prompt_el.child(error_text(
            &format!("automations.pr-error.{id}"),
            "Enter a pull request URL.",
            t,
        ));
    }
    prompt_el
}

fn runs(automations: &Automations, definition: &AutomationDefinitionSummary, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let mut runs: Vec<&AutomationRunSummary> = automations
        .runs
        .get(&definition.id)
        .map(|r| r.iter().collect())
        .unwrap_or_default();
    runs.sort_by(|a, b| b.created_at.cmp(&a.created_at));
    let mut list = col()
        .key("automations.run-list")
        .role("list")
        .label(format!("{} — runs", definition.name))
        .w_full()
        .gap(space::S1)
        .children(runs.iter().map(|run| run_row(run, t)));
    if runs.is_empty() {
        list = list.child(
            text("No runs yet.")
                .key("automations.runs-empty")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        );
    }
    let mut panel = col()
        .key("automations.runs")
        .w_full()
        .gap(space::S2)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
        .py(space::S2)
        .child(
            row()
                .w_full()
                .items_center()
                .gap(space::S2)
                .child(
                    text(format!("{} — runs", definition.name))
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .grow(1.0)
                        .truncate(),
                )
                .child(
                    Button::new("Close")
                        .variant(ButtonVariant::GhostMuted)
                        .control_size(ControlSize::Small)
                        .on_press(super::unselect)
                        .build(k)
                        .key("automations.runs-close"),
                ),
        );
    if let Some(error) = &automations.runs_error {
        panel = panel.child(error_text("automations.runs-error", error, t));
    }
    panel.child(list)
}

fn run_row(run: &AutomationRunSummary, t: &Tokens) -> El {
    let mut words = vec![
        run.state.clone(),
        crate::calendar::ms_of_rfc3339(&run.created_at)
            .map_or_else(|| run.created_at.clone(), crate::calendar::date_time),
    ];
    if let Some(delivered) = run
        .delivered_at
        .as_ref()
        .and_then(|d| crate::calendar::ms_of_rfc3339(d))
    {
        words.push(format!(
            "delivered {}",
            crate::calendar::date_time(delivered)
        ));
    }
    match (&run.automation, &run.occurrence_key) {
        (Some(automation), _) => {
            words.push(crate::desktop::provenance::automation_words(automation))
        }
        (None, Some(occurrence)) if !occurrence.is_empty() => words.push(occurrence.clone()),
        _ => {}
    }
    let said = words.join(" · ");
    let line = text(said.clone())
        .font(type_::LABEL)
        .color(t.text_secondary)
        .wrap();
    let session = run.session_id.clone().filter(|s| !s.is_empty());
    let main = match session {
        Some(session) => Button::new(said)
            .variant(ButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .label_with(move |_, _| line)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::open_run(state, &session, tx);
            })
            .build(attn_ui::kit(t))
            .key(format!("automations.run-open.{}", run.id))
            .w_full(),
        None => line,
    };
    let mut item = col()
        .key(format!("automations.run.{}", run.id))
        .role("listitem")
        .label(run.state.clone())
        .w_full()
        .gap(space::S1)
        .child(main);
    if let Some(error) = run.last_error.as_ref().filter(|e| !e.is_empty()) {
        item = item.child(error_text(
            &format!("automations.run-error-text.{}", run.id),
            error,
            t,
        ));
    }
    item
}
