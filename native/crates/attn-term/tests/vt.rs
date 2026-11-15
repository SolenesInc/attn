use attn_term::{Grid, Vt, Width};

fn screen(vt: &mut Vt) -> Grid {
    let mut grid = Grid::default();
    vt.sync(&mut grid);
    grid
}

#[test]
fn a_snapshot_restores_the_screen_it_was_taken_from() {
    let mut vt = Vt::new(20, 4);
    vt.write(b"hello\r\n\x1b[1mbold\x1b[0m \xe6\x97\xa5\xe6\x9c\xac");
    let mut restored = Vt::restore(&vt.snapshot().expect("a screen at rest encodes"))
        .expect("the snapshot decodes");
    let grid = screen(&mut restored);
    assert_eq!((grid.cols, grid.rows), (20, 4));
    assert_eq!(grid.row_text(0), "hello");
    assert_eq!(grid.row_text(1), "bold 日本");
    assert_eq!(grid.row(1)[5].width, Width::Wide);
    assert_eq!(grid.row(1)[6].width, Width::Spacer);
}

#[test]
fn bytes_that_are_not_a_snapshot_restore_nothing() {
    assert!(Vt::restore(b"not a snapshot").is_none());
}

#[test]
fn only_rows_that_changed_are_read_again() {
    let mut vt = Vt::new(10, 3);
    vt.write(b"one\r\ntwo");
    let mut grid = screen(&mut vt);
    grid.dirty.iter_mut().for_each(|d| *d = false);
    assert!(!vt.sync(&mut grid), "nothing changed since the last read");
    vt.write(b"\r\nthree");
    assert!(vt.sync(&mut grid));
    assert_eq!(grid.row_text(2), "three");
    assert!(!grid.dirty[0], "the first row did not change");
    assert!(grid.dirty[2]);
}

#[test]
fn a_resize_keeps_the_rows_the_daemon_wrote() {
    let mut vt = Vt::new(10, 3);
    vt.write(b"abcdefghijkl");
    vt.resize(6, 3, (8, 16));
    let grid = screen(&mut vt);
    assert_eq!(grid.row_text(0), "abcdef");
    assert_eq!(grid.row_text(1), "kl");
    assert!(vt.dec_mode(7), "wraparound is back on after the resize");
}

#[test]
fn every_clipboard_write_is_found_wherever_the_output_splits_among_other_escapes_and_multibyte_text()
 {
    let output = "é\x1b[31m\x1b]52;c;YQ==\x07\x1b]0;title\x07ü\x1b]52;c;Yg==\x1b\\\x1b[0m\x1b]52;c;?\x07\x1b"
        .as_bytes();
    for split in 0..=output.len() {
        let mut vt = Vt::new(20, 4);
        vt.write(&output[..split]);
        let mut got = vt.take_clipboard_writes();
        vt.write(&output[split..]);
        got.extend(vt.take_clipboard_writes());
        assert_eq!(got, ["a", "b"], "split at {split}");
    }
}

#[test]
fn clipboard_writes_are_read_whole_from_split_chunks_with_either_terminator() {
    let cases: &[(&[&[u8]], &[&str])] = &[
        (&[b"\x1b]52;c;aGVsbG8=\x07"], &["hello"]),
        (&[b"x\x1b]52;p;d29ybGQ=\x1b\\y"], &["world"]),
        (&[b"\x1b]5", b"2;c;aGk=", b"\x07"], &["hi"]),
        (&[b"\x1b]52;c;?\x07"], &[]),
        (&[b"\x1b]52;c;!!notbase64\x07"], &[]),
        (&[b"\x1b]52;c;\x07"], &[""]),
        (&[b"\x1b]52;c;aGk\x07"], &["hi"]),
        (&[b"\x1b]52;c;?\x1b\\\x1b]52;c;d29ybGQ=\x1b\\"], &["world"]),
    ];
    for (chunks, want) in cases {
        let mut vt = Vt::new(20, 4);
        let got: Vec<String> = chunks
            .iter()
            .flat_map(|c| {
                vt.write(c);
                vt.take_clipboard_writes()
            })
            .collect();
        assert_eq!(got, *want, "{chunks:?}");
    }
}
