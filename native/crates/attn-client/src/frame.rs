/// A binary frame from the daemon, laid out as `internal/protocol/binaryframe.go` writes it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Frame {
    PtyOutput {
        terminal: String,
        seq: u32,
        data: Vec<u8>,
    },
    KittyImage {
        terminal: String,
        image: u32,
        generation: u64,
        width: u32,
        height: u32,
        format: KittyFormat,
        pixels: Vec<u8>,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum KittyFormat {
    Rgb,
    Rgba,
    GrayAlpha,
    Gray,
}

const PTY_OUTPUT: u8 = 0x01;
const KITTY_IMAGE: u8 = 0x02;
const KITTY_FIELDS: usize = 4 + 8 + 4 + 4 + 1;

impl Frame {
    /// Why a frame cannot be read, so the caller can log what it dropped.
    pub fn decode(bytes: &[u8]) -> Result<Frame, String> {
        let (&kind, rest) = bytes.split_first().ok_or("an empty binary frame")?;
        let (&id_len, rest) = rest
            .split_first()
            .ok_or("a binary frame without a terminal id")?;
        let id_len = usize::from(id_len);
        if id_len == 0 || rest.len() < id_len {
            return Err(format!(
                "a binary frame of {} bytes whose terminal id needs {id_len}",
                bytes.len()
            ));
        }
        let terminal = String::from_utf8(rest[..id_len].to_vec())
            .map_err(|_| "a terminal id that is not UTF-8")?;
        let rest = &rest[id_len..];
        match kind {
            PTY_OUTPUT => {
                let (seq, data) = rest
                    .split_first_chunk::<4>()
                    .ok_or("a PTY frame without its sequence number")?;
                if data.is_empty() {
                    return Err("a PTY frame without output".into());
                }
                Ok(Frame::PtyOutput {
                    terminal,
                    seq: u32::from_be_bytes(*seq),
                    data: data.to_vec(),
                })
            }
            KITTY_IMAGE => {
                if rest.len() <= KITTY_FIELDS {
                    return Err(format!(
                        "a kitty image frame of {} bytes, too short for its header and pixels",
                        bytes.len()
                    ));
                }
                let be32 = |at: usize| u32::from_be_bytes(rest[at..at + 4].try_into().unwrap());
                let format = match rest[20] {
                    0 => KittyFormat::Rgb,
                    1 => KittyFormat::Rgba,
                    2 => KittyFormat::GrayAlpha,
                    3 => KittyFormat::Gray,
                    code => return Err(format!("a kitty image with unknown pixel format {code}")),
                };
                Ok(Frame::KittyImage {
                    terminal,
                    image: be32(0),
                    generation: u64::from_be_bytes(rest[4..12].try_into().unwrap()),
                    width: be32(12),
                    height: be32(16),
                    format,
                    pixels: rest[KITTY_FIELDS..].to_vec(),
                })
            }
            other => Err(format!("a binary frame of unknown type 0x{other:02x}")),
        }
    }
}
