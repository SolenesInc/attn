//! Settings → Paired senders: the programs paired to send as this profile, each with when it
//! paired and last sent, and Remove.

use attn_client::{Connection, Slices};
use attn_protocol::{PairedSender, SenderUnpairMessage, SendersGetMessage};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, List, Notice, Prose, Section, Status};
use attn_ui::system::{Button, ListRow, Reveal, Size};
use rune::Tx;

use crate::State;
use crate::sidebar::queue::epoch_ms;

/// Why the last Remove failed, and the sender it was pressed on.
#[derive(Default)]
pub(crate) struct Senders {
    refused: Option<Refused>,
}

struct Refused {
    sender: String,
    why: String,
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
        state.window.settings.senders.refused = None;
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
    state.window.settings.senders.refused = None;
    if state
        .requests
        .waits_for(|c| c["cmd"] == "sender_unpair" && c["sender"] == id)
    {
        return;
    }
    let (sender, pressed) = (id.to_owned(), id.to_owned());
    state.ask(
        |request_id| SenderUnpairMessage {
            sender,
            request_id: Some(request_id),
            ..SenderUnpairMessage::new()
        },
        move |state, answer, _| {
            state.window.settings.senders.refused = answer.outcome().err().map(|why| Refused {
                sender: pressed,
                why,
            });
        },
        tx,
    );
}

fn when(at: &str) -> String {
    epoch_ms(at).map_or_else(|| at.to_owned(), crate::calendar::date_time)
}

fn paired_sender(sender: &PairedSender, t: &Tokens) -> ListRow {
    let used = sender.last_used_at.as_deref().map_or_else(
        || "Never used".to_owned(),
        |at| format!("Last used {}", when(at)),
    );
    let copy = format!("Paired {} · {used}", when(&sender.created_at));
    let key = format!("settings.sender.{}", sender.id);
    let id = sender.id.clone();
    ListRow::new(key.clone(), sender.name.clone())
        .still()
        .wrap()
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
        let refused = state.window.settings.senders.refused.as_ref();
        let under = |sender: &PairedSender| match refused {
            Some(refused) if refused.sender == sender.id => vec![refusal(refused, t)],
            _ => Vec::new(),
        };
        section.push(
            senders
                .iter()
                .fold(
                    List::new("settings.senders.list", "Paired senders"),
                    |list, sender| list.row_with(paired_sender(sender, t), under(sender)),
                )
                .build(t),
        )
    };
    if let Some(refused) = state.window.settings.senders.refused.as_ref()
        && !senders.iter().any(|s| s.id == refused.sender)
    {
        section = section.push(refusal(refused, t));
    }
    vec![section.build(t)]
}

fn refusal(refused: &Refused, t: &Tokens) -> Block {
    Notice::error("settings.senders.error", refused.why.clone()).build(t)
}
