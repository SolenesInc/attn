use attn_client::{Frame, KittyFormat};

fn kitty_frame(
    id: &str,
    image: u32,
    generation: u64,
    size: (u32, u32),
    format: u8,
    pixels: &[u8],
) -> Vec<u8> {
    let mut frame = vec![0x02, id.len() as u8];
    frame.extend_from_slice(id.as_bytes());
    frame.extend_from_slice(&image.to_be_bytes());
    frame.extend_from_slice(&generation.to_be_bytes());
    frame.extend_from_slice(&size.0.to_be_bytes());
    frame.extend_from_slice(&size.1.to_be_bytes());
    frame.push(format);
    frame.extend_from_slice(pixels);
    frame
}

#[test]
fn a_kitty_image_frame_reads_as_internal_protocol_binaryframe_go_writes_it() {
    let image = |format| {
        Ok(Frame::KittyImage {
            terminal: "s".into(),
            image: 1,
            generation: 1,
            width: 1,
            height: 1,
            format,
            pixels: vec![9; 4],
        })
    };
    let header_only = {
        let mut frame = kitty_frame("s", 1, 1, (1, 1), 0, &[1]);
        frame.pop();
        frame
    };
    let cases: Vec<(&str, Vec<u8>, Result<Frame, &str>)> = vec![
        (
            "a round trip keeps every field",
            kitty_frame("sess-42", 7, 99, (2, 1), 0, &[1, 2, 3, 4, 5, 6]),
            Ok(Frame::KittyImage {
                terminal: "sess-42".into(),
                image: 7,
                generation: 99,
                width: 2,
                height: 1,
                format: KittyFormat::Rgb,
                pixels: vec![1, 2, 3, 4, 5, 6],
            }),
        ),
        (
            "code 0 is rgb",
            kitty_frame("s", 1, 1, (1, 1), 0, &[9; 4]),
            image(KittyFormat::Rgb),
        ),
        (
            "code 1 is rgba",
            kitty_frame("s", 1, 1, (1, 1), 1, &[9; 4]),
            image(KittyFormat::Rgba),
        ),
        (
            "code 2 is gray and alpha",
            kitty_frame("s", 1, 1, (1, 1), 2, &[9; 4]),
            image(KittyFormat::GrayAlpha),
        ),
        (
            "code 3 is gray",
            kitty_frame("s", 1, 1, (1, 1), 3, &[9; 4]),
            image(KittyFormat::Gray),
        ),
        (
            "a layout it cannot name is dropped, named",
            kitty_frame("s", 1, 1, (1, 1), 9, &[1, 2, 3]),
            Err("a kitty image with unknown pixel format 9"),
        ),
        (
            "a frame with no pixels after its header is dropped",
            header_only,
            Err("a kitty image frame of 24 bytes, too short for its header and pixels"),
        ),
    ];
    for (name, bytes, want) in cases {
        let got = Frame::decode(&bytes);
        assert_eq!(
            got.as_ref().map_err(String::as_str),
            want.as_ref().map_err(|e| *e),
            "{name}"
        );
    }
}
