use std::ffi::c_void;
use std::io::Cursor;
use std::panic::{AssertUnwindSafe, catch_unwind};

const MAX_DIMENSION: u32 = 10_000;
const MAX_BYTES: usize = 400 * 1024 * 1024;

unsafe extern "C" {
    fn attn_ghostty_alloc(allocator: *const c_void, len: usize) -> *mut u8;
    fn attn_ghostty_set_decoded_image(
        out: *mut c_void,
        width: u32,
        height: u32,
        data: *mut u8,
        data_len: usize,
    );
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn attn_decode_png(
    _userdata: *mut c_void,
    allocator: *const c_void,
    data: *const u8,
    data_len: usize,
    out: *mut c_void,
) -> bool {
    catch_unwind(AssertUnwindSafe(|| {
        decode_png(allocator, data, data_len, out)
    }))
    .unwrap_or(false)
}

fn decode_png(
    allocator: *const c_void,
    data: *const u8,
    data_len: usize,
    out: *mut c_void,
) -> bool {
    if data.is_null() || data_len == 0 || out.is_null() {
        return false;
    }
    let bytes = unsafe { std::slice::from_raw_parts(data, data_len) };
    let mut decoder = png::Decoder::new(Cursor::new(bytes));
    decoder.set_transformations(png::Transformations::EXPAND | png::Transformations::STRIP_16);
    let Ok(mut reader) = decoder.read_info() else {
        return false;
    };
    let width = reader.info().width;
    let height = reader.info().height;
    let rgba_len = match usize::try_from(width)
        .ok()
        .and_then(|width| {
            usize::try_from(height)
                .ok()
                .and_then(|height| width.checked_mul(height))
        })
        .and_then(|pixels| pixels.checked_mul(4))
    {
        Some(len)
            if width > 0
                && height > 0
                && width <= MAX_DIMENSION
                && height <= MAX_DIMENSION
                && len <= MAX_BYTES =>
        {
            len
        }
        _ => return false,
    };
    let Some(frame_len) = reader.output_buffer_size() else {
        return false;
    };
    let mut frame = vec![0; frame_len];
    let Ok(info) = reader.next_frame(&mut frame) else {
        return false;
    };
    let source = &frame[..info.buffer_size()];
    let mut rgba = Vec::with_capacity(rgba_len);
    match info.color_type {
        png::ColorType::Rgba => rgba.extend_from_slice(source),
        png::ColorType::Rgb => {
            for pixel in source.chunks_exact(3) {
                rgba.extend_from_slice(&[pixel[0], pixel[1], pixel[2], 255]);
            }
        }
        png::ColorType::GrayscaleAlpha => {
            for pixel in source.chunks_exact(2) {
                rgba.extend_from_slice(&[pixel[0], pixel[0], pixel[0], pixel[1]]);
            }
        }
        png::ColorType::Grayscale => {
            for value in source {
                rgba.extend_from_slice(&[*value, *value, *value, 255]);
            }
        }
        png::ColorType::Indexed => return false,
    }
    if rgba.len() != rgba_len {
        return false;
    }
    let target = unsafe { attn_ghostty_alloc(allocator, rgba.len()) };
    if target.is_null() {
        return false;
    }
    unsafe {
        std::ptr::copy_nonoverlapping(rgba.as_ptr(), target, rgba.len());
        attn_ghostty_set_decoded_image(out, width, height, target, rgba.len());
    }
    true
}
