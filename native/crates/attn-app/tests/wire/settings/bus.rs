use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use attn_protocol::{
    BusConsumerStatus, BusHealthEntry, BusProducerStatus, BusSetConsumerEnabledResultMessage,
    BusStatusResultMessage,
};
use attn_testkit::{Daemon, Request, Target, World, message};
use serde_json::Value;

use super::{accepting, click, open};

fn producer(name: &str, events: i64, share: f64, surging: bool) -> BusProducerStatus {
    BusProducerStatus {
        name: name.into(),
        events,
        bytes: 17_900_000,
        subjects: 103,
        share,
        recent_per_hour: 1701.0,
        baseline_per_hour: 1816.0,
        sustained_per_hour: 680.0,
        surging,
        ..BusProducerStatus::default()
    }
}

fn consumer(name: &str) -> BusConsumerStatus {
    BusConsumerStatus {
        name: name.into(),
        cursor: 100,
        filter: "session.*".into(),
        enabled: true,
        updated_at: "2026-08-10T19:00:00Z".into(),
        live: true,
        ..BusConsumerStatus::default()
    }
}

fn status() -> BusStatusResultMessage {
    BusStatusResultMessage {
        success: true,
        earliest: 1,
        head: 315_188,
        rows: 315_188,
        bytes: 23_815_114,
        oldest_at: "2026-08-02T10:24:55Z".into(),
        newest_at: "2026-08-10T19:28:01Z".into(),
        delivering: true,
        retention_seconds: 30.0 * 86400.0,
        recent_window_seconds: 3600.0,
        baseline_window_seconds: 86400.0,
        surge_rate_per_hour: 1000.0,
        pin_alarm_seconds: 3600.0,
        producers: vec![producer("session.state.changed", 232_213, 0.737, true)],
        ..BusStatusResultMessage::new()
    }
}

fn answering(status: BusStatusResultMessage) -> impl Fn(&Request) -> Value + Send + 'static {
    move |r| {
        message(BusStatusResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ..status.clone()
        })
    }
}

fn on_bus(daemon: Daemon) -> World {
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.eventBus");
    world
}

fn bus(status: BusStatusResultMessage) -> World {
    let (daemon, _) = accepting(&[]);
    daemon.on("bus_status_get", answering(status));
    on_bus(daemon)
}

fn reads(world: &World) -> usize {
    world.daemon.sent_of("bus_status_get").len()
}

fn label(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .name
        .unwrap_or_default()
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

#[test]
fn the_bus_is_read_once_on_arrival_then_again_only_every_thirty_seconds() {
    let mut world = bus(status());
    assert_eq!(reads(&world), 1);

    world.advance(Duration::from_secs(29));
    assert_eq!(reads(&world), 1);
    world.advance(Duration::from_secs(1));
    assert_eq!(reads(&world), 2);

    click(&mut world, "settings.nav.general");
    world.advance(Duration::from_secs(60));
    assert_eq!(reads(&world), 2);
}

#[test]
fn each_producer_shows_its_share_subjects_and_rates_and_the_quiet_tail_folds() {
    let mut world = bus(BusStatusResultMessage {
        producers: vec![
            producer("session.state.changed", 232_213, 0.737, true),
            producer("pr.updated", 53_182, 0.169, false),
            producer("ticket.created", 4, 0.002, false),
            producer("plugin.installed", 1, 0.001, false),
        ],
        ..status()
    });

    let loud = label(&world, "settings.bus.producer.session.state.changed");
    for part in ["232,213", "73.7%", "103", "Loud"] {
        assert!(loud.contains(part), "{part} missing from {loud}");
    }
    assert!(!label(&world, "settings.bus.producer.pr.updated").contains("Loud"));
    assert!(!shows(&world, "settings.bus.producer.ticket.created"));
    let toggle = label(&world, "settings.bus.toggle-quiet");
    for part in ["2 quieter classes", "5 events", "0.3% of the log"] {
        assert!(toggle.contains(part), "{part} missing from {toggle}");
    }

    click(&mut world, "settings.bus.toggle-quiet");
    assert!(shows(&world, "settings.bus.producer.ticket.created"));
}

#[test]
fn the_daemons_health_findings_show_verbatim() {
    let world = bus(BusStatusResultMessage {
        health: vec![
            BusHealthEntry {
                level: "error".into(),
                kind: "consumer_lagging".into(),
                subject: "notifier".into(),
                message: "consumer notifier is 41,000 events behind and not advancing; its cursor has not moved for 2h".into(),
            },
            BusHealthEntry {
                level: "warn".into(),
                kind: "producer_surging".into(),
                subject: "session.state.changed".into(),
                message: "producer session.state.changed is publishing 1816 events/hour sustained over the last 24h, past the 1000/hour tripwire".into(),
            },
        ],
        ..status()
    });

    let findings: Vec<String> = world
        .screen()
        .children(Target::key("settings.bus.health"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(findings.len(), 2);
    assert!(findings[0].starts_with("Error: "));
    assert!(findings[0].contains("41,000 events behind and not advancing"));
    assert!(findings[1].starts_with("Warning: "));
    assert!(findings[1].contains("past the 1000/hour tripwire"));
}

#[test]
fn no_durable_consumers_reads_as_a_sentence_not_an_empty_table() {
    let world = bus(status());
    assert!(
        label(&world, "settings.bus.no-consumers")
            .starts_with("No durable consumers are registered")
    );
    assert!(!shows(&world, "settings.bus.consumers"));
}

#[test]
fn a_disabled_a_pinning_an_absent_and_a_stalled_consumer_are_each_marked() {
    let week_before_the_test_clock = "2026-11-05T09:00:00Z";
    let world = bus(BusStatusResultMessage {
        consumers: vec![
            BusConsumerStatus {
                enabled: false,
                cursor: 12,
                lag: 315_176,
                ..consumer("killed")
            },
            BusConsumerStatus {
                holds_retention_floor: true,
                lag: 41_000,
                oldest_unread_at: week_before_the_test_clock.into(),
                ..consumer("pinner")
            },
            BusConsumerStatus {
                live: false,
                ..consumer("absent")
            },
            BusConsumerStatus {
                stalled: "handler blew up".into(),
                ..consumer("failing")
            },
        ],
        ..status()
    });

    assert!(label(&world, "settings.bus.consumer.killed").contains("Disabled"));
    let pinner = label(&world, "settings.bus.consumer.pinner");
    assert!(pinner.contains("Retention floor"));
    assert!(pinner.contains("41,000"));
    assert!(
        pinner.ends_with(", 7d"),
        "the oldest unread waits 7d: {pinner}"
    );
    assert!(label(&world, "settings.bus.consumer.absent").contains("Not running"));
    assert!(label(&world, "settings.bus.consumer.failing").contains("Stalled"));
}

#[test]
fn a_retention_floor_reads_apart_from_pinning_past_the_tripwire_which_names_it_exactly() {
    let world = bus(BusStatusResultMessage {
        pin_alarm_seconds: 90.0,
        consumers: vec![
            BusConsumerStatus {
                holds_retention_floor: true,
                lag: 12,
                ..consumer("ordinary")
            },
            BusConsumerStatus {
                holds_retention_floor: true,
                pin_alarm: true,
                pinned_bytes: 31_000,
                lag: 41_000,
                ..consumer("ticketwatch")
            },
        ],
        ..status()
    });

    let ordinary = label(&world, "settings.bus.consumer.ordinary");
    assert!(ordinary.contains("Retention floor") && !ordinary.contains("Pinning"));
    let alarming = label(&world, "settings.bus.consumer.ticketwatch");
    assert!(alarming.contains("Pinning 30.3 KB") && !alarming.contains("Retention floor"));
    let pin = world
        .screen()
        .descendants(Target::key("settings.bus.consumer.ticketwatch"))
        .into_iter()
        .find(|n| n.name.as_deref() == Some("Pinning 30.3 KB"))
        .expect("the pinning mark");
    assert!(
        pin.description
            .as_deref()
            .is_some_and(|d| d.contains("longer than 1m30s")),
        "{pin:?}"
    );
}

#[test]
fn nothing_is_claimed_about_delivery_the_daemon_cannot_observe() {
    let mut absent = consumer("absent");
    absent.live = false;
    let world = bus(BusStatusResultMessage {
        delivering: false,
        consumers: vec![absent],
        ..status()
    });

    assert!(!label(&world, "settings.bus.consumer.absent").contains("Not running"));
    let delivery = label(&world, "settings.bus.delivery");
    assert!(delivery.starts_with("Read from the daemon"));
    assert!(!delivery.contains("from the database"));
}

fn switching(answer: fn(&str) -> BusSetConsumerEnabledResultMessage) -> World {
    let (daemon, _) = accepting(&[]);
    daemon.on(
        "bus_status_get",
        answering(BusStatusResultMessage {
            consumers: vec![consumer("notifier")],
            ..status()
        }),
    );
    daemon.on("bus_set_consumer_enabled", move |r: &Request| {
        message(BusSetConsumerEnabledResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ..answer(r["consumer"].as_str().unwrap())
        })
    });
    on_bus(daemon)
}

#[test]
fn a_consumer_switched_off_is_sent_and_the_bus_is_read_again() {
    let mut world = switching(|name| BusSetConsumerEnabledResultMessage {
        success: true,
        consumer: name.into(),
        ..BusSetConsumerEnabledResultMessage::new()
    });

    click(&mut world, "settings.bus.consumer.notifier.switch");

    let sent: Vec<(String, bool)> = world
        .daemon
        .sent_of("bus_set_consumer_enabled")
        .iter()
        .map(|r| {
            (
                r["consumer"].as_str().unwrap().to_owned(),
                r["enabled"].as_bool().unwrap(),
            )
        })
        .collect();
    assert_eq!(sent, [("notifier".to_owned(), false)]);
    assert_eq!(reads(&world), 2);
}

#[test]
fn a_refused_switch_says_why() {
    let mut world = switching(|name| BusSetConsumerEnabledResultMessage {
        success: false,
        consumer: name.into(),
        error: Some("no consumer named notifier".into()),
        ..BusSetConsumerEnabledResultMessage::new()
    });

    click(&mut world, "settings.bus.consumer.notifier.switch");

    assert_eq!(
        label(&world, "settings.bus.error"),
        "no consumer named notifier"
    );
    assert!(shows(&world, "settings.bus.consumers"));
}

#[test]
fn a_bus_that_cannot_be_read_offers_try_again() {
    let readable = Arc::new(AtomicBool::new(false));
    let (daemon, _) = accepting(&[]);
    let now = readable.clone();
    daemon.on("bus_status_get", move |r: &Request| {
        let request_id = r["request_id"].as_str().unwrap().to_owned();
        message(if now.load(Ordering::SeqCst) {
            BusStatusResultMessage {
                request_id,
                ..status()
            }
        } else {
            BusStatusResultMessage {
                request_id,
                success: false,
                error: Some("disk gone".into()),
                ..BusStatusResultMessage::new()
            }
        })
    });
    let mut world = on_bus(daemon);
    assert_eq!(label(&world, "settings.bus.error"), "disk gone");

    readable.store(true, Ordering::SeqCst);
    click(&mut world, "settings.bus.try-again");

    assert!(shows(&world, "settings.bus.producers"));
}
