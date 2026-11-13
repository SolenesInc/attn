//! Settings → Event bus: the daemon's event log, its producers and durable consumers, read when
//! the page shows and every thirty seconds while it does, with a switch for each consumer.

use attn_protocol::{
    BusConsumerStatus, BusProducerStatus, BusSetConsumerEnabledMessage, BusStatusGetMessage,
    BusStatusResultMessage,
};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{Align, El, Tx, col, row, text};
use rune_kit::{Badge, Button, ButtonVariant, Kit};

use super::Section;
use super::pages::CAPTION;
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
    super::is_open(state) && state.window.settings.section == Section::EventBus
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

pub(super) fn page(state: &State, now_ms: i64, t: &Tokens) -> Vec<El> {
    let k = attn_ui::kit(t);
    let bus = &state.window.settings.bus;
    let Some(status) = &bus.status else {
        return vec![match &bus.error {
            Some(error) => col()
                .key("settings.bus.error")
                .role("alert")
                .label(error.clone())
                .gap(space::S2)
                .child(text(error.clone()).font(CAPTION).color(t.warning))
                .child(
                    Button::new("Try again")
                        .on_press_tx(read)
                        .build(k)
                        .key("settings.bus.try-again"),
                ),
            None => text("Reading the event log…")
                .key("settings.bus.loading")
                .font(CAPTION)
                .color(t.text_tertiary),
        }];
    };
    let mut page: Vec<El> = Vec::new();
    if let Some(error) = &bus.error {
        page.push(
            text(error.clone())
                .key("settings.bus.error")
                .role("alert")
                .label(error.clone())
                .font(CAPTION)
                .color(t.warning)
                .wrap(),
        );
    }
    if !status.health.is_empty() {
        page.push(
            col()
                .key("settings.bus.health")
                .role("list")
                .label("Event bus health")
                .gap(space::S1)
                .children(status.health.iter().map(|finding| {
                    let error = finding.level == "error";
                    let level = if error { "Error" } else { "Warning" };
                    row()
                        .role("listitem")
                        .label(format!("{level}: {}", finding.message))
                        .gap(space::S2)
                        .items_center()
                        .child(Badge::new(level).build(k))
                        .child(
                            text(finding.message.clone())
                                .font(CAPTION)
                                .color(if error { t.error } else { t.warning })
                                .wrap()
                                .grow(1.0)
                                .min_w(0.0),
                        )
                })),
        );
    }
    page.push(summary(status, now_ms, t));
    page.extend(producers(bus, status, t, k));
    page.extend(consumers(bus, status, now_ms, t, k));
    page.push(
        row()
            .key("settings.bus.footer")
            .gap(space::S3)
            .items_center()
            .child(
                Button::new(if bus.loading { "Reading…" } else { "Refresh" })
                    .disabled(bus.loading)
                    .on_press_tx(read)
                    .build(k)
                    .key("settings.bus.refresh")
                    .label("Refresh"),
            )
            .child(
                text(if status.delivering {
                    "Read from the daemon that owns delivery, so a consumer that is registered but not running is visible."
                } else {
                    "Read from the daemon, which is running no durable delivery loops right now, so there is nothing to report as running or stalled."
                })
                .key("settings.bus.delivery")
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap()
                .grow(1.0)
                .min_w(0.0),
            ),
    );
    page
}

fn summary(status: &BusStatusResultMessage, now_ms: i64, t: &Tokens) -> El {
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
    row()
        .key("settings.bus.summary")
        .gap(20.0)
        .children(stats.into_iter().map(|(key, label, value)| {
            col()
                .gap(2.0)
                .child(text(label).font(CAPTION).color(t.text_tertiary))
                .child(
                    text(value.clone())
                        .key(format!("settings.bus.summary.{key}"))
                        .label(format!("{label} {value}"))
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
        }))
}

fn section_head(title: &str, hint: String, t: &Tokens) -> El {
    col()
        .gap(space::S1)
        .pt(space::S3)
        .child(
            text(title.to_owned())
                .font(type_::SECTION)
                .color(t.text_primary),
        )
        .child(text(hint).font(CAPTION).color(t.text_tertiary).wrap())
}

/// One table row: the name cell takes what is left, the number cells line up at the end.
fn table_row(key: String, label: String, name: El, numbers: Vec<String>, t: &Tokens) -> El {
    row()
        .key(key)
        .role("row")
        .label(label)
        .w_full()
        .gap(space::S2)
        .py(space::S1)
        .items_center()
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(name.grow(1.0).min_w(0.0))
        .children(numbers.into_iter().map(|n| {
            text(n)
                .font(type_::MONO_CAPTION)
                .color(t.text_secondary)
                .w(72.0)
                .align(Align::End)
        }))
}

fn producers(bus: &Bus, status: &BusStatusResultMessage, t: &Tokens, k: &Kit) -> Vec<El> {
    let recent = span(status.recent_window_seconds).unwrap_or_default();
    let baseline = span(status.baseline_window_seconds).unwrap_or_default();
    let head = section_head(
        "Producers",
        format!(
            "Rates are events per hour over the last {recent} and {baseline}. Far fewer subjects than events means a producer republishing the same entity rather than describing change."
        ),
        t,
    );
    let (loud, quiet): (Vec<&BusProducerStatus>, Vec<&BusProducerStatus>) = status
        .producers
        .iter()
        .partition(|p| p.share >= QUIET_SHARE);
    let shown: Vec<&BusProducerStatus> = if bus.show_quiet {
        status.producers.iter().collect()
    } else {
        loud
    };
    let table = if shown.is_empty() {
        text("Nothing has been published yet.")
            .key("settings.bus.no-producers")
            .font(CAPTION)
            .color(t.text_tertiary)
    } else {
        let header = table_row(
            "settings.bus.producers.head".into(),
            "Columns".into(),
            text("Fact").font(CAPTION).color(t.text_tertiary),
            vec![
                "Events".into(),
                "Share".into(),
                "Subjects".into(),
                format!("{recent}/h"),
                format!("{baseline}/h"),
            ],
            t,
        );
        col()
            .key("settings.bus.producers")
            .role("table")
            .label("Producers")
            .w_full()
            .child(header)
            .children(shown.into_iter().map(|p| {
                let name = row()
                    .gap(space::S2)
                    .items_center()
                    .child(
                        text(p.name.clone())
                            .font(type_::MONO_CAPTION)
                            .color(t.text_primary)
                            .truncate(),
                    )
                    .children(p.surging.then(|| Badge::new("Loud").build(k)));
                let cells = vec![
                    count(p.events),
                    format!("{:.1}%", p.share * 100.0),
                    count(p.subjects),
                    rate(p.recent_per_hour),
                    rate(p.baseline_per_hour),
                ];
                let mut label = vec![p.name.clone()];
                if p.surging {
                    label.push("Loud".into());
                }
                label.extend(cells.iter().cloned());
                table_row(
                    format!("settings.bus.producer.{}", p.name),
                    label.join(", "),
                    name,
                    cells,
                    t,
                )
            }))
    };
    let mut out = vec![head, table];
    if !quiet.is_empty() {
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
        out.push(
            row().child(
                Button::new(label)
                    .variant(ButtonVariant::Ghost)
                    .on_press(|state: &mut State| {
                        let bus = &mut state.window.settings.bus;
                        bus.show_quiet = !bus.show_quiet;
                    })
                    .build(k)
                    .key("settings.bus.toggle-quiet"),
            ),
        );
    }
    out
}

fn consumers(
    bus: &Bus,
    status: &BusStatusResultMessage,
    now_ms: i64,
    t: &Tokens,
    k: &Kit,
) -> Vec<El> {
    let head = section_head(
        "Consumers",
        "Disabling stops delivery and releases retention. Enabling resumes from the saved cursor if it remains in the log.".into(),
        t,
    );
    if status.consumers.is_empty() {
        return vec![
            head,
            text("No durable consumers are registered. Every subscriber on this bus is ephemeral, so nothing holds a cursor and nothing pins retention.")
                .key("settings.bus.no-consumers")
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap(),
        ];
    }
    let header = table_row(
        "settings.bus.consumers.head".into(),
        "Columns".into(),
        text("Consumer").font(CAPTION).color(t.text_tertiary),
        vec!["Cursor".into(), "Lag".into(), "Waiting".into()],
        t,
    )
    .child(rune::el().w(ACTION_W));
    let rows = status.consumers.iter().map(|c| {
        let marks = marks(c, status);
        let name = row()
            .gap(space::S2)
            .items_center()
            .child(
                text(c.name.clone())
                    .font(type_::MONO_CAPTION)
                    .color(t.text_primary),
            )
            .children(marks.iter().map(|(mark, help)| {
                let badge = Badge::new(mark.clone()).build(k);
                match help {
                    Some(help) => badge.help(help.clone()),
                    None => badge,
                }
            }))
            .child(
                text(if c.filter.is_empty() {
                    "all facts".to_owned()
                } else {
                    c.filter.clone()
                })
                .font(CAPTION)
                .color(t.text_tertiary)
                .truncate(),
            );
        let cells = vec![
            count(c.cursor),
            count(c.lag),
            age(&c.oldest_unread_at, now_ms).unwrap_or_else(|| "—".into()),
        ];
        let mut label = vec![c.name.clone()];
        label.extend(marks.iter().map(|(mark, _)| mark.clone()));
        label.extend(cells.iter().cloned());
        let consumer = c.name.clone();
        let enabled = c.enabled;
        let action = Button::new(if enabled { "Disable" } else { "Enable" })
            .variant(if enabled {
                ButtonVariant::Danger
            } else {
                ButtonVariant::Neutral
            })
            .disabled(bus.switching.is_some())
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                switch(state, consumer.clone(), !enabled, tx)
            })
            .build(k)
            .key(format!("settings.bus.consumer.{}.switch", c.name));
        let row = table_row(
            format!("settings.bus.consumer.{}", c.name),
            label.join(", "),
            name,
            cells,
            t,
        )
        .child(row().w(ACTION_W).justify_end().child(action));
        if c.enabled { row } else { row.opacity(0.6) }
    });
    vec![
        head,
        col()
            .key("settings.bus.consumers")
            .role("table")
            .label("Consumers")
            .w_full()
            .child(header)
            .children(rows),
    ]
}

/// The marks a consumer row carries, each with the tooltip that explains it.
fn marks(c: &BusConsumerStatus, status: &BusStatusResultMessage) -> Vec<(String, Option<String>)> {
    let mut marks = Vec::new();
    if c.holds_retention_floor && !c.pin_alarm {
        marks.push((
            "Retention floor".to_owned(),
            Some("Retention stops at this consumer's cursor".to_owned()),
        ));
    }
    if c.pin_alarm {
        marks.push((
            format!("Pinning {}", bytes(c.pinned_bytes)),
            Some(format!(
                "Nothing below this consumer's cursor can be trimmed, and it has held that for longer than {}",
                limit(status.pin_alarm_seconds).unwrap_or_default()
            )),
        ));
    }
    if !c.enabled {
        marks.push(("Disabled".to_owned(), None));
    }
    if !c.stalled.is_empty() {
        marks.push(("Stalled".to_owned(), Some(c.stalled.clone())));
    }
    if status.delivering && c.enabled && !c.live {
        marks.push(("Not running".to_owned(), None));
    }
    marks
}

fn count(n: i64) -> String {
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

fn bytes(n: i64) -> String {
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
