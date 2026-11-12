use attn_term::{Cells, Theme};
use attn_ui::Appearance;
use rune::{Compositor, Frame, Runtime};

use crate::State;

/// Draws the window, with each terminal's cells in its tile's slot.
#[derive(Default)]
pub struct Screens {
    cells: Cells,
}

/// Gives the terminals the window's theme and returns it.
fn theme_terminals(rt: &mut Runtime<State>) -> Theme {
    let setting = rt.state.facts.settings.get("theme").map(String::as_str);
    let theme = Theme::of(Appearance::from_setting(setting).is_dark(rt.env.system_dark));
    let state = &mut rt.state;
    state.terminals.set_theme(theme, &state.daemon);
    theme
}

impl Screens {
    /// A frame of a window with no GPU, as a wire test's world draws one: it takes the
    /// terminals' screens as `render` does and uploads nothing.
    pub fn draw_without_gpu(&mut self, rt: &mut Runtime<State>) {
        theme_terminals(rt);
        rt.state.terminals.grids().for_each(drop);
    }
}

impl Compositor<State> for Screens {
    fn redraws(&self, rt: &Runtime<State>) -> bool {
        rt.state.terminals.undrawn()
    }

    fn render(&mut self, f: &mut Frame<'_, State>) {
        let (device, queue, key) = (f.device, f.queue, f.device_key());
        let scale = f.rt.env.scale;
        let theme = theme_terminals(f.rt);
        let terminals = &mut f.rt.state.terminals;
        let metrics = terminals.metrics();
        self.cells
            .prepare(device, queue, key, scale, metrics, theme, terminals.grids());
        let clear = f.rt.clear_color();
        f.ui.prepare(device, queue, &mut f.rt.text.atlas, &f.rt.instances, f.size);
        let cells = &mut self.cells;
        f.ui.draw_with(f.encoder, f.target, clear, &mut |pass, site| {
            cells.draw(device, queue, pass, site);
        });
    }
}
