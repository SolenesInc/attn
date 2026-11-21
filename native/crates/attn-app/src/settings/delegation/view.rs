//! The Delegation page: the roles table with its model chips, the role and alternative editors,
//! the fallback, adoption of the maintained roles, undo, and the switch in the page head.

use std::collections::BTreeMap;

use attn_protocol::{DelegationChoice, DelegationPreferences, DelegationRole};
use attn_ui::Tokens;
use attn_ui::blocks::{
    Actions, Block, Column, Empty, Field, Inline, Notice, Prose, Section, SettingRow, Status,
    SubRow, Table, TableColumns, TableRow,
};
use attn_ui::system::{self, Control, IconSize, Look, Size, Tone, Type};
use rune::{El, Icon, TextEdit, Tx};
use rune_kit::{Button, ButtonVariant, ControlSize, Select, TextArea, TextInput};

use super::table::{self, NEW, Slot};
use super::{Delegation, commit, commit_undoable, page_state, page_state_mut, persist};
use crate::State;
use crate::settings::harnesses;
use crate::settings::route::{self, Route, RouteHarness, Rules, Target};
use crate::settings::sections::Section as Page;

/// The "Runs on" column: as wide as the widest route chip. "Claude Code · Opus 4.5 · xhigh"
/// measures 273.5 drawn, and "Codex · gpt-5.1-codex-max · xhigh" 247.5.
const RUNS_ON: f32 = 280.0;
const NOT_SAID: &str = "No condition yet. The agent cannot pick this.";
const DESCRIPTION_HINT: &str =
    "Prose, as long as it needs to be. The first line is what the table shows.";

macro_rules! stroke {
    () => {
        r#" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round""#
    };
}

static SEARCH: Icon = Icon {
    name: "delegation-search",
    view: 16.0,
    body: concat!(
        r#"<circle cx="7" cy="7" r="4""#,
        stroke!(),
        "/>",
        r#"<path d="m10 10 4 4""#,
        stroke!(),
        "/>"
    ),
};
static DIAMOND: Icon = Icon {
    name: "delegation-diamond",
    view: 16.0,
    body: concat!(r#"<path d="m8 1 7 7-7 7-7-7Z""#, stroke!(), "/>"),
};
static CODE: Icon = Icon {
    name: "delegation-code",
    view: 16.0,
    body: concat!(
        r#"<path d="m5 4-4 4 4 4m6-8 4 4-4 4M9 2 7 14""#,
        stroke!(),
        "/>"
    ),
};
static ARROW: Icon = Icon {
    name: "delegation-arrow",
    view: 16.0,
    body: concat!(r#"<path d="M3 13 13 3M4 3h9v9""#, stroke!(), "/>"),
};
static LIST: Icon = Icon {
    name: "delegation-list",
    view: 16.0,
    body: concat!(
        r#"<path d="M6 4h8M6 8h8M6 12h8M2 4h1M2 8h1M2 12h1""#,
        stroke!(),
        "/>"
    ),
};
static BUG: Icon = Icon {
    name: "delegation-bug",
    view: 16.0,
    body: concat!(
        r#"<rect x="4" y="5" width="8" height="9" rx="4""#,
        stroke!(),
        "/>",
        r#"<path d="m5 2 2 3m4-3-2 3M1 7h3m8 0h3M1 11h3m8 0h3M8 6v7""#,
        stroke!(),
        "/>"
    ),
};
static SPARK: Icon = Icon {
    name: "delegation-spark",
    view: 16.0,
    body: concat!(
        r#"<path d="m8 1 2 5 5 2-5 2-2 5-2-5-5-2 5-2Z""#,
        stroke!(),
        "/>"
    ),
};
static CIRCLE: Icon = Icon {
    name: "delegation-circle",
    view: 16.0,
    body: concat!(r#"<circle cx="8" cy="8" r="6""#, stroke!(), "/>"),
};
static LAYERS: Icon = Icon {
    name: "delegation-layers",
    view: 16.0,
    body: concat!(
        r#"<path d="M8 2 14 5 8 8 2 5Z M2 8l6 3 6-3M2 11l6 3 6-3""#,
        stroke!(),
        "/>"
    ),
};

const ICONS: [(&str, &Icon); 9] = [
    ("search", &SEARCH),
    ("diamond", &DIAMOND),
    ("code", &CODE),
    ("arrow", &ARROW),
    ("list", &LIST),
    ("bug", &BUG),
    ("spark", &SPARK),
    ("circle", &CIRCLE),
    ("layers", &LAYERS),
];

fn role_glyph(name: &str, which: &str, t: &Tokens) -> El {
    match ICONS.iter().find(|(id, _)| *id == which) {
        Some((_, glyph)) => system::icon(glyph, IconSize::Regular, t.text_secondary).a11y_hidden(),
        None => {
            let initial = name
                .trim()
                .chars()
                .next()
                .map_or_else(|| "○".to_owned(), |c| c.to_uppercase().collect());
            system::words(initial, Type::Label, t.text_secondary).a11y_hidden()
        }
    }
}

fn plural(n: usize, word: &str) -> String {
    format!("{n} {word}{}", if n == 1 { "" } else { "s" })
}

fn lead_text(enabled: bool) -> &'static str {
    if enabled {
        "Roles guide agents that delegate. Each role has a model; an agent reads this table and picks the row that fits the work."
    } else {
        "Off. Agents that delegate pick harness and model on their own. Turn on to route them through this table."
    }
}

/// The Delegation switch in the page head, once the table is read.
pub(in crate::settings) fn pill(state: &State, t: &Tokens) -> Option<El> {
    if state.window.settings.section != Page::Delegation {
        return None;
    }
    let on = page_state(state).preferences.as_ref()?.enabled;
    Some(El::from(system::switch(
        "settings.delegation.switch",
        "Delegation preferences",
        on,
        system::Beside::State,
        false,
        t,
        |state: &mut State, tx: &mut Tx| {
            let Some(mut next) = page_state(state).preferences.clone() else {
                return;
            };
            next.enabled = !next.enabled;
            commit(state, next, tx);
        },
    )))
}

fn harness_rules(state: &State) -> Rules {
    Rules {
        harnesses: harnesses::listed(state)
            .iter()
            .map(RouteHarness::from)
            .collect(),
        allow_none: true,
        ..Rules::default()
    }
}

fn read_route(state: &State, id: &str) -> Option<(Route, Rules)> {
    let preferences = page_state(state).preferences.as_ref()?;
    let selection = Slot::parse(id)?.selection(preferences)?;
    Some((table::route(selection), harness_rules(state)))
}

fn write_route(state: &mut State, id: &str, route: Route, tx: &mut Tx) {
    let Some(preferences) = page_state(state).preferences.as_ref() else {
        return;
    };
    if let Some(next) = Slot::parse(id).and_then(|slot| slot.with(preferences, route)) {
        commit(state, next, tx);
    }
}

/// The table's columns, and the one a row's model goes in.
fn columns() -> (TableColumns, Column) {
    let mut columns = TableColumns::new("Role");
    let runs_on = columns.fixed("Runs on", RUNS_ON);
    (columns, runs_on)
}
const OFF: &str = "Off";
const NEEDS_A_MODEL: &str = "Needs a model";

fn role_mark(name: &str, which: &str, t: &Tokens) -> Control {
    system::icon_box(role_glyph(name, which, t), false, t)
}

fn button(
    label: impl Into<attn_ui::system::Said>,
    look: Look,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> Control {
    system::Button::new(label)
        .look(look)
        .on_press_tx(press)
        .build(t)
}

fn model_chip(state: &State, slot: Slot, label: &str, t: &Tokens) -> Option<Control> {
    route::chip(
        state,
        t,
        Target {
            id: slot.id(),
            read: read_route,
            write: write_route,
        },
        label,
    )
}

/// The page while the table is read, and after.
pub(in crate::settings) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let d = page_state(state);
    let Some(preferences) = &d.preferences else {
        return vec![loading(d, t)];
    };
    let read = state.window.settings.harnesses.read.as_ref();
    let expanded: &[DelegationRole] = read.map_or(&[], |r| r.expanded_roles.as_slice());
    let templates: &[DelegationRole] = read.map_or(&[], |r| r.templates.as_slice());
    let missing = table::missing_templates(preferences, templates);
    let missing_names: Vec<String> = missing
        .iter()
        .map(|t| table::label(table::view(expanded, t)))
        .collect();
    let mut out = vec![
        Prose::lead("settings.delegation.lead", lead_text(preferences.enabled))
            .quiet(!preferences.enabled)
            .build(t),
    ];
    if !d.error.is_empty() {
        out.push(Notice::error("settings.delegation.error", d.error.clone()).build(t));
    }
    let (columns, runs_on) = columns();
    let mut roles = Table::new("settings.delegation.table", columns)
        .head_key("settings.delegation.head")
        .rows(
            preferences
                .roles
                .iter()
                .map(|role| role_row(state, d, role, table::view(expanded, role), runs_on, t)),
        )
        .row(fallback_row(state, d, runs_on, t))
        .dimmed(!preferences.enabled);
    if preferences.roles.is_empty() && d.adoption.is_none() {
        roles = roles.empty(empty(!missing.is_empty(), t));
    }
    if !preferences.roles.is_empty() {
        roles = roles.footer(
            "settings.delegation.add-row",
            add_row(preferences, &missing_names, t),
        );
    }
    out.push(roles.build(t));
    if let Some(adoption) = &d.adoption {
        out.push(adoption_panel(
            state,
            preferences,
            &missing,
            &missing_names,
            adoption,
            t,
        ));
    }
    if let Some(undo) = d.live_undo() {
        out.push(
            Notice::info("settings.delegation.undo", format!("{}.", undo.label))
                .action(
                    system::Button::new("Undo")
                        .look(Look::Quiet)
                        .size(Size::Compact)
                        .on_press_tx(super::undo)
                        .build(t)
                        .key("settings.delegation.undo.button"),
                )
                .build(t),
        );
    }
    if !preferences.roles.is_empty() {
        out.push(foot(preferences, t));
    }
    out
}

fn loading(d: &Delegation, t: &Tokens) -> Block {
    if d.error.is_empty() {
        return Status::new(
            "settings.delegation.loading",
            "Loading delegation preferences…",
            false,
        )
        .build(t);
    }
    Notice::error("settings.delegation.loading", d.error.clone())
        .action(button("Retry", Look::Plain, t, super::reload).key("settings.delegation.retry"))
        .build(t)
}

fn role_row(
    state: &State,
    d: &Delegation,
    role: &DelegationRole,
    view: &DelegationRole,
    runs_on: Column,
    t: &Tokens,
) -> TableRow<State> {
    let id = role.id.clone();
    let open = d.expanded.as_deref() == Some(id.as_str());
    let alts = table::alternatives(role);
    let read_only = role.builtin.is_some();
    let key = format!("settings.delegation.role.{id}");
    let mut row = TableRow::new(key.clone(), view.name.clone(), toggle(id.clone()))
        .leading(role_mark(&view.name, &view.icon, t))
        .subtitle(table::first_line(&view.description))
        .more(format!("{} details", view.name))
        .off(!role.enabled)
        .open(open);
    if read_only {
        row = row.tag("Attn", Tone::Neutral);
    }
    if !role.enabled {
        row = row.tag(OFF, Tone::Neutral);
    }
    if table::needs_model(role) {
        row = row.tag(NEEDS_A_MODEL, Tone::Warn);
    }
    if let Some(chip) = table::default_choice(role).and_then(|main| {
        model_chip(
            state,
            Slot::Choice(id.clone(), main.id.clone()),
            &format!("Model for {}", view.name),
            t,
        )
    }) {
        row = row.control(runs_on, chip);
    }
    if !alts.is_empty() && !open {
        row = row.control(
            runs_on,
            system::expander(
                format!("{key}.alternatives"),
                format!("+{}", plural(alts.len(), "alternative")),
                false,
                false,
                t,
                toggle(id.clone()),
            ),
        );
    }
    if !open {
        return row;
    }
    row = if read_only {
        row.details(
            Field::new(format!("{key}.instructions"), "Instructions")
                .note("maintained by Attn, updates with releases")
                .value(view.instructions.clone())
                .value_key(format!("{key}.instructions"))
                .build(t),
        )
    } else {
        role_editor(state, role, t)
            .into_iter()
            .fold(row, TableRow::details)
    };
    for alt in alts {
        row = row.sub_row(alternative_row(state, d, role, view, alt, runs_on, t));
    }
    let add = {
        let id = id.clone();
        move |state: &mut State, tx: &mut Tx| {
            let Some(preferences) = page_state(state).preferences.clone() else {
                return;
            };
            if let Some((next, choice)) = table::added_alternative(&preferences, &id) {
                commit(state, next, tx);
                page_state_mut(state).expanded_alt = Some(choice);
            }
        }
    };
    row = row.sub_footer(
        runs_on,
        system::Button::new("+ Alternative model")
            .look(Look::Quiet)
            .size(Size::Compact)
            .on_press_tx(add)
            .build(t)
            .key(format!("{key}.add-alternative")),
    );
    row.actions(role_actions(role, view, &key, t))
}

fn role_actions(role: &DelegationRole, view: &DelegationRole, key: &str, t: &Tokens) -> Actions {
    let id = role.id.clone();
    let mut actions = Actions::new(format!("{key}.actions"));
    if role.builtin.is_some() {
        let id = id.clone();
        actions = actions.push(
            button(
                "Make an editable copy",
                Look::Plain,
                t,
                move |state: &mut State, tx: &mut Tx| {
                    let Some(preferences) = page_state(state).preferences.clone() else {
                        return;
                    };
                    let expanded = expanded_roles(state);
                    if let Some((next, copy)) = table::copied(&preferences, &expanded, &id) {
                        commit(state, next, tx);
                        expand(state, copy);
                    }
                },
            )
            .key(format!("{key}.copy")),
        );
    }
    let turn = if role.enabled { "Turn off" } else { "Turn on" };
    let turned = id.clone();
    let name = view.name.clone();
    actions
        .then_end()
        .push(
            button(
                turn,
                Look::Quiet,
                t,
                move |state: &mut State, tx: &mut Tx| {
                    let Some(preferences) = page_state(state).preferences.clone() else {
                        return;
                    };
                    commit(state, table::toggled(&preferences, &turned), tx);
                },
            )
            .key(format!("{key}.turn")),
        )
        .push(
            button(
                "Delete",
                Look::Danger,
                t,
                move |state: &mut State, tx: &mut Tx| {
                    let Some(preferences) = page_state(state).preferences.clone() else {
                        return;
                    };
                    commit_undoable(
                        state,
                        table::deleted(&preferences, &id),
                        format!("Deleted {name}"),
                        tx,
                    );
                    page_state_mut(state).expanded = None;
                },
            )
            .key(format!("{key}.delete")),
        )
}

/// A text the table keeps: it shows the table's text until the first edit, and saves the draft
/// when the focus leaves it, or on Enter in a single line.
fn text_field(
    state: &State,
    field: table::Field,
    label: &'static str,
    placeholder: &str,
    hint: Option<&'static str>,
    t: &Tokens,
) -> Block {
    let d = page_state(state);
    let key = field.key();
    let mut shown = TextEdit::new(
        d.preferences
            .as_ref()
            .map(|p| field.value(p))
            .unwrap_or_default(),
    );
    let draft = d.drafts.get(&key);
    shown.set_dragged_height(draft.and_then(|draft| draft.edit.dragged_height()));
    let edit = draft
        .filter(|draft| draft.edit.focused)
        .map_or(&shown, |draft| &draft.edit);
    let left = key.clone();
    let editing = field.clone();
    let control = if field.single_line() {
        let submitted = key.clone();
        system::line_box(
            TextInput::new(edit)
                .key(key.clone())
                .label(label.to_owned())
                .placeholder(placeholder.to_owned())
                .on_edit(move |state: &mut State| super::draft_mut(state, &editing))
                .on_submit(move |state: &mut State, tx: &mut Tx| {
                    super::commit_draft(state, &submitted, tx);
                    tx.blur();
                })
                .on_blur(move |state: &mut State, tx: &mut Tx| {
                    super::commit_draft(state, &left, tx)
                }),
            system::FieldState::Ready,
            t,
        )
    } else {
        system::area_box(
            TextArea::new(edit)
                .key(key.clone())
                .label(label.to_owned())
                .placeholder(placeholder.to_owned())
                .on_edit(move |state: &mut State| super::draft_mut(state, &editing))
                .on_blur(move |state: &mut State, tx: &mut Tx| {
                    super::commit_draft(state, &left, tx)
                })
                .auto_grow(3, 12)
                .resizable(),
            system::FieldState::Ready,
            t,
        )
    };
    let field = Field::new(key, label).control(control);
    match hint {
        Some(hint) => field.hint(hint),
        None => field,
    }
    .build(t)
}

fn role_editor(state: &State, role: &DelegationRole, t: &Tokens) -> Vec<Block> {
    let id = role.id.clone();
    let icon_button = |which: &'static str, label: String| {
        let id = id.clone();
        let glyph = role_glyph(&role.name, which, t);
        Button::new(label)
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .label_with(move |_, _| glyph)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                let Some(preferences) = page_state(state).preferences.clone() else {
                    return;
                };
                commit(state, table::with_icon(&preferences, &id, which), tx);
            })
            .build(crate::settings::kit(t))
            .key(format!(
                "settings.delegation.role.{}.icon.{}",
                role.id,
                if which.is_empty() { "initial" } else { which }
            ))
            .checked(role.icon == which)
    };
    let icons = Inline::tight()
        .key(format!("settings.delegation.role.{}.icons", role.id))
        .role("group")
        .label("Role icon")
        .child(icon_button("", "Initial as icon".into()))
        .children(
            ICONS
                .iter()
                .map(|(which, _)| icon_button(which, format!("{which} icon"))),
        );
    vec![
        text_field(state, table::Field::Name(id.clone()), "Name", "", None, t),
        Block::custom(
            format!("settings.delegation.role.{}.icons.block", role.id),
            icons,
        ),
        text_field(
            state,
            table::Field::Description(id.clone()),
            "When to choose this role",
            "Describe the work this role fits. The agent reads this to pick a role, so say what it looks like and what it is not for.",
            Some(DESCRIPTION_HINT),
            t,
        ),
        text_field(
            state,
            table::Field::Instructions(id),
            "Instructions",
            "",
            None,
            t,
        ),
    ]
}

fn alternative_row(
    state: &State,
    d: &Delegation,
    role: &DelegationRole,
    view: &DelegationRole,
    alt: &DelegationChoice,
    runs_on: Column,
    t: &Tokens,
) -> SubRow<State> {
    let role_id = role.id.clone();
    let alt_id = alt.id.clone();
    let open = d.expanded_alt.as_deref() == Some(alt_id.as_str());
    let name = if alt.name.is_empty() {
        "alternative".to_owned()
    } else {
        alt.name.clone()
    };
    let shown = if alt.name.is_empty() {
        "Unnamed alternative".to_owned()
    } else {
        alt.name.clone()
    };
    let when = table::first_line(&alt.when);
    let key = format!("settings.delegation.alternative.{role_id}/{alt_id}");
    let toggle = {
        let alt_id = alt_id.clone();
        move |state: &mut State, _: &mut Tx| {
            let d = page_state_mut(state);
            d.expanded_alt = if d.expanded_alt.as_deref() == Some(alt_id.as_str()) {
                None
            } else {
                Some(alt_id.clone())
            };
        }
    };
    let said = if when.is_empty() {
        NOT_SAID.to_owned()
    } else {
        when.clone()
    };
    let remove = {
        let (role_id, alt_id, name, role_name) = (
            role_id.clone(),
            alt_id.clone(),
            name.clone(),
            view.name.clone(),
        );
        move |state: &mut State, tx: &mut Tx| {
            let Some(preferences) = page_state(state).preferences.clone() else {
                return;
            };
            commit_undoable(
                state,
                table::removed_alternative(&preferences, &role_id, &alt_id),
                format!("Removed {name} from {role_name}"),
                tx,
            );
            close_alt(state, &alt_id);
        }
    };
    let mut sub = SubRow::new(key.clone(), shown, toggle)
        .note("when", said, when.is_empty())
        .trailing(
            system::Button::mark("×", system::IconSize::Large)
                .look(Look::Quiet)
                .size(Size::Compact)
                .help(format!("Remove {name}"))
                .on_press_tx(remove)
                .build(t)
                .key(format!("{key}.remove"))
                .label(format!("Remove {name}")),
        )
        .open(open);
    if let Some(chip) = model_chip(
        state,
        Slot::Choice(role_id.clone(), alt_id.clone()),
        &format!("Model for {name}"),
        t,
    ) {
        sub = sub.control(runs_on, chip);
    }
    if !open {
        return sub;
    }
    let make_default = {
        let (role_id, alt_id, role_name) = (role_id.clone(), alt_id.clone(), view.name.clone());
        let name = name.clone();
        move |state: &mut State, tx: &mut Tx| {
            let Some(preferences) = page_state(state).preferences.clone() else {
                return;
            };
            commit_undoable(
                state,
                table::made_default(&preferences, &role_id, &alt_id),
                format!("Made {name} the default for {role_name}"),
                tx,
            );
            close_alt(state, &alt_id);
        }
    };
    sub.details(text_field(
        state,
        table::Field::AlternativeName(role_id.clone(), alt_id.clone()),
        "Name",
        "Short label, shown to the agent with the condition",
        None,
        t,
    ))
    .details(text_field(
        state,
        table::Field::When(role_id, alt_id),
        "When to use this instead of the default",
        "Describe the work this alternative fits. The agent reads this to decide, so say what it looks like and what it is not for.",
        Some(DESCRIPTION_HINT),
        t,
    ))
    .details(
        Actions::new(format!("{key}.actions"))
            .push(button("Make default", Look::Plain, t, make_default).key(format!("{key}.make-default")))
            .build(),
    )
}

fn toggle(key: String) -> impl Fn(&mut State, &mut Tx) + 'static {
    move |state: &mut State, _: &mut Tx| {
        let d = page_state_mut(state);
        d.expanded = if d.expanded.as_deref() == Some(key.as_str()) {
            None
        } else {
            Some(key.clone())
        };
        d.expanded_alt = None;
    }
}

fn expand(state: &mut State, key: String) {
    let d = page_state_mut(state);
    d.expanded = Some(key);
    d.expanded_alt = None;
}

fn expanded_roles(state: &State) -> Vec<DelegationRole> {
    state
        .window
        .settings
        .harnesses
        .read
        .as_ref()
        .map(|r| r.expanded_roles.clone())
        .unwrap_or_default()
}

fn close_alt(state: &mut State, alt: &str) {
    let d = page_state_mut(state);
    if d.expanded_alt.as_deref() == Some(alt) {
        d.expanded_alt = None;
    }
}

const FALLBACK: &str = "__fallback";

fn fallback_row(state: &State, d: &Delegation, runs_on: Column, t: &Tokens) -> TableRow<State> {
    let open = d.expanded.as_deref() == Some(FALLBACK);
    let mut row = TableRow::new(
        "settings.delegation.fallback",
        "Anything else",
        toggle(FALLBACK.into()),
    )
    .leading(system::icon_box(
        role_glyph("Anything else", "arrow", t),
        true,
        t,
    ))
    .subtitle("When no role fits the work. Agents use this instead of choosing a model themselves.")
    .more("Anything else details")
    .open(open);
    if let Some(chip) = model_chip(state, Slot::Fallback, "Model for anything else", t) {
        row = row.control(runs_on, chip);
    }
    if !open {
        return row;
    }
    row.details(text_field(
        state,
        table::Field::FallbackInstructions,
        "Instructions (optional)",
        "Anything every unmatched delegation should hear.",
        None,
        t,
    ))
}

fn add_role(state: &mut State, tx: &mut Tx) {
    let Some(preferences) = page_state(state).preferences.clone() else {
        return;
    };
    let (next, id) = table::added_role(&preferences);
    commit(state, next, tx);
    expand(state, id);
}

/// Adds the maintained roles the table lacks, asking first where one looks like a custom row.
fn adopt(state: &mut State, tx: &mut Tx) {
    let Some(preferences) = page_state(state).preferences.clone() else {
        return;
    };
    let read = state.window.settings.harnesses.read.clone();
    let expanded = read
        .as_ref()
        .map(|r| r.expanded_roles.clone())
        .unwrap_or_default();
    let templates = read.map(|r| r.templates).unwrap_or_default();
    let missing = table::missing_templates(&preferences, &templates);
    let names: Vec<String> = missing
        .iter()
        .map(|t| table::label(table::view(&expanded, t)))
        .collect();
    if table::adoption_conflicts(&preferences, &names) {
        page_state_mut(state).adoption = Some(table::fresh_adoption(&missing));
        return;
    }
    let next = table::adopted(&preferences, &missing, &BTreeMap::new());
    page_state_mut(state).undo = None;
    persist(state, next, true, tx);
}

fn confirm_adoption(state: &mut State, tx: &mut Tx) {
    let Some(preferences) = page_state(state).preferences.clone() else {
        return;
    };
    let templates = state
        .window
        .settings
        .harnesses
        .read
        .as_ref()
        .map(|r| r.templates.clone())
        .unwrap_or_default();
    let missing = table::missing_templates(&preferences, &templates);
    let d = page_state_mut(state);
    let adoption = d.adoption.take().unwrap_or_default();
    d.adoption_open = None;
    d.undo = None;
    let next = table::adopted(&preferences, &missing, &adoption);
    persist(state, next, true, tx);
}

fn empty(can_adopt: bool, t: &Tokens) -> Empty {
    Empty::new(
        "settings.delegation.empty",
        "No roles yet",
        "Start with Attn's maintained roles, then pick a model for each. Attn keeps their instructions current; the model choices stay yours.",
        [
            El::from(
                system::Button::new("Add Attn roles")
                    .look(Look::Primary)
                    .disabled(!can_adopt)
                    .on_press_tx(adopt)
                    .build(t),
            )
            .key("settings.delegation.adopt"),
            El::from(button("+ Custom role", Look::Plain, t, add_role))
                .key("settings.delegation.add"),
        ],
    )
}

fn add_row(preferences: &DelegationPreferences, missing: &[String], t: &Tokens) -> Actions {
    let some = preferences.roles.iter().any(|r| r.builtin.is_some());
    let adopt_label = if !some {
        "Add Attn roles".to_owned()
    } else if missing.len() == 1 {
        format!("+ {}", missing[0])
    } else {
        format!("Add Attn roles: {}", missing.join(", "))
    };
    let actions = Actions::new("settings.delegation.footer.actions")
        .push(button("+ Custom role", Look::Quiet, t, add_role).key("settings.delegation.add"));
    if missing.is_empty() {
        return actions;
    }
    actions.push(button(adopt_label, Look::Quiet, t, adopt).key("settings.delegation.adopt"))
}

fn foot(preferences: &DelegationPreferences, t: &Tokens) -> Block {
    let live = table::live_roles(preferences);
    let needs = preferences
        .roles
        .iter()
        .filter(|r| table::needs_model(r))
        .count();
    let off = preferences.roles.iter().filter(|r| !r.enabled).count();
    let said = if !preferences.enabled {
        "Off. Agents choose harness and model themselves. Your table is kept.".to_owned()
    } else {
        let mut s = format!(
            "Agents see {live} of {}{}.",
            plural(preferences.roles.len(), "role"),
            if table::complete(&preferences.fallback.selection) {
                " and the fallback"
            } else {
                ""
            }
        );
        if needs > 0 {
            s.push_str(&format!(
                " {needs} need{} a model.",
                if needs == 1 { "s" } else { "" }
            ));
        }
        if off > 0 {
            s.push_str(&format!(" {off} off."));
        }
        s
    };
    Status::new("settings.delegation.live", said, preferences.enabled)
        .aside("Agents read this with attn delegate roles.")
        .build(t)
}

const ADD_AS_NEW: &str = "Add as a new role";

/// Each choice of the adoption line for `template`: what it shows and where it sends the role.
fn adoption_choices(
    preferences: &DelegationPreferences,
    adoption: &BTreeMap<String, String>,
    template: &str,
) -> Vec<(String, String)> {
    let mine = adoption.get(template).map(String::as_str);
    let mut choices = vec![(ADD_AS_NEW.to_owned(), NEW.to_owned())];
    for role in &preferences.roles {
        let taken = adoption.values().any(|d| *d == role.id);
        if role.builtin.is_none() && (!taken || mine == Some(role.id.as_str())) {
            choices.push((format!("Replace {}", role.name), role.id.clone()));
        }
    }
    choices
}

fn adoption_select(
    preferences: &DelegationPreferences,
    adoption: &BTreeMap<String, String>,
    template: &str,
    name: &str,
    open: bool,
) -> Select {
    let choices = adoption_choices(preferences, adoption, template);
    let labels: Vec<String> = choices.iter().map(|(l, _)| l.clone()).collect();
    let mine = adoption
        .get(template)
        .cloned()
        .unwrap_or_else(|| NEW.into());
    let current = choices
        .iter()
        .find(|(_, d)| *d == mine)
        .map(|(l, _)| l.clone());
    let key = template.to_owned();
    let toggled = key.clone();
    Select::new(format!("settings.delegation.adopt.{template}"), &labels)
        .label(name.to_owned())
        .current(current.as_deref())
        .open(open)
        .on_toggle(move |state: &mut State| {
            let d = page_state_mut(state);
            d.adoption_open = if d.adoption_open.as_deref() == Some(toggled.as_str()) {
                None
            } else {
                Some(toggled.clone())
            };
        })
        .on_close(|state: &mut State| page_state_mut(state).adoption_open = None)
        .on_choose(move |state: &mut State, chosen: &str, _: &mut Tx| {
            let d = page_state_mut(state);
            d.adoption_open = None;
            if let (Some(adoption), Some((_, destination))) = (
                d.adoption.as_mut(),
                choices.iter().find(|(l, _)| l == chosen),
            ) {
                adoption.insert(key.clone(), destination.clone());
            }
        })
}

fn adoption_panel(
    state: &State,
    preferences: &DelegationPreferences,
    missing: &[&DelegationRole],
    names: &[String],
    adoption: &BTreeMap<String, String>,
    t: &Tokens,
) -> Block {
    let open = page_state(state).adoption_open.as_deref();
    let k = crate::settings::kit(t);
    Section::new("settings.delegation.adoption", "Adopt maintained roles")
        .description("Some Attn roles look like roles you already have. Choose whether to add them beside your custom rows or replace one. Replacing keeps that row's model choices and on/off state.")
        .push_all(missing.iter().zip(names).map(|(template, name)| {
            let template = table::template_key(template);
            let select = adoption_select(preferences, adoption, &template, name, open == Some(template.as_str()))
                .trigger(k);
            SettingRow::new(format!("settings.delegation.adoption.{template}"), name.clone(), "", select).build(t)
        }))
        .push(
            Actions::new("settings.delegation.adoption.actions")
                .push(
                    button("Cancel", Look::Plain, t, |state: &mut State, _: &mut Tx| {
                        let d = page_state_mut(state);
                        d.adoption = None;
                        d.adoption_open = None;
                    })
                    .key("settings.delegation.adoption.cancel"),
                )
                .push(
                    system::Button::new("Add Attn roles")
                        .look(Look::Primary)
                        .on_press_tx(confirm_adoption)
                        .build(t)
                        .key("settings.delegation.adoption.confirm"),
                )
                .build(),
        )
        .build(t)
}

/// The open adoption choice's list, over the page.
pub(in crate::settings) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    if state.window.settings.section != Page::Delegation {
        return;
    }
    let d = page_state(state);
    let (Some(preferences), Some(adoption), Some(open)) =
        (&d.preferences, &d.adoption, &d.adoption_open)
    else {
        return;
    };
    let read = state.window.settings.harnesses.read.as_ref();
    let expanded: &[DelegationRole] = read.map_or(&[], |r| r.expanded_roles.as_slice());
    let templates: &[DelegationRole] = read.map_or(&[], |r| r.templates.as_slice());
    let Some(template) = table::missing_templates(preferences, templates)
        .into_iter()
        .find(|t| table::template_key(t) == *open)
    else {
        return;
    };
    let name = table::label(table::view(expanded, template));
    adoption_select(preferences, adoption, open, &name, true)
        .build(crate::settings::kit(t), layers);
}
