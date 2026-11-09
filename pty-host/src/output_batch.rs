use std::time::{Duration, Instant};

pub const FRAME_TICK: Duration = Duration::from_millis(16);
pub const OUTPUT_BATCH_MAX_BYTES: usize = 256 * 1024;

const PROGRAM_STATUS_OPENING: &[u8] = b"\x1b]7501;";

pub struct OutputBatch {
    bytes: Vec<u8>,
    deadline: Instant,
    carries_program_status: bool,
}

impl OutputBatch {
    pub fn start(first: &[u8], now: Instant, last_emit: Option<Instant>) -> Self {
        let deadline = last_emit.map_or(now, |last| (last + FRAME_TICK).max(now));
        let mut batch = Self {
            bytes: Vec::new(),
            deadline,
            carries_program_status: false,
        };
        batch.push(first);
        batch
    }

    pub fn push(&mut self, read: &[u8]) {
        let unsearched = self
            .bytes
            .len()
            .saturating_sub(PROGRAM_STATUS_OPENING.len() - 1);
        self.bytes.extend_from_slice(read);
        self.carries_program_status = self.carries_program_status
            || self.bytes[unsearched..]
                .windows(PROGRAM_STATUS_OPENING.len())
                .any(|window| window == PROGRAM_STATUS_OPENING);
    }

    pub fn wait(&self, now: Instant) -> Option<Duration> {
        if self.carries_program_status
            || self.bytes.len() >= OUTPUT_BATCH_MAX_BYTES
            || now >= self.deadline
        {
            return None;
        }
        Some(self.deadline - now)
    }

    pub fn bytes(&self) -> &[u8] {
        &self.bytes
    }
}

#[cfg(test)]
mod tests {
    use std::time::{Duration, Instant};

    use super::{FRAME_TICK, OUTPUT_BATCH_MAX_BYTES, OutputBatch, PROGRAM_STATUS_OPENING};

    const MS: Duration = Duration::from_millis(1);

    enum Step {
        Read(usize),
        Quiet(Duration),
    }

    struct Emitted {
        started: Duration,
        at: Duration,
        bytes: Vec<u8>,
    }

    struct Reader {
        origin: Instant,
        now: Instant,
        last_emit: Option<Instant>,
        open: Option<(Duration, OutputBatch)>,
        emitted: Vec<Emitted>,
    }

    impl Reader {
        fn emit(&mut self, at: Instant) {
            let (started, batch) = self.open.take().expect("a batch is open");
            self.emitted.push(Emitted {
                started,
                at: at.duration_since(self.origin),
                bytes: batch.bytes().to_vec(),
            });
            self.last_emit = Some(at);
        }

        fn read(&mut self, data: &[u8]) {
            if let Some((_, batch)) = self.open.as_mut() {
                batch.push(data);
            } else {
                let batch = OutputBatch::start(data, self.now, self.last_emit);
                self.open = Some((self.now.duration_since(self.origin), batch));
            }
            if self.wait().is_none() {
                self.emit(self.now);
            }
        }

        fn quiet(&mut self, duration: Duration) {
            if let Some(wait) = self.wait().filter(|wait| *wait <= duration) {
                self.emit(self.now + wait);
            }
            self.now += duration;
        }

        fn wait(&self) -> Option<Duration> {
            self.open
                .as_ref()
                .and_then(|(_, batch)| batch.wait(self.now))
        }
    }

    fn run(steps: &[Step]) -> (Vec<u8>, Vec<Emitted>) {
        let origin = Instant::now();
        let mut reader = Reader {
            origin,
            now: origin,
            last_emit: None,
            open: None,
            emitted: Vec::new(),
        };
        let mut read = Vec::new();
        for step in steps {
            match step {
                Step::Read(len) => {
                    let data = (read.len()..read.len() + len)
                        .map(|index| u8::try_from(index % 251).expect("below 251"))
                        .collect::<Vec<_>>();
                    read.extend_from_slice(&data);
                    reader.read(&data);
                }
                Step::Quiet(duration) => reader.quiet(*duration),
            }
        }
        reader.quiet(FRAME_TICK);
        (read, reader.emitted)
    }

    fn status_after_output(status: &[u8]) -> Vec<u8> {
        [b"output".as_slice(), status].concat()
    }

    #[test]
    fn a_read_that_opens_a_program_status_ends_the_frame_at_that_read() {
        let status = b"\x1b]7501;state=done\x1b\\";
        let split = PROGRAM_STATUS_OPENING.len() / 2;
        let cases: Vec<(&str, Vec<Vec<u8>>)> = vec![
            ("alone in its read", vec![status.to_vec()]),
            (
                "after earlier output",
                vec![b"a".to_vec(), status_after_output(status)],
            ),
            (
                "opening split across two reads",
                vec![status[..split].to_vec(), status[split..].to_vec()],
            ),
        ];
        for (name, reads) in cases {
            let now = Instant::now();
            let (last, earlier) = reads.split_last().expect("a read");
            let mut batch = OutputBatch::start(earlier.first().unwrap_or(last), now, Some(now));
            if !earlier.is_empty() {
                assert!(
                    batch.wait(now).is_some(),
                    "{name}: output alone waits for the frame"
                );
                batch.push(last);
            }
            assert_eq!(
                batch.wait(now),
                None,
                "{name}: the frame went on past the status"
            );
        }
    }

    #[test]
    fn output_without_a_program_status_waits_for_the_frame() {
        let now = Instant::now();
        let mut batch = OutputBatch::start(b"\x1b]7500;", now, Some(now));
        batch.push(b"plain");
        assert_eq!(batch.wait(now), Some(FRAME_TICK));
    }

    fn reads_every(interval: Duration, count: usize, len: usize) -> Vec<Step> {
        (0..count)
            .flat_map(|_| [Step::Read(len), Step::Quiet(interval)])
            .collect()
    }

    #[test]
    fn batches_keep_every_byte_and_go_out_at_most_once_per_tick() {
        let cases: Vec<(&str, Vec<Step>)> = vec![
            ("one lone read", vec![Step::Read(3)]),
            ("echoes a tick apart", reads_every(FRAME_TICK, 5, 1)),
            ("echoes just inside a tick", reads_every(15 * MS, 5, 1)),
            ("a flood of small reads", reads_every(MS / 10, 2000, 100)),
            ("a flood of full reads", reads_every(MS / 10, 400, 4096)),
            ("reads every 5 ms", reads_every(5 * MS, 50, 100)),
            (
                "bursts with quiet between",
                [
                    reads_every(MS, 40, 100),
                    vec![Step::Quiet(100 * MS)],
                    reads_every(MS, 40, 100),
                ]
                .into_iter()
                .flatten()
                .collect(),
            ),
        ];
        for (name, steps) in cases {
            let (read, emitted) = run(&steps);

            let joined = emitted
                .iter()
                .flat_map(|batch| batch.bytes.iter().copied())
                .collect::<Vec<_>>();
            assert_eq!(
                joined, read,
                "{name}: I1 batches join to the reads in order"
            );

            let mut previous: Option<&Emitted> = None;
            for batch in &emitted {
                let after_quiet_tick =
                    previous.is_none_or(|previous| batch.started >= previous.at + FRAME_TICK);
                if after_quiet_tick {
                    assert_eq!(
                        batch.at, batch.started,
                        "{name}: I2 a read after a quiet tick waited"
                    );
                }
                if let Some(previous) = previous {
                    assert!(
                        batch.bytes.len() >= OUTPUT_BATCH_MAX_BYTES
                            || batch.at >= previous.at + FRAME_TICK,
                        "{name}: I3 a batch at {:?} followed one at {:?}",
                        batch.at,
                        previous.at
                    );
                }
                assert!(
                    batch.bytes.len() < OUTPUT_BATCH_MAX_BYTES + 4096,
                    "{name}: a batch fits one output event"
                );
                previous = Some(batch);
            }
        }
    }
}
