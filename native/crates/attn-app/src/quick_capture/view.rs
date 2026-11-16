use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{ClipboardContent, Cx, El, Tx, col, el, row, text, text_input};

use super::attach::{self, Source};
use super::{Busy, Face, Panel, history};
use attn_ui::Appearance;

/// The key of the message field, which holds the keyboard while the draft shows.
pub(crate) const MESSAGE: &str = "capture.message";

pub(super) fn view(panel: &Panel, cx: &mut Cx<Panel>) -> El {
    cx.controls_keep_focus();
    let appearance =
        Appearance::from_setting(panel.facts.settings.get("theme").map(String::as_str));
    let t = attn_ui::tokens(appearance.is_dark(cx.system_dark()));
    cx.zoom(crate::settings::text_scale(&panel.facts.settings));
    let body = match panel.face {
        Face::Compose => compose(panel, &t),
        Face::Recent => recent(panel, &t),
    };
    col()
        .key("capture")
        .role("dialog")
        .label("Quick Capture")
        .w_full()
        .h_full()
        .fill(t.bg_panel)
        .radius(12.0)
        .border(1.0, t.border)
        .padding(space::S3)
        .gap(space::S2)
        .window_drag()
        .on_drop_files(
            |panel: &mut Panel, paths: &[std::path::PathBuf], tx: &mut Tx| {
                panel.dropping = false;
                let files = paths
                    .iter()
                    .map(|p| (file_name(p), Source::Path(p.clone())))
                    .collect();
                attach::add(panel, files, tx);
                tx.focus(MESSAGE, false);
            },
        )
        .on_drop_hover(|panel: &mut Panel, over: bool, _: &mut Tx| panel.dropping = over)
        .on_window_focus(|panel: &mut Panel, focused: bool, _: &mut Tx| panel.key_window = focused)
        .child(body)
        .children(alert(panel, &t))
        .children((panel.face == Face::Compose).then(|| footer(panel, &t)))
        .children(panel.picking.map(|at| picker(panel, at, &t)))
        .children(panel.dropping.then(|| drop_zone(&t)))
}

fn file_name(path: &std::path::Path) -> String {
    path.file_name().map_or_else(
        || "Dropped file".to_owned(),
        |n| n.to_string_lossy().into_owned(),
    )
}

fn compose(panel: &Panel, t: &Tokens) -> El {
    let to = format!("To: {}", panel.label(&panel.mailbox()));
    let header = row()
        .key("capture.top")
        .items_center()
        .gap(space::S2)
        .child(button(
            "capture.recent",
            "Recent messages",
            "Recent",
            t,
            |panel, tx| {
                history::open(panel, tx);
            },
        ))
        .child(el().grow(1.0))
        .child(
            button(
                "capture.to",
                "Send to",
                &format!("{to} ⌄"),
                t,
                |panel, _| {
                    super::toggle_picker(panel);
                },
            )
            .disabled(!panel.editable())
            .description("⌘K"),
        );
    let placeholder = format!("Message {}", panel.label(&panel.mailbox()));
    let editable = panel.editable();
    let field = text_input(&panel.draft.text, type_::BODY)
        .key(MESSAGE)
        .label("Message")
        .w_full()
        .color(t.text_primary)
        .placeholder(placeholder, t.text_tertiary)
        .caret(t.text_primary, attn_ui::kit(t).selection())
        .return_breaks_line()
        .disabled(!editable)
        .on_input_key(super::on_message_key)
        .on_input_paste(paste);
    let field = if editable {
        field
            .on_edit(|panel: &mut Panel| &mut panel.draft.text)
            .on_input_change(|panel: &mut Panel, tx: &mut Tx| panel.edited(tx))
    } else {
        field
    };
    let message = col()
        .key("capture.field")
        .w_full()
        .grow(1.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .child(field);
    col()
        .w_full()
        .grow(1.0)
        .gap(space::S2)
        .child(header)
        .child(message)
        .children((!panel.draft.files.is_empty()).then(|| files(panel, t)))
}

/// Pasted files attach to the draft, and so does a pasted image, whether it came alone, as a
/// screenshot does, or beside the HTML a copied web image brings; text pastes into the message.
fn paste(panel: &mut Panel, content: &ClipboardContent, tx: &mut Tx) -> bool {
    if !panel.editable() {
        return true;
    }
    if !content.files().is_empty() {
        let files = content
            .files()
            .iter()
            .map(|p| (file_name(p), Source::Path(p.clone())))
            .collect();
        attach::add(panel, files, tx);
        return true;
    }
    if let Some(image) = content.image() {
        let png = image.png().into();
        attach::add(
            panel,
            vec![("Pasted screenshot".into(), Source::Png(png))],
            tx,
        );
        return true;
    }
    false
}

fn files(panel: &Panel, t: &Tokens) -> El {
    row()
        .key("capture.files")
        .role("list")
        .label("Attached files")
        .w_full()
        .gap(space::S2)
        .wrap()
        .children(panel.draft.files.iter().map(|file| {
            let id = file.id.clone();
            let ready = file.ready();
            let detail = if ready {
                size(file.size())
            } else {
                "Attaching…".to_owned()
            };
            row()
                .key(format!("capture.file.{}", file.id))
                .role("listitem")
                .label(file.name.clone())
                .a11y_value(if ready { "ready" } else { "attaching" })
                .items_center()
                .gap(space::S1)
                .px(space::S2)
                .py(space::S1)
                .radius(6.0)
                .fill(t.bg_elevated)
                .border(1.0, t.border)
                .child(
                    text(kind(&file.name, file.image().is_some()))
                        .font(type_::MONO_CAPTION)
                        .color(t.text_tertiary),
                )
                .child(
                    text(file.name.clone())
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .single_line()
                        .truncate()
                        .max_w(180.0),
                )
                .child(text(detail).font(type_::LABEL).color(t.text_tertiary))
                .child(
                    button(
                        &format!("capture.file.{}.remove", file.id),
                        &format!("Remove {}", file.name),
                        "×",
                        t,
                        move |panel, tx| attach::remove(panel, &id, tx),
                    )
                    .disabled(!panel.editable()),
                )
        }))
}

fn alert(panel: &Panel, t: &Tokens) -> Option<El> {
    let error = panel.error.as_ref().or(panel
        .history
        .error
        .as_ref()
        .filter(|_| panel.face == Face::Recent))?;
    Some(
        text(error.clone())
            .key("capture.error")
            .role("alert")
            .font(type_::LABEL)
            .color(t.error)
            .wrap(),
    )
}

fn footer(panel: &Panel, t: &Tokens) -> El {
    let receipt = match panel.busy {
        Busy::Saving => Some("Saving…".to_owned()),
        Busy::Checking => Some("Checking submission…".to_owned()),
        Busy::Idle if panel.draft.uncertain => Some("Submission unconfirmed".to_owned()),
        Busy::Idle if !panel.connected() => Some("Connecting…".to_owned()),
        Busy::Idle => panel
            .saved_for
            .as_ref()
            .map(|m| format!("Saved for {}", panel.label(m))),
    };
    let send = if panel.draft.uncertain {
        "Retry ↵"
    } else {
        "Send ↵"
    };
    row()
        .key("capture.footer")
        .items_center()
        .gap(space::S2)
        .children(receipt.map(|r| {
            text(r)
                .key("capture.receipt")
                .role("status")
                .font(type_::LABEL)
                .color(t.text_secondary)
        }))
        .child(el().grow(1.0))
        .child(
            text("⇧↵ new line")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        )
        .child(
            button(
                "capture.send",
                send.trim_end_matches(" ↵"),
                send,
                t,
                super::send,
            )
            .disabled(!panel.can_send()),
        )
}

fn picker(panel: &Panel, at: usize, t: &Tokens) -> El {
    let chosen = panel.mailbox();
    let options = panel.roster().into_iter().enumerate().map(|(index, m)| {
        let picked = m.id.clone();
        let detail = if m.in_session {
            "In session"
        } else {
            "Between sessions"
        };
        row()
            .key(format!("capture.mailbox.{}", m.id))
            .role("option")
            .label(m.name.clone())
            .focus_selected(index == at)
            .w_full()
            .items_center()
            .gap(space::S2)
            .px(space::S2)
            .py(space::S1)
            .radius(4.0)
            .fill(if index == at {
                t.bg_selected
            } else {
                t.bg_elevated
            })
            .on_press_tx(move |panel: &mut Panel, tx: &mut Tx| {
                super::pick(panel, picked.clone(), tx);
            })
            .child(
                text(m.name.clone())
                    .font(type_::BODY)
                    .color(if m.id == chosen {
                        t.accent
                    } else {
                        t.text_primary
                    }),
            )
            .child(text(detail).font(type_::LABEL).color(t.text_tertiary))
            .child(el().grow(1.0))
            .children((index < 4).then(|| {
                text(format!("⌘{}", index + 1))
                    .font(type_::MONO_CAPTION)
                    .color(t.text_tertiary)
            }))
    });
    let list = col()
        .key("capture.mailboxes")
        .role("listbox")
        .label("Send to")
        .w(260.0)
        .padding(space::S1)
        .gap(2.0)
        .children(options);
    el().absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .on_press_tx(|panel: &mut Panel, _: &mut Tx| panel.picking = None)
        .child(
            col()
                .w_full()
                .items_end()
                .pt(44.0)
                .px(space::S3)
                .child(rune_kit::popover::raised(attn_ui::kit(t), list, 8.0)),
        )
}

fn drop_zone(t: &Tokens) -> El {
    el().key("capture.drop")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .radius(12.0)
        .fill(t.bg_overlay)
        .border(2.0, t.accent)
        .items_center()
        .justify_center()
        .child(
            text("Drop to attach to this message")
                .font(type_::BODY)
                .color(t.text_primary),
        )
}

fn recent(panel: &Panel, t: &Tokens) -> El {
    let ordered = panel.history.ordered();
    let count = ordered.len();
    let heading = row()
        .key("capture.history.heading")
        .items_center()
        .gap(space::S2)
        .child(
            text("Recent")
                .role("heading")
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(
            text(format!(
                "{count} {}",
                if count == 1 { "message" } else { "messages" }
            ))
            .font(type_::LABEL)
            .color(t.text_tertiary),
        )
        .child(el().grow(1.0))
        .child(button(
            "capture.history.back",
            "Back to draft",
            "Back to draft  esc",
            t,
            history::close,
        ));
    let has_draft = !panel.draft.text.text.is_empty() || !panel.draft.files.is_empty();
    let draft = has_draft.then(|| {
        row()
            .key("capture.history.draft")
            .items_center()
            .gap(space::S2)
            .child(
                text("Current draft")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
            .child(el().grow(1.0))
            .child(
                button(
                    "capture.history.discard",
                    "Discard draft",
                    "Discard draft",
                    t,
                    super::discard,
                )
                .disabled(panel.busy != Busy::Idle),
            )
    });
    let empty = (count == 0).then(|| {
        text(if panel.history.loading {
            "Loading messages…"
        } else {
            "No saved messages yet."
        })
        .key("capture.history.empty")
        .font(type_::BODY)
        .color(t.text_tertiary)
    });
    let older = panel.history.next_cursor.is_some().then(|| {
        button(
            "capture.history.older",
            "Show older messages",
            "Show older messages",
            t,
            history::older,
        )
        .disabled(panel.history.loading)
    });
    let rows = ordered.iter().map(|record| item(panel, record, t));
    col()
        .key("capture.history")
        .role("region")
        .label("Recent messages")
        .w_full()
        .grow(1.0)
        .gap(space::S2)
        .child(heading)
        .children(draft)
        .child(
            col()
                .key("capture.history.list")
                .w_full()
                .grow(1.0)
                .gap(space::S2)
                .scroll(rune::ScrollSpec { stick_bottom: true })
                .children(older)
                .children(empty)
                .child(
                    col()
                        .key("capture.history.messages")
                        .role("list")
                        .label("Sent messages")
                        .w_full()
                        .gap(space::S2)
                        .children(rows),
                ),
        )
        .child(
            text("↑↓ move    esc back to draft")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        )
}

fn item(panel: &Panel, record: &attn_protocol::QuickCaptureRecord, t: &Tokens) -> El {
    let mailbox = record.mailbox.member_id.clone().unwrap_or_default();
    let read = panel.read_at(record);
    let selected = panel.history.selected.as_deref() == Some(record.id.as_str());
    let at = read.as_deref().unwrap_or(&record.created_at);
    let when = crate::calendar::ms_of_rfc3339(at).map_or_else(String::new, crate::calendar::clock);
    let status = format!("{} {when}", if read.is_some() { "Read" } else { "Sent" });
    let picked = record.id.clone();
    let files = record.attachments.iter().map(|file| {
        text(format!(
            "{} · {}",
            file.name,
            size(file.bytes.max(0) as u64)
        ))
        .font(type_::LABEL)
        .color(t.text_secondary)
    });
    col()
        .key(format!("capture.history.item.{}", record.id))
        .role("listitem")
        .label(format!("to {}", panel.label(&mailbox)))
        .a11y_value(status.clone())
        .focus_selected(selected)
        .w_full()
        .gap(space::S1)
        .padding(space::S2)
        .radius(8.0)
        .fill(if selected {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_press_tx(move |panel: &mut Panel, _: &mut Tx| {
            panel.history.selected = Some(picked.clone());
        })
        .child(
            text(format!("to {}", panel.label(&mailbox)))
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .children((!record.content.is_empty()).then(|| {
            text(record.content.clone())
                .font(type_::BODY)
                .color(t.text_primary)
                .wrap()
        }))
        .children(files)
        .child(
            text(status)
                .key(format!("capture.history.item.{}.status", record.id))
                .description(if read.is_some() {
                    "Retrieved from the inbox; not necessarily understood or acted upon."
                } else {
                    ""
                })
                .font(type_::LABEL)
                .color(if read.is_some() {
                    t.success
                } else {
                    t.text_tertiary
                }),
        )
}

fn button(
    key: &str,
    name: &str,
    face: &str,
    t: &Tokens,
    press: impl Fn(&mut Panel, &mut Tx) + 'static,
) -> El {
    row()
        .key(key.to_owned())
        .role("button")
        .label(name.to_owned())
        .items_center()
        .px(space::S2)
        .py(space::S1)
        .radius(6.0)
        .fill(t.bg_button)
        .border(1.0, t.border)
        .on_press_tx(press)
        .child(
            text(face.to_owned())
                .font(type_::LABEL)
                .color(t.text_primary),
        )
}

fn kind(name: &str, image: bool) -> String {
    match name.rsplit_once('.') {
        Some((_, extension)) => extension.to_uppercase(),
        None if image => "IMG".into(),
        None => "FILE".into(),
    }
}

fn size(bytes: u64) -> String {
    const K: f64 = 1024.0;
    let b = bytes as f64;
    if bytes < 1024 {
        format!("{bytes} B")
    } else if b < K * K {
        format!("{:.1} KiB", b / K)
    } else if b < K * K * K {
        format!("{:.1} MiB", b / (K * K))
    } else {
        format!("{:.1} GiB", b / (K * K * K))
    }
}
