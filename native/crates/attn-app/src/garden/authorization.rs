use attn_protocol::{Seed, SeedAuthorization, SeedAuthorizationMessage};
use attn_ui::blocks::{Block, Note, Refusal, Section, Stack, rail};
use attn_ui::system::{Type, words};
use rune::{El, KeyEvent, Tx};

use super::model;
use super::sheet::{Sheet, words_field};
use super::spot::{Sends, Spot};
use super::view::quiet;
use crate::State;

impl Sheet<'_> {
    /// What the user lets agents do on this seed beyond the project's guidance, who set it and
    /// whether its tender has been told, with the controls that set, change and clear it.
    pub(super) fn authorization(&self, here: &Seed) -> Block {
        let t = self.t;
        let spot = Spot::new(&self.prefix, &here.id, "authorization");
        let shown = self.garden.shown(&spot);
        let authorization = here.authorization.as_ref();
        let written = authorization.map_or("", |a| a.text.as_str()).to_owned();
        let mut section: Section = self.section("authorization", "Authorization");
        if !shown.writing {
            let opened = (spot.clone(), written.clone());
            let edit: El = quiet(if written.is_empty() { "Set" } else { "Change" })
                .disabled(shown.busy)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    opened.0.write(state, &opened.1, tx)
                })
                .build(t)
                .key(spot.key("edit"))
                .into();
            section = section.control(edit);
            if !written.is_empty() {
                let cleared = spot.clone();
                let clear: El = quiet("Clear")
                    .disabled(shown.busy)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        authorize(&cleared, state, None, tx)
                    })
                    .build(t)
                    .key(spot.key("clear"))
                    .into();
                section = section.control(clear);
            }
        }
        if !written.is_empty() {
            section = section.push(Block::custom(rail(
                words(written.clone(), Type::Body, t.text_primary)
                    .key(spot.key("text"))
                    .wrap(),
                true,
                t,
            )));
        } else if !shown.writing {
            section = section.push(
                Note::new(
                    self.key("authorization.empty"),
                    "No authorization from the user for this task.",
                )
                .build(t),
            );
        }
        if let Some(authorization) = authorization {
            let said = self.said(authorization);
            section = section.push(Block::custom(
                words(said, Type::Small, t.text_tertiary)
                    .key(spot.key("meta"))
                    .wrap(),
            ));
        }
        if let Some(written_now) = shown.words {
            let (typed, edited) = (spot.clone(), spot.clone());
            let field = words_field(
                written_now,
                spot.field(),
                &format!(
                    "Authorize {}: the steps agents take beyond the project's guidance, and where they stop",
                    here.id
                ),
                "Land on main once the checks pass. Ask me before releasing.",
                move |state: &mut State| edited.words_mut(state),
                move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                    let home = typed.key("edit");
                    let had = !written.is_empty();
                    typed.field_key(
                        state,
                        ev,
                        (&home, Sends::Return),
                        |state, words, tx| commit(&typed, state, words, had, tx),
                        tx,
                    )
                },
                t,
            );
            section = section.push(Block::custom(
                Stack::tight().w_full().child(field).child(
                    words(
                        "⏎ save · ⇧⏎ new line · esc cancel",
                        Type::Small,
                        t.text_tertiary,
                    )
                    .a11y_hidden(),
                ),
            ));
        }
        if let Some(failure) = shown.failure {
            section = section.push(Block::custom(
                Refusal::new(spot.key("error"), failure.to_owned()).build(t),
            ));
        }
        section.build(t)
    }

    /// Who set the authorization or cleared it, when, and whether its tender has it yet.
    fn said(&self, authorization: &SeedAuthorization) -> String {
        let by = if authorization.set_by.r#ref == "user" {
            "you".to_owned()
        } else {
            model::who_name(
                &authorization.set_by.r#ref,
                &authorization.set_by.name,
                self.names,
            )
        };
        let mut said = format!(
            "{} by {by} {}",
            if authorization.text.is_empty() {
                "cleared"
            } else {
                "set"
            },
            model::ago(&authorization.set_at, self.now_ms)
        );
        if let Some(update) = &authorization.update {
            let recipient =
                model::who_name(&update.recipient.r#ref, &update.recipient.name, self.names);
            said.push_str(" · ");
            said.push_str(&match (&update.typed_at, update.needs_approval) {
                (Some(at), _) => {
                    format!("typed into {recipient} {}", model::ago(at, self.now_ms))
                }
                (None, Some(true)) => format!("waiting for {recipient}, which needs approval"),
                (None, _) => format!("waiting for {recipient} to take input"),
            });
        }
        said
    }
}

fn commit(spot: &Spot, state: &mut State, words: String, had: bool, tx: &mut Tx) {
    if words.is_empty() {
        spot.fail(
            state,
            if had {
                "Write the new instructions, or press Clear to remove the authorization."
            } else {
                "Write the steps agents take on this task beyond the project's guidance, and where they stop."
            }
            .to_owned(),
        );
        return;
    }
    authorize(spot, state, Some(words), tx);
}

/// Sets the seed's authorization to `words`, or clears it.
fn authorize(spot: &Spot, state: &mut State, words: Option<String>, tx: &mut Tx) {
    let seed_id = spot.seed().to_owned();
    let clear = words.is_none().then_some(true);
    spot.send(
        state,
        |request_id| SeedAuthorizationMessage {
            seed_id,
            text: words,
            clear,
            request_id: Some(request_id),
            ..SeedAuthorizationMessage::new()
        },
        spot.key("edit"),
        tx,
    );
}
