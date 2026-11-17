use std::sync::{Arc, Mutex};

use attn_protocol::{
    AutomationApplyResultMessage, AutomationDefinitionResultMessage, AutomationDefinitionSummary,
    AutomationDeleteResultMessage, AutomationSchedulePreviewResultMessage,
    AutomationSetEnabledResultMessage,
};
use attn_testkit::{Answer, Daemon, Target, World, message};
use serde_json::{Value, json};

use super::super::settings::harness_catalogs::{self, harnesses};
use super::{Scene, changed, click, definition, has, is, name, opened_with};

const API_VERSION: &str = "attn.dev/automations/v1alpha1";
const NEXT_RUNS: [&str; 5] = [
    "2030-06-03T09:00:00Z",
    "2030-06-04T09:00:00Z",
    "2030-06-05T09:00:00Z",
    "2030-06-06T09:00:00Z",
    "2030-06-07T09:00:00Z",
];
const FORM: &str = "automations.form";

fn manual_spec(name: &str) -> Value {
    json!({
        "api_version": API_VERSION,
        "id": 1,
        "name": name,
        "trigger": {"type": "manual"},
        "prompt": "Do the work",
        "launch": {"driver": "codex", "model": "gpt-5.6-luna", "effort": "medium"},
        "location": {"type": "directory", "path": "/tmp/work"},
    })
}

fn with(mut spec: Value, key: &str, value: Value) -> Value {
    spec[key] = value;
    spec
}

fn scheduled_spec(cron: &str) -> Value {
    with(
        manual_spec("PR reviewer"),
        "trigger",
        json!({"type": "scheduled", "schedule": {"cron": cron, "time_zone": "UTC"}, "continuity": "fresh", "catch_up": "latest"}),
    )
}

fn github_spec() -> Value {
    json!({
        "api_version": API_VERSION,
        "id": 1,
        "name": "Reviewer",
        "trigger": {"type": "github_review_requested", "repositories": {"include": ["github.com/acme/widgets"]}},
        "prompt": "Review the PR",
        "launch": {"driver": "claude", "model": "sonnet", "effort": "medium"},
        "location": {"type": "repository_worktree", "repository_sources": {
            "default": {"type": "managed_cache"},
            "overrides": {"github.com/acme/widgets": {"type": "local_clone", "path": "/home/user/widgets"}},
        }},
    })
}

/// What the scripted daemon stores for the definition the form edits.
#[derive(Clone)]
struct Stored {
    definition: Arc<Mutex<AutomationDefinitionSummary>>,
    spec: Arc<Mutex<Value>>,
    yaml: Arc<Mutex<String>>,
}

impl Stored {
    fn of(definition: AutomationDefinitionSummary, spec: Value) -> Stored {
        Stored {
            definition: Arc::new(Mutex::new(definition)),
            spec: Arc::new(Mutex::new(spec)),
            yaml: Arc::default(),
        }
    }
}

fn serve(daemon: &Daemon, stored: &Stored) {
    let s = stored.clone();
    daemon.on("automation_definition_get", move |_| {
        message(AutomationDefinitionResultMessage {
            success: true,
            spec_json: Some(s.spec.lock().unwrap().to_string()),
            spec_yaml: Some(s.yaml.lock().unwrap().clone()),
            definition: Some(s.definition.lock().unwrap().clone()),
            ..AutomationDefinitionResultMessage::new()
        })
    });
    daemon.on("automation_apply", |r| {
        let id = r["expected_id"].as_i64().filter(|id| *id != 0).unwrap_or(4);
        message(AutomationApplyResultMessage {
            success: true,
            definition: Some(AutomationDefinitionSummary {
                revision: 2,
                ..definition(id, "PR reviewer")
            }),
            spec_yaml: Some(String::new()),
            ..AutomationApplyResultMessage::new()
        })
    });
    daemon.on("automation_schedule_preview", |_| {
        message(AutomationSchedulePreviewResultMessage {
            success: true,
            times: Some(NEXT_RUNS.iter().map(|t| (*t).to_owned()).collect()),
            ..AutomationSchedulePreviewResultMessage::new()
        })
    });
    daemon.on("automation_delete", |_| {
        message(AutomationDeleteResultMessage {
            success: true,
            ..AutomationDeleteResultMessage::new()
        })
    });
    daemon.on("automation_set_enabled", |_| {
        message(AutomationSetEnabledResultMessage {
            success: true,
            ..AutomationSetEnabledResultMessage::new()
        })
    });
    harness_catalogs::serve(daemon, &Arc::default(), harnesses());
}

fn open_scene(stored: &Stored, script: impl FnOnce(&Daemon)) -> World {
    let scene = Scene::of(vec![stored.definition.lock().unwrap().clone()]);
    opened_with(&scene, |daemon| {
        serve(daemon, stored);
        script(daemon);
    })
}

fn open_new_with(script: impl FnOnce(&Daemon)) -> World {
    let stored = Stored::of(definition(1, "PR reviewer"), manual_spec("PR reviewer"));
    let mut world = open_scene(&stored, script);
    click(&mut world, "automations.new");
    world
}

fn open_new() -> World {
    open_new_with(|_| {})
}

fn open_edit_with(stored: &Stored, script: impl FnOnce(&Daemon)) -> World {
    let mut world = open_scene(stored, script);
    click(&mut world, "automations.edit.1");
    world
}

fn open_edit(definition: AutomationDefinitionSummary, spec: Value) -> World {
    open_edit_with(&Stored::of(definition, spec), |_| {})
}

fn open_schedule(cron: &str) -> World {
    open_edit(
        AutomationDefinitionSummary {
            trigger_type: "scheduled".into(),
            ..definition(1, "PR reviewer")
        },
        scheduled_spec(cron),
    )
}

fn key(name: &str) -> String {
    format!("{FORM}.{name}")
}

fn press(world: &mut World, name: &str) {
    click(world, &key(name));
}

fn value(world: &World, name: &str) -> String {
    world
        .screen()
        .get(Target::key(key(name)))
        .value
        .unwrap_or_default()
}

/// Puts `text` in place of what the field holds.
fn fill(world: &mut World, name: &str, text: &str) {
    let field = key(name);
    world.gesture(|app| {
        app.click(Target::key(field));
        app.press("mod+a");
        app.press("backspace");
        app.type_text(text);
    });
}

fn fill_manual(world: &mut World) {
    fill(world, "name", "My automation");
    fill(world, "directory", "/tmp/work");
    fill(world, "prompt", "Do the work");
}

/// Each spec the app applied, with the id and revision it expected.
fn applied(world: &World) -> Vec<(Value, i64, i64)> {
    world
        .daemon
        .sent_of("automation_apply")
        .iter()
        .map(|r| {
            (
                serde_json::from_str(r["definition_yaml"].as_str().unwrap()).unwrap(),
                r["expected_id"].as_i64().unwrap(),
                r["expected_revision"].as_i64().unwrap(),
            )
        })
        .collect()
}

fn last_preview(world: &World) -> Value {
    world
        .daemon
        .sent_of("automation_schedule_preview")
        .last()
        .cloned()
        .expect("a preview was asked for")
}

fn route(world: &World) -> String {
    harness_catalogs::route_value(world, "settings.route.automation")
}

fn choose(world: &mut World, select: &str, option: &str) {
    let trigger = format!("{}-trigger", key(select));
    world.gesture(|app| app.click(Target::key(trigger)));
    world.gesture(|app| app.click(Target::role("list-box-option").name(option)));
}

/// Presses the segment named `segment` where it shows. The segmented controls key their
/// segments alike, so a segment is found by its name and pressed by its place.
fn pick(world: &mut World, segment: &str) {
    let [x, y, w, h] = world
        .screen()
        .bounds(Target::role("radio-button").name(segment))
        .unwrap_or_else(|| panic!("no segment {segment}"));
    world.gesture(|app| app.click_at((x + w / 2.0, y + h / 2.0)));
}

/// The segment picked in the segmented control keyed `name`.
fn picked(world: &World, name: &str) -> String {
    world
        .screen()
        .descendants(Target::key(key(name)))
        .into_iter()
        .find(|n| n.role == "radio-button" && n.is("checked"))
        .and_then(|n| n.name)
        .unwrap_or_default()
}

fn list_items(world: &World, list: &str) -> usize {
    world
        .screen()
        .descendants(Target::key(key(list)))
        .iter()
        .filter(|n| n.role == "list-item")
        .count()
}

#[test]
fn a_new_form_takes_the_list_s_place_reads_nothing_and_cancel_goes_back_without_saving() {
    let mut world = open_new();
    assert!(has(&world, FORM));
    assert!(!has(&world, "automations.list"));
    assert!(world.daemon.sent_of("automation_definition_get").is_empty());
    assert!(!has(&world, &key("enabled")));
    assert!(!has(&world, &key("id")));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key),
        Some(key("name"))
    );

    fill(&mut world, "name", "Unsaved");
    press(&mut world, "cancel");
    assert!(!has(&world, FORM));
    assert!(has(&world, "automations.list"));
    assert!(applied(&world).is_empty());
}

#[test]
fn editing_reads_the_definition_and_a_change_elsewhere_keeps_what_is_typed() {
    let mut world = open_edit(definition(1, "PR reviewer"), manual_spec("PR reviewer"));
    let read: Vec<Value> = world
        .daemon
        .sent_of("automation_definition_get")
        .iter()
        .map(|r| r["definition_id"].clone())
        .collect();
    assert_eq!(read, [1]);
    assert_eq!(value(&world, "name"), "PR reviewer");
    assert_eq!(name(&world, &key("id")).as_deref(), Some("ID: 1"));

    fill(&mut world, "name", "still typing");
    let listed = world.daemon.sent_of("automation_definitions_get").len();
    changed(&mut world);
    assert!(world.daemon.sent_of("automation_definitions_get").len() > listed);
    assert_eq!(world.daemon.sent_of("automation_definition_get").len(), 1);
    assert_eq!(value(&world, "name"), "still typing");
}

#[test]
fn a_new_manual_automation_applies_the_spec_it_built_and_the_list_shows_again() {
    let mut world = open_new();
    fill_manual(&mut world);
    press(&mut world, "save");

    let mut expected = with(
        manual_spec("My automation"),
        "launch",
        json!({"driver": "codex"}),
    );
    expected.as_object_mut().unwrap().remove("id");
    assert_eq!(applied(&world), [(expected, 0, 0)]);
    assert!(!has(&world, FORM));
    assert!(has(&world, "automations.list"));
}

#[test]
fn the_form_holds_while_the_daemon_applies_it() {
    let mut world = open_new_with(|daemon| {
        daemon.answer_in_turn("automation_apply", [Answer::Hold]);
    });
    fill_manual(&mut world);
    press(&mut world, "save");
    assert_eq!(applied(&world).len(), 1);
    assert!(is(&world, &key("name"), "disabled"));
    assert_eq!(name(&world, &key("save")).as_deref(), Some("Saving…"));
}

#[test]
fn a_rule_speaks_once_its_field_is_left_and_stops_once_it_is_met_and_save_checks_every_rule() {
    let mut world = open_new();
    let prompt_error = format!("{}.error", key("prompt"));
    click(&mut world, &key("prompt"));
    assert!(!has(&world, &prompt_error));
    click(&mut world, &key("name"));
    assert_eq!(
        name(&world, &prompt_error).as_deref(),
        Some("A prompt is required.")
    );

    fill(&mut world, "prompt", "Review it");
    assert!(!has(&world, &prompt_error));

    press(&mut world, "save");
    assert_eq!(
        name(&world, &format!("{}.error", key("name"))).as_deref(),
        Some("A name is required.")
    );
    assert_eq!(
        name(&world, &format!("{}.error", key("directory"))).as_deref(),
        Some("Must be an absolute path.")
    );
    assert_eq!(
        world.screen().focused().and_then(|n| n.key),
        Some(key("name"))
    );
    assert!(applied(&world).is_empty());
}

#[test]
fn a_new_schedule_saves_its_guard_and_missed_runs_in_the_zone_of_this_machine() {
    let mut world = open_new();
    assert!(!has(&world, &key("guard")));
    press(&mut world, "trigger-scheduled");
    let zone = value(&world, "time-zone");
    assert!(!zone.is_empty());
    fill_manual(&mut world);
    fill(&mut world, "cron", "0 9 * * *");
    press(&mut world, "save");
    assert_eq!(applied(&world)[0].0["trigger"]["catch_up"], "latest");

    let mut world = open_new();
    press(&mut world, "trigger-scheduled");
    fill_manual(&mut world);
    fill(&mut world, "cron", "0 9 * * *");
    press(&mut world, "advanced");
    pick(&mut world, "Skip");
    fill(&mut world, "guard", "git rev-parse HEAD");
    press(&mut world, "save");
    assert_eq!(
        applied(&world),
        [(
            with(
                json!({
                    "api_version": API_VERSION,
                    "name": "My automation",
                    "prompt": "Do the work",
                    "launch": {"driver": "codex"},
                    "location": {"type": "directory", "path": "/tmp/work"},
                }),
                "trigger",
                json!({
                    "type": "scheduled",
                    "schedule": {"cron": "0 9 * * *", "time_zone": zone},
                    "guard": "git rev-parse HEAD",
                    "continuity": "fresh",
                    "catch_up": "skip",
                })
            ),
            0,
            0
        )]
    );
}

#[test]
fn switching_back_to_manual_hides_and_omits_the_guard() {
    let mut world = open_new();
    press(&mut world, "trigger-scheduled");
    fill(&mut world, "guard", "git rev-parse HEAD");
    press(&mut world, "trigger-manual");
    assert!(!has(&world, &key("guard")));
    fill_manual(&mut world);
    press(&mut world, "save");
    let sent = applied(&world);
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0].0["trigger"], json!({"type": "manual"}));
}

#[test]
fn a_cron_the_controls_cannot_write_opens_on_the_cron_tab_and_saves_unchanged() {
    for cron in ["0 9 * * MON-FRI", "@every 90m", "0 9,17 * * *"] {
        let mut world = open_schedule(cron);
        assert_eq!(picked(&world, "schedule-tabs"), "Cron", "{cron}");
        assert_eq!(value(&world, "cron"), cron);
        press(&mut world, "save");
        assert_eq!(
            applied(&world)[0].0["trigger"]["schedule"],
            json!({"cron": cron, "time_zone": "UTC"})
        );
    }
}

#[test]
fn a_schedule_the_controls_can_write_opens_on_its_tab() {
    let world = open_schedule("*/10 7-23 * * *");
    assert_eq!(picked(&world, "schedule-tabs"), "Every");
    assert_eq!(value(&world, "schedule-every-n-trigger"), "10");
    assert!(is(&world, &key("schedule-window"), "checked"));
    assert_eq!(value(&world, "schedule-window-from-trigger"), "07:00");
    assert_eq!(value(&world, "schedule-window-to-trigger"), "23:59");
}

#[test]
fn the_controls_build_a_cron_and_the_daemon_previews_its_next_runs() {
    let mut world = open_new();
    press(&mut world, "trigger-scheduled");
    let zone = value(&world, "time-zone");
    pick(&mut world, "Every");
    choose(&mut world, "schedule-every-n", "10");
    press(&mut world, "schedule-window");

    assert_eq!(value(&world, "cron"), "*/10 9-17 * * 1-5");
    let preview = last_preview(&world);
    assert_eq!(
        (preview["cron"].as_str(), preview["time_zone"].as_str()),
        (Some("*/10 9-17 * * 1-5"), Some(zone.as_str()))
    );
    assert_eq!(list_items(&world, "next-runs"), 5);
}

#[test]
fn a_zone_the_user_types_is_previewed_and_saved_and_one_the_daemon_refuses_holds_save() {
    let mut world = open_schedule("0 9 * * 1-5");
    fill(&mut world, "time-zone", "Europe/Stockholm");
    assert_eq!(last_preview(&world)["time_zone"], "Europe/Stockholm");
    press(&mut world, "save");
    assert_eq!(
        applied(&world)[0].0["trigger"]["schedule"]["time_zone"],
        "Europe/Stockholm"
    );

    let mut world = open_schedule("0 9 * * 1-5");
    refuse_previews(&world.daemon);
    fill(&mut world, "time-zone", "Europe/St");
    assert_eq!(value(&world, "time-zone"), "Europe/St");
    assert!(is(&world, &key("save"), "disabled"));
}

fn suggested_zones(world: &World) -> Vec<String> {
    let suggestions = format!("{}-content", key("time-zones"));
    if !has(world, &suggestions) {
        return Vec::new();
    }
    world
        .screen()
        .descendants(Target::key(suggestions))
        .into_iter()
        .filter(|n| n.role == "list-box-option")
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn typing_part_of_a_zone_suggests_its_full_name_and_choosing_it_sets_the_schedule_s_zone() {
    let mut world = open_schedule("0 9 * * 1-5");
    fill(&mut world, "time-zone", "stockh");
    assert_eq!(suggested_zones(&world), ["Europe/Stockholm"]);

    world.gesture(|app| app.click(Target::role("list-box-option").name("Europe/Stockholm")));
    assert_eq!(value(&world, "time-zone"), "Europe/Stockholm");
    assert!(suggested_zones(&world).is_empty());
    assert_eq!(last_preview(&world)["time_zone"], "Europe/Stockholm");
    press(&mut world, "save");
    assert_eq!(
        applied(&world)[0].0["trigger"]["schedule"]["time_zone"],
        "Europe/Stockholm"
    );
}

#[test]
fn an_old_name_of_a_zone_or_a_fixed_offset_suggests_the_zone_and_choosing_it_saves_it() {
    for (typed, zone) in [
        ("Calcutta", "Asia/Kolkata"),
        ("Kiev", "Europe/Kyiv"),
        ("Saigon", "Asia/Ho_Chi_Minh"),
        ("Etc/GMT+5", "Etc/GMT+5"),
    ] {
        let mut world = open_schedule("0 9 * * 1-5");
        fill(&mut world, "time-zone", typed);
        assert_eq!(suggested_zones(&world), [zone], "typing {typed}");

        world.gesture(|app| app.click(Target::role("list-box-option").name(zone)));
        assert_eq!(last_preview(&world)["time_zone"], zone);
        press(&mut world, "save");
        assert_eq!(
            applied(&world)[0].0["trigger"]["schedule"]["time_zone"],
            zone
        );
    }
}

#[test]
fn the_arrow_keys_walk_the_suggested_zones_return_chooses_one_and_escape_hides_them() {
    let mut world = open_schedule("0 9 * * 1-5");
    fill(&mut world, "time-zone", "america/new_");
    assert_eq!(suggested_zones(&world), ["America/New_York"]);
    world.gesture(|app| {
        app.press("down");
        app.press("enter");
    });
    assert_eq!(value(&world, "time-zone"), "America/New_York");
    assert!(suggested_zones(&world).is_empty());

    world.gesture(|app| app.press("down"));
    assert_eq!(suggested_zones(&world), ["America/New_York"]);
    world.gesture(|app| app.press("escape"));
    assert!(suggested_zones(&world).is_empty());
    assert!(has(&world, &key("time-zone")));
}

fn refuse_previews(daemon: &Daemon) {
    daemon.on("automation_schedule_preview", |_| {
        message(AutomationSchedulePreviewResultMessage {
            success: false,
            error: Some("schedule.cron must be a valid cron expression".into()),
            ..AutomationSchedulePreviewResultMessage::new()
        })
    });
}

#[test]
fn a_cron_the_daemon_refuses_says_why_and_holds_save_until_it_is_valid() {
    let mut world = open_schedule("0 9 * * 1-5");
    assert!(!is(&world, &key("save"), "disabled"));
    assert_eq!(list_items(&world, "next-runs"), 5);
    refuse_previews(&world.daemon);
    fill(&mut world, "cron", "99 99 99 99 99");
    assert!(
        name(&world, &format!("{}.error", key("schedule")))
            .unwrap()
            .contains("must be a valid cron expression")
    );
    assert!(is(&world, &key("save"), "disabled"));
    assert_eq!(list_items(&world, "next-runs"), 0);
}

#[test]
fn a_schedule_s_own_tab_leaves_it_alone_and_a_friendly_tab_replaces_a_custom_cron_with_undo() {
    let mut world = open_schedule("*/5 * * * *");
    pick(&mut world, "Every");
    assert_eq!(value(&world, "cron"), "*/5 * * * *");

    let mut world = open_schedule("0 9 * * MON-FRI");
    pick(&mut world, "On days");
    assert_eq!(value(&world, "cron"), "0 9 * * *");
    assert!(
        world
            .screen()
            .descendants(Target::key(key("schedule-replaced")))
            .iter()
            .any(|n| n
                .name
                .as_deref()
                .is_some_and(|s| s.contains("0 9 * * MON-FRI")))
    );
    press(&mut world, "schedule-undo");
    assert_eq!(value(&world, "cron"), "0 9 * * MON-FRI");
    assert!(!has(&world, &key("schedule-replaced")));
}

#[test]
fn a_worktree_automation_and_a_driver_the_form_does_not_offer_save_unchanged() {
    let worktree = with(
        manual_spec("PR reviewer"),
        "location",
        json!({"type": "repository_worktree", "repository_sources": {
            "default": {"type": "managed_cache"},
            "overrides": {"github.com/acme/widgets": {"type": "local_clone", "path": "/home/user/widgets"}},
        }}),
    );
    let mut world = open_edit(
        AutomationDefinitionSummary {
            location_type: Some("repository_worktree".into()),
            ..definition(1, "PR reviewer")
        },
        worktree.clone(),
    );
    assert!(!has(&world, &key("directory")));
    press(&mut world, "advanced");
    assert_eq!(picked(&world, "location"), "Pull request worktree");
    press(&mut world, "save");
    assert_eq!(applied(&world), [(worktree, 1, 1)]);

    let plugin = with(
        manual_spec("PR reviewer"),
        "launch",
        json!({"driver": "my-plugin", "model": "m1"}),
    );
    let mut world = open_edit(definition(1, "PR reviewer"), plugin);
    press(&mut world, "save");
    assert_eq!(
        applied(&world)[0].0["launch"],
        json!({"driver": "my-plugin", "model": "m1"})
    );
}

#[test]
fn the_stored_definition_shows_read_only_and_advanced_names_only_what_differs() {
    let yaml = "name: PR reviewer\ntrigger: {type: manual}\n";
    let stored = Stored::of(definition(1, "PR reviewer"), manual_spec("PR reviewer"));
    *stored.yaml.lock().unwrap() = yaml.into();
    let mut world = open_edit_with(&stored, |_| {});
    press(&mut world, "advanced");
    assert!(!has(&world, &key("definition")));
    press(&mut world, "definition-toggle");
    assert_eq!(name(&world, &key("definition")).as_deref(), Some(yaml));
    assert_eq!(
        name(&world, &key("definition-copy")).as_deref(),
        Some("Copy")
    );
    assert!(!has(&world, &key("executable")));

    let singleton = with(
        manual_spec("PR reviewer"),
        "trigger",
        json!({"type": "scheduled", "schedule": {"cron": "0 9 * * *", "time_zone": "UTC"}, "continuity": "singleton", "catch_up": "latest"}),
    );
    let world = open_edit(
        AutomationDefinitionSummary {
            trigger_type: "scheduled".into(),
            ..definition(1, "PR reviewer")
        },
        singleton,
    );
    let said = world
        .screen()
        .get(Target::key(key("advanced")))
        .description
        .unwrap_or_default();
    assert!(said.contains("Same agent"), "{said}");
    assert!(!said.contains("Run latest"), "{said}");
}

#[test]
fn a_stored_guard_reads_back_and_clearing_it_drops_it_from_the_spec() {
    let spec = with(
        manual_spec("PR reviewer"),
        "trigger",
        json!({"type": "scheduled", "schedule": {"cron": "0 9 * * *", "time_zone": "UTC"}, "guard": "git rev-parse HEAD", "continuity": "singleton", "catch_up": "latest"}),
    );
    let mut world = open_edit(
        AutomationDefinitionSummary {
            trigger_type: "scheduled".into(),
            ..definition(1, "PR reviewer")
        },
        spec,
    );
    assert_eq!(value(&world, "guard"), "git rev-parse HEAD");
    fill(&mut world, "guard", "");
    press(&mut world, "save");
    assert_eq!(
        applied(&world)[0].0["trigger"],
        json!({"type": "scheduled", "schedule": {"cron": "0 9 * * *", "time_zone": "UTC"}, "continuity": "singleton", "catch_up": "latest"})
    );
}

#[test]
fn a_stored_launch_saves_unchanged_whatever_the_catalog_reports() {
    for launch in [
        json!({"driver": "codex", "model": "retired-model", "effort": "future-effort"}),
        json!({"driver": "codex", "effort": "future-effort"}),
        json!({"driver": "codex"}),
        json!({"driver": "claude", "model": "override-model", "effort": "override-effort", "executable": "/opt/alternate-claude"}),
    ] {
        let spec = with(manual_spec("PR reviewer"), "launch", launch.clone());
        let mut world = open_edit(definition(1, "PR reviewer"), spec);
        for named in [&launch["model"], &launch["effort"]] {
            if let Some(named) = named.as_str() {
                assert!(
                    route(&world).contains(named),
                    "{} shows {named}",
                    route(&world)
                );
            }
        }
        if launch == json!({"driver": "codex"}) {
            assert!(route(&world).contains("Codex default"), "{}", route(&world));
        }
        press(&mut world, "save");
        assert_eq!(applied(&world)[0].0["launch"], launch);
    }
}

#[test]
fn an_executable_override_stops_discovery_and_codex_offers_none() {
    let spec = with(
        manual_spec("PR reviewer"),
        "launch",
        json!({"driver": "claude", "model": "override-model", "effort": "override-effort", "executable": "/opt/alternate-claude"}),
    );
    let world = open_edit(definition(1, "PR reviewer"), spec);
    assert!(world.daemon.sent_of("harness_models").is_empty());

    let mut world = open_edit(definition(1, "PR reviewer"), manual_spec("PR reviewer"));
    press(&mut world, "advanced");
    assert!(!has(&world, &key("executable")));
}

#[test]
fn a_new_automation_starts_on_the_codex_defaults_and_saves_the_model_and_effort_picked() {
    let mut world = open_new();
    assert!(route(&world).starts_with("Codex"), "{}", route(&world));
    harness_catalogs::open_route(&mut world, "Automation model");
    let offered: Vec<String> = harness_catalogs::options(&world, "harnesses")
        .into_iter()
        .map(|(name, ..)| name)
        .collect();
    assert_eq!(offered, ["Claude", "Codex"]);
    harness_catalogs::preview_harness(&mut world, "Claude");
    assert_eq!(
        form_state(&mut world)["spec"]["launch"],
        json!({"driver": "codex"}),
        "browsing commits nothing"
    );
    harness_catalogs::pick_model(&mut world, "Opus 5.5");
    harness_catalogs::pick_effort(&mut world, "high");
    fill_manual(&mut world);
    press(&mut world, "save");
    assert_eq!(
        applied(&world)[0].0["launch"],
        json!({"driver": "claude", "model": "opus", "effort": "high"})
    );
}

#[test]
fn a_github_definition_reads_into_its_fields_and_saves_against_the_revision_it_read() {
    let mut world = open_edit(
        AutomationDefinitionSummary {
            revision: 7,
            trigger_type: "github_review_requested".into(),
            ..definition(1, "Reviewer")
        },
        github_spec(),
    );
    assert!(
        world
            .screen()
            .descendants(Target::key(key("repositories-include-chip.0")))
            .iter()
            .any(|n| n.name.as_deref() == Some("github.com/acme/widgets"))
    );
    assert!(route(&world).contains("Sonnet 5.5"), "{}", route(&world));
    press(&mut world, "save");
    assert_eq!(applied(&world), [(github_spec(), 1, 7)]);
}

#[test]
fn a_repository_entry_is_canonical_and_a_malformed_or_doubled_one_blocks_save() {
    let mut world = open_new();
    press(&mut world, "trigger-github");
    fill(&mut world, "name", "Reviewer");
    fill(&mut world, "prompt", "Review the PR");
    fill(
        &mut world,
        "repositories-include",
        "GitHub.com/Acme/Widgets",
    );
    world.gesture(|app| app.press("enter"));
    fill(
        &mut world,
        "repositories-exclude",
        "github.com/acme/widgets",
    );
    world.gesture(|app| app.press("enter"));
    assert_eq!(
        name(&world, &format!("{}.error", key("repositories-exclude"))).as_deref(),
        Some("This repository is both included and excluded.")
    );
    press(&mut world, "save");
    assert!(applied(&world).is_empty());

    click(&mut world, &key("repositories-exclude-remove.0"));
    press(&mut world, "save");
    assert_eq!(
        applied(&world)[0].0["trigger"],
        json!({"type": "github_review_requested", "repositories": {"include": ["github.com/acme/widgets"]}})
    );
}

#[test]
fn a_definition_changed_elsewhere_offers_reload_which_shows_what_is_there_now() {
    let stored = Stored::of(
        AutomationDefinitionSummary {
            revision: 3,
            ..definition(1, "PR reviewer")
        },
        manual_spec("Original"),
    );
    let mut world = open_edit_with(&stored, |daemon| {
        daemon.on("automation_apply", |_| {
            message(AutomationApplyResultMessage {
                success: false,
                error: Some(
                    "automation definition changed elsewhere — reload before saving".into(),
                ),
                error_code: Some("revision_conflict".into()),
                ..AutomationApplyResultMessage::new()
            })
        });
    });
    assert_eq!(value(&world, "name"), "Original");
    press(&mut world, "save");
    assert!(has(&world, &key("stale")));

    *stored.definition.lock().unwrap() = AutomationDefinitionSummary {
        revision: 4,
        ..definition(1, "PR reviewer")
    };
    *stored.spec.lock().unwrap() = manual_spec("Changed elsewhere");
    press(&mut world, "reload");
    assert_eq!(value(&world, "name"), "Changed elsewhere");
    assert!(!has(&world, &key("stale")));
    assert_eq!(world.daemon.sent_of("automation_definition_get").len(), 2);

    stored_applies(&world.daemon);
    press(&mut world, "save");
    assert_eq!(applied(&world).last().unwrap().2, 4);
}

fn stored_applies(daemon: &Daemon) {
    daemon.on("automation_apply", |r| {
        message(AutomationApplyResultMessage {
            success: true,
            definition: Some(definition(
                r["expected_id"].as_i64().unwrap(),
                "PR reviewer",
            )),
            ..AutomationApplyResultMessage::new()
        })
    });
}

#[test]
fn the_switch_turns_the_definition_off_and_delete_asks_twice_and_forgets_a_first_press() {
    let mut world = open_edit(definition(1, "PR reviewer"), manual_spec("PR reviewer"));
    assert!(is(&world, &key("enabled"), "checked"));
    press(&mut world, "enabled");
    let sent: Vec<(Value, Value)> = world
        .daemon
        .sent_of("automation_set_enabled")
        .iter()
        .map(|r| (r["definition_id"].clone(), r["enabled"].clone()))
        .collect();
    assert_eq!(sent, [(json!(1), json!(false))]);
    assert!(!is(&world, &key("enabled"), "checked"));

    press(&mut world, "delete");
    assert_eq!(
        name(&world, &key("delete")).as_deref(),
        Some("Confirm delete")
    );
    click(&mut world, &key("name"));
    assert_eq!(name(&world, &key("delete")).as_deref(), Some("Delete"));
    assert!(world.daemon.sent_of("automation_delete").is_empty());

    press(&mut world, "delete");
    press(&mut world, "delete");
    let deleted: Vec<Value> = world
        .daemon
        .sent_of("automation_delete")
        .iter()
        .map(|r| r["definition_id"].clone())
        .collect();
    assert_eq!(deleted, [1]);
    assert!(!has(&world, FORM));
}

fn form_state(world: &mut World) -> Value {
    world
        .automate("state", json!({"of": "automations"}))
        .unwrap()["form"]
        .clone()
}

#[test]
fn a_model_typed_by_hand_saves_when_the_harness_list_fails() {
    let spec = with(
        manual_spec("PR reviewer"),
        "launch",
        json!({"driver": "codex"}),
    );
    let mut world = open_edit_with(&Stored::of(definition(1, "PR reviewer"), spec), |daemon| {
        daemon.on("delegation_preferences_get", |_| {
            message(attn_protocol::DelegationPreferencesResultMessage {
                success: false,
                error: Some("Harness list unavailable".into()),
                ..attn_protocol::DelegationPreferencesResultMessage::new()
            })
        });
    });
    harness_catalogs::open_route(&mut world, "Automation model");
    harness_catalogs::enter_model(&mut world, "custom-model", None);
    world.gesture(|app| app.press("escape"));
    assert!(route(&world).contains("custom-model"), "{}", route(&world));
    press(&mut world, "save");
    assert_eq!(
        applied(&world)[0].0["launch"],
        json!({"driver": "codex", "model": "custom-model"})
    );
}

fn serve_launch_desktops(daemon: &Daemon) {
    daemon.on("launch_desktop_get", |r| {
        message(attn_protocol::LaunchDesktopResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            action: "launch_desktop_get".into(),
            success: true,
            desktops: Some(vec![attn_protocol::Desktop {
                id: "review".into(),
                profile_id: "work".into(),
                name: "Review".into(),
                ..attn_protocol::Desktop::default()
            }]),
            ..attn_protocol::LaunchDesktopResultMessage::new()
        })
    });
}

fn choose_desktop(world: &mut World, select: &str, option: &str) {
    click(world, &format!("{FORM}.{select}.desktop-trigger"));
    world
        .automate(
            "press",
            json!({"target": {"role": "option", "name": option}}),
        )
        .unwrap_or_else(|why| panic!("choose {option}: {why}"));
}

#[test]
fn a_new_automation_offers_the_profile_s_desktops_and_saves_the_one_chosen() {
    let mut world = open_new_with(serve_launch_desktops);
    choose_desktop(&mut world, "new", "Review");
    let read = &world.daemon.sent_of("launch_desktop_get")[0];
    assert_eq!(
        (read["kind"].as_str(), read["item_id"].as_str()),
        (Some("automation"), Some(""))
    );
    fill_manual(&mut world);
    press(&mut world, "save");
    assert_eq!(
        world.daemon.sent_of("automation_apply")[0]["launch_desktop_setting"],
        json!({"desktop_id": "review"})
    );
}

#[test]
fn a_new_named_desktop_goes_beside_the_definition_and_never_into_its_spec() {
    let stored = Stored::of(
        AutomationDefinitionSummary {
            profile_id: "work".into(),
            ..definition(1, "PR reviewer")
        },
        manual_spec("PR reviewer"),
    );
    let mut world = open_edit_with(&stored, serve_launch_desktops);
    choose_desktop(&mut world, "1", "A new desktop…");
    assert_eq!(
        world
            .screen()
            .get(Target::key("launch-desktop.name"))
            .value
            .as_deref(),
        Some("PR reviewer")
    );
    world.gesture(|app| {
        app.press("mod+a");
        app.type_text("Checks");
        app.press("enter");
    });
    press(&mut world, "save");
    let request = &world.daemon.sent_of("automation_apply")[0];
    assert_eq!(
        request["launch_desktop_setting"],
        json!({"desktop_name": "Checks"})
    );
    assert_eq!(request["profile_id"], "work");
    assert!(applied(&world)[0].0.get("launch_desktop").is_none());
}

fn reopen_drawer(world: &mut World) {
    click(world, "titlebar.automations");
    assert!(!has(world, "automations.list"));
    click(world, "titlebar.automations");
}

#[test]
fn a_save_answered_after_its_form_closed_leaves_the_next_form_and_its_edits_alone() {
    let mut world = open_new_with(|daemon| {
        daemon.answer_in_turn("automation_apply", [Answer::Hold]);
    });
    fill_manual(&mut world);
    press(&mut world, "save");
    reopen_drawer(&mut world);
    click(&mut world, "automations.new");
    fill(&mut world, "name", "Second draft");

    let save = world.daemon.sent_of("automation_apply")[0].clone();
    world.daemon.reply_to(
        &save,
        AutomationApplyResultMessage {
            success: true,
            definition: Some(definition(4, "My automation")),
            spec_yaml: Some(String::new()),
            ..AutomationApplyResultMessage::new()
        },
    );
    world.settle();

    assert!(has(&world, FORM));
    assert_eq!(value(&world, "name"), "Second draft");
}

#[test]
fn a_read_answered_after_its_form_closed_never_fills_the_next_form() {
    let first = Stored::of(definition(1, "PR reviewer"), manual_spec("PR reviewer"));
    let mut world = open_scene(&first, |daemon| {
        daemon.on("automation_definitions_get", |_| {
            message(attn_protocol::AutomationDefinitionsResultMessage {
                success: true,
                definitions: Some(vec![definition(1, "PR reviewer"), definition(2, "Triage")]),
                ..attn_protocol::AutomationDefinitionsResultMessage::new()
            })
        });
        daemon.answer_in_turn("automation_definition_get", [Answer::Hold, Answer::Hold]);
    });
    click(&mut world, "automations.edit.1");
    reopen_drawer(&mut world);
    click(&mut world, "automations.edit.2");
    let reads = world.daemon.sent_of("automation_definition_get");
    assert_eq!(reads.len(), 2);

    let answer = |id: i64, name: &str| AutomationDefinitionResultMessage {
        success: true,
        spec_json: Some(with(manual_spec(name), "id", json!(id)).to_string()),
        spec_yaml: Some(String::new()),
        definition: Some(definition(id, name)),
        ..AutomationDefinitionResultMessage::new()
    };
    world.daemon.reply_to(&reads[0], answer(1, "PR reviewer"));
    world.settle();
    assert!(!has(&world, &key("id")));
    assert!(!has(&world, &key("name")));

    world.daemon.reply_to(&reads[1], answer(2, "Triage"));
    world.settle();
    assert_eq!(name(&world, &key("id")).as_deref(), Some("ID: 2"));
    fill(&mut world, "name", "Triage, renamed");
    press(&mut world, "save");
    let saves = applied(&world);
    assert_eq!(saves.len(), 1);
    assert_eq!(saves[0].0["name"], "Triage, renamed");
    assert_eq!(saves[0].1, 2);
}

#[test]
fn the_title_bar_closes_the_drawer_while_a_form_field_holds_the_keyboard() {
    let mut world = open_new();
    fill_manual(&mut world);
    click(&mut world, "titlebar.automations");
    assert!(!has(&world, FORM));
    assert!(!has(&world, "automations.list"));
}
