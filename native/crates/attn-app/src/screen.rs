use attn_term::{Cells, Theme};
use attn_ui::Appearance;
use rune::{Compositor, Frame};

use crate::State;

/// Draws the window, with each terminal's cells in its tile's slot.
#[derive(Default)]
pub struct Screens {
    cells: Cells,
}

impl Compositor<State> for Screens {
    fn render(&mut self, f: &mut Frame<'_, State>) {
        let (device, queue, key) = (f.device, f.queue, f.device_key());
        let scale = f.rt.env.scale;
        let system_dark = f.rt.env.system_dark;
        let state = &mut f.rt.state;
        let appearance =
            Appearance::from_setting(state.facts.settings.get("theme").map(String::as_str));
        let theme = Theme::of(appearance.is_dark(system_dark));
        state.terminals.set_theme(theme, &state.daemon);
        let metrics = state.terminals.metrics();
        self.cells.prepare(
            device,
            queue,
            key,
            scale,
            metrics,
            theme,
            state.terminals.grids(),
        );
        let clear = f.rt.clear_color();
        f.ui.prepare(device, queue, &mut f.rt.text.atlas, &f.rt.instances, f.size);
        let cells = &mut self.cells;
        f.ui.draw_with(f.encoder, f.target, clear, &mut |pass, site| {
            cells.draw(device, queue, pass, site);
        });
    }
}
