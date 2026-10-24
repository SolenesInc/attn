#!/usr/bin/env python3
"""Write one payload shape per bus fact name, from a copy of an attn database.

Usage: python3 -I scripts/analytics-payload-shapes.py <copy-of-attn.db> > internal/analytics/testdata/payload-shapes.json

A shape keeps key names and value types only: strings become "string", numbers 0,
booleans false. Each fact name's shape merges every payload recorded under it, so
optional fields appear; names that only ever carried an empty payload map to null.
"""
import json
import sqlite3
import sys


def shape(value):
    if isinstance(value, dict):
        return {key: shape(field) for key, field in value.items()}
    if isinstance(value, list):
        merged = None
        for item in value:
            merged = merge(merged, shape(item))
        return [] if merged is None else [merged]
    if isinstance(value, str):
        return "string"
    if isinstance(value, bool):
        return False
    if value is None:
        return None
    return 0


def merge(known, seen):
    if known is None:
        return seen
    if isinstance(known, dict) and isinstance(seen, dict):
        return {key: merge(known.get(key), seen.get(key)) for key in known.keys() | seen.keys()}
    if isinstance(known, list) and isinstance(seen, list):
        return [merge(known[0] if known else None, seen[0] if seen else None)] if known or seen else []
    return known


def main(path):
    db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    shapes = {}
    for name, payload in db.execute("SELECT name, payload FROM bus_events"):
        shapes.setdefault(name, None)
        if not payload or payload.strip() in ("{}", "null"):
            continue
        shapes[name] = merge(shapes[name], shape(json.loads(payload)))
    json.dump(shapes, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main(sys.argv[1])
