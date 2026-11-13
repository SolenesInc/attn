use attn_protocol::{Seed, SeedAuthorization, SeedAuthorizationMessage};
use rune::tok::{space, type_};
use rune::{El, KeyEvent, Tx, col, row, text, text_input};

use super::model;
use super::sheet::{Sheet, meta_word};
use super::spot::Spot;
use super::view::kit_button;
use crate::State;

impl Sheet<'_> {
    /// What the user lets agents do on this seed beyond the project's guidance, who set it and
    /// whether its tender has been told, with the controls that set, change and clear it.
    pub(super) fn authorization(&self, here: &Seed) -> El {
        let t = self.t;
        let kit = attn_ui::kit(t);
        let spot = Spot::new(&self.prefix, &here.id, "authorization");
        let shown = self.garden.shown(&spot);
        let authorization = here.authorization.as_ref();
        let words = authorization.map_or("", |a| a.text.as_str()).to_owned();
        let mut head = row().w_full().items_center().gap(space::S2).child(
            text("Authorization")
                .role("heading")
                .label("Authorization")
                .font(type_::SECTION)
                .color(t.text_tertiary)
                .grow(1.0),
        );
        if !shown.writing {
            let opened = (spot.clone(), words.clone());
            head = head.child(
                kit_button(if words.is_empty() { "Set" } else { "Change" })
                    .disabled(shown.busy)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        opened.0.write(state, &opened.1, tx)
                    })
                    .build(kit)
                    .key(spot.key("edit")),
            );
            if !words.is_empty() {
                let cleared = spot.clone();
                head = head.child(
                    kit_button("Clear")
                        .disabled(shown.busy)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            authorize(&cleared, state, None, tx)
                        })
                        .build(kit)
                        .key(spot.key("clear")),
                );
            }
        }
        let mut section = col()
            .key(self.key("authorization"))
            .role("region")
            .label("Authorization")
            .w_full()
            .gap(space::S1)
            .pt(space::S3)
            .child(head);
        if !words.is_empty() {
            section = section.child(
                text(words.clone())
                    .key(spot.key("text"))
                    .label(words.clone())
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .wrap(),
            );
        } else if !shown.writing {
            section = section.child(self.note(
                "authorization.empty",
                "No authorization from the user for this task.",
            ));
        }
        if let Some(authorization) = authorization {
            let said = self.said(authorization);
            section = section.child(
                text(said.clone())
                    .key(spot.key("meta"))
                    .label(said)
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .wrap(),
            );
        }
        if shown.writing {
            let typed = spot.clone();
            section = section
                .child(
                    text_input(&self.garden.words, type_::BODY)
                        .key(spot.field())
                        .label(format!(
                            "Authorize {}: the steps agents take beyond the project's guidance, and where they stop",
                            here.id
                        ))
                        .placeholder(
                            "Land on main once the checks pass. Ask me before releasing.",
                            t.text_muted,
                        )
                        .on_edit(|state: &mut State| {
                            let garden = &mut state.window.garden;
                            garden.revision += 1;
                            &mut garden.words
                        })
                        .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                            let home = typed.key("edit");
                            let had = !words.is_empty();
                            typed.field_key(
                                state,
                                ev,
                                &home,
                                |state, words, tx| commit(&typed, state, words, had, tx),
                                tx,
                            )
                        })
                        .w_full()
                        .min_h(56.0)
                        .color(t.text_primary)
                        .fill(t.bg_input)
                        .border(1.0, t.accent)
                        .radius(4.0)
                        .padding(space::S1),
                )
                .child(meta_word("⏎ save · ⇧⏎ new line · esc cancel", t).a11y_hidden());
        }
        if let Some(failure) = shown.failure {
            section = section.child(
                text(failure.to_owned())
                    .key(spot.key("error"))
                    .role("alert")
                    .label(failure.to_owned())
                    .font(type_::LABEL)
                    .color(t.error)
                    .wrap(),
            );
        }
        section
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
