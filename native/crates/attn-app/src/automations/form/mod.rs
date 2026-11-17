//! The form that creates or edits an automation in the drawer's place. A field's rule is
//! checked when the field loses the keyboard, again on each edit once it has failed, and every
//! rule when Save is pressed, as the Tauri app's form does.

pub(crate) mod model;
mod view;

use std::collections::BTreeMap;
use std::sync::OnceLock;

use attn_protocol::{
    AutomationApplyMessage, AutomationApplyResultMessage, AutomationDefinitionGetMessage,
    AutomationDefinitionResultMessage, AutomationDeleteMessage, AutomationSchedulePreviewMessage,
    AutomationSchedulePreviewResultMessage, AutomationSetEnabledMessage, LaunchDesktopSetting,
};
use icu_time::TimeZone;
use icu_time::zone::iana::IanaParserExtended;
use rune::{TextEdit, Tx};

use super::DRAWER;
use super::schedule::{self, Friendly, Tab};
use crate::State;
use crate::requests::Answer;
use model::{CatchUp, Continuity, Field, Location, Override, Trigger, Values};

pub(crate) use view::{overlay, view};

pub(crate) const KEY: &str = "automations.form";
const REVISION_CONFLICT: &str = "revision_conflict";

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Load {
    Loading,
    Ready,
    Failed(String),
}

/// The schedule preview the daemon gave for the cron and zone it was asked about.
#[derive(Default)]
pub(crate) struct Preview {
    asked: Option<(String, String)>,
    pub(crate) times: Vec<String>,
    pub(crate) error: Option<String>,
    /// The daemon refused the schedule, so Save waits for one it accepts; a preview that could
    /// not be asked does not hold Save.
    pub(crate) refused: bool,
    /// Wall-clock milliseconds when the times arrived, which "in 3h" counts from.
    pub(crate) at_ms: i64,
}

pub(crate) struct Form {
    /// The definition being edited; `None` for a new one until it saves.
    pub(crate) id: Option<i64>,
    pub(crate) load: Load,
    pub(crate) revision: i64,
    pub(crate) enabled: Option<bool>,
    pub(crate) profile: String,
    pub(crate) launch_desktop: Option<LaunchDesktopSetting>,
    pub(crate) yaml: String,

    pub(crate) trigger: Trigger,
    pub(crate) continuity: Continuity,
    pub(crate) catch_up: CatchUp,
    pub(crate) location: Location,
    pub(crate) agent: String,
    pub(crate) model: String,
    pub(crate) effort: String,
    pub(crate) name: TextEdit,
    pub(crate) prompt: TextEdit,
    pub(crate) directory: TextEdit,
    pub(crate) guard: TextEdit,
    pub(crate) cron: TextEdit,
    pub(crate) time_zone: TextEdit,
    pub(crate) executable: TextEdit,
    pub(crate) include: Vec<String>,
    pub(crate) exclude: Vec<String>,
    pub(crate) include_draft: TextEdit,
    pub(crate) exclude_draft: TextEdit,
    pub(crate) overrides: Vec<(TextEdit, TextEdit)>,

    pub(crate) errors: BTreeMap<Field, String>,
    pub(crate) saving: bool,
    pub(crate) save_error: Option<String>,
    pub(crate) stale: bool,
    pub(crate) delete_armed: bool,
    pub(crate) advanced: bool,
    pub(crate) definition_shown: bool,
    pub(crate) copied: bool,
    /// The user chose the Cron tab, which shows even for a schedule the controls can write.
    pub(crate) cron_tab: bool,
    /// The custom cron a friendly tab replaced, for Undo.
    pub(crate) replaced: Option<String>,
    pub(crate) preview: Preview,
    pub(crate) open_select: Option<String>,
    pub(crate) zone_suggestions: ZoneSuggestions,
    focused: Option<String>,
    /// This opening of the form: a save or delete answered after it closed changes nothing.
    opening: u64,
    /// The read the form waits for: only its answer fills the form.
    reading: u64,
}

/// The zones listed under the time zone field while the user types in it, as the Tauri form's
/// datalist lists them.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum ZoneSuggestions {
    #[default]
    Hidden,
    /// `active`: the row the arrow keys highlight, by its place among the matches.
    Shown { active: Option<usize> },
}

pub(crate) fn field_key(field: Field) -> String {
    match field {
        Field::Name => format!("{KEY}.name"),
        Field::Prompt => format!("{KEY}.prompt"),
        Field::DirectoryPath => format!("{KEY}.directory"),
        Field::CatchUp => format!("{KEY}.catchup-latest"),
        Field::Include(_) => format!("{KEY}.repositories-include"),
        Field::Exclude(_) => format!("{KEY}.repositories-exclude"),
        Field::OverrideRepository(i) => format!("{KEY}.override-repository.{i}"),
        Field::OverridePath(i) => format!("{KEY}.override-path.{i}"),
    }
}

fn field_of_key(key: &str) -> Option<fn(&Field) -> bool> {
    let rest = key.strip_prefix(KEY)?.strip_prefix('.')?;
    let (name, index) = match rest.split_once('.') {
        Some((name, i)) => (name, i.parse::<usize>().ok()),
        None => (rest, None),
    };
    let check: fn(&Field) -> bool = match (name, index) {
        ("name", None) => |f| *f == Field::Name,
        ("prompt", None) => |f| *f == Field::Prompt,
        ("directory", None) => |f| *f == Field::DirectoryPath,
        ("repositories-include", None) => |f| matches!(f, Field::Include(_)),
        ("repositories-exclude", None) => |f| matches!(f, Field::Exclude(_) | Field::Include(_)),
        ("override-repository", Some(_)) | ("override-path", Some(_)) => {
            |f| matches!(f, Field::OverrideRepository(_) | Field::OverridePath(_))
        }
        _ => return None,
    };
    Some(check)
}

/// The zone the user's clock keeps, by its IANA name, or UTC when the system names none.
fn local_zone() -> String {
    if let Ok(zone) = std::env::var("TZ")
        && let Some(name) = zone.strip_prefix(':').or(Some(zone.as_str()))
        && name.contains('/')
    {
        return name.to_owned();
    }
    std::fs::read_link("/etc/localtime")
        .ok()
        .and_then(|target| {
            let target = target.to_string_lossy().into_owned();
            target
                .split_once("zoneinfo/")
                .map(|(_, name)| name.to_owned())
        })
        .unwrap_or_else(|| "UTC".into())
}

/// Every IANA zone by the canonical name `Intl.supportedValuesOf` gives the Tauri form, UTC
/// first, with each name that finds it in lower case: its own and the old ones linking to it.
fn zones() -> &'static [(String, Vec<String>)] {
    static ZONES: OnceLock<Vec<(String, Vec<String>)>> = OnceLock::new();
    ZONES.get_or_init(|| {
        let mut zones: BTreeMap<String, Vec<String>> = BTreeMap::new();
        for zone in IanaParserExtended::new().iter_all() {
            if zone.time_zone == TimeZone::UNKNOWN {
                continue;
            }
            let canonical = match zone.canonical {
                "Etc/UTC" => "UTC",
                canonical => canonical,
            };
            zones
                .entry(canonical.to_owned())
                .or_insert_with(|| vec![canonical.to_lowercase()])
                .push(zone.normalized.to_lowercase());
        }
        let utc = zones.remove_entry("UTC");
        utc.into_iter().chain(zones).collect()
    })
}

/// The zones that a name holding `typed`, ignoring case, finds, by their canonical name, as a
/// datalist matches; none until something is typed.
pub(crate) fn zones_matching(typed: &str) -> Vec<&'static str> {
    let typed = typed.trim().to_lowercase();
    if typed.is_empty() {
        return Vec::new();
    }
    zones()
        .iter()
        .filter(|(_, names)| names.iter().any(|name| name.contains(&typed)))
        .map(|(zone, _)| zone.as_str())
        .collect()
}

pub(crate) fn choose_zone(state: &mut State, zone: &str) {
    if let Some(f) = form_mut(state) {
        f.time_zone.replace_text(zone);
        f.zone_suggestions = ZoneSuggestions::Hidden;
    }
}

impl Form {
    fn new(id: Option<i64>, profile: String, opening: u64) -> Form {
        let mut form = Form {
            id,
            load: if id.is_some() {
                Load::Loading
            } else {
                Load::Ready
            },
            revision: 0,
            enabled: None,
            profile,
            launch_desktop: None,
            yaml: String::new(),
            trigger: Trigger::Manual,
            continuity: Continuity::Fresh,
            catch_up: CatchUp::Latest,
            location: Location::Directory,
            agent: String::new(),
            model: String::new(),
            effort: String::new(),
            name: TextEdit::default(),
            prompt: TextEdit::default(),
            directory: TextEdit::default(),
            guard: TextEdit::default(),
            cron: TextEdit::default(),
            time_zone: TextEdit::default(),
            executable: TextEdit::default(),
            include: Vec::new(),
            exclude: Vec::new(),
            include_draft: TextEdit::default(),
            exclude_draft: TextEdit::default(),
            overrides: Vec::new(),
            errors: BTreeMap::new(),
            saving: false,
            save_error: None,
            stale: false,
            delete_armed: false,
            advanced: false,
            definition_shown: false,
            copied: false,
            cron_tab: false,
            replaced: None,
            preview: Preview::default(),
            open_select: None,
            zone_suggestions: ZoneSuggestions::Hidden,
            focused: None,
            opening,
            reading: 0,
        };
        form.reset(Values::new(&local_zone()));
        form
    }

    /// Puts `values` in every field and forgets what was checked.
    fn reset(&mut self, values: Values) {
        self.trigger = values.trigger;
        self.continuity = values.continuity;
        self.catch_up = values.catch_up;
        self.location = values.location;
        self.agent = values.agent;
        self.model = values.model;
        self.effort = values.effort;
        self.name = TextEdit::new(values.name);
        self.prompt = TextEdit::new(values.prompt);
        self.directory = TextEdit::new(values.directory);
        self.guard = TextEdit::new(values.guard);
        self.cron = TextEdit::new(values.cron);
        self.time_zone = TextEdit::new(values.time_zone);
        self.executable = TextEdit::new(values.executable);
        self.include = values.include;
        self.exclude = values.exclude;
        self.overrides = values
            .overrides
            .into_iter()
            .map(|o| (TextEdit::new(o.repository), TextEdit::new(o.path)))
            .collect();
        self.errors.clear();
        self.cron_tab = false;
        self.replaced = None;
    }

    pub(crate) fn values(&self) -> Values {
        Values {
            name: self.name.text.clone(),
            id: self.id.unwrap_or(0),
            trigger: self.trigger,
            cron: self.cron.text.clone(),
            time_zone: self.time_zone.text.clone(),
            guard: self.guard.text.clone(),
            continuity: self.continuity,
            catch_up: self.catch_up,
            include: self.include.clone(),
            exclude: self.exclude.clone(),
            agent: self.agent.clone(),
            model: self.model.clone(),
            effort: self.effort.clone(),
            executable: self.executable.text.clone(),
            location: self.location,
            directory: self.directory.text.clone(),
            overrides: self
                .overrides
                .iter()
                .map(|(r, p)| Override {
                    repository: r.text.clone(),
                    path: p.text.clone(),
                })
                .collect(),
            prompt: self.prompt.text.clone(),
        }
    }

    /// Checks the rules of the fields `which` names, keeping the other fields' errors.
    pub(crate) fn check(&mut self, which: impl Fn(&Field) -> bool) {
        let all = model::validate(&self.values());
        self.errors.retain(|f, _| !which(f));
        self.errors
            .extend(all.into_iter().filter(|(f, _)| which(f)));
    }

    /// Checks again the fields `which` names that failed, as an edit to one does.
    pub(crate) fn edited(&mut self, which: impl Fn(&Field) -> bool) {
        if self.errors.keys().any(&which) {
            self.check(which);
        }
    }

    pub(crate) fn mode(&self) -> &'static str {
        if self.id.is_some() { "edit" } else { "create" }
    }

    /// The schedule tab that shows: Cron when chosen or when the controls cannot write the cron.
    pub(crate) fn schedule(&self) -> (Tab, Option<Friendly>) {
        let friendly = schedule::friendly(&self.cron.text);
        let tab = match &friendly {
            Some(f) if !self.cron_tab => f.tab(),
            _ => Tab::Cron,
        };
        (tab, friendly)
    }

    pub(crate) fn save_blocked(&self) -> bool {
        self.saving || (self.trigger == Trigger::Scheduled && self.preview.refused)
    }

    pub(crate) fn automation_state(&self) -> serde_json::Value {
        let values = self.values();
        serde_json::json!({
            "present": true,
            "mode": self.mode(),
            "definitionId": self.id,
            "revision": self.revision,
            "status": match &self.load {
                Load::Loading => "loading",
                Load::Ready => "ready",
                Load::Failed(_) => "load-error",
            },
            "loadError": match &self.load { Load::Failed(e) => e.as_str(), _ => "" },
            "spec": model::spec(&values),
            "errors": self.errors.iter().map(|(f, m)| (f.path(), m.clone())).collect::<BTreeMap<_, _>>(),
            "saving": self.saving,
            "saveBlocked": self.save_blocked(),
            "saveError": self.save_error.clone().unwrap_or_default(),
            "stale": self.stale,
            "enabled": self.enabled,
            "deleteArmed": self.delete_armed,
            "scheduleTab": Tab::ALL.iter().find(|(t, ..)| *t == self.schedule().0).map(|(_, id, _)| *id),
            "replaced": self.replaced,
            "preview": {"times": self.preview.times, "error": self.preview.error},
        })
    }
}

pub(crate) fn form(state: &State) -> Option<&Form> {
    state.window.automations.form.as_ref()
}

pub(crate) fn form_mut(state: &mut State) -> Option<&mut Form> {
    state.window.automations.form.as_mut()
}

/// The form, while it is still the opening that asked.
fn opened_as(state: &mut State, opening: u64) -> Option<&mut Form> {
    form_mut(state).filter(|f| f.opening == opening)
}

/// Opens the form for a new automation, or for `definition` and reads it.
pub(crate) fn open(state: &mut State, definition: Option<i64>, tx: &mut Tx) {
    let profile = state.facts.profiles.selected.clone();
    let opening = state.window.automations.issue();
    state.window.automations.form = Some(Form::new(definition, profile, opening));
    state.window.automations.pull_request = None;
    crate::settings::want_harnesses(state, tx);
    match definition {
        Some(id) => read(state, id, false, tx),
        None => tx.focus(field_key(Field::Name), false),
    }
}

/// Reads the definition into the form: on open, and on Reload after a refused save.
fn read(state: &mut State, id: i64, reload: bool, tx: &mut Tx) {
    let automations = &mut state.window.automations;
    let reading = automations.issue();
    let Some(f) = automations.form.as_mut() else {
        return;
    };
    f.reading = reading;
    state.ask(
        |request_id| AutomationDefinitionGetMessage {
            request_id: Some(request_id),
            definition_id: id,
            ..AutomationDefinitionGetMessage::new()
        },
        move |state, answer, tx: &mut Tx| {
            let Some(f) = form_mut(state).filter(|f| f.reading == reading) else {
                return;
            };
            let read = answer.outcome().and_then(|reply| {
                let result = reply
                    .parse::<AutomationDefinitionResultMessage>()
                    .ok_or("Failed to load automation definition")?;
                let values = model::values_of(
                    result.spec_json.as_deref().unwrap_or_default(),
                    &local_zone(),
                )?;
                Ok((result, values))
            });
            match (read, reload) {
                (Ok((result, values)), _) => {
                    f.reset(values);
                    f.yaml = result.spec_yaml.unwrap_or_default();
                    if let Some(d) = result.definition {
                        f.id = Some(d.id);
                        f.revision = d.revision;
                        f.launch_desktop = d.launch_desktop;
                        f.profile = d.profile_id;
                        f.enabled = Some(d.enabled);
                    }
                    f.load = Load::Ready;
                    f.save_error = None;
                    f.stale = false;
                    if !reload {
                        tx.focus(field_key(Field::Name), false);
                    }
                }
                (Err(why), false) => f.load = Load::Failed(why),
                (Err(why), true) => {
                    f.stale = false;
                    f.save_error = Some(why);
                }
            }
        },
        tx,
    );
}

pub(crate) fn reload(state: &mut State, tx: &mut Tx) {
    if let Some(id) = form(state).and_then(|f| f.id) {
        read(state, id, true, tx);
    }
}

/// Back to the list, dropping what was typed.
/// The Desktop select's choice, which goes to the daemon with the definition when it saves.
pub(crate) fn choose_desktop(
    state: &mut State,
    _item_id: &str,
    desktop: LaunchDesktopSetting,
    _tx: &mut Tx,
) {
    if let Some(f) = form_mut(state) {
        f.launch_desktop = Some(desktop);
    }
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    if state.window.automations.form.take().is_some() {
        crate::settings::close_route_picker(state, tx);
        tx.focus(DRAWER, false);
    }
}

/// Save: every rule first, then the definition goes to the daemon and the list shows again.
pub(crate) fn save(state: &mut State, tx: &mut Tx) {
    let Some(f) = form_mut(state) else { return };
    if f.load != Load::Ready || f.save_blocked() {
        return;
    }
    f.check(|_| true);
    if let Some(first) = f.errors.keys().next().copied() {
        f.advanced |= matches!(
            first,
            Field::OverrideRepository(_) | Field::OverridePath(_) | Field::CatchUp
        );
        tx.focus(field_key(first), true);
        return;
    }
    f.saving = true;
    f.save_error = None;
    f.stale = false;
    let spec = model::spec(&f.values()).to_string();
    let (expected_id, expected_revision) = (f.id.unwrap_or(0), f.revision);
    let launch_desktop = f.launch_desktop.clone();
    let profile = Some(f.profile.clone()).filter(|p| !p.is_empty());
    let opening = f.opening;
    state.ask(
        |request_id| AutomationApplyMessage {
            request_id: Some(request_id),
            definition_yaml: spec,
            expected_id: Some(expected_id),
            expected_revision: Some(expected_revision),
            launch_desktop_setting: launch_desktop,
            profile_id: profile,
            ..AutomationApplyMessage::new()
        },
        move |state, answer, tx: &mut Tx| {
            let Some(f) = opened_as(state, opening) else {
                return;
            };
            f.saving = false;
            let code = match &answer {
                Answer::Reply(reply) => reply
                    .parse::<AutomationApplyResultMessage>()
                    .and_then(|r| r.error_code),
                _ => None,
            };
            match answer.outcome() {
                Ok(_) => {
                    state.window.automations.form = None;
                    tx.focus(DRAWER, false);
                    super::list(state, tx);
                }
                Err(why) => {
                    f.stale = code.as_deref() == Some(REVISION_CONFLICT);
                    f.save_error = Some(why);
                }
            }
        },
        tx,
    );
}

/// Delete: the first press arms it, the second deletes and shows the list.
pub(crate) fn delete(state: &mut State, tx: &mut Tx) {
    let Some(f) = form_mut(state) else { return };
    let Some(id) = f.id else { return };
    if !f.delete_armed {
        f.delete_armed = true;
        return;
    }
    let opening = f.opening;
    state.ask(
        |request_id| AutomationDeleteMessage {
            request_id: Some(request_id),
            definition_id: id,
            ..AutomationDeleteMessage::new()
        },
        move |state, answer, tx: &mut Tx| {
            let outcome = answer.outcome();
            let Some(f) = opened_as(state, opening) else {
                return;
            };
            match outcome {
                Ok(_) => {
                    let automations = &mut state.window.automations;
                    automations.form = None;
                    if automations.selected == Some(id) {
                        automations.selected = None;
                    }
                    tx.focus(DRAWER, false);
                    super::list(state, tx);
                }
                Err(why) => {
                    f.delete_armed = false;
                    f.save_error = Some(why);
                }
            }
        },
        tx,
    );
}

pub(crate) fn toggle_enabled(state: &mut State, tx: &mut Tx) {
    let Some(f) = form(state) else { return };
    let (Some(id), Some(enabled)) = (f.id, f.enabled) else {
        return;
    };
    let next = !enabled;
    state.ask(
        |request_id| AutomationSetEnabledMessage {
            request_id: Some(request_id),
            definition_id: id,
            enabled: next,
            ..AutomationSetEnabledMessage::new()
        },
        move |state, answer, _| {
            let outcome = answer.outcome();
            if let Some(f) = form_mut(state).filter(|f| f.id == Some(id)) {
                match &outcome {
                    Ok(_) => f.enabled = Some(next),
                    Err(why) => f.save_error = Some(why.clone()),
                }
            }
            if outcome.is_ok()
                && let Some(d) = state
                    .window
                    .automations
                    .definitions
                    .iter_mut()
                    .find(|d| d.id == id)
            {
                d.enabled = next;
            }
        },
        tx,
    );
}

pub(crate) fn choose_trigger(state: &mut State, trigger: Trigger) {
    let Some(f) = form_mut(state) else { return };
    f.trigger = trigger;
    f.save_error = None;
    f.stale = false;
    let failed: Vec<Field> = f.errors.keys().copied().collect();
    f.check(|field| failed.contains(field));
}

/// Picks a schedule tab; a friendly one replaces a cron its controls cannot write, which Undo
/// brings back.
pub(crate) fn choose_tab(state: &mut State, tab: Tab) {
    let Some(f) = form_mut(state) else { return };
    if tab == Tab::Cron {
        f.cron_tab = true;
        return;
    }
    f.cron_tab = false;
    let current = schedule::friendly(&f.cron.text);
    if current.as_ref().map(Friendly::tab) == Some(tab) {
        return;
    }
    if current.is_none() && !f.cron.text.trim().is_empty() {
        f.replaced = Some(f.cron.text.clone());
    }
    f.cron = TextEdit::new(Friendly::for_tab(tab, current.as_ref()).cron());
}

pub(crate) fn set_schedule(state: &mut State, schedule: Friendly) {
    if let Some(f) = form_mut(state) {
        f.cron = TextEdit::new(schedule.cron());
    }
}

pub(crate) fn undo_replace(state: &mut State) {
    let Some(f) = form_mut(state) else { return };
    if let Some(cron) = f.replaced.take() {
        f.cron = TextEdit::new(cron);
    }
}

/// Adds a typed repository to the include or exclude list, in its canonical form.
pub(crate) fn add_repository(state: &mut State, exclude: bool) {
    let Some(f) = form_mut(state) else { return };
    let (list, draft) = if exclude {
        (&mut f.exclude, &mut f.exclude_draft)
    } else {
        (&mut f.include, &mut f.include_draft)
    };
    let canonical = draft.text.trim().to_lowercase();
    if canonical.is_empty() {
        return;
    }
    list.push(canonical);
    *draft = TextEdit::default();
    f.check(|f| matches!(f, Field::Include(_) | Field::Exclude(_)));
}

pub(crate) fn remove_repository(state: &mut State, exclude: bool, index: usize) {
    let Some(f) = form_mut(state) else { return };
    let list = if exclude {
        &mut f.exclude
    } else {
        &mut f.include
    };
    if index < list.len() {
        list.remove(index);
    }
    f.check(|f| matches!(f, Field::Include(_) | Field::Exclude(_)));
}

pub(crate) fn add_override(state: &mut State, tx: &mut Tx) {
    let Some(f) = form_mut(state) else { return };
    f.overrides.push((TextEdit::default(), TextEdit::default()));
    tx.focus(
        field_key(Field::OverrideRepository(f.overrides.len() - 1)),
        true,
    );
}

pub(crate) fn remove_override(state: &mut State, index: usize) {
    let Some(f) = form_mut(state) else { return };
    if index < f.overrides.len() {
        f.overrides.remove(index);
    }
    f.errors
        .retain(|field, _| !matches!(field, Field::OverrideRepository(_) | Field::OverridePath(_)));
    f.edited(|_| false);
}

pub(crate) fn browse(tx: &mut Tx) {
    let options = rune::FileDialogOptions {
        title: "Choose a folder".into(),
        ..rune::FileDialogOptions::default()
    };
    tx.open_folder(options, |state: &mut State, chosen, tx| match chosen {
        Ok(Some(folder)) => {
            if let Some(f) = form_mut(state) {
                f.directory = TextEdit::new(folder.to_string_lossy().into_owned());
                f.check(|f| *f == Field::DirectoryPath);
            }
        }
        Ok(None) => {}
        Err(why) => state
            .window
            .notify(format!("Could not ask for a folder: {why}"), tx),
    });
}

/// What follows the form as the window runs: a field checks its rule once the keyboard leaves
/// it, the keyboard moving to another control disarms Delete, the schedule's preview follows its cron
/// and zone, and the model chip's harness is discovered.
pub(crate) fn follow_focus(state: &mut State, tx: &mut Tx) -> bool {
    let Some(f) = form_mut(state) else {
        return false;
    };
    let focused = tx.focused().map(str::to_owned);
    let mut changed = false;
    if focused != f.focused {
        let left = std::mem::replace(&mut f.focused, focused.clone());
        if let Some(which) = left.as_deref().and_then(field_of_key) {
            f.check(which);
            changed = true;
        }
        if focused.as_deref() != Some(view::TIME_ZONE)
            && f.zone_suggestions != ZoneSuggestions::Hidden
        {
            f.zone_suggestions = ZoneSuggestions::Hidden;
            changed = true;
        }
        let stays = matches!(focused.as_deref(), Some(view::DELETE | DRAWER));
        if f.delete_armed && !stays {
            f.delete_armed = false;
            changed = true;
        }
    }
    let wanted = (f.trigger == Trigger::Scheduled && f.load == Load::Ready)
        .then(|| (f.cron.text.clone(), f.time_zone.text.clone()));
    if let Some(asked) = wanted.filter(|w| f.preview.asked.as_ref() != Some(w)) {
        preview(state, asked, tx);
        changed = true;
    }
    changed | crate::settings::discover_routes(state, &[view::route_target()], tx)
}

fn preview(state: &mut State, (cron, time_zone): (String, String), tx: &mut Tx) {
    let Some(f) = form_mut(state) else { return };
    f.preview.asked = Some((cron.clone(), time_zone.clone()));
    let asked = f.preview.asked.clone();
    state.ask(
        |request_id| AutomationSchedulePreviewMessage {
            request_id: Some(request_id),
            cron,
            time_zone,
            ..AutomationSchedulePreviewMessage::new()
        },
        move |state, answer, tx: &mut Tx| {
            let at_ms = state.wall_ms(tx);
            let Some(f) = form_mut(state).filter(|f| f.preview.asked == asked) else {
                return;
            };
            let refused = matches!(&answer, Answer::Reply(_));
            match answer.outcome() {
                Ok(reply) => {
                    let result = reply.parse::<AutomationSchedulePreviewResultMessage>();
                    f.preview.times = result.and_then(|r| r.times).unwrap_or_default();
                    f.preview.error = None;
                    f.preview.refused = false;
                    f.preview.at_ms = at_ms;
                }
                Err(why) => {
                    f.preview.times.clear();
                    f.preview.error = Some(why);
                    f.preview.refused = refused;
                }
            }
        },
        tx,
    );
}
