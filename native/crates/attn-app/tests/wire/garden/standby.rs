use attn_protocol::{SeedReminder, SeedStandbyView, SeedWatchedPullRequest};
use attn_testkit::WALL_START_MS;

use super::*;

const REMINDER_MS: i64 = WALL_START_MS + 30 * 60 * 1000;

fn watching(readiness: &str, state: &str) -> SeedWatchedPullRequest {
    SeedWatchedPullRequest {
        pr_id: "github.com:victorarias/attn#123".into(),
        url: "https://github.com/victorarias/attn/pull/123".into(),
        state: state.into(),
        readiness_state: readiness.into(),
    }
}

fn on_standby(pull_request: SeedWatchedPullRequest, resume_blocked: Option<&str>) -> Seed {
    with(seed("s-stand1", "merge the readiness check"), |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.tender = Some(PartyView {
            name: "Trellis".into(),
            r#ref: "member:trellis".into(),
            session_id: None,
        });
        s.standby = Some(SeedStandbyView {
            note: "Merge once approved".into(),
            pull_requests: vec![pull_request],
            resume_blocked: resume_blocked.map(str::to_owned),
            set_at: "2026-11-05T09:00:00Z".into(),
            set_by: ActorView {
                name: "the user".into(),
                r#ref: "user".into(),
            },
        });
        s.reminder = Some(SeedReminder {
            at: "2026-11-12T09:30:00Z".into(),
            message: "check the rollout".into(),
            ..SeedReminder::default()
        });
    })
}

fn resumed(seed: Seed) -> Seed {
    with(seed, |s| {
        s.standby = None;
        s.rev += 1;
    })
}

fn growing() -> Seed {
    with(seed("s-grow11", "ship the other thing"), |s| {
        s.status = "growing".into()
    })
}

fn reminder() -> String {
    let at = rune::time::LocalTime::at(REMINDER_MS);
    format!("reminder {}:{:02}", at.hour, at.minute)
}

fn words_in(world: &World, key: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn card(world: &World, id: &str) -> Vec<String> {
    words_in(world, &format!("garden.board.card.{id}"))
}

fn row(world: &World, id: &str) -> Vec<String> {
    words_in(world, &format!("garden.row.{id}"))
}

fn marker(world: &World, id: &str) -> Option<(String, String)> {
    let node = world
        .screen()
        .find(Target::key(format!("garden.row.{id}.standby")))?;
    Some((node.name?, node.description.unwrap_or_default()))
}

fn open_board(seeds: Vec<Seed>) -> World {
    let mut world = open_garden_sized(daemon(seeds, Vec::new()), THREE_COLUMNS);
    world.gesture(|app| app.key(board()));
    click(&mut world, "board");
    world
}

fn says(words: &[String], what: &str) -> bool {
    words.iter().any(|w| w == what)
}

#[test]
fn marks_a_standby_card_with_its_note_pull_request_and_reminder_and_follows_the_watch() {
    let mut world = open_board(vec![
        on_standby(watching("blocked", "open"), None),
        growing(),
    ]);
    let shown = card(&world, "s-stand1");
    for words in [
        "standby",
        "#123 blocked",
        "Merge once approved",
        &reminder(),
    ] {
        assert!(says(&shown, words), "{words} in {shown:?}");
    }
    assert!(!says(&card(&world, "s-grow11"), "standby"));

    push(
        &mut world,
        vec![on_standby(watching("ready", "open"), None), growing()],
    );
    let shown = card(&world, "s-stand1");
    assert!(
        says(&shown, "#123 ready") && !says(&shown, "#123 blocked"),
        "{shown:?}"
    );

    push(
        &mut world,
        vec![on_standby(watching("", "merged"), None), growing()],
    );
    assert!(says(&card(&world, "s-stand1"), "#123 merged"));
    assert!(rests(&mut world));
}

#[test]
fn a_card_drops_its_marker_when_standby_ends_and_says_why_a_resume_is_blocked_while_it_is() {
    let standing = on_standby(watching("blocked", "open"), None);
    let mut world = open_board(vec![standing.clone()]);
    assert!(
        !card(&world, "s-stand1")
            .iter()
            .any(|w| w.starts_with("resume blocked"))
    );

    push(
        &mut world,
        vec![on_standby(
            watching("blocked", "open"),
            Some("worktree removed"),
        )],
    );
    let blocked = world
        .screen()
        .descendants(Target::key("garden.board.card.s-stand1"))
        .into_iter()
        .find(|n| n.name.as_deref() == Some("resume blocked: worktree removed"))
        .expect("the card says why the resume is blocked");
    assert_eq!(blocked.description.as_deref(), Some("worktree removed"));

    push(&mut world, vec![standing.clone()]);
    assert!(
        !card(&world, "s-stand1")
            .iter()
            .any(|w| w.starts_with("resume blocked"))
    );

    push(&mut world, vec![resumed(standing)]);
    let shown = card(&world, "s-stand1");
    for gone in ["standby", "Merge once approved", "#123 blocked"] {
        assert!(!says(&shown, gone), "{gone} in {shown:?}");
    }
    assert!(rests(&mut world));
}

#[test]
fn marks_a_standby_row_with_its_note_pull_request_reminder_and_blocked_resume_in_the_tooltip() {
    let mut world = open_garden(vec![
        on_standby(watching("blocked", "open"), None),
        growing(),
    ]);
    let (word, details) = marker(&world, "s-stand1").expect("the row is marked");
    assert_eq!(word, "standby");
    for line in [
        "Merge once approved",
        "victorarias/attn#123 blocked",
        &reminder(),
    ] {
        assert!(details.lines().any(|l| l == line), "{line} in {details:?}");
    }
    assert_eq!(marker(&world, "s-grow11"), None);
    assert!(says(&row(&world, "s-grow11"), "growing"));

    let blocked = on_standby(watching("blocked", "open"), Some("worktree removed"));
    push(&mut world, vec![blocked.clone(), growing()]);
    let (word, details) = marker(&world, "s-stand1").expect("the row is marked");
    assert_eq!(word, "standby · resume blocked");
    assert!(
        details
            .lines()
            .any(|l| l == "resume blocked: worktree removed")
    );

    push(&mut world, vec![resumed(blocked), growing()]);
    assert_eq!(marker(&world, "s-stand1"), None);
    assert!(says(&row(&world, "s-stand1"), "growing"));
    assert!(rests(&mut world));
}

#[test]
fn keeps_a_standby_rows_dependency_blockers_beside_its_marker_until_the_blocker_closes() {
    let standing = on_standby(watching("blocked", "open"), None);
    let blocker = with(seed("s-block1", "the prerequisite"), |s| {
        s.edges.push(edge("blocks", "s-stand1"))
    });
    let other = seed("s-other1", "waits its turn");
    let other_blocker = with(seed("s-block2", "the other prerequisite"), |s| {
        s.edges.push(edge("blocks", "s-other1"))
    });
    let quiet = with(standing.clone(), |s| {
        s.id = "s-stand2".into();
        s.title = "wait for the rollout".into();
    });
    let garden = |standing: Seed, blocker: Seed| {
        vec![
            blocker,
            standing,
            other_blocker.clone(),
            other.clone(),
            quiet.clone(),
        ]
    };
    let mut world = open_garden(garden(standing.clone(), blocker.clone()));
    assert!(says(&row(&world, "s-stand1"), "blocked by 1"));
    assert!(marker(&world, "s-stand2").is_some());
    assert!(!says(&row(&world, "s-stand2"), "blocked by 1"));
    assert!(says(&row(&world, "s-other1"), "blocked by 1"));
    assert_eq!(marker(&world, "s-other1"), None);

    let resume_blocked = on_standby(watching("blocked", "open"), Some("worktree removed"));
    push(&mut world, garden(resume_blocked.clone(), blocker.clone()));
    assert_eq!(
        marker(&world, "s-stand1").map(|(word, _)| word).as_deref(),
        Some("standby · resume blocked")
    );
    assert!(says(&row(&world, "s-stand1"), "blocked by 1"));

    push(
        &mut world,
        garden(resume_blocked, closed(blocker, "harvested")),
    );
    assert!(marker(&world, "s-stand1").is_some());
    assert!(!says(&row(&world, "s-stand1"), "blocked by 1"));
    assert!(rests(&mut world));
}

#[test]
fn keeps_only_the_seeds_on_standby_under_the_standby_lens_and_lets_one_go_when_standby_ends() {
    let standing = on_standby(watching("blocked", "open"), None);
    let waiting = with(seed("s-wait11", "not started yet"), |s| s.ready = true);
    let mut world = open_garden(vec![standing.clone(), growing(), waiting.clone()]);
    assert!(
        rows(&world)
            .iter()
            .any(|r| r.starts_with("ship the other thing"))
    );

    type_query(&mut world, "is:standby");
    let listed = rows(&world);
    assert!(
        listed
            .iter()
            .any(|r| r.starts_with("merge the readiness check")),
        "{listed:?}"
    );
    assert!(!listed.iter().any(|r| r.starts_with("ship the other thing")));
    assert!(!listed.iter().any(|r| r.starts_with("not started yet")));

    push(&mut world, vec![resumed(standing), growing(), waiting]);
    assert!(
        !rows(&world)
            .iter()
            .any(|r| r.starts_with("merge the readiness check"))
    );
    assert!(rests(&mut world));
}
