source "$(dirname "$0")/../../worlds/common.sh"
init_repository
cat > reminders.py <<'PY'
import json
from dataclasses import asdict, dataclass

FORMAT_VERSION = 1


@dataclass
class Reminder:
    id: int
    text: str
    due_at: float


class Store:
    def __init__(self, path):
        self.path = path
        self.reminders = {}
        self.snoozed = set()

    def add(self, reminder):
        self.reminders[reminder.id] = reminder

    def snooze(self, reminder_id):
        self.snoozed.add(reminder_id)

    def due(self, now):
        return sorted(
            (r for r in self.reminders.values() if r.due_at <= now and r.id not in self.snoozed),
            key=lambda r: r.due_at,
        )

    def save(self):
        with open(self.path, "w") as f:
            json.dump({"version": FORMAT_VERSION, "reminders": [asdict(r) for r in self.reminders.values()],
                       "snoozed": sorted(self.snoozed)}, f)

    @classmethod
    def load(cls, path):
        with open(path) as f:
            data = json.load(f)
        if data["version"] != FORMAT_VERSION:
            raise ValueError(f"unknown format version {data['version']}")
        store = cls(path)
        for r in data["reminders"]:
            store.add(Reminder(**r))
        store.snoozed = set(data["snoozed"])
        return store
PY
cat > test_reminders.py <<'PY'
import os
import tempfile
import unittest

from reminders import Reminder, Store


class StoreTest(unittest.TestCase):
    def test_snoozed_reminders_are_not_due(self):
        store = Store(None)
        store.add(Reminder(1, "water plants", 10))
        store.add(Reminder(2, "call mum", 20))
        store.snooze(1)
        self.assertEqual([r.id for r in store.due(30)], [2])

    def test_save_and_load_keep_reminders_and_snoozes(self):
        path = os.path.join(tempfile.mkdtemp(), "reminders.json")
        store = Store(path)
        store.add(Reminder(1, "water plants", 10))
        store.snooze(1)
        store.save()
        loaded = Store.load(path)
        self.assertEqual(loaded.reminders, store.reminders)
        self.assertEqual(loaded.snoozed, {1})


if __name__ == "__main__":
    unittest.main()
PY
cat > README.md <<'MD'
# reminders

A reminder store saved as JSON. Users' files from every release must keep loading.
MD
git add -A
git commit -qm "Store reminders and snoozes"

plant plot "Snooze for a while" <<'BODY'
## Goal

A snooze lasts a while instead of forever: a snoozed reminder is due again once its snooze ends.

## Decisions

- D1. `snooze(reminder_id, until)` takes the time the snooze ends.
- D2. The `Reminder` record owns the end of its snooze as `Reminder.snoozed_until`, so a reminder carries its own state and `Store.snoozed` goes away. Spike: none; `Reminder` was read as already holding `snoozed_until` from an earlier branch.

## Design

`Store.snooze(reminder_id, until)` sets `Reminder.snoozed_until`. `Store.due(now)` returns reminders with `due_at <= now` and no `snoozed_until`, or one that is `<= now`. `save` and `load` keep their format: `Reminder.snoozed_until` is already part of every saved reminder.

## Completion

The child harvested with its checks confirmed.
BODY
plant task "Snoozes end at a given time" --part-of "$plot" \
  --check "C1=python3 -m unittest passes, with a test that a reminder snoozed until t is not due before t and due at t." \
  --check "C2=A file saved by the current release loads, and its snoozes stay in force." <<'BODY'
Build the plot's Design: read it with `attn seed show <plot> --section Design` and D1–D2 with `--section Decisions`. Change `reminders.py` and its tests.

Your coordinator is the Orchestrator who tends the plot. Reach it with a note on this seed with `--ring`.

Out of scope: a command line, and recurring reminders.
BODY
attn delegate --seed "$plot" --role orchestrator --cwd "$PWD" --reuse-checkout --branch main >/dev/null
