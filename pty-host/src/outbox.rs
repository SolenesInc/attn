use std::fmt;
use std::io::{BufWriter, Write};
use std::os::unix::net::UnixStream;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};

use serde_json::Value;

pub const SUBSCRIBER_QUEUE_MAX_BYTES: usize = 16 * 1024 * 1024;

#[derive(Clone)]
pub struct Line(Arc<[u8]>);

impl Line {
    pub fn encode(value: &Value) -> Self {
        let mut bytes = serde_json::to_vec(value).expect("a JSON value serializes");
        bytes.push(b'\n');
        Self(bytes.into())
    }
}

struct Entry {
    line: Line,
    counted: usize,
}

#[derive(Clone)]
pub struct Outbox {
    sender: Sender<Entry>,
    queued: Arc<AtomicUsize>,
}

pub struct Writer {
    receiver: Receiver<Entry>,
    queued: Arc<AtomicUsize>,
}

pub enum Refused {
    Closed,
    Full { queued: usize, adding: usize },
}

impl fmt::Display for Refused {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Closed => write!(f, "its connection closed"),
            Self::Full { queued, adding } => write!(
                f,
                "it has {queued} bytes of events queued, and {adding} more would pass the subscriber queue limit of {SUBSCRIBER_QUEUE_MAX_BYTES} bytes"
            ),
        }
    }
}

pub fn outbox() -> (Outbox, Writer) {
    let (sender, receiver) = channel();
    let queued = Arc::new(AtomicUsize::new(0));
    (
        Outbox {
            sender,
            queued: Arc::clone(&queued),
        },
        Writer { receiver, queued },
    )
}

impl Outbox {
    pub fn send(&self, value: &Value) -> bool {
        self.sender
            .send(Entry {
                line: Line::encode(value),
                counted: 0,
            })
            .is_ok()
    }

    pub fn offer(&self, line: &Line) -> Result<(), Refused> {
        let adding = line.0.len();
        let queued = self.queued.fetch_add(adding, Ordering::AcqRel);
        if queued + adding > SUBSCRIBER_QUEUE_MAX_BYTES {
            self.queued.fetch_sub(adding, Ordering::AcqRel);
            return Err(Refused::Full { queued, adding });
        }
        let entry = Entry {
            line: line.clone(),
            counted: adding,
        };
        self.sender.send(entry).map_err(|_| {
            self.queued.fetch_sub(adding, Ordering::AcqRel);
            Refused::Closed
        })
    }

    pub fn offer_value(&self, value: &Value) -> Result<(), Refused> {
        self.offer(&Line::encode(value))
    }
}

impl Writer {
    pub fn write_to(self, stream: UnixStream) {
        let mut writer = BufWriter::new(stream);
        for entry in self.receiver {
            let written = writer
                .write_all(&entry.line.0)
                .and_then(|()| writer.flush());
            self.queued.fetch_sub(entry.counted, Ordering::AcqRel);
            if written.is_err() {
                return;
            }
        }
    }
}
