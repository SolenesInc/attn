use attn_client::{Connection, Facts, HomeUnreachable, Refusal};
use attn_protocol::{RetryHomeMessage, UpdateRun};
use attn_ui::Tokens;
use attn_ui::blocks::{Banner, BannerTone, Stack};
use attn_ui::system::{Button, Look, Part, Said, Size, Type, words};
use rune::{Cx, El, Tx};

use crate::State;
use crate::update_run;

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
        if update_explains(state, away) {
            return None;
        }
        return Some(format!("{}|{}|{:?}", away.home, away.dial, away.reason));
    }
    start_failed(state).map(|why| why.unwrap_or_default().to_owned())
}

pub(crate) fn lost_home(state: &State) -> Option<&HomeUnreachable> {
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

/// Whether the home refused this machine's build while the update band says so in the user's
/// words and offers the fix, so this band would only repeat it with the daemons' ids.
fn update_explains(state: &State, away: &HomeUnreachable) -> bool {
    refused_for_build(away) && crate::update_run::shows_home_update(state)
}

fn refused_for_build(away: &HomeUnreachable) -> bool {
    away.dial == "refused" && away.refusal.as_deref() == Some("build")
}

pub(crate) fn said(state: &State) -> Option<Banner> {
    if let Some(away) = lost_home(state) {
        if update_explains(state, away) {
            return None;
        }
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
pub(crate) fn home_unreachable(
    t: &Tokens,
    away: &HomeUnreachable,
    run: Option<&UpdateRun>,
    notice: Option<&str>,
    moved_to: Option<&str>,
    rollback_failed: Option<&str>,
) -> El {
    let title = format!("Can’t reach {}", away.home);
    let says = run.and_then(update_run::home_says);
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
    let detail = (!refused_for_build(away) || says.is_none()).then_some(detail);
    let label = match &detail {
        Some(detail) => format!("{title}. {detail}"),
        None => title.clone(),
    };
    let mut screen = Stack::tight()
        .key("connection.home-unreachable")
        .role("alert")
        .label(label)
        .max_w(520.0)
        .child(words(title, Type::Heading, t.text_primary))
        .children(detail.map(|detail| {
            words(detail, Type::Body, t.text_secondary)
                .wrap()
                .selectable(true)
        }))
        .child(El::from(retry));
    if let Some(says) = says {
        screen = screen.child(
            words(says.head, Type::Body, t.text_primary)
                .key("connection.home-unreachable.update")
                .wrap(),
        );
        if let Some(note) = says.note {
            screen = screen.child(
                words(note, Type::Body, t.text_secondary)
                    .key("connection.home-unreachable.update-note")
                    .wrap()
                    .selectable(true),
            );
        }
        if let Some(action) = says.action {
            screen = screen.child(El::from(
                update_run::action_button(&action)
                    .size(Size::Compact)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        update_run::run_action(&action, state, tx)
                    })
                    .build(t)
                    .key("connection.home-unreachable.update-action"),
            ));
        }
    }
    if let Some(to) = moved_to {
        let mut moved = Stack::tight()
            .key("connection.home-unreachable.move")
            .child(words(rollback_said(to), Type::Body, t.text_secondary).wrap())
            .child(El::from(
                Button::new(format!(
                    "Roll back to {}",
                    crate::settings::machines::this_machine()
                ))
                .size(Size::Compact)
                .on_press_tx(crate::settings::machines::roll_back)
                .build(t)
                .key("connection.home-unreachable.roll-back"),
            ));
        if let Some(why) = rollback_failed {
            moved = moved.child(
                words(why.to_owned(), Type::Body, t.error)
                    .key("connection.home-unreachable.roll-back-failed")
                    .wrap()
                    .selectable(true),
            );
        }
        screen = screen.child(moved);
    }
    if let Some(notice) = notice {
        screen = screen.child(
            words(notice.to_owned(), Type::Body, t.text_secondary)
                .key("connection.home-unreachable.notice")
                .wrap()
                .selectable(true),
        );
    }
    screen
}

/// What a rollback of the move to `to` does, for a machine that has not reached `to` since.
pub(crate) fn rollback_said(to: &str) -> String {
    format!(
        "The home moved to {to}, and this machine has not reached it since. Roll back makes this machine the home again; {to} sets aside what it did as the home."
    )
}

/// Asks the daemon on the app's port to dial its home now; it closes the connection once it
/// has, and the link connects again.
pub(crate) fn retry_home(state: &mut State) {
    let _ = state.daemon.send(&RetryHomeMessage::new());
}
