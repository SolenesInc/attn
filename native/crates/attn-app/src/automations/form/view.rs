use std::rc::Rc;

use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::{space, type_};
use rune::{Align, El, Key, KeyEvent, Side, TextEdit, Tx, col, row, text};
use rune_kit::{
    Button, ButtonVariant, Checkbox, ControlSize, PopupItem, PopupList, SegmentedControl, Select,
    TextArea, TextInput,
};

use super::model::{CatchUp, Continuity, Field, Location, Trigger};
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
/// attn checks schedules once a minute.
const SCHEDULER_TICK_MS: i64 = 60_000;

fn kit(t: &Tokens) -> &'static rune_kit::Kit {
    attn_ui::kit(t)
}

fn key(name: &str) -> String {
    format!("{KEY}.{name}")
}

fn hint(words: impl Into<String>, t: &Tokens) -> El {
    text(words.into())
        .font(type_::LABEL)
        .color(t.text_tertiary)
        .wrap()
}

fn error(field_key: String, words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .key(format!("{field_key}.error"))
        .role("alert")
        .font(type_::LABEL)
        .color(t.error)
        .wrap()
}

fn section(label: &str, t: &Tokens) -> El {
    col().w_full().gap(space::S2).child(
        text(label.to_owned())
            .font(type_::SECTION)
            .color(t.text_tertiary),
    )
}

fn field_error(f: &Form, field: Field, t: &Tokens) -> Option<El> {
    f.errors
        .get(&field)
        .map(|words| error(field_key(field), words, t))
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
    let checked = super::field_of_key(&field_key);
    let invalid = checked.is_some_and(|which| f.errors.keys().any(which));
    TextInput::new(edit)
        .key(field_key)
        .label(label)
        .placeholder(placeholder)
        .invalid(invalid)
        .spellcheck(false)
        .disabled(f.saving)
        .on_edit(on_edit)
        .on_change(move |state: &mut State, _: &mut Tx| {
            if let (Some(f), Some(which)) = (form_mut(state), checked) {
                f.edited(which);
            }
        })
        .on_submit(super::save)
        .build(kit(t))
        .w_full()
}

fn edit_of(state: &mut State) -> &mut Form {
    form_mut(state).expect("the form shows")
}

pub(crate) fn view(state: &State, f: &Form, t: &Tokens) -> El {
    let title = if f.id.is_some() {
        "Edit automation"
    } else {
        "New automation"
    };
    let mut header = row()
        .key(key("header"))
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S3)
        .py(space::S2)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            text(title)
                .font(type_::TITLE)
                .color(t.text_primary)
                .grow(1.0),
        );
    if let (Some(enabled), Load::Ready) = (f.enabled, &f.load) {
        header = header.child(
            rune_kit::Switch::new(enabled)
                .label(if enabled { "Enabled" } else { "Disabled" })
                .control_size(ControlSize::Mini)
                .on_toggle_tx(super::toggle_enabled)
                .build(kit(t))
                .key(key("enabled")),
        );
    }
    header = header.child(
        GlyphButton::new(icon::CLOSE, "Close")
            .on_press_tx(super::close)
            .build(kit(t))
            .key(key("close")),
    );
    let body = match &f.load {
        Load::Loading => text("Loading…").font(type_::BODY).color(t.text_secondary),
        Load::Failed(why) => error(key("load"), why, t),
        Load::Ready => fields(state, f, t),
    };
    let mut form_el = col()
        .key(KEY)
        .role("form")
        .label(title)
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(header)
        .child(
            col()
                .key(key("body"))
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .gap(space::S3)
                .padding(space::S3)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .child(body),
        );
    if f.load == Load::Ready {
        form_el = form_el.child(actions(f, t));
    }
    form_el
}

fn fields(state: &State, f: &Form, t: &Tokens) -> El {
    let mut body = col().w_full().gap(space::S3);
    if f.saving {
        body = body.child(hint("Saving…", t).key(key("saving")));
    }
    body.child(name(f, t))
        .child(trigger(f, t))
        .child(launch(state, f, t))
        .child(prompt(f, t))
        .child(advanced(f, t))
}

fn name(f: &Form, t: &Tokens) -> El {
    let mut s = section("Name", t)
        .child(input(
            f,
            &f.name,
            field_key(Field::Name),
            "Name",
            "Automation name",
            |s| &mut edit_of(s).name,
            t,
        ))
        .children(field_error(f, Field::Name, t));
    if let Some(id) = f.id {
        s = s.child(hint(format!("ID: {id}"), t).key(key("id")));
    }
    s
}

fn trigger(f: &Form, t: &Tokens) -> El {
    let cards = [
        (Trigger::Manual, "manual", "Manual"),
        (Trigger::Scheduled, "scheduled", "Scheduled"),
        (Trigger::GitHub, "github", "PR review requested"),
    ];
    let mut s = section("Trigger", t).child(
        row()
            .key(key("triggers"))
            .role("group")
            .label("Trigger")
            .w_full()
            .gap(space::S2)
            .children(cards.into_iter().map(|(trigger, id, label)| {
                Button::new(label)
                    .variant(if f.trigger == trigger {
                        ButtonVariant::Contrast
                    } else {
                        ButtonVariant::Outline
                    })
                    .control_size(ControlSize::Small)
                    .disabled(f.saving)
                    .on_press(move |state: &mut State| super::choose_trigger(state, trigger))
                    .build(kit(t))
                    .key(key(&format!("trigger-{id}")))
                    .checked(f.trigger == trigger)
                    .grow(1.0)
            })),
    );
    match f.trigger {
        Trigger::Manual => {
            s = s.child(hint("Fresh worker each run", t));
            if f.location == Location::Directory {
                s = s.child(directory(f, t));
            }
        }
        Trigger::Scheduled => {
            s = s
                .child(schedule(f, t))
                .child(
                    col()
                        .w_full()
                        .gap(space::S1)
                        .child(input(
                            f,
                            &f.guard,
                            key("guard"),
                            "Only run if",
                            "git rev-parse HEAD",
                            |s| &mut edit_of(s).guard,
                            t,
                        ))
                        .child(hint(
                            "Runs in the folder before each scheduled run. The agent starts only when the command prints something new.",
                            t,
                        )),
                )
                .child(directory(f, t));
        }
        Trigger::GitHub => s = s.child(github(f, t)),
    }
    s
}

fn directory(f: &Form, t: &Tokens) -> El {
    col()
        .w_full()
        .gap(space::S1)
        .child(text("Folder").font(type_::LABEL).color(t.text_secondary))
        .child(
            row()
                .w_full()
                .gap(space::S2)
                .items_center()
                .child(
                    input(
                        f,
                        &f.directory,
                        field_key(Field::DirectoryPath),
                        "Folder",
                        "/absolute/path",
                        |s| &mut edit_of(s).directory,
                        t,
                    )
                    .grow(1.0),
                )
                .child(
                    Button::new("Browse")
                        .control_size(ControlSize::Small)
                        .disabled(f.saving)
                        .on_press_tx(|_: &mut State, tx: &mut Tx| super::browse(tx))
                        .build(kit(t))
                        .key(key("browse")),
                ),
        )
        .children(field_error(f, Field::DirectoryPath, t))
}

fn repository_list(f: &Form, exclude: bool, t: &Tokens) -> El {
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
        let invalid = f.errors.contains_key(&field(i));
        row()
            .key(key(&format!("repositories-{name}-chip.{i}")))
            .gap(space::S1)
            .items_center()
            .px(space::S2)
            .radius(4.0)
            .fill(t.bg_elevated)
            .child(
                text(repository.clone())
                    .font(type_::LABEL)
                    .color(if invalid { t.error } else { t.text_primary }),
            )
            .child(
                GlyphButton::new(icon::CLOSE, format!("Remove {repository}"))
                    .side(18.0)
                    .on_press(move |state: &mut State| {
                        super::remove_repository(state, exclude, i);
                    })
                    .build(kit(t))
                    .key(key(&format!("repositories-{name}-remove.{i}"))),
            )
    });
    let field_input = TextInput::new(draft)
        .key(input_key)
        .label(label)
        .placeholder("host/owner/repository")
        .spellcheck(false)
        .disabled(f.saving)
        .on_edit(move |state: &mut State| {
            let f = edit_of(state);
            if exclude {
                &mut f.exclude_draft
            } else {
                &mut f.include_draft
            }
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
        })
        .build(kit(t))
        .w_full();
    let errors = f
        .errors
        .iter()
        .filter(|(fl, _)| (0..list.len()).any(|i| **fl == field(i)))
        .map(|(_, words)| words.clone())
        .collect::<std::collections::BTreeSet<_>>();
    col()
        .w_full()
        .gap(space::S1)
        .child(text(label).font(type_::LABEL).color(t.text_secondary))
        .child(row().w_full().gap(space::S1).wrap().children(chips))
        .child(field_input)
        .children(
            errors
                .into_iter()
                .map(|words| error(key(&format!("repositories-{name}")), &words, t)),
        )
}

fn github(f: &Form, t: &Tokens) -> El {
    col()
        .w_full()
        .gap(space::S2)
        .child(repository_list(f, false, t))
        .child(repository_list(f, true, t))
        .child(hint("One reviewer per PR — later request cycles return to it", t))
        .child(hint("Existing requests are left alone when enabled", t))
        .child(hint("Missed while attn was off: latest request still runs", t))
        .child(hint(
            "Reviews always run in a fresh worktree checked out at the PR's head commit — your existing clone is never touched.",
            t,
        ))
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
    let f = form(state)?;
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

fn launch(state: &State, f: &Form, t: &Tokens) -> El {
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
    let mut s = section("Runs as", t)
        .child(crate::launch_desktop::field(state, t, &desktop))
        .child(crate::settings::route_chip(
            state,
            t,
            route_target(),
            "Automation model",
        ));
    if overridden(f) {
        s = s.child(hint(
            "Use model and effort IDs accepted by the executable override.",
            t,
        ));
    }
    s.child(hint(
        "Automation sessions always run unattended with the agent's automatic approval mode.",
        t,
    ))
}

fn prompt(f: &Form, t: &Tokens) -> El {
    let checked = super::field_of_key(&field_key(Field::Prompt));
    section("Prompt", t)
        .child(
            TextArea::new(&f.prompt)
                .key(field_key(Field::Prompt))
                .label("Prompt")
                .rows(6)
                .spellcheck(true)
                .on_edit(|s: &mut State| &mut edit_of(s).prompt)
                .on_change(move |state: &mut State, _: &mut Tx| {
                    if let (Some(f), Some(which)) = (form_mut(state), checked) {
                        f.edited(which);
                    }
                })
                .build(kit(t))
                .invalid(f.errors.contains_key(&Field::Prompt))
                .w_full(),
        )
        .child(hint(
            "This is the instruction the agent receives. Trigger details arrive separately as structured context — they can never rewrite this prompt.",
            t,
        ))
        .children(field_error(f, Field::Prompt, t))
}

fn segments<T: Copy + PartialEq + 'static>(
    name: &str,
    label: &str,
    current: T,
    options: &[(T, &str, &str)],
    pick: fn(&mut Form, T),
    t: &Tokens,
) -> El {
    let labels: Vec<&str> = options.iter().map(|(_, _, l)| *l).collect();
    let values: Vec<T> = options.iter().map(|(v, ..)| *v).collect();
    SegmentedControl::new(&labels, options.iter().position(|(v, ..)| *v == current))
        .label(label)
        .fit()
        .on_pick(move |state: &mut State, i: usize, _: &mut Tx| {
            if let Some(f) = form_mut(state) {
                pick(f, values[i]);
            }
        })
        .build(kit(t))
        .key(key(name))
}

fn advanced_chips(f: &Form) -> Vec<String> {
    let mut chips = Vec::new();
    if f.trigger == Trigger::Scheduled && f.continuity == Continuity::Singleton {
        chips.push("Same agent".into());
    }
    if f.trigger == Trigger::Scheduled && f.catch_up == CatchUp::Skip {
        chips.push("Skip missed runs".into());
    }
    if f.trigger == Trigger::Manual && f.location == Location::Worktree {
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

fn advanced(f: &Form, t: &Tokens) -> El {
    let failed = f.errors.keys().any(|field| {
        matches!(
            field,
            Field::CatchUp | Field::OverrideRepository(_) | Field::OverridePath(_)
        )
    });
    let mut summary = row().gap(space::S2).items_center().child(
        text("Advanced")
            .font(type_::LABEL)
            .color(if failed { t.error } else { t.text_secondary }),
    );
    let chips = advanced_chips(f);
    for chip in &chips {
        summary = summary.child(text(chip.clone()).font(type_::LABEL).color(t.text_tertiary));
    }
    let toggle = Button::new("Advanced")
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label_with(move |_, _| summary)
        .expanded(f.advanced)
        .on_press(|state: &mut State| {
            if let Some(f) = form_mut(state) {
                f.advanced = !f.advanced;
            }
        })
        .build(kit(t))
        .key(key("advanced"))
        .description(chips.join(", "));
    let mut s = col().w_full().gap(space::S2).child(toggle);
    if !f.advanced {
        return s;
    }
    if f.trigger == Trigger::Scheduled {
        s = s
            .child(segments(
                "continuity",
                "Agent",
                f.continuity,
                &[
                    (Continuity::Fresh, "fresh", "New each run"),
                    (Continuity::Singleton, "singleton", "Same agent"),
                ],
                |f, v| f.continuity = v,
                t,
            ))
            .child(hint(
                match f.continuity {
                    Continuity::Fresh => "Every run starts a new agent.",
                    Continuity::Singleton => {
                        "Every run goes to one agent, and it resumes if closed. Changing the prompt, model or folder starts a replacement."
                    }
                },
                t,
            ))
            .child(segments(
                "catchup",
                "Missed runs",
                f.catch_up,
                &[
                    (CatchUp::Latest, "latest", "Run latest"),
                    (CatchUp::Skip, "skip", "Skip"),
                ],
                |f, v| {
                    f.catch_up = v;
                    f.check(|field| *field == Field::CatchUp);
                },
                t,
            ))
            .child(hint(
                match f.catch_up {
                    CatchUp::Skip => {
                        "A missed time still runs if attn notices within 5 minutes. Older ones are dropped."
                    }
                    _ => "After downtime, one run for the most recent missed time.",
                },
                t,
            ))
            .children(field_error(f, Field::CatchUp, t));
    }
    if f.trigger == Trigger::Manual {
        s = s
            .child(segments(
                "location",
                "Works in",
                f.location,
                &[
                    (Location::Directory, "directory", "Folder"),
                    (Location::Worktree, "worktree", "Pull request worktree"),
                ],
                |f, v| f.location = v,
                t,
            ))
            .child(hint(
                match f.location {
                    Location::Directory => "The agent works in the folder above.",
                    Location::Worktree => {
                        "Run now asks for a pull request, and the agent works in a fresh worktree at its head."
                    }
                },
                t,
            ));
    }
    if f.values().uses_worktree() {
        s = s.child(overrides(f, t));
    }
    if f.agent != "codex" {
        s = s.child(input(
            f,
            &f.executable,
            key("executable"),
            "Executable",
            "Default from PATH",
            |s| &mut edit_of(s).executable,
            t,
        ));
    }
    if !f.yaml.is_empty() {
        s = s.child(definition(f, t));
    }
    s
}

fn overrides(f: &Form, t: &Tokens) -> El {
    let rows = f
        .overrides
        .iter()
        .enumerate()
        .map(|(i, (repository, path))| {
            let repo_key = field_key(Field::OverrideRepository(i));
            let path_key = field_key(Field::OverridePath(i));
            col()
                .w_full()
                .gap(space::S1)
                .child(
                    row()
                        .key(key(&format!("override.{i}")))
                        .w_full()
                        .gap(space::S2)
                        .items_center()
                        .child(
                            TextInput::new(repository)
                                .key(repo_key)
                                .label("Repository")
                                .placeholder("host/owner/repository")
                                .spellcheck(false)
                                .invalid(f.errors.contains_key(&Field::OverrideRepository(i)))
                                .on_edit(move |s: &mut State| &mut edit_of(s).overrides[i].0)
                                .build(kit(t))
                                .grow(1.0),
                        )
                        .child(
                            TextInput::new(path)
                                .key(path_key)
                                .label("Path")
                                .placeholder("/absolute/path")
                                .spellcheck(false)
                                .invalid(f.errors.contains_key(&Field::OverridePath(i)))
                                .on_edit(move |s: &mut State| &mut edit_of(s).overrides[i].1)
                                .build(kit(t))
                                .grow(1.0),
                        )
                        .child(
                            Button::new("Remove")
                                .variant(ButtonVariant::GhostMuted)
                                .control_size(ControlSize::Small)
                                .on_press(move |state: &mut State| super::remove_override(state, i))
                                .build(kit(t))
                                .key(key(&format!("overrides-remove.{i}"))),
                        ),
                )
                .children(field_error(f, Field::OverrideRepository(i), t))
                .children(field_error(f, Field::OverridePath(i), t))
        });
    col()
        .w_full()
        .gap(space::S2)
        .child(
            text("Local clones")
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .children(rows)
        .child(
            Button::new("Add override")
                .control_size(ControlSize::Small)
                .on_press_tx(super::add_override)
                .build(kit(t))
                .key(key("overrides-add")),
        )
}

fn definition(f: &Form, t: &Tokens) -> El {
    let mut s = row().gap(space::S2).child(
        Button::new(if f.definition_shown {
            "Hide definition"
        } else {
            "Show definition"
        })
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .expanded(f.definition_shown)
        .on_press(|state: &mut State| {
            if let Some(f) = form_mut(state) {
                f.definition_shown = !f.definition_shown;
            }
        })
        .build(kit(t))
        .key(key("definition-toggle")),
    );
    if f.definition_shown {
        let yaml = f.yaml.clone();
        s = s.child(
            Button::new(if f.copied { "Copied" } else { "Copy" })
                .variant(ButtonVariant::GhostMuted)
                .control_size(ControlSize::Small)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    tx.copy(yaml.clone());
                    if let Some(f) = form_mut(state) {
                        f.copied = true;
                    }
                })
                .build(kit(t))
                .key(key("definition-copy")),
        );
    }
    let mut c = col().w_full().gap(space::S1).child(s);
    if f.definition_shown {
        c = c.child(
            text(f.yaml.clone())
                .key(key("definition"))
                .font(type_::MONO_CAPTION)
                .color(t.text_secondary)
                .wrap(),
        );
    }
    c
}

fn actions(f: &Form, t: &Tokens) -> El {
    let mut c = col()
        .key(key("actions"))
        .w_full()
        .gap(space::S2)
        .px(space::S3)
        .py(space::S2)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle);
    if f.stale {
        c = c.child(
            col()
                .key(key("stale"))
                .role("alert")
                .gap(space::S1)
                .child(hint(
                    "This automation changed elsewhere while you were editing. Reload to pick up the latest revision — your unsaved edits will be replaced.",
                    t,
                ))
                .child(
                    Button::new("Reload")
                        .control_size(ControlSize::Small)
                        .on_press_tx(super::reload)
                        .build(kit(t))
                        .key(key("reload")),
                ),
        );
    } else if let Some(why) = &f.save_error {
        c = c.child(error(key("save"), why, t));
    }
    let mut bar = row().w_full().gap(space::S2).items_center();
    if f.id.is_some() {
        bar = bar.child(
            Button::new(if f.delete_armed {
                "Confirm delete"
            } else {
                "Delete"
            })
            .variant(ButtonVariant::Danger)
            .control_size(ControlSize::Small)
            .on_press_tx(super::delete)
            .build(kit(t))
            .key(DELETE),
        );
        if f.delete_armed {
            bar = bar.child(hint("Existing seeds and run history are kept.", t));
        }
    }
    bar = bar
        .child(row().grow(1.0))
        .child(
            Button::new("Cancel")
                .variant(ButtonVariant::GhostMuted)
                .control_size(ControlSize::Small)
                .on_press_tx(super::close)
                .build(kit(t))
                .key(key("cancel")),
        )
        .child(
            Button::new(if f.saving { "Saving…" } else { "Save" })
                .variant(ButtonVariant::Submit)
                .control_size(ControlSize::Small)
                .disabled(f.save_blocked())
                .shortcut("mod+enter")
                .on_press_tx(super::save)
                .build(kit(t))
                .key(key("save")),
        );
    c.child(bar)
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

fn time_choices(name: &str, schedule: &Friendly, hour: u32, minute: u32) -> [Choice; 2] {
    [
        Choice {
            key: key(&format!("schedule-{name}-hour")),
            label: "Hour",
            options: (0..24).map(pad).collect(),
            current: hour as usize,
            pick: change(schedule, |s, i| match s {
                Friendly::Days { hour, .. } | Friendly::Monthly { hour, .. } => *hour = i as u32,
                Friendly::Every { .. } => {}
            }),
        },
        Choice {
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
        },
    ]
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
        Friendly::Days { hour, minute, .. } => {
            time_choices("days", schedule, *hour, *minute).into()
        }
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
fn zone_keys(state: &mut State, ev: &KeyEvent, _: &mut Tx) -> bool {
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
            super::choose_zone(state, zone);
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
        .align(Align::End)
        .items(zones.iter().map(|zone| PopupItem::new(*zone, *zone)))
        .active(active)
        .on_active(|state: &mut State, i| {
            edit_of(state).zone_suggestions = ZoneSuggestions::Shown { active: Some(i) }
        })
        .on_choose(|state: &mut State, zone: &str, _: &mut Tx| super::choose_zone(state, zone))
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
        Button::new(name)
            .variant(if on {
                ButtonVariant::Contrast
            } else {
                ButtonVariant::Outline
            })
            .control_size(ControlSize::Small)
            .label_with(move |font, ink| text(letter).font(font).color(ink))
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
            .build(kit(t))
            .key(key(&format!("schedule-day.{day}")))
            .checked(on)
    });
    let preset = |label: &'static str, name: &str, days: &[u8]| {
        let schedule = schedule.clone();
        let days = days.to_vec();
        Button::new(label)
            .variant(ButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .on_press(move |state: &mut State| {
                super::set_schedule(state, with_days(&schedule, days.clone()));
            })
            .build(kit(t))
            .key(key(name))
    };
    row()
        .w_full()
        .gap(space::S1)
        .items_center()
        .wrap()
        .child(row().role("group").label("Days").gap(2.0).children(buttons))
        .child(preset("Every day", "schedule-every-day", &ALL_DAYS))
        .child(preset("Weekdays", "schedule-weekdays", &WEEKDAYS))
}

fn friendly_controls(f: &Form, schedule: &Friendly, t: &Tokens) -> El {
    let k = kit(t);
    let mut triggers = choices(schedule)
        .into_iter()
        .map(|c| (c.key.clone(), select(f, c).trigger(k)));
    let mut next = || triggers.next().map(|(_, el)| el).unwrap_or_else(row);
    let words = |w: &str| {
        text(w.to_owned())
            .font(type_::LABEL)
            .color(t.text_secondary)
    };
    let line = || row().w_full().gap(space::S2).items_center().wrap();
    match schedule {
        Friendly::Every { unit, window, .. } => {
            let mut first = line().child(words("Every")).child(next()).child(next());
            if *unit == Unit::Hours {
                first = first.child(words("at minute")).child(next());
            }
            let on = window.is_some();
            let schedule_now = schedule.clone();
            let mut second = line().child(
                Checkbox::new(on)
                    .label("Only between")
                    .on_toggle(move |state: &mut State| {
                        let mut s = schedule_now.clone();
                        if let Friendly::Every { window, .. } = &mut s {
                            *window = if on { None } else { Some((9, 17)) };
                        }
                        super::set_schedule(state, s);
                    })
                    .build(k)
                    .key(key("schedule-window")),
            );
            if on {
                second = second.child(next()).child(words("and")).child(next());
            }
            col()
                .w_full()
                .gap(space::S2)
                .child(first)
                .child(second)
                .child(day_buttons(schedule, t))
        }
        Friendly::Days { .. } => col()
            .w_full()
            .gap(space::S2)
            .child(day_buttons(schedule, t))
            .child(
                line()
                    .child(words("at"))
                    .child(next())
                    .child(words(":"))
                    .child(next()),
            ),
        Friendly::Monthly { day, .. } => {
            let mut c = col().w_full().gap(space::S2).child(
                line()
                    .child(words("On day"))
                    .child(next())
                    .child(words("at"))
                    .child(next())
                    .child(words(":"))
                    .child(next()),
            );
            if *day > 28 {
                c = c.child(hint("Months without this day are skipped.", t));
            }
            c
        }
    }
}

/// "Tue 17 Nov, 09:00" for an RFC 3339 time, read on the clock its offset names.
fn long_date(time: &str) -> Option<String> {
    let date = time.get(..10)?;
    let day = crate::calendar::day_of_date(date)?;
    let weekday = WEEKDAY_NAMES[crate::calendar::weekday(day) as usize];
    let month: usize = time.get(5..7)?.parse().ok()?;
    let dom: u32 = time.get(8..10)?.parse().ok()?;
    let clock = time.get(11..16)?;
    Some(format!(
        "{weekday} {dom} {}, {clock}",
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

fn next_runs(f: &Form, t: &Tokens) -> El {
    let zone = f.time_zone.text.trim();
    let elsewhere = zone != super::local_zone();
    let times: Vec<(String, i64)> = f
        .preview
        .times
        .iter()
        .filter_map(|time| Some((time.clone(), crate::calendar::ms_of_rfc3339(time)?)))
        .collect();
    let mut c = col().key(key("next-runs")).w_full().gap(space::S1).child(
        row()
            .w_full()
            .gap(space::S2)
            .items_center()
            .child(
                text(if f.guard.text.trim().is_empty() {
                    "Next runs"
                } else {
                    "Next checks"
                })
                .font(type_::LABEL)
                .color(t.text_secondary)
                .grow(1.0),
            )
            .child(
                TextInput::new(&f.time_zone)
                    .key(TIME_ZONE)
                    .label("Time zone")
                    .spellcheck(false)
                    .active_descendant(active_zone(f).map(zone_option_key))
                    .on_edit(|s: &mut State| &mut edit_of(s).time_zone)
                    .on_change(|s: &mut State, _: &mut Tx| {
                        edit_of(s).zone_suggestions = ZoneSuggestions::Shown { active: None };
                    })
                    .on_key(zone_keys)
                    .build(kit(t))
                    .w(200.0),
            ),
    );
    if let [first, second, ..] = times.as_slice()
        && second.1 - first.1 < SCHEDULER_TICK_MS
    {
        c = c.child(hint(
            "attn checks schedules once a minute, so this runs at most once a minute.",
            t,
        ));
    }
    let list = col()
        .role("list")
        .w_full()
        .gap(2.0)
        .children(times.iter().map(|(time, ms)| {
            let mut when = long_date(time).unwrap_or_else(|| time.clone());
            if elsewhere {
                when = format!("{when} ({} here)", here(*ms));
            }
            row()
                .role("listitem")
                .w_full()
                .gap(space::S2)
                .child(
                    text(when)
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .grow(1.0),
                )
                .child(
                    text(until(*ms, f.preview.at_ms))
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                )
        }));
    c.child(list)
}

fn schedule(f: &Form, t: &Tokens) -> El {
    let (tab, friendly) = f.schedule();
    let tabs: Vec<&str> = Tab::ALL.iter().map(|(_, _, label)| *label).collect();
    let mut c = col().key(key("schedule")).w_full().gap(space::S2).child(
        SegmentedControl::new(&tabs, Tab::ALL.iter().position(|(t, ..)| *t == tab))
            .label("Schedule type")
            .fit()
            .on_pick(|state: &mut State, i: usize, _: &mut Tx| {
                super::choose_tab(state, Tab::ALL[i].0);
            })
            .build(kit(t))
            .key(key("schedule-tabs")),
    );
    match (&friendly, tab) {
        (Some(schedule), tab) if tab != Tab::Cron => c = c.child(friendly_controls(f, schedule, t)),
        (None, _) if !f.cron.text.trim().is_empty() => {
            c = c.child(hint(
                "This schedule is kept as written. The controls can't express it.",
                t,
            ));
        }
        _ => {}
    }
    if let Some(replaced) = &f.replaced {
        c = c.child(
            row()
                .key(key("schedule-replaced"))
                .gap(space::S2)
                .items_center()
                .child(hint(format!("Replaced {replaced}."), t))
                .child(
                    Button::new("Undo")
                        .variant(ButtonVariant::GhostMuted)
                        .control_size(ControlSize::Small)
                        .on_press(super::undo_replace)
                        .build(kit(t))
                        .key(key("schedule-undo")),
                ),
        );
    }
    c = c.child(
        TextInput::new(&f.cron)
            .key(key("cron"))
            .label("Cron")
            .spellcheck(false)
            .invalid(f.preview.error.is_some())
            .on_edit(|s: &mut State| &mut edit_of(s).cron)
            .on_change(|state: &mut State, _: &mut Tx| {
                if let Some(f) = form_mut(state) {
                    f.replaced = None;
                }
            })
            .build(kit(t))
            .w_full(),
    );
    if let Some(why) = &f.preview.error {
        c = c.child(error(key("schedule"), why, t));
    }
    c.child(next_runs(f, t))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_next_run_reads_on_its_own_clock_as_the_tauri_app_writes_it() {
        assert_eq!(
            long_date("2026-11-17T09:00:00+01:00").as_deref(),
            Some("Tue 17 Nov, 09:00")
        );
        assert_eq!(
            long_date("2026-03-01T23:30:00-05:00").as_deref(),
            Some("Sun 1 Mar, 23:30")
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
