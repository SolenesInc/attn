"""Verify a real attn-speech helper: process checks, and idle cost with post-idle latency.

  python3 -I scripts/verify-speech-helper.py check BINARY WORKDIR CLIPS_DIR [PREPARED_CACHE]
  python3 -I scripts/verify-speech-helper.py idle BINARY CACHE CLIP IDLE_SECONDS INTERVAL_SECONDS [--warm-first]

`check` prints one JSON line per check and exits nonzero when any fails. It downloads the
pinned model into empty caches under WORKDIR; PREPARED_CACHE, when given, is cloned for the
checks that start from a published model. CLIPS_DIR holds <voice>/<name>.wav with a
<name>.wav.txt reference transcript beside each clip.

`idle` prepares a helper, transcribes CLIP, then samples proc_pid_rusage counters every
INTERVAL_SECONDS for IDLE_SECONDS and times one request after the idle period;
--warm-first sends warm, as a recording start would, before that request.
"""
import ctypes
import hashlib
import io
import json
import os
import queue
import re
import shutil
import signal
import subprocess
import sys
import threading
import time
import wave

class Helper:
    def __init__(self, binary, cache):
        self.started = time.monotonic()
        self.proc = subprocess.Popen(
            [binary, "serve", "--cache", cache],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.events = queue.Queue()
        self.next_id = 0
        threading.Thread(target=self._read, daemon=True).start()
        self.hello = self.wait(lambda e: e.get("event") == "hello")

    def _read(self):
        for line in self.proc.stdout:
            event = json.loads(line)
            event["_t"] = time.monotonic()
            self.events.put(event)
        self.events.put({"event": "eof", "_t": time.monotonic()})

    def send(self, op, body=None, **fields):
        self.next_id += 1
        request = {"op": op, "id": self.next_id, **fields}
        if body is not None:
            request["bytes"] = len(body)
        self.proc.stdin.write((json.dumps(request) + "\n").encode())
        if body is not None:
            self.proc.stdin.write(body)
        self.proc.stdin.flush()
        return self.next_id

    def cancel(self, target):
        self.proc.stdin.write((json.dumps({"op": "cancel", "target": target}) + "\n").encode())
        self.proc.stdin.flush()

    def wait(self, predicate, timeout=600, on_event=None):
        deadline = time.monotonic() + timeout
        while True:
            event = self.events.get(timeout=max(0.0, deadline - time.monotonic()))
            if on_event:
                on_event(event)
            if predicate(event) or event["event"] == "eof":
                return event

    def finish(self, request_id, timeout=600, on_event=None):
        return self.wait(
            lambda e: e.get("id") == request_id and e["event"] in ("ready", "transcript", "warmed", "error"),
            timeout, on_event)

    def close_lease(self):
        closed = time.monotonic()
        self.proc.stdin.close()
        self.proc.wait(timeout=30)
        return time.monotonic() - closed


def wait_started(helper, request_id):
    return helper.wait(lambda e: e.get("id") == request_id and e["event"] == "started")


def children_of(pid):
    out = subprocess.run(["pgrep", "-P", str(pid)], capture_output=True, text=True).stdout
    return [int(p) for p in out.split()]


_lib = ctypes.CDLL("/usr/lib/libproc.dylib")


class RusageInfoV2(ctypes.Structure):
    _fields_ = [
        ("ri_uuid", ctypes.c_uint8 * 16),
        ("ri_user_time", ctypes.c_uint64),
        ("ri_system_time", ctypes.c_uint64),
        ("ri_pkg_idle_wkups", ctypes.c_uint64),
        ("ri_interrupt_wkups", ctypes.c_uint64),
        ("ri_pageins", ctypes.c_uint64),
        ("ri_wired_size", ctypes.c_uint64),
        ("ri_resident_size", ctypes.c_uint64),
        ("ri_phys_footprint", ctypes.c_uint64),
        ("ri_proc_start_abstime", ctypes.c_uint64),
        ("ri_proc_exit_abstime", ctypes.c_uint64),
        ("ri_child_user_time", ctypes.c_uint64),
        ("ri_child_system_time", ctypes.c_uint64),
        ("ri_child_pkg_idle_wkups", ctypes.c_uint64),
        ("ri_child_interrupt_wkups", ctypes.c_uint64),
        ("ri_child_pageins", ctypes.c_uint64),
        ("ri_child_elapsed_abstime", ctypes.c_uint64),
        ("ri_diskio_bytesread", ctypes.c_uint64),
        ("ri_diskio_byteswritten", ctypes.c_uint64),
    ]


class Timebase(ctypes.Structure):
    _fields_ = [("numer", ctypes.c_uint32), ("denom", ctypes.c_uint32)]


_tb = Timebase()
ctypes.CDLL("/usr/lib/libSystem.B.dylib").mach_timebase_info(ctypes.byref(_tb))


def sample(pid):
    info = RusageInfoV2()
    if _lib.proc_pid_rusage(pid, 2, ctypes.byref(info)) != 0:
        raise OSError(f"proc_pid_rusage({pid}) failed")
    ns = lambda ticks: ticks * _tb.numer / _tb.denom
    return {
        "cpu_s": (ns(info.ri_user_time) + ns(info.ri_system_time)) / 1e9,
        "idle_wakeups": info.ri_pkg_idle_wkups,
        "interrupt_wakeups": info.ri_interrupt_wkups,
        "footprint_mb": info.ri_phys_footprint / 2**20,
        "resident_mb": info.ri_resident_size / 2**20,
        "pageins": info.ri_pageins,
    }


def check(BINARY, WORK, CLIPS, PREPARED):
    MODEL_SHA = "4b50b6dd862bf6e346929aaf4f5eaacec003bfa3f56462d6c874b41ef2f38795"
    VAD_SHA = "0d871a800c420fdd5d1b17a3aea4296d1c39608f0ff4a9295eb76457eb1c88e6"
    HEX_DATA = os.path.expanduser("~/Library/Application Support/hex")
    results = []


    def report(name, passed, **observed):
        line = {"check": name, "pass": bool(passed), **observed}
        results.append(line)
        print(json.dumps(line), flush=True)


    def fresh_cache(name):
        path = os.path.join(WORK, name)
        shutil.rmtree(path, ignore_errors=True)
        return path


    def prepared_cache(name):
        path = fresh_cache(name)
        subprocess.run(["cp", "-cR", PREPARED, path], check=True)
        return path


    def strip(event):
        return {k: v for k, v in event.items() if k != "_t"}


    def wav_bytes(samples, rate=16000):
        buffer = io.BytesIO()
        with wave.open(buffer, "wb") as out:
            out.setnchannels(1)
            out.setsampwidth(2)
            out.setframerate(rate)
            out.writeframes(b"".join(int(s).to_bytes(2, "little", signed=True) for s in samples))
        return buffer.getvalue()


    NUMBER_WORDS = {"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7",
                    "eight": "8", "nine": "9", "ten": "10", "thirty": "30", "sixty": "60"}


    def words(text):
        return [NUMBER_WORDS.get(word, word) for word in re.findall(r"[a-z0-9']+", text.lower())]


    def wer(reference, hypothesis):
        r, h = words(reference), words(hypothesis)
        row = list(range(len(h) + 1))
        for i, rw in enumerate(r, 1):
            previous, row[0] = row[0], i
            for j, hw in enumerate(h, 1):
                previous, row[j] = row[j], min(row[j] + 1, row[j - 1] + 1, previous + (rw != hw))
        return row[len(h)] / max(1, len(r))


    def hex_snapshot():
        entries = {}
        for root, _, files in os.walk(HEX_DATA):
            for name in files:
                path = os.path.join(root, name)
                try:
                    stat = os.stat(path)
                except FileNotFoundError:
                    continue
                entries[os.path.relpath(path, HEX_DATA)] = (stat.st_size, stat.st_mtime_ns)
        pids = subprocess.run(["pgrep", "-x", "Hex"], capture_output=True, text=True).stdout.split()
        return entries, pids


    def wait_for_bytes(helper, request_id, at_least):
        return helper.wait(lambda e: e.get("id") == request_id and (
            (e["event"] == "progress" and e.get("stage") == "downloading" and e.get("bytes", 0) >= at_least)
            or e["event"] in ("ready", "error")))


    def linked_frameworks():
        out = subprocess.run(["otool", "-L", BINARY], capture_output=True, text=True).stdout
        return [line.strip().split(" ")[0] for line in out.splitlines()[1:]]


    hex_before = hex_snapshot()

    frameworks = linked_frameworks()
    audio_capture = [f for f in frameworks if re.search(r"CoreAudio|AVFoundation|AudioToolbox|AVFAudio|CoreMedia", f)]
    report("helper links no audio capture framework", not audio_capture, frameworks=frameworks)

    cache = fresh_cache("fresh")
    h = Helper(BINARY, cache)
    stages = []
    started = time.monotonic()
    ready = h.finish(h.send("prepare"), on_event=lambda e: e["event"] == "progress" and (stages[-1:] != [(e.get("artifact"), e["stage"])]) and stages.append((e.get("artifact"), e["stage"])))
    published = [os.path.exists(os.path.join(cache, sha, name)) for sha, name in ((MODEL_SHA, "parakeet-unified-en-0.6b-Q8_0.gguf"), (VAD_SHA, "silero-vad-v6.2-F32.gguf"))]
    leftover_partials = os.listdir(os.path.join(cache, "partial"))
    report("fresh prepare downloads, verifies, publishes and loads every declared artifact",
           ready["event"] == "ready" and all(published) and not [p for p in leftover_partials if p.endswith(".partial")],
           seconds=round(ready["_t"] - started, 2), ready=strip(ready), stages=stages, published=published, partial_dir=leftover_partials)
    again = h.finish(h.send("prepare"))
    report("repeat prepare on a ready helper answers ready without work", again["event"] == "ready" and again["load_ms"] == 0, ready=strip(again))
    exit_s = h.close_lease()
    report("closing the lease exits the idle helper", h.proc.returncode == 0 and exit_s < 1, exit_seconds=round(exit_s, 3), code=h.proc.returncode)
    if PREPARED is None:
        PREPARED = cache

    cache = fresh_cache("interrupted")
    h = Helper(BINARY, cache)
    request = h.send("prepare")
    progress = wait_for_bytes(h, request, 150_000_000)
    h.proc.send_signal(signal.SIGKILL)
    h.proc.wait()
    orphans = children_of(h.proc.pid)
    partial_bytes = os.path.getsize(os.path.join(cache, "partial", MODEL_SHA + ".partial"))
    h = Helper(BINARY, cache)
    request = h.send("prepare")
    first = h.wait(lambda e: e.get("id") == request and e["event"] in ("progress", "ready", "error"))
    ready = h.finish(request)
    report("a helper killed mid-download resumes from its partial file on the next prepare",
           ready["event"] == "ready" and first.get("bytes", 0) >= partial_bytes and not orphans,
           killed_at_bytes=progress.get("bytes"), partial_bytes=partial_bytes, first_progress_after_restart=strip(first),
           orphans_after_kill=orphans, ready=strip(ready))
    h.close_lease()

    cache = fresh_cache("canceled")
    h = Helper(BINARY, cache)
    request = h.send("prepare")
    wait_for_bytes(h, request, 50_000_000)
    canceled_at = time.monotonic()
    h.cancel(request)
    settled = h.finish(request)
    report("cancel during download settles the prepare with an error and keeps the partial bytes",
           settled["event"] == "error" and "canceled" in settled["message"] and os.path.getsize(os.path.join(cache, "partial", MODEL_SHA + ".partial")) > 0,
           settle_ms=round((settled["_t"] - canceled_at) * 1000, 1), settled=strip(settled))
    request = h.send("prepare")
    wait_for_bytes(h, request, 120_000_000)
    closed = time.monotonic()
    h.proc.stdin.close()
    h.proc.wait(timeout=30)
    report("closing the lease during a download stops it and exits", h.proc.returncode == 0,
           exit_ms=round((time.monotonic() - closed) * 1000, 1), code=h.proc.returncode, children=children_of(h.proc.pid))

    cache = fresh_cache("corrupt")
    os.makedirs(os.path.join(cache, "partial"))
    with open(os.path.join(cache, "partial", VAD_SHA + ".partial"), "wb") as out:
        out.write(b"\0" * 1_241_056)
    os.makedirs(os.path.join(cache, MODEL_SHA))
    subprocess.run(["cp", "-c", os.path.join(PREPARED, MODEL_SHA, "parakeet-unified-en-0.6b-Q8_0.gguf"), os.path.join(cache, MODEL_SHA)], check=True)
    h = Helper(BINARY, cache)
    failed = h.finish(h.send("prepare"))
    retried = h.finish(h.send("prepare"))
    report("a corrupt download fails verification by name, is discarded, and a retry recovers",
           failed["event"] == "error" and "failed verification" in failed["message"] and retried["event"] == "ready",
           failed=strip(failed), retried=strip(retried))
    h.close_lease()

    cache = prepared_cache("prepared")
    h = Helper(BINARY, cache)
    unprepared = h.finish(h.send("transcribe", wav_bytes([0] * 16000)))
    report("transcribe before prepare is refused clearly", unprepared["event"] == "error" and "not prepared" in unprepared["message"], answer=strip(unprepared))
    ready = h.finish(h.send("prepare"))
    report("prepare from a published cache loads and warms", ready["event"] == "ready", ready=strip(ready))
    fixtures = []
    for voice in sorted(os.listdir(CLIPS)):
        for name in sorted(os.listdir(os.path.join(CLIPS, voice))):
            if not name.endswith(".wav"):
                continue
            path = os.path.join(CLIPS, voice, name)
            reference = open(path + ".txt").read()
            sent = time.monotonic()
            answer = h.finish(h.send("transcribe", open(path, "rb").read()))
            fixtures.append({"clip": f"{voice}/{name}", "audio_ms": answer.get("audio_ms"), "wall_ms": round((answer["_t"] - sent) * 1000, 1),
                             "vad_ms": answer.get("vad_ms"), "asr_ms": answer.get("asr_ms"), "wer": round(wer(reference, answer.get("text", "")), 3),
                             "text": answer.get("text"), "event": answer["event"]})
    report("every batch fixture transcribes", all(f["event"] == "transcript" and f["wer"] < 0.25 for f in fixtures), fixtures=fixtures)
    silence = h.finish(h.send("transcribe", wav_bytes([0] * 48000)))
    hum = h.finish(h.send("transcribe", wav_bytes([int(300 * ((i * 7919) % 101 - 50) / 50) for i in range(48000)])))
    report("audio without speech returns no text", silence.get("speech") is False and silence.get("text") == "" and hum.get("speech") is False,
           silence=strip(silence), noise=strip(hum))
    garbage = h.finish(h.send("transcribe", b"not a wav"))
    report("a malformed recording is refused by name and the helper keeps serving", garbage["event"] == "error" and "WAV" in garbage["message"]
           and h.finish(h.send("warm"))["event"] == "warmed", answer=strip(garbage))

    xlong = open(os.path.join(CLIPS, "Daniel", "xlong.wav"), "rb").read()
    request = h.send("transcribe", xlong)
    wait_started(h, request)
    canceled_at = time.monotonic()
    h.cancel(request)
    settled = h.finish(request)
    after = h.finish(h.send("transcribe", open(os.path.join(CLIPS, "Daniel", "short.wav"), "rb").read()))
    report("cancel stops an in-flight transcription and the helper keeps serving",
           settled["event"] == "error" and settled["message"] == "transcription canceled" and after["event"] == "transcript",
           settle_ms=round((settled["_t"] - canceled_at) * 1000, 1), settled=strip(settled), next=strip(after))

    request = h.send("transcribe", xlong)
    wait_started(h, request)
    closed = time.monotonic()
    h.proc.stdin.close()
    h.proc.wait(timeout=30)
    report("closing the lease mid-transcription stops the inference and exits", h.proc.returncode == 0,
           exit_ms=round((time.monotonic() - closed) * 1000, 1), code=h.proc.returncode)

    h = Helper(BINARY, cache)
    h.finish(h.send("prepare"))
    wait_started(h, h.send("transcribe", xlong))
    h.proc.send_signal(signal.SIGKILL)
    eof = h.wait(lambda e: e["event"] == "eof", timeout=10)
    h = Helper(BINARY, cache)
    ready = h.finish(h.send("prepare"))
    answer = h.finish(h.send("transcribe", open(os.path.join(CLIPS, "Samantha", "short.wav"), "rb").read()))
    report("a helper crash mid-transcription is seen as EOF and a new helper serves from the intact cache",
           eof["event"] == "eof" and ready["event"] == "ready" and ready["load_ms"] > 0 and answer["event"] == "transcript",
           ready=strip(ready), answer=strip(answer))
    h.close_lease()

    hex_after = hex_snapshot()
    changed = sorted(k for k in set(hex_before[0]) | set(hex_after[0]) if hex_before[0].get(k) != hex_after[0].get(k))
    report("installed Hex data and processes are untouched", not changed and hex_before[1] == hex_after[1],
           hex_files=len(hex_after[0]), changed=changed, hex_pids_before=hex_before[1], hex_pids_after=hex_after[1])

    print(json.dumps({"summary": f"{sum(r['pass'] for r in results)}/{len(results)} passed"}), flush=True)
    sys.exit(0 if all(r["pass"] for r in results) else 1)


def idle(binary, cache, clip, idle_s, interval, warm_first):
    h = Helper(binary, cache)
    pid = h.proc.pid
    ready = h.finish(h.send("prepare"))
    body = open(clip, "rb").read()


    def timed_request():
        started = time.monotonic()
        r = h.finish(h.send("transcribe", body))
        return {"wall_ms": round((r["_t"] - started) * 1000, 1), "asr_ms": r.get("asr_ms"), "vad_ms": r.get("vad_ms")}


    print(json.dumps({"ready": {k: v for k, v in ready.items() if k != "_t"}, "after_prepare": sample(pid)}), flush=True)
    print(json.dumps({"warm_requests": [timed_request() for _ in range(3)]}), flush=True)
    start = time.monotonic()
    previous = sample(pid)
    print(json.dumps({"t": 0, **previous}), flush=True)
    while time.monotonic() - start < idle_s:
        time.sleep(interval)
        current = sample(pid)
        elapsed = interval
        print(json.dumps({
            "t": round(time.monotonic() - start),
            "cpu_ms_per_min": round((current["cpu_s"] - previous["cpu_s"]) * 1000 * 60 / elapsed, 2),
            "idle_wakeups_per_s": round((current["idle_wakeups"] - previous["idle_wakeups"]) / elapsed, 3),
            "interrupt_wakeups_per_s": round((current["interrupt_wakeups"] - previous["interrupt_wakeups"]) / elapsed, 3),
            "footprint_mb": round(current["footprint_mb"], 1),
            "resident_mb": round(current["resident_mb"], 1),
            "pageins": current["pageins"],
        }), flush=True)
        previous = current
    if warm_first:
        started = time.monotonic()
        warmed = h.finish(h.send("warm"))
        print(json.dumps({"post_idle_warm_ms": round((warmed["_t"] - started) * 1000, 1)}), flush=True)
    print(json.dumps({"post_idle_request": timed_request(), "after": sample(pid)}), flush=True)
    print(json.dumps({"next_request": timed_request()}), flush=True)
    print(json.dumps({"lease_exit_s": round(h.close_lease(), 3), "rc": h.proc.returncode}), flush=True)


if __name__ == "__main__":
    command, args = sys.argv[1], sys.argv[2:]
    if command == "check":
        sys.exit(check(args[0], args[1], args[2], args[3] if len(args) > 3 else None))
    if command == "idle":
        idle(args[0], args[1], args[2], float(args[3]), float(args[4]), args[5:] == ["--warm-first"])
        sys.exit(0)
    sys.exit(__doc__)
