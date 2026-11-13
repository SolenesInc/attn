use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use attn_protocol::{
    AnalyticsBuild, AnalyticsOutboxRow, ClearAnalyticsRecordsResultMessage,
    ForgetAnalyticsInstallResultMessage, GardenSeedsUpdatedMessage,
    GetAnalyticsStatusResultMessage, InitialStateMessage, ListAnalyticsRecordsResultMessage,
    OutpostInfo, OutpostsUpdatedMessage, PlantVentSeedResultMessage, Profile, Seed,
    SetVentDismissedResultMessage, SetVentKindResultMessage, SetVentSendResultMessage,
    UnplantVentSeedResultMessage, Vent, VentContext, VentTranscript, VentsUpdatedMessage,
};
use attn_testkit::{Daemon, Request, Target, World, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

use super::super::garden::seed;
use super::super::sidebar::session;
use super::{accepting, click, open, state, writes};

const PROFILE: &str = "work";
const INGEST: &str = "https://attn-telemetry.caketroll.cloud";

fn build(dirty: bool) -> AnalyticsBuild {
    AnalyticsBuild {
        version: "0.42.1".into(),
        commit: "a3f9e1c22".into(),
        channel: "source".into(),
        dirty,
        os: "darwin".into(),
        arch: "arm64".into(),
        ..AnalyticsBuild::default()
    }
}

/// The vent the sharing tests triage: an attn vent from a live session, with no stake seed.
fn plain_vent() -> Vent {
    Vent {
        id: "v-new".into(),
        session_id: "s1".into(),
        kind: "attn".into(),
        text: "`attn seed tend s-f68sja` refused: held by a session that ended two hours ago."
            .into(),
        created_at: "2026-10-24T14:02:11Z".into(),
        context: VentContext {
            session_label: "fix garden bell staleness".into(),
            harness: "claude".into(),
            build: build(false),
            ..VentContext::default()
        },
        ..Vent::default()
    }
}

/// The vent the triage tests work on: with its model, stake seed and transcript.
fn vent(id: &str) -> Vent {
    let mut vent = plain_vent();
    vent.id = id.into();
    vent.context = VentContext {
        harness_version: Some("2.4.1".into()),
        model: Some("claude-opus-5-5".into()),
        model_requested: Some("opus".into()),
        seed_id: Some("s-stake1".into()),
        build: build(true),
        experimental: vec!["asks_enabled".into()],
        transcript: Some(VentTranscript {
            path: "/tmp/transcript.jsonl".into(),
            bytes: 4096,
        }),
        ..vent.context
    };
    vent
}

fn withdrawn() -> Vent {
    let mut gone = vent("v-gone");
    gone.text = "kubectl cannot reach the cluster.".into();
    gone.withdrawn_at = Some("2026-10-24T14:05:00Z".into());
    gone.context.session_label = "songcheck deploy".into();
    gone
}

fn newer() -> Vent {
    let mut newer = vent("v-newer");
    newer.text = "make test ran the Go suite for an app-only change.".into();
    newer
}

fn slugged(id: &str, slug: &str) -> Seed {
    Seed {
        step_slug: slug.into(),
        ..seed(id, slug)
    }
}

fn status() -> GetAnalyticsStatusResultMessage {
    GetAnalyticsStatusResultMessage {
        success: true,
        records: 40,
        bytes: 4096,
        waiting: 12,
        last_sent_at: Some("2026-10-24T14:00:12Z".into()),
        ingest_url: INGEST.into(),
        ..GetAnalyticsStatusResultMessage::new()
    }
}

fn answering_status(
    status: GetAnalyticsStatusResultMessage,
) -> impl Fn(&Request) -> Value + Send + 'static {
    move |r| {
        message(GetAnalyticsStatusResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ..status.clone()
        })
    }
}

fn request_id(r: &Request) -> String {
    r["request_id"].as_str().unwrap().into()
}

fn vent_id(r: &Request) -> String {
    r["vent_id"].as_str().unwrap().into()
}

type Vents = Arc<Mutex<Vec<Vent>>>;

fn updated(vents: &Vents) -> Value {
    message(VentsUpdatedMessage {
        profile_id: PROFILE.into(),
        vents: vents.lock().unwrap().clone(),
        ..VentsUpdatedMessage::new()
    })
}

/// A daemon that keeps the vents as the real one does: each change is answered, then
/// broadcast.
fn serve_vents(daemon: &Daemon, vents: Vents) {
    let kept = vents.clone();
    daemon.on("set_vent_send", move |r: &Request| {
        let send = r["send"].as_bool().unwrap();
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            v.send = send.then(|| "queued".into());
        }
        vec![
            message(SetVentSendResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                ..SetVentSendResultMessage::new()
            }),
            updated(&kept),
        ]
    });
    let kept = vents.clone();
    daemon.on("set_vent_kind", move |r: &Request| {
        let kind = r["kind"].as_str().unwrap().to_owned();
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            if kind == "tools" {
                v.send = None;
            }
            v.kind = kind.clone();
        }
        vec![
            message(SetVentKindResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                ..SetVentKindResultMessage::new()
            }),
            updated(&kept),
        ]
    });
    let kept = vents.clone();
    daemon.on("set_vent_dismissed", move |r: &Request| {
        let dismissed = r["dismissed"].as_bool().unwrap();
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            v.dismissed_at = dismissed.then(|| "2026-10-24T14:10:00Z".into());
        }
        vec![
            message(SetVentDismissedResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                ..SetVentDismissedResultMessage::new()
            }),
            updated(&kept),
        ]
    });
    let kept = vents.clone();
    daemon.on("plant_vent_seed", move |r: &Request| {
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            v.planted_seed_id = Some("s-planted".into());
        }
        let planted = slugged("s-planted", "seed-tend-refused-held-session");
        vec![
            message(PlantVentSeedResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                seed: Some(planted.clone()),
                ..PlantVentSeedResultMessage::new()
            }),
            message(GardenSeedsUpdatedMessage {
                profile_id: PROFILE.into(),
                seeds: vec![slugged("s-stake1", "garden-show-stale-bell"), planted],
                total: 2,
                ..GardenSeedsUpdatedMessage::new()
            }),
            updated(&kept),
        ]
    });
    let kept = vents;
    daemon.on("unplant_vent_seed", move |r: &Request| {
        for v in kept
            .lock()
            .unwrap()
            .iter_mut()
            .filter(|v| v.id == vent_id(r))
        {
            v.planted_seed_id = None;
        }
        vec![
            message(UnplantVentSeedResultMessage {
                request_id: request_id(r),
                success: true,
                vent_id: vent_id(r),
                ..UnplantVentSeedResultMessage::new()
            }),
            updated(&kept),
        ]
    });
    daemon.on("forget_analytics_install", |r: &Request| {
        message(ForgetAnalyticsInstallResultMessage {
            request_id: request_id(r),
            success: true,
            ..ForgetAnalyticsInstallResultMessage::new()
        })
    });
}

/// The dialog open on Analytics, turned on with `settings`, over a daemon that holds `vents`
/// and `seeds` and the live session s1.
fn on_analytics(
    settings: &[(&str, &str)],
    vents: Vec<Vent>,
    seeds: Vec<Seed>,
    serve: impl FnOnce(&Daemon),
) -> World {
    let mut all = vec![("analytics.enabled", "true")];
    all.extend_from_slice(settings);
    let (daemon, _) = accepting(&all);
    let daemon = daemon.state(InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(
            all.iter()
                .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
                .collect::<BTreeMap<_, _>>(),
        ),
        sessions: Some(vec![session("s1", "idle")]),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds),
        vents: Some(vents),
        ..InitialStateMessage::new()
    });
    serve(&daemon);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.analytics");
    world
}

/// Analytics as the sharing tests open it: `settings` on, `status` read, one vent.
fn sharing(
    settings: &[(&str, &str)],
    status: GetAnalyticsStatusResultMessage,
    vents: Vec<Vent>,
) -> World {
    let kept: Vents = Arc::new(Mutex::new(vents.clone()));
    on_analytics(settings, vents, Vec::new(), move |daemon| {
        daemon.on("get_analytics_status", answering_status(status));
        serve_vents(daemon, kept);
    })
}

/// Analytics as the vent tests open it: a new vent and a withdrawn one, with the stake seed.
fn triage() -> World {
    triage_of(vec![vent("v-new"), withdrawn()])
}

fn triage_of(vents: Vec<Vent>) -> World {
    let kept: Vents = Arc::new(Mutex::new(vents.clone()));
    on_analytics(
        &[],
        vents,
        vec![slugged("s-stake1", "garden-show-stale-bell")],
        move |daemon| {
            daemon.on(
                "get_analytics_status",
                answering_status(GetAnalyticsStatusResultMessage {
                    records: 0,
                    bytes: 0,
                    waiting: 0,
                    last_sent_at: None,
                    ..status()
                }),
            );
            serve_vents(daemon, kept);
        },
    )
}

fn press(world: &mut World, typed: &str) {
    let key = match typed {
        "Enter" => Key::Enter,
        other => Key::Text(other.into()),
    };
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn named(world: &World, key: &str) -> String {
    world
        .screen()
        .find(Target::key(key))
        .and_then(|n| n.name)
        .unwrap_or_default()
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

/// Every name under the element, in reading order.
fn text_of(world: &World, key: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn reads_as(world: &World, key: &str) -> String {
    text_of(world, key).join(" ")
}

fn inspector(world: &World) -> Vec<String> {
    text_of(world, "settings.analytics.inspector")
}

fn machine(world: &World) -> String {
    reads_as(world, "settings.analytics.machine")
}

/// The rows of the shown list, each with the group it sits in.
fn rows(world: &World) -> Vec<String> {
    let mut group = String::new();
    let mut rows = Vec::new();
    for node in world
        .screen()
        .descendants(Target::key("settings.analytics.list"))
    {
        match node.role.as_str() {
            "group" => group = node.name.unwrap_or_default(),
            "list-box-option" => rows.push(format!("{group}: {}", node.name.unwrap_or_default())),
            _ => {}
        }
    }
    rows
}

/// What the last action said.
fn said(world: &World) -> String {
    let toast = world.screen().get(Target::key("settings.analytics.toast"));
    toast
        .description
        .filter(|d| !d.is_empty())
        .or(toast.name)
        .unwrap_or_default()
}

fn sent(world: &World, cmd: &str, field: &str) -> Vec<Value> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|r| r[field].clone())
        .collect()
}

fn stat(world: &World, id: &str) -> String {
    named(world, &format!("settings.analytics.stat.{id}"))
}

fn record(id: i64, stream: &str, name: &str, at: &str, extra: &str) -> AnalyticsOutboxRow {
    AnalyticsOutboxRow {
        id,
        record: format!(r#"{{"stream":"{stream}","seq":{id},"name":"{name}","at":"{at}"{extra}}}"#),
    }
}

fn records() -> Vec<AnalyticsOutboxRow> {
    vec![
        record(
            9,
            "bus",
            "session.state.changed",
            "2026-10-24T14:03:12Z",
            r#","subject":"h:77c2e01fa4d3""#,
        ),
        record(
            8,
            "bus",
            "pr.updated",
            "2026-10-24T14:02:58Z",
            r#","subject":"h:0d7e44a1b9c2""#,
        ),
        record(
            7,
            "bus",
            "pr.updated",
            "2026-10-24T14:02:58Z",
            r#","subject":"h:3a19f0c27e18""#,
        ),
        record(
            6,
            "bus",
            "pr.updated",
            "2026-10-24T14:02:58Z",
            r#","subject":"h:c4e1022b9d07""#,
        ),
        record(
            5,
            "app",
            "shortcut",
            "2026-10-24T14:00:44Z",
            r#","props":{"id":"queue.next"}"#,
        ),
    ]
}

/// A daemon that keeps the records as the real one does, filtering by stream and clearing them.
fn serve_records(daemon: &Daemon) {
    let stored = Arc::new(Mutex::new(records()));
    let kept = stored.clone();
    daemon.on("list_analytics_records", move |r: &Request| {
        let stream = r["stream"].as_str().map(|s| format!(r#""stream":"{s}""#));
        let rows: Vec<AnalyticsOutboxRow> = kept
            .lock()
            .unwrap()
            .iter()
            .filter(|row| stream.as_ref().is_none_or(|s| row.record.contains(s)))
            .cloned()
            .collect();
        message(ListAnalyticsRecordsResultMessage {
            request_id: request_id(r),
            success: true,
            waiting: rows.len() as i64,
            records: Some(rows),
            ..ListAnalyticsRecordsResultMessage::new()
        })
    });
    let kept = stored.clone();
    daemon.on("get_analytics_status", move |r: &Request| {
        let rows = kept.lock().unwrap();
        message(GetAnalyticsStatusResultMessage {
            request_id: request_id(r),
            success: true,
            records: rows.len() as i64,
            bytes: rows.iter().map(|row| row.record.len() as i64).sum(),
            waiting: rows.len() as i64,
            ingest_url: INGEST.into(),
            ..GetAnalyticsStatusResultMessage::new()
        })
    });
    daemon.on("clear_analytics_records", move |r: &Request| {
        let mut rows = stored.lock().unwrap();
        let records = rows.len() as i64;
        rows.clear();
        message(ClearAnalyticsRecordsResultMessage {
            request_id: request_id(r),
            success: true,
            records,
            vents: 1,
            ..ClearAnalyticsRecordsResultMessage::new()
        })
    });
}

fn on_records() -> World {
    on_analytics(&[], Vec::new(), Vec::new(), serve_records)
}

#[test]
fn no_record_list_is_read_until_one_is_shown_then_one_page_per_list() {
    let mut world = on_records();
    assert!(world.daemon.sent_of("list_analytics_records").is_empty());

    press(&mut world, "]");
    press(&mut world, "]");

    let asked: Vec<(Value, Value, Value)> = world
        .daemon
        .sent_of("list_analytics_records")
        .iter()
        .map(|r| {
            (
                r["stream"].clone(),
                r["limit"].clone(),
                r["before_id"].clone(),
            )
        })
        .collect();
    assert_eq!(
        asked,
        [
            (Value::Null, json!(200), Value::Null),
            (json!("bus"), json!(200), Value::Null)
        ]
    );
}

#[test]
fn a_same_second_burst_folds_opens_with_enter_and_each_record_shows_exactly_as_stored() {
    let mut world = on_records();
    press(&mut world, "]");
    let listed = rows(&world);
    assert_eq!(listed.len(), 3, "{listed:?}");
    assert!(listed[1].contains("pr.updated ×3"), "{listed:?}");

    press(&mut world, "j");
    assert_eq!(
        named(&world, "settings.analytics.record.heading"),
        "3 records in the same second"
    );

    press(&mut world, "Enter");
    assert_eq!(rows(&world).len(), 5);
    assert!(inspector(&world).contains(&records()[1].record));
    press(&mut world, "j");
    press(&mut world, "j");
    press(&mut world, "j");
    assert!(inspector(&world).contains(&records()[4].record));
}

#[test]
fn records_are_read_again_only_when_asked() {
    let mut world = on_records();
    press(&mut world, "]");
    press(&mut world, "r");
    click(&mut world, "settings.analytics.refresh");
    assert_eq!(world.daemon.sent_of("list_analytics_records").len(), 3);
}

#[test]
fn the_record_count_and_size_show_and_clear_empties_them_after_a_confirm() {
    let mut world = on_records();
    let size: usize = records().iter().map(|r| r.record.len()).sum();
    assert_eq!(stat(&world, "kept"), format!("7 days · {size} B"));

    press(&mut world, "m");
    assert!(machine(&world).contains(&format!(
        "5 records, {size} B. Dismissed and withdrawn vents are kept 30 days."
    )));
    click(&mut world, "settings.analytics.clear");
    assert!(world.daemon.sent_of("clear_analytics_records").is_empty());
    click(&mut world, "settings.analytics.confirm.yes");

    assert_eq!(world.daemon.sent_of("clear_analytics_records").len(), 1);
    assert_eq!(said(&world), "Cleared 5 records and 1 dismissed vent.");
    assert!(machine(&world).contains("0 records, 0 B."));
    assert_eq!(world.daemon.sent_of("get_analytics_status").len(), 2);
}

#[test]
fn turning_analytics_off_keeps_its_page_open_marked_off_and_unusable() {
    let mut world = on_analytics(&[], Vec::new(), Vec::new(), |_| {});
    assert!(!shows(&world, "settings.nav-off.analytics"));

    click(&mut world, "settings.feature.analytics.enabled");

    assert!(writes(&world).contains(&("analytics.enabled".to_owned(), "false".to_owned())));
    assert_eq!(state(&mut world)["activeSection"], "analytics");
    assert!(shows(&world, "settings.analytics"));
    assert!(shows(&world, "settings.nav-off.analytics"));
    let content = world
        .screen()
        .get(Target::key("settings.section.analytics"));
    assert!(content.is("disabled"), "{content:?}");
}

fn outpost(reach: &str) -> OutpostInfo {
    OutpostInfo {
        id: "o1".into(),
        name: "ai-sandbox".into(),
        destination: "ai-sandbox".into(),
        state: if reach == "reachable" {
            "connected".into()
        } else {
            "lost".into()
        },
        reach: reach.into(),
        state_since: "2026-10-24T14:00:00Z".into(),
        ..OutpostInfo::default()
    }
}

fn outposts(world: &mut World, reach: &str) {
    world.daemon.emit(OutpostsUpdatedMessage {
        outposts: vec![outpost(reach)],
        ..OutpostsUpdatedMessage::new()
    });
    world.settle();
}

#[test]
fn an_unreachable_outpost_is_named_until_it_reconnects() {
    let mut world = sharing(&[], status(), vec![plain_vent()]);
    assert!(!shows(&world, "settings.analytics.stat.outposts"));

    outposts(&mut world, "unreachable");
    assert_eq!(stat(&world, "outposts"), "1 of 1 unreachable");
    assert_eq!(
        named(&world, "settings.analytics.outposts"),
        "ai-sandbox: unreachable, so what it holds isn't shown here."
    );

    outposts(&mut world, "reachable");
    assert_eq!(stat(&world, "outposts"), "Up to 5 min behind");
    assert!(!shows(&world, "settings.analytics.outposts"));
}

#[test]
fn with_sharing_off_every_vent_is_kept_here_and_x_does_nothing() {
    let mut world = sharing(&[], status(), vec![plain_vent()]);
    assert_eq!(stat(&world, "sharing"), "Off");
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination-note"),
        "· sharing is off"
    );

    press(&mut world, "x");
    assert!(world.daemon.sent_of("set_vent_send").is_empty());
}

#[test]
fn a_vent_queued_earlier_waits_for_sharing_and_x_keeps_it_here() {
    let queued = Vent {
        send: Some("queued".into()),
        ..plain_vent()
    };
    let mut world = sharing(&[("analytics.share_vents", "true")], status(), vec![queued]);
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Sending once sharing is on"
    );

    press(&mut world, "x");
    assert_eq!(sent(&world, "set_vent_send", "send"), [json!(false)]);
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );

    press(&mut world, "m");
    assert!(machine(&world).contains("New attn vents are sent with their text."));
}

#[test]
fn x_sends_a_vent_x_again_keeps_it_and_u_undoes() {
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        status(),
        vec![plain_vent()],
    );
    assert!(
        stat(&world, "sharing").starts_with("On · sent "),
        "{}",
        stat(&world, "sharing")
    );

    press(&mut world, "x");
    assert_eq!(
        said(&world),
        "Sending “fix garden bell staleness” with the next batch."
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Sending with the next batch"
    );
    press(&mut world, "x");
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );
    press(&mut world, "u");

    assert_eq!(
        sent(&world, "set_vent_send", "send"),
        [json!(true), json!(false), json!(true)]
    );
}

#[test]
fn a_tool_vent_stays_here_until_s_files_it_under_attn_and_u_puts_it_back_in_the_queue() {
    let tools = Vent {
        kind: "tools".into(),
        ..plain_vent()
    };
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        status(),
        vec![tools],
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination-note"),
        "· tool vents never leave it"
    );
    assert!(rows(&world)[0].contains("fix garden bell staleness · tools"));
    press(&mut world, "x");
    assert!(world.daemon.sent_of("set_vent_send").is_empty());

    press(&mut world, "s");
    assert_eq!(
        said(&world),
        "Filed “fix garden bell staleness” under attn."
    );
    assert!(!shows(&world, "settings.analytics.vent.destination-note"));
    press(&mut world, "x");
    press(&mut world, "s");
    assert_eq!(
        said(&world),
        "Filed “fix garden bell staleness” under tools; it stays on this machine."
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );
    assert_eq!(
        sent(&world, "set_vent_kind", "kind"),
        [json!("attn"), json!("tools")]
    );
    assert_eq!(sent(&world, "set_vent_send", "send"), [json!(true)]);

    press(&mut world, "u");
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Sending with the next batch"
    );
    assert_eq!(
        sent(&world, "set_vent_kind", "kind"),
        [json!("attn"), json!("tools"), json!("attn")]
    );
    assert_eq!(
        sent(&world, "set_vent_send", "send"),
        [json!(true), json!(true)]
    );
}

#[test]
fn tool_vents_turn_on_from_the_machine_settings() {
    let mut world = sharing(&[], status(), vec![plain_vent()]);
    press(&mut world, "m");
    assert!(
        named(&world, "settings.analytics.machine.tool-vents.copy")
            .starts_with("Agents vent only about attn.")
    );

    click(&mut world, "settings.analytics.switch.analytics.tool_vents");
    assert_eq!(
        writes(&world),
        [("analytics.tool_vents".to_owned(), "true".to_owned())]
    );
}

const REFUSAL: &str = "attn-ingest refused the batch: events_per_install_per_minute is 10000, and this batch would make it 10200; retrying in 41s";

#[test]
fn a_refusal_shows_with_its_limit_on_the_strip_and_in_the_share_row() {
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        GetAnalyticsStatusResultMessage {
            last_refusal: Some(REFUSAL.into()),
            ..status()
        },
        vec![plain_vent()],
    );
    assert_eq!(stat(&world, "sharing"), "Refused · 12 waiting");
    assert_eq!(named(&world, "settings.analytics.refusal"), REFUSAL);

    press(&mut world, "m");
    assert_eq!(
        named(&world, "settings.analytics.machine.share.copy"),
        REFUSAL
    );
}

#[test]
fn the_share_row_says_where_uploads_go() {
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        status(),
        vec![plain_vent()],
    );
    press(&mut world, "m");
    assert!(
        named(&world, "settings.analytics.machine.share.copy")
            .ends_with(&format!("12 waiting · uploads go to {INGEST}"))
    );
}

#[test]
fn an_instance_that_is_not_production_says_why_it_uploads_nowhere_before_and_after_sharing_is_on() {
    let reason = "Uploads go to http://127.0.0.1:1, where nothing listens, because this is instance dev, not production. Start its daemon with ATTN_ANALYTICS_INGEST_URL=<url> to upload to an attn-ingest.";
    let off = GetAnalyticsStatusResultMessage {
        ingest_url: "http://127.0.0.1:1".into(),
        ingest_off_reason: Some(reason.into()),
        ..status()
    };
    let mut before = sharing(&[], off.clone(), vec![plain_vent()]);
    press(&mut before, "m");
    assert_eq!(
        named(&before, "settings.analytics.machine.share.copy"),
        reason
    );

    let mut after = sharing(
        &[("analytics.sharing_enabled", "true")],
        GetAnalyticsStatusResultMessage {
            last_refusal: Some("dial tcp 127.0.0.1:1: connect: connection refused".into()),
            ..off
        },
        vec![plain_vent()],
    );
    assert_eq!(named(&after, "settings.analytics.refusal"), reason);
    assert!(!reads_as(&after, "settings.analytics").contains("connection refused"));
    press(&mut after, "m");
    assert_eq!(
        named(&after, "settings.analytics.machine.share.copy"),
        reason
    );
}

#[test]
fn turning_sharing_off_offers_to_delete_the_server_copy() {
    let mut world = sharing(
        &[
            ("analytics.sharing_enabled", "true"),
            ("analytics.share_vents", "true"),
        ],
        status(),
        vec![plain_vent()],
    );
    press(&mut world, "m");
    assert!(machine(&world).contains("Last sent"));
    assert!(machine(&world).contains("New attn vents are sent with their text."));

    click(&mut world, "settings.analytics.switch.sharing");
    assert_eq!(
        writes(&world),
        [("analytics.sharing_enabled".to_owned(), "false".to_owned())]
    );
    assert_eq!(
        named(&world, "settings.analytics.machine.share.copy"),
        "Sharing is off. Delete what the server already holds?"
    );
    click(&mut world, "settings.analytics.confirm.yes");

    assert_eq!(world.daemon.sent_of("forget_analytics_install").len(), 1);
    assert!(said(&world).starts_with("Forgot this install."));
}

#[test]
fn forgetting_this_install_waits_for_a_confirmation() {
    let mut world = sharing(
        &[("analytics.sharing_enabled", "true")],
        status(),
        vec![plain_vent()],
    );
    press(&mut world, "m");
    click(&mut world, "settings.analytics.forget");
    assert!(world.daemon.sent_of("forget_analytics_install").is_empty());

    click(&mut world, "settings.analytics.confirm.yes");
    assert_eq!(world.daemon.sent_of("forget_analytics_install").len(), 1);
}

#[test]
fn new_vents_list_before_done_ones_and_the_selected_vent_shows_its_context() {
    let world = triage();

    let listed = rows(&world);
    assert!(
        listed[0].starts_with("New: fix garden bell staleness"),
        "{listed:?}"
    );
    assert!(
        listed[1].starts_with("Done: songcheck deploy") && listed[1].contains("withdrawn"),
        "{listed:?}"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.seed"),
        "garden-show-stale-bell"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.build"),
        "Source a3f9e1c, uncommitted changes"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.experimental"),
        "Asks"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.agent"),
        "Claude 2.4.1 · claude-opus-5-5 (requested opus)"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.destination"),
        "Kept on this machine"
    );
}

#[test]
fn d_dismisses_u_undoes_and_the_withdrawn_vent_cannot_be_dismissed() {
    let mut world = triage();

    press(&mut world, "d");
    assert_eq!(said(&world), "Dismissed “fix garden bell staleness”.");
    assert_eq!(
        named(&world, "settings.analytics.vent.outcome"),
        "Dismissed"
    );
    press(&mut world, "u");
    press(&mut world, "j");
    press(&mut world, "d");

    let asked: Vec<(String, Value)> = world
        .daemon
        .sent_of("set_vent_dismissed")
        .iter()
        .map(|r| (vent_id(r), r["dismissed"].clone()))
        .collect();
    assert_eq!(
        asked,
        [
            ("v-new".to_owned(), json!(true)),
            ("v-new".to_owned(), json!(false))
        ]
    );
    assert!(!shows(&world, "settings.analytics.vent.action.d"));
}

#[test]
fn dismissing_a_vent_moves_on_to_the_next_new_one() {
    let mut world = triage_of(vec![newer(), vent("v-new"), withdrawn()]);
    press(&mut world, "j");
    press(&mut world, "k");

    press(&mut world, "d");

    let asked: Vec<String> = world
        .daemon
        .sent_of("set_vent_dismissed")
        .iter()
        .map(vent_id)
        .collect();
    assert_eq!(asked, ["v-newer"]);
    assert!(
        named(&world, "settings.analytics.vent.text")
            .starts_with("`attn seed tend s-f68sja` refused")
    );
}

#[test]
fn p_plants_a_seed_from_the_vent_shows_it_done_with_its_seed_and_u_undoes() {
    let mut world = triage();

    press(&mut world, "p");
    let planted: Vec<String> = world
        .daemon
        .sent_of("plant_vent_seed")
        .iter()
        .map(vent_id)
        .collect();
    assert_eq!(planted, ["v-new"]);
    assert_eq!(
        said(&world),
        "Planted seed-tend-refused-held-session from “fix garden bell staleness”."
    );
    let listed = rows(&world);
    assert!(
        listed
            .iter()
            .any(|r| r.starts_with("Done: fix garden bell staleness")
                && r.contains("seed-tend-refused-held-session")),
        "{listed:?}"
    );
    assert_eq!(
        named(&world, "settings.analytics.vent.outcome"),
        "Planted · seed-tend-refused-held-session"
    );
    assert!(!shows(&world, "settings.analytics.vent.action.p"));

    press(&mut world, "u");
    let unplanted: Vec<String> = world
        .daemon
        .sent_of("unplant_vent_seed")
        .iter()
        .map(vent_id)
        .collect();
    assert_eq!(unplanted, ["v-new"]);
    assert_eq!(said(&world), "Undone.");
    assert!(rows(&world)[0].starts_with("New: fix garden bell staleness"));
}

#[test]
fn t_opens_the_transcript_and_g_the_stake_seed_closing_settings() {
    let mut world = triage();

    press(&mut world, "t");
    assert_eq!(world.opened_links(), ["file:///tmp/transcript.jsonl"]);
    assert_eq!(state(&mut world)["open"], true);

    press(&mut world, "g");
    assert_eq!(state(&mut world)["open"], false);
}

#[test]
fn a_vent_that_arrives_shows_at_once_without_reading_records() {
    let mut world = triage();
    world.daemon.emit(VentsUpdatedMessage {
        profile_id: PROFILE.into(),
        vents: vec![newer(), vent("v-new"), withdrawn()],
        ..VentsUpdatedMessage::new()
    });
    world.settle();

    assert!(rows(&world)[0].contains("make test ran the Go suite"));
    assert!(world.daemon.sent_of("list_analytics_records").is_empty());
}
