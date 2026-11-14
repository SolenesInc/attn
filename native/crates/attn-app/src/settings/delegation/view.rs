//! The Delegation page: the roles table with its model chips, the role and alternative editors,
//! the fallback, adoption of the maintained roles, undo, and the switch in the page head.

use std::collections::BTreeMap;

use attn_protocol::{DelegationChoice, DelegationPreferences, DelegationRole};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Icon, TextEdit, Tx, col, icon, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Select, TextArea, TextInput};

use super::table::{self, Field, NEW, Slot};
use super::{Delegation, commit, commit_undoable, page_state, page_state_mut, persist};
use crate::State;
use crate::settings::harnesses;
use crate::settings::pages::{CAPTION, switch, warning};
use crate::settings::route::{self, Route, RouteHarness, Rules, Target};
use crate::settings::sections::Section;

const ICON_COLUMN: f32 = 36.0;
const CELL_COLUMN: f32 = 280.0;
const MORE_COLUMN: f32 = 40.0;
const COLUMN_GAP: f32 = 14.0;
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

/// A role's icon, or its name's initial when it has none.
fn role_icon(name: &str, which: &str, t: &Tokens) -> El {
    match ICONS.iter().find(|(id, _)| *id == which) {
        Some((_, glyph)) => icon(glyph, 16.0).color(t.text_secondary).a11y_hidden(),
        None => {
            let initial = name
                .trim()
                .chars()
                .next()
                .map_or_else(|| "○".to_owned(), |c| c.to_uppercase().collect());
            text(initial)
                .font(type_::LABEL)
                .color(t.text_secondary)
                .a11y_hidden()
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
    if state.window.settings.section != Section::Delegation {
        return None;
    }
    let on = page_state(state).preferences.as_ref()?.enabled;
    Some(switch(
        "settings.delegation.switch".into(),
        "Delegation preferences",
        on,
        true,
        t,
        |state: &mut State, tx: &mut Tx| {
            let Some(mut next) = page_state(state).preferences.clone() else {
                return;
            };
            next.enabled = !next.enabled;
            commit(state, next, tx);
        },
    ))
}

fn button(
    label: impl Into<String>,
    variant: ButtonVariant,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(label)
        .variant(variant)
        .control_size(ControlSize::Small)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
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

fn model_chip(state: &State, slot: Slot, label: &str, t: &Tokens) -> El {
    route::chip(
        state,
        t,
        Target {
            id: slot.id(),
            read: read_route,
            write: write_route,
        },
        label,
        false,
    )
}

/// The page while the table is read, and after.
pub(in crate::settings) fn page(state: &State, t: &Tokens) -> Vec<El> {
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
        text(lead_text(preferences.enabled))
            .key("settings.delegation.lead")
            .font(type_::BODY)
            .color(if preferences.enabled {
                t.text_secondary
            } else {
                t.text_tertiary
            })
            .wrap(),
    ];
    if !d.error.is_empty() {
        out.push(warning(
            "settings.delegation.error".into(),
            d.error.clone(),
            t,
        ));
    }
    let mut rows = col()
        .key("settings.delegation.table")
        .w_full()
        .radius(10.0)
        .border(1.0, t.border)
        .fill(t.bg_panel)
        .child(head(t));
    if preferences.roles.is_empty() && d.adoption.is_none() {
        rows = rows.child(empty_panel(!missing.is_empty(), t));
    }
    for role in &preferences.roles {
        rows = rows.child(role_row(state, d, role, table::view(expanded, role), t));
    }
    rows = rows.child(fallback_row(state, d, t));
    if !preferences.roles.is_empty() {
        rows = rows.child(add_row(preferences, &missing_names, t));
    }
    if !preferences.enabled {
        rows = rows.opacity(0.5);
    }
    out.push(rows);
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
        let said = format!("{}.", undo.label);
        out.push(
            row()
                .key("settings.delegation.undo")
                .role("status")
                .label(said.clone())
                .gap(space::S2)
                .items_center()
                .child(
                    text(said)
                        .font(CAPTION)
                        .color(t.text_secondary)
                        .a11y_hidden(),
                )
                .child(
                    button("Undo", ButtonVariant::Ghost, t, super::undo)
                        .key("settings.delegation.undo.button"),
                ),
        );
    }
    if !preferences.roles.is_empty() {
        out.push(foot(preferences, t));
    }
    out
}

fn loading(d: &Delegation, t: &Tokens) -> El {
    let said = if d.error.is_empty() {
        "Loading delegation preferences…".to_owned()
    } else {
        d.error.clone()
    };
    row()
        .key("settings.delegation.loading")
        .role("status")
        .label(said.clone())
        .gap(space::S2)
        .items_center()
        .child(
            text(said)
                .font(type_::BODY)
                .color(t.text_secondary)
                .a11y_hidden(),
        )
        .children((!d.error.is_empty()).then(|| {
            button("Retry", ButtonVariant::Neutral, t, super::reload)
                .key("settings.delegation.retry")
        }))
}

fn columns(icon_el: El, who: El, cell: El, more: El) -> El {
    row()
        .w_full()
        .gap(COLUMN_GAP)
        .items_center()
        .px(16.0)
        .child(row().w(ICON_COLUMN).items_center().child(icon_el))
        .child(row().grow(1.0).min_w(0.0).child(who))
        .child(
            row()
                .w(CELL_COLUMN)
                .gap(space::S2)
                .items_center()
                .child(cell),
        )
        .child(row().w(MORE_COLUMN).items_center().child(more))
}

fn head(t: &Tokens) -> El {
    let label = |s: &str| {
        text(s.to_uppercase())
            .font(type_::SECTION)
            .color(t.text_tertiary)
    };
    columns(row(), label("Role"), label("Runs on"), row())
        .key("settings.delegation.head")
        .py(space::S2)
        .fill(t.bg_elevated)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
}

const OFF: &str = "Off";

/// The button that names a row and opens its details, with what the row is for under its name;
/// dimmed while the row is off.
fn who(
    key: String,
    name: &str,
    tags: Vec<&'static str>,
    description: String,
    open: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let dim = tags.contains(&OFF);
    let said: Vec<String> = tags
        .iter()
        .map(|s| (*s).to_owned())
        .chain((!description.is_empty()).then(|| description.clone()))
        .collect();
    let (primary, secondary, warning, muted) =
        (t.text_primary, t.text_tertiary, t.warning, t.text_muted);
    let shown = name.to_owned();
    let tag_key = key.clone();
    Button::new(name)
        .variant(ButtonVariant::Ghost)
        .expanded(open)
        .label_with(move |font, _| {
            col()
                .w_full()
                .min_w(0.0)
                .gap(2.0)
                .items_start()
                .opacity(if dim { 0.45 } else { 1.0 })
                .child(
                    row()
                        .gap(space::S2)
                        .items_center()
                        .child(text(shown).font(font).color(primary))
                        .children(tags.into_iter().map(|tag| {
                            let ink = if tag == "Needs a model" {
                                warning
                            } else {
                                muted
                            };
                            text(tag)
                                .key(format!("{tag_key}.tag.{tag}"))
                                .font(CAPTION)
                                .color(ink)
                        })),
                )
                .children((!description.is_empty()).then(|| {
                    text(description)
                        .font(CAPTION)
                        .color(secondary)
                        .w_full()
                        .min_w(0.0)
                        .truncate()
                }))
        })
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key)
        .description(said.join(" · "))
        .h(48.0)
        .w_full()
        .min_w(0.0)
        .justify_start()
}

fn more(
    key: String,
    name: &str,
    open: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new("···")
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .expanded(open)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key)
        .label(format!("{name} details"))
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

fn role_row(
    state: &State,
    d: &Delegation,
    role: &DelegationRole,
    view: &DelegationRole,
    t: &Tokens,
) -> El {
    let id = role.id.clone();
    let open = d.expanded.as_deref() == Some(id.as_str());
    let alts = table::alternatives(role);
    let read_only = role.builtin.is_some();
    let mut tags = Vec::new();
    if read_only {
        tags.push("Attn");
    }
    if !role.enabled {
        tags.push(OFF);
    }
    if table::needs_model(role) {
        tags.push("Needs a model");
    }
    let key = format!("settings.delegation.role.{id}");
    let main = table::default_choice(role);
    let mut cell = row().gap(space::S2).items_center();
    if let Some(main) = main {
        cell = cell.child(model_chip(
            state,
            Slot::Choice(id.clone(), main.id.clone()),
            &format!("Model for {}", view.name),
            t,
        ));
    }
    if !alts.is_empty() && !open {
        let n = alts.len();
        cell = cell.child(
            button(
                format!("+{n} alternative{}", if n == 1 { "" } else { "s" }),
                ButtonVariant::Ghost,
                t,
                toggle(id.clone()),
            )
            .key(format!("{key}.alternatives")),
        );
    }
    let mut out = col()
        .key(format!("{key}.row"))
        .w_full()
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            columns(
                row()
                    .opacity(if role.enabled { 1.0 } else { 0.45 })
                    .child(role_icon(&view.name, &view.icon, t)),
                who(
                    key.clone(),
                    &view.name,
                    tags,
                    table::first_line(&view.description),
                    open,
                    t,
                    toggle(id.clone()),
                ),
                cell,
                more(
                    format!("{key}.more"),
                    &view.name,
                    open,
                    t,
                    toggle(id.clone()),
                ),
            )
            .py(space::S3),
        );
    if !open {
        return out;
    }
    let details = if read_only {
        read_only_field(
            "Instructions",
            &view.instructions,
            "maintained by Attn, updates with releases",
            format!("{key}.instructions"),
            t,
        )
    } else {
        role_editor(state, role, t)
    };
    out = out.child(details_block(details));
    for alt in alts {
        out = out.child(alternative_row(state, d, role, view, alt, t));
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
    out = out.child(
        columns(
            row(),
            button("+ Alternative model", ButtonVariant::Ghost, t, add)
                .key(format!("{key}.add-alternative")),
            row(),
            row(),
        )
        .pb(space::S2),
    );
    let mut actions = row().w_full().gap(space::S2).items_center();
    if read_only {
        let id = id.clone();
        actions = actions.child(
            button(
                "Make an editable copy",
                ButtonVariant::Neutral,
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
    actions = actions.child(row().grow(1.0));
    let turn = if role.enabled { "Turn off" } else { "Turn on" };
    {
        let id = id.clone();
        actions = actions.child(
            button(
                turn,
                ButtonVariant::Ghost,
                t,
                move |state: &mut State, tx: &mut Tx| {
                    let Some(preferences) = page_state(state).preferences.clone() else {
                        return;
                    };
                    commit(state, table::toggled(&preferences, &id), tx);
                },
            )
            .key(format!("{key}.turn")),
        );
    }
    let name = view.name.clone();
    actions = actions.child(
        button(
            "Delete",
            ButtonVariant::Ghost,
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
    );
    out.child(details_block(actions))
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

fn details_block(body: El) -> El {
    col()
        .w_full()
        .gap(space::S3)
        .pl(16.0 + ICON_COLUMN + COLUMN_GAP)
        .pr(16.0 + MORE_COLUMN + COLUMN_GAP)
        .pb(space::S3)
        .child(body)
}

fn read_only_field(label: &str, value: &str, note: &str, key: String, t: &Tokens) -> El {
    col()
        .w_full()
        .gap(space::S1)
        .child(field_label(label, Some(note), t))
        .child(
            text(value.to_owned())
                .key(key)
                .label(value.to_owned())
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        )
}

fn field_label(label: &str, note: Option<&str>, t: &Tokens) -> El {
    row()
        .gap(space::S2)
        .items_center()
        .child(
            text(label.to_owned())
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .children(note.map(|n| text(n.to_owned()).font(CAPTION).color(t.text_tertiary)))
}

/// A text the table keeps: it shows the table's text until the first edit, and saves the draft
/// when the focus leaves it, or on Enter in a single line.
fn text_field(
    state: &State,
    field: Field,
    label: &str,
    placeholder: &str,
    hint: Option<&str>,
    t: &Tokens,
) -> El {
    let d = page_state(state);
    let key = field.key();
    let label_key = format!("{key}.label");
    let shown = TextEdit::new(
        d.preferences
            .as_ref()
            .map(|p| field.value(p))
            .unwrap_or_default(),
    );
    let edit = d.drafts.get(&key).map_or(&shown, |draft| &draft.edit);
    let k = attn_ui::kit(t);
    let control = if field.single_line() {
        let editing = field.clone();
        let submitted = key.clone();
        TextInput::new(edit)
            .key(key)
            .label(label.to_owned())
            .placeholder(placeholder.to_owned())
            .on_edit(move |state: &mut State| super::draft_mut(state, &editing))
            .on_submit(move |state: &mut State, tx: &mut Tx| {
                super::commit_draft(state, &submitted, tx);
                tx.blur();
            })
            .width(None)
            .build(k)
    } else {
        let editing = field.clone();
        TextArea::new(edit)
            .key(key)
            .label(label.to_owned())
            .placeholder(placeholder.to_owned())
            .on_edit(move |state: &mut State| super::draft_mut(state, &editing))
            .auto_grow(3, 12)
            .width(None)
            .build(k)
    };
    col()
        .w_full()
        .gap(space::S1)
        .child(field_label(label, None, t).key(label_key))
        .child(control)
        .children(hint.map(|h| {
            text(h.to_owned())
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap()
        }))
}

fn role_editor(state: &State, role: &DelegationRole, t: &Tokens) -> El {
    let id = role.id.clone();
    let icon_button = |which: &'static str, label: String| {
        let id = id.clone();
        let glyph = role_icon(&role.name, which, t);
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
            .build(attn_ui::kit(t))
            .key(format!(
                "settings.delegation.role.{}.icon.{}",
                role.id,
                if which.is_empty() { "initial" } else { which }
            ))
            .checked(role.icon == which)
    };
    let icons = row()
        .key(format!("settings.delegation.role.{}.icons", role.id))
        .role("group")
        .label("Role icon")
        .gap(space::S1)
        .child(icon_button("", "Initial as icon".into()))
        .children(
            ICONS
                .iter()
                .map(|(which, _)| icon_button(which, format!("{which} icon"))),
        );
    col()
        .w_full()
        .gap(space::S3)
        .child(text_field(state, Field::Name(id.clone()), "Name", "", None, t))
        .child(icons)
        .child(text_field(
            state,
            Field::Description(id.clone()),
            "When to choose this role",
            "Describe the work this role fits. The agent reads this to pick a role, so say what it looks like and what it is not for.",
            Some(DESCRIPTION_HINT),
            t,
        ))
        .child(text_field(state, Field::Instructions(id), "Instructions", "", None, t))
}

fn alternative_row(
    state: &State,
    d: &Delegation,
    role: &DelegationRole,
    view: &DelegationRole,
    alt: &DelegationChoice,
    t: &Tokens,
) -> El {
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
    let (primary, secondary, tertiary) = (t.text_primary, t.text_secondary, t.text_tertiary);
    let said = if when.is_empty() {
        NOT_SAID.to_owned()
    } else {
        when.clone()
    };
    let blank = when.is_empty();
    let label_name = shown.clone();
    let shown_when = said.clone();
    let who_button = Button::new(shown.clone())
        .variant(ButtonVariant::Ghost)
        .expanded(open)
        .label_with(move |font, _| {
            col()
                .gap(2.0)
                .items_start()
                .child(text(label_name).font(font).color(primary))
                .child(
                    row()
                        .gap(space::S1)
                        .child(text("when").font(CAPTION).color(tertiary))
                        .child(
                            text(shown_when)
                                .font(CAPTION)
                                .color(if blank { tertiary } else { secondary })
                                .truncate(),
                        ),
                )
        })
        .on_press_tx(toggle)
        .build(attn_ui::kit(t))
        .key(key.clone())
        .description(said)
        .h(44.0)
        .w_full()
        .justify_start();
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
    let remove_button = Button::new("×")
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .help(format!("Remove {name}"))
        .on_press_tx(remove)
        .build(attn_ui::kit(t))
        .key(format!("{key}.remove"))
        .label(format!("Remove {name}"));
    let mut out = col().w_full().child(
        columns(
            row(),
            who_button,
            model_chip(
                state,
                Slot::Choice(role_id.clone(), alt_id.clone()),
                &format!("Model for {name}"),
                t,
            ),
            remove_button,
        )
        .pb(space::S2),
    );
    if open {
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
        out = out.child(details_block(
            col()
                .w_full()
                .gap(space::S3)
                .child(text_field(
                    state,
                    Field::AlternativeName(role_id.clone(), alt_id.clone()),
                    "Name",
                    "Short label, shown to the agent with the condition",
                    None,
                    t,
                ))
                .child(text_field(
                    state,
                    Field::When(role_id, alt_id),
                    "When to use this instead of the default",
                    "Describe the work this alternative fits. The agent reads this to decide, so say what it looks like and what it is not for.",
                    Some(DESCRIPTION_HINT),
                    t,
                ))
                .child(row().child(button("Make default", ButtonVariant::Ghost, t, make_default).key(format!("{key}.make-default")))),
        ));
    }
    out
}

fn close_alt(state: &mut State, alt: &str) {
    let d = page_state_mut(state);
    if d.expanded_alt.as_deref() == Some(alt) {
        d.expanded_alt = None;
    }
}

const FALLBACK: &str = "__fallback";

fn fallback_row(state: &State, d: &Delegation, t: &Tokens) -> El {
    let open = d.expanded.as_deref() == Some(FALLBACK);
    let key = "settings.delegation.fallback".to_owned();
    let mut out = col().key(format!("{key}.row")).w_full().child(
        columns(
            role_icon("Anything else", "arrow", t),
            who(
                key.clone(),
                "Anything else",
                Vec::new(),
                "When no role fits the work. Agents use this instead of choosing a model themselves.".into(),
                open,
                t,
                toggle(FALLBACK.into()),
            ),
            model_chip(state, Slot::Fallback, "Model for anything else", t),
            more(format!("{key}.more"), "Anything else", open, t, toggle(FALLBACK.into())),
        )
        .py(space::S3),
    );
    if open {
        out = out.child(details_block(text_field(
            state,
            Field::FallbackInstructions,
            "Instructions (optional)",
            "Anything every unmatched delegation should hear.",
            None,
            t,
        )));
    }
    out
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

fn empty_panel(can_adopt: bool, t: &Tokens) -> El {
    col()
        .key("settings.delegation.empty")
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(space::S6)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            text("No roles yet")
                .key("settings.delegation.empty.heading")
                .role("heading")
                .label("No roles yet")
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(
            text("Start with Attn's maintained roles, then pick a model for each. Attn keeps their instructions current; the model choices stay yours.")
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        )
        .child(
            row()
                .gap(space::S2)
                .child(
                    Button::new("Add Attn roles")
                        .variant(ButtonVariant::Submit)
                        .control_size(ControlSize::Small)
                        .disabled(!can_adopt)
                        .on_press_tx(adopt)
                        .build(attn_ui::kit(t))
                        .key("settings.delegation.adopt"),
                )
                .child(button("+ Custom role", ButtonVariant::Neutral, t, add_role).key("settings.delegation.add")),
        )
}

fn add_row(preferences: &DelegationPreferences, missing: &[String], t: &Tokens) -> El {
    let some = preferences.roles.iter().any(|r| r.builtin.is_some());
    let adopt_label = if !some {
        "Add Attn roles".to_owned()
    } else if missing.len() == 1 {
        format!("+ {}", missing[0])
    } else {
        format!("Add Attn roles: {}", missing.join(", "))
    };
    row()
        .key("settings.delegation.add-row")
        .gap(space::S2)
        .px(16.0)
        .py(space::S2)
        .child(
            button("+ Custom role", ButtonVariant::Ghost, t, add_role)
                .key("settings.delegation.add"),
        )
        .children((!missing.is_empty()).then(|| {
            button(adopt_label, ButtonVariant::Ghost, t, adopt).key("settings.delegation.adopt")
        }))
}

fn foot(preferences: &DelegationPreferences, t: &Tokens) -> El {
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
    let dot = if preferences.enabled {
        t.success
    } else {
        t.text_tertiary
    };
    row()
        .key("settings.delegation.foot")
        .w_full()
        .gap(space::S2)
        .items_center()
        .child(
            row()
                .key("settings.delegation.live")
                .role("status")
                .label(said.clone())
                .gap(space::S2)
                .items_center()
                .child(row().size(6.0, 6.0).radius(3.0).fill(dot))
                .child(
                    text(said)
                        .font(CAPTION)
                        .color(t.text_secondary)
                        .a11y_hidden(),
                ),
        )
        .child(row().grow(1.0))
        .child(
            text("Agents read this with attn delegate roles.")
                .font(CAPTION)
                .color(t.text_tertiary),
        )
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
) -> El {
    let open = page_state(state).adoption_open.as_deref();
    let k = attn_ui::kit(t);
    col()
        .key("settings.delegation.adoption")
        .role("group")
        .label("Adopt maintained roles")
        .w_full()
        .gap(space::S2)
        .padding(space::S3)
        .radius(8.0)
        .border(1.0, t.border)
        .child(text("Some Attn roles look like roles you already have").font(type_::TITLE).color(t.text_primary))
        .child(
            text("Choose whether to add them beside your custom rows or replace one. Replacing keeps that row's model choices and on/off state.")
                .font(CAPTION)
                .color(t.text_secondary)
                .wrap(),
        )
        .children(missing.iter().zip(names).map(|(template, name)| {
            let template = table::template_key(template);
            row()
                .gap(space::S3)
                .items_center()
                .child(text(name.clone()).font(type_::LABEL).color(t.text_primary).grow(1.0))
                .child(adoption_select(preferences, adoption, &template, name, open == Some(template.as_str())).trigger(k))
        }))
        .child(
            row()
                .gap(space::S2)
                .child(
                    button("Cancel", ButtonVariant::Neutral, t, |state: &mut State, _: &mut Tx| {
                        let d = page_state_mut(state);
                        d.adoption = None;
                        d.adoption_open = None;
                    })
                    .key("settings.delegation.adoption.cancel"),
                )
                .child(
                    Button::new("Add Attn roles")
                        .variant(ButtonVariant::Submit)
                        .control_size(ControlSize::Small)
                        .on_press_tx(confirm_adoption)
                        .build(k)
                        .key("settings.delegation.adoption.confirm"),
                ),
        )
}

/// The open adoption choice's list, over the page.
pub(in crate::settings) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    if state.window.settings.section != Section::Delegation {
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
    adoption_select(preferences, adoption, open, &name, true).build(attn_ui::kit(t), layers);
}
