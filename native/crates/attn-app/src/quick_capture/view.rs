use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Inline, Menu, Notice, Prose, Sheet, Stack, Status};
use attn_ui::system::{
    Button, IconSize, ListRow, Look, Size, Trigger, TriggerLook, Type, composer, drop_overlay,
    file_chip, icon_button, image_tile, keycaps, pick_area, words,
};
use rune::{ClipboardContent, Cx, El, SurfaceFit, Tx, col, el};
use rune_kit::{Placement, popover};

use super::attach::{self, Source};
use super::previews::{self, Picture, Sent};
use super::{Busy, Face, Panel, history};

/// The key of the message field, which holds the keyboard while the draft shows.
pub(crate) const MESSAGE: &str = "capture.message";
pub(crate) const BACK: &str = "capture.history.back";

pub(super) fn view(panel: &Panel, cx: &mut Cx<Panel>) -> El {
    cx.controls_keep_focus();
    let t = crate::colors(&panel.facts.settings, cx.system_dark()).ui;
    cx.zoom(crate::settings::text_scale(&panel.facts.settings));
    let panel_el = match panel.face {
        Face::Compose => compose(panel, &t).framed().build(&t),
        Face::Recent => col().key("capture").w_full().h_full().child(
            recent(panel, &t)
                .framed()
                .build(&t)
                .role("region")
                .label("Recent messages"),
        ),
    };
    el().key("capture.window")
        .w_full()
        .h_full()
        .child(panel_el.role("dialog").label("Quick Capture"))
        .window_drag()
        .preserve_focus_on_press(panel.face == Face::Compose)
        .on_key(super::on_picker_key)
        .on_drop_files(
            |panel: &mut Panel, paths: &[std::path::PathBuf], tx: &mut Tx| {
                panel.dropping = false;
                let files = paths
                    .iter()
                    .map(|p| (file_name(p), Source::Path(p.clone())))
                    .collect();
                attach::add(panel, files, tx);
                panel.take_keys();
                tx.focus(MESSAGE, false);
            },
        )
        .on_drop_hover(|panel: &mut Panel, over: bool, _: &mut Tx| panel.dropping = over)
        .on_window_focus(|panel: &mut Panel, focused: bool, _: &mut Tx| panel.key_window = focused)
        .children(panel.picking.map(|at| picker(panel, at, &t)))
        .children(
            panel
                .dropping
                .then(|| drop_overlay("capture.drop", "Drop to attach to this message", &t)),
        )
}

fn file_name(path: &std::path::Path) -> String {
    path.file_name().map_or_else(
        || "Dropped file".to_owned(),
        |n| n.to_string_lossy().into_owned(),
    )
}

fn compose(panel: &Panel, t: &Tokens) -> Sheet {
    let picking = panel.picking.is_some();
    let to = Trigger::new(
        "capture.to",
        "Send to",
        words(
            format!("To: {}", panel.label(&panel.mailbox())),
            Type::Name,
            t.text_primary,
        ),
    )
    .look(TriggerLook::Quiet)
    .open(picking)
    .disabled(!panel.editable())
    .build(t, super::toggle_picker);
    let to = El::from(to).description("⌘K").focusable(false);
    let recent = Button::new("Recent")
        .look(Look::Quiet)
        .size(Size::Compact)
        .on_press_tx(|panel: &mut Panel, tx: &mut Tx| history::open(panel, tx))
        .build(t)
        .key("capture.recent")
        .label("Recent messages");
    let placeholder = format!("Message {}", panel.label(&panel.mailbox()));
    let editable = panel.editable();
    let field = composer(&panel.draft.text, placeholder, t)
        .key(MESSAGE)
        .label("Message")
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
    let picked = panel
        .picking
        .and_then(|at| panel.roster().get(at).map(|m| mailbox_key(&m.id)));
    let field = match picked {
        Some(key) => field.active_descendant(key),
        None => field,
    };
    let sheet = Sheet::new("capture", "Quick Capture")
        .head(Inline::joined().child(to))
        .aside(recent)
        .push(Block::custom(
            "capture.field.block",
            Stack::tight().key("capture.field").w_full().child(field),
        ))
        .push_all(
            (!panel.draft.files.is_empty())
                .then(|| Block::custom("capture.files.block", files(panel, t))),
        )
        .footer(footer(panel, t));
    with_alert(sheet, alert(panel, t))
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
    Inline::loose()
        .key("capture.files")
        .role("list")
        .label("Attached files")
        .w_full()
        .children(panel.draft.files.iter().map(|file| {
            let id = file.id.clone();
            let remove = remove_button(
                &format!("capture.file.{}.remove", file.id),
                &format!("Remove {}", file.name),
                !panel.editable(),
                t,
                move |panel, tx| attach::remove(panel, &id, tx),
            );
            let preview = file
                .image()
                .and_then(|path| panel.previews.drafted(path))
                .and_then(|picture| self::picture(&file.name, picture, TILE));
            if let Some(picture) = preview {
                return image_tile(
                    format!("capture.file.{}", file.id),
                    &file.name,
                    Some(picture),
                    TILE,
                    Some(remove),
                    t,
                )
                .a11y_value("ready");
            }
            let ready = file.ready();
            let detail = if ready {
                size(file.size())
            } else {
                "Attaching…".to_owned()
            };
            file_chip(
                format!("capture.file.{}", file.id),
                &file.name,
                kind(&file.name, file.image().is_some()),
                &detail,
                Some(remove),
                t,
            )
            .a11y_value(if ready { "ready" } else { "attaching" })
        }))
}

/// A drafted image's picture: wide enough to tell screenshots apart, two rows of words high.
const TILE: (f32, f32) = (84.0, 58.0);

/// A sent image's picture.
const SENT_TILE: (f32, f32) = (132.0, 84.0);

fn remove_button(
    key: &str,
    label: &str,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut Panel, &mut Tx) + 'static,
) -> El {
    icon_button(
        key.to_owned(),
        label.to_owned(),
        attn_ui::glyph::TIMES,
        IconSize::Small,
    )
    .disabled(disabled)
    .on_press_tx(press)
    .build(t)
    .into()
}

/// `picture` cropped to fill `size`, named `name` once it has decoded, and nothing when it
/// could not decode.
fn picture(name: &str, picture: &Picture, (w, h): (f32, f32)) -> Option<El> {
    let frame = el().role("image").size(w, h);
    match picture {
        Picture::Decoding(_) => Some(frame.label(format!("{name} (loading)"))),
        Picture::Ready(image) => Some(
            frame
                .label(name)
                .pixels_fit(image.pixels(), SurfaceFit::Fill),
        ),
        Picture::Broken => None,
    }
}

fn with_alert(sheet: Sheet, alert: Option<Block>) -> Sheet {
    match alert {
        Some(alert) => sheet.footer(alert),
        None => sheet,
    }
}

fn alert(panel: &Panel, t: &Tokens) -> Option<Block> {
    let error = panel.error.as_ref().or(panel
        .history
        .error
        .as_ref()
        .filter(|_| panel.face == Face::Recent))?;
    Some(Notice::error("capture.error", error.clone()).build(t))
}

fn footer(panel: &Panel, t: &Tokens) -> Block {
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
        "Retry"
    } else {
        "Send"
    };
    let hint = Inline::tight()
        .key("capture.hint")
        .label("⇧↵ new line")
        .child(keycaps(["⇧↵"], t).a11y_hidden())
        .child(words("new line", Type::Small, t.text_muted).a11y_hidden());
    let send = El::from(
        Button::new(send)
            .look(Look::Primary)
            .disabled(!panel.can_send())
            .trailing(keycaps(["↵"], t).a11y_hidden())
            .on_press_tx(super::send)
            .build(t)
            .key("capture.send"),
    )
    .default_button();
    let mut line = Actions::new("capture.actions");
    if let Some(receipt) = receipt {
        line = line.push(
            words(receipt.clone(), Type::Small, t.text_secondary)
                .key("capture.receipt")
                .role("status")
                .label(receipt),
        );
    }
    line.then_end().push(hint).push(send).build()
}

fn mailbox_key(id: &str) -> String {
    format!("capture.mailbox.{id}")
}

/// The list of mailboxes under the To control.
fn picker(panel: &Panel, at: usize, t: &Tokens) -> El {
    let chosen = panel.mailbox();
    let rows = panel.roster().into_iter().enumerate().map(|(index, m)| {
        let picked = m.id.clone();
        let detail = if m.in_session {
            "In session"
        } else {
            "Between sessions"
        };
        let mut row = ListRow::new(mailbox_key(&m.id), m.name.clone())
            .role("option")
            .unfocusable()
            .cursor(index == at)
            .selected(index == at)
            .title(m.name.clone())
            .tag(words(detail, Type::Small, t.text_muted));
        if index < 4 {
            row = row.aside(keycaps([format!("⌘{}", index + 1)], t));
        }
        if m.id == chosen {
            row = row.aside(
                words("✓", Type::Small, t.brand)
                    .key(format!("capture.mailbox.{}.chosen", m.id))
                    .a11y_hidden(),
            );
        }
        row.build(t)
            .on_press_tx(move |panel: &mut Panel, tx: &mut Tx| {
                super::pick(panel, picked.clone(), tx);
            })
    });
    let menu = Menu::new("capture.mailboxes", "Send to")
        .listbox()
        .rows(rows)
        .build(t);
    el().absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .on_background_press(|panel: &mut Panel| panel.picking = None)
        .child(popover::anchored(
            attn_ui::kit(t),
            menu,
            "capture.to",
            Placement::BottomStart,
            PICKER_DROP,
        ))
}

/// How far under the To control the list opens.
const PICKER_DROP: f32 = 4.0;

fn recent(panel: &Panel, t: &Tokens) -> Sheet {
    let ordered = panel.history.ordered();
    let count = ordered.len();
    let back = Button::new("Back to draft")
        .size(Size::Compact)
        .trailing(keycaps(["esc"], t).a11y_hidden())
        .on_press_tx(history::close)
        .build(t)
        .key(BACK)
        .label("Back to draft");
    let count_said = words(
        format!(
            "{count} {}",
            if count == 1 { "message" } else { "messages" }
        ),
        Type::Small,
        t.text_tertiary,
    );
    let heading = Inline::loose()
        .key("capture.history.heading")
        .child(words("Recent", Type::Heading, t.text_primary).role("heading"))
        .child(count_said);
    let has_draft = !panel.draft.text.text.is_empty() || !panel.draft.files.is_empty();
    let draft = has_draft.then(|| {
        Actions::new("capture.history.actions")
            .push(
                words("Current draft", Type::Small, t.text_secondary).key("capture.history.draft"),
            )
            .then_end()
            .push(
                Button::new("Discard draft")
                    .size(Size::Compact)
                    .disabled(panel.busy != Busy::Idle)
                    .on_press_tx(super::discard)
                    .build(t)
                    .key("capture.history.discard"),
            )
            .build()
    });
    let empty = (count == 0).then(|| {
        Prose::lead(
            "capture.history.empty",
            if panel.history.loading {
                "Loading messages…"
            } else {
                "No saved messages yet."
            },
        )
        .quiet(true)
        .build(t)
    });
    let older = panel.history.next_cursor.is_some().then(|| {
        Block::control(
            "capture.history.older.block",
            Button::new("Show older messages")
                .size(Size::Compact)
                .disabled(panel.history.loading)
                .on_press_tx(history::older)
                .build(t)
                .key("capture.history.older"),
        )
    });
    let mut shown_day = None;
    let mut rows = Vec::new();
    for record in &ordered {
        let ms = crate::calendar::ms_of_rfc3339(&record.created_at);
        let day = ms.map(crate::calendar::local_day);
        if let Some(ms) = ms.filter(|_| day != shown_day) {
            rows.push(
                words(day_label(ms, panel.today), Type::Label, t.text_tertiary)
                    .key(format!("capture.history.day.{}", record.id)),
            );
        }
        shown_day = day;
        rows.push(item(panel, record, t));
    }
    let sheet = Sheet::new("capture.history", "Recent messages")
        .body_key("capture.history.list")
        .opens_at_end()
        .head(heading)
        .aside(back)
        .push_all(draft)
        .push_all(older)
        .push_all(empty)
        .push(Block::custom(
            "capture.history.messages.block",
            Stack::tight()
                .key("capture.history.messages")
                .role("list")
                .label("Sent messages")
                .w_full()
                .children(rows),
        ))
        .footer(
            Status::new(
                "capture.history.keys",
                "↑↓ move    esc back to draft",
                false,
            )
            .build(t),
        );
    with_alert(sheet, alert(panel, t))
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
        if file.media_type.starts_with("image/") {
            return sent_image(panel, record, file, t);
        }
        words(
            format!("{} · {}", file.name, size(file.bytes.max(0) as u64)),
            Type::Small,
            t.text_secondary,
        )
    });
    let to = format!("to {}", panel.label(&mailbox));
    let content = Stack::tight()
        .w_full()
        .child(words(to.clone(), Type::Small, t.text_secondary))
        .children(
            (!record.content.is_empty())
                .then(|| words(record.content.clone(), Type::Body, t.text_primary).wrap()),
        )
        .children(files)
        .child(
            words(
                status.clone(),
                Type::Small,
                if read.is_some() {
                    t.success
                } else {
                    t.text_tertiary
                },
            )
            .key(format!("capture.history.item.{}.status", record.id))
            .description(if read.is_some() {
                "Retrieved from the inbox; not necessarily understood or acted upon."
            } else {
                ""
            }),
        );
    pick_area(
        format!("capture.history.item.{}", record.id),
        to,
        status,
        selected,
        content,
        t,
        move |panel: &mut Panel, _: &mut Tx| {
            panel.history.selected = Some(picked.clone());
        },
    )
}

/// An image a message sent: its picture cropped to fill, its name and size under it, and why
/// it could not be read with a way to read it again.
fn sent_image(
    panel: &Panel,
    record: &attn_protocol::QuickCaptureRecord,
    file: &attn_protocol::QuickCaptureAttachment,
    t: &Tokens,
) -> El {
    let key = (record.id.clone(), file.id.clone());
    let sent = panel.history.images.get(&key);
    let shown = match sent {
        Some(Sent::Downloaded(shown)) => picture(&file.name, shown, SENT_TILE),
        _ => None,
    };
    let failed = match sent {
        Some(Sent::Failed(why)) => Some(why.clone()),
        _ => None,
    };
    Stack::tight()
        .key(format!("capture.history.file.{}.{}", record.id, file.id))
        .w(SENT_TILE.0)
        .child(image_tile(
            format!("capture.history.file.{}.{}.picture", record.id, file.id),
            &file.name,
            shown,
            SENT_TILE,
            None,
            t,
        ))
        .child(words(file.name.clone(), Type::Small, t.text_tertiary).wrap())
        .child(words(
            size(file.bytes.max(0) as u64),
            Type::Small,
            t.text_tertiary,
        ))
        .children(failed.map(|why| words(why, Type::Small, t.error).role("alert").wrap()))
        .children(matches!(sent, Some(Sent::Failed(_))).then(|| {
            let media_type = file.media_type.clone();
            El::from(
                Button::new("Retry image")
                    .size(Size::Compact)
                    .on_press_tx(move |panel: &mut Panel, tx: &mut Tx| {
                        previews::retry(panel, key.clone(), media_type.clone(), tx)
                    })
                    .build(t)
                    .key(format!(
                        "capture.history.file.{}.{}.retry",
                        record.id, file.id
                    ))
                    .label(format!("Retry {}", file.name)),
            )
        }))
}

/// The day a Recent message was sent, as the Tauri panel heads each day: "Today",
/// "Yesterday", or the month and day, with the year when it is not this year's.
fn day_label(ms: i64, today: i64) -> String {
    const MONTHS: [&str; 12] = [
        "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
    ];
    let day = crate::calendar::local_day(ms);
    if day == today {
        return "Today".to_owned();
    }
    if day == today - 1 {
        return "Yesterday".to_owned();
    }
    let local = rune::time::LocalTime::at(ms);
    let this_year =
        rune::time::LocalTime::at(crate::calendar::local_instant(today, 12 * 3600)).year;
    let month = MONTHS[(local.month as usize).saturating_sub(1) % 12];
    if local.year == this_year {
        format!("{month} {}", local.day)
    } else {
        format!("{month} {}, {}", local.day, local.year)
    }
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
