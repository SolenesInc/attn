//! How far the home reaches the machine a session runs on, and what each surface makes of it.

use attn_protocol::{OutpostInfo, Session};
use rune::Tx;

use crate::State;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Reach {
    /// The home itself, or an outpost it reaches.
    Reachable,
    /// Reachable, but the bridge has been silent for a while; typing still goes.
    Stalled,
    Connecting,
    Unreachable,
}

impl Reach {
    /// Whether the terminal attaches and takes typing.
    pub(crate) fn streams(self) -> bool {
        matches!(self, Reach::Reachable | Reach::Stalled)
    }

    pub(crate) fn offline(self) -> bool {
        self == Reach::Unreachable
    }
}

/// How the home reaches `host`; a host no outpost row has is unreachable.
pub(crate) fn reach(host: Option<&str>, outposts: &[OutpostInfo]) -> Reach {
    let Some(host) = host.filter(|h| !h.is_empty()) else {
        return Reach::Reachable;
    };
    let Some(outpost) = outposts.iter().find(|o| o.id == host) else {
        return Reach::Unreachable;
    };
    match outpost.reach.as_str() {
        "reachable" if outpost.state == "stalled" => Reach::Stalled,
        "reachable" => Reach::Reachable,
        "connecting" => Reach::Connecting,
        _ => Reach::Unreachable,
    }
}

pub(crate) fn of(session: &Session, outposts: &[OutpostInfo]) -> Reach {
    reach(session.host.as_deref(), outposts)
}

/// Whether the agent runs on a machine the home cannot reach.
pub(crate) fn offline(session: &Session, outposts: &[OutpostInfo]) -> bool {
    of(session, outposts).offline()
}

/// What a remote session's tile says about its machine while it is not plainly reachable.
pub(crate) fn notice(session: &Session, outposts: &[OutpostInfo]) -> Option<String> {
    let name = crate::desktop::host::name(session, outposts)?;
    match of(session, outposts) {
        Reach::Unreachable => Some(format!("{name} unreachable · typing is not sent")),
        Reach::Connecting => Some(format!("Connecting to {name} · typing is not sent")),
        Reach::Stalled => Some(format!("{name} not responding")),
        Reach::Reachable => None,
    }
}

/// Why an action on `session` that needs its machine cannot run now: its machine is
/// unreachable.
pub(crate) fn unreachable(state: &State, session: &str) -> Option<String> {
    let session = state.facts.sessions.iter().find(|s| s.id == session)?;
    if !offline(session, &state.facts.outposts) {
        return None;
    }
    let name = crate::desktop::host::name(session, &state.facts.outposts)?;
    Some(format!("{name} is unreachable; this needs it now"))
}

/// Refuses an action on `session` while its machine is unreachable, saying so; whether it
/// refused. A recoverable session closes and reloads without its machine having to answer
/// first.
pub(crate) fn refuse_unless_recoverable(state: &mut State, session: &str, tx: &mut Tx) -> bool {
    let recoverable = state
        .facts
        .sessions
        .iter()
        .any(|s| s.id == session && s.state == "recoverable");
    let Some(why) = unreachable(state, session).filter(|_| !recoverable) else {
        return false;
    };
    state.window.notify(why, tx);
    true
}

/// Whether the outpost runs another build than home, or lacks the shared terminal host.
pub(crate) fn needs_update(row: &OutpostInfo, home_build: &str) -> bool {
    !home_build.is_empty()
        && (row.host_missing == Some(true)
            || match row.build.as_deref().filter(|b| !b.is_empty()) {
                Some(build) => build != home_build,
                None => row.state == "refused",
            })
}

pub(crate) fn updating(row: &OutpostInfo) -> bool {
    row.update
        .as_ref()
        .is_some_and(|u| u.phase != "done" && u.phase != "failed")
}

pub(crate) fn update_text(row: &OutpostInfo) -> Option<String> {
    let update = row.update.as_ref()?;
    if let Some(outcome) = update.outcome.as_ref().filter(|o| !o.is_empty()) {
        return Some(outcome.clone());
    }
    Some(
        match update.phase.as_str() {
            "connecting" => "Connecting.",
            "building" => "Building attn for Linux. The first build takes about a minute.",
            "downloading" => "Downloading attn for Linux.",
            "sending" => "Copying it over.",
            "verifying" => "Checking it runs.",
            "restarting" => "Restarting.",
            "done" => "Up to date.",
            "failed" => "Update failed.",
            other => other,
        }
        .to_owned(),
    )
}

/// Outposts by name as a band says them: one, two joined by "and", or a count past two.
pub(crate) fn names(names: &[&str]) -> String {
    match names {
        [a, b] => format!("{a} and {b}"),
        [_, _, _, ..] => format!("{} outposts", names.len()),
        _ => names.concat(),
    }
}
