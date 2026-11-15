use percent_encoding::percent_decode_str;
use rune::Tx;

use crate::State;
use crate::picker::{self, Direction, Spawn};

/// An agent a `spawn` link asks for: the one already working in `cwd` in this profile, or a
/// new Claude there named `label`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Link {
    cwd: String,
    label: Option<String>,
}

/// Takes a link the system opened with the app, `<scheme>://spawn?cwd=<dir>&label=<name>`:
/// links wait for the current desktop, and anything else is dropped.
pub fn open_link(state: &mut State, url: &str, tx: &mut Tx) {
    let Some(link) = parse(url) else {
        eprintln!("attn-app: dropped a link it does not handle: {url}");
        return;
    };
    state.window.links.push(link);
    settle(state, tx);
}

/// Follows the links that waited, once the current desktop is here.
pub(crate) fn settle(state: &mut State, tx: &mut Tx) {
    if state.window.links.is_empty() || state.facts.current_desktop().is_none() {
        return;
    }
    for link in std::mem::take(&mut state.window.links) {
        follow(state, link, tx);
    }
}

fn follow(state: &mut State, link: Link, tx: &mut Tx) {
    let profile = &state.facts.profiles.selected;
    let working = state
        .facts
        .sessions
        .iter()
        .find(|s| s.directory == link.cwd && &s.profile_id == profile)
        .map(|s| s.id.clone());
    match working {
        Some(session) => crate::sidebar::open_agent(state, &session, tx),
        None => picker::spawn(
            state,
            Spawn {
                path: link.cwd,
                agent: "claude".into(),
                yolo: false,
                desktop: None,
                anchor: None,
                direction: Direction::Vertical,
                label: link.label,
            },
            tx,
        ),
    }
}

fn parse(url: &str) -> Option<Link> {
    let (_, rest) = url.split_once("://")?;
    let rest = rest.split('#').next().unwrap_or_default();
    let (host, query) = rest.split_once('?').unwrap_or((rest, ""));
    if host.trim_end_matches('/') != "spawn" {
        return None;
    }
    let mut cwd = None;
    let mut label = None;
    for pair in query.split('&') {
        let (key, value) = pair.split_once('=').unwrap_or((pair, ""));
        let value = decode(value);
        match decode(key).as_str() {
            "cwd" if cwd.is_none() => cwd = Some(value),
            "label" if label.is_none() => label = Some(value),
            _ => {}
        }
    }
    Some(Link {
        cwd: cwd.filter(|c| !c.is_empty())?,
        label: label.filter(|l| !l.is_empty()),
    })
}

fn decode(component: &str) -> String {
    percent_decode_str(&component.replace('+', " "))
        .decode_utf8_lossy()
        .into_owned()
}
