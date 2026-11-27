use attn_client::{Connection, Facts, HomeUnreachable, Refusal};
use attn_protocol::RetryHomeMessage;
use attn_ui::Tokens;
use attn_ui::blocks::{Banner, BannerTone, Stack};
use attn_ui::system::{Button, Look, Part, Said, Size, Type, words};
use rune::{Cx, El};

use crate::State;

pub(crate) fn connecting(t: &Tokens) -> El {
    words("Connecting to the daemon", Type::Body, t.text_secondary)
        .key("connection.connecting")
        .role("status")
}

/// Whether the window shows the desk: while connected, and while the daemon or the home is
/// away after the window had the home's state, which it keeps showing.
pub(crate) fn shows_desk(facts: &Facts) -> bool {
    match facts.connection {
        Connection::Connected | Connection::Reconnecting { .. } => true,
        Connection::HomeUnreachable(_) => {
            !facts.daemon_instance.is_empty() || crate::cold::shows(facts)
        }
        Connection::Connecting | Connection::Refused(_) => false,
    }
}

pub(crate) fn banner(state: &State, t: &Tokens, cx: &Cx<State>) -> Option<El> {
    let banner = said(state)?.details(crate::shell::opens_details(crate::shell::Band::Connection));
    Some(banner.build(t, cx))
}

/// The message the banner shows: why the daemon is not answering.
pub(crate) fn identity(state: &State) -> Option<String> {
    if let Some(away) = lost_home(state) {
        return Some(format!("{}|{}|{:?}", away.home, away.dial, away.reason));
    }
    start_failed(state).map(|why| why.unwrap_or_default().to_owned())
}

fn lost_home(state: &State) -> Option<&HomeUnreachable> {
    match &state.facts.connection {
        Connection::HomeUnreachable(away) if shows_desk(&state.facts) => Some(away),
        _ => None,
    }
}

fn start_failed(state: &State) -> Option<Option<&str>> {
    match &state.facts.connection {
        Connection::Reconnecting { start_failed } => Some(start_failed.as_deref()),
        _ => None,
    }
}

pub(crate) fn said(state: &State) -> Option<Banner> {
    if let Some(away) = lost_home(state) {
        let status = match away.reason.as_deref() {
            Some(reason) if away.dial == "refused" => {
                format!("{} refused this machine: {reason}", away.home)
            }
            Some(reason) if crate::cold::shows(&state.facts) => {
                format!("Can’t reach {}: {reason}. Dialing again.", away.home)
            }
            None if crate::cold::shows(&state.facts) => {
                format!("Can’t reach {}. Dialing again.", away.home)
            }
            Some(reason) => format!("Lost {}: {reason}. Reconnecting.", away.home),
            None => format!("Lost {}. Reconnecting.", away.home),
        };
        return Some(
            Banner::new("connection", "Home connection", BannerTone::Warn)
                .announces("connection.home-lost", status)
                .action(
                    "connection.home-lost.retry",
                    Button::new("Retry"),
                    |state: &mut State, _: &mut rune::Tx| retry_home(state),
                ),
        );
    }
    let start_failed = start_failed(state)?;
    let tone = if start_failed.is_some() {
        BannerTone::Bad
    } else {
        BannerTone::Warn
    };
    let status = match start_failed {
        None => Said::from("The daemon is not answering. Reconnecting."),
        Some(reason) => {
            let failed = "The daemon is not answering, and starting it failed: ";
            match reason.strip_suffix(crate::daemon::INSTALL) {
                Some(before) => Said::parts([
                    Part::Text(format!("{failed}{before}")),
                    Part::Code(crate::daemon::INSTALL.to_owned()),
                    Part::Text(". Retrying.".to_owned()),
                ]),
                None => Said::from(format!("{failed}{reason}. Retrying.")),
            }
        }
    };
    Some(
        Banner::new("connection", "Daemon connection", tone)
            .announces("connection.reconnecting", status),
    )
}

pub(crate) fn refused(t: &Tokens, refusal: &Refusal) -> El {
    let (title, detail) = explain(refusal);
    Stack::tight()
        .key("connection.refused")
        .role("alert")
        .label(format!("{title}. {detail}"))
        .max_w(520.0)
        .child(words(title, Type::Heading, t.text_primary))
        .child(words(detail, Type::Body, t.text_secondary).wrap())
}

fn explain(refusal: &Refusal) -> (&'static str, String) {
    match refusal {
        Refusal::ProtocolMismatch { app, daemon } => (
            "This app and the daemon speak different protocols",
            format!(
                "The app speaks protocol {app} and the daemon speaks {daemon}. Restart the daemon, or reinstall attn so both come from one build."
            ),
        ),
        Refusal::InstanceMismatch { app, daemon } => (
            "This daemon serves another instance",
            format!(
                "This app belongs to instance \"{app}\", but the daemon on its port serves instance \"{daemon}\". Open the app for \"{daemon}\", or restart the daemon with ATTN_INSTANCE={app}."
            ),
        ),
        Refusal::Unauthorized(reason) => ("The daemon refused this app", reason.clone()),
        Refusal::MigrationFailed(marker) => (
            "attn seems broken after migration",
            format!(
                "A data migration failed at startup; its details are in {}.",
                marker.path
            ),
        ),
    }
}

/// Whether the app has the home's live state, so the home's popped-out tiles and title bar
/// controls show.
pub(crate) fn shows_desktops(connection: &Connection) -> bool {
    matches!(
        connection,
        Connection::Connected | Connection::Reconnecting { .. }
    )
}

/// What the app shows while the daemon on its port cannot reach the home it passes the app to:
/// the home, why, and a way to dial it now. The link connects again by itself once the dial
/// changes.
pub(crate) fn home_unreachable(t: &Tokens, away: &HomeUnreachable) -> El {
    let title = format!("Can’t reach {}", away.home);
    let detail = match (away.dial.as_str(), away.reason.as_deref()) {
        ("refused", Some(reason)) => format!("{} refused this machine: {reason}", away.home),
        (_, Some(reason)) => format!("{reason}. Dialing again."),
        ("refused", None) => format!("{} refused this machine.", away.home),
        (_, None) => "Dialing again.".to_owned(),
    };
    let retry = Button::new("Retry")
        .look(Look::Primary)
        .size(Size::Compact)
        .on_press(retry_home)
        .build(t)
        .key("connection.home-unreachable.retry");
    Stack::tight()
        .key("connection.home-unreachable")
        .role("alert")
        .label(format!("{title}. {detail}"))
        .max_w(520.0)
        .child(words(title, Type::Heading, t.text_primary))
        .child(
            words(detail, Type::Body, t.text_secondary)
                .wrap()
                .selectable(true),
        )
        .child(El::from(retry))
}

/// Asks the daemon on the app's port to dial its home now; it closes the connection once it
/// has, and the link connects again.
pub(crate) fn retry_home(state: &mut State) {
    let _ = state.daemon.send(&RetryHomeMessage::new());
}
