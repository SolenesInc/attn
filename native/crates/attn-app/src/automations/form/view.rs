use std::rc::Rc;

use attn_ui::blocks::{
    Actions, Block, Disclosure, Field as FieldBlock, Inline, Notice, Prose, Refusal, Sheet, Stack,
};
use attn_ui::system::{
    Beside, Button, FieldState, IconSize, Look, Segment, Segmented, Size, Trigger, TriggerLook,
    Type, area_box, code_box, icon_button, line_box, switch, words,
};
use attn_ui::{Tokens, glyph};
use rune::{Align, El, Key, KeyEvent, Side, TextEdit, Tx};
use rune_kit::{Placement, PopupItem, PopupList, Select, TextArea, TextInput};

use super::model::{CatchUp, Continuity, Field, Location, Trigger as Starts};
use super::{Form, KEY, Load, ZoneSuggestions, field_key, form, form_mut};
use crate::State;
use crate::automations::schedule::{ALL_DAYS, Friendly, Tab, Unit, WEEKDAYS};
use crate::settings::{Route, RouteHarness, RouteTarget, Rules};

pub(super) const DELETE: &str = "automations.form.delete";
pub(super) const TIME_ZONE: &str = "automations.form.time-zone";
const TIME_ZONES: &str = "automations.form.time-zones";
const AUTOMATION_HARNESSES: [&str; 2] = ["codex", "claude"];
const MINUTE_STEPS: [u32; 7] = [1, 2, 5, 10, 15, 20, 30];
const HOUR_STEPS: [u32; 7] = [1, 2, 3, 4, 6, 8, 12];
const DAYS: [(u8, &str, &str); 7] = [
    (1, "M", "Monday"),
    (2, "T", "Tuesday"),
    (3, "W", "Wednesday"),
    (4, "T", "Thursday"),
    (5, "F", "Friday"),
    (6, "S", "Saturday"),
    (0, "S", "Sunday"),
];
const MONTHS: [&str; 12] = [
    "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];
const WEEKDAY_NAMES: [&str; 7] = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
/// The prompt's least height, the Tauri app's `.automation-form__textarea { min-height: 120px }`.
const PROMPT_HEIGHT: f32 = 120.0;
/// attn checks schedules once a minute.
const SCHEDULER_TICK_MS: i64 = 60_000;

fn kit(t: &Tokens) -> &'static rune_kit::Kit {
    attn_ui::kit(t)
}

fn key(name: &str) -> String {
    format!("{KEY}.{name}")
}

fn field_error(f: &Form, field: Field) -> Option<String> {
    f.errors.get(&field).cloned()
}

fn edited(state: &mut State, checked: Option<fn(&Field) -> bool>) {
    if let (Some(f), Some(which)) = (form_mut(state), checked) {
        f.edited(which);
    }
}

fn input(
    f: &Form,
    edit: &TextEdit,
    field_key: String,
    label: &str,
    placeholder: &str,
    on_edit: fn(&mut State) -> &mut TextEdit,
    t: &Tokens,
) -> El {
    text_field(f, edit, field_key, label, placeholder, on_edit, line_box, t)
}

#[allow(clippy::too_many_arguments)]
fn text_field(
    f: &Form,
    edit: &TextEdit,
    field_key: String,
    label: &str,
    placeholder: &str,
    on_edit: fn(&mut State) -> &mut TextEdit,
    shown: fn(TextInput, FieldState, &Tokens) -> attn_ui::system::Control,
    t: &Tokens,
) -> El {
    let checked = super::field_of_key(&field_key);
    let refused = checked.is_some_and(|which| f.errors.keys().any(which));
    let input = TextInput::new(edit)
        .key(field_key)
        .label(label)
        .placeholder(placeholder)
        .spellcheck(false)
        .on_edit(on_edit)
        .on_change(move |state: &mut State, _: &mut Tx| edited(state, checked))
        .on_submit(super::save);
    shown(input, state_of(f, refused), t).into()
}

fn state_of(f: &Form, refused: bool) -> FieldState {
    if f.saving {
        FieldState::Disabled
    } else {
        FieldState::failed_if(refused)
    }
}

fn field_of(state: &mut State, pick: impl FnOnce(&mut Form) -> &mut TextEdit) -> &mut TextEdit {
    pick(
        state
            .window
            .automations
            .form
            .as_mut()
            .expect("the form shows"),
    )
}

pub(crate) fn view(state: &State, f: &Form, t: &Tokens) -> El {
    let title = if f.id.is_some() {
        "Edit automation"
    } else {
        "New automation"
    };
    let close = icon_button(key("close"), "Close", glyph::TIMES, IconSize::Large)
        .on_press_tx(super::close)
        .build(t);
    let mut sheet = Sheet::new(KEY, title);
    if let (Some(id), Load::Ready) = (f.id, &f.load) {
        let said = format!("ID: {id}");
        sheet = sheet.aside(
            words(said.clone(), Type::Small, t.text_tertiary)
                .key(key("id"))
                .label(said),
        );
    }
    if let (Some(enabled), Load::Ready) = (f.enabled, &f.load) {
        let word = if enabled { "Enabled" } else { "Disabled" };
        sheet = sheet.aside(Inline::loose().child(El::from(switch(
            key("enabled"),
            word,
            enabled,
            Beside::Label,
            false,
            t,
            |state: &mut State, tx: &mut Tx| super::toggle_enabled(state, tx),
        ))));
    }
    let sheet = sheet.close(close);
    let sheet = match &f.load {
        Load::Loading => sheet.push(Prose::lead(key("loading"), "Loading…").quiet(true).build(t)),
        Load::Failed(why) => sheet.push(
            Notice::error(format!("{}.error", key("load")), why.clone())
                .action(
                    Button::new("Retry")
                        .on_press_tx(super::reload)
                        .build(t)
                        .key(key("retry")),
                )
                .build(t),
        ),
        Load::Ready => {
            let mut sheet = sheet.push_all(fields(state, f, t));
            for block in footer(f, t) {
                sheet = sheet.footer(block);
            }
            sheet
        }
    };
    sheet.build(t).role("form").label(title)
}

fn fields(state: &State, f: &Form, t: &Tokens) -> Vec<Block> {
    let mut out = Vec::new();
    if f.saving {
        out.push(Prose::lead(key("saving"), "Saving…").quiet(true).build(t));
    }
    out.push(
        FieldBlock::new(field_key(Field::Name), "Name")
            .control(input(
                f,
                &f.name,
                field_key(Field::Name),
                "Name",
                "Automation name",
                |s| field_of(s, |f| &mut f.name),
                t,
            ))
            .error(field_error(f, Field::Name))
            .build(t),
    );
    let mut starts = FieldBlock::new(key("triggers"), "Trigger").control(trigger(f, t));
    if f.trigger == Starts::Manual {
        starts = starts.hint("Fresh worker each run");
    }
    out.push(starts.build(t));
    match f.trigger {
        Starts::Manual => {
            if f.location == Location::Directory {
                out.push(directory(f, t));
            }
        }
        Starts::Scheduled => {
            out.extend(schedule(f, t));
            out.push(
                FieldBlock::new(key("guard"), "Only run if")
                    .control(text_field(
                        f,
                        &f.guard,
                        key("guard"),
                        "Only run if",
                        "git rev-parse HEAD",
                        |s| field_of(s, |f| &mut f.guard),
                        code_box,
                        t,
                    ))
                    .hint("Runs in the folder before each scheduled run. The agent starts only when the command prints something new.")
                    .build(t),
            );
            out.push(directory(f, t));
        }
        Starts::GitHub => out.extend(github(f, t)),
    }
    out.extend(launch(state, f, t));
    out.push(prompt(f, t));
    out.push(advanced(f, t));
    out
}

fn trigger(f: &Form, t: &Tokens) -> El {
    let starts = [
        (Starts::Manual, "manual", "Manual"),
        (Starts::Scheduled, "scheduled", "Scheduled"),
        (Starts::GitHub, "github", "PR review requested"),
    ];
    El::from(
        Segmented::new(key("triggers"), "Trigger")
            .disabled(f.saving)
            .options(starts.into_iter().map(|(trigger, id, label)| {
                Segment::new(key(&format!("trigger-{id}")), label)
                    .chosen(f.trigger == trigger)
                    .on_press_tx(move |state: &mut State, _: &mut Tx| {
                        super::choose_trigger(state, trigger)
                    })
            }))
            .build(t),
    )
    .self_start()
}

fn directory(f: &Form, t: &Tokens) -> Block {
    FieldBlock::new(field_key(Field::DirectoryPath), "Folder")
        .control(input(
            f,
            &f.directory,
            field_key(Field::DirectoryPath),
            "Folder",
            "/absolute/path",
            |s| field_of(s, |f| &mut f.directory),
            t,
        ))
        .action(
            Button::new("Browse")
                .disabled(f.saving)
                .on_press_tx(|_: &mut State, tx: &mut Tx| super::browse(tx))
                .build(t)
                .key(key("browse")),
        )
        .error(field_error(f, Field::DirectoryPath))
        .build(t)
}

fn repository_list(f: &Form, exclude: bool, t: &Tokens) -> Block {
    let (list, draft, name, label) = if exclude {
        (
            &f.exclude,
            &f.exclude_draft,
            "exclude",
            "Exclude repositories",
        )
    } else {
        (
            &f.include,
            &f.include_draft,
            "include",
            "Include repositories",
        )
    };
    let field: fn(usize) -> Field = if exclude {
        Field::Exclude
    } else {
        Field::Include
    };
    let input_key = key(&format!("repositories-{name}"));
    let chips = list.iter().enumerate().map(|(i, repository)| {
        let refused = f.errors.contains_key(&field(i));
        let remove = icon_button(
            key(&format!("repositories-{name}-remove.{i}")),
            format!("Remove {repository}"),
            glyph::TIMES,
            IconSize::Small,
        )
        .on_press(move |state: &mut State| {
            super::remove_repository(state, exclude, i);
        })
        .build(t);
        Inline::tight()
            .key(key(&format!("repositories-{name}-chip.{i}")))
            .child(words(
                repository.clone(),
                Type::Small,
                if refused { t.error } else { t.text_primary },
            ))
            .child(remove.into())
    });
    let draft_input = TextInput::new(draft)
        .key(input_key.clone())
        .label(label)
        .placeholder("host/owner/repository")
        .spellcheck(false)
        .on_edit(move |state: &mut State| {
            field_of(state, |f| {
                if exclude {
                    &mut f.exclude_draft
                } else {
                    &mut f.include_draft
                }
            })
        })
        .on_submit(move |state: &mut State, _: &mut Tx| super::add_repository(state, exclude))
        .on_key(move |state: &mut State, ev: &KeyEvent, _: &mut Tx| {
            if ev.key != Key::Backspace {
                return false;
            }
            let Some(f) = form(state) else { return false };
            let (list, draft) = if exclude {
                (&f.exclude, &f.exclude_draft)
            } else {
                (&f.include, &f.include_draft)
            };
            if !draft.text.is_empty() || list.is_empty() {
                return false;
            }
            let last = list.len() - 1;
            super::remove_repository(state, exclude, last);
            true
        });
    let errors = f
        .errors
        .iter()
        .filter(|(fl, _)| (0..list.len()).any(|i| **fl == field(i)))
        .map(|(_, words)| words.clone())
        .collect::<std::collections::BTreeSet<_>>();
    let mut out = FieldBlock::new(input_key, label);
    if !list.is_empty() {
        out = out.control(Inline::tight().children(chips));
    }
    out.control(line_box(draft_input, state_of(f, false), t))
        .error((!errors.is_empty()).then(|| errors.into_iter().collect::<Vec<_>>().join(" ")))
        .build(t)
}

fn github(f: &Form, t: &Tokens) -> Vec<Block> {
    let facts = [
        "One reviewer per PR — later request cycles return to it.",
        "Existing requests are left alone when enabled.",
        "Missed while attn was off: the latest request still runs.",
    ];
    vec![
        repository_list(f, false, t),
        repository_list(f, true, t),
        Block::custom(key("github-facts.block"), 
            Stack::tight()
                .key(key("github-facts"))
                .children(facts.map(|fact| words(fact, Type::Small, t.text_secondary).wrap())),
        ),
        Notice::info(
            key("github-worktree"),
            "Reviews always run in a fresh worktree checked out at the PR's head commit — your existing clone is never touched.",
        )
        .build(t),
    ]
}

pub(super) fn route_target() -> RouteTarget {
    RouteTarget {
        id: "automation".into(),
        read: read_route,
        write: write_route,
    }
}

fn overridden(f: &Form) -> bool {
    f.agent != "codex" && !f.executable.text.trim().is_empty()
}

fn read_route(state: &State, _: &str) -> Option<(Route, Rules)> {
    let f = form(state).filter(|f| f.load == Load::Ready)?;
    let offered: Vec<&str> = AUTOMATION_HARNESSES
        .into_iter()
        .chain(std::iter::once(f.agent.as_str()))
        .collect();
    let overridden = overridden(f);
    let mut harnesses: Vec<RouteHarness> = Vec::new();
    for h in crate::settings::listed_harnesses(state) {
        if offered.contains(&h.id.as_str()) && !harnesses.iter().any(|o| o.id == h.id) {
            let mut h = RouteHarness::from(&h);
            h.discovery &= !overridden;
            harnesses.push(h);
        }
    }
    Some((
        Route::stored(&f.agent, &f.model, &f.effort),
        Rules {
            harnesses,
            ..Rules::default()
        },
    ))
}

fn write_route(state: &mut State, _: &str, route: Route, _: &mut Tx) {
    if let Some(f) = form_mut(state) {
        f.model = route.stored_model();
        f.agent = route.harness;
        f.effort = route.effort;
    }
}

fn launch(state: &State, f: &Form, t: &Tokens) -> Vec<Block> {
    let desktop = crate::launch_desktop::Field {
        key: match f.id {
            Some(id) => format!("{KEY}.{id}"),
            None => format!("{KEY}.new"),
        },
        kind: "automation".into(),
        item_id: f.id.map(|id| id.to_string()).unwrap_or_default(),
        profile_id: f.profile.clone(),
        default_name: f.name.text.clone(),
        value: f.launch_desktop.clone(),
        disabled: f.saving,
        on_change: super::choose_desktop,
    };
    let (choice, unread) = crate::launch_desktop::trigger(state, t, &desktop);
    let mut hint =
        "Automation sessions always run unattended with the agent's automatic approval mode."
            .to_owned();
    if overridden(f) {
        hint.push_str(" Use model and effort IDs accepted by the executable override.");
    }
    vec![
        FieldBlock::new(desktop.select_key(), "Desktop")
            .control(choice)
            .error(unread)
            .build(t),
        FieldBlock::new(key("runs-as"), "Runs as")
            .control(crate::settings::route_chip(
                state,
                t,
                route_target(),
                "Automation model",
            ))
            .hint(hint)
            .build(t),
    ]
}

fn prompt(f: &Form, t: &Tokens) -> Block {
    let checked = super::field_of_key(&field_key(Field::Prompt));
    let area = TextArea::new(&f.prompt)
        .key(field_key(Field::Prompt))
        .label("Prompt")
        .spellcheck(true)
        .min_height(PROMPT_HEIGHT)
        .resizable()
        .on_edit(|s: &mut State| field_of(s, |f| &mut f.prompt))
        .on_change(move |state: &mut State, _: &mut Tx| edited(state, checked));
    FieldBlock::new(field_key(Field::Prompt), "Prompt")
        .control(area_box(area, state_of(f, f.errors.contains_key(&Field::Prompt)), t))
        .hint("This is the instruction the agent receives. Trigger details arrive separately as structured context — they can never rewrite this prompt.")
        .error(field_error(f, Field::Prompt))
        .build(t)
}

fn segments<T: Copy + PartialEq + 'static>(
    name: &str,
    label: &str,
    current: T,
    options: &[(T, &'static str)],
    pick: fn(&mut Form, T),
    t: &Tokens,
) -> El {
    El::from(
        Segmented::new(key(name), label)
            .options(options.iter().enumerate().map(|(i, &(value, words))| {
                Segment::new(format!("segment{i}"), words)
                    .chosen(value == current)
                    .on_press_tx(move |state: &mut State, _: &mut Tx| {
                        if let Some(f) = form_mut(state) {
                            pick(f, value);
                        }
                    })
            }))
            .build(t),
    )
    .self_start()
}

fn advanced_chips(f: &Form) -> Vec<String> {
    let mut chips = Vec::new();
    if f.trigger == Starts::Scheduled && f.continuity == Continuity::Singleton {
        chips.push("Same agent".into());
    }
    if f.trigger == Starts::Scheduled && f.catch_up == CatchUp::Skip {
        chips.push("Skip missed runs".into());
    }
    if f.trigger == Starts::Manual && f.location == Location::Worktree {
        chips.push("Pull request worktree".into());
    }
    let clones = f.overrides.len();
    if f.values().uses_worktree() && clones > 0 {
        chips.push(if clones == 1 {
            "1 local clone".into()
        } else {
            format!("{clones} local clones")
        });
    }
    if overridden(f) {
        chips.push("Custom executable".into());
    }
    chips
}

fn advanced(f: &Form, t: &Tokens) -> Block {
    let failed = f.errors.keys().any(|field| {
        matches!(
            field,
            Field::CatchUp | Field::OverrideRepository(_) | Field::OverridePath(_)
        )
    });
    let mut part = Disclosure::new(
        key("advanced"),
        "Advanced",
        f.advanced,
        |state: &mut State, _: &mut Tx| {
            if let Some(f) = form_mut(state) {
                f.advanced = !f.advanced;
            }
        },
    )
    .tags(advanced_chips(f))
    .failed(failed);
    if f.trigger == Starts::Scheduled {
        part = part
            .push(
                FieldBlock::new(key("continuity-field"), "Agent")
                    .control(segments(
                        "continuity",
                        "Agent",
                        f.continuity,
                        &[
                            (Continuity::Fresh, "New each run"),
                            (Continuity::Singleton, "Same agent"),
                        ],
                        |f, v| f.continuity = v,
                        t,
                    ))
                    .hint(match f.continuity {
                        Continuity::Fresh => "Every run starts a new agent.",
                        Continuity::Singleton => {
                            "Every run goes to one agent, and it resumes if closed. Changing the prompt, model or folder starts a replacement."
                        }
                    })
                    .build(t),
            )
            .push(
                FieldBlock::new(field_key(Field::CatchUp), "Missed runs")
                    .control(segments(
                        "catchup",
                        "Missed runs",
                        f.catch_up,
                        &[(CatchUp::Latest, "Run latest"), (CatchUp::Skip, "Skip")],
                        |f, v| {
                            f.catch_up = v;
                            f.check(|field| *field == Field::CatchUp);
                        },
                        t,
                    ))
                    .hint(match f.catch_up {
                        CatchUp::Skip => {
                            "A missed time still runs if attn notices within 5 minutes. Older ones are dropped."
                        }
                        _ => "After downtime, one run for the most recent missed time.",
                    })
                    .error(field_error(f, Field::CatchUp))
                    .build(t),
            );
    }
    if f.trigger == Starts::Manual {
        part = part.push(
            FieldBlock::new(key("location-field"), "Works in")
                .control(segments(
                    "location",
                    "Works in",
                    f.location,
                    &[
                        (Location::Directory, "Folder"),
                        (Location::Worktree, "Pull request worktree"),
                    ],
                    |f, v| f.location = v,
                    t,
                ))
                .hint(match f.location {
                    Location::Directory => "The agent works in the folder above.",
                    Location::Worktree => {
                        "Run now asks for a pull request, and the agent works in a fresh worktree at its head."
                    }
                })
                .build(t),
        );
    }
    if f.values().uses_worktree() {
        part = part.push(overrides(f, t));
    }
    if f.agent != "codex" {
        part = part.push(
            FieldBlock::new(key("executable"), "Executable")
                .control(input(
                    f,
                    &f.executable,
                    key("executable"),
                    "Executable",
                    "Default from PATH",
                    |s| field_of(s, |f| &mut f.executable),
                    t,
                ))
                .build(t),
        );
    }
    if !f.yaml.is_empty() {
        part = definition(f, t)
            .into_iter()
            .fold(part, |part, block| part.push(block));
    }
    part.build(t)
}

fn overrides(f: &Form, t: &Tokens) -> Block {
    let clone_input = |edit: &TextEdit,
                       field: Field,
                       label: &str,
                       placeholder: &str,
                       on_edit: Box<dyn Fn(&mut State) -> &mut TextEdit>| {
        let input = TextInput::new(edit)
            .key(field_key(field))
            .label(label)
            .placeholder(placeholder)
            .spellcheck(false)
            .on_edit(move |s: &mut State| on_edit(s));
        El::from(line_box(
            input,
            state_of(f, f.errors.contains_key(&field)),
            t,
        ))
    };
    let mut part = FieldBlock::new(key("overrides"), "Local clones");
    let mut refusals = Vec::new();
    for (i, (repository, path)) in f.overrides.iter().enumerate() {
        part = part.control(
            Inline::loose()
                .key(key(&format!("override.{i}")))
                .w_full()
                .child(
                    clone_input(
                        repository,
                        Field::OverrideRepository(i),
                        "Repository",
                        "host/owner/repository",
                        Box::new(move |s: &mut State| field_of(s, |f| &mut f.overrides[i].0)),
                    )
                    .grow(1.0),
                )
                .child(
                    clone_input(
                        path,
                        Field::OverridePath(i),
                        "Path",
                        "/absolute/path",
                        Box::new(move |s: &mut State| field_of(s, |f| &mut f.overrides[i].1)),
                    )
                    .grow(1.0),
                )
                .child(
                    Button::new("Remove")
                        .look(Look::Quiet)
                        .on_press(move |state: &mut State| super::remove_override(state, i))
                        .build(t)
                        .key(key(&format!("overrides-remove.{i}")))
                        .into(),
                ),
        );
        for field in [Field::OverrideRepository(i), Field::OverridePath(i)] {
            if let Some(why) = field_error(f, field) {
                refusals.push(Refusal::new(format!("{}.error", field_key(field)), why).build(t));
            }
        }
    }
    for refusal in refusals {
        part = part.control(refusal);
    }
    part.control(
        Button::new("Add override")
            .on_press_tx(super::add_override)
            .build(t)
            .key(key("overrides-add")),
    )
    .build(t)
}

fn definition(f: &Form, t: &Tokens) -> Vec<Block> {
    let mut actions = Actions::new(key("definition.actions")).push(
        Button::new(if f.definition_shown {
            "Hide definition"
        } else {
            "Show definition"
        })
        .look(Look::Quiet)
        .expanded(f.definition_shown)
        .on_press(|state: &mut State| {
            if let Some(f) = form_mut(state) {
                f.definition_shown = !f.definition_shown;
            }
        })
        .build(t)
        .key(key("definition-toggle")),
    );
    if f.definition_shown {
        let yaml = f.yaml.clone();
        actions = actions.push(
            Button::new(if f.copied { "Copied" } else { "Copy" })
                .look(Look::Quiet)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    tx.copy(yaml.clone());
                    if let Some(f) = form_mut(state) {
                        f.copied = true;
                    }
                })
                .build(t)
                .key(key("definition-copy")),
        );
    }
    let mut out = vec![actions.build()];
    if f.definition_shown {
        out.push(Block::custom(
            key("definition.block"),
            words(f.yaml.clone(), Type::Mono, t.text_secondary)
                .key(key("definition"))
                .label(f.yaml.clone())
                .wrap(),
        ));
    }
    out
}

fn footer(f: &Form, t: &Tokens) -> Vec<Block> {
    let mut out = Vec::new();
    if f.stale {
        out.push(
            Notice::warn(
                key("stale"),
                "This automation changed elsewhere while you were editing. Reload to pick up the latest revision — your unsaved edits will be replaced.",
            )
            .action(
                Button::new("Reload")
                    .on_press_tx(super::reload)
                    .build(t)
                    .key(key("reload")),
            )
            .build(t),
        );
    } else if let Some(why) = &f.save_error {
        out.push(Notice::error(format!("{}.error", key("save")), why.clone()).build(t));
    }
    let mut actions = Actions::new(key("actions"));
    if f.id.is_some() {
        let words_shown = if f.delete_armed {
            "Confirm delete"
        } else {
            "Delete"
        };
        actions = actions.push(
            Button::new(words_shown)
                .look(Look::Danger)
                .on_press_tx(super::delete)
                .build(t)
                .key(DELETE),
        );
        if f.delete_armed {
            actions = actions.push(words(
                "Existing seeds and run history are kept.",
                Type::Small,
                t.text_tertiary,
            ));
        }
    }
    out.push(
        actions
            .then_end()
            .push(
                Button::new("Cancel")
                    .on_press_tx(super::close)
                    .build(t)
                    .key(key("cancel")),
            )
            .push(
                Button::new(if f.saving { "Saving…" } else { "Save" })
                    .look(Look::Primary)
                    .disabled(f.save_blocked())
                    .shortcut("mod+enter")
                    .on_press_tx(super::save)
                    .build(t)
                    .key(key("save")),
            )
            .build(),
    );
    out
}

/// What a schedule control does with the option picked, by its place in the list.
type Pick = Rc<dyn Fn(&mut State, usize)>;

/// A schedule control that picks one of `options`, its list drawn by [`overlay`].
struct Choice {
    key: String,
    label: &'static str,
    options: Vec<String>,
    current: usize,
    pick: Pick,
}

fn select(f: &Form, c: Choice) -> Select {
    let open = f.open_select.as_deref() == Some(c.key.as_str());
    let (toggled, closed) = (c.key.clone(), c.key.clone());
    let options = c.options.clone();
    let pick = c.pick;
    Select::new(c.key.clone(), &c.options)
        .label(c.label)
        .placement(Placement::BottomStart)
        .current(c.options.get(c.current).map(String::as_str))
        .open(open)
        .disabled(f.saving)
        .on_toggle(move |state: &mut State| {
            if let Some(f) = form_mut(state) {
                f.open_select = match f.open_select.take() {
                    Some(open) if open == toggled => None,
                    _ => Some(toggled.clone()),
                };
            }
        })
        .on_close(move |state: &mut State| {
            if let Some(f) = form_mut(state)
                && f.open_select.as_deref() == Some(closed.as_str())
            {
                f.open_select = None;
            }
        })
        .on_choose(move |state: &mut State, label: &str, _: &mut Tx| {
            if let Some(f) = form_mut(state) {
                f.open_select = None;
            }
            if let Some(i) = options.iter().position(|o| o == label) {
                pick(state, i);
            }
        })
}

fn pad(n: u32) -> String {
    format!("{n:02}")
}

fn change(schedule: &Friendly, edit: impl Fn(&mut Friendly, usize) + 'static) -> Pick {
    let schedule = schedule.clone();
    Rc::new(move |state: &mut State, i: usize| {
        let mut next = schedule.clone();
        edit(&mut next, i);
        super::set_schedule(state, next);
    })
}

/// The hour, minute and, on a 12-hour clock, AM or PM a schedule runs at, on the user's clock
/// as the system's time field writes it.
fn time_choices(name: &str, schedule: &Friendly, hour: u32, minute: u32) -> Vec<Choice> {
    let set_hour = |at: fn(u32, usize) -> u32| {
        change(schedule, move |s, i| match s {
            Friendly::Days { hour, .. } | Friendly::Monthly { hour, .. } => *hour = at(*hour, i),
            Friendly::Every { .. } => {}
        })
    };
    let twelve = crate::calendar::hour_cycle() == crate::calendar::HourCycle::Twelve;
    let hours = match twelve {
        true => Choice {
            key: key(&format!("schedule-{name}-hour")),
            label: "Hour",
            options: std::iter::once(12).chain(1..12).map(pad).collect(),
            current: (hour % 12) as usize,
            pick: set_hour(|hour, i| hour / 12 * 12 + i as u32),
        },
        false => Choice {
            key: key(&format!("schedule-{name}-hour")),
            label: "Hour",
            options: (0..24).map(pad).collect(),
            current: hour as usize,
            pick: set_hour(|_, i| i as u32),
        },
    };
    let minutes = Choice {
        key: key(&format!("schedule-{name}-minute")),
        label: "Minute",
        options: (0..60).map(pad).collect(),
        current: minute as usize,
        pick: change(schedule, |s, i| match s {
            Friendly::Days { minute, .. } | Friendly::Monthly { minute, .. } => {
                *minute = i as u32;
            }
            Friendly::Every { .. } => {}
        }),
    };
    let mut out = vec![hours, minutes];
    if twelve {
        out.push(Choice {
            key: key(&format!("schedule-{name}-half")),
            label: "AM or PM",
            options: vec!["AM".into(), "PM".into()],
            current: usize::from(hour >= 12),
            pick: set_hour(|hour, i| hour % 12 + 12 * i as u32),
        });
    }
    out
}

/// The schedule's controls that pick from a list, in the order they show.
fn choices(schedule: &Friendly) -> Vec<Choice> {
    match schedule {
        Friendly::Every {
            unit,
            n,
            window,
            minute,
            ..
        } => {
            let steps: &[u32] = match unit {
                Unit::Minutes => &MINUTE_STEPS,
                Unit::Hours => &HOUR_STEPS,
            };
            let mut offered = steps.to_vec();
            if !offered.contains(n) {
                offered.push(*n);
                offered.sort_unstable();
            }
            let current = offered.iter().position(|o| o == n).unwrap_or(0);
            let mut out = vec![
                Choice {
                    key: key("schedule-every-n"),
                    label: "Interval",
                    options: offered.iter().map(u32::to_string).collect(),
                    current,
                    pick: change(schedule, move |s, i| {
                        if let Friendly::Every { n, .. } = s {
                            *n = offered[i];
                        }
                    }),
                },
                Choice {
                    key: key("schedule-every-unit"),
                    label: "Unit",
                    options: vec!["minutes".into(), "hours".into()],
                    current: usize::from(*unit == Unit::Hours),
                    pick: change(schedule, |s, i| {
                        let chosen = if i == 0 { Unit::Minutes } else { Unit::Hours };
                        if let Friendly::Every {
                            unit, n, minute, ..
                        } = s
                            && *unit != chosen
                        {
                            *unit = chosen;
                            *n = if chosen == Unit::Minutes { 30 } else { 1 };
                            *minute = 0;
                        }
                    }),
                },
            ];
            if *unit == Unit::Hours {
                out.push(Choice {
                    key: key("schedule-every-minute"),
                    label: "Minute",
                    options: (0..60).map(pad).collect(),
                    current: *minute as usize,
                    pick: change(schedule, |s, i| {
                        if let Friendly::Every { minute, .. } = s {
                            *minute = i as u32;
                        }
                    }),
                });
            }
            if let Some((from, to)) = *window {
                out.push(Choice {
                    key: key("schedule-window-from"),
                    label: "From hour",
                    options: (0..24).map(|h| format!("{}:00", pad(h))).collect(),
                    current: from as usize,
                    pick: change(schedule, move |s, i| {
                        if let Friendly::Every { window, .. } = s {
                            let from = i as u32;
                            *window = Some((from, from.max(to)));
                        }
                    }),
                });
                out.push(Choice {
                    key: key("schedule-window-to"),
                    label: "To hour",
                    options: (0..24).map(|h| format!("{}:59", pad(h))).collect(),
                    current: to as usize,
                    pick: change(schedule, move |s, i| {
                        if let Friendly::Every { window, .. } = s {
                            let to = i as u32;
                            *window = Some((from.min(to), to));
                        }
                    }),
                });
            }
            out
        }
        Friendly::Days { hour, minute, .. } => time_choices("days", schedule, *hour, *minute),
        Friendly::Monthly { day, hour, minute } => {
            let mut out = vec![Choice {
                key: key("schedule-month-day"),
                label: "Day of month",
                options: (1..=31).map(|d: u32| d.to_string()).collect(),
                current: (*day as usize).saturating_sub(1),
                pick: change(schedule, |s, i| {
                    if let Friendly::Monthly { day, .. } = s {
                        *day = i as u32 + 1;
                    }
                }),
            }];
            out.extend(time_choices("monthly", schedule, *hour, *minute));
            out
        }
    }
}

fn zone_option_key(zone: &str) -> String {
    format!("{TIME_ZONES}-option-{zone}")
}

fn active_zone(f: &Form) -> Option<&'static str> {
    let ZoneSuggestions::Shown { active: Some(i) } = f.zone_suggestions else {
        return None;
    };
    super::zones_matching(&f.time_zone.text).get(i).copied()
}

/// Down and Up walk the suggestions, looping, Return chooses the highlighted one and Escape
/// hides them, as a datalist's keys do.
fn zone_keys(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(f) = form_mut(state) else {
        return false;
    };
    let count = super::zones_matching(&f.time_zone.text).len();
    let active = match f.zone_suggestions {
        ZoneSuggestions::Shown { active } => active,
        ZoneSuggestions::Hidden => None,
    };
    let shown = f.zone_suggestions != ZoneSuggestions::Hidden;
    let step = match ev.key {
        Key::Down if count > 0 => Some(active.map_or(0, |i| (i + 1) % count)),
        Key::Up if shown && count > 0 => {
            Some(active.map_or(count - 1, |i| (i + count - 1) % count))
        }
        Key::Escape if shown && count > 0 => None,
        Key::Enter => {
            let Some(zone) = active_zone(f) else {
                return false;
            };
            super::choose_zone(state, zone, tx);
            return true;
        }
        _ => return false,
    };
    f.zone_suggestions = match step {
        Some(i) => ZoneSuggestions::Shown { active: Some(i) },
        None => ZoneSuggestions::Hidden,
    };
    true
}

fn zone_suggestions(f: &Form, t: &Tokens, layers: &mut Vec<El>) {
    let ZoneSuggestions::Shown { active } = f.zone_suggestions else {
        return;
    };
    let zones = super::zones_matching(&f.time_zone.text);
    if zones.is_empty() {
        return;
    }
    PopupList::new(TIME_ZONES, TIME_ZONE)
        .label("Time zones")
        .side(Side::Bottom)
        .align(Align::Start)
        .items(zones.iter().map(|zone| PopupItem::new(*zone, *zone)))
        .active(active)
        .on_active(|state: &mut State, i| {
            if let Some(f) = form_mut(state) {
                f.zone_suggestions = ZoneSuggestions::Shown { active: Some(i) };
            }
        })
        .on_choose(super::choose_zone)
        .build(kit(t), layers);
}

/// The open schedule list or the time zone suggestions over the window, when one is open.
pub(crate) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let Some(f) = form(state) else { return };
    if state.window.layer.is_none() {
        crate::settings::route_overlay(state, t, layers);
        crate::launch_desktop::overlay(state, t, layers);
    }
    zone_suggestions(f, t, layers);
    let Some(open) = &f.open_select else { return };
    let Some(schedule) = f.schedule().1 else {
        return;
    };
    if let Some(choice) = choices(&schedule).into_iter().find(|c| &c.key == open) {
        select(f, choice).build(kit(t), layers);
    }
}

fn days_of(schedule: &Friendly) -> Option<&Vec<u8>> {
    match schedule {
        Friendly::Every { days, .. } | Friendly::Days { days, .. } => Some(days),
        Friendly::Monthly { .. } => None,
    }
}

fn with_days(schedule: &Friendly, next: Vec<u8>) -> Friendly {
    let mut schedule = schedule.clone();
    if let Friendly::Every { days, .. } | Friendly::Days { days, .. } = &mut schedule {
        *days = next;
    }
    schedule
}

fn day_buttons(schedule: &Friendly, t: &Tokens) -> El {
    let days = days_of(schedule).cloned().unwrap_or_default();
    let buttons = DAYS.into_iter().map(|(day, letter, name)| {
        let on = days.contains(&day);
        let schedule = schedule.clone();
        let current = days.clone();
        El::from(
            Button::new(letter)
                .size(Size::Compact)
                .look(Look::Quiet)
                .chosen(on)
                .on_press(move |state: &mut State| {
                    if on && current.len() == 1 {
                        return;
                    }
                    let next = if on {
                        current.iter().copied().filter(|d| *d != day).collect()
                    } else {
                        let mut next = current.clone();
                        next.push(day);
                        next
                    };
                    super::set_schedule(state, with_days(&schedule, next));
                })
                .build(t)
                .key(key(&format!("schedule-day.{day}")))
                .label(name),
        )
        .checked(on)
    });
    let preset = |label: &'static str, name: &str, days: &[u8]| -> El {
        let schedule = schedule.clone();
        let days = days.to_vec();
        Button::new(label)
            .size(Size::Compact)
            .look(Look::Quiet)
            .on_press(move |state: &mut State| {
                super::set_schedule(state, with_days(&schedule, days.clone()));
            })
            .build(t)
            .key(key(name))
            .into()
    };
    Inline::loose()
        .child(
            Inline::tight()
                .role("group")
                .label("Days")
                .children(buttons),
        )
        .child(preset("Every day", "schedule-every-day", &ALL_DAYS))
        .child(preset("Weekdays", "schedule-weekdays", &WEEKDAYS))
}

/// The field that opens a schedule control's list, keyed as the list's anchor.
fn choice_trigger(f: &Form, c: &Choice, t: &Tokens) -> El {
    let open = f.open_select.as_deref() == Some(c.key.as_str());
    let shown = c.options.get(c.current).cloned().unwrap_or_default();
    let toggled = c.key.clone();
    El::from(
        Trigger::new(
            format!("{}-trigger", c.key),
            c.label,
            words(shown.clone(), Type::Body, t.text_primary),
        )
        .look(TriggerLook::Field)
        .value(shown)
        .open(open)
        .disabled(f.saving)
        .build(t, move |state: &mut State, _: &mut Tx| {
            if let Some(f) = form_mut(state) {
                f.open_select = match f.open_select.take() {
                    Some(open) if open == toggled => None,
                    _ => Some(toggled.clone()),
                };
            }
        }),
    )
    .role("combobox")
}

fn friendly_controls(f: &Form, schedule: &Friendly, t: &Tokens) -> Vec<El> {
    let mut triggers = choices(schedule)
        .into_iter()
        .map(|c| choice_trigger(f, &c, t))
        .collect::<Vec<_>>()
        .into_iter();
    let mut next = || triggers.next().unwrap_or_else(rune::el);
    let said = |w: &str| words(w.to_owned(), Type::Body, t.text_secondary);
    let twelve = crate::calendar::hour_cycle() == crate::calendar::HourCycle::Twelve;
    let clock = |line: El, next: &mut dyn FnMut() -> El| {
        line.child(next())
            .child(said(":"))
            .child(next())
            .children(twelve.then(next))
    };
    match schedule {
        Friendly::Every { unit, window, .. } => {
            let mut first = Inline::loose()
                .child(said("Every"))
                .child(next())
                .child(next());
            if *unit == Unit::Hours {
                first = first.child(said("at minute")).child(next());
            }
            let on = window.is_some();
            let schedule_now = schedule.clone();
            let mut second = Inline::loose().child(El::from(switch(
                key("schedule-window"),
                "Only between",
                on,
                Beside::Label,
                false,
                t,
                move |state: &mut State, _: &mut Tx| {
                    let mut s = schedule_now.clone();
                    if let Friendly::Every { window, .. } = &mut s {
                        *window = if on { None } else { Some((9, 17)) };
                    }
                    super::set_schedule(state, s);
                },
            )));
            if on {
                second = second.child(next()).child(said("and")).child(next());
            }
            vec![first, second, day_buttons(schedule, t)]
        }
        Friendly::Days { .. } => vec![
            day_buttons(schedule, t),
            clock(Inline::loose().child(said("at")), &mut next),
        ],
        Friendly::Monthly { day, .. } => {
            let line = clock(
                Inline::loose()
                    .child(said("On day"))
                    .child(next())
                    .child(said("at")),
                &mut next,
            );
            let mut out = vec![line];
            if *day > 28 {
                out.push(words(
                    "Months without this day are skipped.",
                    Type::Small,
                    t.text_tertiary,
                ));
            }
            out
        }
    }
}

/// "Tue 17 Nov at 09:00" for an RFC 3339 time, read on the clock its offset names.
fn long_date(time: &str) -> Option<String> {
    let date = time.get(..10)?;
    let day = crate::calendar::day_of_date(date)?;
    let weekday = WEEKDAY_NAMES[crate::calendar::weekday(day) as usize];
    let month: usize = time.get(5..7)?.parse().ok()?;
    let dom: u32 = time.get(8..10)?.parse().ok()?;
    let clock = time.get(11..16)?;
    Some(format!(
        "{weekday} {dom} {} at {clock}",
        MONTHS.get(month - 1)?
    ))
}

/// `ms` on the user's clock, two-digit hours of 24: "18:05".
fn here(ms: i64) -> String {
    let day = crate::calendar::local_day(ms);
    let seconds = (ms - crate::calendar::local_instant(day, 0)) / 1000;
    format!("{:02}:{:02}", seconds / 3600, seconds / 60 % 60)
}

fn until(at_ms: i64, now_ms: i64) -> String {
    let minutes = ((at_ms - now_ms) as f64 / 60_000.0).round().max(0.0) as i64;
    if minutes < 60 {
        return format!("in {minutes}m");
    }
    let hours = (minutes as f64 / 60.0).round() as i64;
    if hours < 48 {
        format!("in {hours}h")
    } else {
        format!("in {}d", (hours as f64 / 24.0).round() as i64)
    }
}

fn next_runs(f: &Form, t: &Tokens) -> Vec<Block> {
    let zone = f.time_zone.text.trim();
    let elsewhere = zone != super::local_zone();
    let times: Vec<(String, i64)> = f
        .preview
        .times
        .iter()
        .filter_map(|time| Some((time.clone(), crate::calendar::ms_of_rfc3339(time)?)))
        .collect();
    let zone_input = TextInput::new(&f.time_zone)
        .key(TIME_ZONE)
        .label("Time zone")
        .spellcheck(false)
        .active_descendant(active_zone(f).map(zone_option_key))
        .on_edit(|s: &mut State| field_of(s, |f| &mut f.time_zone))
        .on_change(|s: &mut State, tx: &mut Tx| {
            if let Some(f) = form_mut(s) {
                f.zone_suggestions = ZoneSuggestions::Shown { active: None };
            }
            super::follow_preview(s, tx);
        })
        .on_key(zone_keys);
    let mut zone = FieldBlock::new(TIME_ZONE, "Time zone").control(line_box(
        zone_input,
        state_of(f, false),
        t,
    ));
    if let [first, second, ..] = times.as_slice()
        && second.1 - first.1 < SCHEDULER_TICK_MS
    {
        zone =
            zone.hint("attn checks schedules once a minute, so this runs at most once a minute.");
    }
    let list = Stack::tight()
        .key(key("next-runs"))
        .role("list")
        .label(if f.guard.text.trim().is_empty() {
            "Next runs"
        } else {
            "Next checks"
        })
        .w_full()
        .children(times.iter().map(|(time, ms)| {
            let mut when = long_date(time).unwrap_or_else(|| time.clone());
            if elsewhere {
                when = format!("{when} ({} here)", here(*ms));
            }
            Inline::loose()
                .role("listitem")
                .w_full()
                .child(words(when, Type::Body, t.text_secondary).grow(1.0))
                .child(words(
                    until(*ms, f.preview.at_ms),
                    Type::Small,
                    t.text_tertiary,
                ))
        }));
    let next = FieldBlock::new(
        key("next-runs-field"),
        if f.guard.text.trim().is_empty() {
            "Next runs"
        } else {
            "Next checks"
        },
    )
    .control(list);
    vec![zone.build(t), next.build(t)]
}

fn schedule(f: &Form, t: &Tokens) -> Vec<Block> {
    let (tab, friendly) = f.schedule();
    let mut field = FieldBlock::new(key("schedule"), "Schedule").control(
        El::from(
            Segmented::new(key("schedule-tabs"), "Schedule type")
                .disabled(f.saving)
                .options(Tab::ALL.iter().enumerate().map(|(i, &(each, _, label))| {
                    Segment::new(format!("segment{i}"), label)
                        .chosen(each == tab)
                        .on_press_tx(move |state: &mut State, _: &mut Tx| {
                            super::choose_tab(state, each)
                        })
                }))
                .build(t),
        )
        .self_start(),
    );
    match (&friendly, tab) {
        (Some(schedule), tab) if tab != Tab::Cron => {
            for line in friendly_controls(f, schedule, t) {
                field = field.control(line);
            }
        }
        (None, _) if !f.cron.text.trim().is_empty() => {
            field = field.hint("This schedule is kept as written. The controls can't express it.");
        }
        _ => {}
    }
    if let Some(replaced) = &f.replaced {
        field = field.control(
            Inline::loose()
                .key(key("schedule-replaced"))
                .child(words(
                    format!("Replaced {replaced}."),
                    Type::Small,
                    t.text_tertiary,
                ))
                .child(
                    Button::new("Undo")
                        .size(Size::Compact)
                        .look(Look::Quiet)
                        .on_press(super::undo_replace)
                        .build(t)
                        .key(key("schedule-undo"))
                        .into(),
                ),
        );
    }
    let cron = TextInput::new(&f.cron)
        .key(key("cron"))
        .label("Cron")
        .spellcheck(false)
        .on_edit(|s: &mut State| field_of(s, |f| &mut f.cron))
        .on_change(|state: &mut State, tx: &mut Tx| {
            if let Some(f) = form_mut(state) {
                f.replaced = None;
            }
            super::follow_preview(state, tx);
        });
    let mut out = vec![
        field.error(f.preview.error.clone()).build(t),
        FieldBlock::new(key("cron"), "Cron")
            .control(code_box(cron, state_of(f, f.preview.error.is_some()), t))
            .build(t),
    ];
    out.extend(next_runs(f, t));
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_next_run_reads_on_its_own_clock_as_the_tauri_app_writes_it() {
        assert_eq!(
            long_date("2026-11-17T09:00:00+01:00").as_deref(),
            Some("Tue 17 Nov at 09:00")
        );
        assert_eq!(
            long_date("2026-03-01T23:30:00-05:00").as_deref(),
            Some("Sun 1 Mar at 23:30")
        );
    }

    #[test]
    fn until_rounds_to_minutes_then_hours_then_days() {
        let m = 60_000;
        for (ahead, said) in [
            (0, "in 0m"),
            (59 * m, "in 59m"),
            (90 * m, "in 2h"),
            (47 * 60 * m, "in 47h"),
            (49 * 60 * m, "in 2d"),
        ] {
            assert_eq!(until(ahead, 0), said);
        }
    }
}
