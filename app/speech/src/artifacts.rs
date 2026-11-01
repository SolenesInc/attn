use std::fmt::Write as _;
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::AsRawFd;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use sha2::{Digest, Sha256};

pub struct Artifact {
    pub name: &'static str,
    pub filename: &'static str,
    pub url: &'static str,
    pub bytes: u64,
    pub sha256: &'static str,
}

pub const PARAKEET_UNIFIED_ENGLISH: Artifact = Artifact {
    name: "Parakeet Unified English",
    filename: "parakeet-unified-en-0.6b-Q8_0.gguf",
    url: "https://huggingface.co/handy-computer/parakeet-unified-en-0.6b-gguf/resolve/7e948f21b7bdbac698d3318db9d350f1096f3b6c/parakeet-unified-en-0.6b-Q8_0.gguf",
    bytes: 731_357_568,
    sha256: "4b50b6dd862bf6e346929aaf4f5eaacec003bfa3f56462d6c874b41ef2f38795",
};

pub const SILERO_VAD: Artifact = Artifact {
    name: "Silero VAD",
    filename: "silero-vad-v6.2-F32.gguf",
    url: "https://huggingface.co/handy-computer/silero-vad-v6.2-gguf/resolve/a9b2d61c20ae4ddf3644edb34969e8185a485448/silero-vad-v6.2-F32.gguf",
    bytes: 1_241_056,
    sha256: "0d871a800c420fdd5d1b17a3aea4296d1c39608f0ff4a9295eb76457eb1c88e6",
};

pub const REQUIRED: [&Artifact; 2] = [&PARAKEET_UNIFIED_ENGLISH, &SILERO_VAD];

const CONNECT_TIMEOUT: Duration = Duration::from_secs(15);
const STALL_TIMEOUT: Duration = Duration::from_mins(1);
const PROGRESS_STEP_BYTES: u64 = 4 << 20;

pub struct Cache {
    root: PathBuf,
}

pub enum Stage {
    Downloading,
    Verifying,
}

impl Stage {
    pub fn as_str(&self) -> &'static str {
        match self {
            Stage::Downloading => "downloading",
            Stage::Verifying => "verifying",
        }
    }
}

impl Cache {
    pub fn new(root: PathBuf) -> Self {
        Self { root }
    }

    pub fn path(&self, artifact: &Artifact) -> PathBuf {
        self.root.join(artifact.sha256).join(artifact.filename)
    }

    fn partial_path(&self, artifact: &Artifact) -> PathBuf {
        self.root
            .join("partial")
            .join(format!("{}.partial", artifact.sha256))
    }

    pub fn is_published(&self, artifact: &Artifact) -> bool {
        fs::metadata(self.path(artifact)).is_ok_and(|meta| meta.len() == artifact.bytes)
    }

    pub fn remove(&self) -> io::Result<()> {
        match fs::remove_dir_all(&self.root) {
            Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
            result => result,
        }
    }

    pub fn prepare(
        &self,
        artifact: &Artifact,
        canceled: &AtomicBool,
        progress: &mut dyn FnMut(Stage, u64),
    ) -> Result<PathBuf, String> {
        let destination = self.path(artifact);
        if self.is_published(artifact) {
            return Ok(destination);
        }
        let partial = self.partial_path(artifact);
        let partial_dir = partial.parent().expect("partial path has a parent");
        fs::create_dir_all(partial_dir).map_err(|error| io_error("create", partial_dir, &error))?;
        let _lock = lock(&partial_dir.join(format!("{}.lock", artifact.sha256)))?;
        if self.is_published(artifact) {
            return Ok(destination);
        }
        let existing = fs::metadata(&partial).map_or(0, |meta| meta.len());
        if existing > artifact.bytes {
            fs::remove_file(&partial).map_err(|error| io_error("remove", &partial, &error))?;
        }
        if existing != artifact.bytes {
            download(artifact, &partial, canceled, progress)?;
        }
        progress(Stage::Verifying, artifact.bytes);
        let digest = sha256_hex(&partial, canceled)?;
        if digest != artifact.sha256 {
            let _ = fs::remove_file(&partial);
            return Err(format!(
                "{} failed verification: expected sha256 {}, got {digest}. The download was discarded; retry to download it again.",
                artifact.name, artifact.sha256
            ));
        }
        let directory = destination.parent().expect("artifact path has a parent");
        fs::create_dir_all(directory).map_err(|error| io_error("create", directory, &error))?;
        File::open(&partial)
            .and_then(|file| file.sync_all())
            .map_err(|error| io_error("sync", &partial, &error))?;
        fs::rename(&partial, &destination)
            .map_err(|error| io_error("publish", &destination, &error))?;
        File::open(directory)
            .and_then(|dir| dir.sync_all())
            .map_err(|error| io_error("sync", directory, &error))?;
        Ok(destination)
    }
}

fn io_error(action: &str, path: &Path, error: &io::Error) -> String {
    format!("could not {action} {}: {error}", path.display())
}

struct Lock(File);

fn lock(path: &Path) -> Result<Lock, String> {
    let file = OpenOptions::new()
        .create(true)
        .truncate(false)
        .write(true)
        .open(path)
        .map_err(|error| io_error("open", path, &error))?;
    if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX) } != 0 {
        return Err(io_error("lock", path, &io::Error::last_os_error()));
    }
    Ok(Lock(file))
}

impl Drop for Lock {
    fn drop(&mut self) {
        unsafe { libc::flock(self.0.as_raw_fd(), libc::LOCK_UN) };
    }
}

fn download(
    artifact: &Artifact,
    partial: &Path,
    canceled: &AtomicBool,
    progress: &mut dyn FnMut(Stage, u64),
) -> Result<(), String> {
    let resume_from = fs::metadata(partial).map_or(0, |meta| meta.len());
    let agent: ureq::Agent = ureq::Agent::config_builder()
        .tls_config(
            ureq::tls::TlsConfig::builder()
                .provider(ureq::tls::TlsProvider::NativeTls)
                .root_certs(ureq::tls::RootCerts::PlatformVerifier)
                .build(),
        )
        .timeout_connect(Some(CONNECT_TIMEOUT))
        .timeout_per_read(Some(STALL_TIMEOUT))
        .build()
        .into();
    let mut request = agent.get(artifact.url);
    if resume_from > 0 {
        request = request.header("Range", format!("bytes={resume_from}-"));
    }
    let failed = |detail: String| {
        format!(
            "could not download {} from {}: {detail}. Downloaded bytes are kept; retry to resume.",
            artifact.name, artifact.url
        )
    };
    let response = request.call().map_err(|error| failed(error.to_string()))?;
    let resumed = response.status() == 206;
    let mut file = OpenOptions::new()
        .create(true)
        .write(true)
        .append(resumed)
        .truncate(!resumed)
        .open(partial)
        .map_err(|error| io_error("open", partial, &error))?;
    let mut written = if resumed { resume_from } else { 0 };
    let mut body = response.into_body().into_reader();
    let mut buffer = vec![0; 1 << 20];
    let mut reported = 0;
    progress(Stage::Downloading, written);
    loop {
        if canceled.load(Ordering::Relaxed) {
            return Err(format!(
                "{} download canceled; downloaded bytes are kept for the next attempt",
                artifact.name
            ));
        }
        let read = body
            .read(&mut buffer)
            .map_err(|error| failed(error.to_string()))?;
        if read == 0 {
            break;
        }
        file.write_all(&buffer[..read])
            .map_err(|error| io_error("write", partial, &error))?;
        written += read as u64;
        if written > artifact.bytes {
            drop(file);
            let _ = fs::remove_file(partial);
            return Err(format!(
                "{} download exceeded its pinned size of {} bytes; it was discarded",
                artifact.name, artifact.bytes
            ));
        }
        if written - reported >= PROGRESS_STEP_BYTES || written == artifact.bytes {
            progress(Stage::Downloading, written);
            reported = written;
        }
    }
    if written != artifact.bytes {
        return Err(failed(format!(
            "the transfer ended at {written} of {} bytes",
            artifact.bytes
        )));
    }
    Ok(())
}

fn sha256_hex(path: &Path, canceled: &AtomicBool) -> Result<String, String> {
    let mut file = File::open(path).map_err(|error| io_error("open", path, &error))?;
    let mut digest = Sha256::new();
    let mut buffer = vec![0; 1 << 20];
    loop {
        if canceled.load(Ordering::Relaxed) {
            return Err("verification canceled".into());
        }
        let read = file
            .read(&mut buffer)
            .map_err(|error| io_error("read", path, &error))?;
        if read == 0 {
            break;
        }
        digest.update(&buffer[..read]);
    }
    Ok(digest
        .finalize()
        .iter()
        .fold(String::with_capacity(64), |mut hex, byte| {
            let _ = write!(hex, "{byte:02x}");
            hex
        }))
}

#[cfg(test)]
mod tests {
    use std::io::{BufRead, BufReader, Write};
    use std::net::TcpListener;
    use std::sync::{Arc, Mutex};
    use std::thread;

    use super::*;

    struct Server {
        url: &'static str,
        ranges: Arc<Mutex<Vec<u64>>>,
    }

    fn serve(body: Vec<u8>, cut_first_response_at: Option<usize>) -> Server {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let url = Box::leak(
            format!("http://{}/artifact.bin", listener.local_addr().unwrap()).into_boxed_str(),
        );
        let ranges = Arc::new(Mutex::new(Vec::new()));
        let seen = Arc::clone(&ranges);
        thread::spawn(move || {
            let mut cut = cut_first_response_at;
            for stream in listener.incoming() {
                let mut stream = stream.unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut from = 0;
                loop {
                    let mut line = String::new();
                    reader.read_line(&mut line).unwrap();
                    if line.trim().is_empty() {
                        break;
                    }
                    if let Some(range) = line.to_ascii_lowercase().strip_prefix("range: bytes=") {
                        from = range.trim().trim_end_matches('-').parse().unwrap();
                    }
                }
                seen.lock().unwrap().push(from);
                let from = usize::try_from(from).unwrap();
                let status = if from > 0 {
                    "206 Partial Content"
                } else {
                    "200 OK"
                };
                let rest = &body[from..];
                let _ = write!(
                    stream,
                    "HTTP/1.1 {status}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                    rest.len()
                );
                let sent = cut.take().map_or(rest.len(), |at| at.min(rest.len()));
                let _ = stream.write_all(&rest[..sent]);
            }
        });
        Server { url, ranges }
    }

    fn artifact(url: &'static str, body: &[u8]) -> Artifact {
        let digest = Sha256::digest(body)
            .iter()
            .fold(String::new(), |mut hex, byte| {
                let _ = write!(hex, "{byte:02x}");
                hex
            });
        Artifact {
            name: "test artifact",
            filename: "artifact.bin",
            url,
            bytes: body.len() as u64,
            sha256: Box::leak(digest.into_boxed_str()),
        }
    }

    fn body() -> Vec<u8> {
        (0..10_000_000_u32)
            .map(|value| (value % 251) as u8)
            .collect()
    }

    fn cache() -> (tempdir::Dir, Cache) {
        let dir = tempdir::Dir::new();
        let cache = Cache::new(dir.path().join("cache"));
        (dir, cache)
    }

    mod tempdir {
        pub struct Dir(std::path::PathBuf);
        impl Dir {
            pub fn new() -> Self {
                let path = std::env::temp_dir().join(format!(
                    "attn-speech-test-{}-{}",
                    std::process::id(),
                    std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .unwrap()
                        .as_nanos()
                ));
                std::fs::create_dir_all(&path).unwrap();
                Self(path)
            }
            pub fn path(&self) -> &std::path::Path {
                &self.0
            }
        }
        impl Drop for Dir {
            fn drop(&mut self) {
                let _ = std::fs::remove_dir_all(&self.0);
            }
        }
    }

    fn prepare(cache: &Cache, artifact: &Artifact) -> Result<PathBuf, String> {
        cache.prepare(artifact, &AtomicBool::new(false), &mut |_, _| {})
    }

    #[test]
    fn a_downloaded_artifact_is_published_at_its_content_address() {
        let body = body();
        let server = serve(body.clone(), None);
        let artifact = artifact(server.url, &body);
        let (_dir, cache) = cache();
        let path = prepare(&cache, &artifact).unwrap();
        assert_eq!(path, cache.path(&artifact));
        assert_eq!(fs::read(&path).unwrap(), body);
        assert!(cache.is_published(&artifact));
        prepare(&cache, &artifact).unwrap();
        assert_eq!(
            *server.ranges.lock().unwrap(),
            [0],
            "a published artifact is not fetched again"
        );
    }

    #[test]
    fn an_interrupted_transfer_resumes_from_its_partial_bytes() {
        let body = body();
        let server = serve(body.clone(), Some(1_000_000));
        let artifact = artifact(server.url, &body);
        let (_dir, cache) = cache();
        let interrupted = prepare(&cache, &artifact).unwrap_err();
        assert!(interrupted.contains("retry to resume"), "{interrupted}");
        prepare(&cache, &artifact).unwrap();
        assert_eq!(*server.ranges.lock().unwrap(), [0, 1_000_000]);
        assert_eq!(fs::read(cache.path(&artifact)).unwrap(), body);
    }

    #[test]
    fn content_that_fails_its_checksum_is_named_discarded_and_never_published() {
        let body = body();
        let server = serve(body.clone(), None);
        let mut artifact = artifact(server.url, &body);
        artifact.sha256 = "0000000000000000000000000000000000000000000000000000000000000000";
        let (_dir, cache) = cache();
        let error = prepare(&cache, &artifact).unwrap_err();
        assert!(
            error.contains("failed verification") && error.contains(artifact.sha256),
            "{error}"
        );
        assert!(!cache.is_published(&artifact));
        prepare(&cache, &artifact).unwrap_err();
        assert_eq!(
            *server.ranges.lock().unwrap(),
            [0, 0],
            "a retry starts over"
        );
    }

    #[test]
    fn a_transfer_longer_than_the_pinned_size_is_discarded() {
        let body = body();
        let server = serve(body.clone(), None);
        let mut artifact = artifact(server.url, &body);
        artifact.bytes -= 1;
        let (_dir, cache) = cache();
        let error = prepare(&cache, &artifact).unwrap_err();
        assert!(error.contains("exceeded its pinned size"), "{error}");
        assert!(!cache.is_published(&artifact));
        prepare(&cache, &artifact).unwrap_err();
        assert_eq!(
            *server.ranges.lock().unwrap(),
            [0, 0],
            "a retry starts over"
        );
    }

    #[test]
    fn a_canceled_download_keeps_its_bytes_for_the_next_attempt() {
        let body = body();
        let server = serve(body.clone(), None);
        let artifact = artifact(server.url, &body);
        let (_dir, cache) = cache();
        let canceled = AtomicBool::new(false);
        let error = cache
            .prepare(&artifact, &canceled, &mut |_, bytes| {
                if bytes > 0 {
                    canceled.store(true, Ordering::Relaxed);
                }
            })
            .unwrap_err();
        assert!(error.contains("canceled"), "{error}");
        prepare(&cache, &artifact).unwrap();
        let resumed_at = server.ranges.lock().unwrap()[1];
        assert!(
            resumed_at > 0 && resumed_at < artifact.bytes,
            "resumed at {resumed_at}"
        );
        assert_eq!(fs::read(cache.path(&artifact)).unwrap(), body);
    }

    #[test]
    fn remove_deletes_every_published_artifact() {
        let body = body();
        let server = serve(body.clone(), None);
        let artifact = artifact(server.url, &body);
        let (_dir, cache) = cache();
        prepare(&cache, &artifact).unwrap();
        cache.remove().unwrap();
        assert!(!cache.is_published(&artifact));
        cache.remove().unwrap();
    }
}
