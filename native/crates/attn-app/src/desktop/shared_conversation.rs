use attn_client::Desktop;
use attn_protocol::Session;
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Notice};

/// Where each session whose conversation another terminal also has open says it is open, from
/// the desktop that shows the session.
pub(super) fn notices(
    sessions: &[Session],
    desktops: &[Desktop],
    viewing: &str,
) -> Vec<(String, String)> {
    sessions
        .iter()
        .filter_map(|s| {
            let peers = s
                .conversation_shared_with
                .as_ref()
                .filter(|p| !p.is_empty())?;
            let open_in: Vec<String> = peers
                .iter()
                .map(|peer| where_open(peer, sessions, desktops, viewing))
                .collect();
            Some((
                s.id.clone(),
                format!("This conversation is also open in {}.", open_in.join(", ")),
            ))
        })
        .collect()
}

fn where_open(peer: &str, sessions: &[Session], desktops: &[Desktop], viewing: &str) -> String {
    let named = sessions
        .iter()
        .find(|s| s.id == peer)
        .map(|s| s.label.as_str())
        .filter(|l| !l.is_empty())
        .map_or_else(|| "another terminal".to_owned(), |l| format!("“{l}”"));
    let home = desktops.iter().find(|d| {
        d.panes
            .iter()
            .any(|p| p.kind == "agent" && p.session_id == peer)
    });
    match home {
        None => named,
        Some(d) if d.id == viewing => format!("{named} on this desktop"),
        Some(d) => format!(
            "{named} on {}",
            crate::sidebar::model::desktop_title(d, desktops)
        ),
    }
}

/// What says over the top of a session's terminal that another terminal has its conversation
/// open.
pub(super) fn notice(session: &str, lead: &str, t: &Tokens) -> Block {
    Notice::warn(
        format!("session.{session}.shared-conversation"),
        format!(
            "{lead} Two agent processes on one conversation split its history. Close one of them."
        ),
    )
    .build(t)
}
