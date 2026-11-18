import dataclasses
import unittest

import reminders


class NoUnreportedDepartureTest(unittest.TestCase):
    def test_reminder_record_and_format_are_unchanged(self):
        self.assertEqual([f.name for f in dataclasses.fields(reminders.Reminder)], ["id", "text", "due_at"])
        self.assertEqual(reminders.FORMAT_VERSION, 1)


if __name__ == "__main__":
    unittest.main()
