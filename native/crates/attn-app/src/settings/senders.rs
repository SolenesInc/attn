//! Settings → Paired senders: the programs paired to send as this profile, each with when it
//! paired and last sent, and Remove.

use attn_client::{Connection, Slices};
use attn_protocol::{PairedSender, SenderUnpairMessage, SendersGetMessage};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};
use rune_kit::Button;

use super::pages::{CAPTION, block, warning};
use crate::State;
use crate::sidebar::queue::epoch_ms;

/// Why the last Remove failed.
#[derive(Default)]
pub(crate) struct Senders {
    error: Option<String>,
}

fn shown(state: &State) -> bool {
    super::is_open(state) && state.window.settings.section == super::Section::Senders
}

fn read(state: &mut State) {
    let _ = state.daemon.send(&SendersGetMessage::new());
}

/// Reads the senders when the page comes into view, starting without the error it last
/// showed.
pub(super) fn arrive(state: &mut State) {
    if shown(state) {
        state.window.settings.senders.error = None;
        read(state);
    }
}

/// Reads the senders again when the connection comes back under the page.
pub(super) fn follow(state: &mut State, changed: Slices) {
    if changed.intersects(Slices::CONNECTION)
        && state.facts.connection == Connection::Connected
        && shown(state)
    {
        read(state);
    }
}

/// Unpairs a sender, once however often it is pressed before the daemon answers.
fn remove(state: &mut State, id: &str, tx: &mut Tx) {
    state.window.settings.senders.error = None;
    if state
        .requests
        .waits_for(|c| c["cmd"] == "sender_unpair" && c["sender"] == id)
    {
        return;
    }
    let sender = id.to_owned();
    state.ask(
        |request_id| SenderUnpairMessage {
            sender,
            request_id: Some(request_id),
            ..SenderUnpairMessage::new()
        },
        |state, answer, _| state.window.settings.senders.error = answer.outcome().err(),
        tx,
    );
}

fn when(at: &str) -> String {
    epoch_ms(at).map_or_else(|| at.to_owned(), crate::calendar::date_time)
}

fn paired_sender(sender: &PairedSender, t: &Tokens) -> El {
    let used = sender.last_used_at.as_deref().map_or_else(
        || "Never used".to_owned(),
        |at| format!("Last used {}", when(at)),
    );
    let copy = format!("Paired {} · {used}", when(&sender.created_at));
    let key = format!("settings.sender.{}", sender.id);
    let id = sender.id.clone();
    row()
        .key(key.clone())
        .role("listitem")
        .label(sender.name.clone())
        .w_full()
        .gap(space::S3)
        .items_center()
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(space::S1)
                .child(
                    text(sender.name.clone())
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .child(
                    text(copy.clone())
                        .key(format!("{key}.copy"))
                        .label(copy)
                        .font(CAPTION)
                        .color(t.text_tertiary),
                ),
        )
        .child(
            Button::new("Remove")
                .on_press_tx(move |state: &mut State, tx: &mut Tx| remove(state, &id, tx))
                .build(attn_ui::kit(t))
                .key(format!("{key}.remove")),
        )
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let Some(senders) = state.facts.senders.as_ref() else {
        return vec![
            text("Loading paired senders…")
                .key("settings.senders.loading")
                .font(type_::BODY)
                .color(t.text_tertiary),
        ];
    };
    let mut body = Vec::new();
    if senders.is_empty() {
        let said = "No paired senders. A program pairs with attn send pair --name <name>.";
        body.push(
            text(said)
                .key("settings.senders.empty")
                .label(said)
                .font(type_::BODY)
                .color(t.text_tertiary)
                .wrap(),
        );
    } else {
        body.push(
            col()
                .key("settings.senders.list")
                .role("list")
                .label("Paired senders")
                .w_full()
                .gap(space::S2)
                .children(senders.iter().map(|s| paired_sender(s, t))),
        );
    }
    if let Some(error) = &state.window.settings.senders.error {
        body.push(warning("settings.senders.error".into(), error.clone(), t));
    }
    vec![block(
        "senders",
        "Paired senders",
        "A removed sender is refused at once and must pair again to send.",
        t,
        body,
    )]
}
