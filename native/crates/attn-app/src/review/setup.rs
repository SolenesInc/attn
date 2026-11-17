//! A review's language servers, as the key line shows them, and their setup: s starts an agent
//! on the review's machine that sets up a language with no working server, and x stops
//! offering that in the repository.

use attn_protocol::{
    CodeNavSetupMessage, CodeNavSetupResultMessage, ReviewBundle, ReviewNavigation, Session,
    SetSettingMessage,
};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, Tx, row, text};

use super::nav::Note;
use crate::State;

/// What the key line needs to show the servers: the agents that may be setting one up, the
/// settings that say whether setup is offered, and the wall clock.
#[derive(Clone, Copy)]
pub(crate) struct Servers<'a> {
    pub(crate) sessions: &'a [Session],
    pub(crate) settings: &'a std::collections::BTreeMap<String, String>,
    pub(crate) now_ms: i64,
}

const SETUP_RUNNING: [&str; 3] = ["launching", "working", "pending_approval"];

fn has_no_server(n: &ReviewNavigation) -> bool {
    matches!(n.state.as_str(), "missing" | "failed")
}

/// The agent setting up `n`'s server, while it works on it.
fn setting_up<'a>(n: &ReviewNavigation, sessions: &'a [Session]) -> Option<&'a Session> {
    let id = n.setup_session_id.as_deref().filter(|_| has_no_server(n))?;
    sessions
        .iter()
        .find(|s| s.id == id && SETUP_RUNNING.contains(&s.state.as_str()))
}

fn offer_setting(bundle: &ReviewBundle, host: Option<&str>) -> String {
    format!(
        "code_review_setup_offer_{}_{}",
        host.unwrap_or("local"),
        bundle.repository
    )
}

fn offered(bundle: &ReviewBundle, host: Option<&str>, servers: Servers) -> bool {
    servers
        .settings
        .get(&offer_setting(bundle, host))
        .map(String::as_str)
        != Some("off")
}

/// The first language with no working server and no setup running: the one s sets up.
fn to_set_up<'a>(bundle: &'a ReviewBundle, sessions: &[Session]) -> Option<&'a ReviewNavigation> {
    if bundle.repository.is_empty() {
        return None;
    }
    bundle
        .navigation
        .iter()
        .find(|n| has_no_server(n) && setting_up(n, sessions).is_none())
}

fn state_text(state: &str) -> &str {
    match state {
        "starting" => "loading",
        "missing" => "no server",
        other => other,
    }
}

fn seconds_since(started_at: &str, now_ms: i64) -> Option<i64> {
    let began = crate::sidebar::queue::epoch_ms(started_at)?;
    Some((now_ms - began).max(0) / 1000)
}

/// What the servers show that the clock and other desktops' agents change: the seconds while
/// one starts and the labels of the agents setting one up. The tiles redraw when it changes.
pub(crate) fn reads(facts: &attn_client::Facts, desktop: &str, now_ms: i64) -> Vec<String> {
    facts
        .tile_contents
        .tiles
        .iter()
        .filter(|(k, _)| k.desktop == desktop)
        .filter_map(|(_, c)| c.message.review.as_ref())
        .flat_map(|b| &b.navigation)
        .filter_map(|n| match setting_up(n, &facts.sessions) {
            Some(s) => Some(crate::sidebar::model::display_name(s)),
            None => n
                .started_at
                .as_deref()
                .and_then(|at| seconds_since(at, now_ms))
                .map(|s| s.to_string()),
        })
        .collect()
}

/// The next wall-clock millisecond a starting server's seconds move, in any review on screen.
pub(crate) fn next_change(state: &State, now_ms: i64) -> Option<i64> {
    state
        .facts
        .tile_contents
        .tiles
        .values()
        .filter_map(|c| c.message.review.as_ref())
        .flat_map(|b| &b.navigation)
        .filter(|n| n.state == "starting")
        .filter_map(|n| crate::sidebar::queue::epoch_ms(n.started_at.as_deref()?))
        .map(|began| now_ms + 1000 - (now_ms - began).rem_euclid(1000))
        .min()
}

/// Each language's server as the review's navigation reports it, and the setup keys beside
/// the one s would set up.
pub(crate) fn states(
    tile: &str,
    bundle: &ReviewBundle,
    host: Option<&str>,
    servers: Servers,
    t: &Tokens,
) -> Option<El> {
    if bundle.navigation.is_empty() {
        return None;
    }
    let candidate = to_set_up(bundle, servers.sessions);
    let offering = offered(bundle, host, servers);
    let label = |s: String| text(s).font(type_::LABEL);
    let entries = bundle.navigation.iter().map(|n| {
        let language = &n.language;
        if let Some(agent) = setting_up(n, servers.sessions) {
            let session = agent.id.clone();
            return label(format!(
                "{language} setting up in {}",
                crate::sidebar::model::display_name(agent)
            ))
            .color(t.text_secondary)
            .role("button")
            .key(format!("{}.server.{language}", super::prefix(tile)))
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                crate::sidebar::open_agent(state, &session, tx)
            });
        }
        let seconds = (n.state == "starting")
            .then_some(n.started_at.as_deref())
            .flatten()
            .and_then(|at| seconds_since(at, servers.now_ms))
            .map(|s| format!(" {s}s"))
            .unwrap_or_default();
        let detail = n
            .detail
            .as_deref()
            .filter(|d| !d.is_empty() && matches!(n.state.as_str(), "starting" | "failed"))
            .map(|d| format!(": {d}"))
            .unwrap_or_default();
        let mut said = label(format!(
            "{language} {}{seconds}{detail}",
            state_text(&n.state)
        ));
        if let Some(why) = n.detail.as_deref().filter(|d| !d.is_empty()) {
            said = said.description(why.to_owned());
        }
        let said = said
            .key(format!("{}.server.{language}", super::prefix(tile)))
            .color(match n.state.as_str() {
                "ready" => t.success,
                "missing" | "failed" => t.warning,
                _ => t.text_secondary,
            });
        let keys = candidate.is_some_and(|o| std::ptr::eq(o, n)).then(|| {
            let press = |keys: &str, what: &str, key: &str, act: fn(&mut State, &str, &mut Tx)| {
                let tile = tile.to_owned();
                label(format!("{keys} {what}"))
                    .color(t.text_secondary)
                    .role("button")
                    .label(what.to_owned())
                    .key(format!("{}.setup.{key}", super::prefix(&tile)))
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| act(state, &tile, tx))
            };
            let toggle: fn(&mut State, &str, &mut Tx) = |s, tile, tx| {
                toggle_offer(s, tile, tx);
            };
            if offering {
                vec![
                    press("s", "set up", "start", |s, tile, tx| {
                        start(s, tile, tx);
                    }),
                    press("x", "not here", "offer", toggle),
                ]
            } else {
                vec![press("x", "offer setup", "offer", toggle).color(t.text_tertiary)]
            }
        });
        row()
            .gap(space::S2)
            .child(said)
            .children(keys.into_iter().flatten())
    });
    Some(
        row()
            .key(format!("{}.servers", super::prefix(tile)))
            .role("group")
            .label("Language servers")
            .gap(16.0)
            .children(entries),
    )
}

fn bundle_of<'a>(
    state: &'a State,
    tile: &str,
) -> Option<(&'a ReviewBundle, Option<String>, String)> {
    state
        .facts
        .tile_contents
        .tiles
        .iter()
        .find(|(k, c)| k.tile == tile && c.message.review.is_some())
        .map(|(k, c)| {
            (
                c.message.review.as_ref().expect("filtered on a review"),
                c.message.host.clone().filter(|h| !h.is_empty()),
                k.desktop.clone(),
            )
        })
}

fn say(state: &mut State, tile: &str, note: Note) {
    if let Some(nav) = state.readings.reviews.nav_mut(tile) {
        nav.note = Some(note);
        state.readings.revision += 1;
    }
}

/// Starts an agent that sets up the first language with no working server. False when no
/// language needs one.
pub(crate) fn start(state: &mut State, tile: &str, tx: &mut Tx) -> bool {
    let Some((bundle, host, desktop)) = bundle_of(state, tile) else {
        return false;
    };
    let Some(n) = to_set_up(bundle, &state.facts.sessions).cloned() else {
        return false;
    };
    let document = super::source_of_tile(state, tile)
        .map(|(_, d)| d)
        .unwrap_or_default();
    let repository = bundle.repository.clone();
    say(
        state,
        tile,
        Note::Asking(format!(
            "Starting an agent to set up {} navigation…",
            n.language
        )),
    );
    let tile = tile.to_owned();
    state.ask(
        |id| CodeNavSetupMessage {
            desktop_id: desktop,
            host,
            document,
            repository,
            language: n.language,
            file: n.file,
            state: n.state,
            detail: n.detail,
            request_id: Some(id),
            ..CodeNavSetupMessage::new()
        },
        move |state, answer, _| {
            let problem = match answer {
                crate::requests::Answer::NotSent => Some("the daemon is not reachable".to_owned()),
                other => match other.outcome() {
                    Err(why) => Some(why),
                    Ok(r) => match r.parse::<CodeNavSetupResultMessage>() {
                        Some(m) if m.success => None,
                        Some(m) => Some(
                            m.error
                                .unwrap_or_else(|| "the daemon gave no reason".into()),
                        ),
                        None => Some("the daemon's answer did not parse".into()),
                    },
                },
            };
            if let Some(nav) = state.readings.reviews.nav_mut(&tile) {
                nav.note = problem.map(|p| Note::Alert(format!("Could not start setup: {p}")));
                state.readings.revision += 1;
            }
        },
        tx,
    );
    true
}

/// Stops offering setup in the review's repository, or offers it again. False when every
/// language has a working server.
pub(crate) fn toggle_offer(state: &mut State, tile: &str, _: &mut Tx) -> bool {
    let Some((bundle, host, _)) = bundle_of(state, tile) else {
        return false;
    };
    if bundle.repository.is_empty() || !bundle.navigation.iter().any(has_no_server) {
        return false;
    }
    let key = offer_setting(bundle, host.as_deref());
    let servers = Servers {
        sessions: &state.facts.sessions,
        settings: &state.facts.settings,
        now_ms: 0,
    };
    let was = offered(bundle, host.as_deref(), servers);
    let _ = state.daemon.send(&SetSettingMessage {
        key,
        value: if was { "off".into() } else { String::new() },
        ..SetSettingMessage::new()
    });
    say(
        state,
        tile,
        Note::Status(
            if was {
                "Setup is no longer offered in this repository; s still sets up."
            } else {
                "Setup is offered again in this repository."
            }
            .into(),
        ),
    );
    true
}
