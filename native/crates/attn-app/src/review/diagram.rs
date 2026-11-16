//! A review's sequence diagram, drawn from the flow the daemon parsed: participants with their
//! lifelines, the messages as arrows with their labels and captions, and the blocks around
//! them as boxes.

use std::collections::HashMap;

use attn_protocol::{ReviewFlow, ReviewFlowGroup, ReviewFlowMessage, ReviewFlowParticipant};
use attn_ui::Tokens;
use rune::tok::{radius, type_};
use rune::{Color, El, LineCap, Shape, TextStyle, canvas, col, el, text};

const CHAR_WIDTH: f32 = 6.6;
const CAPTION_CHAR_WIDTH: f32 = 5.9;
const LABEL_LIMIT: usize = 48;
const LINE_LIMIT: usize = 26;
const CAPTION_LIMIT: usize = 34;
const CAPTION_LINES: usize = 3;
const LINE_HEIGHT: f32 = 14.0;
const CAPTION_LINE_HEIGHT: f32 = 13.0;
const BOX_HEIGHT: f32 = 30.0;
const TWO_LINE_BOX_HEIGHT: f32 = 42.0;
const ROW_PAD: f32 = 16.0;
const GROUP_HEAD: f32 = 20.0;
const GROUP_FOOT: f32 = 8.0;
const GROUP_PAD: f32 = 10.0;
const MARGIN: f32 = 16.0;
const SELF_LOOP: f32 = 28.0;

fn is_section(group: &ReviewFlowGroup) -> bool {
    matches!(group.kind.as_str(), "else" | "and" | "option")
}

fn clip(text: &str, limit: usize) -> String {
    if text.chars().count() > limit {
        let kept: String = text.chars().take(limit - 1).collect();
        format!("{kept}…")
    } else {
        text.to_owned()
    }
}

/// Words in at most `count` lines of about `limit` characters; the last line takes the rest,
/// cut short.
fn wrap(text: &str, limit: usize, count: usize) -> Vec<String> {
    let mut lines: Vec<String> = Vec::new();
    for word in text.split_whitespace() {
        match lines.last_mut() {
            Some(last) if last.chars().count() + 1 + word.chars().count() <= limit => {
                last.push(' ');
                last.push_str(word);
            }
            _ => lines.push(word.to_owned()),
        }
    }
    if lines.len() <= count {
        return lines
            .iter()
            .map(|l| clip(l, limit.max(LABEL_LIMIT)))
            .collect();
    }
    let mut kept: Vec<String> = lines[..count - 1].to_vec();
    kept.push(clip(&lines[count - 1..].join(" "), limit));
    kept
}

fn label_lines(text: &str) -> Vec<String> {
    wrap(text, LINE_LIMIT, 2)
}

fn caption_lines(text: Option<&str>) -> Vec<String> {
    text.filter(|t| !t.is_empty())
        .map(|t| wrap(t, CAPTION_LIMIT, CAPTION_LINES))
        .unwrap_or_default()
}

fn lines_width(lines: &[String], char_width: f32) -> f32 {
    lines
        .iter()
        .map(|l| l.chars().count() as f32 * char_width)
        .fold(0.0, f32::max)
}

/// A participant's name and, after a `<br/>` in its alias, its second line.
fn participant_lines(participant: &ReviewFlowParticipant) -> (String, String) {
    let lower = participant.label.to_ascii_lowercase();
    let cut = ["<br/>", "<br />", "<br>"]
        .iter()
        .filter_map(|tag| lower.find(tag).map(|at| (at, tag.len())))
        .min();
    match cut {
        Some((at, len)) => (
            clip(participant.label[..at].trim(), LABEL_LIMIT),
            clip(
                &participant.label[at + len..]
                    .replace("<br/>", " ")
                    .replace("<br>", " "),
                LABEL_LIMIT,
            )
            .trim()
            .to_owned(),
        ),
        None => (clip(participant.label.trim(), LABEL_LIMIT), String::new()),
    }
}

/// A block's title: its keyword and what follows it, except for `rect`, whose label is a color.
fn group_title(group: &ReviewFlowGroup) -> String {
    match group.kind.as_str() {
        "rect" => String::new(),
        _ if group.label.is_empty() => group.kind.clone(),
        _ => format!("{}  {}", group.kind, group.label),
    }
}

struct Row<'a> {
    message: &'a ReviewFlowMessage,
    top: f32,
    arrow_y: f32,
    bottom: f32,
    label: Vec<String>,
    caption: Vec<String>,
}

struct GroupBox<'a> {
    group: &'a ReviewFlowGroup,
    left: f32,
    right: f32,
    top: f32,
    bottom: f32,
}

struct Divider<'a> {
    group: &'a ReviewFlowGroup,
    y: f32,
    block: Option<i64>,
}

struct Layout<'a> {
    width: f32,
    height: f32,
    box_height: f32,
    box_width: Vec<f32>,
    x: HashMap<&'a str, f32>,
    rows: Vec<Row<'a>>,
    boxes: Vec<GroupBox<'a>>,
    dividers: Vec<Divider<'a>>,
}

/// Columns are as far apart as the widest label or caption between neighbours needs; rows are
/// as tall as their label and caption, with room for the head of each block that opens on them.
fn layout(flow: &ReviewFlow) -> Layout<'_> {
    let ids: Vec<&str> = flow.participants.iter().map(|p| p.id.as_str()).collect();
    let column: HashMap<&str, usize> = ids.iter().enumerate().map(|(i, id)| (*id, i)).collect();
    let names: Vec<(String, String)> = flow.participants.iter().map(participant_lines).collect();
    let box_height = if names.iter().any(|(_, second)| !second.is_empty()) {
        TWO_LINE_BOX_HEIGHT
    } else {
        BOX_HEIGHT
    };
    let box_width: Vec<f32> = names
        .iter()
        .map(|(name, second)| {
            (name.chars().count() as f32 * CHAR_WIDTH + 28.0)
                .max(second.chars().count() as f32 * CAPTION_CHAR_WIDTH + 28.0)
                .clamp(96.0, 220.0)
        })
        .collect();
    let mut gaps: Vec<f32> = (1..ids.len())
        .map(|i| (box_width[i - 1] + box_width[i]) / 2.0 + 28.0)
        .collect();
    for message in &flow.messages {
        let from = column.get(message.from.as_str()).copied().unwrap_or(0);
        let to = column.get(message.to.as_str()).copied().unwrap_or(0);
        let needed = lines_width(&label_lines(&message.text), CHAR_WIDTH).max(lines_width(
            &caption_lines(message.note.as_deref()),
            CAPTION_CHAR_WIDTH,
        )) + 36.0;
        if from == to && from < gaps.len() {
            gaps[from] = gaps[from].max(needed + SELF_LOOP);
        } else if from.abs_diff(to) == 1 {
            let gap = from.min(to);
            gaps[gap] = gaps[gap].max(needed);
        }
    }
    let mut x: HashMap<&str, f32> = HashMap::new();
    let mut at = 0.0;
    for (i, id) in ids.iter().enumerate() {
        x.insert(id, at);
        at += gaps.get(i).copied().unwrap_or(0.0);
    }

    let blocks: Vec<&ReviewFlowGroup> = flow.groups.iter().filter(|g| !is_section(g)).collect();
    let mut tops: HashMap<i64, f32> = HashMap::new();
    let mut bottoms: HashMap<i64, f32> = HashMap::new();
    let mut dividers = Vec::new();
    let mut rows = Vec::new();
    let mut y = MARGIN + box_height + 18.0;
    for message in &flow.messages {
        for group in flow
            .groups
            .iter()
            .filter(|g| g.first_message == message.line)
        {
            if is_section(group) {
                dividers.push(Divider {
                    group,
                    y,
                    block: group.block,
                });
            } else {
                tops.insert(group.line, y);
            }
            y += GROUP_HEAD;
        }
        let label = label_lines(&message.text);
        let caption = caption_lines(message.note.as_deref());
        let self_loop = message.from == message.to;
        let top = y;
        let arrow_y =
            y + (label.len() as f32 * LINE_HEIGHT).max(if self_loop { 18.0 } else { 0.0 }) + 6.0;
        y = arrow_y
            + if caption.is_empty() {
                0.0
            } else {
                6.0 + caption.len() as f32 * CAPTION_LINE_HEIGHT
            }
            + ROW_PAD;
        rows.push(Row {
            message,
            top,
            arrow_y,
            bottom: y,
            label,
            caption,
        });
        for block in blocks
            .iter()
            .rev()
            .filter(|b| b.last_message == message.line)
        {
            y += GROUP_FOOT;
            bottoms.insert(block.line, y);
        }
    }

    let extent = |row: &Row, x: &HashMap<&str, f32>| -> (f32, f32) {
        let x1 = x.get(row.message.from.as_str()).copied().unwrap_or(0.0);
        let x2 = x.get(row.message.to.as_str()).copied().unwrap_or(0.0);
        let wide =
            lines_width(&row.label, CHAR_WIDTH).max(lines_width(&row.caption, CAPTION_CHAR_WIDTH));
        if x1 == x2 {
            return (x1 - 4.0, x1 + SELF_LOOP + 6.0 + wide);
        }
        let middle = (x1 + x2) / 2.0;
        (
            x1.min(x2).min(middle - wide / 2.0),
            x1.max(x2).max(middle + wide / 2.0),
        )
    };
    let mut boxed: Vec<GroupBox> = Vec::new();
    for block in blocks.iter().rev() {
        let inside: Vec<&Row> = rows
            .iter()
            .filter(|r| {
                r.message.line >= block.first_message && r.message.line <= block.last_message
            })
            .collect();
        let (Some(&top), Some(&bottom)) = (tops.get(&block.line), bottoms.get(&block.line)) else {
            continue;
        };
        if inside.is_empty() {
            continue;
        }
        let spans: Vec<(f32, f32)> = inside
            .iter()
            .map(|r| extent(r, &x))
            .chain(
                boxed
                    .iter()
                    .filter(|b| b.top >= top && b.bottom <= bottom)
                    .map(|b| (b.left, b.right)),
            )
            .collect();
        let left = spans.iter().map(|s| s.0).fold(f32::INFINITY, f32::min) - GROUP_PAD;
        let right = (left
            + group_title(block).chars().count() as f32 * CAPTION_CHAR_WIDTH
            + 2.0 * GROUP_PAD)
            .max(spans.iter().map(|s| s.1).fold(f32::NEG_INFINITY, f32::max) + GROUP_PAD);
        boxed.push(GroupBox {
            group: block,
            left,
            right,
            top,
            bottom,
        });
    }
    boxed.sort_by_key(|b| blocks.iter().position(|g| g.line == b.group.line));

    let lefts = ids
        .iter()
        .enumerate()
        .map(|(i, id)| x[id] - box_width[i] / 2.0)
        .chain(rows.iter().map(|r| extent(r, &x).0))
        .chain(boxed.iter().map(|b| b.left));
    let rights: Vec<f32> = ids
        .iter()
        .enumerate()
        .map(|(i, id)| x[id] + box_width[i] / 2.0)
        .chain(rows.iter().map(|r| extent(r, &x).1))
        .chain(boxed.iter().map(|b| b.right))
        .collect();
    let shift = MARGIN - lefts.fold(0.0, f32::min);
    for value in x.values_mut() {
        *value += shift;
    }
    for b in &mut boxed {
        b.left += shift;
        b.right += shift;
    }
    let width = rights.into_iter().fold(0.0, f32::max) + shift + MARGIN;
    let kept: Vec<i64> = boxed.iter().map(|b| b.group.line).collect();
    Layout {
        width,
        height: y,
        box_height,
        box_width,
        x,
        rows,
        boxes: boxed,
        dividers: dividers
            .into_iter()
            .filter(|d| d.block.is_none_or(|b| kept.contains(&b)))
            .collect(),
    }
}

/// How a message is drawn: the review tile and the place key it takes the cursor under, if
/// any, and whether the cursor or the Map stands on it.
pub(super) struct Mark {
    pub(super) place: Option<(String, String)>,
    pub(super) current: bool,
}

/// The diagram of `flow`, keyed `key`, each message marked as `mark` says.
pub(super) fn diagram(
    key: &str,
    flow: &ReviewFlow,
    mark: &dyn Fn(&ReviewFlowMessage) -> Mark,
    t: &Tokens,
) -> El {
    let geometry = layout(flow);
    let label_font = TextStyle {
        size: 11.0,
        line_height: LINE_HEIGHT,
        ..type_::LABEL
    };
    let caption_font = TextStyle {
        size: 10.0,
        line_height: CAPTION_LINE_HEIGHT,
        ..type_::BODY
    };
    let line_color = t.text_tertiary;
    let mut shape = Shape::new();
    let mut texts: Vec<El> = Vec::new();
    let mut hits: Vec<El> = Vec::new();
    let place = |content: El, x: f32, y: f32| content.pin(Some(x), Some(y), None, None);
    let centered = |lines: &[String], x: f32, y: f32, width: f32, font: TextStyle, color: Color| {
        place(
            col().w(width).items_center().children(
                lines
                    .iter()
                    .map(|l| text(l.clone()).font(font).color(color)),
            ),
            x - width / 2.0,
            y,
        )
    };

    for b in &geometry.boxes {
        texts.push(place(
            el().w(b.right - b.left)
                .h(b.bottom - b.top)
                .radius(radius::SM)
                .border(1.0, t.border)
                .fill(t.text_primary.alpha(0.03))
                .a11y_hidden(),
            b.left,
            b.top,
        ));
        let title = group_title(b.group);
        if !title.is_empty() {
            texts.push(place(
                text(title).font(caption_font).color(t.text_secondary),
                b.left + 6.0,
                b.top + 3.0,
            ));
        }
    }
    for d in &geometry.dividers {
        let (left, right) = geometry
            .boxes
            .iter()
            .find(|b| Some(b.group.line) == d.block)
            .map_or((MARGIN, geometry.width - MARGIN), |b| (b.left, b.right));
        shape = dashed(shape, [left, d.y], [right, d.y], t.border);
        texts.push(place(
            text(group_title(d.group))
                .font(caption_font)
                .color(t.text_secondary),
            left + 6.0,
            d.y + 3.0,
        ));
    }
    for (i, participant) in flow.participants.iter().enumerate() {
        let x = geometry.x[participant.id.as_str()];
        let width = geometry.box_width[i];
        shape = shape.line(
            [x, MARGIN + geometry.box_height],
            [x, geometry.height],
            1.0,
            LineCap::Butt,
            line_color.alpha(0.5),
        );
        let (name, second) = participant_lines(participant);
        let corner = if participant.actor {
            geometry.box_height / 2.0
        } else {
            5.0
        };
        texts.push(place(
            col()
                .w(width)
                .h(geometry.box_height)
                .center()
                .radius(corner)
                .border(1.0, t.border_hover)
                .fill(t.bg_elevated)
                .role("text")
                .label(if second.is_empty() {
                    name.clone()
                } else {
                    format!("{name} {second}")
                })
                .child(
                    text(name)
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .a11y_hidden(),
                )
                .children((!second.is_empty()).then(|| {
                    text(second)
                        .font(caption_font)
                        .color(t.text_secondary)
                        .a11y_hidden()
                })),
            x - width / 2.0,
            MARGIN,
        ));
    }
    for row in &geometry.rows {
        let m = row.message;
        let Mark {
            place: placed,
            current,
        } = mark(m);
        let x1 = geometry.x.get(m.from.as_str()).copied().unwrap_or(0.0);
        let x2 = geometry.x.get(m.to.as_str()).copied().unwrap_or(0.0);
        let y = row.arrow_y;
        let stop = m.stop_line.is_some();
        let color = if current {
            t.accent
        } else if stop {
            t.text_primary
        } else {
            t.text_secondary
        };
        let dash = m.arrow.starts_with("--");
        let segment = |shape: Shape, a: [f32; 2], b: [f32; 2]| {
            if dash {
                dashed(shape, a, b, color)
            } else {
                shape.line(a, b, 1.25, LineCap::Butt, color)
            }
        };
        let (text_x, label_top) = if x1 == x2 {
            shape = segment(shape, [x1, y - 14.0], [x1 + SELF_LOOP, y - 14.0]);
            shape = segment(shape, [x1 + SELF_LOOP, y - 14.0], [x1 + SELF_LOOP, y]);
            shape = segment(shape, [x1 + SELF_LOOP, y], [x1 + 2.0, y]);
            shape = head(shape, &m.arrow, x1, y, -1.0, color);
            (None, y - 4.0 - row.label.len() as f32 * LINE_HEIGHT)
        } else {
            let direction = (x2 - x1).signum();
            shape = segment(shape, [x1, y], [x2 - direction * 2.0, y]);
            shape = head(shape, &m.arrow, x2, y, direction, color);
            (
                Some((x1 + x2) / 2.0),
                y - 7.0 - row.label.len() as f32 * LINE_HEIGHT + 2.0,
            )
        };
        let label_w = lines_width(&row.label, CHAR_WIDTH) + 12.0;
        let caption_w = lines_width(&row.caption, CAPTION_CHAR_WIDTH) + 12.0;
        match text_x {
            Some(mid) => {
                texts.push(centered(
                    &row.label, mid, label_top, label_w, label_font, color,
                ));
                if !row.caption.is_empty() {
                    texts.push(centered(
                        &row.caption,
                        mid,
                        y + 4.0,
                        caption_w,
                        caption_font,
                        t.text_secondary,
                    ));
                }
            }
            None => {
                let left = x1 + SELF_LOOP + 6.0;
                texts.push(place(
                    col().children(
                        row.label
                            .iter()
                            .map(|l| text(l.clone()).font(label_font).color(color)),
                    ),
                    left,
                    label_top,
                ));
                if !row.caption.is_empty() {
                    texts.push(place(
                        col().children(
                            row.caption.iter().map(|l| {
                                text(l.clone()).font(caption_font).color(t.text_secondary)
                            }),
                        ),
                        left,
                        y + 4.0,
                    ));
                }
            }
        }
        let name = match m.note.as_deref().filter(|n| !n.is_empty()) {
            Some(note) => format!("{}: {note}", m.text),
            None => m.text.clone(),
        };
        let mut hit = el()
            .role("row")
            .label(name)
            .w(geometry.width - 4.0)
            .h(row.bottom - row.top)
            .radius(radius::SM);
        if current {
            hit = hit.fill(t.accent.alpha(0.14)).focus_selected(true);
        }
        if let Some((tile, k)) = placed {
            hit = super::place_press(hit.key(k.clone()), &tile, k);
        } else {
            hit = hit.key(format!("{key}.message.{}", m.line));
        }
        hits.push(place(hit, 2.0, row.top - ROW_PAD / 2.0));
    }

    let drawing = el()
        .w(geometry.width)
        .h(geometry.height)
        .child(
            canvas(shape)
                .w(geometry.width)
                .h(geometry.height)
                .a11y_hidden(),
        )
        .children(texts)
        .children(hits);
    col()
        .key(key.to_owned())
        .role("figure")
        .label("Sequence diagram")
        .gap(4.0)
        .child(col().w_full().scroll_x().child(drawing))
        .children(flow.note.as_deref().filter(|n| !n.is_empty()).map(|note| {
            text(note.to_owned())
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap()
        }))
}

fn dashed(mut shape: Shape, a: [f32; 2], b: [f32; 2], color: Color) -> Shape {
    let (dx, dy) = (b[0] - a[0], b[1] - a[1]);
    let length = (dx * dx + dy * dy).sqrt();
    if length == 0.0 {
        return shape;
    }
    let (ux, uy) = (dx / length, dy / length);
    let mut at = 0.0;
    while at < length {
        let end = (at + 4.0).min(length);
        shape = shape.line(
            [a[0] + ux * at, a[1] + uy * at],
            [a[0] + ux * end, a[1] + uy * end],
            1.25,
            LineCap::Butt,
            color,
        );
        at += 7.0;
    }
    shape
}

fn head(shape: Shape, arrow: &str, x: f32, y: f32, direction: f32, color: Color) -> Shape {
    if arrow.ends_with(">>") {
        shape.fill(
            vec![
                [x, y],
                [x - direction * 9.0, y - 4.5],
                [x - direction * 9.0, y + 4.5],
            ],
            color,
        )
    } else if arrow.ends_with(')') {
        shape.stroke(
            vec![
                [x - direction * 8.0, y - 5.0],
                [x, y],
                [x - direction * 8.0, y + 5.0],
            ],
            1.25,
            color,
        )
    } else if arrow.ends_with('x') {
        shape
            .line(
                [x - direction * 9.0, y - 4.5],
                [x - direction, y + 4.5],
                1.25,
                LineCap::Butt,
                color,
            )
            .line(
                [x - direction, y - 4.5],
                [x - direction * 9.0, y + 4.5],
                1.25,
                LineCap::Butt,
                color,
            )
    } else {
        shape
    }
}

/// The width the diagram of `flow` draws at.
pub(super) fn width(flow: &ReviewFlow) -> f32 {
    layout(flow).width
}
