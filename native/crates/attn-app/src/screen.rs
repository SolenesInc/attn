use attn_term::Cells;
use attn_ui::colors::TerminalColors;
use rune::{Compositor, Frame, Runtime, RuntimeCore};

use crate::State;

/// Draws the window, with each terminal's cells in its tile's slot.
#[derive(Default)]
pub struct Screens {
    cells: Cells,
}

/// Gives the terminals the window's theme and returns it.
fn theme_terminals(rt: &mut Runtime<State>) -> TerminalColors {
    let theme = crate::colors(&rt.state().facts.settings, rt.env.system_dark).terminal;
    let state = rt.state_mut();
    state.terminals.set_theme(theme, &state.daemon);
    theme
}

impl Screens {
    /// A frame of a window with no GPU, as a wire test's world draws one: it takes the
    /// terminals' screens as `render` does and uploads nothing.
    pub fn draw_without_gpu(&mut self, rt: &mut Runtime<State>) {
        theme_terminals(rt);
        rt.state_mut().terminals.grids().for_each(drop);
    }
}

impl Compositor<State> for Screens {
    fn redraws(&self, rt: &Runtime<State>) -> bool {
        rt.state().terminals.undrawn()
    }

    fn render(&mut self, f: &mut Frame<'_, State>) {
        let (device, queue, key) = (f.device, f.queue, f.device_key());
        let scale = f.rt.env.scale;
        let theme = theme_terminals(f.rt);
        let terminals = &mut f.rt.state_mut().terminals;
        let metrics = terminals.metrics();
        self.cells
            .prepare(device, queue, key, scale, metrics, theme, terminals.grids());
        let clear = f.rt.clear_color();
        let core: &mut RuntimeCore = f.rt;
        f.ui.prepare(device, queue, &mut core.text.atlas, &core.instances, f.size);
        let cells = &mut self.cells;
        f.ui.draw_with(f.encoder, f.target, clear, &mut |pass, site| {
            cells.draw(device, queue, pass, site);
        });
    }
}
