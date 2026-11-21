//! Kitty's Unicode placeholders: a program shows a virtual placement by writing U+10EEEE in each
//! cell it covers. The cell's foreground names the image, its underline color the placement, and
//! its diacritics the image row, the image column and the image id's high byte. Read and laid out
//! as Ghostty does (`src/terminal/kitty/graphics_unicode.zig`), which follows Kitty.

use super::{Placed, Virtual};

pub const PLACEHOLDER: u32 = 0x10EEEE;

/// What one placeholder cell names. A row or column it leaves out continues the cell to its left.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Placeholder {
    image_low: u32,
    image_high: Option<u32>,
    placement_id: u32,
    row: Option<u32>,
    col: Option<u32>,
}

impl Placeholder {
    /// The cell whose foreground and underline colors read as `fg` and `underline` (a palette
    /// index, 0xRRGGBB, or 0 when unset), with `marks` after the placeholder. A mark that is not
    /// one of Kitty's diacritics counts as left out, and marks past the third are ignored.
    pub fn read(fg: u32, underline: u32, marks: &[u32]) -> Placeholder {
        let index = |at: usize| marks.get(at).and_then(|&cp| diacritic(cp));
        Placeholder {
            image_low: fg,
            image_high: index(2).filter(|&high| high <= 0xFF),
            placement_id: underline,
            row: index(0),
            col: index(1),
        }
    }
}

/// Neighbouring placeholder cells of one row that show one strip of an image: `width` cells from
/// column `col`, showing the image's cells from `image_row`, `image_col`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct Run {
    pub col: u16,
    pub width: u16,
    pub image_id: u32,
    pub placement_id: u32,
    pub image_row: u32,
    pub image_col: u32,
}

/// A row's placeholder cells, by column, grouped into runs. A cell joins the run to its left
/// when it names the same image and placement and continues its row and column.
pub fn runs(cells: impl IntoIterator<Item = (u16, Placeholder)>) -> Vec<Run> {
    let mut runs = Vec::new();
    let mut open: Option<(Run, Placeholder)> = None;
    for (col, cell) in cells {
        if let Some((run, first)) = open.as_mut()
            && col == run.col + run.width
            && cell.image_low == first.image_low
            && cell.placement_id == first.placement_id
            && cell.row.is_none_or(|row| row == run.image_row)
            && cell
                .col
                .is_none_or(|c| c == run.image_col + u32::from(run.width))
            && (cell.image_high.is_none() || cell.image_high == first.image_high)
        {
            run.width += 1;
            continue;
        }
        runs.extend(open.take().map(|(run, _)| run));
        let run = Run {
            col,
            width: 1,
            image_id: cell.image_low | (cell.image_high.unwrap_or(0) << 24),
            placement_id: cell.placement_id,
            image_row: cell.row.unwrap_or(0),
            image_col: cell.col.unwrap_or(0),
        };
        open = Some((run, cell));
    }
    runs.extend(open.map(|(run, _)| run));
    runs
}

/// Where `run` draws `target`'s `image`-sized picture, in `cell`-sized cells of device pixels,
/// on screen row `row`: the image scaled to fit the placement's cells and centred in them, and
/// the strip of it the run's cells cover. None when the run covers only the margin around it.
pub fn strip(
    run: &Run,
    target: &Virtual,
    image: (u32, u32),
    cell: (u32, u32),
    row: u32,
) -> Option<Placed> {
    let cells = |asked: u32, pixels: u32, side: u32| match asked {
        0 => pixels.div_ceil(side),
        asked => asked,
    };
    if cell.0 == 0 || cell.1 == 0 {
        return None;
    }
    let columns = f64::from(cells(target.columns, image.0, cell.0));
    let rows = f64::from(cells(target.rows, image.1, cell.1));
    if columns == 0.0 || rows == 0.0 {
        return None;
    }
    let (image_w, image_h) = (f64::from(image.0), f64::from(image.1));
    let (cell_w, cell_h) = (f64::from(cell.0), f64::from(cell.1));
    let (grid_w, grid_h) = (columns * cell_w, rows * cell_h);
    let (scale, margin_x, margin_y) = if image_w * grid_h > image_h * grid_w {
        let scale = grid_w / image_w.max(1.0);
        (scale, 0.0, (grid_h - image_h * scale) / 2.0 / scale)
    } else {
        let scale = grid_h / image_h.max(1.0);
        (scale, (grid_w - image_w * scale) / 2.0 / scale, 0.0)
    };
    let width = f64::from(run.width);
    let (x, source_w, offset_x, dest_w) = fit_axis(
        image_w,
        margin_x,
        scale,
        (f64::from(run.image_col), width, columns),
        width * cell_w,
    );
    let (y, source_h, offset_y, dest_h) = fit_axis(
        image_h,
        margin_y,
        scale,
        (f64::from(run.image_row), 1.0, rows),
        cell_h,
    );
    let px = |n: f64| n.round().max(0.0) as u32;
    let source = [px(x), px(y), px(source_w), px(source_h)];
    if source[2] == 0 || source[3] == 0 {
        return None;
    }
    Some(Placed {
        image_id: target.image_id,
        placement_id: run.placement_id,
        generation: target.generation,
        z: -1,
        row,
        col: i32::from(run.col),
        offset: [px(offset_x), px(offset_y)],
        pixel_width: px(dest_w),
        pixel_height: px(dest_h),
        source,
    })
}

/// One axis of a strip: the run covers `span` of the placement's `total` cells from `start`, of
/// an image `image` pixels long with `margin` image pixels of padding on each side. Gives the
/// source start and length in image pixels, and the destination offset and length in device
/// pixels, starting from `dest` for the run's whole cells.
fn fit_axis(
    image: f64,
    margin: f64,
    scale: f64,
    (start, span, total): (f64, f64, f64),
    dest: f64,
) -> (f64, f64, f64, f64) {
    let padded = image + margin * 2.0;
    let mut source = padded * (start / total);
    let mut length = padded * (span / total);
    let mut offset = 0.0;
    let mut dest = dest;
    if source < margin {
        offset = margin - source;
        length -= offset;
        dest -= offset * scale;
        source = 0.0;
        if length > image {
            length = image;
            dest = image * scale;
        }
    } else if source + length > padded - margin {
        source -= margin;
        length = image - source;
        dest = length * scale;
    } else {
        source -= margin;
    }
    (source, length, offset * scale, dest)
}

/// The row or column a diacritic stands for, or None when it is not one of Kitty's.
fn diacritic(cp: u32) -> Option<u32> {
    DIACRITICS.binary_search(&cp).ok().map(|at| at as u32)
}

/// Kitty's row and column diacritics, in order: the index is the value. From
/// https://sw.kovidgoyal.net/kitty/_downloads/f0a0de9ec8d9ff4456206db8e0814937/rowcolumn-diacritics.txt
const DIACRITICS: [u32; 297] = [
    0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F, 0x0346, 0x034A, 0x034B, 0x034C,
    0x0350, 0x0351, 0x0352, 0x0357, 0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
    0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484, 0x0485, 0x0486, 0x0487, 0x0592,
    0x0593, 0x0594, 0x0595, 0x0597, 0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1,
    0x05A8, 0x05A9, 0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611, 0x0612, 0x0613, 0x0614, 0x0615,
    0x0616, 0x0617, 0x0657, 0x0658, 0x0659, 0x065A, 0x065B, 0x065D, 0x065E, 0x06D6, 0x06D7, 0x06D8,
    0x06D9, 0x06DA, 0x06DB, 0x06DC, 0x06DF, 0x06E0, 0x06E1, 0x06E2, 0x06E4, 0x06E7, 0x06E8, 0x06EB,
    0x06EC, 0x0730, 0x0732, 0x0733, 0x0735, 0x0736, 0x073A, 0x073D, 0x073F, 0x0740, 0x0741, 0x0743,
    0x0745, 0x0747, 0x0749, 0x074A, 0x07EB, 0x07EC, 0x07ED, 0x07EE, 0x07EF, 0x07F0, 0x07F1, 0x07F3,
    0x0816, 0x0817, 0x0818, 0x0819, 0x081B, 0x081C, 0x081D, 0x081E, 0x081F, 0x0820, 0x0821, 0x0822,
    0x0823, 0x0825, 0x0826, 0x0827, 0x0829, 0x082A, 0x082B, 0x082C, 0x082D, 0x0951, 0x0953, 0x0954,
    0x0F82, 0x0F83, 0x0F86, 0x0F87, 0x135D, 0x135E, 0x135F, 0x17DD, 0x193A, 0x1A17, 0x1A75, 0x1A76,
    0x1A77, 0x1A78, 0x1A79, 0x1A7A, 0x1A7B, 0x1A7C, 0x1B6B, 0x1B6D, 0x1B6E, 0x1B6F, 0x1B70, 0x1B71,
    0x1B72, 0x1B73, 0x1CD0, 0x1CD1, 0x1CD2, 0x1CDA, 0x1CDB, 0x1CE0, 0x1DC0, 0x1DC1, 0x1DC3, 0x1DC4,
    0x1DC5, 0x1DC6, 0x1DC7, 0x1DC8, 0x1DC9, 0x1DCB, 0x1DCC, 0x1DD1, 0x1DD2, 0x1DD3, 0x1DD4, 0x1DD5,
    0x1DD6, 0x1DD7, 0x1DD8, 0x1DD9, 0x1DDA, 0x1DDB, 0x1DDC, 0x1DDD, 0x1DDE, 0x1DDF, 0x1DE0, 0x1DE1,
    0x1DE2, 0x1DE3, 0x1DE4, 0x1DE5, 0x1DE6, 0x1DFE, 0x20D0, 0x20D1, 0x20D4, 0x20D5, 0x20D6, 0x20D7,
    0x20DB, 0x20DC, 0x20E1, 0x20E7, 0x20E9, 0x20F0, 0x2CEF, 0x2CF0, 0x2CF1, 0x2DE0, 0x2DE1, 0x2DE2,
    0x2DE3, 0x2DE4, 0x2DE5, 0x2DE6, 0x2DE7, 0x2DE8, 0x2DE9, 0x2DEA, 0x2DEB, 0x2DEC, 0x2DED, 0x2DEE,
    0x2DEF, 0x2DF0, 0x2DF1, 0x2DF2, 0x2DF3, 0x2DF4, 0x2DF5, 0x2DF6, 0x2DF7, 0x2DF8, 0x2DF9, 0x2DFA,
    0x2DFB, 0x2DFC, 0x2DFD, 0x2DFE, 0x2DFF, 0xA66F, 0xA67C, 0xA67D, 0xA6F0, 0xA6F1, 0xA8E0, 0xA8E1,
    0xA8E2, 0xA8E3, 0xA8E4, 0xA8E5, 0xA8E6, 0xA8E7, 0xA8E8, 0xA8E9, 0xA8EA, 0xA8EB, 0xA8EC, 0xA8ED,
    0xA8EE, 0xA8EF, 0xA8F0, 0xA8F1, 0xAAB0, 0xAAB2, 0xAAB3, 0xAAB7, 0xAAB8, 0xAABE, 0xAABF, 0xAAC1,
    0xFE20, 0xFE21, 0xFE22, 0xFE23, 0xFE24, 0xFE25, 0xFE26, 0x10A0F, 0x10A38, 0x1D185, 0x1D186,
    0x1D187, 0x1D188, 0x1D189, 0x1D1AA, 0x1D1AB, 0x1D1AC, 0x1D1AD, 0x1D242, 0x1D243, 0x1D244,
];
