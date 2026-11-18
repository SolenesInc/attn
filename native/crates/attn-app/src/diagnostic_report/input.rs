use std::collections::HashMap;
use std::path::Path;

use serde_json::{Map, Value, json};

/// Input samples the report carries from the log, the newest, as the Tauri report keeps.
const HISTORY_CAPACITY: usize = 256;
const STRING_LIMIT: usize = 240;
const ARRAY_LIMIT: usize = 400;
const DEPTH_LIMIT: usize = 7;

/// The keys a logged sample keeps, the Tauri report's allowlist: the log is the Tauri app's
/// too, and nothing else it holds goes into a report.
const ALLOWED_KEYS: &[&str] = &[
    "action",
    "active",
    "activeElement",
    "activeWrapperCount",
    "activeWrappers",
    "agent",
    "app",
    "appStartedAt",
    "at",
    "attempt",
    "backend",
    "bail",
    "base64Chars",
    "blank",
    "browserComposing",
    "browserHostOwners",
    "build",
    "canvas",
    "capturedAtMonotonicMs",
    "capturedAtUnixMs",
    "cellHeight",
    "cellWidth",
    "center",
    "ch",
    "clientHeight",
    "clientWidth",
    "clipping",
    "cols",
    "command",
    "commit",
    "composing",
    "compositionEndedAt",
    "compositionStartedAt",
    "connected",
    "counts",
    "cw",
    "daemonWriteMs",
    "dataBytes",
    "decodeCount",
    "decodedBytes",
    "decodeMs",
    "delay",
    "delayMs",
    "desktop",
    "display",
    "documentFocused",
    "dpr",
    "droppedForRecordBudget",
    "droppedOps",
    "durationMs",
    "event",
    "extraCols",
    "extraRows",
    "fingerprint",
    "flooredCols",
    "flooredRows",
    "focus",
    "force",
    "fromCols",
    "fromRows",
    "generation",
    "hasMeasuredSize",
    "height",
    "initialStateReceived",
    "isActivePane",
    "keyClass",
    "kind",
    "lastCommandAt",
    "lastCommandName",
    "lastCommandRuntimeId",
    "lastEventAt",
    "lastEventName",
    "lastEventRuntimeId",
    "lastEventSeq",
    "lastInputAt",
    "lastListenerError",
    "lastListenerErrorAt",
    "lastPaintAt",
    "lastPaintQuads",
    "lastProbe",
    "lastPtyInputAt",
    "lastPtyInputRuntimeId",
    "lastPtyOutputAt",
    "lastPtyOutputRuntimeId",
    "lastPtyOutputSeq",
    "lastReceipt",
    "lastRenderAt",
    "lastResize",
    "lastSend",
    "lastWriteAt",
    "lateByMs",
    "len",
    "listenerErrorCount",
    "model",
    "modelPrintable",
    "modifiers",
    "noop",
    "observerId",
    "offset",
    "opacity",
    "operation",
    "outcome",
    "overflowPx",
    "paintedSinceResize",
    "pane",
    "paneCount",
    "paneIds",
    "paneKind",
    "pty",
    "ptyInputBytes",
    "ptyInputCount",
    "ptyJsonParseMs",
    "ptyOutputBase64Chars",
    "ptyOutputCount",
    "quads",
    "ready",
    "reason",
    "reasons",
    "recent",
    "recentEvents",
    "rect",
    "rendererEpoch",
    "repairAttempts",
    "repeat",
    "restore",
    "retainedWriteBytes",
    "retries",
    "rightOverflowPx",
    "root",
    "rootChildren",
    "rows",
    "runtimeId",
    "schema",
    "seq",
    "session",
    "shouldRender",
    "skipped",
    "snapshot",
    "snapshotBytes",
    "snapshotTruncated",
    "socketState",
    "source",
    "splitCount",
    "state",
    "success",
    "syncActive",
    "tag",
    "terminalFocused",
    "toCols",
    "toRows",
    "transportReady",
    "trigger",
    "trusted",
    "updatedAt",
    "version",
    "view",
    "visibility",
    "visibilityState",
    "visible",
    "width",
    "winInnerHeight",
    "winInnerWidth",
    "window",
    "wsJsonParseMs",
    "wsMessageBytes",
    "wsMessageCount",
    "x",
    "y",
];

/// The input samples in the log at `path`, as the report's `diagnostics.input.historical`
/// carries them.
pub(super) fn historical(path: Option<&Path>, captured_at_ms: i64) -> Value {
    let contents = path
        .and_then(|path| std::fs::read_to_string(path).ok())
        .unwrap_or_default();
    let samples: Vec<Value> = contents
        .lines()
        .filter_map(|line| serde_json::from_str::<Value>(line).ok())
        .filter(|record| record["kind"] == "input")
        .map(|record| sanitized(&record, 0, ""))
        .collect();
    let kept = &samples[samples.len().saturating_sub(HISTORY_CAPACITY)..];
    json!({
        "capacity": HISTORY_CAPACITY,
        "total": samples.len(),
        "capturedAtUnixMs": captured_at_ms,
        "events": kept,
    })
}

fn sanitized(value: &Value, depth: usize, parent: &str) -> Value {
    if depth > DEPTH_LIMIT {
        return value.clone();
    }
    match value {
        Value::String(text) if text.chars().count() > STRING_LIMIT => Value::String(format!(
            "{}…",
            text.chars().take(STRING_LIMIT).collect::<String>()
        )),
        Value::Array(items) => Value::Array(
            items[items.len().saturating_sub(ARRAY_LIMIT)..]
                .iter()
                .map(|item| sanitized(item, depth + 1, parent))
                .collect(),
        ),
        Value::Object(fields) => {
            let mut kept = Map::new();
            for (key, entry) in fields {
                if parent == "counts" {
                    if is_counter(key) && entry.is_number() {
                        kept.insert(key.clone(), entry.clone());
                    }
                } else if ALLOWED_KEYS.contains(&key.as_str()) {
                    kept.insert(key.clone(), sanitized(entry, depth + 1, key));
                }
            }
            Value::Object(kept)
        }
        _ => value.clone(),
    }
}

fn is_counter(key: &str) -> bool {
    let word =
        |part: &str| !part.is_empty() && part.bytes().all(|b| b.is_ascii_lowercase() || b == b'_');
    match key.split_once(':') {
        Some((event, outcome)) => word(event) && word(outcome),
        None => word(key),
    }
}

/// Each traced input's path from the window to the daemon's write, under its trace id, as the
/// Tauri report's `deriveInputJourneys` concludes it. The native app keeps no output timings,
/// so a journey ends at the daemon's write.
pub(super) fn journeys(frontend: &Value, daemon: Option<&Value>) -> Value {
    let events = frontend["events"]
        .as_array()
        .map(Vec::as_slice)
        .unwrap_or_default();
    let mut order: Vec<&str> = Vec::new();
    let mut by_trace: HashMap<&str, Vec<&Value>> = HashMap::new();
    for event in events {
        let Some(trace) = event["traceId"].as_str() else {
            continue;
        };
        by_trace
            .entry(trace)
            .or_insert_with(|| {
                order.push(trace);
                Vec::new()
            })
            .push(event);
    }
    let writes: HashMap<&str, &Value> = daemon
        .and_then(|d| d["input_traces"].as_array())
        .into_iter()
        .flatten()
        .filter_map(|w| Some((w["trace_id"].as_str()?, w)))
        .collect();
    let journeys: Vec<Value> = order
        .into_iter()
        .map(|trace| {
            let events = &by_trace[trace];
            let stage = |name: &str| events.iter().find(|e| e["stage"] == name).copied();
            let (document, terminal, transport) =
                (stage("document"), stage("terminal"), stage("transport"));
            let write = writes.get(trace).copied();
            let runtime = transport
                .and_then(|e| e["runtimeId"].as_str())
                .or_else(|| terminal.and_then(|e| e["runtimeId"].as_str()))
                .or_else(|| write.and_then(|w| w["runtime_id"].as_str()));
            let ready = transport
                .map(|e| e["socketState"] == 1 && e["initialStateReceived"] == true);
            let conclusion = match (terminal, transport, write) {
                (None, ..) => "document_observed_no_terminal_decision".to_owned(),
                (Some(terminal), None, _) => format!(
                    "terminal_{}_no_transport",
                    terminal["outcome"].as_str().unwrap_or("observed")
                ),
                (_, Some(_), None) if ready == Some(true) => {
                    "transport_sent_no_daemon_evidence".to_owned()
                }
                (_, Some(_), None) => "transport_queued_while_unready".to_owned(),
                (_, _, Some(write)) if write["write_result"] != "accepted" => {
                    "pty_write_failed".to_owned()
                }
                _ => "pty_write_succeeded_no_agent_evidence".to_owned(),
            };
            let mut journey = json!({
                "traceId": trace,
                "startedAtUnixMs": document.or(events.first().copied()).map_or(json!(0), |e| e["atUnixMs"].clone()),
                "conclusion": conclusion,
            });
            if let Some(runtime) = runtime {
                journey["runtimeId"] = runtime.into();
            }
            if let Some(outcome) = terminal.and_then(|e| e["outcome"].as_str()) {
                journey["terminalOutcome"] = outcome.into();
            }
            if let Some(ready) = ready {
                journey["transportReady"] = ready.into();
            }
            if let Some(write) = write {
                let mut daemon_write = json!({
                    "result": write["write_result"],
                    "durationUs": write["write_duration_us"],
                });
                if let Some(class) = write["error_class"].as_str() {
                    daemon_write["errorClass"] = class.into();
                }
                journey["daemonWrite"] = daemon_write;
            }
            journey
        })
        .collect();
    Value::Array(journeys)
}
