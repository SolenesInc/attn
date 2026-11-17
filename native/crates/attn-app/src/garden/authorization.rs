use attn_protocol::{Seed, SeedAuthorization, SeedAuthorizationMessage};
use attn_ui::type_;
use rune::tok::space;
use rune::{El, KeyEvent, Tx, row, text, text_input};

use super::model;
use super::sheet::{Sheet, meta_word, page_heading, page_region};
use super::spot::{Sends, Spot};
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
        let mut head = row()
            .w_full()
            .items_center()
            .gap(10.0)
            .child(page_heading("Authorization", t));
        let action = |label: &'static str| {
            let ink = t.text_muted;
            kit_button(label)
                .disabled(shown.busy)
                .variant(rune_kit::ButtonVariant::Ghost)
                .label_with(move |_, _| text(label).font(type_::CAPTION).color(ink))
        };
        let quiet = |button: El| {
            button
                .h(18.0)
                .px(6.0)
                .radius(4.0)
                .fill(rune::Color::TRANSPARENT)
                .hover(t.bg_button)
        };
        if !shown.writing {
            let opened = (spot.clone(), words.clone());
            let mut actions = row().gap(4.0).child(quiet(
                action(if words.is_empty() { "Set" } else { "Change" })
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        opened.0.write(state, &opened.1, tx)
                    })
                    .build(kit)
                    .key(spot.key("edit")),
            ));
            if !words.is_empty() {
                let cleared = spot.clone();
                actions = actions.child(quiet(
                    action("Clear")
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            authorize(&cleared, state, None, tx)
                        })
                        .build(kit)
                        .key(spot.key("clear")),
                ));
            }
            head = head.child(actions);
        }
        let mut section = page_region(&self.key("authorization"), "Authorization", t)
            .gap(6.0)
            .child(head);
        if !words.is_empty() {
            section = section.child(
                text(words.clone())
                    .key(spot.key("text"))
                    .label(words.clone())
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .wrap()
                    .mx(2.0)
                    .pl(9.0)
                    .border_sides([0.0, 0.0, 0.0, 2.0], t.brand),
            );
        } else if !shown.writing {
            section = section.child(
                super::sheet::quiet_note(
                    &self.key("authorization.empty"),
                    "No authorization from the user for this task.",
                    t.text_dimmed,
                )
                .mx(2.0),
            );
        }
        if let Some(authorization) = authorization {
            let said = self.said(authorization);
            section = section.child(
                text(said.clone())
                    .key(spot.key("meta"))
                    .label(said)
                    .font(type_::CAPTION)
                    .color(t.text_dimmed)
                    .wrap()
                    .mx(2.0),
            );
        }
        if let Some(written) = shown.words {
            let (typed, edited) = (spot.clone(), spot.clone());
            section = section
                .child(
                    text_input(written, type_::BODY)
                        .key(spot.field())
                        .label(format!(
                            "Authorize {}: the steps agents take beyond the project's guidance, and where they stop",
                            here.id
                        ))
                        .placeholder(
                            "Land on main once the checks pass. Ask me before releasing.",
                            t.text_muted,
                        )
                        .on_edit(move |state: &mut State| edited.words_mut(state))
                        .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                            let home = typed.key("edit");
                            let had = !words.is_empty();
                            typed.field_key(
                                state,
                                ev,
                                (&home, Sends::Return),
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
