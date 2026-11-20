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
use super::reach::{tab_round_in_view, unreached_by_tab};
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
    World::start(daemon(settings, run))
}

fn start_sized(settings: &[(&str, &str)], run: Option<UpdateRun>, size: (f32, f32)) -> World {
    World::start_sized(daemon(settings, run), size)
}

fn daemon(settings: &[(&str, &str)], run: Option<UpdateRun>) -> attn_testkit::Daemon {
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
    daemon
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

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key,
            mods: rune::Mods::default(),
        })
    });
}

fn tab_to(world: &mut World, key: &str) {
    for _ in 0..40 {
        if world.screen().focused().and_then(|n| n.key).as_deref() == Some(key) {
            return;
        }
        press(world, Key::Tab);
    }
    panic!("Tab never reaches {key}");
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
fn outposts_at_the_same_step_share_one_note() {
    let world = showing(run(
        "r1",
        "staging",
        vec![
            outpost("o1", "sandbox", "staging", Some("sending")),
            outpost("o2", "oma", "staging", Some("sending")),
            outpost("o3", "laptop", "staging", Some("verifying")),
        ],
    ));
    assert_eq!(
        name(&world, "update-run.outpost.o1").as_deref(),
        Some("sandbox and oma: Copying it over.")
    );
    assert!(!has(&world, "update-run.outpost.o2"));
    assert_eq!(
        name(&world, "update-run.outpost.o3").as_deref(),
        Some("laptop: Checking it runs.")
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

const NO_SPACE: &str = "cat: write error: No space left on device";

/// A run that could not stage `hosts`, worded as the daemon words it.
fn could_not_stage(hosts: &[&str]) -> UpdateRun {
    let each: Vec<String> = hosts.iter().map(|h| format!("{h}: {NO_SPACE}")).collect();
    let outposts = hosts
        .iter()
        .enumerate()
        .map(|(i, h)| outpost(&format!("o{i}"), h, "failed", Some(NO_SPACE)))
        .collect();
    UpdateRun {
        outcome: Some(format!(
            "could not stage {}. attn did not restart. Log: /Users/sam/.attn/update/run-20261119.log",
            each.join("; ")
        )),
        ..run("r1", "failed", outposts)
    }
}

/// The smallest window at the largest text size, and its height under the title bar in the
/// points it lays out in: 600 / 1.5 - 32.
const SMALLEST: (f32, f32) = (800.0, 600.0);
const SENTENCE_LINE: f64 = 16.25;
const SMALLEST_WORK_AREA: f64 = 368.0;

fn inside(rect: [f64; 4], of: [f64; 4]) -> bool {
    rect[0] >= of[0] - 0.5
        && rect[1] >= of[1] - 0.5
        && rect[0] + rect[2] <= of[0] + of[2] + 0.5
        && rect[1] + rect[3] <= of[1] + of[3] + 0.5
}

#[test]
fn a_long_failure_at_the_smallest_window_with_large_text_leaves_the_work_area_and_its_controls_in_bounds()
 {
    for hosts in [
        &["build-linux-primary.us-east-1.example.dev"][..],
        &[
            "build-linux-primary.us-east-1.example.dev",
            "ci-runner-07.build-and-validation.platform.example.org",
            "studio",
        ][..],
        &twelve_hosts()
            .iter()
            .map(String::as_str)
            .collect::<Vec<_>>()[..],
    ] {
        let failed = could_not_stage(hosts);
        let said = failed.outcome.clone();
        let mut world = start_sized(&[("uiScale", "1.5")], Some(failed), SMALLEST);
        assert_eq!(name(&world, "update-run.text"), said);
        let band = world.rect(BANNER);
        let window = world.rect("window");
        let work = world.rect("regions");
        assert!(
            work[3] >= SMALLEST_WORK_AREA * 2.0 / 3.0,
            "{hosts:?}: the failure takes {} and leaves the work area {} of {SMALLEST_WORK_AREA}",
            band[3],
            work[3]
        );
        for key in [
            "update-run.retry",
            "update-run.without",
            "update-run.dismiss",
        ] {
            let rect = world.rect(key);
            assert!(
                inside(rect, band) && inside(rect, window),
                "{hosts:?}: {key} at {rect:?} leaves the band at {band:?}"
            );
        }
    }
}

fn twelve_hosts() -> Vec<String> {
    (1..=12)
        .map(|n| format!("ci-runner-{n:02}.build-and-validation.platform.example.org"))
        .collect()
}

/// The floor of the work area under every banner, as the shell holds it.
const WORK_AREA_FLOOR: f64 = 125.0;

/// How much of the work area shows inside the window.
fn work_shown(world: &mut World) -> f64 {
    let work = world.rect("regions");
    let window = world.rect("window");
    (work[1] + work[3]).min(window[1] + window[3]) - work[1]
}

fn distinct_errors(count: usize) -> UpdateRun {
    let outposts = (1..=count)
        .map(|n| {
            let detail = format!(
                "ssh: connect to host ci-runner-{n:02}.example.org port 22: Connection timed out"
            );
            outpost(
                &format!("o{n}"),
                &format!("ci-runner-{n:02}.build-and-validation.platform.example.org"),
                "failed",
                Some(&detail),
            )
        })
        .collect();
    run("r1", "outposts", outposts)
}

fn staging_update() -> UpdateRun {
    let steps = [
        "connecting",
        "building",
        "downloading",
        "sending",
        "verifying",
    ];
    let outposts = steps
        .iter()
        .enumerate()
        .map(|(i, step)| {
            outpost(
                &format!("o{i}"),
                &format!("ci-runner-{i:02}.build-and-validation.platform.example.org"),
                "staging",
                Some(step),
            )
        })
        .collect();
    run("r1", "staging", outposts)
}

#[test]
fn no_count_of_outposts_grows_the_bar_past_its_bound_or_the_work_area_past_its_floor() {
    let mut heights = Vec::new();
    for (what, update) in [
        ("1 error", distinct_errors(1)),
        ("12 errors", distinct_errors(12)),
        ("18 errors", distinct_errors(18)),
        ("100 errors", distinct_errors(100)),
        ("staging", staging_update()),
    ] {
        let mut world = start_sized(&[("uiScale", "1.5")], Some(update), SMALLEST);
        let band = world.rect(BANNER);
        let work = work_shown(&mut world);
        assert!(
            work >= WORK_AREA_FLOOR,
            "{what}: the bar takes {} and leaves {work} of the work area in the window",
            band[3]
        );
        heights.push(band[3]);
    }
    assert_eq!(
        heights[1..4],
        [heights[1]; 3],
        "more errors than the bar names add no line to it"
    );
}

#[test]
fn details_opens_a_failure_cut_short_whole_in_a_dialog_that_closes_back_to_the_bar() {
    let failed = could_not_stage(&[
        "build-linux-primary.us-east-1.example.dev",
        "ci-runner-07.build-and-validation.platform.example.org",
        "studio",
    ]);
    let said = failed.outcome.clone();
    let mut world = start_sized(&[("uiScale", "1.5")], Some(failed), SMALLEST);
    let bar = world.rect(BANNER)[3];
    assert!(!has(&world, "update-run.whole-dialog"));

    click(&mut world, "update-run.details");
    assert!(has(&world, "update-run.whole-dialog"));
    assert_eq!(name(&world, "whole.update-run.text"), said);
    assert!(has(&world, "whole.update-run.retry"));
    assert!(has(&world, "whole.update-run.without"));
    assert_eq!(world.rect(BANNER)[3], bar, "Details changed the bar");

    press(&mut world, Key::Escape);
    assert!(!has(&world, "update-run.whole-dialog"));

    let one = could_not_stage(&["build-linux-primary.us-east-1.example.dev"]);
    change(&mut world, one);
    assert!(
        !has(&world, "update-run.details"),
        "a failure that fits its lines offers Details"
    );
}

#[test]
fn a_clamped_failure_at_rest_asks_for_no_frame_and_commits_nothing() {
    let mut world = start_sized(
        &[("uiScale", "1.5")],
        Some(could_not_stage(&[
            "build-linux-primary.us-east-1.example.dev",
            "ci-runner-07.build-and-validation.platform.example.org",
            "studio",
        ])),
        SMALLEST,
    );
    assert!(has(&world, "update-run.details"));
    world.gesture(|_| {});
    assert!(world.runtime_is_at_rest());
    let (commits, paints) = (world.runtime().stats.commits, world.runtime().stats.paints);
    for _ in 0..5 {
        world.gesture(|_| {});
    }
    assert_eq!(
        (world.runtime().stats.commits, world.runtime().stats.paints),
        (commits, paints),
        "a resting banner went on committing or painting"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn at_every_width_a_failure_cut_short_offers_details() {
    let failed = could_not_stage(&[
        "build-linux-primary.us-east-1.example.dev",
        "ci-runner-07.build-and-validation.platform.example.org",
        "studio",
    ]);
    let whole = failed.outcome.clone().expect("a failure says why");
    for width in (800..=1440).step_by(40) {
        let mut world = start_sized(
            &[("uiScale", "1.5")],
            Some(failed.clone()),
            (width as f32, 600.0),
        );
        let [_, _, w, h] = world.rect("update-run.text");
        let shows = (h / SENTENCE_LINE).round();
        let style = rune::TextStyle::new(13.0, 600.0);
        let needs = (f64::from(rune::text::measure(&whole, &[], style, Some(w as f32)).1)
            / SENTENCE_LINE)
            .round();
        assert_eq!(
            has(&world, "update-run.details"),
            needs > shows,
            "at {width}: the sentence shows {shows} of its {needs} lines"
        );
        assert!(world.runtime_is_at_rest(), "at {width}");
    }
}

#[test]
fn details_close_when_retry_from_them_starts_a_new_run() {
    let failed = could_not_stage(&[
        "build-linux-primary.us-east-1.example.dev",
        "ci-runner-07.build-and-validation.platform.example.org",
        "studio",
    ]);
    let mut world = start_sized(&[("uiScale", "1.5")], Some(failed.clone()), SMALLEST);
    click(&mut world, "update-run.details");
    click(&mut world, "whole.update-run.retry");
    assert_eq!(without_outposts(&world), vec![None]);
    assert!(
        !has(&world, "update-run.whole-dialog"),
        "Retry left the details open"
    );
    click(&mut world, "update-run.details");

    change(&mut world, run("r2", "staging", vec![]));
    assert!(
        !has(&world, "update-run.whole-dialog"),
        "the new run opened with the failure's details"
    );
    change(
        &mut world,
        UpdateRun {
            id: "r2".into(),
            ..failed
        },
    );
    assert!(
        !has(&world, "update-run.whole-dialog"),
        "the new run's failure opened with the last one's details"
    );
}

#[test]
fn a_staging_note_longer_than_its_line_offers_details_reading_it_whole() {
    let name =
        "ci-runner-07.build-and-validation.platform.example.org.eu-central-1.internal.cluster";
    let mut world = start_sized(
        &[("uiScale", "1.5")],
        Some(run(
            "r1",
            "outposts",
            vec![outpost("o1", name, "staging", None)],
        )),
        SMALLEST,
    );
    let note = name_of_note(&world, "update-run.outpost.o1");
    assert!(note.starts_with(name), "{note}");
    click(&mut world, "update-run.details");
    assert_eq!(name_of_note(&world, "whole.update-run.outpost.o1"), note);
    assert!(
        world.rect("whole.update-run.outpost.o1")[3] > 16.0,
        "Details cuts the note short to one line"
    );
}

fn name_of_note(world: &World, key: &str) -> String {
    name(world, key).unwrap_or_else(|| panic!("no {key}"))
}

#[test]
fn every_control_of_a_twelve_outpost_failure_is_reached_by_tab_in_its_details_and_shown_with_its_ring()
 {
    let hosts = twelve_hosts();
    let ids: Vec<String> = (0..hosts.len()).map(|i| format!("o{i}")).collect();
    let hosts: Vec<&str> = hosts.iter().map(String::as_str).collect();
    let mut world = start_sized(
        &[("uiScale", "1.5")],
        Some(could_not_stage(&hosts)),
        SMALLEST,
    );
    world.gesture(|app| app.runtime.set_full_keyboard_access(true));
    tab_to(&mut world, "update-run.details");
    press(&mut world, Key::Text(" ".into()));
    assert!(has(&world, "update-run.whole-dialog"));

    let reached = tab_round_in_view(&mut world, |_| "update-run.whole-dialog".to_owned());
    for key in ["whole.update-run.retry", "whole.update-run.without"] {
        assert!(
            reached.iter().any(|k| k == key),
            "Tab never reached {key}: {reached:?}"
        );
    }
    tab_to(&mut world, "whole.update-run.without");
    press(&mut world, Key::Text(" ".into()));
    assert_eq!(without_outposts(&world), vec![Some(ids)]);
}

#[test]
fn desktop_chords_switch_desktops_from_every_control_of_a_banner() {
    let mut state: InitialStateMessage = one_desktop(vec![session("s1", "idle")]);
    let mut second = super::sidebar::desktop("d2", "b", "Second", vec![]);
    second.shortcut_slot = Some(2);
    state.desktops.get_or_insert_default().push(second);
    state.settings = Some([("uiScale".to_owned(), "1.5".to_owned())].into());
    state.update_run = Some(could_not_stage(&[
        "build-linux-primary.us-east-1.example.dev",
        "ci-runner-07.build-and-validation.platform.example.org",
        "studio",
    ]));
    let daemon = super::palette::daemon(state, vec![]);
    let mut world = World::start_sized(daemon, SMALLEST);
    world.gesture(|app| app.runtime.set_full_keyboard_access(true));
    for control in [
        "update-run.retry",
        "update-run.without",
        "update-run.details",
        "update-run.dismiss",
    ] {
        tab_to(&mut world, control);
        for action in [Action::SessionNext, Action::SessionPrev] {
            let sent = world.daemon.sent_of("desktop_set_current").len();
            let keys = chord(action.clone());
            world.gesture(|app| app.key(keys));
            assert_eq!(
                world.daemon.sent_of("desktop_set_current").len(),
                sent + 1,
                "{} switched no desktop from {control}",
                action.id()
            );
        }
    }
}
