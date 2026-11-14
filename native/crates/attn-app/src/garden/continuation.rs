use attn_protocol::{
    Seed, SeedDocument, SeedResumeMessage, SeedResumeResultMessage, SeedSendToChiefMessage,
};
use rune::tok::{space, type_};
use rune::{El, KeyEvent, Tx, col, row, text, text_input};

use super::model;
use super::sheet::Sheet;
use super::spot::{Sends, Spot};
use super::view::kit_button;
use crate::State;
use crate::requests::Refused;

impl Sheet<'_> {
    fn chief_spot(&self, here: &Seed) -> Spot {
        Spot::new(&self.prefix, &here.id, "chief")
    }

    /// Resume, Handover and Send to Chief, while the seed is open and the daemon's reading of it is the
    /// seed the garden holds: Resume when the seed's agent can come back, naming the outpost it
    /// runs on, Handover when its last agent left a continuation, and Send to Chief while the
    /// profile has a Chief; neither of the last two while a note to the next agent or the Chief
    /// is being written.
    pub(super) fn continuation(
        &self,
        held: Option<&Seed>,
        document: Option<&SeedDocument>,
    ) -> Vec<El> {
        let Some(seed) = document.map(|d| &d.seed) else {
            return Vec::new();
        };
        if held.is_some_and(|h| h.rev != seed.rev) || model::is_closed(seed) {
            return Vec::new();
        }
        let kit = attn_ui::kit(self.t);
        let mut actions = Vec::new();
        if let Some(continuation) = &seed.continuation {
            let unreachable =
                continuation.directory_state == "unreachable" && !continuation.resume_available;
            let outpost = continuation.outpost.as_deref();
            if continuation.resume_available || (outpost.is_some() && unreachable) {
                let id = seed.id.clone();
                let button = match outpost {
                    Some(outpost) => kit_button(format!("Resume on {outpost}")).label_with({
                        let words = format!("Resume · {outpost}");
                        move |font, ink| text(words).font(font).color(ink)
                    }),
                    None => kit_button("Resume"),
                };
                let button = match continuation
                    .resume_reason
                    .as_deref()
                    .filter(|_| unreachable)
                {
                    Some(reason) => button.disabled(true).help(reason),
                    None => button,
                };
                actions.push(
                    button
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| resume(state, &id, tx))
                        .build(kit)
                        .key(self.key("resume")),
                );
            }
        }
        if self.composing(seed) {
            return actions;
        }
        actions.extend(self.handover_button(seed));
        let chief = self.chief_spot(seed);
        if self.chief {
            let opened = chief.clone();
            actions.push(
                kit_button("Send to Chief")
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| opened.write(state, "", tx))
                    .build(kit)
                    .key(chief.key("open")),
            );
        }
        actions
    }

    /// The note to the Chief being written, under the seed's head.
    pub(super) fn chief_form(&self, document: Option<&SeedDocument>) -> Option<El> {
        let seed = &document?.seed;
        let spot = self.chief_spot(seed);
        let shown = self.garden.shown(&spot);
        let written = shown.words.filter(|_| self.chief)?;
        let t = self.t;
        let kit = attn_ui::kit(t);
        let rev = seed.rev;
        let (typed, edited) = (spot.clone(), spot.clone());
        let mut form = col()
            .key(spot.key("form"))
            .role("form")
            .label("Send to Chief")
            .w_full()
            .gap(space::S1)
            .padding(space::S2)
            .radius(6.0)
            .fill(t.bg_elevated)
            .border(1.0, t.border)
            .child(
                text("What should Chief know? (optional)")
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .a11y_hidden(),
            )
            .child(
                text_input(written, type_::BODY)
                    .key(spot.field())
                    .label("What should Chief know? (optional)")
                    .on_edit(move |state: &mut State| edited.words_mut(state))
                    .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                        let home = typed.key("open");
                        typed.field_key(
                            state,
                            ev,
                            (&home, Sends::CmdReturn),
                            |state, words, tx| send_to_chief(&typed, state, rev, words, tx),
                            tx,
                        )
                    })
                    .w_full()
                    .min_h(56.0)
                    .color(t.text_primary)
                    .fill(t.bg_input)
                    .border(1.0, t.border_focus)
                    .radius(4.0)
                    .padding(space::S1),
            );
        if let Some(failure) = shown.failure {
            form = form.child(
                text(failure.to_owned())
                    .key(spot.key("error"))
                    .role("alert")
                    .label(failure.to_owned())
                    .font(type_::LABEL)
                    .color(t.error)
                    .wrap(),
            );
        }
        let actions = if shown.busy {
            row().child(
                text("Sending to Chief…")
                    .role("status")
                    .label("Sending to Chief…")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
        } else {
            let (cancelled, sent) = (spot.clone(), spot.clone());
            row()
                .gap(space::S1)
                .child(
                    kit_button("Cancel")
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            cancelled.put_away(state, &cancelled.key("open"), tx)
                        })
                        .build(kit)
                        .key(spot.key("cancel")),
                )
                .child(
                    kit_button("Send to Chief")
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            let words = sent.words(state);
                            send_to_chief(&sent, state, rev, words, tx)
                        })
                        .build(kit)
                        .key(spot.key("send")),
                )
        };
        Some(form.child(actions))
    }
}

fn send_to_chief(spot: &Spot, state: &mut State, expected_rev: i64, words: String, tx: &mut Tx) {
    let seed_id = spot.seed().to_owned();
    spot.send(
        state,
        |request_id| SeedSendToChiefMessage {
            seed_id,
            expected_rev,
            guidance: (!words.is_empty()).then_some(words),
            request_id: Some(request_id),
            ..SeedSendToChiefMessage::new()
        },
        spot.key("open"),
        tx,
    );
}

/// Asks the daemon to bring the seed's agent back, and goes to the session it answers with.
fn resume(state: &mut State, id: &str, tx: &mut Tx) {
    let seed_id = id.to_owned();
    state.act(
        |request_id| SeedResumeMessage {
            seed_id,
            request_id: Some(request_id),
            ..SeedResumeMessage::new()
        },
        Refused::Bare,
        |state, reply, tx| {
            let session = reply
                .parse::<SeedResumeResultMessage>()
                .and_then(|r| r.session_id);
            if let Some(session) = session {
                crate::sidebar::open_agent(state, &session, tx);
            }
        },
        tx,
    );
}
