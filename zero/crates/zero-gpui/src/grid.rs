//! Paints a libghostty-vt terminal into a GPUI window: backgrounds as quads, text as shaped runs
//! positioned per cell so wide glyphs and fallback fonts never drift the columns.
use anyhow::Result;
use gpui::{
    App, BorderStyle, Bounds, Font, FontStyle, FontWeight, Hsla, Pixels, Point, SharedString,
    ShapedLine, StrikethroughStyle, TextRun, UnderlineStyle, Window, fill, font, point, px, quad,
    size,
};
use libghostty_vt::render::{CellIteration, CellIterator, Dirty, RenderState, RowIterator};
use libghostty_vt::screen::CellWide;
use libghostty_vt::style::{RgbColor, StyleColor, Underline};
use libghostty_vt::terminal::Terminal;

use crate::theme;

pub struct Metrics {
    pub font: Font,
    pub font_size: Pixels,
    pub cell_width: Pixels,
    pub line_height: Pixels,
}

impl Metrics {
    pub fn measure(window: &Window) -> Result<Self> {
        let font = font("JetBrains Mono");
        let font_size = px(13.);
        let text_system = window.text_system();
        let font_id = text_system.resolve_font(&font);
        let cell_width = text_system.advance(font_id, font_size, 'M')?.width;
        Ok(Self {
            font,
            font_size,
            cell_width,
            line_height: px((f32::from(font_size) * 1.32).round()),
        })
    }
}

#[derive(Clone, Debug)]
struct Cell {
    text: String,
    fg: Hsla,
    bg: Option<Hsla>,
    bold: bool,
    italic: bool,
    underline: bool,
    strikethrough: bool,
    wide: bool,
}

pub struct Grid {
    state: RenderState<'static>,
    rows: RowIterator<'static>,
    cells: CellIterator<'static>,
    scratch: String,
    lines: Vec<Vec<Cell>>,
    cursor: Option<(u16, u16)>,
    size: (u16, u16),
}

impl Grid {
    pub fn new() -> Result<Self> {
        Ok(Self {
            state: RenderState::new()?,
            rows: RowIterator::new()?,
            cells: CellIterator::new()?,
            scratch: String::with_capacity(16),
            lines: Vec::new(),
            cursor: None,
            size: (0, 0),
        })
    }

    pub fn size(&self) -> (u16, u16) {
        self.size
    }

    pub fn refresh(&mut self, terminal: &Terminal<'static, 'static>, cols: u16, rows: u16) -> Result<()> {
        let mut lines = Vec::with_capacity(rows as usize);
        {
            let snapshot = self.state.update(terminal)?;
            self.cursor = if snapshot.cursor_visible()? {
                snapshot.cursor_viewport()?.map(|cursor| (cursor.x, cursor.y))
            } else {
                None
            };
            {
                let mut row_iter = self.rows.update(&snapshot)?;
                while let Some(row) = row_iter.next() {
                    if lines.len() >= rows as usize {
                        break;
                    }
                    let mut line = Vec::with_capacity(cols as usize);
                    {
                        let mut cell_iter = self.cells.update(row)?;
                        while let Some(cell) = cell_iter.next() {
                            if line.len() >= cols as usize {
                                break;
                            }
                            line.push(convert(cell, &mut self.scratch)?);
                        }
                    }
                    row.set_dirty(false)?;
                    lines.push(line);
                }
            }
            snapshot.set_dirty(Dirty::Clean)?;
        }
        self.lines = lines;
        self.size = (cols, rows);
        Ok(())
    }

    pub fn prepare(
        &self,
        metrics: &Metrics,
        origin: Point<Pixels>,
        focused: bool,
        cursor_color: Hsla,
        window: &mut Window,
    ) -> Prepared {
        let (cw, lh) = (metrics.cell_width, metrics.line_height);
        let mut quads = Vec::new();
        let mut lines = Vec::new();
        for (y, row) in self.lines.iter().enumerate() {
            let top = origin.y + lh * y as f32;
            let mut x = 0;
            while x < row.len() {
                let Some(color) = row[x].bg else {
                    x += 1;
                    continue;
                };
                let start = x;
                while x < row.len() && row[x].bg == Some(color) {
                    x += 1;
                }
                quads.push((
                    Bounds {
                        origin: point(origin.x + cw * start as f32, top),
                        size: size(cw * (x - start) as f32, lh),
                    },
                    color,
                ));
            }
            let mut segment = Segment::new(0);
            for (x, cell) in row.iter().enumerate() {
                if cell.wide {
                    segment.flush(metrics, origin, top, window, &mut lines);
                    let mut wide = Segment::new(x);
                    wide.push(cell, metrics);
                    wide.flush(metrics, origin, top, window, &mut lines);
                    segment = Segment::new(x + 1);
                } else {
                    segment.push(cell, metrics);
                }
            }
            segment.flush(metrics, origin, top, window, &mut lines);
        }
        let cursor = self.cursor.map(|(x, y)| {
            (
                Bounds {
                    origin: point(origin.x + cw * x as f32, origin.y + lh * y as f32),
                    size: size(cw, lh),
                },
                cursor_color,
                focused,
            )
        });
        Prepared { quads, lines, cursor, line_height: lh }
    }
}

struct Segment {
    column: usize,
    text: String,
    runs: Vec<TextRun>,
    last_visible: usize,
}

impl Segment {
    fn new(column: usize) -> Self {
        Self { column, text: String::new(), runs: Vec::new(), last_visible: 0 }
    }

    fn push(&mut self, cell: &Cell, metrics: &Metrics) {
        let piece = if cell.text.is_empty() { " " } else { cell.text.as_str() };
        self.text.push_str(piece);
        if piece != " " || cell.underline || cell.strikethrough {
            self.last_visible = self.text.len();
        }
        let font = Font {
            weight: if cell.bold { FontWeight::BOLD } else { FontWeight::NORMAL },
            style: if cell.italic { FontStyle::Italic } else { FontStyle::Normal },
            ..metrics.font.clone()
        };
        let underline = cell.underline.then(|| UnderlineStyle {
            thickness: px(1.),
            color: Some(cell.fg),
            wavy: false,
        });
        let strikethrough = cell
            .strikethrough
            .then(|| StrikethroughStyle { thickness: px(1.), color: Some(cell.fg) });
        if let Some(last) = self.runs.last_mut()
            && last.font == font
            && last.color == cell.fg
            && last.underline == underline
            && last.strikethrough == strikethrough
        {
            last.len += piece.len();
            return;
        }
        self.runs.push(TextRun {
            len: piece.len(),
            font,
            color: cell.fg,
            background_color: None,
            underline,
            strikethrough,
        });
    }

    fn flush(
        &mut self,
        metrics: &Metrics,
        origin: Point<Pixels>,
        top: Pixels,
        window: &mut Window,
        lines: &mut Vec<(Point<Pixels>, ShapedLine)>,
    ) {
        if self.last_visible == 0 {
            return;
        }
        let mut remaining = self.text.len() - self.last_visible;
        while remaining > 0 {
            let last = self.runs.last_mut().expect("runs cover the text");
            let cut = last.len.min(remaining);
            last.len -= cut;
            remaining -= cut;
            if last.len == 0 {
                self.runs.pop();
            }
        }
        self.text.truncate(self.last_visible);
        let line = window.text_system().shape_line(
            SharedString::from(std::mem::take(&mut self.text)),
            metrics.font_size,
            &self.runs,
            None,
        );
        lines.push((point(origin.x + metrics.cell_width * self.column as f32, top), line));
        self.runs.clear();
        self.last_visible = 0;
    }
}

pub struct Prepared {
    quads: Vec<(Bounds<Pixels>, Hsla)>,
    lines: Vec<(Point<Pixels>, ShapedLine)>,
    cursor: Option<(Bounds<Pixels>, Hsla, bool)>,
    line_height: Pixels,
}

impl Prepared {
    pub fn paint(self, window: &mut Window, cx: &mut App) {
        for (bounds, color) in self.quads {
            window.paint_quad(fill(bounds, color));
        }
        if let Some((bounds, color, focused)) = self.cursor {
            if focused {
                window.paint_quad(fill(bounds, color.alpha(0.6)));
            } else {
                window.paint_quad(quad(
                    bounds,
                    0.,
                    gpui::transparent_black(),
                    1.,
                    color.alpha(0.45),
                    BorderStyle::Solid,
                ));
            }
        }
        for (origin, line) in self.lines {
            line.paint(origin, self.line_height, window, cx).ok();
        }
    }
}

fn convert(cell: &CellIteration<'_, '_>, scratch: &mut String) -> Result<Cell> {
    let style = cell.has_styling()?.then(|| cell.style()).transpose()?;
    let mut fg = match style.as_ref().map(|style| &style.fg_color) {
        Some(StyleColor::Palette(index)) if usize::from(index.0) < 16 => theme::ansi(usize::from(index.0)),
        _ => cell.fg_color()?.map(to_hsla).unwrap_or_else(theme::fg),
    };
    let mut bg = match style.as_ref().map(|style| &style.bg_color) {
        Some(StyleColor::Palette(index)) if usize::from(index.0) < 16 => Some(theme::ansi(usize::from(index.0))),
        _ => cell.bg_color()?.map(to_hsla),
    };
    let mut out = Cell {
        text: String::new(),
        fg,
        bg,
        bold: false,
        italic: false,
        underline: false,
        strikethrough: false,
        wide: false,
    };
    let mut invisible = false;
    if let Some(style) = &style {
        if style.inverse {
            let old = fg;
            fg = bg.unwrap_or_else(theme::card);
            bg = Some(old);
        }
        if style.faint {
            fg = fg.alpha(0.6);
        }
        out.bold = style.bold;
        out.italic = style.italic;
        out.underline = !matches!(style.underline, Underline::None);
        out.strikethrough = style.strikethrough;
        invisible = style.invisible;
    }
    out.fg = fg;
    out.bg = bg;
    let wide = cell.raw_cell()?.wide()?;
    out.wide = matches!(wide, CellWide::Wide);
    if !invisible
        && !matches!(wide, CellWide::SpacerTail | CellWide::SpacerHead)
        && cell.graphemes_len()? > 0
    {
        scratch.clear();
        cell.graphemes_utf8(scratch)?;
        out.text = scratch.clone();
    }
    Ok(out)
}

fn to_hsla(color: RgbColor) -> Hsla {
    gpui::rgb(((color.r as u32) << 16) | ((color.g as u32) << 8) | color.b as u32).into()
}
