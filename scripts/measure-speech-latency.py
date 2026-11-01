"""Measure the attn-speech helper's stop-to-text time.

  python3 -I scripts/measure-speech-latency.py dictate BINARY CACHE CLIP [--reps N] [--seconds 5,10,20,30]
  python3 -I scripts/measure-speech-latency.py idle BINARY CACHE CLIP IDLE_SECONDS [--warm-first] [--seconds 30]

CACHE is a prepared speech-model cache (attn-speech serve --cache). CLIP is a 16 kHz mono
16-bit WAV at least as long as the longest requested recording; each recording is its first
N seconds.

Every recording is replayed in real time, as a recorder would stream it: listen, then audio
in --chunk-ms chunks of little-endian f32 samples, then finish at the stop. Stop-to-text is
the wall time from writing finish to reading the transcript: the helper's share of the
raw-visible budget. Each row carries the 1-minute load average read when the transcript
arrived, the number of segments the helper transcribed, and its finish_ms.

`dictate` prepares one helper and replays the lengths in round-robin for --reps rounds; its
last line is a per-length summary.
`idle` prepares a helper, dictates once, sleeps IDLE_SECONDS, then replays one recording per
length; --warm-first sends warm before the first, as a recording start does.
"""
import argparse
import json
import os
import queue
import statistics
import struct
import subprocess
import sys
import threading
import time
import wave


class Helper:
    def __init__(self, binary, cache, log):
        self.proc = subprocess.Popen(
            [binary, "serve", "--cache", cache],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=open(log, "ab") if log else subprocess.DEVNULL,
        )
        self.events = queue.Queue()
        self.next_id = 0
        threading.Thread(target=self._read, daemon=True).start()
        hello = self.events.get()
        if hello.get("protocol") != 2:
            sys.exit(f"expected a protocol 2 hello, got {hello}")

    def _read(self):
        for line in self.proc.stdout:
            event = json.loads(line)
            event["_at"] = time.monotonic()
            self.events.put(event)
        self.events.put({"event": "eof"})

    def post(self, request, body=b""):
        self.proc.stdin.write((json.dumps(request) + "\n").encode() + body)
        self.proc.stdin.flush()

    def send(self, op):
        self.next_id += 1
        self.post({"op": op, "id": self.next_id})
        return self.next_id

    def answer(self, request_id):
        while True:
            event = self.events.get()
            if event["event"] == "eof":
                sys.exit(f"helper exited with {self.proc.wait()}")
            if event["event"] == "error":
                sys.exit(f"request {event.get('id')} failed: {event['message']}")
            if event.get("id") == request_id and event["event"] != "progress":
                return event

    def request(self, op):
        started = time.monotonic()
        event = self.answer(self.send(op))
        return event, (event.pop("_at") - started) * 1000

    def dictate(self, pcm, chunk_bytes, real_time):
        recording = self.send("listen")
        began = time.monotonic()
        for start in range(0, len(pcm), chunk_bytes):
            if real_time:
                time.sleep(max(0, began + start / 4 / 16000 - time.monotonic()))
            chunk = pcm[start:start + chunk_bytes]
            self.post({"op": "audio", "target": recording, "bytes": len(chunk)}, chunk)
        if real_time:
            time.sleep(max(0, began + len(pcm) / 4 / 16000 - time.monotonic()))
        stopped = time.monotonic()
        self.post({"op": "finish", "target": recording})
        event = self.answer(recording)
        return event, (event.pop("_at") - stopped) * 1000

    def close(self):
        self.proc.stdin.close()
        self.proc.wait()


class Clip:
    def __init__(self, path):
        with wave.open(path) as clip:
            if (clip.getframerate(), clip.getnchannels(), clip.getsampwidth()) != (16000, 1, 2):
                sys.exit(f"{path} is not 16 kHz mono 16-bit")
            frames = clip.readframes(clip.getnframes())
        self.samples = struct.unpack(f"<{len(frames) // 2}h", frames)
        self.seconds = len(self.samples) / 16000

    def first(self, seconds):
        if seconds > self.seconds:
            sys.exit(f"the clip is {self.seconds:.1f} s; {seconds} s were asked for")
        return b"".join(struct.pack("<f", sample / 32768) for sample in self.samples[:int(seconds * 16000)])


def row(seconds, event, stop_to_text_ms, **extra):
    return {
        "sec": seconds,
        "stop_to_text_ms": round(stop_to_text_ms, 1),
        "finish_ms": event["finish_ms"],
        "segments": event["segments"],
        "asr_ms": event["asr_ms"],
        "words": len(event["text"].split()),
        "load1": round(os.getloadavg()[0], 2),
        **extra,
    }


def emit(value):
    print(json.dumps(value), flush=True)


def dictate_runs(args, clip, chunk_bytes):
    helper = Helper(args.binary, args.cache, args.helper_log)
    emit({"ready": helper.request("prepare")[0]})
    recordings = {seconds: clip.first(seconds) for seconds in args.seconds}
    times = {seconds: [] for seconds in args.seconds}
    loads = []
    for rep in range(args.reps):
        for seconds in args.seconds:
            result = row(seconds, *helper.dictate(recordings[seconds], chunk_bytes, real_time=True), rep=rep)
            times[seconds].append(result["stop_to_text_ms"])
            loads.append(result["load1"])
            emit(result)
    helper.close()
    emit({"summary": {
        str(seconds): {
            "n": len(values),
            "median_ms": statistics.median(values),
            "min_ms": min(values),
            "max_ms": max(values),
        } for seconds, values in times.items()
    }, "load1_min": min(loads), "load1_max": max(loads)})


def idle_run(args, clip, chunk_bytes):
    helper = Helper(args.binary, args.cache, args.helper_log)
    emit({"ready": helper.request("prepare")[0]})
    recordings = {seconds: clip.first(seconds) for seconds in args.seconds}
    first = args.seconds[0]
    emit(row(first, *helper.dictate(recordings[first], chunk_bytes, real_time=False), phase="before_idle"))
    time.sleep(args.idle_seconds)
    extra = {"phase": "after_idle", "idle_s": args.idle_seconds}
    if args.warm_first:
        warmed, warm_wall_ms = helper.request("warm")
        extra["warm_ms"] = warmed["ms"]
        extra["warm_wall_ms"] = round(warm_wall_ms, 1)
    for seconds in args.seconds:
        emit(row(seconds, *helper.dictate(recordings[seconds], chunk_bytes, real_time=True), **extra))
        extra = {"phase": "after_idle_next"}
    helper.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("mode", choices=["dictate", "idle"])
    parser.add_argument("binary")
    parser.add_argument("cache")
    parser.add_argument("clip")
    parser.add_argument("idle_seconds", nargs="?", type=float)
    parser.add_argument("--reps", type=int, default=5)
    parser.add_argument("--seconds", default=None)
    parser.add_argument("--chunk-ms", type=int, default=100)
    parser.add_argument("--warm-first", action="store_true")
    parser.add_argument("--helper-log", help="append the helper's stderr to this file")
    args = parser.parse_args()
    default = "5,10,20,30" if args.mode == "dictate" else "30"
    args.seconds = [float(value) for value in (args.seconds or default).split(",")]
    args.seconds = [int(value) if value.is_integer() else value for value in args.seconds]
    clip = Clip(args.clip)
    chunk_bytes = args.chunk_ms * 16 * 4
    if args.mode == "dictate":
        dictate_runs(args, clip, chunk_bytes)
    else:
        if args.idle_seconds is None:
            parser.error("idle needs IDLE_SECONDS")
        idle_run(args, clip, chunk_bytes)


if __name__ == "__main__":
    main()
