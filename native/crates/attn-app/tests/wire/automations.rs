use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{
    AutomationDefinitionSummary, AutomationDefinitionsResultMessage, AutomationGuardCheck,
    AutomationProvenance, AutomationRunFix, AutomationRunResultMessage, AutomationRunSummary,
    AutomationRunsResultMessage, AutomationSetEnabledResultMessage, AutomationsChangedMessage,
    Desktop, InitialStateMessage, Profile, ProfileArrangementChangedMessage,
};
use attn_testkit::{Daemon, Target, World, message};
use serde_json::Value;

use super::queue::{ago, one_desktop};
use super::sidebar::session;

mod form;
mod runs;

/// A window tall enough that the whole form shows without scrolling.
const TALL: (f32, f32) = (1280.0, 2400.0);

pub(super) fn definition(id: i64, name: &str) -> AutomationDefinitionSummary {
    AutomationDefinitionSummary {
        id,
        profile_id: "default".into(),
        name: name.into(),
        enabled: true,
        revision: 1,
        trigger_type: "manual".into(),
        updated_at: ago(60),
        ..AutomationDefinitionSummary::default()
    }
}

pub(super) fn run(id: &str) -> AutomationRunSummary {
    AutomationRunSummary {
        id: id.into(),
        definition_id: 1,
        state: "delivered".into(),
        created_at: ago(60),
        updated_at: ago(60),
        ..AutomationRunSummary::default()
    }
}

/// What the scripted daemon lists, which a test changes before saying automations changed.
#[derive(Clone, Default)]
pub(super) struct Scene {
    pub(super) definitions: Arc<Mutex<Vec<AutomationDefinitionSummary>>>,
    pub(super) runs: Arc<Mutex<Vec<AutomationRunSummary>>>,
}

impl Scene {
    pub(super) fn of(definitions: Vec<AutomationDefinitionSummary>) -> Scene {
        let scene = Scene::default();
        *scene.definitions.lock().unwrap() = definitions;
        scene
    }

    pub(super) fn with_runs(self, runs: Vec<AutomationRunSummary>) -> Scene {
        *self.runs.lock().unwrap() = runs;
        self
    }

    pub(super) fn serve(&self, daemon: &Daemon) {
        let definitions = self.definitions.clone();
        daemon.on("automation_definitions_get", move |_| {
            message(AutomationDefinitionsResultMessage {
                success: true,
                definitions: Some(definitions.lock().unwrap().clone()),
                ..AutomationDefinitionsResultMessage::new()
            })
        });
        let runs = self.runs.clone();
        daemon.on("automation_runs_get", move |r| {
            message(AutomationRunsResultMessage {
                success: true,
                definition_id: r["definition_id"].as_i64().unwrap(),
                runs: Some(runs.lock().unwrap().clone()),
                ..AutomationRunsResultMessage::new()
            })
        });
    }
}

fn two_agents() -> InitialStateMessage {
    one_desktop(vec![session("s1", "idle"), session("s2", "idle")])
}

pub(super) fn opened_with(scene: &Scene, script: impl FnOnce(&Daemon)) -> World {
    let daemon = super::palette::daemon(two_agents(), vec![]);
    scene.serve(&daemon);
    script(&daemon);
    let mut world = World::start_sized(daemon, TALL);
    click(&mut world, "titlebar.automations");
    world
}

pub(super) fn opened(scene: &Scene) -> World {
    opened_with(scene, |_| {})
}

pub(super) fn click(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(key)));
}

pub(super) fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

pub(super) fn name(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

pub(super) fn is(world: &World, key: &str, state: &str) -> bool {
    world.screen().get(Target::key(key)).is(state)
}

pub(super) fn changed(world: &mut World) {
    world.daemon.emit(AutomationsChangedMessage {
        definition_ids: vec![1],
        ..AutomationsChangedMessage::new()
    });
    world.settle();
}

fn listed(world: &World) -> usize {
    world.daemon.sent_of("automation_definitions_get").len()
}

fn run_ids(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("automation_run")
        .iter()
        .map(|r| r["request_id"].as_str().unwrap().to_owned())
        .collect()
}

fn refuse_runs(daemon: &Daemon, error: &'static str) {
    daemon.on("automation_run", move |_| {
        message(AutomationRunResultMessage {
            success: false,
            error: Some(error.into()),
            ..AutomationRunResultMessage::new()
        })
    });
}

#[test]
fn nothing_is_read_until_the_drawer_opens_and_the_title_bar_closes_it_again() {
    let daemon = super::palette::daemon(two_agents(), vec![]);
    Scene::of(vec![definition(1, "PR reviewer")]).serve(&daemon);
    let mut world = World::start(daemon);
    world.settle();
    assert_eq!(listed(&world), 0);

    assert_eq!(
        name(&world, "titlebar.automations").as_deref(),
        Some("Show Automations")
    );
    click(&mut world, "titlebar.automations");
    assert!(has(&world, "automations"));
    assert_eq!(listed(&world), 1);
    assert_eq!(
        name(&world, "titlebar.automations").as_deref(),
        Some("Hide Automations")
    );

    click(&mut world, "titlebar.automations");
    assert!(!has(&world, "automations"));
}

#[test]
fn escape_closes_the_drawer_and_the_palette_opens_it() {
    let scene = Scene::of(vec![definition(1, "PR reviewer")]);
    let mut world = opened(&scene);
    world.gesture(|app| app.press("escape"));
    assert!(!has(&world, "automations"));

    super::palette::run_command(&mut world, "Show automations");
    assert!(has(&world, "automations"));
}

#[test]
fn an_empty_list_offers_the_first_automation_in_the_app() {
    let mut world = opened(&Scene::of(vec![]));
    assert!(has(&world, "automations.empty"));
    click(&mut world, "automations.new-empty");
    assert!(has(&world, "automations.form"));
    assert!(world.daemon.sent_of("automation_definition_get").is_empty());
}

#[test]
fn each_definition_says_what_triggers_it_and_offers_run_now() {
    let scheduled = AutomationDefinitionSummary {
        trigger_type: "scheduled".into(),
        schedule_cron: Some("0 9 * * *".into()),
        schedule_time_zone: Some("UTC".into()),
        ..definition(2, "Nightly digest")
    };
    let github = AutomationDefinitionSummary {
        trigger_type: "github_review_requested".into(),
        ..definition(3, "Recurring reviewer")
    };
    let mut world = opened(&Scene::of(vec![
        definition(1, "PR reviewer"),
        scheduled,
        github,
    ]));

    let rows = world
        .screen()
        .children(Target::key("automations.list"))
        .len();
    assert_eq!(rows, 3);
    let state = world.automate("state", serde_json::json!({"of": "automations"}));
    let triggers: Vec<String> = state.unwrap()["definitions"]
        .as_array()
        .unwrap()
        .iter()
        .map(|d| d["trigger"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(
        triggers,
        ["Manual", "Scheduled — 0 9 * * * (UTC)", "GitHub"]
    );
    for id in 1..=3 {
        assert!(has(&world, &format!("automations.run-now.{id}")));
    }
}

#[test]
fn a_failed_last_run_marks_its_definition_with_the_reason() {
    let failed = AutomationDefinitionSummary {
        last_run: Some(AutomationRunSummary {
            state: "failed".into(),
            last_error: Some("boom".into()),
            ..run("r1")
        }),
        ..definition(1, "PR reviewer")
    };
    let world = opened(&Scene::of(vec![failed]));
    assert_eq!(
        name(&world, "automations.failure.1").as_deref(),
        Some("Failed")
    );
    assert_eq!(
        name(&world, "automations.last-error.1").as_deref(),
        Some("boom")
    );
    assert!(!has(&world, "automations.fix.1"));
}

#[test]
fn a_failed_run_that_names_a_folder_opens_the_harness_there_to_trust_it() {
    let failed = AutomationDefinitionSummary {
        last_run: Some(AutomationRunSummary {
            state: "failed".into(),
            last_error: Some("Codex does not trust /code/shop".into()),
            fix: Some(AutomationRunFix {
                agent: "codex".into(),
                directory: "/code/shop".into(),
            }),
            ..run("run-1")
        }),
        ..definition(1, "PR reviewer")
    };
    let mut world = opened(&Scene::of(vec![failed]));
    assert_eq!(
        name(&world, "automations.fix.1").as_deref(),
        Some("Trust folder in Codex")
    );
    click(&mut world, "automations.fix.1");

    let spawn = world.daemon.sent_of("spawn_session");
    assert_eq!(spawn.len(), 1);
    assert_eq!(spawn[0]["agent"], "codex");
    assert_eq!(spawn[0]["cwd"], "/code/shop");
    assert_eq!(spawn[0]["label"], "shop");
    assert!(spawn[0].get("initial_prompt").is_none());
}

#[test]
fn a_superseded_run_says_so_and_a_run_opens_the_session_it_started() {
    let superseded = AutomationRunSummary {
        session_id: Some("s2".into()),
        seed_id: Some("s-seed01".into()),
        automation: Some(AutomationProvenance {
            definition_id: 1,
            definition_name: "Sweep".into(),
            run_id: "old".into(),
            trigger_type: "scheduled".into(),
            superseded: Some(true),
            ..AutomationProvenance::default()
        }),
        ..run("old")
    };
    let scene = Scene::of(vec![definition(1, "Sweep")]).with_runs(vec![superseded, run("none")]);
    let mut world = opened(&scene);
    click(&mut world, "automations.select.1");

    assert!(
        name(&world, "automations.run-open.old")
            .unwrap()
            .contains("Superseded session")
    );
    assert!(has(&world, "automations.run.none"));
    assert!(!has(&world, "automations.run-open.none"));

    click(&mut world, "automations.run-open.old");
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shown.last().unwrap()["session_id"], "s2");
}

#[test]
fn a_change_lists_again_and_reads_the_selected_runs_without_reselecting() {
    let scene =
        Scene::of(vec![definition(1, "PR reviewer")]).with_runs(vec![AutomationRunSummary {
            state: "pending".into(),
            ..run("r1")
        }]);
    let mut world = opened(&scene);
    click(&mut world, "automations.select.1");
    assert_eq!(
        world
            .screen()
            .children(Target::key("automations.run-list"))
            .len(),
        1
    );
    let before = listed(&world);

    scene
        .definitions
        .lock()
        .unwrap()
        .push(definition(2, "Nightly sweep"));
    *scene.runs.lock().unwrap() = vec![run("r1"), run("r2")];
    changed(&mut world);

    assert_eq!(listed(&world), before + 1);
    assert!(has(&world, "automations.row.2"));
    assert_eq!(
        world
            .screen()
            .children(Target::key("automations.run-list"))
            .len(),
        2
    );
    let read: Vec<Value> = world
        .daemon
        .sent_of("automation_runs_get")
        .iter()
        .map(|r| r["definition_id"].clone())
        .collect();
    assert_eq!(read, [1, 1]);
}

#[test]
fn the_guard_check_ages_with_each_list_and_a_failed_check_marks_the_definition() {
    let guarded = |outcome: &str, at: String, checks: i64, detail: Option<&str>| {
        AutomationDefinitionSummary {
            trigger_type: "scheduled".into(),
            guard_check: Some(AutomationGuardCheck {
                outcome: outcome.into(),
                checked_at: at,
                checks_since_run: checks,
                detail: detail.map(Into::into),
            }),
            last_run: Some(run("r1")),
            ..definition(1, "Guarded review")
        }
    };
    let scene = Scene::of(vec![
        guarded("unchanged", ago(3), 14, None),
        definition(2, "Unguarded"),
    ]);
    let mut world = opened(&scene);
    assert_eq!(
        name(&world, "automations.guard.1").as_deref(),
        Some("Guard: unchanged · checked 3 min ago · 14 checks since the last run")
    );
    assert!(!has(&world, "automations.guard.2"));
    assert!(!has(&world, "automations.failure.1"));

    world.advance(Duration::from_secs(60));
    assert!(
        name(&world, "automations.guard.1")
            .unwrap()
            .contains("checked 3 min ago")
    );
    changed(&mut world);
    assert!(
        name(&world, "automations.guard.1")
            .unwrap()
            .contains("checked 4 min ago")
    );

    *scene.definitions.lock().unwrap() = vec![guarded(
        "failed",
        ago(-1),
        15,
        Some("exit 17: network unavailable"),
    )];
    changed(&mut world);
    assert_eq!(
        name(&world, "automations.guard.1").as_deref(),
        Some("Guard: failed · checked just now · 15 checks since the last run")
    );
    assert_eq!(
        name(&world, "automations.failure.1").as_deref(),
        Some("Failed")
    );
    assert_eq!(
        name(&world, "automations.guard-error.1").as_deref(),
        Some("exit 17: network unavailable")
    );
}

#[test]
fn a_refused_switch_says_why_and_stays_as_the_daemon_has_it() {
    let mut world = opened_with(&Scene::of(vec![definition(1, "PR reviewer")]), |daemon| {
        daemon.on("automation_set_enabled", |_| {
            message(AutomationSetEnabledResultMessage {
                success: false,
                error: Some("automation definition is disabled elsewhere".into()),
                ..AutomationSetEnabledResultMessage::new()
            })
        });
    });
    click(&mut world, "automations.toggle.1");

    let sent = world.daemon.sent_of("automation_set_enabled");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        (sent[0]["definition_id"].clone(), sent[0]["enabled"].clone()),
        (1.into(), false.into())
    );
    assert_eq!(
        name(&world, "automations.toggle-error.1").as_deref(),
        Some("automation definition is disabled elsewhere")
    );
    assert!(is(&world, "automations.toggle.1", "checked"));
}

#[test]
fn run_now_starts_a_folder_automation_at_once_and_is_ready_again_once_answered() {
    let scheduled = AutomationDefinitionSummary {
        trigger_type: "scheduled".into(),
        location_type: Some("directory".into()),
        ..definition(1, "Nightly digest")
    };
    let mut world = opened_with(&Scene::of(vec![scheduled]), |daemon| {
        daemon.on("automation_run", |r| {
            message(AutomationRunResultMessage {
                success: true,
                run: Some(AutomationRunSummary {
                    definition_id: r["definition_id"].as_i64().unwrap(),
                    ..run("run-1")
                }),
                ..AutomationRunResultMessage::new()
            })
        });
    });
    click(&mut world, "automations.run-now.1");

    let sent = world.daemon.sent_of("automation_run");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["definition_id"], 1);
    assert!(sent[0].get("pr_url").is_none());
    assert!(!is(&world, "automations.run-now.1", "disabled"));
    assert!(!has(&world, "automations.run-error.1"));
}

#[test]
fn run_now_asks_a_worktree_automation_for_its_pull_request_first() {
    let review = AutomationDefinitionSummary {
        trigger_type: "github_review_requested".into(),
        location_type: Some("repository_worktree".into()),
        ..definition(3, "Recurring reviewer")
    };
    let mut world = opened(&Scene::of(vec![review]));
    click(&mut world, "automations.run-now.3");
    assert!(has(&world, "automations.pr.3"));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("automations.pr-url.3")
    );
    assert!(run_ids(&world).is_empty());

    click(&mut world, "automations.pr-run.3");
    assert_eq!(
        name(&world, "automations.pr-error.3").as_deref(),
        Some("Enter a pull request URL.")
    );
    assert!(run_ids(&world).is_empty());

    world.gesture(|app| {
        app.click(Target::key("automations.pr-url.3"));
        app.type_text("https://github.com/victorarias/attn/pull/42");
        app.press("enter");
    });
    let sent = world.daemon.sent_of("automation_run");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        sent[0]["pr_url"],
        "https://github.com/victorarias/attn/pull/42"
    );

    world.gesture(|app| app.press("escape"));
    assert!(!has(&world, "automations.pr.3"));
    assert!(has(&world, "automations"));
}

#[test]
fn a_refused_run_says_why_and_the_next_click_is_a_new_run() {
    let mut world = opened_with(&Scene::of(vec![definition(1, "PR reviewer")]), |daemon| {
        refuse_runs(daemon, "automation is disabled");
    });
    click(&mut world, "automations.run-now.1");
    assert_eq!(
        name(&world, "automations.run-error.1").as_deref(),
        Some("automation is disabled")
    );
    assert!(!is(&world, "automations.run-now.1", "disabled"));
    click(&mut world, "automations.run-now.1");

    let ids = run_ids(&world);
    assert_eq!(ids.len(), 2);
    assert_ne!(ids[0], ids[1]);
}

#[test]
fn an_unanswered_run_is_still_in_flight_after_thirty_seconds_and_a_retry_reuses_it() {
    let mut world = opened(&Scene::of(vec![definition(1, "PR reviewer")]));
    click(&mut world, "automations.run-now.1");
    assert!(is(&world, "automations.run-now.1", "disabled"));
    world.advance(Duration::from_secs(30));

    assert_eq!(
        name(&world, "automations.run-error.1").as_deref(),
        Some(
            "Run request is still in flight — it will appear in run history; clicking again retries the same run."
        )
    );
    click(&mut world, "automations.run-now.1");
    let ids = run_ids(&world);
    assert_eq!(ids.len(), 2);
    assert_eq!(ids[0], ids[1]);
}

#[test]
fn a_retried_run_is_new_again_once_history_shows_it_delivered_and_not_while_another_is_pending() {
    let scene = Scene::of(vec![definition(1, "PR reviewer")]);
    let mut world = opened(&scene);
    click(&mut world, "automations.run-now.1");
    world.advance(Duration::from_secs(30));
    let own = run_ids(&world)[0].clone();

    let last_run = |key: &str, state: &str| AutomationDefinitionSummary {
        last_run: Some(AutomationRunSummary {
            occurrence_key: Some(key.into()),
            state: state.into(),
            ..run("r1")
        }),
        ..definition(1, "PR reviewer")
    };
    *scene.definitions.lock().unwrap() = vec![last_run("manual:some-other-pending-run", "pending")];
    changed(&mut world);
    click(&mut world, "automations.run-now.1");
    world.advance(Duration::from_secs(30));
    assert_eq!(run_ids(&world), [own.clone(), own.clone()]);

    *scene.definitions.lock().unwrap() = vec![last_run(&format!("manual:{own}"), "delivered")];
    changed(&mut world);
    click(&mut world, "automations.run-now.1");
    let ids = run_ids(&world);
    assert_eq!(ids.len(), 3);
    assert_ne!(ids[2], own);
}

#[test]
fn after_a_relaunch_run_now_retries_the_manual_run_still_pending_and_never_a_scheduled_one() {
    let pending = |key: &str| AutomationDefinitionSummary {
        last_run: Some(AutomationRunSummary {
            occurrence_key: Some(key.into()),
            state: "pending".into(),
            ..run("r1")
        }),
        ..definition(1, "PR reviewer")
    };
    let mut world = opened(&Scene::of(vec![pending("manual:restart-key-1")]));
    click(&mut world, "automations.run-now.1");
    assert_eq!(run_ids(&world), ["restart-key-1"]);

    let mut world = opened(&Scene::of(vec![pending("sched:2026-01-01T00:00:00Z")]));
    click(&mut world, "automations.run-now.1");
    let ids = run_ids(&world);
    assert_eq!(ids.len(), 1);
    assert!(!ids[0].contains("2026-01-01"));
}

#[test]
fn the_list_takes_only_the_answer_to_its_own_request() {
    let daemon = super::palette::daemon(two_agents(), vec![]);
    let mut world = World::start(daemon);
    click(&mut world, "titlebar.automations");
    let request = world.daemon.sent_of("automation_definitions_get");
    let request_id = request[0]["request_id"].as_str().unwrap().to_owned();
    assert!(request_id.starts_with("automation_definitions_get:"));

    world.daemon.emit(AutomationDefinitionsResultMessage {
        request_id: Some("another-request".into()),
        success: true,
        definitions: Some(vec![definition(99, "Not asked for")]),
        ..AutomationDefinitionsResultMessage::new()
    });
    world.settle();
    assert!(!has(&world, "automations.row.99"));

    world.daemon.emit(AutomationDefinitionsResultMessage {
        request_id: Some(request_id),
        success: true,
        definitions: Some(vec![definition(1, "PR reviewer")]),
        ..AutomationDefinitionsResultMessage::new()
    });
    world.settle();
    assert!(has(&world, "automations.row.1"));
    assert!(!has(&world, "automations.row.99"));
}

fn listing(definitions: Vec<AutomationDefinitionSummary>) -> attn_testkit::Answer {
    attn_testkit::Answer::Reply(message(AutomationDefinitionsResultMessage {
        success: true,
        definitions: Some(definitions),
        ..AutomationDefinitionsResultMessage::new()
    }))
}

fn switch_profile(world: &mut World, profile: &str) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: Profile {
            id: profile.into(),
            name: profile.into(),
            current_desktop_id: format!("{profile}-d1"),
            revision: 1,
            ..Profile::default()
        },
        desktops: vec![Desktop {
            id: format!("{profile}-d1"),
            profile_id: profile.into(),
            order_key: "k1".into(),
            ..Desktop::default()
        }],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

#[test]
fn a_profile_switch_shows_the_new_profile_s_automations_and_drops_what_the_old_one_asked_for() {
    let work = AutomationDefinitionSummary {
        profile_id: "work".into(),
        ..definition(1, "Work reviewer")
    };
    let home = AutomationDefinitionSummary {
        profile_id: "home".into(),
        ..definition(2, "Home triage")
    };
    let mut world = opened_with(&Scene::default().with_runs(vec![run("r1")]), |daemon| {
        daemon.answer_in_turn(
            "automation_definitions_get",
            [
                listing(vec![work.clone()]),
                attn_testkit::Answer::Hold,
                listing(vec![home.clone()]),
            ],
        );
    });
    click(&mut world, "automations.row.1");
    assert!(has(&world, "automations.runs"));
    changed(&mut world);
    let held = world.daemon.sent_of("automation_definitions_get")[1].clone();

    switch_profile(&mut world, "home");
    assert_eq!(listed(&world), 3);
    assert!(has(&world, "automations.row.2"));
    assert!(!has(&world, "automations.row.1"));
    assert!(!has(&world, "automations.runs"));

    world.daemon.reply_to(
        &held,
        AutomationDefinitionsResultMessage {
            success: true,
            definitions: Some(vec![work]),
            ..AutomationDefinitionsResultMessage::new()
        },
    );
    world.settle();
    assert!(has(&world, "automations.row.2"));
    assert!(!has(&world, "automations.row.1"));
    let state = world
        .automate("state", serde_json::json!({"of": "automations"}))
        .unwrap();
    assert_eq!(state["selected"], Value::Null);
    assert_eq!(state["runs"], Value::Null);
}
