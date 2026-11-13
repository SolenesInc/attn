use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    ActorView, AutomationProvenance, CommandUsage, Desktop, DesktopPane,
    GetCommandUsageResultMessage, InitialStateMessage, Profile, ProfileArrangementChangedMessage,
    PullRequestProvenance, RecordCommandUsageResultMessage, Session, SessionDelegationRole,
    SessionPullRequest, SessionStateChangedMessage, SessionUsage, SessionUsageModel,
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
    start_with(vec![a, b])
}

/// The two tiles show the first two of `sessions`; the rest run off screen.
fn start_with(sessions: Vec<Session>) -> World {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile()]),
        desktops: Some(vec![desktop("a")]),
        sessions: Some(sessions),
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
fn an_automations_header_names_its_definition_and_the_pull_request_it_reviews_and_opens_it_beside_the_sessions_own()
 {
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
        pull_requests: Some(vec![SessionPullRequest {
            ci_status: Some("failure".into()),
            ..pull_request(71, "open", "2026-08-30T12:00:00Z")
        }]),
        ..session("a", "feed-nexus-web#101 · gpt-5.6-sol", Some(priced(2.61)))
    };
    let mut world = start(reviewing, session("b", "docs", None));
    let shown = texts(&world, "tile.a.provenance");
    for piece in [
        "⚡ Automation",
        "GPT Sol medium",
        "Open pull request feed-nexus-web#101",
        "Fix validation race",
        "⎇ PR",
        "Pull request attn#71 details",
        "checks failed",
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
            mods: Mods {
                cmd: true,
                ..Mods::default()
            },
        })
    });
    assert_eq!(
        world.clipboard.text().map(|t| t.to_string()),
        None,
        "a held modifier leaves c to the app"
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

#[test]
fn a_headers_pull_request_status_names_the_first_thing_in_its_way() {
    let mut world = start(
        with_pull_requests(vec![pull_request(71, "open", "2026-08-30T12:00:00Z")]),
        session("b", "docs", None),
    );
    let with =
        |number: i64, state: &str, ci: Option<&str>, review: Option<&str>, merge: Option<&str>| {
            let mut pr = pull_request(number, state, "2026-08-30T12:00:00Z");
            pr.ci_status = ci.map(Into::into);
            pr.review_status = review.map(Into::into);
            pr.mergeable_state = merge.map(Into::into);
            pr
        };
    for (case, prs, named, status) in [
        (
            "the newest of several open ones",
            vec![
                pull_request(71, "open", "2026-08-30T10:00:00Z"),
                pull_request(74, "open", "2026-08-30T12:00:00Z"),
            ],
            "attn#74",
            "open",
        ),
        (
            "an open one over a newer merged one",
            vec![
                pull_request(74, "merged", "2026-08-30T14:00:00Z"),
                with(71, "open", Some("pending"), None, None),
            ],
            "attn#71",
            "checks running",
        ),
        (
            "a draft over a newer merged one",
            vec![
                pull_request(71, "merged", "2026-08-30T13:00:00Z"),
                pull_request(74, "draft", "2026-08-30T12:00:00Z"),
            ],
            "attn#74",
            "draft",
        ),
        (
            "conflicts before failing checks or requested changes",
            vec![with(
                71,
                "open",
                Some("failure"),
                Some("changes_requested"),
                Some("dirty"),
            )],
            "attn#71",
            "conflicts",
        ),
        (
            "failing checks before requested changes",
            vec![with(
                71,
                "open",
                Some("failure"),
                Some("changes_requested"),
                None,
            )],
            "attn#71",
            "checks failed",
        ),
        (
            "requested changes while checks still run",
            vec![with(
                71,
                "open",
                Some("pending"),
                Some("changes_requested"),
                None,
            )],
            "attn#71",
            "changes requested",
        ),
        (
            "ready to merge once approved with a clean merge",
            vec![with(71, "open", None, Some("approved"), Some("clean"))],
            "attn#71",
            "ready to merge",
        ),
        (
            "only approved while the merge is blocked",
            vec![with(71, "open", None, Some("approved"), Some("blocked"))],
            "attn#71",
            "approved",
        ),
        (
            "merged whatever its checks did",
            vec![with(71, "merged", Some("failure"), None, None)],
            "attn#71",
            "merged",
        ),
        (
            "a merged one when no open one is left",
            vec![
                pull_request(71, "merged", "2026-08-30T10:00:00Z"),
                pull_request(74, "closed", "2026-08-30T12:00:00Z"),
            ],
            "attn#71",
            "merged",
        ),
    ] {
        changes(&mut world, with_pull_requests(prs));
        let shown = texts(&world, "tile.a.provenance");
        let details = format!("Pull request {named} details");
        assert!(shown.contains(&details), "{case}: {details:?} in {shown:?}");
        assert!(
            shown.iter().any(|s| s == status),
            "{case}: {status:?} in {shown:?}"
        );
    }
    rest(&mut world);
}

#[test]
fn the_details_list_every_pull_request_open_then_merged_then_closed_newest_first_and_open_one_from_its_title()
 {
    let mut world = start(
        with_pull_requests(vec![
            pull_request(60, "closed", "2026-08-30T15:00:00Z"),
            pull_request(68, "merged", "2026-08-30T09:00:00Z"),
            pull_request(72, "open", "2026-08-30T10:00:00Z"),
            pull_request(74, "open", "2026-08-30T14:00:00Z"),
            pull_request(69, "merged", "2026-08-30T11:00:00Z"),
        ]),
        session("b", "docs", None),
    );
    world.gesture(|app| app.click(Target::key("tile.a.details")));
    let listed: Vec<String> = world
        .screen()
        .children(Target::role("list").name("Pull requests from this session"))
        .into_iter()
        .filter_map(|n| n.name?.split(' ').next().map(str::to_owned))
        .collect();
    assert_eq!(listed, ["#74", "#72", "#69", "#68", "#60"]);

    world.gesture(|app| app.click(Target::role("button").name("Open attn#74 on GitHub")));
    assert_eq!(
        world.opened_links(),
        ["https://github.com/victorarias/attn/pull/74"]
    );
    rest(&mut world);
}

fn delegate_of(id: &str, label: &str, dispatcher: &str) -> Session {
    Session {
        agent: "codex".into(),
        dispatcher_session_id: Some(format!("s-{dispatcher}")),
        ..session(id, label, None)
    }
}

fn chain(world: &World) -> Option<Vec<String>> {
    world
        .screen()
        .find(Target::role("dialog").name("Delegation chain"))
        .map(|_| {
            world
                .screen()
                .children(Target::role("tree").name("Agents in delegation chain"))
                .into_iter()
                .filter_map(|n| n.name)
                .collect()
        })
}

fn chain_levels(world: &World) -> Vec<(String, u64)> {
    world
        .screen()
        .children(Target::role("tree").name("Agents in delegation chain"))
        .into_iter()
        .map(|n| {
            let name = n.name.unwrap_or_default();
            let short = name.split(" · ").next().unwrap_or_default().to_owned();
            (short, n.level.unwrap_or(0))
        })
        .collect()
}

fn role_button(world: &World) -> Option<String> {
    world.screen().find(Target::key("tile.a.role"))?.name
}

#[test]
fn a_role_that_arrives_after_the_header_shows_and_its_chain_opens_the_agents_it_links() {
    let mut world = start_with(vec![
        session("a", "ledger sweep", None),
        session("b", "docs", None),
        session("dispatcher", "docs sweep", None),
        delegate_of("delegate", "glossary rework", "a"),
    ]);
    assert_eq!(
        role_button(&world),
        Some("Show delegation chain for ledger sweep".into()),
        "a session with a delegate is in a chain before it has a role"
    );
    assert!(world.screen().find(Target::key("tile.b.role")).is_none());

    changes(
        &mut world,
        Session {
            dispatcher_session_id: Some("s-dispatcher".into()),
            delegation_role: Some(SessionDelegationRole {
                name: "Orchestrator".into(),
                ..SessionDelegationRole::default()
            }),
            ..session("a", "ledger sweep", None)
        },
    );
    assert_eq!(
        role_button(&world),
        Some("Orchestrator · Show delegation chain for ledger sweep".into())
    );

    world.gesture(|app| app.click(Target::key("tile.a.role")));
    assert_eq!(
        chain(&world).expect("a press pins the chain"),
        [
            "docs sweep · Unknown harness",
            "ledger sweep · Orchestrator · current",
            "glossary rework · Codex",
        ]
    );
    world.gesture(|app| app.click(Target::role("tree-item").name("docs sweep · Unknown harness")));
    assert_eq!(chain(&world), None);
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(
        shown.last().map(|r| r["session_id"].clone()),
        Some("s-dispatcher".into())
    );
    rest(&mut world);
}

#[test]
fn a_pinned_chain_starts_on_its_agent_walks_with_the_arrows_home_and_end_and_opens_with_return() {
    let mut world = start_with(vec![
        delegate_of("a", "ledger sweep", "dispatcher"),
        session("b", "docs", None),
        session("dispatcher", "docs sweep", None),
        delegate_of("delegate", "glossary rework", "a"),
        delegate_of("sibling", "release notes", "dispatcher"),
    ]);
    let picked = |world: &World| {
        let keys: Vec<String> = (0..4)
            .map(|i| format!("tile.a.role.popover.agent.{i}"))
            .collect();
        let rows = chain(world).expect("the chain is open");
        assert_eq!(
            rows,
            [
                "docs sweep · Unknown harness",
                "ledger sweep · Codex · current",
                "glossary rework · Codex",
                "release notes · Codex",
            ]
        );
        keys.iter().position(|k| {
            world
                .screen()
                .find(Target::key(k.clone()))
                .is_some_and(|n| n.is("selected"))
        })
    };
    world.gesture(|app| app.click(Target::key("tile.a.role")));
    assert_eq!(picked(&world), Some(1));
    key(&mut world, Key::Down);
    assert_eq!(picked(&world), Some(2));
    key(&mut world, Key::End);
    assert_eq!(picked(&world), Some(3));
    key(&mut world, Key::Home);
    assert_eq!(picked(&world), Some(0));
    key(&mut world, Key::Up);
    assert_eq!(picked(&world), Some(0));
    key(&mut world, Key::Down);
    key(&mut world, Key::Down);
    key(&mut world, Key::Enter);
    assert_eq!(chain(&world), None);
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(
        shown.last().map(|r| r["session_id"].clone()),
        Some("s-delegate".into())
    );

    world.gesture(|app| app.click(Target::key("tile.a.role")));
    key(&mut world, Key::Escape);
    assert_eq!(chain(&world), None);
    rest(&mut world);
}

fn alder_sent(id: &str, label: &str) -> Session {
    Session {
        dispatcher: Some(ActorView {
            name: "Alder".into(),
            r#ref: "member:alder".into(),
        }),
        ..delegate_of(id, label, "gone")
    }
}

#[test]
fn a_chain_whose_first_dispatcher_attn_cannot_show_names_it_above_the_agents() {
    let mut world = start_with(vec![
        Session {
            dispatcher: Some(ActorView {
                name: "Chief".into(),
                r#ref: "member:chief".into(),
            }),
            ..session("a", "ledger sweep", None)
        },
        alder_sent("b", "docs"),
        alder_sent("sibling", "release notes"),
    ]);
    world.gesture(|app| app.click(Target::key("tile.a.role")));
    assert!(texts(&world, "tile.a.role.popover").contains(&"↑ Chief · unavailable".into()));
    key(&mut world, Key::Escape);

    world.gesture(|app| app.click(Target::key("tile.b.role")));
    assert!(texts(&world, "tile.b.role.popover").contains(&"↑ Alder · unavailable".into()));
    assert!(
        !chain(&world)
            .into_iter()
            .flatten()
            .any(|row| row.contains("Alder")),
        "a dispatcher that has ended is text, not a row to open"
    );
    assert_eq!(
        chain(&world).expect("the chain is open"),
        ["docs · Codex · current", "release notes · Codex"],
        "the delegates of a dispatcher attn cannot show are the chain's first rows"
    );
    rest(&mut world);
}

#[test]
fn a_chain_never_loops_around_a_dispatch_cycle() {
    let mut world = start_with(vec![
        delegate_of("a", "first", "b"),
        delegate_of("b", "second", "a"),
    ]);
    world.gesture(|app| app.click(Target::key("tile.a.role")));
    let mut rows = chain(&world).expect("the chain is open");
    rows.sort();
    assert_eq!(rows, ["first · Codex · current", "second · Codex"]);
    rest(&mut world);
}

#[test]
fn a_chain_walks_the_whole_tree_from_its_root_with_each_agents_depth() {
    let mut world = start_with(vec![
        delegate_of("a", "grandchild", "child"),
        session("b", "unrelated", None),
        session("root", "root session", None),
        delegate_of("child", "child", "root"),
        delegate_of("peer", "peer", "root"),
    ]);
    assert!(world.screen().find(Target::key("tile.b.role")).is_none());
    world.gesture(|app| app.click(Target::key("tile.a.role")));
    assert_eq!(
        chain_levels(&world),
        [
            ("root session".to_owned(), 1),
            ("child".to_owned(), 2),
            ("grandchild".to_owned(), 3),
            ("peer".to_owned(), 2),
        ]
    );
    rest(&mut world);
}

#[test]
fn the_details_of_one_pull_request_spell_out_every_status_the_daemon_reported() {
    let mut pr = pull_request(71, "open", "2026-08-30T12:00:00Z");
    pr.status_fetched_at = Some("2026-08-30T12:05:00Z".into());
    pr.ci_status = Some("failure".into());
    pr.review_status = Some("pending".into());
    pr.mergeable_state = Some("clean".into());
    let mut world = start(with_pull_requests(vec![pr]), session("b", "docs", None));
    world.gesture(|app| app.click(Target::key("tile.a.details")));
    let shown = details(&world).expect("a press pins the details");
    let lines: Vec<&str> = shown.lines().collect();
    let after = |name: &str| {
        lines
            .iter()
            .position(|l| *l == name)
            .and_then(|i| lines.get(i + 1).copied())
            .unwrap_or_else(|| panic!("{name:?} in {shown}"))
    };
    assert_eq!(after("checks"), "failed");
    assert_eq!(after("review"), "waiting on a reviewer");
    assert_eq!(after("merge"), "no conflicts");
    assert_eq!(after("state"), "open");
    assert!(after("opened").contains(" ago · "), "{shown}");
    assert!(lines.contains(&"↵ open · c copy url"), "{shown}");
    assert!(
        world
            .screen()
            .find(Target::role("list").name("Pull requests from this session"))
            .is_none(),
        "one pull request has no list to pick from"
    );
    rest(&mut world);
}

fn focused(world: &World) -> Option<String> {
    world
        .screen()
        .all(Target::role("button"))
        .into_iter()
        .chain(world.screen().all(Target::role("dialog")))
        .find(|n| n.is("focused"))
        .and_then(|n| n.key)
}

#[test]
fn the_command_palette_pins_the_delegation_chain_of_the_agent_on_screen() {
    let mut world = start_with(vec![
        delegate_of("a", "ledger sweep", "dispatcher"),
        session("b", "docs", None),
        session("dispatcher", "docs sweep", None),
    ]);
    run_command(&mut world, "delegation chain");
    assert_eq!(
        chain(&world).expect("the command pins the chain"),
        [
            "docs sweep · Unknown harness",
            "ledger sweep · Codex · current"
        ]
    );
    assert_eq!(focused(&world).as_deref(), Some("tile.a.role.popover"));
    key(&mut world, Key::Up);
    key(&mut world, Key::Enter);
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(
        shown.last().map(|r| r["session_id"].clone()),
        Some("s-dispatcher".into())
    );
    rest(&mut world);
}

#[test]
fn a_chain_taller_than_the_window_fits_it_and_scrolls_to_the_agent_it_picks() {
    let mut sessions = vec![
        delegate_of("a", "ledger sweep", "d59"),
        session("b", "docs", None),
        session("root", "root session", None),
    ];
    sessions
        .extend((0..60).map(|i| delegate_of(&format!("d{i}"), &format!("delegate {i}"), "root")));
    let mut world = start_with(sessions);
    world.gesture(|app| app.click(Target::key("tile.a.role")));
    world.settle();
    let drawn = |world: &World, key: &str| {
        world
            .runtime()
            .presented(key)
            .unwrap_or_else(|| panic!("{key} is drawn"))
            .0
    };
    let [_, card_y, _, card_h] = drawn(&world, "tile.a.role.popover");
    assert!(
        card_y >= 0.0 && card_y + card_h <= SIZE.1,
        "the card fits the window: {card_y} + {card_h}"
    );
    let shows = |world: &World, row: usize| {
        let [_, top, _, h] = drawn(world, "tile.a.role.popover.agents");
        let [_, y, _, rh] = drawn(world, &format!("tile.a.role.popover.agent.{row}"));
        y >= top - 0.5 && y + rh <= top + h + 0.5
    };
    let current = 61;
    assert_eq!(chain_levels(&world)[current].0, "ledger sweep");
    assert!(
        shows(&world, current),
        "the chain opens scrolled to its agent"
    );
    assert!(!shows(&world, 0), "the root is out of view");

    let resting = middle(&world, "tile.a.role.popover.agent.58");
    world.gesture(|app| app.move_to(resting));
    let picked = |world: &World| {
        (0..62).find(|i| {
            world
                .screen()
                .find(Target::key(format!("tile.a.role.popover.agent.{i}")))
                .is_some_and(|n| n.is("selected"))
        })
    };
    assert_eq!(
        picked(&world),
        Some(58),
        "moving the pointer picks the row under it"
    );

    key(&mut world, Key::Home);
    world.settle();
    assert!(shows(&world, 0), "Home brings the root into view");
    assert_eq!(
        picked(&world),
        Some(0),
        "the row the scroll brings under the still pointer leaves the pick to the keys"
    );
    key(&mut world, Key::End);
    world.settle();
    assert!(shows(&world, current), "End brings the last agent back");
    rest(&mut world);
}
