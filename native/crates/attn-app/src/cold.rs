//! The window before the home has ever answered, on a machine whose daemon relays the app to a
//! home elsewhere: this machine's own terminals of the profile the home last scoped this app to,
//! live in one row, which the app derives from its link to this machine's daemon. The row is no
//! desktop: the app keeps no layout for it and sends nothing the home owns while it shows, and
//! the home's desktops replace it once the home answers. With no profile known, it shows none.

use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use attn_client::{Desktop, Facts};
use attn_protocol::{DesktopPane, Profile};

/// The id the row goes by, which no desktop of the home has.
pub(crate) const ROW: &str = "";

const LAST_PROFILE: &str = "last-profile.json";

/// The profile the home last scoped this app to, kept in `last-profile.json` in the app's data
/// folder, so a launch before the home answers shows that profile's terminals and no other's.
#[derive(Clone, Default)]
pub struct LastProfile(Option<Arc<Mutex<Kept>>>);

struct Kept {
    path: PathBuf,
    id: Option<String>,
}

impl LastProfile {
    /// The profile kept in `data_dir`, as an earlier run left it.
    pub fn in_dir(data_dir: &Path) -> LastProfile {
        let path = data_dir.join(LAST_PROFILE);
        let id = std::fs::read(&path)
            .ok()
            .and_then(|bytes| serde_json::from_slice::<String>(&bytes).ok())
            .filter(|id| !id.is_empty());
        LastProfile(Some(Arc::new(Mutex::new(Kept { path, id }))))
    }

    /// Keeps the profile the home scoped this app to in `facts`, and returns the one kept.
    fn follow(&self, facts: &Facts) -> Option<String> {
        let mut kept = self.0.as_ref()?.lock().unwrap();
        let selected = &facts.profiles.selected;
        let scoped = facts.connection == attn_client::Connection::Connected && !selected.is_empty();
        if scoped && kept.id.as_ref() != Some(selected) {
            let written = serde_json::to_vec(selected)
                .map_err(|e| e.to_string())
                .and_then(|bytes| crate::quick_capture::draft::commit(&kept.path, &bytes));
            if let Err(why) = written {
                eprintln!(
                    "attn-app: the profile this app shows was not kept for its next launch: {why}"
                );
            }
            kept.id = Some(selected.clone());
        }
        kept.id.clone()
    }
}

/// Whether the window shows this machine's row.
pub(crate) fn shows(facts: &Facts) -> bool {
    awaits_home(facts) && facts.desktops.len() == 1 && facts.desktops[0].id == ROW
}

/// The home is unreachable and has not answered yet, and this machine's own daemon has.
fn awaits_home(facts: &Facts) -> bool {
    matches!(
        facts.connection,
        attn_client::Connection::HomeUnreachable(_)
    ) && facts.daemon_instance.is_empty()
        && facts.machine.home.is_some()
        && facts.machine.connected
}

/// What the window reads: the facts, and while the home has not answered, this machine's
/// sessions of the profile `last` keeps in place of the home's, with the row as the one desktop,
/// when one of them has a terminal.
pub(crate) fn view(mut facts: Facts, last: &LastProfile) -> Facts {
    let profile = last.follow(&facts);
    let Some(profile) = profile.filter(|_| awaits_home(&facts)) else {
        return facts;
    };
    let machine = facts.machine.clone();
    let here = Some(machine.id.clone());
    let sessions: Vec<_> = machine
        .sessions
        .iter()
        .filter(|session| session.profile_id == profile)
        .cloned()
        .map(|mut session| {
            session.host.clone_from(&here);
            session
        })
        .collect();
    let panes: Vec<DesktopPane> = sessions
        .iter()
        .filter_map(|session| {
            let binding = machine
                .terminals
                .iter()
                .find(|b| b.session_id == session.id && b.ask_id.is_none())?;
            Some(DesktopPane {
                desktop_id: ROW.to_owned(),
                kind: if session.agent == "shell" {
                    "shell"
                } else {
                    "agent"
                }
                .into(),
                pane_id: binding.terminal_id.clone(),
                runtime_id: binding.terminal_id.clone(),
                session_id: session.id.clone(),
                status: "running".into(),
                title: session.label.clone(),
                ..DesktopPane::default()
            })
        })
        .collect();
    if panes.is_empty() {
        return facts;
    }
    let tree = row(panes.iter().map(|p| p.pane_id.as_str()));
    let desktop = attn_protocol::Desktop {
        id: ROW.to_owned(),
        name: if rune::Platform::current() == rune::Platform::Mac {
            "This Mac"
        } else {
            "This machine"
        }
        .into(),
        active_pane_id: panes.first().map(|p| p.pane_id.clone()).unwrap_or_default(),
        panes,
        tree_json: tree,
        ..attn_protocol::Desktop::default()
    };
    facts.sessions = Arc::new(sessions);
    facts.desktops = Arc::new(vec![Desktop::from(desktop)]);
    facts.profiles = Arc::new(attn_client::Profiles {
        all: vec![Profile {
            current_desktop_id: ROW.to_owned(),
            ..Profile::default()
        }],
        selected: String::new(),
    });
    facts
}

/// What the sidebar says it waits for while it shows this machine's row.
pub(crate) fn waiting(facts: &Facts) -> Option<String> {
    if !shows(facts) {
        return None;
    }
    let home = match &facts.connection {
        attn_client::Connection::HomeUnreachable(away) => away.home.as_str(),
        _ => "the home",
    };
    Some(format!("Waiting for {home}: desktops, queue, garden"))
}

/// The tree of `panes` side by side, as the daemon writes a row.
fn row<'a>(panes: impl DoubleEndedIterator<Item = &'a str>) -> String {
    let mut columns = panes
        .rev()
        .map(|id| serde_json::json!({"type": "pane", "pane_id": id}));
    let Some(mut tree) = columns.next() else {
        return String::new();
    };
    for column in columns {
        let id = format!(
            "vertical:{}",
            column["pane_id"].as_str().unwrap_or_default()
        );
        tree = serde_json::json!({"type": "split", "split_id": id, "direction": "vertical",
            "ratio": 0.5, "children": [column, tree]});
    }
    tree.to_string()
}
