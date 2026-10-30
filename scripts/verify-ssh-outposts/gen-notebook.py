#!/usr/bin/env python3
"""Generate a Notebook-shaped folder for the first-sync bulk measurement (s-xn69r4 deferred item 4).

The shape mirrors the real Notebook's size histogram (measured from file metadata only, 2026-10-28:
8,619 files / 2.16 GB in 822 folders) scaled to the brief's 6,800 files / ~1.7 GB. Files under 64 KiB are
markdown-like text; larger ones are random bytes (receipts: archives, images, videos, logs). BEP sends
blocks uncompressed by default (compression "metadata"), so content type does not change bytes on the wire.
"""
import argparse, json, os, random
from pathlib import Path

# (upper bound bytes, files, total bytes) per bucket, scaled by 6800/8619 from the measured histogram.
BUCKETS = [(1 << 10, 1564, 623_927), (4 << 10, 1667, 4_026_342), (16 << 10, 1577, 12_370_000), (64 << 10, 862, 31_978_000),
           (256 << 10, 473, 60_447_000), (1 << 20, 404, 218_787_000), (4 << 20, 188, 407_893_000), (16 << 20, 38, 295_074_000),
           (64 << 20, 27, 674_672_000)]
WORDS = 'seed check receipt outpost bridge notebook journal plan decision finding verify home agent session ledger sync'.split()

p = argparse.ArgumentParser()
p.add_argument('--out', required=True, type=Path)
p.add_argument('--scale', type=float, default=1.0, help='multiply every bucket count (a smaller run when time is short)')
p.add_argument('--seed', type=int, default=20261028)
a = p.parse_args()
if a.out.exists(): raise SystemExit(f'{a.out} exists')
rng = random.Random(a.seed)
folders = [a.out / 'seeds' / f's-{rng.getrandbits(32):08x}' for _ in range(round(650 * a.scale) or 1)]
for f in folders: f.mkdir(parents=True)
(a.out / 'journal').mkdir()
text_block = ('\n'.join(' '.join(rng.choice(WORDS) for _ in range(12)) for _ in range(4096)) + '\n').encode()
files = total = 0
for upper, count, size_total in BUCKETS:
    n = max(1, round(count * a.scale))
    mean = size_total / count
    lower = upper // 4 if upper > 1024 else 16
    for i in range(n):
        size = int(min(upper - 1, max(lower, rng.uniform(0.5, 1.5) * mean)))
        folder = rng.choice(folders)
        if upper <= 64 << 10:
            path = folder / f'note-{files:05d}.md'
            data = (text_block * (size // len(text_block) + 1))[:size]
            path.write_bytes(data)
        else:
            path = folder / f'receipt-{files:05d}.{rng.choice(["tar.gz", "png", "mp4", "log"])}'
            with path.open('wb') as out:
                left = size
                while left:
                    chunk = os.urandom(min(left, 1 << 22)); out.write(chunk); left -= len(chunk)
        files += 1; total += size
(a.out / 'index.md').write_text('# Notebook (generated)\n'); (a.out / 'log.md').write_text('# Log\n')
print(json.dumps({'out': str(a.out), 'files': files + 2, 'bytes': total, 'folders': len(folders), 'scale': a.scale}))
