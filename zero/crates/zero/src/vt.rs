use anyhow::Result;
use libghostty_vt::render::{CellIterator, Dirty, RenderState, RowIterator};
use libghostty_vt::screen::CellWide;
use libghostty_vt::style::{RgbColor, Underline};
use libghostty_vt::terminal::Terminal;
use ratatui::Frame;
use ratatui::layout::Rect;
use ratatui::style::{Color, Modifier, Style};

pub struct VtRenderer {
    state: RenderState<'static>,
    rows: RowIterator<'static>,
    cells: CellIterator<'static>,
    text: String,
    cached: Vec<CachedCell>,
    cached_size: (u16, u16),
    cursor: Option<(u16, u16)>,
}

#[derive(Clone)]
struct CachedCell {
    symbol: String,
    style: Style,
}

impl VtRenderer {
    pub fn new() -> Result<Self> {
        Ok(Self {
            state: RenderState::new()?,
            rows: RowIterator::new()?,
            cells: CellIterator::new()?,
            text: String::with_capacity(16),
            cached: Vec::new(),
            cached_size: (0, 0),
            cursor: None,
        })
    }

    pub fn render(
        &mut self,
        frame: &mut Frame<'_>,
        terminal: &Terminal<'static, 'static>,
        area: Rect,
        focused: bool,
        refresh: bool,
    ) -> Result<()> {
        if area.is_empty() {
            return Ok(());
        }
        if refresh || self.cached_size != (area.width, area.height) {
            self.refresh(terminal, area.width, area.height)?;
        }
        for y in 0..area.height {
            for x in 0..area.width {
                let cached = &self.cached[y as usize * area.width as usize + x as usize];
                if let Some(target) = frame.buffer_mut().cell_mut((area.x + x, area.y + y)) {
                    target.set_symbol(&cached.symbol).set_style(cached.style);
                }
            }
        }
        if focused
            && let Some((x, y)) = self.cursor
            && x < area.width
            && y < area.height
        {
            frame.set_cursor_position((area.x + x, area.y + y));
        }
        Ok(())
    }

    fn refresh(
        &mut self,
        terminal: &Terminal<'static, 'static>,
        width: u16,
        height: u16,
    ) -> Result<()> {
        {
            let snapshot = self.state.update(terminal)?;
            let colors = snapshot.colors()?;
            self.cursor = if snapshot.cursor_visible()? {
                snapshot
                    .cursor_viewport()?
                    .map(|cursor| (cursor.x, cursor.y))
            } else {
                None
            };
            let blank = CachedCell {
                symbol: " ".to_string(),
                style: Style::new()
                    .fg(rgb(colors.foreground))
                    .bg(rgb(colors.background)),
            };
            self.cached = vec![blank; width as usize * height as usize];
            self.cached_size = (width, height);
            {
                let mut rows = self.rows.update(&snapshot)?;
                let mut y = 0u16;
                while let Some(row) = rows.next() {
                    if y >= height {
                        break;
                    }
                    {
                        let mut cells = self.cells.update(row)?;
                        let mut x = 0u16;
                        while let Some(cell) = cells.next() {
                            if x >= width {
                                break;
                            }
                            let raw_style =
                                cell.has_styling()?.then(|| cell.style()).transpose()?;
                            let mut style = Style::new()
                                .fg(rgb(cell.fg_color()?.unwrap_or(colors.foreground)))
                                .bg(rgb(cell.bg_color()?.unwrap_or(colors.background)));
                            let mut invisible = false;
                            if let Some(raw) = raw_style {
                                let mut modifiers = Modifier::empty();
                                modifiers.set(Modifier::BOLD, raw.bold);
                                modifiers.set(Modifier::ITALIC, raw.italic);
                                modifiers.set(Modifier::DIM, raw.faint);
                                modifiers.set(Modifier::SLOW_BLINK, raw.blink);
                                modifiers.set(Modifier::REVERSED, raw.inverse);
                                modifiers.set(Modifier::CROSSED_OUT, raw.strikethrough);
                                modifiers.set(
                                    Modifier::UNDERLINED,
                                    !matches!(raw.underline, Underline::None),
                                );
                                style = style.add_modifier(modifiers);
                                invisible = raw.invisible;
                            }
                            let wide = cell.raw_cell()?.wide()?;
                            let symbol = if invisible
                                || matches!(wide, CellWide::SpacerTail | CellWide::SpacerHead)
                                || cell.graphemes_len()? == 0
                            {
                                " ".to_string()
                            } else {
                                self.text.clear();
                                cell.graphemes_utf8(&mut self.text)?;
                                self.text.clone()
                            };
                            self.cached[y as usize * width as usize + x as usize] =
                                CachedCell { symbol, style };
                            x += 1;
                        }
                    }
                    row.set_dirty(false)?;
                    y += 1;
                }
            }
            snapshot.set_dirty(Dirty::Clean)?;
        }
        Ok(())
    }
}

fn rgb(color: RgbColor) -> Color {
    Color::Rgb(color.r, color.g, color.b)
}
