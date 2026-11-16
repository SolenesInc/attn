use std::collections::HashMap;
use std::fs::OpenOptions;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::time::Duration;

use attn_client::Facts;
use attn_protocol::{PtyInputMessage, PtyInputProbeResultMessage};
use rune::Tx;
use serde_json::{Map, Value, json};

use crate::State;
use crate::requests::Answer;
use crate::terminal::Typed;

/// Input slower than this, from the key's arrival to its handler or from a write to the
/// daemon's receipt, is an incident.
const SLOW: Duration = Duration::from_millis(250);
/// Seconds between the probes of one terminal's typing.
const PROBE_EVERY: f64 = 2.0;
/// Seconds after which an unanswered probe is an incident.
const PROBE_TIMEOUT: f64 = 5.0;
/// Seconds one terminal keeps quiet about a reason it reported, counting what it held back.
const COOLDOWN: f64 = 30.0;
/// The incident log starts over past this size, as the Tauri app's does.
const LOG_CAP_BYTES: u64 = 8 * 1024 * 1024;

const INCIDENTS_FILE: &str = "terminal-incidents.jsonl";

/// How long typing takes to reach each terminal's program: the key's wait between the platform
/// and its handler, and a write's round trip to the daemon, probed every two seconds of typing.
/// A slow one is written to the incident log `attn debug incidents` reads.
#[derive(Default)]
pub struct InputLatency {
    log: Option<PathBuf>,
    probes: HashMap<String, Probing>,
    reported: HashMap<(String, Reason), Episode>,
}

struct Probing {
    last_sent: f64,
    pending: Option<Pending>,
}

struct Pending {
    probe: String,
    sent: f64,
}

struct Episode {
    reported: f64,
    suppressed: u32,
    max_suppressed_ms: f64,
}

#[derive(Clone, Copy, PartialEq, Eq, Hash)]
enum Reason {
    KeyEventDelay,
    RoundTripDelay,
    ProbeTimeout,
    ProbeFailed,
}

impl Reason {
    fn wire(self) -> &'static str {
        match self {
            Reason::KeyEventDelay => "terminal_key_event_delay",
            Reason::RoundTripDelay => "pty_input_round_trip_delay",
            Reason::ProbeTimeout => "pty_input_probe_timeout",
            Reason::ProbeFailed => "pty_input_probe_failed",
        }
    }
}

/// When it happened, on the window's clock and on the wall's.
#[derive(Clone, Copy)]
struct At {
    clock: f64,
    wall_ms: i64,
}

impl At {
    fn of(state: &State, tx: &Tx) -> At {
        At {
            clock: tx.clock(),
            wall_ms: state.wall_ms(tx),
        }
    }
}

impl InputLatency {
    /// Writes incidents to `terminal-incidents.jsonl` in `debug_dir`, the app's debug folder.
    pub(crate) fn log_to(&mut self, debug_dir: &Path) {
        self.log = Some(debug_dir.join(INCIDENTS_FILE));
    }

    pub(crate) fn forget(&mut self, runtime: &str) {
        self.probes.remove(runtime);
        self.reported.retain(|(id, _), _| id != runtime);
    }

    /// Whether the next write to `runtime` carries a probe; a probe unanswered too long is
    /// reported and given up first.
    fn probe_due(&mut self, runtime: &str, facts: &Facts, at: At) -> bool {
        let Some(probing) = self.probes.get_mut(runtime) else {
            return true;
        };
        if let Some(pending) = &probing.pending {
            let waited = at.clock - pending.sent;
            if waited < PROBE_TIMEOUT {
                return false;
            }
            probing.pending = None;
            self.report(
                runtime,
                facts,
                Reason::ProbeTimeout,
                waited * 1000.0,
                json!({ "thresholdMs": PROBE_TIMEOUT * 1000.0 }),
                at,
            );
        }
        self.probes
            .get(runtime)
            .is_none_or(|p| at.clock - p.last_sent >= PROBE_EVERY)
    }

    fn sent(&mut self, runtime: &str, probe: String, at: At) {
        self.probes.insert(
            runtime.to_owned(),
            Probing {
                last_sent: at.clock,
                pending: Some(Pending {
                    probe,
                    sent: at.clock,
                }),
            },
        );
    }

    fn answered(
        &mut self,
        runtime: &str,
        probe: &str,
        result: Option<PtyInputProbeResultMessage>,
        facts: &Facts,
        at: At,
    ) {
        let Some(probing) = self.probes.get_mut(runtime) else {
            return;
        };
        let Some(pending) = probing.pending.take_if(|p| p.probe == probe) else {
            return;
        };
        let Some(result) = result else {
            return;
        };
        let latency_ms = ((at.clock - pending.sent) * 1000.0).max(0.0);
        let daemon_write_ms = (result.write_duration_us as f64 / 1000.0).max(0.0);
        if !result.success {
            let error = result
                .error
                .unwrap_or_else(|| "PTY input failed".to_owned());
            let detail = json!({ "daemonWriteMs": rounded(daemon_write_ms), "error": error });
            self.report(runtime, facts, Reason::ProbeFailed, latency_ms, detail, at);
        } else if latency_ms >= SLOW.as_secs_f64() * 1000.0 {
            let detail = json!({
                "thresholdMs": SLOW.as_millis(),
                "daemonWriteMs": rounded(daemon_write_ms),
                "transportAndUiMs": rounded((latency_ms - daemon_write_ms).max(0.0)),
                "resultRuntimeId": result.id,
            });
            self.report(
                runtime,
                facts,
                Reason::RoundTripDelay,
                latency_ms,
                detail,
                at,
            );
        }
    }

    fn key_waited(&mut self, runtime: &str, waited: Duration, facts: &Facts, at: At) {
        if waited >= SLOW {
            let detail = json!({ "thresholdMs": SLOW.as_millis() });
            let waited_ms = waited.as_secs_f64() * 1000.0;
            self.report(runtime, facts, Reason::KeyEventDelay, waited_ms, detail, at);
        }
    }

    /// Writes an incident for `runtime`, unless it reported `reason` within the cooldown; then it
    /// counts the one held back, for the next report to carry.
    fn report(
        &mut self,
        runtime: &str,
        facts: &Facts,
        reason: Reason,
        latency_ms: f64,
        detail: Value,
        at: At,
    ) {
        let key = (runtime.to_owned(), reason);
        if let Some(episode) = self.reported.get_mut(&key)
            && at.clock - episode.reported < COOLDOWN
        {
            episode.suppressed += 1;
            episode.max_suppressed_ms = episode.max_suppressed_ms.max(latency_ms);
            return;
        }
        let held_back = self.reported.insert(
            key,
            Episode {
                reported: at.clock,
                suppressed: 0,
                max_suppressed_ms: 0.0,
            },
        );
        let Some(log) = &self.log else {
            return;
        };
        let mut fields = Map::new();
        fields.insert("runtimeId".into(), runtime.into());
        fields.insert("latencyMs".into(), rounded(latency_ms).into());
        if let Some(episode) = held_back.filter(|e| e.suppressed > 0) {
            fields.insert("suppressed".into(), episode.suppressed.into());
            fields.insert(
                "maxSuppressedLatencyMs".into(),
                rounded(episode.max_suppressed_ms).into(),
            );
        }
        if let Value::Object(more) = detail {
            fields.extend(more);
        }
        let record = json!({
            "at": at.wall_ms,
            "kind": "incident",
            "pane": runtime,
            "session": session_of(facts, runtime),
            "reason": reason.wire(),
            "detail": fields,
        });
        if let Err(why) = append(log, &format!("{record}\n")) {
            eprintln!("attn-app: could not log a terminal incident: {why}");
        }
    }
}

/// Sends what the user typed to `terminal`, held while it attaches, and notes how long the key
/// waited to be handled. Every two seconds of typing, the write carries a probe whose answer
/// times its round trip.
pub(crate) fn type_into(state: &mut State, terminal: &str, bytes: &[u8], tx: &mut Tx) -> Typed {
    let at = At::of(state, tx);
    if let Some(waited) = tx.input_time().map(|arrived| arrived.elapsed()) {
        state
            .terminals
            .latency
            .key_waited(terminal, waited, &state.facts, at);
    }
    if !state.terminals.live(terminal)
        || !state
            .terminals
            .latency
            .probe_due(terminal, &state.facts, at)
    {
        return state.terminals.input(terminal, bytes, &state.daemon);
    }
    let probe = state.next_request_id();
    let input = PtyInputMessage {
        id: terminal.into(),
        data: String::from_utf8_lossy(bytes).into_owned(),
        source: Some("user".into()),
        probe_id: Some(probe.clone()),
        ..PtyInputMessage::new()
    };
    state.terminals.latency.sent(terminal, probe.clone(), at);
    let runtime = terminal.to_owned();
    state.ask_as(
        probe.clone(),
        &input,
        move |state: &mut State, answer: Answer, tx: &mut Tx| {
            let at = At::of(state, tx);
            let result = answer.parse::<PtyInputProbeResultMessage>();
            state
                .terminals
                .latency
                .answered(&runtime, &probe, result, &state.facts, at);
        },
        tx,
    );
    Typed::Taken
}

fn session_of(facts: &Facts, runtime: &str) -> Option<String> {
    facts
        .desktops
        .iter()
        .flat_map(|d| &d.panes)
        .find(|p| p.runtime_id == runtime)
        .map(|p| p.session_id.clone())
        .filter(|s| !s.is_empty())
}

fn rounded(ms: f64) -> f64 {
    (ms * 10.0).round() / 10.0
}

fn append(path: &Path, line: &str) -> std::io::Result<()> {
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir)?;
    }
    let size = std::fs::metadata(path).map_or(0, |m| m.len());
    let full = size + line.len() as u64 > LOG_CAP_BYTES;
    let mut file = OpenOptions::new()
        .create(true)
        .append(!full)
        .write(true)
        .truncate(full)
        .open(path)?;
    file.write_all(line.as_bytes())
}
