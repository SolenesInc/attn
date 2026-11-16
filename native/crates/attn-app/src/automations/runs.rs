//! Automation runs on the desktops, grouped by the automation that started them: the runs that
//! need the user oldest first, then the ones still working, then the finished ones newest first.
//! ⌘⇧J walks the runs that need the user, group by group, and wraps.

use std::collections::{BTreeMap, HashSet};

use attn_client::Facts;
use attn_protocol::Session;
use rune::Tx;

use crate::State;
use crate::sidebar::queue::{Places, is_snoozed};

const LIVE: [&str; 4] = ["launching", "working", "scheduled", "recoverable"];

pub(crate) struct Group<'a> {
    pub(crate) id: i64,
    pub(crate) name: String,
    /// The current runs; a superseded singleton session is left out.
    pub(crate) runs: Vec<&'a Session>,
    /// Every run owed a turn and not snoozed, superseded or not.
    pub(crate) needing_you: Vec<&'a Session>,
}

pub(crate) fn needs_you(run: &Session, now_ms: i64) -> bool {
    run.turn_owed == Some(true) && !is_snoozed(run.turn_snoozed_until.as_deref(), now_ms)
}

fn phase(run: &Session, now_ms: i64) -> u8 {
    if needs_you(run, now_ms) {
        0
    } else if LIVE.contains(&run.state.as_str()) {
        1
    } else {
        2
    }
}

/// The automation runs among `sessions`, in desktop order, each once.
pub(crate) fn groups<'a>(
    sessions: impl IntoIterator<Item = &'a Session>,
    now_ms: i64,
) -> Vec<Group<'a>> {
    let mut seen = HashSet::new();
    let mut by_definition: BTreeMap<i64, Group<'a>> = BTreeMap::new();
    for session in sessions {
        let Some(automation) = &session.automation else {
            continue;
        };
        if !seen.insert(session.id.as_str()) {
            continue;
        }
        by_definition
            .entry(automation.definition_id)
            .or_insert_with(|| Group {
                id: automation.definition_id,
                name: automation.definition_name.clone(),
                runs: Vec::new(),
                needing_you: Vec::new(),
            })
            .runs
            .push(session);
    }
    let mut groups: Vec<Group<'a>> = by_definition
        .into_values()
        .map(|mut group| {
            group.runs.sort_by(|a, b| {
                let (pa, pb) = (phase(a, now_ms), phase(b, now_ms));
                let by_time = match pa {
                    0 => a.turn_opened_at.cmp(&b.turn_opened_at),
                    2 => b.state_since.cmp(&a.state_since),
                    _ => std::cmp::Ordering::Equal,
                };
                pa.cmp(&pb).then(by_time).then_with(|| a.id.cmp(&b.id))
            });
            group.needing_you = group
                .runs
                .iter()
                .copied()
                .filter(|run| needs_you(run, now_ms))
                .collect();
            group.runs.retain(|run| {
                run.automation
                    .as_ref()
                    .is_none_or(|a| a.superseded != Some(true))
            });
            group
        })
        .filter(|g| !g.runs.is_empty() || !g.needing_you.is_empty())
        .collect();
    groups.sort_by(|a, b| a.name.cmp(&b.name).then(a.id.cmp(&b.id)));
    groups
}

/// The runs on the selected profile's desktops.
pub(crate) fn on_desktops(facts: &Facts, now_ms: i64) -> Vec<Group<'_>> {
    let places = Places::of(facts).places;
    groups(
        places
            .into_iter()
            .filter(|(desktop, ..)| desktop.is_some())
            .flat_map(|(_, _, sessions)| sessions),
        now_ms,
    )
}

/// The next run needing the user after `current`, its place in that batch and the batch's size.
pub(crate) fn next_needing_you<'a>(
    groups: &[Group<'a>],
    current: Option<&str>,
) -> Option<(&'a Session, usize, usize)> {
    let batch: Vec<&Session> = groups
        .iter()
        .flat_map(|g| g.needing_you.iter().copied())
        .collect();
    if batch.is_empty() {
        return None;
    }
    let at = current
        .and_then(|id| batch.iter().position(|run| run.id == id))
        .map_or(0, |i| (i + 1) % batch.len());
    Some((batch[at], at + 1, batch.len()))
}

/// ⌘⇧J: shows the next run that stopped and is owed a turn, after the agent on screen.
pub(crate) fn next_run(state: &mut State, tx: &mut Tx) {
    let now_ms = state.wall_ms(tx);
    let on_screen = state.window.on_screen(&state.facts);
    let groups = on_desktops(&state.facts, now_ms);
    let next = next_needing_you(&groups, on_screen.as_deref()).map(|(run, ..)| run.id.clone());
    if let Some(id) = next {
        crate::sidebar::open_agent(state, &id, tx);
    }
}

#[cfg(test)]
mod tests {
    use attn_protocol::AutomationProvenance;

    use super::*;

    const NOW: &str = "2026-09-26T12:00:00Z";

    fn now() -> i64 {
        crate::sidebar::queue::epoch_ms(NOW).unwrap()
    }

    fn run(id: &str, definition: &str, state: &str) -> Session {
        let (definition_id, name) = match definition {
            "docs" => (1, "nightly docs"),
            _ => (2, "pr reviewer"),
        };
        Session {
            id: id.into(),
            state: state.into(),
            automation: Some(AutomationProvenance {
                definition_id,
                definition_name: name.into(),
                ..AutomationProvenance::default()
            }),
            ..Session::default()
        }
    }

    fn owed(mut session: Session, opened_at: &str) -> Session {
        session.turn_owed = Some(true);
        session.turn_opened_at = Some(opened_at.into());
        session
    }

    fn since(mut session: Session, at: &str) -> Session {
        session.state_since = at.into();
        session
    }

    fn ids(runs: &[&Session]) -> Vec<String> {
        runs.iter().map(|r| r.id.clone()).collect()
    }

    #[test]
    fn runs_needing_you_come_oldest_first_then_live_runs_then_finished_runs_newest_first() {
        let sessions = [
            since(
                run("finished-old", "review", "idle"),
                "2026-09-25T08:00:00Z",
            ),
            owed(
                run("asking-new", "review", "waiting_input"),
                "2026-09-26T11:00:00Z",
            ),
            run("live", "review", "working"),
            since(
                run("finished-new", "review", "idle"),
                "2026-09-26T10:00:00Z",
            ),
            owed(
                run("asking-old", "review", "waiting_input"),
                "2026-09-26T09:00:00Z",
            ),
            Session {
                id: "manual".into(),
                state: "working".into(),
                ..Session::default()
            },
        ];
        let groups = groups(&sessions, now());

        assert_eq!(groups.len(), 1);
        assert_eq!(
            ids(&groups[0].runs),
            [
                "asking-old",
                "asking-new",
                "live",
                "finished-new",
                "finished-old"
            ]
        );
        assert_eq!(ids(&groups[0].needing_you), ["asking-old", "asking-new"]);
    }

    #[test]
    fn a_snoozed_run_does_not_need_you_and_a_run_on_two_desktops_counts_once() {
        let mut snoozed = owed(
            run("snoozed", "docs", "waiting_input"),
            "2026-09-26T09:00:00Z",
        );
        snoozed.turn_snoozed_until = Some("2026-09-26T13:00:00Z".into());
        let asking = owed(
            run("asking", "docs", "waiting_input"),
            "2026-09-26T10:00:00Z",
        );
        let sessions = [&snoozed, &asking, &asking];
        let groups = groups(sessions, now());

        assert_eq!(ids(&groups[0].runs), ["asking", "snoozed"]);
        assert_eq!(ids(&groups[0].needing_you), ["asking"]);
    }

    #[test]
    fn the_next_run_walks_the_batch_in_group_order_wraps_and_starts_over_from_a_run_that_no_longer_needs_you()
     {
        let sessions = [
            owed(
                run("review-1", "review", "waiting_input"),
                "2026-09-26T08:00:00Z",
            ),
            owed(
                run("docs-1", "docs", "waiting_input"),
                "2026-09-26T10:00:00Z",
            ),
            owed(
                run("docs-2", "docs", "waiting_input"),
                "2026-09-26T11:00:00Z",
            ),
            run("docs-live", "docs", "working"),
        ];
        let groups = groups(&sessions, now());
        let mut walk = Vec::new();
        let mut current: Option<String> = None;
        for _ in 0..4 {
            let (run, at, of) = next_needing_you(&groups, current.as_deref()).unwrap();
            walk.push(format!("{} {at}/{of}", run.id));
            current = Some(run.id.clone());
        }
        assert_eq!(
            walk,
            ["docs-1 1/3", "docs-2 2/3", "review-1 3/3", "docs-1 1/3"]
        );
        assert_eq!(
            next_needing_you(&groups, Some("docs-live")).map(|(r, ..)| r.id.as_str()),
            Some("docs-1")
        );
        let idle = [run("a", "docs", "idle")];
        assert!(next_needing_you(&super::groups(&idle, now()), None).is_none());
    }
}
