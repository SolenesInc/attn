use std::collections::HashSet;

use attn_client::Facts;
use attn_protocol::Session;

use crate::setting;
use crate::sidebar::model::{AgentRow, AgentState};
use crate::sidebar::queue::{self, Places};

/// The activity generator's intervals in seconds, watching and present, when the setting
/// holds none.
const DEFAULT_ACTIVITY_INTERVALS_S: [i64; 2] = [120, 300];
const ACTIVITY_INTERVAL_MIN_S: i64 = 30;
const ACTIVITY_INTERVALS: &str = "activity.intervals";

/// The state groups of the Sessions card, in the order it lists them.
const STATE_GROUPS: [(AgentState, &str, &str); 8] = [
    (AgentState::WaitingInput, "waiting", "Waiting for input"),
    (AgentState::PendingApproval, "pending", "Pending approval"),
    (AgentState::Launching, "launching", "Launching"),
    (AgentState::Working, "working", "Working"),
    (AgentState::Scheduled, "scheduled", "Scheduled"),
    (AgentState::Idle, "idle", "Idle"),
    (AgentState::Recoverable, "recoverable", "Recoverable"),
    (AgentState::Unknown, "unknown", "Unknown / error"),
];

const STILL_RUNNING: [(AgentState, &str); 4] = [
    (AgentState::Working, "working"),
    (AgentState::Launching, "launching"),
    (AgentState::Scheduled, "scheduled"),
    (AgentState::Recoverable, "recoverable"),
];

/// The home screen's Sessions card and the banner above it.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Home {
    /// In queue flow with nothing owed: what is still running, for the All settled banner.
    pub all_settled: Option<String>,
    pub empty: bool,
    pub turns: Vec<HomeAgent>,
    /// The Settled heading over the state groups, in queue flow.
    pub settled_heading: bool,
    pub groups: Vec<Group>,
    pub snoozed: Vec<HomeAgent>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Group {
    pub key: &'static str,
    pub label: &'static str,
    pub rows: Vec<HomeAgent>,
}

/// One agent as a home row shows it. A row is built again only when this changes.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct HomeAgent {
    pub agent: AgentRow,
    pub chief: bool,
    pub activity: Option<Activity>,
    pub age: String,
    pub snoozed_until: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Activity {
    pub line: String,
    /// When a line generated before a gap in the generator's refreshes was generated: such a
    /// line shows dimmed, as of that time.
    pub stale_since: Option<String>,
}

/// The Sessions card as the Tauri app's Dashboard builds it: in queue flow, the owed turns
/// oldest first and the rest by state under Settled; otherwise every agent by state. Snoozed
/// agents sit apart in either.
pub fn home(facts: &Facts, now_ms: i64) -> Home {
    let places = Places::of(facts);
    let sessions: Vec<&Session> = places.agents().collect();
    let queue_flow = setting::on(&facts.settings, queue::QUEUE_MODE);
    let bands = queue_flow.then(|| queue::bands(facts, now_ms));
    let stale_ms = activity_stale_ms(facts);
    let row = |session: &Session, age: String, snoozed_until: String| HomeAgent {
        agent: AgentRow::of(session, false),
        chief: session.chief_of_staff == Some(true),
        activity: activity(session, now_ms, stale_ms),
        age,
        snoozed_until,
    };

    let mut snoozed: Vec<&Session> = match &bands {
        Some(bands) => {
            let mut queued: Vec<&Session> = bands.snoozed.iter().map(|r| r.session).collect();
            let chief = sessions.iter().find(|s| s.chief_of_staff == Some(true));
            if let Some(chief) = chief.filter(|c| !queued.iter().any(|s| s.id == c.id)) {
                queued.push(chief);
            }
            queued
        }
        None => sessions.clone(),
    };
    snoozed.retain(|s| queue::is_snoozed(s.turn_snoozed_until.as_deref(), now_ms));
    snoozed.sort_by(|a, b| queue::compare_wake_order(a, b));
    let turns: Vec<&Session> = bands
        .as_ref()
        .map(|b| b.turns.iter().map(|r| r.session).collect())
        .unwrap_or_default();
    let aside: HashSet<&str> = snoozed
        .iter()
        .chain(&turns)
        .map(|s| s.id.as_str())
        .collect();
    let settled: Vec<&Session> = sessions
        .iter()
        .copied()
        .filter(|s| !aside.contains(s.id.as_str()))
        .collect();
    let state_of = |s: &Session| AgentState::parse(&s.state);
    let groups: Vec<Group> = STATE_GROUPS
        .iter()
        .map(|&(state, key, label)| Group {
            key,
            label,
            rows: settled
                .iter()
                .filter(|s| state_of(s) == state)
                .map(|s| row(s, String::new(), String::new()))
                .collect(),
        })
        .filter(|g| !g.rows.is_empty())
        .collect();
    let all_settled = bands
        .as_ref()
        .filter(|b| {
            b.turns.is_empty()
                && (b.chief.is_some() || !b.settled.is_empty() || !b.snoozed.is_empty())
        })
        .map(|_| {
            let counts: Vec<String> = STILL_RUNNING
                .iter()
                .map(|&(state, label)| {
                    (
                        settled.iter().filter(|s| state_of(s) == state).count(),
                        label,
                    )
                })
                .filter(|(n, _)| *n > 0)
                .map(|(n, label)| format!("{n} {label}"))
                .collect();
            if counts.is_empty() {
                "Nothing is running.".to_owned()
            } else {
                counts.join(" · ")
            }
        });
    Home {
        all_settled,
        empty: sessions.is_empty(),
        turns: turns
            .iter()
            .map(|s| {
                row(
                    s,
                    queue::turn_age(s.turn_opened_at.as_deref(), now_ms),
                    String::new(),
                )
            })
            .collect(),
        settled_heading: queue_flow && !groups.is_empty(),
        groups,
        snoozed: snoozed
            .iter()
            .map(|s| {
                row(
                    s,
                    String::new(),
                    queue::wake_time_long(s.turn_snoozed_until.as_deref(), now_ms),
                )
            })
            .collect(),
    }
}

/// The next moment an activity line the home screen shows turns stale.
pub fn next_change(facts: &Facts, now_ms: i64) -> Option<i64> {
    let stale_ms = activity_stale_ms(facts);
    Places::of(facts)
        .agents()
        .filter_map(activity_at)
        .map(|at| at + stale_ms + 1)
        .filter(|at| *at > now_ms)
        .min()
}

fn activity(session: &Session, now_ms: i64, stale_ms: i64) -> Option<Activity> {
    let line = session.activity.as_deref()?.trim();
    if line.is_empty() {
        return None;
    }
    Some(Activity {
        line: line.to_owned(),
        stale_since: activity_at(session)
            .filter(|at| now_ms - at > stale_ms)
            .map(crate::sidebar::snooze::clock),
    })
}

fn activity_at(session: &Session) -> Option<i64> {
    session.activity.as_ref()?;
    session.activity_at.as_deref().and_then(queue::epoch_ms)
}

/// Three times the longer of the activity generator's intervals, as the Tauri app's
/// `activityStaleMs` reads the setting.
fn activity_stale_ms(facts: &Facts) -> i64 {
    let stored: Option<serde_json::Value> = facts
        .settings
        .get(ACTIVITY_INTERVALS)
        .filter(|raw| !raw.trim().is_empty())
        .and_then(|raw| serde_json::from_str(raw).ok());
    let interval = |field: &str, default: i64| {
        stored
            .as_ref()
            .map_or(Some(default), |v| match v.get(field) {
                None | Some(serde_json::Value::Null) => Some(default),
                Some(n) => n.as_f64().map(|n| n as i64),
            })
            .unwrap_or(0)
    };
    let [watching, present] = DEFAULT_ACTIVITY_INTERVALS_S;
    let seconds = interval("watching", watching)
        .max(interval("present", present))
        .max(ACTIVITY_INTERVAL_MIN_S);
    seconds * 3 * 1000
}
