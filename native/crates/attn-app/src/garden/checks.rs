use attn_protocol::{
    ActorView, Seed, SeedCheck, SeedCheckEvidence, SeedCheckMessage, SeedDocument, SeedSharedCheck,
};
use attn_ui::GlyphButton;
use attn_ui::blocks::{Block, Inline, Refusal, Stack, rail};
use attn_ui::system::{FieldState, Type, line_box, words};
use rune::{El, KeyEvent, Tx};
use rune_kit::TextInput;

use super::CheckFocus;
use super::model::{self, SessionNames};
use super::sheet::Sheet;
use super::spot::{Sends, Spot};
use super::view::quiet;
use crate::State;

fn box_glyph(state: &str) -> &'static str {
    match state {
        "reported" => "?",
        "confirmed" => "✓",
        "rejected" => attn_ui::glyph::TIMES,
        _ => "",
    }
}

/// How wide a check's box is: a glyph button's side, so a removed check's mark lines up with it.
const CHECK_BOX: f32 = 22.0;

fn judgeable(state: &str) -> bool {
    !matches!(state, "confirmed" | "rejected")
}

fn author(by: &ActorView, names: &SessionNames) -> String {
    if by.r#ref == "user" {
        return "you".into();
    }
    model::who_name(&by.r#ref, &by.name, names)
}

fn short_commit(commit: &str) -> &str {
    commit.get(..7).unwrap_or(commit)
}

/// The words a piece of evidence shows: its artifact, or its link without the scheme.
fn evidence_label(evidence: &attn_protocol::SeedCheckEvidence) -> String {
    if let Some(artifact) = &evidence.artifact {
        return artifact.clone();
    }
    let url = evidence.url.clone().unwrap_or_default();
    if let Some(number) = url
        .split_once("github.com/")
        .and_then(|(_, rest)| rest.splitn(3, '/').nth(2))
        .and_then(|rest| rest.strip_prefix("pull/"))
        .map(|rest| rest.split('/').next().unwrap_or(rest))
    {
        return format!("PR {number}");
    }
    let bare = url.split_once("://").map_or(url.as_str(), |(_, rest)| rest);
    bare.trim_start_matches("www.")
        .trim_end_matches('/')
        .to_owned()
}

impl Sheet<'_> {
    /// The seed's checks, as the daemon's reading of the seed the garden holds has them, each
    /// with the moves that judge it, then the checks it shares with every child, then the
    /// removed ones while shown.
    pub(super) fn checks(&self, here: &Seed, document: Option<&SeedDocument>) -> Option<Block> {
        let here = document
            .map(|d| &d.seed)
            .filter(|s| s.id == here.id && s.rev == here.rev)
            .unwrap_or(here);
        let checks: Vec<&SeedCheck> = here.checks.iter().flatten().collect();
        let shared: Vec<&SeedSharedCheck> = here.shared_checks.iter().flatten().collect();
        if checks.is_empty() && shared.is_empty() {
            return None;
        }
        let t = self.t;
        let mut list = self.section("checks", "Checks").reachable();
        if let Some(tally) = model::tally_of(checks.iter().copied()) {
            list = list.control(
                words(model::tally_words(&tally), Type::Small, t.text_tertiary)
                    .key(self.key("checks.tally"))
                    .label(format!("Checks: {}", model::tally_sentence(&tally))),
            );
        }
        let live = checks.iter().filter(|c| !c.removed);
        list = list.push_all(live.map(|c| {
            Block::custom(
                self.key(&format!("check.{}.block", c.id)),
                self.check_row(here, document, c),
            )
        }));
        let live_shared: Vec<_> = shared.iter().filter(|c| !c.removed).collect();
        if !live_shared.is_empty() {
            list = list
                .push(Block::custom(
                    self.key("checks.shared"),
                    words("Shared by every child", Type::Label, t.text_tertiary),
                ))
                .push_all(live_shared.into_iter().map(|c| {
                    Block::custom(
                        self.key(&format!("shared.{}.block", c.id)),
                        self.shared_row(c, document),
                    )
                }));
        }
        let removed = checks.iter().filter(|c| c.removed).count()
            + shared.iter().filter(|c| c.removed).count();
        if removed > 0 {
            let fold = self.key(&format!("checks.removed.{}", here.id));
            let open = self.garden.folds.contains(&fold);
            let toggled = fold.clone();
            list = list.push(Block::control(
                self.key("checks.removed.block"),
                quiet(format!("{removed} removed"))
                    .expanded(open)
                    .on_press_tx(move |state: &mut State, _: &mut Tx| toggle_fold(state, &toggled))
                    .build(t)
                    .key(self.key("checks.removed")),
            ));
            if open {
                list = list
                    .push_all(checks.iter().filter(|c| c.removed).map(|c| {
                        Block::custom(
                            self.key(&format!("check.{}.block", c.id)),
                            self.check_row(here, document, c),
                        )
                    }))
                    .push_all(shared.iter().filter(|c| c.removed).map(|c| {
                        Block::custom(
                            self.key(&format!("shared.{}.block", c.id)),
                            self.shared_row(c, document),
                        )
                    }));
            }
        }
        Some(list.build(t))
    }

    fn check_row(&self, here: &Seed, document: Option<&SeedDocument>, check: &SeedCheck) -> El {
        let t = self.t;
        let kit = attn_ui::kit(t);
        let spot = Spot::new(&self.prefix, &here.id, &format!("check.{}", check.id));
        let shown = self.garden.shown(&spot);
        let (busy, rejecting) = (shown.busy, shown.words);
        let state = check.state.as_str();
        let mark = if check.removed {
            words(box_glyph(state), Type::Label, t.text_secondary)
                .label(state.to_owned())
                .min_w(CHECK_BOX)
        } else {
            let confirm = judgeable(state);
            let (verb, word) = if confirm {
                ("confirm", "Confirm")
            } else {
                ("uncheck", "Uncheck")
            };
            let pressed = spot.clone();
            GlyphButton::new(box_glyph(state), format!("{word} {}", check.id))
                .help(format!(
                    "{state}: {}",
                    if confirm {
                        "confirm it"
                    } else {
                        "clear the verdict"
                    }
                ))
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    judge(&pressed, state, verb, None, tx)
                })
                .build(kit)
                .key(spot.key("box"))
                .border(1.0, t.border)
        };
        let mut line = Inline::top()
            .child(words(check.id.clone(), Type::Mono, t.text_tertiary))
            .child(
                words(check.text.clone(), Type::Body, t.text_primary)
                    .wrap()
                    .grow(1.0)
                    .min_w(0.0),
            );
        if let Some(from) = &check.from {
            line = line.child(words(format!("from {from}"), Type::Small, t.text_tertiary));
        }
        if check.removed {
            line = line.child(words("removed", Type::Small, t.text_tertiary));
        }
        let mut main = Stack::tight().grow(1.0).min_w(0.0).child(line);
        if let Some(report) = &check.report {
            let mut words = format!("report: {}", report.summary);
            if report.detail == Some(true) {
                words.push_str(" · detail in its note");
            }
            for evidence in &report.evidence {
                words.push_str(&format!(" · {}", evidence_label(evidence)));
                if evidence.changed_since_report == Some(true) {
                    words.push_str(" (changed since this report)");
                }
            }
            if let Some(commit) = &report.commit {
                words.push_str(&format!(" · @{}", short_commit(commit)));
            }
            words.push_str(&format!(" · {}", author(&report.by, self.names)));
            main = main.child(
                attn_ui::system::words(words, Type::Small, t.text_secondary)
                    .key(self.key(&format!("check.{}.report", check.id)))
                    .wrap(),
            );
            if !report.evidence.is_empty() {
                main = main.child(self.evidence(here, document, &check.id, &report.evidence));
            }
        }
        if let Some(verdict) = &check.verdict {
            let mut words = format!(
                "{} by {}",
                if verdict.confirmed {
                    "confirmed"
                } else {
                    "rejected"
                },
                author(&verdict.by, self.names)
            );
            if let Some(reason) = verdict.reason.as_ref().filter(|r| !r.is_empty()) {
                words.push_str(&format!(": {reason}"));
            }
            if check.report.is_none() {
                words.push_str(" · no report");
            }
            main = main.child(
                attn_ui::system::words(
                    words,
                    Type::Small,
                    if verdict.confirmed {
                        t.success
                    } else {
                        t.error
                    },
                )
                .key(self.key(&format!("check.{}.verdict", check.id)))
                .wrap(),
            );
        }
        if let Some(written) = rejecting {
            let (typed, edited) = (spot.clone(), spot.clone());
            let input = TextInput::new(written)
                .key(spot.field())
                .label(format!("Reject {}: what would prove it", check.id))
                .placeholder("what would prove it")
                .on_edit(move |state: &mut State| edited.words_mut(state))
                .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                    let home = typed.key("box");
                    typed.field_key(
                        state,
                        ev,
                        (&home, Sends::Return),
                        |s, w, tx| reject(&typed, s, w, tx),
                        tx,
                    )
                });
            main = main.child(El::from(line_box(input, FieldState::Ready, t)));
        }
        if let Some(failure) = shown.failure.map(str::to_owned) {
            main = main.child(
                Refusal::new(self.key(&format!("check.{}.error", check.id)), failure).build(t),
            );
        }
        let mut actions = Inline::tight();
        if !check.removed && rejecting.is_none() {
            let action = |word: &'static str, verb: &'static str, name: String, part: &str| {
                let pressed = spot.clone();
                El::from(
                    quiet(word)
                        .disabled(busy)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| match verb {
                            "reject" => pressed.write(state, "", tx),
                            verb => judge(&pressed, state, verb, None, tx),
                        })
                        .build(t)
                        .key(self.key(&format!("check.{}.{part}", check.id)))
                        .label(name),
                )
            };
            if state == "rejected" {
                actions = actions.child(action("Uncheck", "uncheck", "Uncheck".into(), "uncheck"));
            }
            if check.report.is_some() {
                actions = actions.child(action(
                    "Reopen",
                    "reopen",
                    format!("Reopen {}", check.id),
                    "reopen",
                ));
            }
            if judgeable(state) {
                actions = actions.child(action(
                    "Reject…",
                    "reject",
                    format!("Reject {}", check.id),
                    "reject-start",
                ));
            }
        }
        Inline::top()
            .key(self.key(&format!("check.{}", check.id)))
            .role("listitem")
            .label(format!("{} {} · {}", check.id, check.text, check.state))
            .child(mark)
            .child(main)
            .child(actions)
    }

    fn shared_row(&self, check: &SeedSharedCheck, document: Option<&SeedDocument>) -> El {
        let t = self.t;
        let key = self.key(&format!("shared.{}", check.id));
        let open = self.garden.folds.contains(&key);
        let rollup = model::tally_states(check.seeds.iter().map(|s| s.state.as_str()))
            .map(|tally| model::tally_sentence(&tally))
            .unwrap_or_default();
        let toggled = key.clone();
        let mut item = Stack::tight().w_full().child(
            quiet(format!("{} {} · {rollup}", check.id, check.text))
                .expanded(open)
                .on_press_tx(move |state: &mut State, _: &mut Tx| toggle_fold(state, &toggled))
                .build(t)
                .key(key.clone())
                .into(),
        );
        if open {
            let mut seeds = Stack::tight()
                .key(format!("{key}.seeds"))
                .role("list")
                .label(format!("Seeds that prove {}", check.id));
            for entry in &check.seeds {
                let held = self
                    .seeds
                    .iter()
                    .map(|s| s.as_ref())
                    .find(|s| s.id == entry.seed_id)
                    .or_else(|| {
                        document.and_then(|doc| doc.children.iter().find(|s| s.id == entry.seed_id))
                    })
                    .or_else(|| self.terminals.resolve_seed(&entry.seed_id));
                let title = held.map_or(entry.seed_id.as_str(), |s| s.title.as_str());
                let status = held.map_or(entry.seed_id.as_str(), |s| s.status.as_str());
                let (go, id) = (self.go.clone(), entry.seed_id.clone());
                let focus = (
                    self.prefix.clone(),
                    Spot::new(&self.prefix, &entry.seed_id, &format!("check.{}", check.id))
                        .key("box"),
                );
                let mark = box_glyph(&entry.state);
                seeds = seeds.child(
                    quiet(format!("{mark} {title} · {status}"))
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            state.window.garden.check_focus = Some(CheckFocus {
                                sheet: focus.0.clone(),
                                seed: id.clone(),
                                check_box: focus.1.clone(),
                            });
                            go.to(state, &id, tx)
                        })
                        .build(t)
                        .key(format!("{key}.{}", entry.seed_id))
                        .label(format!("{title} · {status} · {}", entry.state))
                        .into(),
                );
            }
            item = item.child(rail(seeds, false, t));
        }
        item
    }
}

fn toggle_fold(state: &mut State, key: &str) {
    let garden = &mut state.window.garden;
    if !garden.folds.remove(key) {
        garden.folds.insert(key.to_owned());
    }
    garden.revision += 1;
}

/// Sends `verb` for the check at `spot`, whose keyboard home is its box.
fn judge(spot: &Spot, state: &mut State, verb: &'static str, summary: Option<String>, tx: &mut Tx) {
    let (seed_id, check_id) = (spot.seed().to_owned(), check_of(spot));
    spot.send(
        state,
        |request_id| SeedCheckMessage {
            seed_id,
            check_id: Some(check_id),
            verb: verb.into(),
            summary,
            request_id: Some(request_id),
            ..SeedCheckMessage::new()
        },
        spot.key("box"),
        tx,
    );
}

fn check_of(spot: &Spot) -> String {
    spot.part().trim_start_matches("check.").to_owned()
}

fn reject(spot: &Spot, state: &mut State, reason: String, tx: &mut Tx) {
    if reason.is_empty() {
        let check = check_of(spot);
        spot.fail(
            state,
            format!("rejecting {check} needs a reason: say what would prove it"),
        );
        return;
    }
    judge(spot, state, "reject", Some(reason), tx);
}

impl Sheet<'_> {
    /// A report's evidence as controls, each opening where the Artifacts section opens it: a
    /// kept document opens and another kept file shows in the file manager, a link opens in the
    /// browser and a linked file in a markdown tile. Evidence that names nothing the seed holds
    /// stays words in the report.
    fn evidence(
        &self,
        here: &Seed,
        document: Option<&SeedDocument>,
        check: &str,
        evidence: &[SeedCheckEvidence],
    ) -> El {
        let mut shown = Inline::tight();
        for (n, piece) in evidence.iter().enumerate() {
            let Some(opens) = super::artifacts::opens(piece, document) else {
                continue;
            };
            let seed = here.id.clone();
            shown = shown.child(
                quiet(evidence_label(piece))
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        super::artifacts::open_evidence(state, &seed, &opens, tx)
                    })
                    .build(self.t)
                    .key(self.key(&format!("check.{check}.evidence.{n}")))
                    .into(),
            );
        }
        shown
    }
}
