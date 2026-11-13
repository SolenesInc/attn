use attn_client::Refusal;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, col, text};

pub(crate) fn connecting(t: &Tokens) -> El {
    text("Connecting to the daemon")
        .key("connection.connecting")
        .role("status")
        .font(type_::BODY)
        .color(t.text_secondary)
}

pub(crate) fn reconnecting(t: &Tokens, start_failed: Option<&str>) -> El {
    let status = match start_failed {
        None => "The daemon is not answering. Reconnecting.".to_string(),
        Some(reason) => {
            format!("The daemon is not answering, and starting it failed: {reason}. Retrying.")
        }
    };
    text(status)
        .key("connection.reconnecting")
        .role("status")
        .wrap()
        .font(type_::BODY)
        .color(t.text_secondary)
        .padding(space::S2)
}

pub(crate) fn refused(t: &Tokens, refusal: &Refusal) -> El {
    let (title, detail) = explain(refusal);
    col()
        .key("connection.refused")
        .role("alert")
        .label(format!("{title}. {detail}"))
        .max_w(520.0)
        .gap(space::S2)
        .child(text(title).font(type_::TITLE).color(t.text_primary))
        .child(
            text(detail)
                .wrap()
                .font(type_::BODY)
                .color(t.text_secondary),
        )
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
    }
}
