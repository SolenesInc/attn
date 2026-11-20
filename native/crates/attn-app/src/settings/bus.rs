//! Settings → Event bus: the daemon's event log, its producers and durable consumers, read when
//! the page shows and every thirty seconds while it does, with a switch for each consumer.

use attn_protocol::{
    BusConsumerStatus, BusProducerStatus, BusSetConsumerEnabledMessage, BusStatusGetMessage,
    BusStatusResultMessage,
};
use attn_ui::Tokens;
use rune::{El, Tx};

use attn_ui::blocks::{
    Actions, Block, Facts, Inline, Notice, Prose, Section, Stack, Status, Table, TableColumns,
    TableRow,
};
use attn_ui::system::{self, Look, Said, Size, Tone, Type, tag, words};

use crate::State;

/// The daemon answers with one pass over the whole log, 209 ms at 945k rows, so the page reads
/// it seldom.
const REFRESH_SECS: f64 = 30.0;

/// A producer under this share of the log folds into the quiet tail: 35 of 50 classes in
/// production hold 0.3% between them.
const QUIET_SHARE: f64 = 0.005;

/// The consumer table's last column, wide enough for Disable and Enable.
const ACTION_W: f32 = 80.0;

#[derive(Default)]
pub(crate) struct Bus {
    status: Option<BusStatusResultMessage>,
    error: Option<String>,
    loading: bool,
    /// Rises with each read, so only the newest read's answer lands.
    epoch: u64,
    /// When the page reads the bus again, on the window's clock.
    next_read: f64,
    show_quiet: bool,
    /// The consumer whose switch waits on the daemon; every switch waits with it.
    switching: Option<String>,
}

fn shown(state: &State) -> bool {
    super::is_open(state) && state.window.settings.section == super::Section::EventBus
}

/// Reads the bus when the page has just come into view.
pub(super) fn arrive(state: &mut State, tx: &mut Tx) {
    if shown(state) {
        read(state, tx);
    }
}

/// Reads the bus again once its refresh comes due while the page shows.
pub(super) fn poll(state: &mut State, now: f64, tx: &mut Tx) {
    if shown(state) && now >= state.window.settings.bus.next_read {
        read(state, tx);
    }
}

fn read(state: &mut State, tx: &mut Tx) {
    let bus = &mut state.window.settings.bus;
    bus.epoch += 1;
    bus.loading = true;
    bus.next_read = tx.clock() + REFRESH_SECS;
    tx.wake_at(bus.next_read);
    let epoch = bus.epoch;
    state.ask(
        |request_id| BusStatusGetMessage {
            request_id,
            ..BusStatusGetMessage::new()
        },
        move |state, answer, _tx: &mut Tx| {
            let bus = &mut state.window.settings.bus;
            if bus.epoch != epoch {
                return;
            }
            bus.loading = false;
            match answer
                .outcome()
                .map(|reply| reply.parse::<BusStatusResultMessage>())
            {
                Ok(Some(status)) => {
                    bus.status = Some(status);
                    bus.error = None;
                }
                Ok(None) => bus.error = Some("Could not read the event bus".into()),
                Err(why) => bus.error = Some(why),
            }
        },
        tx,
    );
}

fn switch(state: &mut State, consumer: String, enabled: bool, tx: &mut Tx) {
    state.window.settings.bus.switching = Some(consumer.clone());
    state.ask(
        |request_id| BusSetConsumerEnabledMessage {
            request_id,
            consumer,
            enabled,
            ..BusSetConsumerEnabledMessage::new()
        },
        |state, answer, tx: &mut Tx| {
            state.window.settings.bus.switching = None;
            match answer.outcome() {
                Ok(_) if shown(state) => read(state, tx),
                Ok(_) => {}
                Err(why) => state.window.settings.bus.error = Some(why),
            }
        },
        tx,
    );
}

pub(super) fn page(state: &State, now_ms: i64, t: &Tokens) -> Vec<Block> {
    let bus = &state.window.settings.bus;
    let Some(status) = &bus.status else {
        return vec![match &bus.error {
            Some(error) => Notice::warn("settings.bus.error", error.clone())
                .action(
                    system::Button::new("Try again")
                        .on_press_tx(read)
                        .build(t)
                        .key("settings.bus.try-again"),
                )
                .build(t),
            None => Status::new("settings.bus.loading", "Reading the event log…", true).build(t),
        }];
    };
    let mut page: Vec<Block> = Vec::new();
    if let Some(error) = &bus.error {
        page.push(Notice::warn("settings.bus.error", error.clone()).build(t));
    }
    if !status.health.is_empty() {
        page.push(Block::custom(
            "settings.bus.health.block",
            Stack::tight()
                .key("settings.bus.health")
                .role("list")
                .label("Event bus health")
                .children(status.health.iter().enumerate().map(|(i, finding)| {
                    let error = finding.level == "error";
                    let level = if error { "Error" } else { "Warning" };
                    Inline::loose()
                        .role("listitem")
                        .label(format!("{level}: {}", finding.message))
                        .child(
                            El::from(tag(
                                format!("settings.bus.health.{i}.level"),
                                level,
                                if error { Tone::Bad } else { Tone::Warn },
                                t,
                            ))
                            .role("status"),
                        )
                        .child(
                            words(
                                finding.message.clone(),
                                Type::Small,
                                if error { t.error } else { t.warning },
                            )
                            .wrap()
                            .shrink(),
                        )
                })),
        ));
    }
    page.push(summary(status, now_ms, t));
    page.push(producers(bus, status, t));
    page.push(consumers(bus, status, now_ms, t));
    page.push(
        Actions::new("settings.bus.actions")
            .push(
                system::Button::new(if bus.loading { "Reading…" } else { "Refresh" })
                    .disabled(bus.loading)
                    .on_press_tx(read)
                    .build(t)
                    .key("settings.bus.refresh")
                    .label("Refresh"),
            )
            .build(),
    );
    page.push(
        Prose::hint(
            "settings.bus.delivery",
            if status.delivering {
                "Read from the daemon that owns delivery, so a consumer that is registered but not running is visible."
            } else {
                "Read from the daemon, which is running no durable delivery loops right now, so there is nothing to report as running or stalled."
            },
        )
        .build(t),
    );
    page
}

fn summary(status: &BusStatusResultMessage, now_ms: i64, t: &Tokens) -> Block {
    let stats = [
        ("events", "Events", count(status.rows)),
        ("weight", "Weight", bytes(status.bytes)),
        ("seq", "Seq", format!("{}–{}", status.earliest, status.head)),
        (
            "oldest",
            "Oldest",
            age(&status.oldest_at, now_ms).unwrap_or_else(|| "—".into()),
        ),
        (
            "retention",
            "Retention",
            span(status.retention_seconds).unwrap_or_default(),
        ),
    ];
    stats
        .into_iter()
        .fold(
            Facts::new("settings.bus.summary").tiles(),
            |facts, (key, label, value)| {
                facts.code(format!("settings.bus.summary.{key}"), label, value)
            },
        )
        .build(t)
}

/// The width of a number column: the widest count a producer reaches, "1,048,000,000", 13
/// characters of Mono at about 7.1 points each in the tour, 92 points, and room to spare.
const NUMBER_W: f32 = 96.0;

fn producers(bus: &Bus, status: &BusStatusResultMessage, t: &Tokens) -> Block {
    let recent = span(status.recent_window_seconds).unwrap_or_default();
    let baseline = span(status.baseline_window_seconds).unwrap_or_default();
    let section = Section::new("settings.bus.producers.part", "Producers").description(format!(
        "Rates are events per hour over the last {recent} and {baseline}. Far fewer subjects than events means a producer republishing the same entity rather than describing change."
    ));
    let (loud, quiet): (Vec<&BusProducerStatus>, Vec<&BusProducerStatus>) = status
        .producers
        .iter()
        .partition(|p| p.share >= QUIET_SHARE);
    let shown: Vec<&BusProducerStatus> = if bus.show_quiet {
        status.producers.iter().collect()
    } else {
        loud
    };
    let section = if shown.is_empty() {
        section.push(
            Prose::empty(
                "settings.bus.no-producers",
                "Nothing has been published yet.",
            )
            .build(t),
        )
    } else {
        let mut columns = TableColumns::new("Fact").identifiers();
        let events = columns.fixed_end("Events", NUMBER_W);
        let share = columns.fixed_end("Share", NUMBER_W);
        let subjects = columns.fixed_end("Subjects", NUMBER_W);
        let now = columns.fixed_end(format!("{recent}/h"), NUMBER_W);
        let usual = columns.fixed_end(format!("{baseline}/h"), NUMBER_W);
        let rows = shown.into_iter().map(|p| {
            let row = TableRow::<State>::still(
                format!("settings.bus.producer.{}", p.name),
                p.name.clone(),
            );
            let row = if p.surging {
                row.tag_keyed(
                    format!("settings.bus.producer.{}.loud", p.name),
                    "Loud",
                    Tone::Warn,
                    None,
                )
            } else {
                row
            };
            row.cell(events, count(p.events))
                .cell(share, format!("{:.1}%", p.share * 100.0))
                .cell(subjects, count(p.subjects))
                .cell(now, rate(p.recent_per_hour))
                .cell(usual, rate(p.baseline_per_hour))
        });
        section.push(
            Table::new("settings.bus.producers", columns)
                .compact()
                .named("Producers")
                .rows(rows)
                .build(t),
        )
    };
    let toggle = (!quiet.is_empty()).then(|| {
        let events: i64 = quiet.iter().map(|p| p.events).sum();
        let share: f64 = quiet.iter().map(|p| p.share).sum();
        let n = quiet.len();
        let label = if bus.show_quiet {
            "Hide the quieter classes".to_owned()
        } else {
            format!(
                "Show {n} quieter class{} — {} events, {:.1}% of the log",
                if n == 1 { "" } else { "es" },
                count(events),
                share * 100.0
            )
        };
        Actions::new("settings.bus.quiet")
            .push(
                system::Button::new(label)
                    .look(Look::Quiet)
                    .on_press(|state: &mut State| {
                        let bus = &mut state.window.settings.bus;
                        bus.show_quiet = !bus.show_quiet;
                    })
                    .build(t)
                    .key("settings.bus.toggle-quiet"),
            )
            .build()
    });
    section.push_all(toggle).build(t)
}

fn consumers(bus: &Bus, status: &BusStatusResultMessage, now_ms: i64, t: &Tokens) -> Block {
    let section = Section::new("settings.bus.consumers.part", "Consumers").description(
        "Disabling stops delivery and releases retention. Enabling resumes from the saved cursor if it remains in the log.",
    );
    if status.consumers.is_empty() {
        return section
            .push(
                Prose::empty(
                    "settings.bus.no-consumers",
                    "No durable consumers are registered. Every subscriber on this bus is ephemeral, so nothing holds a cursor and nothing pins retention.",
                )
                .build(t),
            )
            .build(t);
    }
    let mut columns = TableColumns::new("Consumer").identifiers();
    let cursor = columns.fixed_end("Cursor", NUMBER_W);
    let lag = columns.fixed_end("Lag", NUMBER_W);
    let waiting = columns.fixed_end("Waiting", NUMBER_W);
    let action = columns.fixed("", ACTION_W);
    let rows = status.consumers.iter().map(|c| {
        let row =
            TableRow::<State>::still(format!("settings.bus.consumer.{}", c.name), c.name.clone())
                .subtitle(if c.filter.is_empty() {
                    Said::from("all facts")
                } else {
                    Said::from(c.filter.clone())
                });
        let row =
            marks(c, status)
                .into_iter()
                .enumerate()
                .fold(row, |row, (i, (mark, tone, help))| {
                    row.tag_keyed(
                        format!("settings.bus.consumer.{}.mark.{i}", c.name),
                        mark,
                        tone,
                        help,
                    )
                });
        let consumer = c.name.clone();
        let enabled = c.enabled;
        let switch = system::Button::new(if enabled { "Disable" } else { "Enable" })
            .look(if enabled { Look::Danger } else { Look::Plain })
            .size(Size::Compact)
            .disabled(bus.switching.is_some())
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                switch(state, consumer.clone(), !enabled, tx)
            })
            .build(t)
            .key(format!("settings.bus.consumer.{}.switch", c.name));
        row.cell(cursor, count(c.cursor))
            .cell(lag, count(c.lag))
            .cell(
                waiting,
                age(&c.oldest_unread_at, now_ms).unwrap_or_else(|| "—".into()),
            )
            .control(action, switch)
            .off(!c.enabled)
    });
    section
        .push(
            Table::new("settings.bus.consumers", columns)
                .compact()
                .named("Consumers")
                .rows(rows)
                .build(t),
        )
        .build(t)
}

/// The marks a consumer row carries, each with the tooltip that explains it.
fn marks(
    c: &BusConsumerStatus,
    status: &BusStatusResultMessage,
) -> Vec<(String, Tone, Option<String>)> {
    let mut marks = Vec::new();
    if c.holds_retention_floor && !c.pin_alarm {
        marks.push((
            "Retention floor".to_owned(),
            Tone::Neutral,
            Some("Retention stops at this consumer's cursor".to_owned()),
        ));
    }
    if c.pin_alarm {
        marks.push((
            format!("Pinning {}", bytes(c.pinned_bytes)),
            Tone::Bad,
            Some(format!(
                "Nothing below this consumer's cursor can be trimmed, and it has held that for longer than {}",
                limit(status.pin_alarm_seconds).unwrap_or_default()
            )),
        ));
    }
    if !c.enabled {
        marks.push(("Disabled".to_owned(), Tone::Warn, None));
    }
    if !c.stalled.is_empty() {
        marks.push(("Stalled".to_owned(), Tone::Bad, Some(c.stalled.clone())));
    }
    if status.delivering && c.enabled && !c.live {
        marks.push(("Not running".to_owned(), Tone::Bad, None));
    }
    marks
}

pub(super) fn count(n: i64) -> String {
    let digits = n.unsigned_abs().to_string();
    let mut grouped = String::new();
    for (i, d) in digits.chars().enumerate() {
        if i > 0 && (digits.len() - i).is_multiple_of(3) {
            grouped.push(',');
        }
        grouped.push(d);
    }
    if n < 0 {
        format!("-{grouped}")
    } else {
        grouped
    }
}

pub(super) fn bytes(n: i64) -> String {
    const KB: i64 = 1 << 10;
    const MB: i64 = 1 << 20;
    if n >= MB {
        format!("{:.1} MB", n as f64 / MB as f64)
    } else if n >= KB {
        format!("{:.1} KB", n as f64 / KB as f64)
    } else {
        format!("{n} B")
    }
}

fn rate(n: f64) -> String {
    if n >= 10.0 {
        format!("{n:.0}")
    } else {
        format!("{n:.1}")
    }
}

/// An observed span, rounded to its largest unit: "7d", "3h", "12m", "40s".
fn span(seconds: f64) -> Option<String> {
    if !seconds.is_finite() || seconds <= 0.0 {
        return None;
    }
    Some(if seconds >= 48.0 * 3600.0 {
        format!("{}d", (seconds / 86400.0).round())
    } else if seconds >= 3600.0 {
        format!("{}h", (seconds / 3600.0).round())
    } else if seconds >= 60.0 {
        format!("{}m", (seconds / 60.0).round())
    } else {
        format!("{}s", seconds.round())
    })
}

/// A configured limit, exactly: "1h", "1m30s", "45s", as the daemon's `limitDuration` says it.
fn limit(seconds: f64) -> Option<String> {
    if !seconds.is_finite() || seconds <= 0.0 {
        return None;
    }
    let whole = seconds.round() as i64;
    let (h, m, s) = (whole / 3600, (whole % 3600) / 60, whole % 60);
    Some(match (h, m, s) {
        (0, 0, s) => format!("{s}s"),
        (0, m, 0) => format!("{m}m"),
        (0, m, s) => format!("{m}m{s}s"),
        (h, 0, 0) => format!("{h}h"),
        (h, m, 0) => format!("{h}h{m}m"),
        (h, m, s) => format!("{h}h{m}m{s}s"),
    })
}

fn age(at: &str, now_ms: i64) -> Option<String> {
    let at_ms = crate::sidebar::queue::epoch_ms(at)?;
    span((now_ms - at_ms) as f64 / 1000.0)
}
