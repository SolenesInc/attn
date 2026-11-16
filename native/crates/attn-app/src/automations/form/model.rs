//! What the form edits, the rules it checks before a save (the daemon's own, from
//! `internal/automation/automation.go`, so it refuses the same shapes), and the spec it sends.

use std::collections::{BTreeMap, HashSet};

use serde_json::{Map, Value, json};

pub(crate) const API_VERSION: &str = "attn.dev/automations/v1alpha1";
const REPOSITORY: &str = "Use host/owner/repository, e.g. github.com/victorarias/attn.";
const BOTH_LISTS: &str = "This repository is both included and excluded.";
const LISTED_TWICE: &str = "This repository is already listed.";
const ABSOLUTE: &str = "Must be an absolute path.";
pub(crate) const UNEDITABLE_LOCATION: &str =
    "This automation uses a location the form cannot edit. Use the CLI for this definition.";

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum Trigger {
    #[default]
    Manual,
    Scheduled,
    GitHub,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum Continuity {
    #[default]
    Fresh,
    Singleton,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum CatchUp {
    #[default]
    Unchosen,
    Skip,
    Latest,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum Location {
    #[default]
    Directory,
    Worktree,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct Override {
    pub(crate) repository: String,
    pub(crate) path: String,
}

/// The fields a rule can name, each with its place in a list where it has one.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub(crate) enum Field {
    Name,
    Prompt,
    DirectoryPath,
    CatchUp,
    Include(usize),
    Exclude(usize),
    OverrideRepository(usize),
    OverridePath(usize),
}

impl Field {
    pub(crate) fn path(self) -> String {
        match self {
            Field::Name => "name".into(),
            Field::Prompt => "prompt".into(),
            Field::DirectoryPath => "directoryPath".into(),
            Field::CatchUp => "catchUp".into(),
            Field::Include(i) => format!("repositoriesInclude.{i}"),
            Field::Exclude(i) => format!("repositoriesExclude.{i}"),
            Field::OverrideRepository(i) => format!("repositoryOverrides.{i}.repository"),
            Field::OverridePath(i) => format!("repositoryOverrides.{i}.path"),
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct Values {
    pub(crate) name: String,
    pub(crate) id: i64,
    pub(crate) trigger: Trigger,
    pub(crate) cron: String,
    pub(crate) time_zone: String,
    pub(crate) guard: String,
    pub(crate) continuity: Continuity,
    pub(crate) catch_up: CatchUp,
    pub(crate) include: Vec<String>,
    pub(crate) exclude: Vec<String>,
    pub(crate) agent: String,
    pub(crate) model: String,
    pub(crate) effort: String,
    pub(crate) executable: String,
    pub(crate) location: Location,
    pub(crate) directory: String,
    pub(crate) overrides: Vec<Override>,
    pub(crate) prompt: String,
}

impl Values {
    /// A new automation's: manual, run by Codex on its defaults, and a schedule of weekday
    /// mornings in `time_zone` for when the user picks one.
    pub(crate) fn new(time_zone: &str) -> Values {
        Values {
            cron: "0 9 * * 1-5".into(),
            time_zone: time_zone.into(),
            catch_up: CatchUp::Latest,
            agent: "codex".into(),
            ..Values::default()
        }
    }

    pub(crate) fn uses_worktree(&self) -> bool {
        self.trigger == Trigger::GitHub
            || (self.trigger == Trigger::Manual && self.location == Location::Worktree)
    }
}

fn canonical(raw: &str) -> String {
    raw.trim().to_lowercase()
}

fn is_repository(raw: &str) -> bool {
    let identity = canonical(raw);
    let parts: Vec<&str> = identity.split('/').collect();
    let [host, owner, repo] = parts.as_slice() else {
        return false;
    };
    let host_ok = host.starts_with(|c: char| c.is_ascii_lowercase() || c.is_ascii_digit())
        && host
            .chars()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '.' || c == '-');
    let component_ok = |c: &str| {
        c != "."
            && c != ".."
            && !c.is_empty()
            && c.chars().all(|ch| {
                ch.is_ascii_lowercase() || ch.is_ascii_digit() || matches!(ch, '_' | '.' | '-')
            })
    };
    host_ok && component_ok(owner) && component_ok(repo)
}

fn check_list(list: &[String], field: fn(usize) -> Field, errors: &mut BTreeMap<Field, String>) {
    let mut seen = HashSet::new();
    for (i, entry) in list.iter().enumerate() {
        if !is_repository(entry) {
            errors.insert(field(i), REPOSITORY.into());
        } else if !seen.insert(canonical(entry)) {
            errors.insert(field(i), LISTED_TWICE.into());
        }
    }
}

fn check_overrides(overrides: &[Override], errors: &mut BTreeMap<Field, String>) {
    let mut seen = HashSet::new();
    for (i, o) in overrides.iter().enumerate() {
        if !is_repository(&o.repository) {
            errors.insert(Field::OverrideRepository(i), REPOSITORY.into());
        } else if !seen.insert(canonical(&o.repository)) {
            errors.insert(
                Field::OverrideRepository(i),
                "This repository already has a directory override.".into(),
            );
        }
        if !o.path.starts_with('/') {
            errors.insert(Field::OverridePath(i), ABSOLUTE.into());
        }
    }
}

/// What keeps `values` from saving, by field.
pub(crate) fn validate(values: &Values) -> BTreeMap<Field, String> {
    let mut errors = BTreeMap::new();
    if values.name.trim().is_empty() {
        errors.insert(Field::Name, "A name is required.".into());
    }
    if values.prompt.trim().is_empty() {
        errors.insert(Field::Prompt, "A prompt is required.".into());
    }
    match values.trigger {
        Trigger::Manual if values.location == Location::Worktree => {
            check_overrides(&values.overrides, &mut errors);
        }
        Trigger::Manual => {
            if !values.directory.starts_with('/') {
                errors.insert(Field::DirectoryPath, ABSOLUTE.into());
            }
        }
        Trigger::Scheduled => {
            if values.catch_up == CatchUp::Unchosen {
                errors.insert(Field::CatchUp, "Choose what happens to missed runs.".into());
            }
            if !values.directory.starts_with('/') {
                errors.insert(Field::DirectoryPath, ABSOLUTE.into());
            }
        }
        Trigger::GitHub => {
            check_list(&values.include, Field::Include, &mut errors);
            check_list(&values.exclude, Field::Exclude, &mut errors);
            let included: HashSet<String> = values
                .include
                .iter()
                .filter(|e| is_repository(e))
                .map(|e| canonical(e))
                .collect();
            for (i, entry) in values.exclude.iter().enumerate() {
                if is_repository(entry) && included.contains(&canonical(entry)) {
                    errors.insert(Field::Exclude(i), BOTH_LISTS.into());
                }
            }
            check_overrides(&values.overrides, &mut errors);
        }
    }
    errors
}

fn deduped(entries: &[String]) -> Vec<String> {
    let mut seen = HashSet::new();
    entries
        .iter()
        .map(|e| canonical(e))
        .filter(|e| seen.insert(e.clone()))
        .collect()
}

fn launch(values: &Values) -> Value {
    let mut launch = Map::new();
    launch.insert("driver".into(), json!(values.agent));
    let model = values.model.trim();
    let executable = values.executable.trim();
    if !model.is_empty() {
        launch.insert("model".into(), json!(model));
    }
    if !values.effort.is_empty() {
        launch.insert("effort".into(), json!(values.effort));
    }
    if !executable.is_empty() && values.agent != "codex" {
        launch.insert("executable".into(), json!(executable));
    }
    Value::Object(launch)
}

fn trigger(values: &Values) -> Value {
    match values.trigger {
        Trigger::Manual => json!({"type": "manual"}),
        Trigger::Scheduled => {
            let mut trigger = Map::new();
            trigger.insert("type".into(), json!("scheduled"));
            trigger.insert(
                "schedule".into(),
                json!({"cron": values.cron.trim(), "time_zone": values.time_zone}),
            );
            let guard = values.guard.trim();
            if !guard.is_empty() {
                trigger.insert("guard".into(), json!(guard));
            }
            trigger.insert(
                "continuity".into(),
                json!(match values.continuity {
                    Continuity::Fresh => "fresh",
                    Continuity::Singleton => "singleton",
                }),
            );
            trigger.insert(
                "catch_up".into(),
                json!(match values.catch_up {
                    CatchUp::Skip => "skip",
                    CatchUp::Latest => "latest",
                    CatchUp::Unchosen => "",
                }),
            );
            Value::Object(trigger)
        }
        Trigger::GitHub => {
            let mut trigger = Map::new();
            trigger.insert("type".into(), json!("github_review_requested"));
            let (include, exclude) = (deduped(&values.include), deduped(&values.exclude));
            let mut repositories = Map::new();
            if !include.is_empty() {
                repositories.insert("include".into(), json!(include));
            }
            if !exclude.is_empty() {
                repositories.insert("exclude".into(), json!(exclude));
            }
            if !repositories.is_empty() {
                trigger.insert("repositories".into(), Value::Object(repositories));
            }
            Value::Object(trigger)
        }
    }
}

fn location(values: &Values) -> Value {
    if !values.uses_worktree() {
        return json!({"type": "directory", "path": values.directory});
    }
    let mut sources = Map::new();
    sources.insert("default".into(), json!({"type": "managed_cache"}));
    let overrides: Map<String, Value> = values
        .overrides
        .iter()
        .map(|o| {
            (
                canonical(&o.repository),
                json!({"type": "local_clone", "path": o.path}),
            )
        })
        .collect();
    if !overrides.is_empty() {
        sources.insert("overrides".into(), Value::Object(overrides));
    }
    json!({"type": "repository_worktree", "repository_sources": sources})
}

/// The definition `values` make, as the daemon reads it.
pub(crate) fn spec(values: &Values) -> Value {
    let mut spec = Map::new();
    spec.insert("api_version".into(), json!(API_VERSION));
    if values.id != 0 {
        spec.insert("id".into(), json!(values.id));
    }
    spec.insert("name".into(), json!(values.name));
    spec.insert("trigger".into(), trigger(values));
    spec.insert("prompt".into(), json!(values.prompt));
    spec.insert("launch".into(), launch(values));
    spec.insert("location".into(), location(values));
    Value::Object(spec)
}

fn string(value: &Value) -> String {
    value.as_str().unwrap_or_default().to_owned()
}

fn overrides_of(location: &Value) -> Vec<Override> {
    location["repository_sources"]["overrides"]
        .as_object()
        .map(|o| {
            o.iter()
                .map(|(repository, source)| Override {
                    repository: repository.clone(),
                    path: string(&source["path"]),
                })
                .collect()
        })
        .unwrap_or_default()
}

fn strings(value: &Value) -> Vec<String> {
    value
        .as_array()
        .map(|a| {
            a.iter()
                .filter_map(|v| v.as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default()
}

/// The values a stored definition holds, or why the form cannot edit it.
pub(crate) fn values_of(spec_json: &str, local_zone: &str) -> Result<Values, String> {
    let root: Value = serde_json::from_str(spec_json)
        .map_err(|_| "Automation spec is not valid JSON.".to_owned())?;
    if root["api_version"] != API_VERSION {
        return Err(format!(
            "Unrecognized automation spec api_version {}.",
            json_text(&root["api_version"])
        ));
    }
    let launch = &root["launch"];
    let trigger = &root["trigger"];
    let location = &root["location"];
    let mut values = Values {
        name: string(&root["name"]),
        id: root["id"].as_i64().unwrap_or(0),
        prompt: string(&root["prompt"]),
        time_zone: local_zone.to_owned(),
        agent: string(&launch["driver"]),
        model: string(&launch["model"]),
        effort: string(&launch["effort"]),
        executable: string(&launch["executable"]),
        ..Values::default()
    };
    match trigger["type"].as_str() {
        Some("manual") => match location["type"].as_str() {
            Some("repository_worktree") => {
                values.location = Location::Worktree;
                values.overrides = overrides_of(location);
            }
            Some("directory") => values.directory = string(&location["path"]),
            _ => return Err(UNEDITABLE_LOCATION.into()),
        },
        Some("scheduled") => {
            if location["type"] != "directory" {
                return Err(UNEDITABLE_LOCATION.into());
            }
            values.trigger = Trigger::Scheduled;
            values.directory = string(&location["path"]);
            values.cron = string(&trigger["schedule"]["cron"]);
            if let Some(zone) = trigger["schedule"]["time_zone"]
                .as_str()
                .filter(|z| !z.is_empty())
            {
                values.time_zone = zone.to_owned();
            }
            values.guard = string(&trigger["guard"]);
            if trigger["continuity"] == "singleton" {
                values.continuity = Continuity::Singleton;
            }
            values.catch_up = match trigger["catch_up"].as_str() {
                Some("skip") => CatchUp::Skip,
                Some("latest") => CatchUp::Latest,
                _ => CatchUp::Unchosen,
            };
        }
        Some("github_review_requested") => {
            values.trigger = Trigger::GitHub;
            values.include = strings(&trigger["repositories"]["include"]);
            values.exclude = strings(&trigger["repositories"]["exclude"]);
            values.overrides = overrides_of(location);
        }
        _ => {
            return Err(format!(
                "Unrecognized automation trigger type {}.",
                json_text(&trigger["type"])
            ));
        }
    }
    Ok(values)
}

/// A value as `JSON.stringify` writes it, `undefined` for one that is missing.
fn json_text(value: &Value) -> String {
    match value {
        Value::Null => "undefined".into(),
        other => other.to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn valid(trigger: Trigger) -> Values {
        Values {
            name: "Nightly".into(),
            trigger,
            cron: "0 9 * * *".into(),
            time_zone: "UTC".into(),
            catch_up: CatchUp::Latest,
            agent: "codex".into(),
            directory: "/tmp/work".into(),
            prompt: "Do the work".into(),
            include: vec!["github.com/acme/widgets".into()],
            ..Values::default()
        }
    }

    fn round_trip(values: &Values) -> Value {
        let written = spec(values);
        let back = values_of(&written.to_string(), "UTC").expect("reads back");
        assert_eq!(spec(&back), written, "spec survives a round trip");
        written
    }

    #[test]
    fn a_manual_spec_round_trips_and_omits_what_it_does_not_name() {
        let spec = round_trip(&valid(Trigger::Manual));
        assert_eq!(spec["trigger"], json!({"type": "manual"}));
        assert_eq!(spec["launch"], json!({"driver": "codex"}));
        assert!(spec.get("id").is_none());
        assert_eq!(
            spec["location"],
            json!({"type": "directory", "path": "/tmp/work"})
        );
    }

    #[test]
    fn a_scheduled_spec_round_trips_with_its_time_zone_and_no_empty_launch_keys() {
        let spec = round_trip(&valid(Trigger::Scheduled));
        assert_eq!(
            spec["trigger"],
            json!({"type": "scheduled", "schedule": {"cron": "0 9 * * *", "time_zone": "UTC"}, "continuity": "fresh", "catch_up": "latest"})
        );
        assert_eq!(spec["launch"], json!({"driver": "codex"}));
    }

    #[test]
    fn a_github_spec_round_trips_with_its_lists_and_overrides_in_canonical_form() {
        let mut values = valid(Trigger::GitHub);
        values.include = vec![
            "GitHub.com/Acme/Widgets".into(),
            "github.com/acme/widgets".into(),
        ];
        values.exclude = vec!["github.com/acme/legacy".into()];
        values.overrides = vec![Override {
            repository: "GitHub.com/Acme/Widgets".into(),
            path: "/src/widgets".into(),
        }];
        let written = spec(&values);
        assert_eq!(
            written["trigger"]["repositories"],
            json!({"include": ["github.com/acme/widgets"], "exclude": ["github.com/acme/legacy"]})
        );
        assert_eq!(
            written["location"]["repository_sources"],
            json!({"default": {"type": "managed_cache"}, "overrides": {"github.com/acme/widgets": {"type": "local_clone", "path": "/src/widgets"}}})
        );
        round_trip(&values);
        values.include.clear();
        values.exclude.clear();
        assert!(
            super::spec(&values)["trigger"]
                .get("repositories")
                .is_none()
        );
    }

    #[test]
    fn each_rule_names_its_field() {
        let mut empty_name = valid(Trigger::Manual);
        empty_name.name = "  ".into();
        let mut no_catch_up = valid(Trigger::Scheduled);
        no_catch_up.catch_up = CatchUp::Unchosen;
        let mut relative = valid(Trigger::Manual);
        relative.directory = "work".into();
        let mut malformed = valid(Trigger::GitHub);
        malformed.include = vec!["acme/widgets".into()];
        let mut twice = valid(Trigger::GitHub);
        twice.include = vec!["github.com/acme/w".into(), "GITHUB.com/acme/w".into()];
        let mut both = valid(Trigger::GitHub);
        both.exclude = vec!["github.com/acme/widgets".into()];
        let mut overridden_twice = valid(Trigger::GitHub);
        overridden_twice.overrides = vec![
            Override {
                repository: "github.com/a/b".into(),
                path: "/a".into(),
            },
            Override {
                repository: "github.com/a/b".into(),
                path: "b".into(),
            },
        ];
        let mut no_prompt = valid(Trigger::Manual);
        no_prompt.prompt = String::new();
        let cases: [(&Values, &[(Field, &str)]); 8] = [
            (&empty_name, &[(Field::Name, "A name is required.")]),
            (
                &no_catch_up,
                &[(Field::CatchUp, "Choose what happens to missed runs.")],
            ),
            (&relative, &[(Field::DirectoryPath, ABSOLUTE)]),
            (&malformed, &[(Field::Include(0), REPOSITORY)]),
            (&twice, &[(Field::Include(1), LISTED_TWICE)]),
            (&both, &[(Field::Exclude(0), BOTH_LISTS)]),
            (
                &overridden_twice,
                &[
                    (
                        Field::OverrideRepository(1),
                        "This repository already has a directory override.",
                    ),
                    (Field::OverridePath(1), ABSOLUTE),
                ],
            ),
            (&no_prompt, &[(Field::Prompt, "A prompt is required.")]),
        ];
        for (values, expected) in cases {
            let errors = validate(values);
            let expected: BTreeMap<Field, String> = expected
                .iter()
                .map(|(f, m)| (*f, (*m).to_owned()))
                .collect();
            assert_eq!(errors, expected, "{values:?}");
        }
        for trigger in [Trigger::Manual, Trigger::Scheduled, Trigger::GitHub] {
            assert!(
                validate(&valid(trigger)).is_empty(),
                "{trigger:?} fixture is valid"
            );
        }
    }

    #[test]
    fn model_and_effort_are_kept_as_stored_and_may_both_be_empty() {
        for (model, effort) in [
            ("", ""),
            ("gpt-5.6-luna", ""),
            ("", "future-effort"),
            ("retired", "future"),
        ] {
            let mut values = valid(Trigger::Manual);
            values.model = model.into();
            values.effort = effort.into();
            assert!(validate(&values).is_empty());
            let launch = &spec(&values)["launch"];
            assert_eq!(launch["model"].as_str().unwrap_or(""), model);
            assert_eq!(launch["effort"].as_str().unwrap_or(""), effort);
        }
    }

    #[test]
    fn a_scheduled_trigger_without_continuity_reads_as_fresh() {
        let spec = json!({
            "api_version": API_VERSION, "name": "n", "prompt": "p",
            "trigger": {"type": "scheduled", "schedule": {"cron": "0 9 * * *", "time_zone": "UTC"}, "catch_up": "skip"},
            "launch": {"driver": "codex"},
            "location": {"type": "directory", "path": "/tmp"},
        });
        let values = values_of(&spec.to_string(), "Europe/Oslo").unwrap();
        assert_eq!(values.continuity, Continuity::Fresh);
        assert_eq!(values.time_zone, "UTC");
    }
}
