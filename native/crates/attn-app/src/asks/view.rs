use attn_protocol::{AskAnswer, AskQuestion, Session};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Side, Tx, col, el, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Markdown, Switch};

use super::{Chip, Item, Kind, Lists, questions};
use crate::State;
use crate::desktop::header_pop::{self, Pop, What};
use crate::sidebar::queue;

pub(super) const SNOOZE: &str = "asks.snooze";
const OPEN_SEED: &str = "asks.open-seed";
const LIST: &str = "asks.list";
const WIDTH: f32 = 360.0;

pub(super) fn choice_key(question: &str, i: usize) -> String {
    format!("asks.q.{question}.choice.{i}")
}

pub(super) fn other_key(question: &str) -> String {
    format!("asks.q.{question}.other.field")
}

pub(super) fn text_key(question: &str) -> String {
    format!("asks.q.{question}.text.field")
}

const NOTE: &str = "asks.note.field";

fn send_chord() -> &'static str {
    if rune::KeyPlatform::current() == rune::KeyPlatform::Mac {
        "⌘↩"
    } else {
        "Ctrl+↩"
    }
}

/// The drawer: the asks it lists, and the one it shows, with the questions to answer.
pub(crate) fn drawer(state: &State, t: &Tokens) -> El {
    let lists = Lists::of(&state.facts, state.window.now_ms);
    let live = lists.live.len();
    let replied = lists.replied.len();
    let asks = &state.window.asks;
    let items = lists.shown(asks.show_replied);
    let shown = state.shown_ask().map(|(id, _)| id);
    let frame = col()
        .key(super::DRAWER)
        .role("region")
        .label("Asks")
        .focusable(true)
        .on_key(drawer_key)
        .w(WIDTH)
        .h_full()
        .min_h(0.0)
        .fill(t.bg_panel)
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
        .child(header(live, replied, asks.show_replied, t));
    let Some(at) = shown.and_then(|id| items.iter().position(|i| i.ask.id == id)) else {
        return frame.child(empty(replied, t));
    };
    frame
        .child(list(&items, &items[at].ask.id, state.window.now_ms, t))
        .child(article(state, &items[at], t))
        .child(footer(state, &items[at], items.len() > 1, t))
}

fn header(live: usize, replied: usize, show_replied: bool, t: &Tokens) -> El {
    let replied_label = if replied > 0 {
        format!("Replied {replied}")
    } else {
        "Replied".to_owned()
    };
    row()
        .key("asks.header")
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S3)
        .py(space::S2)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            text("Asks")
                .font(type_::SECTION)
                .color(t.text_primary)
                .grow(1.0),
        )
        .children((live > 0).then(|| {
            text(live.to_string())
                .key("asks.count")
                .role("status")
                .label(format!("{live} open"))
                .font(type_::LABEL)
                .color(t.accent)
        }))
        .child(
            Switch::new(show_replied)
                .label(replied_label)
                .control_size(ControlSize::Mini)
                .on_toggle_tx(toggle_replied)
                .build(attn_ui::kit(t))
                .key("asks.replied"),
        )
        .child(
            attn_ui::GlyphButton::new(attn_ui::icon::CLOSE, "Close asks")
                .on_press_tx(super::close)
                .build(attn_ui::kit(t))
                .key("asks.close"),
        )
}

fn empty(replied: usize, t: &Tokens) -> El {
    let replies = match replied {
        0 => None,
        1 => Some("r shows the one you replied to in the last day".to_owned()),
        n => Some(format!("r shows the {n} you replied to in the last day")),
    };
    col()
        .key("asks.empty")
        .w_full()
        .padding(space::S3)
        .gap(space::S2)
        .child(
            text("No open asks.")
                .font(type_::BODY)
                .color(t.text_primary),
        )
        .child(
            text("When an agent needs a decision from you, its ask lands here.")
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .wrap(),
        )
        .children(replies.map(|r| text(r).font(type_::LABEL).color(t.text_tertiary)))
}

fn list(items: &[Item<'_>], shown: &str, now_ms: i64, t: &Tokens) -> El {
    let replied = items.iter().any(|i| i.ask.reply.is_some());
    let mut rows = Vec::new();
    for (at, item) in items.iter().enumerate() {
        let before = at.checked_sub(1).map(|b| &items[b]);
        if item.snoozed_until.is_some() && before.is_none_or(|b| b.snoozed_until.is_none()) {
            rows.push(divider("Snoozed", t));
        }
        if item.ask.reply.is_some() && before.is_none_or(|b| b.ask.reply.is_none()) {
            rows.push(divider("Replied in the last day", t));
        }
        rows.push(list_row(item, item.ask.id == shown, now_ms, t));
    }
    col()
        .key(LIST)
        .role("listbox")
        .label(if replied {
            "Open and replied asks"
        } else {
            "Open asks"
        })
        .w_full()
        .max_h(180.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .py(space::S1)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .children(rows)
}

fn divider(words: &'static str, t: &Tokens) -> El {
    text(words)
        .role("listitem")
        .label(words)
        .font(type_::LABEL)
        .color(t.text_tertiary)
        .px(space::S3)
        .pt(space::S2)
}

fn list_row(item: &Item<'_>, selected: bool, now_ms: i64, t: &Tokens) -> El {
    let ask = item.ask;
    let mut words = vec![item.from.clone(), ask.subject.clone()];
    if item.questions > 1 {
        words.push(format!("{} questions", item.questions));
    }
    if let Some(until) = item.snoozed_until {
        words.push(format!(
            "until {}",
            queue::wake_time_short(Some(until), now_ms)
        ));
    }
    let waiting = ask.reply.is_none()
        && item.snoozed_until.is_none()
        && item.holder.is_some_and(|s| s.turn_owed == Some(true));
    if waiting {
        words.push("Waiting on you".into());
    }
    if ask.reply.is_some() {
        words.push("replied".into());
    }
    let age = queue::turn_age(Some(super::age_from(ask)), now_ms);
    let dimmed = ask.reply.is_some() || item.snoozed_until.is_some();
    let id = ask.id.clone();
    let tags: Vec<String> = words[2..].to_vec();
    row()
        .key(format!("asks.row.{}", ask.id))
        .role("option")
        .label(format!("{} {age}", words.join(" · ")))
        .a11y_value(if selected { "current" } else { "" })
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S3)
        .py(space::S1)
        .fill(if selected {
            t.bg_selected
        } else {
            rune::Color::TRANSPARENT
        })
        .hover(t.bg_elevated)
        .opacity(if dimmed { 0.6 } else { 1.0 })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            state.window.asks.selected = Some(id.clone());
            state.window.asks.error = None;
            super::follow(state, tx);
        })
        .child(
            text(item.from.clone())
                .font(type_::LABEL)
                .color(t.text_secondary)
                .single_line()
                .truncate()
                .max_w(110.0),
        )
        .child(
            text(ask.subject.clone())
                .font(type_::LABEL)
                .color(t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0)
                .min_w(0.0),
        )
        .children(tags.into_iter().map(|tag| {
            let waiting = tag == "Waiting on you";
            if waiting {
                el().w(6.0).h(6.0).radius(3.0).fill(t.accent)
            } else {
                text(tag).font(type_::LABEL).color(t.text_tertiary)
            }
        }))
        .child(text(age).font(type_::MONO_CAPTION).color(t.text_tertiary))
}

fn article(state: &State, item: &Item<'_>, t: &Tokens) -> El {
    let ask = item.ask;
    let replied = ask.reply.is_some();
    let kit = attn_ui::kit(t);
    let snoozable = item
        .holder
        .filter(|s| !super::offline(s, &state.facts.outposts));
    let snoozed = snoozable
        .is_some_and(|s| queue::is_snoozed(s.turn_snoozed_until.as_deref(), state.window.now_ms));
    let from = row()
        .w_full()
        .items_center()
        .gap(space::S2)
        .children(item.holder.map(|s| {
            crate::sidebar::state_mark("asks.from", &crate::sidebar::AgentRow::of(s, false), t)
        }))
        .child(
            text(item.from.clone())
                .key("asks.from")
                .font(type_::LABEL)
                .color(t.text_secondary)
                .grow(1.0)
                .min_w(0.0)
                .single_line()
                .truncate(),
        )
        .children(item.holder.map(|s| open_agent(s, &item.from, t)))
        .children(snoozable.map(|_| snooze_button(snoozed, &item.from, t)));
    let seed = ask.seed_id.as_ref().map(|seed| {
        let id = seed.clone();
        row()
            .key("asks.seed")
            .w_full()
            .items_center()
            .gap(space::S2)
            .child(
                text(ask.seed_slug.clone().unwrap_or_else(|| seed.clone()))
                    .font(type_::MONO_CAPTION)
                    .color(t.text_tertiary),
            )
            .child(
                text(ask.seed_title.clone().unwrap_or_default())
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .grow(1.0)
                    .min_w(0.0)
                    .single_line()
                    .truncate(),
            )
            .child(
                small_button(
                    OPEN_SEED,
                    "Open in Garden",
                    "Open the seed in the garden (g)",
                    t,
                )
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::open_seed(state, &id, tx)
                }),
            )
    });
    let draft = state.window.asks.drafts.get(&ask.id);
    let reply_answers = ask.reply.as_ref().map(|r| r.answers.as_slice());
    let mut index = 0;
    let parts = ask
        .parts
        .iter()
        .enumerate()
        .map(|(at, part)| match &part.question {
            Some(question) => {
                index += 1;
                let answer = match reply_answers {
                    Some(answers) => Shown::Replied(answers.iter().find(|a| a.key == question.key)),
                    None => Shown::Draft(draft.and_then(|d| d.answers.get(&question.key))),
                };
                question_card(&ask.id, index, question, answer, kit, t)
            }
            None => col()
                .key(format!("asks.part.{at}"))
                .w_full()
                .child(Markdown::new(part.markdown.clone().unwrap_or_default()).build(kit)),
        });
    let note_placeholder = if item.holder.is_some() || ask.seed_id.is_none() {
        format!("Anything else for {}?", item.from)
    } else {
        "Anything else for its next tender?".to_owned()
    };
    let note = match &ask.reply {
        Some(reply) => reply
            .note
            .as_ref()
            .filter(|n| !n.is_empty())
            .map(|n| replied_text("asks.note".into(), "Note", n, t)),
        None => {
            let edit = draft.map(|d| &d.note);
            let id = ask.id.clone();
            Some(
                field(edit, NOTE, "Note", &note_placeholder, t)
                    .on_edit(move |state: &mut State| state.note(&id)),
            )
        }
    };
    let error = state
        .window
        .asks
        .error
        .as_ref()
        .filter(|(id, _)| *id == ask.id)
        .map(|(_, why)| {
            text(why.clone())
                .key("asks.error")
                .role("alert")
                .label(why.clone())
                .font(type_::LABEL)
                .color(t.error)
                .wrap()
        });
    col()
        .key("asks.ask")
        .role("article")
        .label(ask.subject.clone())
        .a11y_value(if replied { "replied" } else { "" })
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .padding(space::S3)
        .gap(space::S3)
        .child(from)
        .children(seed)
        .child(
            text(ask.subject.clone())
                .role("heading")
                .font(type_::TITLE)
                .color(t.text_primary)
                .wrap(),
        )
        .children(parts)
        .children(note)
        .children(error)
}

fn open_agent(session: &Session, from: &str, t: &Tokens) -> El {
    let id = session.id.clone();
    small_button(
        "asks.open-agent",
        "Open agent",
        &format!("Open {from} (o)"),
        t,
    )
    .on_press_tx(move |state: &mut State, tx: &mut Tx| super::open_agent(state, &id, tx))
}

fn snooze_button(snoozed: bool, from: &str, t: &Tokens) -> El {
    let (words, hint) = if snoozed {
        ("Wake", format!("Wake {from} (s)"))
    } else {
        ("Snooze", format!("Snooze {from} and its asks (s)"))
    };
    small_button(SNOOZE, words, &hint, t).on_press_tx(super::toggle_snooze)
}

fn small_button(key: &str, words: &str, hint: &str, t: &Tokens) -> El {
    let button = Button::new(words).control_size(ControlSize::Small);
    let button = if hint.is_empty() {
        button
    } else {
        button.help(hint)
    };
    button.build(attn_ui::kit(t)).key(key.to_owned())
}

enum Shown<'a> {
    Draft(Option<&'a super::Answer>),
    Replied(Option<&'a AskAnswer>),
}

impl Shown<'_> {
    fn picked(&self, label: &str) -> bool {
        match self {
            Shown::Draft(a) => a.is_some_and(|a| a.picked.iter().any(|p| p == label)),
            Shown::Replied(a) => a.is_some_and(|a| a.picked.iter().any(|p| p == label)),
        }
    }
}

fn question_card(
    ask: &str,
    index: usize,
    question: &AskQuestion,
    answer: Shown<'_>,
    kit: &rune_kit::Kit,
    t: &Tokens,
) -> El {
    let kind = Kind::of(question);
    let prompt = row()
        .w_full()
        .gap(space::S2)
        .child(
            text(index.to_string())
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary),
        )
        .child(
            text(question.prompt.clone())
                .font(type_::BODY)
                .color(t.text_primary)
                .wrap()
                .grow(1.0)
                .min_w(0.0),
        );
    let context = question
        .context
        .as_ref()
        .filter(|c| !c.is_empty())
        .map(|c| Markdown::new(c.clone()).build(kit).w_full());
    let body = match kind {
        Kind::Text => written(ask, question, &answer, t),
        Kind::One | Kind::Any => choices(ask, question, kind, &answer, t),
    };
    col()
        .key(format!("asks.q.{}", question.key))
        .role("group")
        .label(question.prompt.clone())
        .w_full()
        .gap(space::S2)
        .padding(space::S2)
        .radius(6.0)
        .border(1.0, t.border_subtle)
        .child(prompt)
        .children(context)
        .child(body)
}

fn written(ask: &str, question: &AskQuestion, answer: &Shown<'_>, t: &Tokens) -> El {
    let key = question.key.clone();
    match answer {
        Shown::Replied(answer) => replied_text(
            format!("asks.q.{key}.text"),
            &question.prompt,
            answer.and_then(|a| a.text.as_deref()).unwrap_or_default(),
            t,
        ),
        Shown::Draft(draft) => {
            let edit = draft.map(|a| &a.text);
            let empty = edit.is_none_or(|e| e.text.trim().is_empty());
            let placeholder = match &question.recommended {
                Some(r) if !r.is_empty() => format!("Suggested: {r}"),
                _ => "Your answer…".to_owned(),
            };
            let (ask_id, q) = (ask.to_owned(), key.clone());
            let field = field(edit, &text_key(&key), &question.prompt, &placeholder, t)
                .on_edit(move |state: &mut State| &mut state.answer(&ask_id, &q).text);
            let suggestion = question
                .recommended
                .clone()
                .filter(|r| !r.is_empty() && empty)
                .map(|r| {
                    let (ask_id, q) = (ask.to_owned(), key.clone());
                    small_button(
                        &format!("asks.q.{key}.suggestion"),
                        "Use the suggestion",
                        "",
                        t,
                    )
                    .on_press(move |state: &mut State| {
                        state.answer(&ask_id, &q).text = rune::TextEdit::new(r.clone());
                    })
                });
            col()
                .w_full()
                .gap(space::S1)
                .child(field)
                .children(suggestion)
        }
    }
}

fn choices(ask: &str, question: &AskQuestion, kind: Kind, answer: &Shown<'_>, t: &Tokens) -> El {
    let replied = matches!(answer, Shown::Replied(_));
    let rows = question.choices.iter().enumerate().map(|(i, choice)| {
        let picked = answer.picked(&choice.label);
        let recommended = question.recommended.as_deref() == Some(choice.label.as_str());
        let (ask_id, q, label) = (ask.to_owned(), question.key.clone(), choice.label.clone());
        let mark = match (kind, picked) {
            (Kind::One, true) => attn_ui::icon::CIRCLE_DOT,
            (Kind::One, false) => attn_ui::icon::CIRCLE_OPEN,
            (_, true) => attn_ui::icon::CHECKED,
            (_, false) => attn_ui::icon::UNCHECKED,
        };
        let el =
            row()
                .key(choice_key(&question.key, i))
                .role(if kind == Kind::One {
                    "radio"
                } else {
                    "checkbox"
                })
                .label(choice.label.clone())
                .checked(picked)
                .disabled(replied)
                .focusable(!replied)
                .w_full()
                .gap(space::S2)
                .px(space::S2)
                .py(space::S1)
                .radius(4.0)
                .fill(if picked {
                    t.bg_selected
                } else {
                    rune::Color::TRANSPARENT
                })
                .hover(t.bg_elevated)
                .child(text(mark).font(type_::BODY).color(t.text_secondary))
                .child(
                    col()
                        .grow(1.0)
                        .min_w(0.0)
                        .child(
                            row()
                                .gap(space::S2)
                                .child(
                                    text(choice.label.clone())
                                        .font(type_::BODY)
                                        .color(t.text_primary),
                                )
                                .children(recommended.then(|| {
                                    text("recommended").font(type_::LABEL).color(t.accent)
                                })),
                        )
                        .children(
                            choice
                                .description
                                .clone()
                                .filter(|d| !d.is_empty())
                                .map(|d| text(d).font(type_::LABEL).color(t.text_tertiary).wrap()),
                        ),
                );
        if replied {
            el
        } else {
            el.on_press(move |state: &mut State| super::pick(state, &ask_id, &q, kind, &label))
        }
    });
    let label = format!("Other answer to {}", question.prompt);
    let other = match answer {
        Shown::Replied(a) => a
            .and_then(|a| a.other.as_deref())
            .filter(|o| !o.is_empty())
            .map(|o| replied_text(format!("asks.q.{}.other", question.key), &label, o, t)),
        Shown::Draft(draft) => {
            let (ask_id, q) = (ask.to_owned(), question.key.clone());
            Some(
                field(
                    draft.map(|a| &a.other),
                    &other_key(&question.key),
                    &label,
                    "Other…",
                    t,
                )
                .single_line()
                .on_edit({
                    let (ask_id, q) = (ask_id.clone(), q.clone());
                    move |state: &mut State| &mut state.answer(&ask_id, &q).other
                })
                .on_input_change(move |state: &mut State, _: &mut Tx| {
                    super::other_edited(state, &ask_id, &q, kind)
                }),
            )
        }
    };
    col()
        .key(format!("asks.q.{}.choices", question.key))
        .role(if kind == Kind::One {
            "radiogroup"
        } else {
            "group"
        })
        .label(question.prompt.clone())
        .w_full()
        .gap(2.0)
        .children(rows)
        .children(other)
}

fn replied_text(key: String, label: &str, value: &str, t: &Tokens) -> El {
    let shown = if value.is_empty() {
        "Not answered"
    } else {
        value
    };
    row()
        .key(key)
        .role("group")
        .label(label.to_owned())
        .a11y_value(shown.to_owned())
        .w_full()
        .child(
            text(shown.to_owned())
                .font(type_::BODY)
                .color(if value.is_empty() {
                    t.text_muted
                } else {
                    t.text_secondary
                })
                .wrap(),
        )
}

fn field(
    edit: Option<&rune::TextEdit>,
    key: &str,
    label: &str,
    placeholder: &str,
    t: &Tokens,
) -> El {
    let empty = rune::TextEdit::default();
    text_input(edit.unwrap_or(&empty), type_::BODY)
        .key(key.to_owned())
        .label(label.to_owned())
        .placeholder(placeholder.to_owned(), t.text_muted)
        .w_full()
        .min_h(28.0)
        .padding(space::S2)
        .radius(4.0)
        .fill(t.bg_input)
        .border(1.0, t.border)
        .color(t.text_primary)
        .on_input_key(field_key)
}

/// A drawer field's keys it does not take as text: ⌘↩ sends and Escape closes.
fn field_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Enter if ev.mods.cmd || ev.mods.ctrl => super::send(state, tx),
        Key::Escape => {
            crate::shell::escape_drawers(state, tx);
        }
        _ => return false,
    }
    true
}

fn footer(state: &State, item: &Item<'_>, several: bool, t: &Tokens) -> El {
    let ask = item.ask;
    let snoozable = item
        .holder
        .filter(|s| !super::offline(s, &state.facts.outposts));
    let snoozed = snoozable
        .is_some_and(|s| queue::is_snoozed(s.turn_snoozed_until.as_deref(), state.window.now_ms));
    let mut hints = Vec::new();
    if several {
        hints.push("j k next · previous");
    }
    if item.holder.is_some() {
        hints.push("o open agent");
    }
    if snoozable.is_some() {
        hints.push(if snoozed { "s wake" } else { "s snooze" });
    }
    hints.push("r replied");
    hints.push("esc close");
    let sending = state.window.asks.sending.is_some();
    let accept =
        (ask.reply.is_none() && super::recommended(ask).len() > 1 && !sending).then(|| {
            let id = ask.id.clone();
            small_button("asks.accept", "Accept all recommended a", "", t)
                .on_press(move |state: &mut State| super::accept_recommended(state, &id))
        });
    let action = match &ask.reply {
        Some(reply) => {
            let age = queue::turn_age(Some(&reply.answered_at), state.window.now_ms);
            let words = if age == "now" {
                "Replied just now".to_owned()
            } else {
                format!("Replied {age} ago")
            };
            text(words.clone())
                .key("asks.replied-when")
                .label(words)
                .font(type_::LABEL)
                .color(t.text_tertiary)
        }
        None => {
            let sendable = !sending
                && state
                    .window
                    .asks
                    .drafts
                    .get(&ask.id)
                    .is_some_and(|d| d.sendable(ask));
            let words = format!(
                "{} {}",
                if sending { "Sending…" } else { "Send" },
                send_chord()
            );
            Button::new(words)
                .variant(ButtonVariant::Submit)
                .disabled(!sendable)
                .on_press_tx(super::send)
                .build(attn_ui::kit(t))
                .key("asks.send")
        }
    };
    col()
        .key("asks.footer")
        .w_full()
        .gap(space::S2)
        .padding(space::S3)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
        .child(
            text(hints.join("  ·  "))
                .key("asks.hints")
                .font(type_::LABEL)
                .color(t.text_muted)
                .wrap(),
        )
        .child(
            row()
                .w_full()
                .items_center()
                .gap(space::S2)
                .child(el().grow(1.0))
                .children(accept)
                .child(action),
        )
}

fn toggle_replied(state: &mut State, tx: &mut Tx) {
    state.window.asks.show_replied = !state.window.asks.show_replied;
    state.tick(tx);
    super::follow(state, tx);
}

/// The drawer's keys while it or a control in it holds focus: ⌘↩ sends from anywhere in it;
/// the letters act only outside a text field.
fn drawer_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let m = ev.mods;
    if ev.key == Key::Enter && (m.cmd || m.ctrl) {
        super::send(state, tx);
        return true;
    }
    if ev.key == Key::Escape {
        crate::shell::escape_drawers(state, tx);
        return true;
    }
    if tx.in_text_field() || m.cmd || m.ctrl || m.alt {
        return false;
    }
    let Key::Text(typed) = &ev.key else {
        return false;
    };
    let shown = state.shown_ask();
    let ask = shown
        .as_ref()
        .and_then(|(id, _)| state.facts.asks.iter().find(|a| a.id == *id))
        .cloned();
    let holder = ask
        .as_ref()
        .and_then(|a| super::holder(a, &state.facts))
        .map(|s| s.id.clone());
    match typed.as_str() {
        "j" if ask.is_some() => super::step(state, 1, tx),
        "k" if ask.is_some() => super::step(state, -1, tx),
        "r" => toggle_replied(state, tx),
        "a" => match &ask {
            Some(ask) if ask.reply.is_none() && super::recommended(ask).len() > 1 => {
                super::accept_recommended(state, &ask.id)
            }
            _ => return false,
        },
        "o" => match holder {
            Some(session) => super::open_agent(state, &session, tx),
            None => return false,
        },
        "s" if holder.is_some() => super::toggle_snooze(state, tx),
        "g" => match ask.as_ref().and_then(|a| a.seed_id.clone()) {
            Some(seed) => super::open_seed(state, &seed, tx),
            None => return false,
        },
        _ => return false,
    }
    true
}

/// The `?` an agent's row carries for its open asks, with their number past one.
pub(crate) fn marker(row_key: &str, count: usize, t: &Tokens) -> Option<El> {
    if count == 0 {
        return None;
    }
    let words = format!("{count} open {}", if count == 1 { "ask" } else { "asks" });
    let glyph = if count > 1 {
        format!("?{count}")
    } else {
        "?".to_owned()
    };
    Some(
        text(glyph)
            .key(format!("{row_key}.ask"))
            .role("img")
            .label(words)
            .font(type_::MONO_CAPTION)
            .color(t.accent),
    )
}

/// The agent's open-ask chip in its tile header: the oldest ask's subject and how many more, at
/// the level the tile's width leaves it. Resting the pointer on it previews the asks; a press
/// opens the drawer on the oldest.
pub(crate) fn chip(leaf: &str, session: &str, chip: &Chip, level: ChipLevel, t: &Tokens) -> El {
    let label = if chip.more > 0 {
        format!("Open ask: {}, and {} more", chip.subject, chip.more)
    } else {
        format!("Open ask: {}", chip.subject)
    };
    let id = chip.ask.clone();
    let subject = (level != ChipLevel::Icon).then(|| {
        let el = text(chip.subject.clone())
            .font(type_::LABEL)
            .color(t.text_primary)
            .single_line()
            .truncate()
            .min_w(0.0);
        if level == ChipLevel::Short {
            el.max_w(CHIP_SHORT)
        } else {
            el
        }
    });
    let (accent, tertiary) = (t.accent, t.text_tertiary);
    let more = (chip.more > 0 && level != ChipLevel::Icon).then(|| format!("+{}", chip.more));
    let el = Button::new(label)
        .label_with(move |_, _| {
            row()
                .items_center()
                .gap(CHIP_GAP)
                .min_w(0.0)
                .child(text("?").font(type_::LABEL).color(accent))
                .children(subject)
                .children(more.map(|more| text(more).font(type_::LABEL).color(tertiary)))
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::close(state);
            super::show(state, Some(&id), tx)
        })
        .build(attn_ui::kit(t))
        .key(chip_key(leaf))
        .px(CHIP_PAD)
        .min_w(0.0)
        .radius(5.0)
        .fill(t.bg_button)
        .border(1.0, t.accent);
    header_pop::hovers(el, leaf, session, What::Ask)
}

const CHIP_GAP: f32 = 4.0;
const CHIP_PAD: f32 = 6.0;

/// The chip's widths in full, with its subject cut, and as its mark alone.
pub(crate) fn chip_widths(chip: &Chip) -> crate::desktop::Widths {
    let w = |s: &str| crate::desktop::text_w(s, type_::LABEL);
    let frame = 2.0 * CHIP_PAD + 2.0 + w("?");
    let more = if chip.more > 0 {
        CHIP_GAP + w(&format!("+{}", chip.more))
    } else {
        0.0
    };
    let subject = w(&chip.subject);
    crate::desktop::Widths {
        full: frame + CHIP_GAP + subject + more,
        short: (subject > CHIP_SHORT).then_some(frame + CHIP_GAP + CHIP_SHORT + more),
        icon: Some(frame),
    }
}

pub(crate) fn chip_key(leaf: &str) -> String {
    header_pop::anchor(leaf, What::Ask)
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum ChipLevel {
    Full,
    Short,
    Icon,
}

pub(crate) const CHIP_SHORT: f32 = 120.0;

/// The preview of an agent's open asks under its chip: each ask's questions and choices, and
/// Answer, which opens the drawer on it.
pub(crate) fn preview(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let asks = super::of_session(&state.facts, session);
    if asks.is_empty() {
        return None;
    }
    let sections = asks.iter().map(|ask| {
        let id = ask.id.clone();
        let questions = questions(ask).map(|q| {
            let lines: Vec<El> = match Kind::of(q) {
                Kind::Text => vec![
                    text("Written answer")
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                ],
                _ => {
                    q.choices
                        .iter()
                        .map(|c| {
                            let recommended = q.recommended.as_deref() == Some(c.label.as_str());
                            row()
                                .gap(space::S1)
                                .child(
                                    text(c.label.clone())
                                        .font(type_::LABEL)
                                        .color(t.text_secondary),
                                )
                                .children(
                                    c.description
                                        .clone()
                                        .map(|d| text(d).font(type_::LABEL).color(t.text_tertiary)),
                                )
                                .children(recommended.then(|| {
                                    text("recommended").font(type_::LABEL).color(t.accent)
                                }))
                        })
                        .collect()
                }
            };
            col()
                .gap(2.0)
                .child(
                    text(q.prompt.clone())
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .wrap(),
                )
                .children(lines)
        });
        col()
            .key(format!("asks.preview.{}", ask.id))
            .role("group")
            .label(ask.subject.clone())
            .gap(space::S1)
            .child(
                text(ask.subject.clone())
                    .font(type_::LABEL)
                    .color(t.text_primary),
            )
            .children(questions)
            .child(
                small_button(&format!("asks.preview.{}.answer", ask.id), "Answer", "", t)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        header_pop::close(state);
                        super::show(state, Some(&id), tx)
                    }),
            )
    });
    Some(
        col()
            .key(pop.surface())
            .role("dialog")
            .label("Open asks from this agent")
            .w(300.0)
            .padding(space::S2)
            .gap(space::S2)
            .radius(6.0)
            .fill(t.bg_elevated)
            .border(1.0, t.border)
            .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
                header_pop::hover_popover(state, inside, tx)
            })
            .children(sections)
            .anchored_fit(pop.anchor(), Side::Bottom, rune::Align::End, 4.0),
    )
}

/// The title bar's asks button, while asks are on or any exists: it counts the open asks, but a
/// snoozed agent's, and opens or closes the drawer.
pub(crate) fn title_button(state: &State, t: &Tokens) -> Option<El> {
    if !super::available(&state.facts) {
        return None;
    }
    let count = super::open_count(&state.facts, state.window.now_ms);
    let keys = state
        .keymap
        .binding(&crate::Action::DockAsks)
        .map(|b| format!(" ({})", state.keymap.label(b)))
        .unwrap_or_default();
    let label = match count {
        0 => format!("Asks{keys}"),
        1 => format!("1 open ask{keys}"),
        n => format!("{n} open asks{keys}"),
    };
    let open = state.window.shows(crate::shell::Drawer::Asks);
    let tokens = *t;
    Some(
        Button::new(label)
            .variant(ButtonVariant::GhostMuted)
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::toggle(state, tx))
            .label_with(move |_, _| {
                row()
                    .gap(2.0)
                    .items_center()
                    .child(text("?").font(type_::BODY).color(tokens.text_secondary))
                    .children((count > 0).then(|| {
                        text(count.to_string())
                            .font(type_::MONO_CAPTION)
                            .color(tokens.accent)
                    }))
            })
            .build(attn_ui::kit(t))
            .key("titlebar.asks")
            .a11y_value(count.to_string())
            .checked(open)
            .h(22.0)
            .min_w(24.0)
            .px(space::S1)
            .radius(4.0)
            .fill(if open {
                t.bg_selected
            } else {
                rune::Color::TRANSPARENT
            })
            .hover(t.bg_elevated),
    )
}
