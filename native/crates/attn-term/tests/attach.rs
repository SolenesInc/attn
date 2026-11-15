use attn_term::attach::{
    Attached, Chunk, Output, Policy, QUEUED_OUTPUT, Restore, Snapshot, Stream, decodable,
    resize_after, restore,
};

const OURS: &str = "abc123";

fn chunk(seq: u32) -> Chunk {
    Chunk {
        seq,
        data: format!("#{seq}").into_bytes(),
    }
}

fn seqs(chunks: &[Chunk]) -> Vec<u32> {
    chunks.iter().map(|c| c.seq).collect()
}

fn snapshot(format: Option<&str>) -> Snapshot<'_> {
    Snapshot {
        cols: 90,
        rows: 30,
        bytes: b"snap",
        format,
    }
}

#[test]
fn only_a_snapshot_in_this_builds_format_decodes() {
    let cases = [
        (Some("abc123"), true),
        (Some(" abc123\n"), true),
        (Some("older"), false),
        (Some(""), false),
        (None, false),
    ];
    for (format, expected) in cases {
        assert_eq!(decodable(format, OURS), expected, "{format:?}");
    }
}

#[test]
fn an_attach_restores_from_the_snapshot_or_from_the_clients_own_watermark() {
    struct Case {
        name: &'static str,
        format: Option<Option<&'static str>>,
        policy: Policy,
        last_seq: Option<u32>,
        previous: Option<u32>,
        queued: Vec<u32>,
        size: Option<(u16, u16)>,
        snapshot: bool,
        baseline: u32,
        write: Vec<u32>,
        fallback: Vec<u32>,
    }
    let cases = [
        Case {
            name: "a snapshot continues from the daemon's last_seq",
            format: Some(Some(OURS)),
            policy: Policy::SameAppRemount,
            last_seq: Some(10),
            previous: None,
            queued: vec![9, 10, 11, 12],
            size: Some((90, 30)),
            snapshot: true,
            baseline: 12,
            write: vec![11, 12],
            fallback: vec![9, 10],
        },
        Case {
            name: "output already drawn before a re-attach is not a fallback",
            format: Some(Some(OURS)),
            policy: Policy::SameAppRemount,
            last_seq: Some(10),
            previous: Some(9),
            queued: vec![8, 9, 10, 11],
            size: Some((90, 30)),
            snapshot: true,
            baseline: 11,
            write: vec![11],
            fallback: vec![10],
        },
        Case {
            name: "without a snapshot the client's watermark keeps queued output",
            format: None,
            policy: Policy::SameAppRemount,
            last_seq: Some(50),
            previous: Some(3),
            queued: vec![3, 4, 5],
            size: None,
            snapshot: false,
            baseline: 5,
            write: vec![4, 5],
            fallback: vec![],
        },
        Case {
            name: "a snapshot in another format is no snapshot",
            format: Some(Some("older")),
            policy: Policy::SameAppRemount,
            last_seq: Some(50),
            previous: None,
            queued: vec![1, 2],
            size: None,
            snapshot: false,
            baseline: 2,
            write: vec![1, 2],
            fallback: vec![],
        },
        Case {
            name: "a relaunch without a snapshot takes the PTY's size",
            format: None,
            policy: Policy::RelaunchRestore,
            last_seq: None,
            previous: None,
            queued: vec![],
            size: Some((100, 40)),
            snapshot: false,
            baseline: 0,
            write: vec![],
            fallback: vec![],
        },
    ];
    for case in cases {
        let attached = Attached {
            cols: Some(100),
            rows: Some(40),
            last_seq: case.last_seq,
            snapshot: case.format.map(snapshot),
        };
        let Restore {
            size,
            snapshot,
            baseline,
            write,
            fallback,
        } = restore(
            &attached,
            case.policy,
            case.previous,
            case.queued.iter().map(|&s| chunk(s)),
            OURS,
        );
        assert_eq!(size, case.size, "{}", case.name);
        assert_eq!(snapshot.is_some(), case.snapshot, "{}", case.name);
        assert_eq!(baseline, case.baseline, "{}", case.name);
        assert_eq!(seqs(&write), case.write, "{}", case.name);
        assert_eq!(seqs(&fallback), case.fallback, "{}", case.name);
    }
}

#[test]
fn the_app_resizes_after_an_attach_only_when_it_owns_the_size_it_asked_for() {
    let attached = |cols, rows| Attached {
        cols: Some(cols),
        rows: Some(rows),
        ..Attached::default()
    };
    let cases = [
        (Policy::SameAppRemount, attached(80, 24), true),
        (Policy::SameAppRemount, attached(100, 40), false),
        (Policy::SameAppRemount, Attached::default(), true),
        (Policy::RelaunchRestore, attached(80, 24), false),
        (Policy::Revive, attached(80, 24), false),
    ];
    for (policy, attached, expected) in cases {
        assert_eq!(
            resize_after((100, 40), &attached, policy),
            expected,
            "{policy:?} {:?}",
            (attached.cols, attached.rows)
        );
    }
}

#[test]
fn live_output_drops_what_the_stream_already_holds() {
    let mut stream = Stream::Live { last_seq: 5 };
    let outcomes: Vec<&str> = [4, 5, 6, 6, 8]
        .into_iter()
        .map(|seq| match stream.receive(chunk(seq)) {
            Output::Write(_) => "write",
            Output::Stale => "stale",
            Output::Queued => "queued",
        })
        .collect();
    assert_eq!(outcomes, ["stale", "stale", "write", "stale", "write"]);
}

#[test]
fn output_during_an_attach_waits_and_the_oldest_goes_past_the_cap() {
    let mut stream = Stream::Attaching(Default::default());
    for seq in 1..=(QUEUED_OUTPUT as u32 + 2) {
        assert!(matches!(stream.receive(chunk(seq)), Output::Queued));
    }
    let Stream::Attaching(queue) = stream else {
        unreachable!()
    };
    assert_eq!(queue.len(), QUEUED_OUTPUT);
    assert_eq!(queue.front().map(|c| c.seq), Some(3));
}
