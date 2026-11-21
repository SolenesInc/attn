use attn_client::Refusal;
use attn_ui::Tokens;
use attn_ui::blocks::{Banner, BannerTone, Stack};
use attn_ui::system::{Part, Said, Type, words};
use rune::{Cx, El};

pub(crate) fn connecting(t: &Tokens) -> El {
    words("Connecting to the daemon", Type::Body, t.text_secondary)
        .key("connection.connecting")
        .role("status")
}

pub(crate) fn reconnecting<S>(t: &Tokens, cx: &Cx<S>, start_failed: Option<&str>) -> El {
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
    Banner::new("connection", "Daemon connection", tone)
        .announces("connection.reconnecting", status)
        .build(t, cx)
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
