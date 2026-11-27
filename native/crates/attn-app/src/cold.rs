//! The window before the home has ever answered, on a machine whose daemon relays the app to a
//! home elsewhere: this machine's own terminals, live in one row, which the app derives from
//! its link to this machine's daemon. The row is no desktop: the app keeps no layout for it and
//! sends nothing the home owns while it shows, and the home's desktops replace it once the home
//! answers.

use std::sync::Arc;

use attn_client::{Desktop, Facts};
use attn_protocol::{DesktopPane, Profile};

/// The id the row goes by, which no desktop of the home has.
pub(crate) const ROW: &str = "";

/// Whether the window shows this machine's row: the home has not answered yet, and this
/// machine's own daemon has.
pub(crate) fn shows(facts: &Facts) -> bool {
    facts.daemon_instance.is_empty() && facts.machine.home.is_some() && facts.machine.connected
}

/// What the window reads while it shows this machine's row: the facts, with this machine's
/// sessions in place of the home's, and the row as the one desktop.
pub(crate) fn view(mut facts: Facts) -> Facts {
    if !shows(&facts) {
        return facts;
    }
    let machine = facts.machine.clone();
    let here = Some(machine.id.clone());
    let sessions: Vec<_> = machine
        .sessions
        .iter()
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
                .find(|b| b.session_id == session.id)?;
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
