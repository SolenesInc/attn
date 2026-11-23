use std::collections::BTreeMap;

use attn_protocol::{
    Desktop, DesktopPane, Harness, HarnessCatalogMessage, HarnessLogo, HarnessLogoResultMessage,
    InitialStateMessage, Profile, Session,
};
use attn_testkit::{Answer, Daemon, Target, World, message, tree};

const HASH_A: &str = "deadbeef0123456789abcdef01234567";
const HASH_B: &str = "cafe0000111122223333444455556666";
const LOGO_A: &str = r##"<rect x="2" y="2" width="20" height="20" rx="4" fill="#d97757"/>"##;
const LOGO_B: &str = r##"<circle cx="12" cy="12" r="10" fill="#2f8f83"/>"##;

fn plugin_state() -> InitialStateMessage {
    let session = Session {
        id: "s1".into(),
        label: "nebula-work".into(),
        agent: "nebula".into(),
        state: "working".into(),
        profile_id: "work".into(),
        ..Session::default()
    };
    let desktop = Desktop {
        id: "d1".into(),
        name: "Work".into(),
        order_key: "a".into(),
        profile_id: "work".into(),
        tree_json: tree::pane("p1").to_string(),
        panes: vec![DesktopPane {
            pane_id: "p1".into(),
            session_id: "s1".into(),
            ..DesktopPane::default()
        }],
        ..Desktop::default()
    };
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: "d1".into(),
            ..Profile::default()
        }]),
        desktops: Some(vec![desktop]),
        sessions: Some(vec![session]),
        settings: Some(BTreeMap::from([(
            "sidebar_harness_logos_enabled".into(),
            "true".into(),
        )])),
        ..InitialStateMessage::new()
    }
}

fn nebula(hash: Option<&str>) -> Harness {
    Harness {
        id: "nebula".into(),
        name: "Nebula Agent".into(),
        installation: "installed".into(),
        offered: true,
        logo: hash.map(str::to_owned),
        ..Harness::default()
    }
}

fn answer_logo(hash: Option<&str>) -> serde_json::Value {
    let logo = match hash {
        Some(HASH_A) => Some(HarnessLogo {
            view: 24.0,
            body: LOGO_A.into(),
        }),
        Some(HASH_B) => Some(HarnessLogo {
            view: 24.0,
            body: LOGO_B.into(),
        }),
        _ => None,
    };
    message(HarnessLogoResultMessage {
        success: logo.is_some(),
        logo,
        ..HarnessLogoResultMessage::new()
    })
}

/// The painted icon instances inside the sidebar lead slot, identified by their render kind and UV.
fn painted_lead(world: &World) -> Vec<(u32, [u32; 4])> {
    let scale = world.runtime().env.scale;
    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key("sidebar.session.s1.state"))
        .expect("sidebar lead bounds")
        .map(|v| v * scale);
    world
        .runtime()
        .instances
        .iter()
        .filter(|inst| {
            let [ix, iy, iw, ih] = inst.rect;
            iw > 0.0
                && ih > 0.0
                && ix >= x - scale
                && iy >= y - scale
                && ix + iw <= x + w + scale
                && iy + ih <= y + h + scale
                && inst.params[1] != rune::render::KIND_RECT
        })
        .map(|inst| (inst.params[1].to_bits(), inst.uv.map(f32::to_bits)))
        .collect()
}

#[test]
fn plugin_logo_fetches_once_draws_and_updates_when_hash_changes_or_clears() {
    let daemon = Daemon::scripted()
        .state(plugin_state())
        .harnesses(vec![nebula(Some(HASH_A))]);
    daemon.answer_in_turn(
        "harness_logo",
        [Answer::Hold, Answer::Reply(answer_logo(Some(HASH_B)))],
    );
    let mut world = World::start(daemon);
    world.open_desktop();

    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.session.s1.state"))
            .name
            .as_deref(),
        Some("Nebula Agent · working")
    );
    let fallback = painted_lead(&world);
    assert!(!fallback.is_empty(), "fallback mark paints while fetching");

    let first = world.daemon.sent_of("harness_logo").remove(0);
    assert_eq!(first["hash"], HASH_A);
    world.daemon.reply_to(&first, answer_logo(Some(HASH_A)));
    world.settle();

    let drawn_a = painted_lead(&world);
    assert_ne!(drawn_a, fallback, "loaded logo replaces fallback mark");
    assert!(world.runtime_is_at_rest());

    world.daemon.emit(HarnessCatalogMessage {
        harnesses: vec![nebula(Some(HASH_A))],
        ..HarnessCatalogMessage::new()
    });
    world.settle();
    assert_eq!(
        world.daemon.sent_of("harness_logo").len(),
        1,
        "same hash is never re-fetched"
    );

    world.daemon.emit(HarnessCatalogMessage {
        harnesses: vec![nebula(Some(HASH_B))],
        ..HarnessCatalogMessage::new()
    });
    world.settle();
    let drawn_b = painted_lead(&world);
    assert_ne!(drawn_b, drawn_a, "new hash replaces painted logo");
    assert_eq!(world.daemon.sent_of("harness_logo").len(), 2);
    assert!(world.runtime_is_at_rest());

    world.daemon.emit(HarnessCatalogMessage {
        harnesses: vec![nebula(Some(HASH_A))],
        ..HarnessCatalogMessage::new()
    });
    world.settle();
    assert_eq!(
        painted_lead(&world),
        drawn_a,
        "switching back to an already-cached hash updates the memoized sidebar row"
    );
    assert_eq!(world.daemon.sent_of("harness_logo").len(), 2);

    world.daemon.emit(HarnessCatalogMessage {
        harnesses: vec![nebula(None)],
        ..HarnessCatalogMessage::new()
    });
    world.settle();
    assert_eq!(
        painted_lead(&world),
        fallback,
        "clearing logo returns the memoized sidebar row to the fallback mark"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn plugin_logo_persists_across_launches_without_calling_the_daemon_again() {
    let shared_data = tempfile::tempdir().expect("shared data folder");
    let data_dir = shared_data.path().to_path_buf();

    let first_daemon = Daemon::scripted()
        .state(plugin_state())
        .harnesses(vec![nebula(Some(HASH_A))]);
    first_daemon.on("harness_logo", |r| answer_logo(r["hash"].as_str()));
    let mut first = World::start_with_data_dir(first_daemon, data_dir.clone());
    first.open_desktop();
    assert_eq!(first.daemon.sent_of("harness_logo").len(), 1);
    let expected = painted_lead(&first);
    drop(first);

    let second_daemon = Daemon::scripted()
        .state(plugin_state())
        .harnesses(vec![nebula(Some(HASH_A))]);
    let mut second = World::start_with_data_dir(second_daemon, data_dir);
    second.open_desktop();
    assert!(
        second.daemon.sent_of("harness_logo").is_empty(),
        "second launch reads the logo from disk without calling harness_logo"
    );
    assert_eq!(
        painted_lead(&second).first().map(|(kind, _)| *kind),
        expected.first().map(|(kind, _)| *kind),
        "second launch paints the cached color logo, not the monochrome fallback"
    );
    assert!(second.runtime_is_at_rest());
}

#[test]
fn plugin_that_leaves_after_a_missed_logo_fetches_again_when_it_rejoins() {
    let daemon = Daemon::scripted()
        .state(plugin_state())
        .harnesses(vec![nebula(Some(HASH_A))]);
    daemon.answer_in_turn(
        "harness_logo",
        [
            Answer::Reply(answer_logo(None)),
            Answer::Reply(answer_logo(Some(HASH_A))),
        ],
    );
    let mut world = World::start(daemon);
    world.open_desktop();
    let fallback = painted_lead(&world);
    assert_eq!(world.daemon.sent_of("harness_logo").len(), 1);

    world.daemon.emit(HarnessCatalogMessage {
        harnesses: Vec::new(),
        ..HarnessCatalogMessage::new()
    });
    world.settle();
    world.daemon.emit(HarnessCatalogMessage {
        harnesses: vec![nebula(Some(HASH_A))],
        ..HarnessCatalogMessage::new()
    });
    world.settle();

    assert_eq!(world.daemon.sent_of("harness_logo").len(), 2);
    assert_ne!(
        painted_lead(&world),
        fallback,
        "rejoined plugin fetches and paints its logo"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_monochrome_symbol_use_plugin_logo_paints_in_the_sidebar() {
    const HASH: &str = "11223344556677889900aabbccddeeff";
    const SYMBOL_USE: &str = r##"<defs><symbol id="m" viewBox="0 0 24 24"><path d="M4 4h16v16H4z"/></symbol></defs><use href="#m" fill="currentColor"/>"##;
    let daemon = Daemon::scripted()
        .state(plugin_state())
        .harnesses(vec![nebula(Some(HASH))]);
    daemon.answer_in_turn("harness_logo", [Answer::Hold]);
    let mut world = World::start(daemon);
    world.open_desktop();
    let fallback = painted_lead(&world);
    assert!(!fallback.is_empty());

    let req = world.daemon.sent_of("harness_logo").remove(0);
    world.daemon.reply_to(
        &req,
        message(HarnessLogoResultMessage {
            success: true,
            logo: Some(HarnessLogo {
                view: 24.0,
                body: SYMBOL_USE.into(),
            }),
            ..HarnessLogoResultMessage::new()
        }),
    );
    world.settle();

    let drawn = painted_lead(&world);
    assert!(!drawn.is_empty(), "symbol/use currentColor logo must paint");
    assert_ne!(
        drawn, fallback,
        "symbol/use currentColor logo replaces fallback"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_unrenderable_plugin_logo_keeps_the_fallback_mark() {
    const HASH: &str = "ffeeddccbbaa00998877665544332211";
    let dir = tempfile::tempdir().expect("logo cache dir");
    let daemon = Daemon::scripted()
        .state(plugin_state())
        .harnesses(vec![nebula(Some(HASH))]);
    daemon.answer_in_turn("harness_logo", [Answer::Hold]);
    let mut world = World::start_with_data_dir(daemon, dir.path().to_path_buf());
    world.open_desktop();
    let fallback = painted_lead(&world);
    assert!(!fallback.is_empty());

    let req = world.daemon.sent_of("harness_logo").remove(0);
    world.daemon.reply_to(
        &req,
        message(HarnessLogoResultMessage {
            success: true,
            logo: Some(HarnessLogo {
                view: 24.0,
                body: r##"<use href="#missing" fill="currentColor"/>"##.into(),
            }),
            ..HarnessLogoResultMessage::new()
        }),
    );
    world.settle();

    assert_eq!(
        painted_lead(&world),
        fallback,
        "an SVG that renders nothing must keep the visible fallback mark"
    );
    assert!(
        !dir.path()
            .join("cache/harness-logos")
            .join(format!("{HASH}.json"))
            .exists(),
        "an unrenderable logo must not be cached to disk"
    );
    assert!(world.runtime_is_at_rest());
}
