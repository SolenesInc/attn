//! Settings → Delegation against a daemon that keeps the roles table as the real one does: each
//! save names the revision it edits, and one made against an older revision is refused.

use std::sync::{Arc, Mutex};

use attn_protocol::{
    DelegationChoice, DelegationFallback, DelegationPreferences,
    DelegationPreferencesChangedMessage, DelegationPreferencesResultMessage, DelegationRole,
    DelegationSelection, Harness, HarnessModelsResultMessage, SettingsUpdatedMessage, TierDefaults,
};
use attn_testkit::{Request, Target, World, message};
use rune::Key;
use serde_json::{Value, json};

use super::harness_catalogs::{
    enter_model, model, open_route, options, pick_effort, pick_model, picker_open, preview_harness,
    route_value,
};
use super::{accepting, click, open, press, rest, value};

const REVISION_CONFLICT: &str =
    "delegation preferences changed; reload before saving or choosing a role";
const FALLBACK_FIELD: &str = "settings.delegation.field.fallback-instructions";

type Expand = Box<dyn Fn(&[DelegationRole]) -> Vec<DelegationRole> + Send>;

/// The table the scripted daemon keeps, and how it answers.
struct Table {
    preferences: DelegationPreferences,
    templates: Vec<DelegationRole>,
    expanded: Expand,
    harnesses: Vec<Harness>,
    holding_saves: bool,
    held_saves: Vec<Request>,
    holding_loads: bool,
    held_loads: Vec<Request>,
    load_failure: String,
}

#[derive(Clone)]
struct Server(Arc<Mutex<Table>>);

#[derive(Default)]
struct Spec {
    roles: Vec<DelegationRole>,
    enabled: Option<bool>,
    templates: Vec<DelegationRole>,
    expanded: Option<Expand>,
    harnesses: Option<Vec<Harness>>,
}

fn codex() -> Harness {
    Harness {
        id: "codex".into(),
        name: "Codex".into(),
        available: true,
        model_pin: true,
        effort_pin: true,
        discovery: true,
    }
}

fn selection(harness: &str, provider: &str, model: &str, effort: &str) -> DelegationSelection {
    DelegationSelection {
        harness: harness.into(),
        provider: provider.into(),
        model: model.into(),
        effort: effort.into(),
    }
}

fn choice(id: &str, name: &str, when: &str, selection: DelegationSelection) -> DelegationChoice {
    DelegationChoice {
        id: id.into(),
        name: name.into(),
        when: when.into(),
        selection,
    }
}

fn custom(name_of_default: &str, instructions: &str) -> DelegationRole {
    DelegationRole {
        id: "build".into(),
        name: "Build".into(),
        icon: "code".into(),
        enabled: true,
        description: "Implement a change".into(),
        instructions: instructions.into(),
        default_choice_id: "default".into(),
        choices: vec![choice(
            "default",
            name_of_default,
            "",
            DelegationSelection::default(),
        )],
        builtin: None,
    }
}

fn template() -> DelegationRole {
    DelegationRole {
        id: "builder".into(),
        builtin: Some("builder".into()),
        name: String::new(),
        icon: String::new(),
        description: String::new(),
        instructions: String::new(),
        ..custom("Everyday", "")
    }
}

fn expanded_template() -> DelegationRole {
    DelegationRole {
        name: "Builder".into(),
        icon: "code".into(),
        description: "Implement a change\nSecond line".into(),
        instructions: "Run relevant tests".into(),
        ..template()
    }
}

fn expand(role: &DelegationRole) -> DelegationRole {
    if role.builtin.is_none() {
        return role.clone();
    }
    let shown = expanded_template();
    DelegationRole {
        name: shown.name,
        icon: shown.icon,
        description: shown.description,
        instructions: shown.instructions,
        ..role.clone()
    }
}

/// The editing tests' table: the Builder template, expanded as the daemon would.
fn with_builder(roles: Vec<DelegationRole>) -> Spec {
    Spec {
        roles,
        templates: vec![template()],
        expanded: Some(Box::new(|roles| {
            roles
                .iter()
                .map(expand)
                .chain([expanded_template()])
                .collect()
        })),
        ..Spec::default()
    }
}

impl Server {
    fn table(&self) -> std::sync::MutexGuard<'_, Table> {
        self.0.lock().unwrap()
    }

    fn preferences(&self) -> DelegationPreferences {
        self.table().preferences.clone()
    }

    fn result(table: &Table) -> Value {
        message(DelegationPreferencesResultMessage {
            success: true,
            preferences: Some(table.preferences.clone()),
            templates: Some(table.templates.clone()),
            expanded_roles: Some((table.expanded)(&table.preferences.roles)),
            harnesses: Some(table.harnesses.clone()),
            workflow_skill_paths: Some(Vec::new()),
            ..DelegationPreferencesResultMessage::new()
        })
    }

    fn refusal(error: &str) -> Value {
        message(DelegationPreferencesResultMessage {
            success: false,
            error: Some(error.into()),
            ..DelegationPreferencesResultMessage::new()
        })
    }

    fn answer_save(&self, save: &Request) -> Value {
        let mut table = self.table();
        let preferences: DelegationPreferences =
            serde_json::from_value(save["preferences"].clone()).expect("a saved table");
        if preferences.revision != table.preferences.revision {
            return Server::refusal(REVISION_CONFLICT);
        }
        table.preferences = DelegationPreferences {
            revision: preferences.revision + 1,
            ..preferences
        };
        Server::result(&table)
    }

    fn answer_load(&self) -> Value {
        let table = self.table();
        if table.load_failure.is_empty() {
            Server::result(&table)
        } else {
            Server::refusal(&table.load_failure)
        }
    }

    fn change_elsewhere(&self, change: impl FnOnce(&mut DelegationPreferences)) {
        let mut table = self.table();
        change(&mut table.preferences);
        table.preferences.revision += 1;
    }

    fn announce(&self, world: &mut World) {
        let revision = self.table().preferences.revision;
        world.daemon.emit(DelegationPreferencesChangedMessage {
            revision,
            ..DelegationPreferencesChangedMessage::new()
        });
        world.settle();
    }

    fn hold_saves(&self) {
        self.table().holding_saves = true;
    }

    fn answer_held_save(&self, world: &mut World) {
        let save = {
            let mut table = self.table();
            (!table.held_saves.is_empty()).then(|| table.held_saves.remove(0))
        };
        if let Some(save) = save {
            world.daemon.reply_to(&save, self.answer_save(&save));
        }
        world.settle();
    }

    fn resume_saves(&self, world: &mut World) {
        let held = {
            let mut table = self.table();
            table.holding_saves = false;
            std::mem::take(&mut table.held_saves)
        };
        for save in held {
            world.daemon.reply_to(&save, self.answer_save(&save));
        }
        world.settle();
    }

    fn hold_loads(&self) {
        self.table().holding_loads = true;
    }

    fn resume_loads(&self, world: &mut World) {
        let held = {
            let mut table = self.table();
            table.holding_loads = false;
            std::mem::take(&mut table.held_loads)
        };
        for load in held {
            world.daemon.reply_to(&load, self.answer_load());
        }
        world.settle();
    }

    fn fail_loads(&self, reason: &str) {
        self.table().load_failure = reason.into();
    }
}

fn everyday_catalog() -> HarnessModelsResultMessage {
    HarnessModelsResultMessage {
        success: true,
        models: vec![model(
            "codex",
            "model-a",
            "Everyday model",
            None,
            &["medium", "high"],
        )],
        detail: "Reported by Codex".into(),
        ..HarnessModelsResultMessage::new()
    }
}

/// The app on Settings → Delegation over a daemon keeping `spec`'s table.
fn on_delegation(spec: Spec) -> (World, Server) {
    let (daemon, _) = accepting(&[]);
    let enabled = spec.enabled.unwrap_or(!spec.roles.is_empty());
    let server = Server(Arc::new(Mutex::new(Table {
        preferences: DelegationPreferences {
            enabled,
            revision: 0,
            workflow_skill_enabled: false,
            roles: spec.roles,
            fallback: DelegationFallback::default(),
        },
        templates: spec.templates,
        expanded: spec
            .expanded
            .unwrap_or_else(|| Box::new(|roles| roles.to_vec())),
        harnesses: spec.harnesses.unwrap_or_else(|| vec![codex()]),
        holding_saves: false,
        held_saves: Vec::new(),
        holding_loads: false,
        held_loads: Vec::new(),
        load_failure: String::new(),
    })));
    let loads = server.clone();
    daemon.on("delegation_preferences_get", move |r| {
        if loads.table().holding_loads {
            loads.table().held_loads.push(r.clone());
            return Vec::new();
        }
        vec![loads.answer_load()]
    });
    let saves = server.clone();
    daemon.on("delegation_preferences_save", move |r| {
        if saves.table().holding_saves {
            saves.table().held_saves.push(r.clone());
            return Vec::new();
        }
        vec![saves.answer_save(r)]
    });
    daemon.on("harness_models", |_| message(everyday_catalog()));
    let mut world = World::start_sized(daemon, (1440.0, 1000.0));
    server.hold_loads();
    open(&mut world);
    click(&mut world, "settings.nav.delegation");
    server.resume_loads(&mut world);
    (world, server)
}

fn saves(world: &World) -> Vec<Request> {
    world.daemon.sent_of("delegation_preferences_save")
}

fn saved(save: &Request) -> DelegationPreferences {
    serde_json::from_value(save["preferences"].clone()).unwrap()
}

fn last_saved(world: &World) -> DelegationPreferences {
    saved(saves(world).last().expect("a save"))
}

fn loads(world: &World) -> usize {
    world.daemon.sent_of("delegation_preferences_get").len()
}

fn models_asked(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("harness_models")
        .iter()
        .map(|r| r["harness"].as_str().unwrap_or_default().to_owned())
        .collect()
}

fn press_named(world: &mut World, role: &str, name: &str) {
    world
        .automate("press", json!({"target": {"role": role, "name": name}}))
        .unwrap_or_else(|why| panic!("press the {role} {name}: {why}"));
}

fn button(world: &mut World, name: &str) {
    press_named(world, "button", name);
}

fn has_button(world: &World, name: &str) -> bool {
    world
        .screen()
        .find(Target::role("button").name(name))
        .is_some()
}

fn shown(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn alert(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("settings.delegation.error"))
        .and_then(|n| n.name)
}

fn described(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .description
        .unwrap_or_default()
}

fn status(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .name
        .unwrap_or_default()
}

/// Types `text` into the field `key` in place of what it holds, as one edit; each newline
/// goes in as Return.
fn type_into(world: &mut World, key: &str, text: &str) {
    click(world, key);
    for _ in 0..value(world, key).chars().count() {
        press(world, Key::Backspace);
    }
    for (i, line) in text.split('\n').enumerate() {
        if i > 0 {
            press(world, Key::Enter);
        }
        world.gesture(|app| app.type_text(line));
    }
}

/// Moves the focus out of the field `key`, by pressing its label.
fn leave_field(world: &mut World, key: &str) {
    click(world, &format!("{key}.label"));
    world.settle();
}

/// Focuses the field, replaces its text and leaves it.
fn write(world: &mut World, key: &str, text: &str) {
    type_into(world, key, text);
    leave_field(world, key);
}

fn open_fallback(spec: Spec) -> (World, Server) {
    let (mut world, server) = on_delegation(spec);
    button(&mut world, "Anything else");
    (world, server)
}

fn show_section_again(world: &mut World) {
    click(world, "settings.nav.connectivity");
    click(world, "settings.nav.delegation");
    world.settle();
}

fn sent_instructions(world: &World) -> Vec<(i64, String)> {
    saves(world)
        .iter()
        .map(saved)
        .map(|p| (p.revision, p.fallback.instructions))
        .collect()
}

#[test]
fn return_in_a_multi_line_field_breaks_the_line_and_saves_both_lines() {
    let (mut world, server) = open_fallback(Spec::default());
    click(&mut world, FALLBACK_FIELD);
    world.gesture(|app| app.type_text("first"));
    press(&mut world, Key::Enter);
    world.gesture(|app| app.type_text("second"));
    assert_eq!(value(&world, FALLBACK_FIELD), "first\nsecond");
    assert!(saves(&world).is_empty(), "Return does not save the field");

    leave_field(&mut world, FALLBACK_FIELD);
    assert_eq!(server.preferences().fallback.instructions, "first\nsecond");
}

#[test]
fn edits_made_during_a_save_collapse_into_one_follow_up_carrying_the_returned_revision() {
    let (mut world, server) = open_fallback(Spec::default());
    server.hold_saves();
    write(&mut world, FALLBACK_FIELD, "one");
    write(&mut world, FALLBACK_FIELD, "two");
    write(&mut world, FALLBACK_FIELD, "three");
    assert_eq!(value(&world, FALLBACK_FIELD), "three");
    assert_eq!(sent_instructions(&world), [(0, "one".to_owned())]);

    server.resume_saves(&mut world);
    assert_eq!(
        sent_instructions(&world),
        [(0, "one".to_owned()), (1, "three".to_owned())]
    );
    assert_eq!(server.preferences().fallback.instructions, "three");
    assert_eq!(loads(&world), 1);

    write(&mut world, FALLBACK_FIELD, "four");
    assert_eq!(sent_instructions(&world)[2], (2, "four".to_owned()));
    rest(&mut world);
}

#[test]
fn an_install_queued_behind_a_running_save_survives_the_edits_that_collapse_into_it() {
    let (mut world, server) = open_fallback(Spec {
        templates: vec![DelegationRole {
            name: "Builder".into(),
            ..template()
        }],
        ..Spec::default()
    });
    server.hold_saves();
    write(&mut world, FALLBACK_FIELD, "one");
    button(&mut world, "Add Attn roles");
    write(&mut world, FALLBACK_FIELD, "three");
    server.resume_saves(&mut world);
    let sent: Vec<(String, Option<bool>)> = saves(&world)
        .iter()
        .map(|s| {
            (
                saved(s).fallback.instructions,
                s["install_workflow_skill"].as_bool(),
            )
        })
        .collect();
    assert_eq!(
        sent,
        [("one".to_owned(), None), ("three".to_owned(), Some(true))]
    );
    let builtins: Vec<Option<String>> = server
        .preferences()
        .roles
        .into_iter()
        .map(|r| r.builtin)
        .collect();
    assert_eq!(builtins, [Some("builder".to_owned())]);
    rest(&mut world);
}

#[test]
fn undo_stays_when_the_push_announcing_its_own_save_reloads_the_same_revision() {
    let (mut world, server) = on_delegation(Spec {
        roles: vec![custom("Default", "")],
        ..Spec::default()
    });
    button(&mut world, "Build");
    button(&mut world, "Delete");
    assert_eq!(server.preferences().revision, 1);

    server.announce(&mut world);
    assert_eq!(loads(&world), 2);
    assert!(has_button(&world, "Undo"));
    rest(&mut world);
}

#[test]
fn an_edit_made_while_the_conflict_reload_is_loading_is_discarded() {
    let (mut world, server) = open_fallback(Spec::default());
    server.change_elsewhere(|_| {});
    server.hold_saves();
    server.hold_loads();
    write(&mut world, FALLBACK_FIELD, "one");
    server.answer_held_save(&mut world);
    assert!(alert(&world).unwrap().contains("reload before saving"));

    write(&mut world, FALLBACK_FIELD, "two");
    server.resume_loads(&mut world);
    assert_eq!(saves(&world).len(), 1);
    assert_eq!(server.preferences().fallback.instructions, "");
    assert_eq!(value(&world, FALLBACK_FIELD), "");
    assert!(alert(&world).unwrap().contains("reload before saving"));
    rest(&mut world);
}

#[test]
fn a_conflict_drops_the_local_edit_and_reloads_the_table() {
    let (mut world, server) = open_fallback(Spec::default());
    server.change_elsewhere(|p| p.fallback.instructions = "theirs".into());
    write(&mut world, FALLBACK_FIELD, "mine");
    assert!(alert(&world).unwrap().contains("reload before saving"));
    assert_eq!(value(&world, FALLBACK_FIELD), "theirs");
    assert_eq!(loads(&world), 2);

    write(&mut world, FALLBACK_FIELD, "mine again");
    assert_eq!(
        sent_instructions(&world),
        [(0, "mine".to_owned()), (1, "mine again".to_owned())]
    );
    rest(&mut world);
}

#[test]
fn a_push_while_idle_reloads_the_table() {
    let (mut world, server) = open_fallback(Spec::default());
    server.change_elsewhere(|p| p.fallback.instructions = "from elsewhere".into());
    server.announce(&mut world);
    assert_eq!(value(&world, FALLBACK_FIELD), "from elsewhere");
    assert_eq!(loads(&world), 2);
    assert!(saves(&world).is_empty());
    rest(&mut world);
}

#[test]
fn a_reload_asked_for_during_a_save_waits_for_the_save_to_drain() {
    let (mut world, server) = open_fallback(Spec::default());
    server.hold_saves();
    write(&mut world, FALLBACK_FIELD, "one");
    show_section_again(&mut world);
    button(&mut world, "Anything else");
    write(&mut world, FALLBACK_FIELD, "two");
    assert_eq!(loads(&world), 1);

    server.answer_held_save(&mut world);
    assert_eq!(saves(&world).len(), 2);
    assert_eq!(loads(&world), 1);
    assert_eq!(value(&world, FALLBACK_FIELD), "two");

    write(&mut world, FALLBACK_FIELD, "two!");
    server.resume_saves(&mut world);
    assert_eq!(server.preferences().fallback.instructions, "two!");
    assert_eq!(value(&world, FALLBACK_FIELD), "two!");
    assert_eq!(loads(&world), 2);
    rest(&mut world);
}

#[test]
fn a_failed_save_whose_recovery_load_fails_too_shows_the_last_confirmed_table() {
    let (mut world, server) = open_fallback(Spec::default());
    server.change_elsewhere(|p| p.fallback.instructions = "theirs".into());
    server.fail_loads("daemon unreachable");
    server.hold_saves();
    write(&mut world, FALLBACK_FIELD, "mine");
    assert_eq!(value(&world, FALLBACK_FIELD), "mine");

    server.resume_saves(&mut world);
    assert_eq!(loads(&world), 2);
    assert!(alert(&world).unwrap().contains("daemon unreachable"));
    assert_eq!(value(&world, FALLBACK_FIELD), "");

    server.fail_loads("");
    show_section_again(&mut world);
    button(&mut world, "Anything else");
    assert_eq!(alert(&world), None);
    assert_eq!(value(&world, FALLBACK_FIELD), "theirs");
    rest(&mut world);
}

#[test]
fn copying_a_maintained_role_keeps_its_configured_model_and_state_not_the_template() {
    let configured = DelegationRole {
        enabled: false,
        choices: vec![choice(
            "default",
            "Default",
            "",
            selection("codex", "", "model-a", "high"),
        )],
        ..template()
    };
    let (mut world, _) = on_delegation(with_builder(vec![configured]));
    button(&mut world, "Builder");
    button(&mut world, "Make an editable copy");
    assert_eq!(saves(&world).len(), 1);
    let copy = last_saved(&world).roles[1].clone();
    assert_eq!(copy.builtin, None);
    assert_eq!(copy.name, "Builder (custom)");
    assert_eq!(copy.instructions, "Run relevant tests");
    assert!(!copy.enabled);
    let s = &copy.choices[0].selection;
    assert_eq!(
        (&*s.harness, &*s.provider, &*s.model, &*s.effort),
        ("codex", "", "model-a", "high")
    );
    rest(&mut world);
}

#[test]
fn a_focused_draft_outlasts_a_reload_from_elsewhere_and_saves_when_the_focus_leaves() {
    let (mut world, server) =
        on_delegation(with_builder(vec![custom("Everyday", "Run relevant tests")]));
    button(&mut world, "Build");
    let field = "settings.delegation.field.instructions.build";
    type_into(&mut world, field, "Typed here");
    server.change_elsewhere(|p| p.roles[0].instructions = "Changed elsewhere".into());
    server.announce(&mut world);
    assert_eq!(loads(&world), 2);
    assert_eq!(value(&world, field), "Typed here");
    leave_field(&mut world, field);
    assert_eq!(saves(&world).len(), 1);
    assert_eq!(saved(&saves(&world)[0]).revision, 1);
    assert_eq!(server.preferences().roles[0].instructions, "Typed here");
    rest(&mut world);
}

#[test]
fn the_empty_table_adopts_the_maintained_roles_with_the_install_and_shows_their_guidance_read_only()
{
    let (mut world, _) = on_delegation(with_builder(Vec::new()));
    assert!(shown(&world, "settings.delegation.empty.heading"));
    assert!(models_asked(&world).is_empty());
    button(&mut world, "Add Attn roles");
    let all = saves(&world);
    assert_eq!(all.len(), 1);
    assert_eq!(all[0]["install_workflow_skill"], true);
    let adopted = saved(&all[0]);
    assert!(adopted.workflow_skill_enabled);
    assert_eq!(adopted.roles[0].builtin.as_deref(), Some("builder"));
    assert_eq!(adopted.roles[0].instructions, "");

    let row = described(&world, "settings.delegation.role.builder");
    assert!(row.contains("Implement a change"), "{row}");
    assert!(row.contains("Needs a model"), "{row}");
    button(&mut world, "Builder");
    assert_eq!(
        status(&world, "settings.delegation.role.builder.instructions"),
        "Run relevant tests"
    );
    assert!(!shown(
        &world,
        "settings.delegation.field.instructions.builder"
    ));
    assert!(has_button(&world, "Make an editable copy"));
    rest(&mut world);
}

#[test]
fn a_custom_role_is_guided_by_a_description_and_instructions() {
    let (mut world, _) = on_delegation(with_builder(Vec::new()));
    button(&mut world, "+ Custom role");
    assert_eq!(saves(&world).len(), 1);
    let role = &saves(&world)[0]["preferences"]["roles"][0];
    let mut keys: Vec<&String> = role.as_object().unwrap().keys().collect();
    keys.sort();
    assert_eq!(
        keys,
        [
            "choices",
            "default_choice_id",
            "description",
            "enabled",
            "icon",
            "id",
            "instructions",
            "name"
        ]
    );
    let id = role["id"].as_str().unwrap();
    assert!(shown(
        &world,
        &format!("settings.delegation.field.instructions.{id}")
    ));
    rest(&mut world);
}

#[test]
fn the_row_saves_a_picked_model_an_alternative_with_its_condition_and_the_switch() {
    let (mut world, server) =
        on_delegation(with_builder(vec![custom("Everyday", "Run relevant tests")]));
    open_route(&mut world, "Model for Build");
    assert!(
        world
            .screen()
            .descendants(Target::key("settings.route-picker"))
            .iter()
            .any(|n| n.name.as_deref() == Some("Choose a harness to see its models."))
    );
    preview_harness(&mut world, "Codex");
    assert!(saves(&world).is_empty());
    assert_eq!(models_asked(&world), ["codex"]);
    pick_model(&mut world, "Everyday model");
    assert_eq!(saves(&world).len(), 1);
    pick_effort(&mut world, "medium");
    assert_eq!(saves(&world).len(), 2);
    let s = last_saved(&world).roles[0].choices[0].selection.clone();
    assert_eq!(
        (&*s.harness, &*s.provider, &*s.model, &*s.effort),
        ("codex", "", "model-a", "medium")
    );
    assert!(!described(&world, "settings.delegation.role.build").contains("Needs a model"));
    assert!(!picker_open(&world));

    button(&mut world, "Build");
    button(&mut world, "+ Alternative model");
    assert_eq!(saves(&world).len(), 3);
    let alt = last_saved(&world).roles[0].choices[1].id.clone();
    write(
        &mut world,
        &format!("settings.delegation.field.alternative-name.build/{alt}"),
        "Hard verification",
    );
    assert_eq!(saves(&world).len(), 4);
    write(
        &mut world,
        &format!("settings.delegation.field.when.build/{alt}"),
        "Verification is difficult\n\nOr requirements are ambiguous.",
    );
    assert_eq!(saves(&world).len(), 5);
    let alternative = server.preferences().roles[0].choices[1].clone();
    assert_eq!(alternative.name, "Hard verification");
    assert!(alternative.when.contains("ambiguous"));
    let s = alternative.selection;
    assert_eq!(
        (&*s.harness, &*s.provider, &*s.model, &*s.effort),
        ("codex", "", "model-a", "medium")
    );
    assert_eq!(
        described(
            &world,
            &format!("settings.delegation.alternative.build/{alt}")
        ),
        "Verification is difficult"
    );

    button(&mut world, "Build");
    assert!(has_button(&world, "+1 alternative"));
    assert!(status(&world, "settings.delegation.live").starts_with("Agents see 1 of 1 role."));

    press_named(&mut world, "switch", "Delegation preferences");
    assert!(!server.preferences().enabled);
    assert!(
        !world
            .screen()
            .get(Target::role("switch").name("Delegation preferences"))
            .is("checked")
    );
    assert_eq!(server.preferences().roles[0].choices.len(), 2);
    assert_eq!(models_asked(&world).len(), 1);
    rest(&mut world);
}

#[test]
fn a_deleted_role_comes_back_with_undo_and_a_conflict_reloads_the_table() {
    let (mut world, server) =
        on_delegation(with_builder(vec![custom("Everyday", "Run relevant tests")]));
    button(&mut world, "Build");
    button(&mut world, "Delete");
    assert!(server.preferences().roles.is_empty());
    assert!(shown(&world, "settings.delegation.empty.heading"));
    button(&mut world, "Undo");
    assert_eq!(server.preferences().roles.len(), 1);
    assert!(has_button(&world, "Build"));

    server.change_elsewhere(|_| {});
    button(&mut world, "Build");
    button(&mut world, "Turn off");
    assert!(alert(&world).unwrap().contains("reload before saving"));
    assert!(server.preferences().roles[0].enabled);
    assert_eq!(loads(&world), 2);
    assert!(!described(&world, "settings.delegation.role.build").contains("Off"));
    rest(&mut world);
}

#[test]
fn a_maintained_role_shows_by_its_built_in_kind_and_a_missing_one_is_offered_by_name() {
    let configured = DelegationRole {
        id: "orchestrator".into(),
        builtin: Some("pathfinder".into()),
        ..template()
    };
    let pathfinder = DelegationRole {
        name: "Pathfinder".into(),
        icon: "search".into(),
        description: "Investigate the path".into(),
        instructions: "Find the path".into(),
        ..configured.clone()
    };
    let orchestrator = DelegationRole {
        id: "orchestrator".into(),
        builtin: Some("orchestrator".into()),
        name: "Orchestrator".into(),
        icon: "spark".into(),
        description: "Coordinate work".into(),
        instructions: "Coordinate".into(),
        ..template()
    };
    let views = vec![pathfinder, orchestrator.clone()];
    let (mut world, _) = on_delegation(Spec {
        roles: vec![configured],
        templates: vec![orchestrator],
        expanded: Some(Box::new(move |_| views.clone())),
        harnesses: Some(Vec::new()),
        ..Spec::default()
    });
    assert!(has_button(&world, "Pathfinder"));
    assert!(!has_button(&world, "Orchestrator"));
    button(&mut world, "+ Orchestrator");
    let builtins: Vec<Option<String>> = saved(&saves(&world)[0])
        .roles
        .into_iter()
        .map(|r| r.builtin)
        .collect();
    assert_eq!(
        builtins,
        [
            Some("pathfinder".to_owned()),
            Some("orchestrator".to_owned())
        ]
    );
    rest(&mut world);
}

#[test]
fn a_reload_from_elsewhere_withdraws_undo() {
    let (mut world, server) =
        on_delegation(with_builder(vec![custom("Everyday", "Run relevant tests")]));
    button(&mut world, "Build");
    button(&mut world, "Delete");
    assert!(server.preferences().roles.is_empty());
    assert!(has_button(&world, "Undo"));

    server.change_elsewhere(|_| {});
    server.announce(&mut world);
    assert_eq!(loads(&world), 2);
    assert!(!has_button(&world, "Undo"));
    assert!(server.preferences().roles.is_empty());
    rest(&mut world);
}

#[test]
fn the_delegation_switch_saving_withdraws_undo() {
    let (mut world, server) =
        on_delegation(with_builder(vec![custom("Everyday", "Run relevant tests")]));
    button(&mut world, "Build");
    button(&mut world, "Delete");
    assert!(has_button(&world, "Undo"));

    press_named(&mut world, "switch", "Delegation preferences");
    assert!(!server.preferences().enabled);
    assert!(!has_button(&world, "Undo"));
    rest(&mut world);
}

#[test]
fn the_fallback_row_stays_when_the_table_has_no_roles() {
    let (mut world, _) = on_delegation(Spec {
        enabled: Some(true),
        ..with_builder(Vec::new())
    });
    assert!(shown(&world, "settings.delegation.empty.heading"));
    assert!(has_button(&world, "Model for anything else"));
    rest(&mut world);
}

#[test]
fn showing_the_section_again_reloads_the_table_and_its_harnesses() {
    let (mut world, server) = on_delegation(Spec::default());
    assert_eq!(loads(&world), 1);
    server.table().harnesses.push(Harness {
        id: "plug".into(),
        name: "Plug".into(),
        discovery: false,
        ..codex()
    });
    show_section_again(&mut world);
    assert_eq!(loads(&world), 2);
    open_route(&mut world, "Model for anything else");
    assert!(
        options(&world, "harnesses")
            .iter()
            .any(|(name, _, _)| name == "Plug")
    );
    assert!(saves(&world).is_empty());
    rest(&mut world);
}

#[test]
fn making_an_alternative_the_default_keeps_the_former_default_as_an_alternative() {
    let role = DelegationRole {
        choices: vec![
            choice(
                "default",
                "Everyday",
                "",
                selection("codex", "", "model-a", ""),
            ),
            choice(
                "careful",
                "Hard verification",
                "Verification is difficult.",
                selection("codex", "", "model-b", "high"),
            ),
        ],
        ..custom("Everyday", "Run relevant tests")
    };
    let (mut world, server) = on_delegation(with_builder(vec![role]));
    button(&mut world, "Build");
    button(&mut world, "Hard verification");
    button(&mut world, "Make default");
    assert_eq!(saves(&world).len(), 1);
    let saved_role = server.preferences().roles[0].clone();
    assert_eq!(saved_role.default_choice_id, "careful");
    let ids: Vec<String> = saved_role.choices.into_iter().map(|c| c.id).collect();
    assert_eq!(ids, ["default", "careful"]);
    assert!(has_button(&world, "Everyday"));
    assert!(!has_button(&world, "Hard verification"));
    assert_eq!(
        described(&world, "settings.delegation.alternative.build/default"),
        "No condition yet. The agent cannot pick this."
    );
    assert_eq!(
        status(&world, "settings.delegation.undo"),
        "Made Hard verification the default for Build."
    );
    button(&mut world, "Undo");
    assert_eq!(saves(&world).len(), 2);
    assert_eq!(server.preferences().roles[0].default_choice_id, "default");
    assert!(models_asked(&world).is_empty());
    rest(&mut world);
}

fn picker_harnesses() -> Vec<Harness> {
    vec![
        Harness {
            id: "claude".into(),
            name: "Claude".into(),
            ..codex()
        },
        Harness {
            id: "house".into(),
            name: "House".into(),
            model_pin: false,
            effort_pin: false,
            discovery: false,
            ..codex()
        },
        Harness {
            id: "plug".into(),
            name: "Plug".into(),
            discovery: false,
            ..codex()
        },
    ]
}

fn claude_model(
    id: &str,
    name: &str,
    tier: Option<&str>,
    efforts: &[&str],
) -> attn_protocol::HarnessModel {
    model("claude", id, name, tier, efforts)
}

fn claude_catalog() -> HarnessModelsResultMessage {
    let both = &["medium", "high"];
    HarnessModelsResultMessage {
        success: true,
        tier_defaults: TierDefaults {
            deep: Some("opus".into()),
            standard: Some("sonnet".into()),
            light: Some("haiku".into()),
        },
        models: vec![
            claude_model("default", "Default (recommended)", None, both),
            claude_model("opus", "Opus", Some("deep"), both),
            claude_model("deep-two", "Second deep", Some("deep"), both),
            claude_model("haiku", "Haiku", Some("light"), &[]),
            claude_model("sonnet", "Sonnet", Some("standard"), &[]),
            claude_model("new-id", "New release", None, both),
        ],
        detail: "Reported by Claude".into(),
        ..HarnessModelsResultMessage::new()
    }
}

fn build_on(selection: DelegationSelection) -> DelegationRole {
    DelegationRole {
        choices: vec![choice("default", "Everyday", "", selection)],
        ..custom("Everyday", "Run relevant tests")
    }
}

/// The Build role on `selection`, its picker open over the harnesses and the Claude catalog;
/// `held` leaves each discovery unanswered.
fn picker(selection: DelegationSelection, held: bool) -> (World, Server, Arc<Mutex<Vec<Request>>>) {
    let (mut world, server) = on_delegation(Spec {
        roles: vec![build_on(selection)],
        harnesses: Some(picker_harnesses()),
        ..Spec::default()
    });
    let requests: Arc<Mutex<Vec<Request>>> = Arc::default();
    let asked = requests.clone();
    world.daemon.on("harness_models", move |r| {
        if held {
            asked.lock().unwrap().push(r.clone());
            return Vec::new();
        }
        vec![message(claude_catalog())]
    });
    open_route(&mut world, "Model for Build");
    (world, server, requests)
}

fn build_route(world: &World) -> (String, String, String, String) {
    let s = last_saved(world).roles[0].choices[0].selection.clone();
    (s.harness, s.provider, s.model, s.effort)
}

fn route_of(
    harness: &str,
    provider: &str,
    model: &str,
    effort: &str,
) -> (String, String, String, String) {
    (harness.into(), provider.into(), model.into(), effort.into())
}

fn close_picker(world: &mut World) {
    press(world, Key::Escape);
}

fn effort_field(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("settings.route-picker.effort"))
        .filter(|n| n.role != "group")
        .map(|n| n.value.unwrap_or_default())
}

fn picker_alert(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("settings.route-picker.error"))
        .and_then(|n| n.name)
}

const BUILD_CHIP: &str = "settings.route.delegation.build/default";

#[test]
fn an_unavailable_harness_stays_pickable_and_marked_without_reading_the_table_again() {
    let (mut world, _, _) = picker(selection("claude", "", "", ""), false);
    let availability = |world: &mut World, on: &str| {
        world.daemon.emit(SettingsUpdatedMessage {
            settings: Some([("claude_available".to_owned(), on.to_owned())].into()),
            ..SettingsUpdatedMessage::new()
        });
        world.settle();
    };
    availability(&mut world, "true");
    let reads = loads(&world);
    availability(&mut world, "false");
    let claude = world
        .screen()
        .get(Target::key("settings.route-picker.harness.claude"));
    assert!(!claude.is("disabled"));
    assert_eq!(
        claude.description.as_deref(),
        Some("Unavailable on this daemon")
    );
    assert_eq!(loads(&world), reads);
    rest(&mut world);
}

#[test]
fn a_failed_capability_refresh_keeps_the_confirmed_harnesses() {
    let (mut world, server, _) = picker(selection("claude", "", "opus", "high"), false);
    server.fail_loads("Harness refresh unavailable");
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("claude_cap_model_discovery".to_owned(), "false".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(
        options(&world, "harnesses")
            .iter()
            .any(|(name, _, _)| name.starts_with("Plug"))
    );
    preview_harness(&mut world, "Plug");
    assert!(saves(&world).is_empty());
    enter_model(&mut world, "openai/gpt-next", None);
    let s = server.preferences().roles[0].choices[0].selection.clone();
    assert_eq!(
        (s.harness, s.provider, s.model, s.effort),
        route_of("plug", "openai", "gpt-next", "")
    );
    rest(&mut world);
}

#[test]
fn one_discovery_serves_a_reopen_and_refresh_asks_again() {
    let (mut world, _, _) = picker(selection("claude", "", "", ""), false);
    assert_eq!(models_asked(&world).len(), 1);
    close_picker(&mut world);
    open_route(&mut world, "Model for Build");
    assert_eq!(models_asked(&world).len(), 1);
    click(&mut world, "settings.route-picker.refresh");
    let last = world.daemon.sent_of("harness_models").pop().unwrap();
    assert_eq!(last["harness"], "claude");
    assert_eq!(last["refresh"], true);
    rest(&mut world);
}

#[test]
fn models_group_by_tier_in_reported_order_with_the_alias_first() {
    let (mut world, _, _) = picker(selection("claude", "", "", ""), false);
    let names: Vec<String> = options(&world, "models")
        .into_iter()
        .map(|(n, _, _)| n)
        .collect();
    assert_eq!(
        names,
        [
            "Claude default",
            "Default (recommended) no tier",
            "Opus",
            "Second deep",
            "Sonnet",
            "Haiku",
            "New release no tier"
        ]
    );
    rest(&mut world);
}

#[test]
fn a_model_and_effort_save_as_complete_routes_and_the_effort_closes_the_picker() {
    let (mut world, _, _) = picker(selection("claude", "", "", ""), false);
    pick_model(&mut world, "Opus");
    assert_eq!(build_route(&world), route_of("claude", "", "opus", ""));
    pick_effort(&mut world, "high");
    assert_eq!(build_route(&world), route_of("claude", "", "opus", "high"));
    assert!(!picker_open(&world));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some(BUILD_CHIP)
    );
    rest(&mut world);
}

#[test]
fn browsing_another_harness_neither_saves_nor_wipes_the_model() {
    let (mut world, _, _) = picker(selection("claude", "", "opus", "high"), false);
    preview_harness(&mut world, "Plug");
    assert!(saves(&world).is_empty());
    close_picker(&mut world);
    let chip = route_value(&world, BUILD_CHIP);
    assert!(chip.contains("Opus") && chip.contains("high"), "{chip}");
    rest(&mut world);
}

#[test]
fn a_stored_missing_model_and_effort_stay_shown_without_a_save_on_open() {
    let (mut world, _, _) = picker(selection("claude", "", "retired", "future-effort"), false);
    assert!(
        options(&world, "models")
            .iter()
            .any(|(name, selected, _)| name.contains("retired") && *selected)
    );
    assert_eq!(effort_field(&world).as_deref(), Some("future-effort"));
    assert!(saves(&world).is_empty());
    rest(&mut world);
}

#[test]
fn an_exact_provider_qualified_model_saves_on_a_harness_without_discovery() {
    let (mut world, _, _) = picker(selection("claude", "", "", ""), false);
    preview_harness(&mut world, "Plug");
    enter_model(&mut world, "openai/gpt-next", None);
    assert_eq!(
        build_route(&world),
        route_of("plug", "openai", "gpt-next", "")
    );
    assert_eq!(models_asked(&world), ["claude"]);
    rest(&mut world);
}

#[test]
fn a_known_unsupported_manual_model_is_refused_while_an_unknown_provider_is_accepted() {
    for (by_enter, query) in [
        (false, "vendor/blocked"),
        (true, "vendor/blocked"),
        (false, "blocked"),
        (true, "blocked"),
    ] {
        let house = Harness {
            id: "house".into(),
            name: "House".into(),
            ..codex()
        };
        let (mut world, _) = on_delegation(Spec {
            roles: vec![build_on(selection("house", "", "", ""))],
            harnesses: Some(vec![house]),
            ..Spec::default()
        });
        world.daemon.on("harness_models", |_| {
            let blocked = |provider: &str, name: &str, access: &str| attn_protocol::HarnessModel {
                provider: provider.into(),
                access: access.into(),
                ..model("house", "blocked", name, Some("deep"), &["medium", "high"])
            };
            message(HarnessModelsResultMessage {
                success: true,
                models: vec![
                    blocked("vendor", "Blocked model", "unsupported"),
                    blocked("other", "Other provider model", "supported"),
                ],
                ..HarnessModelsResultMessage::new()
            })
        });
        open_route(&mut world, "Model for Build");
        let submit = |world: &mut World, query: &str| {
            if by_enter {
                click(world, "settings.route-picker.filter");
                press(world, Key::Enter);
            } else {
                world
                    .automate(
                        "press",
                        json!({"target": {"role": "button", "name": format!("Use “{query}”")}}),
                    )
                    .unwrap_or_else(|why| panic!("press Use “{query}”: {why}"));
                world.settle();
            }
        };
        let retype = |world: &mut World, key: &str, text: &str| {
            click(world, key);
            for _ in 0..value(world, key).chars().count() {
                press(world, Key::Backspace);
            }
            world.gesture(|app| app.type_text(text));
        };
        retype(&mut world, "settings.route-picker.filter", query);
        if query == "blocked" {
            retype(&mut world, "settings.route-picker.provider", "vendor");
        }
        submit(&mut world, query);
        assert!(saves(&world).is_empty(), "{query} by enter {by_enter}");
        let manual = world
            .screen()
            .find(Target::key("settings.route-picker.manual"));
        assert!(
            manual.is_some_and(|m| m.is("disabled")),
            "{query} by enter {by_enter}: {}",
            world.screen()
        );
        retype(&mut world, "settings.route-picker.provider", "");
        let unknown = if query == "blocked" {
            "blocked"
        } else {
            "unknown/blocked"
        };
        retype(&mut world, "settings.route-picker.filter", unknown);
        if query == "blocked" {
            retype(&mut world, "settings.route-picker.provider", "unknown");
        }
        submit(&mut world, unknown);
        assert_eq!(
            build_route(&world),
            route_of("house", "unknown", "blocked", ""),
            "{query} by enter {by_enter}"
        );
        rest(&mut world);
    }
}

#[test]
fn a_harness_that_chooses_its_model_commits_only_when_its_default_row_is_chosen() {
    let (mut world, _, _) = picker(DelegationSelection::default(), false);
    preview_harness(&mut world, "House");
    assert!(saves(&world).is_empty());
    pick_model(&mut world, "House default");
    assert_eq!(build_route(&world), route_of("house", "", "", ""));
    assert!(models_asked(&world).is_empty());
    rest(&mut world);
}

#[test]
fn a_plugin_that_discovers_but_cannot_pin_offers_only_its_default() {
    for stored in ["locked", ""] {
        let house = Harness {
            id: "house".into(),
            name: "House".into(),
            model_pin: false,
            effort_pin: false,
            ..codex()
        };
        let (mut world, _) = on_delegation(Spec {
            roles: vec![build_on(selection("house", "", stored, "high"))],
            harnesses: Some(vec![house]),
            ..Spec::default()
        });
        world.daemon.on("harness_models", |_| {
            message(HarnessModelsResultMessage {
                success: true,
                models: vec![model(
                    "house",
                    "locked",
                    "Locked model",
                    Some("deep"),
                    &["medium", "high"],
                )],
                tier_defaults: TierDefaults {
                    deep: Some("locked".into()),
                    ..TierDefaults::default()
                },
                ..HarnessModelsResultMessage::new()
            })
        });
        open_route(&mut world, "Model for Build");
        close_picker(&mut world);
        let chip = route_value(&world, BUILD_CHIP);
        let expected = if stored.is_empty() {
            "House default"
        } else {
            "Locked model"
        };
        assert!(chip.contains(expected), "{chip}");
        assert!(saves(&world).is_empty());
        open_route(&mut world, "Model for Build");
        let names: Vec<String> = options(&world, "models")
            .into_iter()
            .map(|(n, _, _)| n)
            .collect();
        assert_eq!(names, ["House default"]);
        assert!(!shown(&world, "settings.route-picker.filter"));
        pick_model(&mut world, "House default");
        assert_eq!(build_route(&world), route_of("house", "", "", ""));
        rest(&mut world);
    }
}

#[test]
fn effort_stays_free_when_the_harness_chooses_the_model() {
    for alias in [false, true] {
        let house = Harness {
            id: "house".into(),
            name: "House".into(),
            ..codex()
        };
        let (mut world, _) = on_delegation(Spec {
            roles: vec![build_on(selection("house", "", "pinned", "high"))],
            harnesses: Some(vec![house]),
            ..Spec::default()
        });
        world.daemon.on("harness_models", move |_| {
            let mut models = vec![
                model("house", "first", "First reported", None, &[]),
                model("house", "pinned", "Pinned model", Some("deep"), &["high"]),
            ];
            if alias {
                models.push(model("house", "default", "Default alias", None, &["low"]));
            }
            message(HarnessModelsResultMessage {
                success: true,
                models,
                ..HarnessModelsResultMessage::new()
            })
        });
        open_route(&mut world, "Model for Build");
        pick_model(&mut world, "House default");
        assert_eq!(build_route(&world), route_of("house", "", "", "high"));
        assert_eq!(effort_field(&world).as_deref(), Some("high"));
        assert!(
            world
                .screen()
                .find(Target::role("group").name("Effort"))
                .is_none()
        );
        rest(&mut world);
    }
}

#[test]
fn a_discovery_in_flight_is_shared_over_a_close_and_reopen() {
    let (mut world, _, requests) = picker(selection("claude", "", "", ""), true);
    assert_eq!(
        status(&world, "settings.route-picker.loading"),
        "Discovering models…"
    );
    close_picker(&mut world);
    open_route(&mut world, "Model for Build");
    let held = requests.lock().unwrap().clone();
    assert_eq!(held.len(), 1);
    world.daemon.reply_to(&held[0], claude_catalog());
    world.settle();
    assert!(
        options(&world, "models")
            .iter()
            .any(|(name, _, _)| name.starts_with("Opus"))
    );
    rest(&mut world);
}

#[test]
fn a_stored_effort_stays_editable_when_discovery_fails() {
    let (mut world, _, requests) = picker(selection("claude", "", "retired", "future"), true);
    let held = requests.lock().unwrap().clone();
    world.daemon.reply_to(
        &held[0],
        HarnessModelsResultMessage {
            success: false,
            error: Some("Catalog unavailable".into()),
            ..HarnessModelsResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(effort_field(&world).as_deref(), Some("future"));
    assert_eq!(picker_alert(&world).as_deref(), Some("Catalog unavailable"));
    rest(&mut world);
}

#[test]
fn an_empty_catalog_reads_differently_from_a_failed_discovery() {
    let (mut world, _, requests) = picker(selection("claude", "", "", ""), true);
    let held = requests.lock().unwrap().clone();
    world.daemon.reply_to(
        &held[0],
        HarnessModelsResultMessage {
            success: true,
            detail: "No discovery API".into(),
            ..HarnessModelsResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(
        status(&world, "settings.route-picker.detail"),
        "No discovery API"
    );
    assert_eq!(picker_alert(&world), None);
    world.daemon.on("harness_models", |_| {
        message(HarnessModelsResultMessage {
            success: false,
            error: Some("Harness unavailable".into()),
            ..HarnessModelsResultMessage::new()
        })
    });
    click(&mut world, "settings.route-picker.refresh");
    assert_eq!(picker_alert(&world).as_deref(), Some("Harness unavailable"));
    rest(&mut world);
}

#[test]
fn none_clears_the_route_and_closes_the_picker() {
    let (mut world, _, _) = picker(selection("claude", "", "opus", ""), false);
    click(&mut world, "settings.route-picker.harness.none");
    assert_eq!(build_route(&world), route_of("", "", "", ""));
    assert!(!picker_open(&world));
    rest(&mut world);
}

#[test]
fn a_typed_effort_saves_when_a_press_outside_closes_the_picker() {
    let (mut world, _, _) = picker(selection("claude", "", "retired", ""), false);
    click(&mut world, "settings.route-picker.effort");
    world.gesture(|app| app.type_text(" max "));
    click(&mut world, "settings.heading");
    assert_eq!(
        build_route(&world),
        route_of("claude", "", "retired", "max")
    );
    assert!(!picker_open(&world));
    rest(&mut world);
}
