use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    InitialStateMessage, StartUpdateResultMessage, UpdateChangedMessage, UpdateOutpostProgress,
    UpdateRun,
};
use attn_testkit::{Target, World, message};
use rune::Key;

use super::queue::one_desktop;
use super::reach::unreached_by_tab;
use super::sidebar::{chord, session};

const BANNER: &str = "update-run";

fn outpost(id: &str, name: &str, state: &str, detail: Option<&str>) -> UpdateOutpostProgress {
    UpdateOutpostProgress {
        id: id.into(),
        name: name.into(),
        state: state.into(),
        detail: detail.map(Into::into),
    }
}

fn run(id: &str, phase: &str, outposts: Vec<UpdateOutpostProgress>) -> UpdateRun {
    UpdateRun {
        id: id.into(),
        install_kind: "release_app".into(),
        log_path: format!("/tmp/{id}.log"),
        phase: phase.into(),
        outcome: match phase {
            "done" => Some("attn is up to date.".into()),
            "failed" => Some("The update failed.".into()),
            _ => None,
        },
        outposts,
        ..UpdateRun::default()
    }
}

fn start(settings: &[(&str, &str)], run: Option<UpdateRun>) -> World {
    let mut state: InitialStateMessage = one_desktop(vec![session("s1", "idle")]);
    state.settings = Some(
        settings
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect::<BTreeMap<_, _>>(),
    );
    state.update_run = run;
    let daemon = super::palette::daemon(state, vec![]);
    daemon.on("start_update", |_| ());
    World::start(daemon)
}

fn showing(run: UpdateRun) -> World {
    start(&[], Some(run))
}

fn change(world: &mut World, run: UpdateRun) {
    world.daemon.emit(UpdateChangedMessage {
        run,
        ..UpdateChangedMessage::new()
    });
    world.settle();
}

fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn name(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn click(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(key)));
}

fn without_outposts(world: &World) -> Vec<Option<Vec<String>>> {
    world
        .daemon
        .sent_of("start_update")
        .iter()
        .map(|r| {
            r["without_outposts"].as_array().map(|ids| {
                ids.iter()
                    .map(|id| id.as_str().unwrap().to_owned())
                    .collect()
            })
        })
        .collect()
}

fn hover(world: &mut World, inside: bool) {
    let at = if inside {
        let [x, y, w, h] = world.screen().bounds(Target::key(BANNER)).unwrap();
        (x + w / 2.0, y + h / 2.0)
    } else {
        (600.0, 700.0)
    };
    world.gesture(|app| app.move_to(at));
}

#[test]
fn a_running_update_shows_its_phase_and_each_outposts_step_and_cannot_be_dismissed() {
    let mut world = showing(run(
        "r1",
        "outposts",
        vec![
            outpost("o1", "builder", "staging", Some("downloading")),
            outpost("o2", "gpu", "switching", None),
        ],
    ));

    assert_eq!(name(&world, BANNER).as_deref(), Some("attn update"));
    assert_eq!(
        name(&world, "update-run.text").as_deref(),
        Some("Finishing up.")
    );
    assert_eq!(
        name(&world, "update-run.outpost.o1").as_deref(),
        Some("builder: Downloading attn for Linux.")
    );
    assert_eq!(
        name(&world, "update-run.outpost.o2").as_deref(),
        Some("gpu: Switching to the new build.")
    );
    assert!(!has(&world, "update-run.dismiss"));
    assert!(!has(&world, "update-run.retry"));

    change(&mut world, run("r1", "applying", vec![]));
    assert_eq!(
        name(&world, "update-run.text").as_deref(),
        Some("Installing the update. attn will restart; your agents keep running.")
    );
}

#[test]
fn a_failed_run_retries_whole_or_without_the_outpost_that_failed() {
    let mut world = showing(run(
        "r1",
        "failed",
        vec![
            outpost("o1", "builder", "failed", Some("ssh: connection refused")),
            outpost("o2", "gpu", "done", None),
        ],
    ));
    assert_eq!(
        name(&world, "update-run.text").as_deref(),
        Some("The update failed.")
    );
    assert!(!has(&world, "update-run.outpost.o1"));
    assert_eq!(
        name(&world, "update-run.without").as_deref(),
        Some("Update without builder")
    );

    click(&mut world, "update-run.retry");
    click(&mut world, "update-run.without");

    assert_eq!(
        without_outposts(&world),
        [None, Some(vec!["o1".to_owned()])]
    );
}

#[test]
fn a_run_that_failed_on_several_outposts_offers_to_update_without_all_of_them() {
    let mut world = showing(run(
        "r1",
        "failed",
        vec![
            outpost("o1", "builder", "failed", None),
            outpost("o2", "gpu", "failed", None),
            outpost("o3", "spare", "done", None),
        ],
    ));
    assert_eq!(
        name(&world, "update-run.without").as_deref(),
        Some("Update without 2 outposts")
    );

    click(&mut world, "update-run.without");

    assert_eq!(
        without_outposts(&world),
        [Some(vec!["o1".to_owned(), "o2".to_owned()])]
    );
}

#[test]
fn a_refused_retry_says_why() {
    let mut world = showing(run("r1", "failed", vec![]));
    world.daemon.on("start_update", |_| {
        message(StartUpdateResultMessage {
            success: false,
            error: Some("an update is already running".into()),
            ..StartUpdateResultMessage::new()
        })
    });

    click(&mut world, "update-run.retry");

    assert_eq!(
        name(&world, "notice.text").as_deref(),
        Some("an update is already running")
    );
}

#[test]
fn a_run_that_ended_well_closes_after_a_countdown_the_pointer_holds() {
    let mut world = showing(run(
        "r1",
        "done",
        vec![outpost("o1", "builder", "done", None)],
    ));
    assert_eq!(
        name(&world, "update-run.text").as_deref(),
        Some("attn is up to date.")
    );
    assert_eq!(
        name(&world, "update-run.countdown").as_deref(),
        Some("Closing in 5s")
    );

    world.advance(Duration::from_secs(1));
    assert_eq!(
        name(&world, "update-run.countdown").as_deref(),
        Some("Closing in 4s")
    );

    hover(&mut world, true);
    world.advance(Duration::from_secs(10));
    assert_eq!(
        name(&world, "update-run.countdown").as_deref(),
        Some("Closing in 4s")
    );

    hover(&mut world, false);
    world.advance(Duration::from_secs(3));
    assert_eq!(
        name(&world, "update-run.countdown").as_deref(),
        Some("Closing in 1s")
    );
    world.advance(Duration::from_secs(1));
    assert!(!has(&world, BANNER));

    change(&mut world, run("r1", "done", vec![]));
    assert!(!has(&world, BANNER), "a closed run stays closed");
    change(&mut world, run("r2", "preparing", vec![]));
    assert_eq!(
        name(&world, "update-run.text").as_deref(),
        Some("Building the update.")
    );
}

#[test]
fn a_run_that_ends_under_the_pointer_holds_its_countdown_until_the_pointer_leaves() {
    let mut world = showing(run("r1", "applying", vec![]));
    hover(&mut world, true);

    change(&mut world, run("r1", "done", vec![]));
    world.advance(Duration::from_secs(10));
    assert_eq!(
        name(&world, "update-run.countdown").as_deref(),
        Some("Closing in 5s")
    );

    hover(&mut world, false);
    world.advance(Duration::from_secs(5));
    assert!(!has(&world, BANNER));
}

#[test]
fn a_run_that_left_an_outpost_behind_stays_until_dismissed() {
    let mut world = showing(run(
        "r1",
        "done",
        vec![
            outpost("o1", "builder", "done", None),
            outpost("o2", "gpu", "skipped", Some("left out by request")),
        ],
    ));
    assert!(!has(&world, "update-run.countdown"));

    world.advance(Duration::from_secs(10));
    assert!(has(&world, BANNER));

    click(&mut world, "update-run.dismiss");
    assert!(!has(&world, BANNER));
}

#[test]
fn tab_reaches_the_buttons_of_a_failed_run() {
    let mut world = showing(run(
        "r1",
        "failed",
        vec![outpost("o1", "builder", "failed", None)],
    ));

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn the_palette_updates_attn_where_the_daemon_can_update_it_and_says_why_it_was_refused() {
    let updatable = [
        ("self_update_enabled", "true"),
        ("update_install_kind", "release_app"),
    ];
    let mut world = start(&updatable, None);
    world.daemon.on("start_update", |_| {
        message(StartUpdateResultMessage {
            success: false,
            error: Some("attn is already up to date".into()),
            ..StartUpdateResultMessage::new()
        })
    });
    open_palette_on(&mut world, "update attn");
    assert!(has(&world, "palette.row.command:update-attn"));
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: Key::Enter,
            mods: rune::Mods::default(),
        })
    });
    assert_eq!(without_outposts(&world), [None]);
    assert_eq!(
        name(&world, "notice.text").as_deref(),
        Some("attn is already up to date")
    );

    for settings in [
        [
            ("self_update_enabled", "false"),
            ("update_install_kind", "release_app"),
        ],
        [
            ("self_update_enabled", "true"),
            ("update_install_kind", "outpost"),
        ],
    ] {
        let mut world = start(&settings, None);
        open_palette_on(&mut world, "update attn");
        assert!(!has(&world, "palette.row.command:update-attn"));
    }
}

fn open_palette_on(world: &mut World, query: &str) {
    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    world.gesture(|app| app.type_text(query));
}
