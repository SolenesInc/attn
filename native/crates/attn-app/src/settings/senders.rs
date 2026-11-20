//! Settings → Paired senders: the programs paired to send as this profile, each with when it
//! paired and last sent, and Remove.

use attn_client::{Connection, Slices};
use attn_protocol::{PairedSender, SenderUnpairMessage, SendersGetMessage};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, List, Notice, Prose, Section, Status};
use attn_ui::system::{Button, ListRow, Reveal, Size};
use rune::{El, Tx};

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
    ListRow::new(key.clone(), sender.name.clone())
        .still()
        .title(sender.name.clone())
        .subtitle_keyed(format!("{key}.copy"), copy, t)
        .controls(
            [Button::new("Remove")
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| remove(state, &id, tx))
                .build(t)
                .key(format!("{key}.remove"))
                .into()],
            Reveal::Always,
        )
        .build(t)
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let Some(senders) = state.facts.senders.as_ref() else {
        return vec![
            Status::new("settings.senders.loading", "Loading paired senders…", true).build(t),
        ];
    };
    let mut section = Section::new("settings.block.senders", "Paired senders")
        .description("A removed sender is refused at once and must pair again to send.");
    section = if senders.is_empty() {
        section.push(
            Prose::empty(
                "settings.senders.empty",
                "No paired senders. A program pairs with `attn send pair --name <name>`.",
            )
            .build(t),
        )
    } else {
        section.push(
            List::new("settings.senders.list", "Paired senders")
                .rows(senders.iter().map(|s| paired_sender(s, t)))
                .build(t),
        )
    };
    if let Some(error) = &state.window.settings.senders.error {
        section = section.push(Notice::warn("settings.senders.error", error.clone()).build(t));
    }
    vec![section.build(t)]
}
