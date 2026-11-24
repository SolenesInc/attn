package store

import (
	"path/filepath"
	"testing"
)

func TestUpgradeRetiresGardenReviewJobsAndTaskNotifications(t *testing.T) {
	const beforeRetirement = 1795483227414757 - 1
	path := filepath.Join(t.TempDir(), "garden-review-retire.db")
	old, err := newStoreAtVersion(path, beforeRetirement)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO jobs(id, kind, state, scheduled_at, created_at, updated_at) VALUES
			('gr-queued', 'garden_review_classify', 'queued', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z'),
			('gr-dead', 'garden_review_classify', 'dead', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z'),
			('keep-title', 'session_title', 'dead', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`,
		`INSERT INTO tasks(id, kind, subject, state, next_attempt_at, created_at, updated_at) VALUES
			('gr-task', 'garden_review_classify', 'run-1', 'queued', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z'),
			('keep-task', 'session_title', 'sess-1', 'dead', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`,
		`INSERT INTO notifications(id, kind, source_kind, source_id, created_at) VALUES
			('task_failed:garden_review_classify:run-1', 'task_failed', 'task', 'gr-dead', '2026-11-01T00:00:00Z'),
			('task_failed:garden_review_classify:orphan', 'task_failed', 'task', 'missing-job', '2026-11-01T00:00:00Z'),
			('task_failed:custom:by-source', 'task_failed', 'task', 'gr-queued', '2026-11-01T00:00:00Z'),
			('task_failed:session_title:sess-1', 'task_failed', 'task', 'keep-title', '2026-11-01T00:00:00Z')`,
	} {
		if _, err := old.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	var jobCount, taskCount, notifCount int
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = 'garden_review_classify'`).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE kind = 'garden_review_classify'`).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE id != 'task_failed:session_title:sess-1'`).Scan(&notifCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 0 || taskCount != 0 || notifCount != 0 {
		t.Fatalf("retired rows survived upgrade: jobs=%d tasks=%d notifications=%d", jobCount, taskCount, notifCount)
	}

	var keptJobs, keptTasks, keptNotifs int
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id = 'keep-title'`).Scan(&keptJobs); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = 'keep-task'`).Scan(&keptTasks); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE id = 'task_failed:session_title:sess-1'`).Scan(&keptNotifs); err != nil {
		t.Fatal(err)
	}
	if keptJobs != 1 || keptTasks != 1 || keptNotifs != 1 {
		t.Fatalf("non-review rows were lost: jobs=%d tasks=%d notifications=%d", keptJobs, keptTasks, keptNotifs)
	}
}
