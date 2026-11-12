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
