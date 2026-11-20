use attn_protocol::{AutomationDefinitionSummary, AutomationRunSummary};
use attn_ui::blocks::{Actions, Block, Inline, List, Notice, Refusal, Section, Sheet};
use attn_ui::system::{
    Button, FieldState, IconSize, ListRow, Look, Reveal, Size, Tone, Type, icon_button, line_box,
    switch, tag, words,
};
use attn_ui::{Tokens, glyph};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col};
use rune_kit::TextInput;

use super::{Automations, DRAWER, form};
use crate::State;

pub(crate) fn pull_request_field(definition: i64) -> String {
    format!("automations.pr-url.{definition}")
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
        .h_full()
        .min_h(0.0)
        .child(body)
}

fn drawer_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key == Key::Escape {
        crate::shell::escape_drawers(state, tx);
        return true;
    }
    false
}

fn close_button(key: &str, label: &str, t: &Tokens) -> El {
    icon_button(key, label, glyph::TIMES, IconSize::Large)
        .on_press_tx(super::close)
        .build(t)
        .into()
}

fn new_button(key: &str, t: &Tokens) -> El {
    Button::new("New automation")
        .size(Size::Compact)
        .look(Look::Primary)
        .on_press_tx(super::new_form)
        .build(t)
        .key(key.to_owned())
        .into()
}

fn list(automations: &Automations, t: &Tokens) -> El {
    let mut sheet = Sheet::new("automations.sheet", "Automations")
        .aside(new_button("automations.new", t))
        .close(close_button("automations.close", "Close automations", t));
    if let Some(error) = &automations.error {
        sheet = sheet.push(Notice::error("automations.error", error.clone()).build(t));
    }
    let shows_empty =
        automations.loaded && automations.error.is_none() && automations.definitions.is_empty();
    let mut definitions = List::new("automations.list", "Automations").card();
    for definition in &automations.definitions {
        let (row, under) = definition_row(automations, definition, t);
        definitions = definitions.row_with(row, under);
    }
    if shows_empty {
        definitions = definitions.empty(
            "automations.empty",
            "No automations yet.",
            Some(new_button("automations.new-empty", t)),
        );
    }
    sheet = sheet.push(definitions.build(t));
    if let Some(selected) = automations
        .selected
        .and_then(|id| automations.definitions.iter().find(|d| d.id == id))
    {
        sheet = sheet.push(runs(automations, selected, t));
    }
    sheet.build(t).key("automations.body")
}

fn refusal(key: String, said: &str, t: &Tokens) -> Block {
    Block::custom(
        format!("{key}.block"),
        Refusal::new(key, said.to_owned()).build(t),
    )
}

fn note(key: String, said: String, t: &Tokens) -> Block {
    Block::custom(
        format!("{key}.block"),
        words(said.clone(), Type::Small, t.text_tertiary)
            .key(key)
            .label(said)
            .wrap(),
    )
}

fn definition_row(
    automations: &Automations,
    definition: &AutomationDefinitionSummary,
    t: &Tokens,
) -> (El, Vec<Block>) {
    let id = definition.id;
    let latest = definition.last_run.as_ref();
    let guard = definition.guard_check.as_ref();
    let failed =
        latest.is_some_and(|r| r.state == "failed") || guard.is_some_and(|g| g.outcome == "failed");
    let selected = automations.selected == Some(id);
    let mut line = super::trigger_label(definition);
    if let Some(desktop) = definition
        .launch_desktop
        .as_ref()
        .and_then(|l| l.label.clone())
    {
        line = format!("{line} · {desktop}");
    }
    let enabled = definition.enabled;
    let word = if enabled { "Enabled" } else { "Disabled" };
    let toggling = automations.toggling.contains(&id);
    let controls: Vec<El> = vec![
        switch(
            format!("automations.toggle.{id}"),
            word,
            enabled,
            false,
            toggling,
            t,
            move |state: &mut State, tx: &mut Tx| super::set_enabled(state, id, !enabled, tx),
        )
        .into(),
        Button::new("Run now")
            .size(Size::Compact)
            .disabled(automations.running.contains_key(&id))
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run_now(state, id, tx))
            .build(t)
            .key(format!("automations.run-now.{id}"))
            .into(),
        Button::new("Edit")
            .size(Size::Compact)
            .look(Look::Quiet)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::edit(state, id, tx))
            .build(t)
            .key(format!("automations.edit.{id}"))
            .into(),
    ];
    let mut row = ListRow::new(format!("automations.row.{id}"), definition.name.clone())
        .title(definition.name.clone())
        .subtitle(line, t)
        .selected(selected)
        .select(
            format!("automations.select.{id}"),
            definition.name.clone(),
            move |state: &mut State, tx: &mut Tx| super::select(state, id, tx),
        )
        .controls(controls, Reveal::Always);
    if failed {
        row = row
            .tag(tag(format!("automations.failure.{id}"), "Failed", Tone::Bad, t).label("Failed"));
    }
    let mut under = Vec::new();
    if let Some(prompt) = automations
        .pull_request
        .as_ref()
        .filter(|p| p.definition == id)
    {
        under.push(pull_request_prompt(automations, prompt, t));
    }
    if let Some(error) = automations.toggle_errors.get(&id) {
        under.push(refusal(format!("automations.toggle-error.{id}"), error, t));
    }
    if let Some(error) = automations.run_errors.get(&id) {
        under.push(refusal(format!("automations.run-error.{id}"), error, t));
    }
    if let Some(guard) = guard {
        let checks = if guard.checks_since_run == 1 {
            "check"
        } else {
            "checks"
        };
        let ago = crate::settings::long_ago(&guard.checked_at, automations.listed_ms);
        under.push(note(
            format!("automations.guard.{id}"),
            format!(
                "Guard: {} · checked {ago} · {} {checks} since the last run",
                guard.outcome, guard.checks_since_run
            ),
            t,
        ));
        if guard.outcome == "failed"
            && let Some(detail) = guard.detail.as_ref().filter(|d| !d.is_empty())
        {
            under.push(refusal(format!("automations.guard-error.{id}"), detail, t));
        }
    }
    if let Some(run) = latest.filter(|r| r.state == "failed") {
        if let Some(error) = run.last_error.as_ref().filter(|e| !e.is_empty()) {
            under.push(refusal(format!("automations.last-error.{id}"), error, t));
        }
        if let Some(fix) = &run.fix {
            let fix = fix.clone();
            let harness = crate::picker::agents::label(&fix.agent);
            let help = fix.directory.clone();
            under.push(
                Actions::new("automations.trust.actions")
                    .push(
                        Button::new(format!("Trust folder in {harness}"))
                            .size(Size::Compact)
                            .help(help)
                            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                super::fix(state, fix.clone(), tx);
                            })
                            .build(t)
                            .key(format!("automations.fix.{id}")),
                    )
                    .build(),
            );
        }
    }
    (row.build(t), under)
}

fn pull_request_url(state: &mut State) -> &mut TextEdit {
    &mut state
        .window
        .automations
        .pull_request
        .as_mut()
        .expect("the prompt shows")
        .url
}

fn pull_request_prompt(
    automations: &Automations,
    prompt: &super::PullRequestPrompt,
    t: &Tokens,
) -> Block {
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
        });
    let line = Inline::loose()
        .w_full()
        .child(El::from(line_box(field, FieldState::failed_if(prompt.missing), t)).grow(1.0))
        .child(
            Button::new("Run")
                .size(Size::Compact)
                .look(Look::Primary)
                .disabled(automations.running.contains_key(&id))
                .on_press_tx(super::run_pull_request)
                .build(t)
                .key(format!("automations.pr-run.{id}"))
                .into(),
        )
        .child(
            Button::new("Cancel")
                .size(Size::Compact)
                .look(Look::Quiet)
                .on_press(super::cancel_pull_request)
                .build(t)
                .key(format!("automations.pr-cancel.{id}"))
                .into(),
        );
    let prompt_el = col()
        .key(format!("automations.pr.{id}"))
        .w_full()
        .child(line)
        .children(prompt.missing.then(|| {
            Refusal::new(
                format!("automations.pr-error.{id}"),
                "Enter a pull request URL.",
            )
            .build(t)
        }));
    Block::custom("automations.trust.prompt.block", prompt_el)
}

fn runs(automations: &Automations, definition: &AutomationDefinitionSummary, t: &Tokens) -> Block {
    let mut runs: Vec<&AutomationRunSummary> = automations
        .runs
        .get(&definition.id)
        .map(|r| r.iter().collect())
        .unwrap_or_default();
    runs.sort_by(|a, b| b.created_at.cmp(&a.created_at));
    let title = format!("{} — runs", definition.name);
    let mut section = Section::new("automations.runs", title.clone()).control(
        Button::new("Close")
            .size(Size::Compact)
            .look(Look::Quiet)
            .on_press(super::unselect)
            .build(t)
            .key("automations.runs-close"),
    );
    if let Some(error) = &automations.runs_error {
        section = section.push(Notice::error("automations.runs-error", error.clone()).build(t));
    }
    section
        .push(
            runs.iter()
                .fold(
                    List::new("automations.run-list", title).card(),
                    |list, run| {
                        let (row, under) = run_row(run, t);
                        list.row_with(row, under)
                    },
                )
                .empty("automations.runs-empty", "No runs yet.", None)
                .build(t),
        )
        .build(t)
}

fn run_row(run: &AutomationRunSummary, t: &Tokens) -> (El, Vec<Block>) {
    let when = crate::calendar::ms_of_rfc3339(&run.created_at)
        .map_or_else(|| run.created_at.clone(), crate::calendar::date_time);
    let mut words = vec![run.state.clone(), when.clone()];
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
    let tone = match run.state.as_str() {
        "failed" => Tone::Bad,
        "delivered" => Tone::Good,
        _ => Tone::Neutral,
    };
    let mut row = ListRow::new(format!("automations.run.{}", run.id), run.state.clone())
        .title(when)
        .tag(tag(
            format!("automations.run-state.{}", run.id),
            &run.state,
            tone,
            t,
        ));
    let rest = words[2..].join(" · ");
    if !rest.is_empty() {
        row = row.subtitle(rest, t);
    }
    row = match run.session_id.clone().filter(|s| !s.is_empty()) {
        Some(session) => row.select(
            format!("automations.run-open.{}", run.id),
            words.join(" · "),
            move |state: &mut State, tx: &mut Tx| super::open_run(state, &session, tx),
        ),
        None => row.role("listitem").unfocusable(),
    };
    let under = run
        .last_error
        .as_ref()
        .filter(|e| !e.is_empty())
        .map(|error| refusal(format!("automations.run-error-text.{}", run.id), error, t))
        .into_iter()
        .collect();
    (row.build(t), under)
}
