use rune::{El, Shape, canvas, col, row};

use super::block::{Block, Kind};
use crate::Tokens;
use crate::system::{Type, words};

/// How tall a chart's line runs, as tall as three lines of Body text: the height the weather
/// chart of the views mock-up (`site/2b-parts.html`) gives its line over twelve hours.
const LINE_HIGH: f32 = 60.0;
/// How wide the line strokes, as the mock-up draws it.
const STROKE: f32 = 1.5;
/// The share of the line's height left free above its highest point and below its lowest.
const ROOM: f32 = 0.1;

/// A line chart: one point per item, left to right, each with its value over its name under the
/// line, such as the temperature of each hour.
pub struct Chart {
    key: String,
    label: String,
    points: Vec<(String, String, f64)>,
}

impl Chart {
    /// A chart keyed `key`, read as `label`.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Chart {
        Chart {
            key: key.into(),
            label: label.into(),
            points: Vec::new(),
        }
    }

    /// A point named `name` at `value`, which reads as `shown`.
    pub fn point(mut self, name: impl Into<String>, shown: impl Into<String>, value: f64) -> Self {
        self.points.push((name.into(), shown.into(), value));
        self
    }

    /// The height of the line, without the words under it.
    pub fn line_height() -> f32 {
        LINE_HIGH
    }

    pub fn build(self, t: &Tokens) -> Block {
        Block::new(Kind::Custom, self.el(t))
    }

    pub fn el(self, t: &Tokens) -> El {
        let n = self.points.len().max(1);
        let (low, high) = self.points.iter().fold(
            (f64::INFINITY, f64::NEG_INFINITY),
            |(low, high), (_, _, v)| (low.min(*v), high.max(*v)),
        );
        let span = high - low;
        let points: Vec<[f32; 2]> = (self.points.iter().enumerate())
            .map(|(i, (_, _, v))| {
                let up = if span > 0.0 {
                    ((v - low) / span) as f32
                } else {
                    0.5
                };
                [i as f32 + 0.5, 1.0 - ROOM - up * (1.0 - 2.0 * ROOM)]
            })
            .collect();
        let line = canvas(
            Shape::new()
                .stroke(points, STROKE, t.accent)
                .view_box(n as f32, 1.0),
        )
        .key(format!("{}.line", self.key))
        .w_full()
        .h(LINE_HIGH)
        .a11y_hidden();
        let said: Vec<String> = (self.points.iter())
            .map(|(name, shown, _)| format!("{name}: {shown}"))
            .collect();
        let names =
            row()
                .w_full()
                .equal_columns(n)
                .children(
                    self.points
                        .into_iter()
                        .enumerate()
                        .map(|(i, (name, shown, _))| {
                            col()
                                .key(format!("{}.{i}", self.key))
                                .min_w(0.0)
                                .items_center()
                                .a11y_hidden()
                                .child(
                                    words(shown, Type::Name, t.text_primary)
                                        .single_line()
                                        .truncate(),
                                )
                                .child(
                                    words(name, Type::Small, t.text_tertiary)
                                        .single_line()
                                        .truncate(),
                                )
                        }),
                );
        col()
            .key(self.key)
            .role("img")
            .label(self.label)
            .a11y_value(said.join(", "))
            .w_full()
            .child(line)
            .child(names)
    }
}
