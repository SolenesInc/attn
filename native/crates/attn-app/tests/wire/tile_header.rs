use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    AutomationProvenance, CommandUsage, Desktop, DesktopPane, GetCommandUsageResultMessage,
    InitialStateMessage, Profile, ProfileArrangementChangedMessage, PullRequestProvenance,
    RecordCommandUsageResultMessage, Session, SessionPullRequest, SessionStateChangedMessage,
    SessionUsage, SessionUsageModel,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Daemon, Target, World, key_event, message};
use rune::{Key, KeyEvent, KeyPlatform, Mods};

const SIZE: (f32, f32) = (1600.0, 900.0);
const DESKTOP: &str = "work/desktop_1";

fn session(id: &str, label: &str, usage: Option<SessionUsage>) -> Session {
    Session {
        id: format!("s-{id}"),
        label: label.into(),
        directory: format!("/work/{id}"),
        profile_id: "work".into(),
        usage,
        ..Session::default()
    }
}

fn model_usage(purpose: &str, counts: SessionUsageModel) -> SessionUsageModel {
    SessionUsageModel {
        model: "claude-opus-5".into(),
        purpose: purpose.into(),
        ..counts
    }
}

fn usage(cost: Option<f64>, unpriced: bool, tokens: i64) -> SessionUsage {
    SessionUsage {
        total_tokens: tokens,
        cost_usd: cost,
        has_unpriced_usage: unpriced,
        models: vec![model_usage(
            "agent",
            SessionUsageModel {
                input_tokens: 4,
                output_tokens: tokens - 4,
                total_tokens: tokens,
                cost_usd: cost,
                has_unpriced_usage: unpriced,
                ..SessionUsageModel::default()
            },
        )],
        ..SessionUsage::default()
    }
}

fn priced(cost: f64) -> SessionUsage {
    usage(Some(cost), false, 1_000)
}

fn agent_pane(id: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: DESKTOP.into(),
        pane_id: id.into(),
        runtime_id: format!("r-{id}"),
        session_id: format!("s-{id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktop(active: &str) -> Desktop {
    Desktop {
        id: DESKTOP.into(),
        profile_id: "work".into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: row(vec![pane("a"), pane("b")]).to_string(),
        active_pane_id: active.into(),
        panes: vec![agent_pane("a"), agent_pane("b")],
        revision: 1,
    }
}

fn profile() -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: DESKTOP.into(),
        ..Profile::default()
    }
}

fn start(a: Session, b: Session) -> World {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile()]),
        desktops: Some(vec![desktop("a")]),
        sessions: Some(vec![a, b]),
        ..InitialStateMessage::new()
    });
    daemon
        .on("get_command_usage", |r| {
            message(GetCommandUsageResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                profile_id: r["profile_id"].as_str().unwrap().into(),
                success: true,
                entries: Vec::<CommandUsage>::new(),
                ..GetCommandUsageResultMessage::new()
            })
        })
        .on("record_command_usage", |r| {
            message(RecordCommandUsageResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                ..RecordCommandUsageResultMessage::new()
            })
        });
    let mut world = World::start_sized(daemon, SIZE);
    world.open_desktop();
    world
}

fn changes(world: &mut World, session: Session) {
    world.daemon.emit(SessionStateChangedMessage {
        session,
        ..SessionStateChangedMessage::new()
    });
    world.settle();
}

fn badge(world: &World, leaf: &str) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("tile.{leaf}.usage")))
        .map(|n| n.name.unwrap_or_default())
}

fn breakdown(world: &World, leaf: &str) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("tile.{leaf}.usage.popover")))
        .map(|_| texts(world, &format!("tile.{leaf}.usage.popover")).join("\n"))
}

fn texts(world: &World, key: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn middle(world: &World, key: &str) -> (f32, f32) {
    let [x, y, w, h] = world
        .runtime()
        .presented(key)
        .unwrap_or_else(|| panic!("{key} is drawn"))
        .0;
    (x + w / 2.0, y + h / 2.0)
}

fn rest_on(world: &mut World, key: &str) {
    let at = middle(world, key);
    world.gesture(|app| app.move_to(at));
}

fn key(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn run_command(world: &mut World, query: &str) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current())
        .binding(&Action::UiCommandPalette)
    {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("the command palette has no single chord: {other:?}"),
    };
    world.gesture(|app| app.key(ev));
    let query = query.to_owned();
    world.gesture(|app| app.type_text(&query));
    key(world, Key::Enter);
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(2));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_headers_usage_reads_its_cost_to_the_cent_under_a_cent_partly_priced_or_as_compact_tokens() {
    for (name, usage, label) in [
        ("dollars", priced(1.234), "Session usage $1.23"),
        ("under a cent", priced(0.004), "Session usage <$0.01"),
        (
            "partly priced",
            usage(Some(1.234), true, 1_000),
            "Session usage $1.23*, some usage has no price",
        ),
        (
            "no price",
            usage(None, true, 184_000),
            "Session usage 184k tokens, some usage has no price",
        ),
    ] {
        let world = start(
            session("a", "ledger sweep", Some(usage)),
            session("b", "docs", None),
        );
        assert_eq!(badge(&world, "a").as_deref(), Some(label), "{name}");
    }
}

#[test]
fn a_header_shows_no_cost_before_usage_or_while_it_is_incomplete_and_follows_fresh_usage() {
    let mut world = start(
        session("a", "ledger sweep", None),
        session("b", "docs", None),
    );
    assert_eq!(badge(&world, "a"), None);

    changes(&mut world, session("a", "ledger sweep", Some(priced(0.42))));
    assert_eq!(badge(&world, "a").as_deref(), Some("Session usage $0.42"));

    changes(&mut world, session("a", "ledger sweep", Some(priced(0.73))));
    assert_eq!(badge(&world, "a").as_deref(), Some("Session usage $0.73"));

    let incomplete = SessionUsage {
        measurement_incomplete: Some(true),
        ..priced(0.73)
    };
    changes(&mut world, session("a", "ledger sweep", Some(incomplete)));
    assert_eq!(badge(&world, "a"), None);
    rest(&mut world);
}

#[test]
fn the_usage_breakdown_counts_each_model_exactly_with_the_guardian_beside_the_agent_and_why_some_is_unpriced()
 {
    let usage = SessionUsage {
        total_tokens: 186_197,
        cost_usd: Some(1.2345),
        has_unpriced_usage: true,
        models: vec![
            model_usage(
                "agent",
                SessionUsageModel {
                    input_tokens: 12_345,
                    output_tokens: 6_789,
                    cache_read_tokens: 150_000,
                    cache_write_5m_tokens: 10_000,
                    cache_write_1h_tokens: 5_000,
                    cache_write_unclassified_tokens: 187,
                    total_tokens: 184_321,
                    cost_usd: Some(1.234),
                    has_unpriced_usage: true,
                    unpriced_reason: Some("Cache write duration is unavailable.".into()),
                    ..SessionUsageModel::default()
                },
            ),
            model_usage(
                "guardian",
                SessionUsageModel {
                    input_tokens: 1_746,
                    output_tokens: 130,
                    total_tokens: 1_876,
                    cost_usd: Some(0.0005),
                    ..SessionUsageModel::default()
                },
            ),
        ],
        ..SessionUsage::default()
    };
    let mut world = start(
        session("a", "ledger sweep", Some(usage)),
        session("b", "docs", None),
    );
    rest_on(&mut world, "tile.a.usage");
    world.advance(Duration::from_millis(160));

    let shown = breakdown(&world, "a").expect("the breakdown opens");
    for line in [
        "186,197 tokens",
        "$1.23*",
        "claude-opus-5",
        "184,321 tokens · $1.23*",
        "12,345",
        "6,789",
        "150,000",
        "187",
        "Guardian · claude-opus-5",
        "1,876 tokens · <$0.01",
        "1,746",
        "* Cache write duration is unavailable.",
    ] {
        assert!(shown.lines().any(|l| l == line), "{line:?} in {shown}");
    }
    assert!(
        !shown.contains("esc close"),
        "a preview has no close hint: {shown}"
    );
}

#[test]
fn the_usage_preview_waits_for_the_pointer_to_rest_and_stays_open_while_the_pointer_moves_into_it()
{
    let mut world = start(
        session("a", "ledger sweep", Some(priced(0.42))),
        session("b", "docs", None),
    );
    rest_on(&mut world, "tile.a.usage");
    world.advance(Duration::from_millis(100));
    assert_eq!(breakdown(&world, "a"), None);
    world.advance(Duration::from_millis(60));
    assert!(breakdown(&world, "a").is_some());

    rest_on(&mut world, "tile.a.usage.popover");
    world.advance(Duration::from_millis(400));
    assert!(
        breakdown(&world, "a").is_some(),
        "the preview stays while the pointer is on it"
    );

    rest_on(&mut world, "tile.b");
    world.advance(Duration::from_millis(240));
    assert_eq!(breakdown(&world, "a"), None);
    rest(&mut world);
}

#[test]
fn a_press_on_the_usage_pins_its_breakdown_until_escape_or_a_second_press() {
    let mut world = start(
        session("a", "ledger sweep", Some(priced(0.42))),
        session("b", "docs", None),
    );
    world.gesture(|app| app.click(Target::key("tile.a.usage")));
    let pinned = breakdown(&world, "a").expect("a press pins the breakdown");
    assert!(pinned.contains("esc close"), "{pinned}");

    rest_on(&mut world, "tile.b");
    world.advance(Duration::from_millis(400));
    assert!(
        breakdown(&world, "a").is_some(),
        "a pinned breakdown outlasts the pointer"
    );

    key(&mut world, Key::Escape);
    assert_eq!(breakdown(&world, "a"), None);

    let at = middle(&world, "tile.a.usage");
    world.gesture(|app| app.click_at(at));
    assert!(breakdown(&world, "a").is_some());
    world.gesture(|app| app.click_at(at));
    assert_eq!(breakdown(&world, "a"), None, "a second press closes it");
    rest(&mut world);
}

#[test]
fn the_command_palette_pins_the_usage_of_the_agent_on_screen_and_each_later_request_opens_it_again()
{
    let mut world = start(
        session("a", "ledger sweep", Some(usage(Some(0.42), false, 1_111))),
        session("b", "docs sweep", Some(usage(Some(0.42), false, 2_222))),
    );
    run_command(&mut world, "ledger sweep usage");
    let pinned = breakdown(&world, "a").expect("the command pins the breakdown");
    assert!(
        pinned.contains("1,111 tokens") && pinned.contains("esc close"),
        "{pinned}"
    );
    key(&mut world, Key::Escape);
    assert_eq!(breakdown(&world, "a"), None);

    run_command(&mut world, "ledger sweep usage");
    assert!(breakdown(&world, "a").is_some_and(|b| b.contains("1,111 tokens")));
    key(&mut world, Key::Escape);

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop("b")],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    run_command(&mut world, "docs sweep usage");
    assert!(breakdown(&world, "b").is_some_and(|b| b.contains("2,222 tokens")));
    assert_eq!(breakdown(&world, "a"), None);
    rest(&mut world);
}

#[test]
fn a_pinned_or_previewed_breakdown_goes_with_its_tile_and_its_usage_and_does_not_come_back_on_its_own()
 {
    let mut world = start(
        session("a", "ledger sweep", Some(priced(0.42))),
        session("b", "docs", None),
    );
    let other = Desktop {
        id: "work/desktop_2".into(),
        name: "Desktop 2".into(),
        shortcut_slot: Some(2),
        order_key: "2".into(),
        tree_json: String::new(),
        active_pane_id: String::new(),
        panes: vec![],
        ..desktop("a")
    };
    let show = |world: &mut World, current: &str| {
        world.daemon.emit(ProfileArrangementChangedMessage {
            profile: Profile {
                current_desktop_id: current.into(),
                ..profile()
            },
            desktops: vec![desktop("a"), other.clone()],
            ..ProfileArrangementChangedMessage::new()
        });
        world.settle();
    };

    world.gesture(|app| app.click(Target::key("tile.a.usage")));
    assert!(breakdown(&world, "a").is_some());
    show(&mut world, "work/desktop_2");
    show(&mut world, DESKTOP);
    assert_eq!(
        breakdown(&world, "a"),
        None,
        "a pin ends when its tile leaves the screen"
    );

    rest_on(&mut world, "tile.a.usage");
    world.advance(Duration::from_millis(160));
    assert!(breakdown(&world, "a").is_some());
    show(&mut world, "work/desktop_2");
    show(&mut world, DESKTOP);
    assert_eq!(
        breakdown(&world, "a"),
        None,
        "a preview ends when its tile leaves the screen"
    );

    world.gesture(|app| app.click(Target::key("tile.a.usage")));
    let incomplete = SessionUsage {
        measurement_incomplete: Some(true),
        ..priced(0.42)
    };
    changes(&mut world, session("a", "ledger sweep", Some(incomplete)));
    changes(&mut world, session("a", "ledger sweep", Some(priced(0.42))));
    assert_eq!(
        breakdown(&world, "a"),
        None,
        "a pin ends when its usage goes"
    );
    rest(&mut world);
}

#[test]
fn a_press_outside_a_pinned_breakdown_closes_it() {
    let mut world = start(
        session("a", "ledger sweep", Some(priced(0.42))),
        session("b", "docs", None),
    );
    world.gesture(|app| app.click(Target::key("tile.a.usage")));
    assert!(breakdown(&world, "a").is_some());
    let at = middle(&world, "tile.b");
    world.gesture(|app| app.click_at(at));
    assert_eq!(breakdown(&world, "a"), None);
    rest(&mut world);
}

fn pull_request(number: i64, state: &str, created: &str) -> SessionPullRequest {
    SessionPullRequest {
        repository: "github.com/victorarias/attn".into(),
        number,
        url: format!("https://github.com/victorarias/attn/pull/{number}"),
        title: Some(format!("Change {number}")),
        created_at: created.into(),
        state: state.into(),
        ..SessionPullRequest::default()
    }
}

fn with_pull_requests(prs: Vec<SessionPullRequest>) -> Session {
    Session {
        pull_requests: Some(prs),
        ..session("a", "ledger sweep", None)
    }
}

fn details(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("tile.a.details.popover"))
        .map(|n| {
            let mut lines = vec![n.name.unwrap_or_default()];
            lines.extend(texts(world, "tile.a.details.popover"));
            lines.join("\n")
        })
}

#[test]
fn an_automations_header_names_its_definition_and_the_pull_request_it_reviews_and_opens_it() {
    let reviewing = Session {
        automation: Some(AutomationProvenance {
            run_id: "run-1".into(),
            definition_id: 1,
            definition_name: "Requested PR review - GPT Sol medium".into(),
            trigger_type: "github_review_requested".into(),
            pull_request: Some(PullRequestProvenance {
                repository: "ghe.example.net/feeds/feed-nexus-web".into(),
                number: 101,
                url: "https://ghe.example.net/feeds/feed-nexus-web/pull/101".into(),
                title: Some("Fix validation race".into()),
                head_sha: "82f1c7a".into(),
            }),
            ..AutomationProvenance::default()
        }),
        ..session("a", "feed-nexus-web#101 · gpt-5.6-sol", Some(priced(2.61)))
    };
    let mut world = start(reviewing, session("b", "docs", None));
    let shown = texts(&world, "tile.a.provenance");
    for piece in [
        "⚡ Automation",
        "GPT Sol medium",
        "Open pull request feed-nexus-web#101",
        "Fix validation race",
    ] {
        assert!(shown.iter().any(|s| s == piece), "{piece:?} in {shown:?}");
    }
    assert_eq!(badge(&world, "a").as_deref(), Some("Session usage $2.61"));

    world.gesture(|app| app.click(Target::key("tile.a.automation.target")));
    assert_eq!(
        world.opened_links(),
        ["https://ghe.example.net/feeds/feed-nexus-web/pull/101"]
    );
    rest(&mut world);
}

#[test]
fn a_header_names_the_sessions_best_pull_request_with_its_status_and_none_when_all_are_closed() {
    let mut open = pull_request(71, "open", "2026-08-30T12:00:00Z");
    open.ci_status = Some("failure".into());
    let world = start(
        with_pull_requests(vec![
            pull_request(70, "merged", "2026-08-31T12:00:00Z"),
            open,
            pull_request(69, "closed", "2026-09-01T12:00:00Z"),
        ]),
        Session {
            pull_requests: Some(vec![pull_request(5, "closed", "2026-08-30T12:00:00Z")]),
            ..session("b", "docs", None)
        },
    );
    let shown = texts(&world, "tile.a.provenance");
    for piece in [
        "⎇ PR",
        "Pull request attn#71 details",
        "checks failed",
        "Change 71",
    ] {
        assert!(shown.iter().any(|s| s == piece), "{piece:?} in {shown:?}");
    }
    assert!(
        world
            .screen()
            .find(Target::key("tile.b.provenance"))
            .is_none()
    );
}

#[test]
fn the_pull_request_details_open_on_hover_and_close_soon_after_the_pointer_leaves_them() {
    let mut world = start(
        with_pull_requests(vec![pull_request(71, "open", "2026-08-30T12:00:00Z")]),
        session("b", "docs", None),
    );
    rest_on(&mut world, "tile.a.details");
    let shown = details(&world).expect("the details open at once");
    for line in [
        "Pull request attn#71",
        "Open attn#71 on GitHub",
        "attn#71",
        "open",
        "waiting for GitHub",
        "↵ open · c copy url",
    ] {
        assert!(shown.lines().any(|l| l == line), "{line:?} in {shown}");
    }
    rest_on(&mut world, "tile.a.details.popover");
    world.advance(Duration::from_millis(400));
    assert!(
        details(&world).is_some(),
        "the details stay while the pointer is on them"
    );
    rest_on(&mut world, "tile.b");
    world.advance(Duration::from_millis(100));
    assert!(details(&world).is_some());
    world.advance(Duration::from_millis(20));
    assert_eq!(details(&world), None);
    rest(&mut world);
}

#[test]
fn pinned_pull_request_details_pick_with_the_arrows_open_with_return_and_copy_with_c() {
    let mut first = pull_request(71, "open", "2026-08-30T12:00:00Z");
    first.status_fetched_at = Some("2026-08-30T12:05:00Z".into());
    first.ci_status = Some("success".into());
    first.review_status = Some("approved".into());
    first.mergeable_state = Some("clean".into());
    let mut world = start(
        with_pull_requests(vec![
            first,
            pull_request(60, "merged", "2026-08-01T12:00:00Z"),
        ]),
        session("b", "docs", None),
    );
    world.gesture(|app| app.click(Target::key("tile.a.details")));
    let shown = details(&world).expect("a press pins the details");
    for line in [
        "passed",
        "approved",
        "no conflicts",
        "↑↓ pick · ↵ open · c copy url",
    ] {
        assert!(shown.lines().any(|l| l == line), "{line:?} in {shown}");
    }

    key(&mut world, Key::Down);
    assert!(details(&world).is_some_and(|d| d.starts_with("Pull request attn#60")));
    key(&mut world, Key::Enter);
    assert_eq!(
        world.opened_links(),
        ["https://github.com/victorarias/attn/pull/60"]
    );
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Text("c".into()),
            mods: Mods::default(),
        })
    });
    assert_eq!(
        world.clipboard.text().map(|t| t.to_string()).as_deref(),
        Some("https://github.com/victorarias/attn/pull/60")
    );
    key(&mut world, Key::Up);
    assert!(details(&world).is_some_and(|d| d.starts_with("Pull request attn#71")));

    key(&mut world, Key::Escape);
    assert_eq!(details(&world), None);
    world.advance(Duration::from_millis(500));
    assert_eq!(
        details(&world),
        None,
        "Escape under a still pointer leaves the details closed"
    );
    rest(&mut world);
}
