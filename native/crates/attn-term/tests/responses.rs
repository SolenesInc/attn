use attn_term::responses::without_daemon_owned;

#[test]
fn the_daemon_answers_cursor_attributes_and_colour_queries_itself() {
    let cases: &[(&str, &[u8], &[u8])] = &[
        ("a cursor position report", b"\x1b[24;1R", b""),
        ("an extended cursor position report", b"\x1b[?24;1;1R", b""),
        ("a DA1 report", b"\x1b[?1;2c", b""),
        (
            "an ST-ended OSC 11",
            b"\x1b]11;rgb:0000/0000/0000\x1b\\",
            b"",
        ),
        ("a BEL-ended OSC 10", b"\x1b]10;rgb:ffff/ffff/ffff\x07", b""),
        ("an OSC 12", b"\x1b]12;rgb:3b3b/3b3b/3b3b\x1b\\", b""),
        ("a DSR reply", b"\x1b[0n", b"\x1b[0n"),
        (
            "an OSC 52 write",
            b"\x1b]52;c;aGVsbG8=\x1b\\",
            b"\x1b]52;c;aGVsbG8=\x1b\\",
        ),
        (
            "replies inside text",
            b"hello\x1b[5;7R\x1b[?1;2c\x1b]11;rgb:0000/0000/0000\x1b\\world",
            b"helloworld",
        ),
        ("a secondary DA reply", b"\x1b[>1;10;0c", b"\x1b[>1;10;0c"),
        ("a lone number before R", b"\x1b[5R", b"\x1b[5R"),
    ];
    for (case, reply, kept) in cases {
        assert_eq!(
            without_daemon_owned(reply),
            *kept,
            "{case}: {:?}",
            String::from_utf8_lossy(reply)
        );
    }
}
