//! The roles table as the page reads and changes it: each role as its maintained template
//! shows it, the edits the page makes, and the maintained roles it adopts.

use std::collections::{BTreeMap, BTreeSet};

use attn_protocol::{DelegationChoice, DelegationPreferences, DelegationRole, DelegationSelection};

use crate::settings::route::Route;

/// What the adoption sends a maintained role to when it replaces no row.
pub(super) const NEW: &str = "new";

pub(super) fn new_id(prefix: &str) -> String {
    format!("{prefix}-{}", uuid::Uuid::new_v4())
}

pub(super) fn first_line(text: &str) -> String {
    text.lines()
        .map(str::trim)
        .find(|line| !line.is_empty())
        .unwrap_or_default()
        .to_owned()
}

pub(super) fn complete(selection: &DelegationSelection) -> bool {
    !selection.harness.is_empty()
}

pub(super) fn default_choice(role: &DelegationRole) -> Option<&DelegationChoice> {
    role.choices
        .iter()
        .find(|c| c.id == role.default_choice_id)
        .or_else(|| role.choices.first())
}

pub(super) fn alternatives(role: &DelegationRole) -> Vec<&DelegationChoice> {
    role.choices
        .iter()
        .filter(|c| c.id != role.default_choice_id)
        .collect()
}

pub(super) fn needs_model(role: &DelegationRole) -> bool {
    role.enabled && !default_choice(role).is_some_and(|c| complete(&c.selection))
}

pub(super) fn live_roles(preferences: &DelegationPreferences) -> usize {
    preferences
        .roles
        .iter()
        .filter(|r| r.enabled && default_choice(r).is_some_and(|c| complete(&c.selection)))
        .count()
}

/// A maintained role as the daemon expands it for its built-in kind; a custom role as it is.
pub(super) fn view<'a>(
    expanded: &'a [DelegationRole],
    role: &'a DelegationRole,
) -> &'a DelegationRole {
    if role.builtin.is_none() {
        return role;
    }
    expanded
        .iter()
        .find(|r| r.id == role.id && r.builtin == role.builtin)
        .unwrap_or(role)
}

pub(super) fn label(view: &DelegationRole) -> String {
    [
        Some(view.name.as_str()),
        view.builtin.as_deref(),
        Some(view.id.as_str()),
    ]
    .into_iter()
    .flatten()
    .find(|s| !s.is_empty())
    .unwrap_or_default()
    .to_owned()
}

pub(super) fn template_key(template: &DelegationRole) -> String {
    template
        .builtin
        .clone()
        .unwrap_or_else(|| template.id.clone())
}

pub(super) fn missing_templates<'a>(
    preferences: &DelegationPreferences,
    templates: &'a [DelegationRole],
) -> Vec<&'a DelegationRole> {
    templates
        .iter()
        .filter(|t| !preferences.roles.iter().any(|r| r.builtin == t.builtin))
        .collect()
}

/// Whether a custom row looks like one of the maintained roles `names` gives.
pub(super) fn adoption_conflicts(preferences: &DelegationPreferences, names: &[String]) -> bool {
    names.iter().any(|name| {
        let name = name.to_lowercase();
        preferences
            .roles
            .iter()
            .any(|r| r.builtin.is_none() && r.name.trim().to_lowercase().starts_with(&name))
    })
}

pub(super) fn fresh_adoption(templates: &[&DelegationRole]) -> BTreeMap<String, String> {
    templates
        .iter()
        .map(|t| (template_key(t), NEW.to_owned()))
        .collect()
}

/// The table with `templates` added, each beside the custom rows or in place of the one
/// `adoption` names, which keeps that row's id, model choices and on/off state.
pub(super) fn adopted(
    preferences: &DelegationPreferences,
    templates: &[&DelegationRole],
    adoption: &BTreeMap<String, String>,
) -> DelegationPreferences {
    let replaced: BTreeSet<&str> = adoption
        .values()
        .map(String::as_str)
        .filter(|d| *d != NEW)
        .collect();
    let mut occupied: BTreeSet<String> = preferences.roles.iter().map(|r| r.id.clone()).collect();
    let mut additions = Vec::new();
    for template in templates {
        let destination = adoption
            .get(&template_key(template))
            .map_or(NEW, String::as_str);
        let previous = (destination != NEW)
            .then(|| preferences.roles.iter().find(|r| r.id == destination))
            .flatten();
        if let Some(previous) = previous {
            additions.push(DelegationRole {
                id: previous.id.clone(),
                enabled: previous.enabled,
                default_choice_id: previous.default_choice_id.clone(),
                choices: previous.choices.clone(),
                ..(*template).clone()
            });
            continue;
        }
        let mut id = template.id.clone();
        while occupied.contains(&id) {
            id = new_id(&template.id);
        }
        occupied.insert(id.clone());
        additions.push(DelegationRole {
            id,
            ..(*template).clone()
        });
    }
    let mut roles: Vec<DelegationRole> = preferences
        .roles
        .iter()
        .filter(|r| !replaced.contains(r.id.as_str()))
        .cloned()
        .collect();
    roles.extend(additions);
    DelegationPreferences {
        workflow_skill_enabled: true,
        roles,
        ..preferences.clone()
    }
}

fn with_role(
    preferences: &DelegationPreferences,
    id: &str,
    change: impl FnOnce(&mut DelegationRole),
) -> DelegationPreferences {
    let mut next = preferences.clone();
    if let Some(role) = next.roles.iter_mut().find(|r| r.id == id) {
        change(role);
    }
    next
}

fn with_choice(
    preferences: &DelegationPreferences,
    role: &str,
    choice: &str,
    change: impl FnOnce(&mut DelegationChoice),
) -> DelegationPreferences {
    with_role(preferences, role, |r| {
        if let Some(c) = r.choices.iter_mut().find(|c| c.id == choice) {
            change(c);
        }
    })
}

pub(super) fn role<'a>(
    preferences: &'a DelegationPreferences,
    id: &str,
) -> Option<&'a DelegationRole> {
    preferences.roles.iter().find(|r| r.id == id)
}

/// A custom role named New role, and its id.
pub(super) fn added_role(preferences: &DelegationPreferences) -> (DelegationPreferences, String) {
    let id = new_id("role");
    let mut next = preferences.clone();
    next.roles.push(DelegationRole {
        id: id.clone(),
        name: "New role".into(),
        enabled: true,
        default_choice_id: "default".into(),
        choices: vec![DelegationChoice {
            id: "default".into(),
            name: "Default".into(),
            ..DelegationChoice::default()
        }],
        ..DelegationRole::default()
    });
    (next, id)
}

/// A custom copy of a maintained role after it: its guidance as the daemon shows it, with the
/// row's model choices and on/off state; and the copy's id.
pub(super) fn copied(
    preferences: &DelegationPreferences,
    expanded: &[DelegationRole],
    id: &str,
) -> Option<(DelegationPreferences, String)> {
    let index = preferences.roles.iter().position(|r| r.id == id)?;
    let role = &preferences.roles[index];
    let shown = view(expanded, role);
    let copy = DelegationRole {
        id: new_id("role"),
        builtin: None,
        name: format!("{} (custom)", shown.name),
        enabled: role.enabled,
        default_choice_id: role.default_choice_id.clone(),
        choices: role.choices.clone(),
        ..shown.clone()
    };
    let copy_id = copy.id.clone();
    let mut next = preferences.clone();
    next.roles.insert(index + 1, copy);
    Some((next, copy_id))
}

pub(super) fn deleted(preferences: &DelegationPreferences, id: &str) -> DelegationPreferences {
    let mut next = preferences.clone();
    next.roles.retain(|r| r.id != id);
    next
}

pub(super) fn toggled(preferences: &DelegationPreferences, id: &str) -> DelegationPreferences {
    with_role(preferences, id, |r| r.enabled = !r.enabled)
}

pub(super) fn with_icon(
    preferences: &DelegationPreferences,
    id: &str,
    icon: &str,
) -> DelegationPreferences {
    with_role(preferences, id, |r| r.icon = icon.to_owned())
}

pub(super) fn made_default(
    preferences: &DelegationPreferences,
    role: &str,
    choice: &str,
) -> DelegationPreferences {
    with_role(preferences, role, |r| {
        r.default_choice_id = choice.to_owned()
    })
}

pub(super) fn removed_alternative(
    preferences: &DelegationPreferences,
    role: &str,
    choice: &str,
) -> DelegationPreferences {
    with_role(preferences, role, |r| r.choices.retain(|c| c.id != choice))
}

/// An alternative on the role's default model, and its id.
pub(super) fn added_alternative(
    preferences: &DelegationPreferences,
    role: &str,
) -> Option<(DelegationPreferences, String)> {
    let selection = default_choice(self::role(preferences, role)?)
        .map(|c| c.selection.clone())
        .unwrap_or_default();
    let id = new_id("choice");
    let choice = DelegationChoice {
        id: id.clone(),
        name: "Alternative".into(),
        when: String::new(),
        selection,
    };
    Some((with_role(preferences, role, |r| r.choices.push(choice)), id))
}

pub(super) fn harnesses_routed(preferences: &DelegationPreferences) -> Vec<String> {
    preferences
        .roles
        .iter()
        .flat_map(|r| r.choices.iter().map(|c| c.selection.harness.clone()))
        .chain([preferences.fallback.selection.harness.clone()])
        .filter(|h| !h.is_empty())
        .collect()
}

/// Which route of the table a model chip edits.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) enum Slot {
    Fallback,
    Choice(String, String),
}

const SLOT_PREFIX: &str = "delegation.";
const FALLBACK_SLOT: &str = "delegation.fallback";

impl Slot {
    pub(super) fn id(&self) -> String {
        match self {
            Slot::Fallback => FALLBACK_SLOT.into(),
            Slot::Choice(role, choice) => format!("{SLOT_PREFIX}{role}/{choice}"),
        }
    }

    pub(super) fn parse(id: &str) -> Option<Slot> {
        if id == FALLBACK_SLOT {
            return Some(Slot::Fallback);
        }
        let (role, choice) = id.strip_prefix(SLOT_PREFIX)?.split_once('/')?;
        Some(Slot::Choice(role.into(), choice.into()))
    }

    pub(super) fn selection<'a>(
        &self,
        preferences: &'a DelegationPreferences,
    ) -> Option<&'a DelegationSelection> {
        match self {
            Slot::Fallback => Some(&preferences.fallback.selection),
            Slot::Choice(role, choice) => self::role(preferences, role)?
                .choices
                .iter()
                .find(|c| c.id == *choice)
                .map(|c| &c.selection),
        }
    }

    pub(super) fn with(
        &self,
        preferences: &DelegationPreferences,
        route: Route,
    ) -> Option<DelegationPreferences> {
        self.selection(preferences)?;
        let selection = DelegationSelection {
            harness: route.harness,
            provider: route.provider,
            model: route.model,
            effort: route.effort,
        };
        Some(match self {
            Slot::Fallback => {
                let mut next = preferences.clone();
                next.fallback.selection = selection;
                next
            }
            Slot::Choice(role, choice) => {
                with_choice(preferences, role, choice, |c| c.selection = selection)
            }
        })
    }
}

pub(super) fn route(selection: &DelegationSelection) -> Route {
    Route {
        harness: selection.harness.clone(),
        provider: selection.provider.clone(),
        model: selection.model.clone(),
        effort: selection.effort.clone(),
    }
}

/// A text the page edits in place.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) enum Field {
    Name(String),
    Description(String),
    Instructions(String),
    AlternativeName(String, String),
    When(String, String),
    FallbackInstructions,
}

impl Field {
    pub(super) fn key(&self) -> String {
        let at = "settings.delegation.field";
        match self {
            Field::Name(role) => format!("{at}.name.{role}"),
            Field::Description(role) => format!("{at}.description.{role}"),
            Field::Instructions(role) => format!("{at}.instructions.{role}"),
            Field::AlternativeName(role, choice) => {
                format!("{at}.alternative-name.{role}/{choice}")
            }
            Field::When(role, choice) => format!("{at}.when.{role}/{choice}"),
            Field::FallbackInstructions => format!("{at}.fallback-instructions"),
        }
    }

    pub(super) fn value(&self, preferences: &DelegationPreferences) -> String {
        let choice = |role: &str, choice: &str| {
            role_choice(preferences, role, choice)
                .cloned()
                .unwrap_or_default()
        };
        let of_role = |id: &str| role(preferences, id).cloned().unwrap_or_default();
        match self {
            Field::Name(id) => of_role(id).name,
            Field::Description(id) => of_role(id).description,
            Field::Instructions(id) => of_role(id).instructions,
            Field::AlternativeName(r, c) => choice(r, c).name,
            Field::When(r, c) => choice(r, c).when,
            Field::FallbackInstructions => preferences.fallback.instructions.clone(),
        }
    }

    /// The table with `text` kept, unless it changes nothing or the field refuses it.
    pub(super) fn commit(
        &self,
        preferences: &DelegationPreferences,
        text: &str,
    ) -> Option<DelegationPreferences> {
        if text == self.value(preferences) {
            return None;
        }
        let text = text.to_owned();
        Some(match self {
            Field::Name(id) => {
                let name = text.trim().to_owned();
                if name.is_empty() {
                    return None;
                }
                with_role(preferences, id, |r| r.name = name)
            }
            Field::Description(id) => with_role(preferences, id, |r| r.description = text),
            Field::Instructions(id) => with_role(preferences, id, |r| r.instructions = text),
            Field::AlternativeName(r, c) => {
                let name = Some(text.trim().to_owned())
                    .filter(|n| !n.is_empty())
                    .unwrap_or_else(|| "Alternative".into());
                with_choice(preferences, r, c, |choice| choice.name = name)
            }
            Field::When(r, c) => with_choice(preferences, r, c, |choice| choice.when = text),
            Field::FallbackInstructions => {
                let mut next = preferences.clone();
                next.fallback.instructions = text;
                next
            }
        })
    }

    pub(super) fn single_line(&self) -> bool {
        matches!(self, Field::Name(_) | Field::AlternativeName(..))
    }
}

fn role_choice<'a>(
    preferences: &'a DelegationPreferences,
    role: &str,
    choice: &str,
) -> Option<&'a DelegationChoice> {
    self::role(preferences, role)?
        .choices
        .iter()
        .find(|c| c.id == choice)
}
