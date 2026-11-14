use std::collections::BTreeMap;

use base64::Engine;
use serde::Serialize;
use serde_json::{Value, json};

pub(super) const SCHEMA: &str = "attn.support-report.v1";
const PANE_CONTENT_BYTE_LIMIT: usize = 32 * 1024;
const REPORT_BYTE_LIMIT: usize = 8 * 1024 * 1024;
const NOT_COLLECTED: [&str; 3] = [
    "diagnostics.input",
    "diagnostics.terminal",
    "diagnostics.ui",
];

/// A terminal pane the report can carry the output of.
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct Pane {
    pub(crate) pane_id: String,
    pub(crate) runtime_id: String,
    pub(crate) session_id: String,
    pub(crate) title: String,
    pub(crate) session_label: String,
    pub(crate) desktop_id: String,
    pub(crate) desktop_label: String,
    pub(crate) available: bool,
}

/// What the window looked like when the user asked for the report.
pub(crate) struct Capture {
    pub(crate) origin: Value,
    pub(crate) affected_pane: Option<String>,
    pub(crate) panes: Vec<Pane>,
    pub(crate) sessions: Vec<Value>,
    pub(crate) desktops: Vec<Value>,
    pub(crate) settings: BTreeMap<String, String>,
}

/// The output of a ticked pane at the moment the user saves.
pub(crate) enum Output {
    Text(String),
    Gone,
}

#[derive(Serialize)]
struct Omission {
    section: String,
    reason: &'static str,
    #[serde(rename = "omittedBytes", skip_serializing_if = "Option::is_none")]
    omitted_bytes: Option<usize>,
}

impl Omission {
    fn of(section: impl Into<String>, reason: &'static str) -> Omission {
        Omission {
            section: section.into(),
            reason,
            omitted_bytes: None,
        }
    }
}

const SETTINGS: [&str; 4] = [
    "pty_backend_mode",
    "pty_shared_host_enabled",
    "pty_shared_host_active",
    "theme",
];

pub(crate) fn chosen_settings(settings: &BTreeMap<String, String>) -> BTreeMap<String, String> {
    SETTINGS
        .iter()
        .filter_map(|key| Some(((*key).to_owned(), settings.get(*key)?.clone())))
        .collect()
}

/// `path` with the user's home folder written as `~`.
pub(crate) fn shortened(path: &str) -> String {
    for prefix in ["/Users/", "/home/"] {
        if let Some(rest) = path.strip_prefix(prefix) {
            return match rest.find('/') {
                Some(slash) => format!("~{}", &rest[slash..]),
                None => "~".into(),
            };
        }
    }
    path.to_owned()
}

/// The report, as `attn.support-report.v1` writes it. `daemon` is the daemon's answer to
/// `support_snapshot`, when it came in time. `output` is each ticked pane's output now.
pub(crate) fn build(
    capture: &Capture,
    selected: &[String],
    daemon: Option<&Value>,
    output: impl Fn(&Pane) -> Output,
    created_at_ms: i64,
    report_id: String,
) -> Result<String, String> {
    let mut omissions = Vec::new();
    if daemon.is_none() {
        omissions.push(Omission::of("daemons", "snapshots_unavailable"));
    }
    omissions.extend(
        NOT_COLLECTED
            .iter()
            .map(|section| Omission::of(*section, "not_collected_by_native_app")),
    );
    let pane_content: Vec<Value> = capture
        .panes
        .iter()
        .filter(|pane| selected.contains(&pane.pane_id))
        .map(|pane| match output(pane) {
            Output::Gone => {
                omissions.push(Omission::of(
                    format!("paneContent:{}", pane.pane_id),
                    "pane_not_mounted",
                ));
                json!({"paneId": pane.pane_id, "runtimeId": pane.runtime_id, "available": false})
            }
            Output::Text(text) => {
                let plain = PlainText::of(&text);
                if plain.truncated_at_start {
                    omissions.push(Omission {
                        omitted_bytes: Some(plain.omitted_bytes),
                        ..Omission::of(
                            format!("paneContent:{}", pane.pane_id),
                            "truncated_at_start",
                        )
                    });
                }
                json!({
                    "paneId": pane.pane_id,
                    "runtimeId": pane.runtime_id,
                    "sessionId": pane.session_id,
                    "available": true,
                    "text": plain.text,
                    "chars": plain.chars,
                    "bytes": plain.bytes,
                    "omittedChars": plain.omitted_chars,
                    "omittedBytes": plain.omitted_bytes,
                    "truncatedAtStart": plain.truncated_at_start,
                })
            }
        })
        .collect();
    let mut report = json!({
        "schema": SCHEMA,
        "reportId": report_id,
        "createdAtUnixMs": created_at_ms,
        "capture": {
            "origin": capture.origin,
            "confirmation": { "capturedAtUnixMs": created_at_ms },
        },
        "consent": {
            "reviewedAtUnixMs": created_at_ms,
            "paneContent": if selected.is_empty() { "metadata_only" } else { "selected_panes" },
            "selectedPaneIds": selected,
        },
        "build": build_identity(),
        "platform": { "os": std::env::consts::OS, "arch": std::env::consts::ARCH },
        "app": {
            "settings": capture.settings,
            "sessions": capture.sessions,
            "desktops": capture.desktops,
            "panes": capture.panes,
        },
        "daemons": daemon.into_iter().collect::<Vec<_>>(),
        "paneContent": pane_content,
        "omissions": [],
        "privacy": {
            "metadataOnlyByDefault": true,
            "selectedPaneIds": selected,
            "excluded": [
                "environment variables", "credentials", "clipboard", "raw prompt bodies",
                "raw PTY bytes", "raw daemon logs", "arbitrary errors and stacks",
                "model fault replay captures",
            ],
        },
        "limits": {
            "reportBytes": REPORT_BYTE_LIMIT,
            "paneContentBytes": PANE_CONTENT_BYTE_LIMIT,
            "measuredBytes": 0,
        },
    });
    fit(
        &mut report,
        capture.affected_pane.as_deref(),
        &mut omissions,
    )
}

/// Drops pane output, others' before the affected pane's, until the report fits its limit,
/// naming each drop, then records the size it measured.
fn fit(
    report: &mut Value,
    affected_pane: Option<&str>,
    omissions: &mut Vec<Omission>,
) -> Result<String, String> {
    let sized = |report: &mut Value, omissions: &Vec<Omission>| {
        report["omissions"] = serde_json::to_value(omissions).unwrap_or_default();
        serialized(report)
    };
    let mut text = sized(report, omissions);
    for affected in [false, true] {
        if text.len() <= REPORT_BYTE_LIMIT {
            break;
        }
        let panes = report["paneContent"].as_array_mut().into_iter().flatten();
        for pane in panes {
            if (pane["paneId"].as_str() == affected_pane) != affected {
                continue;
            }
            let Some(object) = pane.as_object_mut() else {
                continue;
            };
            let Some(Value::String(dropped)) = object.remove("text") else {
                continue;
            };
            object.insert("contentIncluded".into(), Value::Bool(false));
            omissions.push(Omission {
                omitted_bytes: Some(dropped.len()),
                ..Omission::of(
                    format!(
                        "paneContent:{}",
                        object["paneId"].as_str().unwrap_or_default()
                    ),
                    "report_size_limit",
                )
            });
        }
        text = sized(report, omissions);
    }
    let mut measured = text.len();
    for _ in 0..3 {
        report["limits"]["measuredBytes"] = json!(measured);
        text = serialized(report);
        if text.len() == measured {
            break;
        }
        measured = text.len();
    }
    if text.len() > REPORT_BYTE_LIMIT {
        return Err(format!(
            "Diagnostic metadata is {} bytes, past the report size limit of {REPORT_BYTE_LIMIT} bytes",
            text.len()
        ));
    }
    Ok(text)
}

fn serialized(report: &Value) -> String {
    let mut text = serde_json::to_string_pretty(report).unwrap_or_default();
    text.push('\n');
    text
}

fn build_identity() -> Value {
    let fingerprint = option_env!("ATTN_BUILD_SOURCE_FINGERPRINT")
        .filter(|f| !f.is_empty())
        .unwrap_or("unknown");
    json!({
        "version": stamp(option_env!("ATTN_BUILD_VERSION")),
        "sourceFingerprint": fingerprint,
        "gitCommit": stamp(option_env!("ATTN_BUILD_GIT_COMMIT")),
        "buildTime": stamp(option_env!("ATTN_BUILD_TIME")),
        "instance": option_env!("ATTN_BUILD_INSTANCE").filter(|i| !i.is_empty()).unwrap_or("default"),
        "snapshotFormat": attn_term::SNAPSHOT_FORMAT,
        "sourceDirty": if fingerprint.starts_with("tree:") {
            json!(true)
        } else if fingerprint.starts_with("git:") {
            json!(false)
        } else {
            Value::Null
        },
        "dirtyPaths": dirty_paths(option_env!("ATTN_BUILD_SOURCE_DIRTY_PATHS_BASE64").unwrap_or("")),
    })
}

fn stamp(value: Option<&str>) -> &str {
    value.filter(|v| !v.is_empty()).unwrap_or("unknown")
}

fn dirty_paths(encoded: &str) -> Vec<String> {
    base64::engine::general_purpose::STANDARD
        .decode(encoded)
        .ok()
        .and_then(|bytes| String::from_utf8(bytes).ok())
        .map(|paths| {
            paths
                .split('\0')
                .filter(|p| !p.is_empty())
                .map(str::to_owned)
                .collect()
        })
        .unwrap_or_default()
}

/// A pane's output as plain text: line endings as `\n`, control characters gone, and only
/// its last [`PANE_CONTENT_BYTE_LIMIT`] bytes kept.
struct PlainText {
    text: String,
    chars: usize,
    bytes: usize,
    omitted_chars: usize,
    omitted_bytes: usize,
    truncated_at_start: bool,
}

impl PlainText {
    fn of(raw: &str) -> PlainText {
        let plain: String = raw
            .replace("\r\n", "\n")
            .replace('\r', "\n")
            .chars()
            .filter(|c| *c == '\n' || *c == '\t' || !c.is_control())
            .collect();
        let start = if plain.len() <= PANE_CONTENT_BYTE_LIMIT {
            0
        } else {
            let mut start = plain.len() - PANE_CONTENT_BYTE_LIMIT;
            while !plain.is_char_boundary(start) {
                start += 1;
            }
            start
        };
        let text = plain[start..].to_owned();
        PlainText {
            chars: text.chars().count(),
            bytes: text.len(),
            omitted_chars: plain[..start].chars().count(),
            omitted_bytes: start,
            truncated_at_start: start > 0,
            text,
        }
    }
}
