use attn_term::{FONT_SIZE, Metrics};

#[test]
fn fitted_grids_never_overflow_or_trigger_spurious_zoom_at_fractional_scales() {
    for cell_w in [7.0_f32, 8.0, 9.0, 10.0, 11.0] {
        for cell_h in [18.0_f32, 20.0, 21.0, 23.0] {
            let metrics = Metrics {
                font_size: FONT_SIZE,
                cell_w,
                cell_h,
            };
            for display in [1.0_f32, 1.25, 1.5, 2.0] {
                for tenths in 7..=15 {
                    let scale = display * (tenths as f32 / 10.0);
                    let (cw, ch) = metrics.device_cell(scale);
                    let (cell_w_pt, cell_h_pt) = metrics.cell_points(scale);
                    for (w, h) in [
                        (480.0_f32, 320.0_f32),
                        (774.0, 650.0),
                        (955.5, 712.25),
                        (1548.0, 900.0),
                    ] {
                        let (cols, rows) = metrics.fit(w, h, scale);
                        let (w_px, h_px) = (w * scale, h * scale);
                        assert!(
                            f32::from(cols) * cw <= w_px.floor()
                                && f32::from(rows) * ch <= h_px.floor(),
                            "cell={cell_w}x{cell_h} scale={scale} slot={w}x{h}: {cols}x{rows} at {cw}x{ch}px overflows {}x{}px",
                            w_px.floor(),
                            h_px.floor()
                        );
                        assert_eq!(
                            metrics.cell_rect((cols, rows), (w, h), scale, (0, 0)),
                            [0.0, 0.0, cell_w_pt, cell_h_pt],
                            "cell={cell_w}x{cell_h} scale={scale} slot={w}x{h}: fitted grid {cols}x{rows} shifted or zoomed cell (0, 0)"
                        );
                        assert_eq!(
                            metrics.pixels((cols, rows), scale),
                            (u32::from(cols) * cw as u32, u32::from(rows) * ch as u32),
                            "cell={cell_w}x{cell_h} scale={scale}: pixel size matches drawn cells"
                        );
                        let last =
                            metrics.cell_rect((cols, rows), (w, h), scale, (cols - 1, rows - 1));
                        assert!(
                            last[0] + last[2] <= w + 1e-3 && last[1] + last[3] <= h + 1e-3,
                            "cell={cell_w}x{cell_h} scale={scale} slot={w}x{h}: last cell {last:?} past slot"
                        );
                    }
                }
            }
        }
    }
}
